package subscription_basic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"meshvianex/core/plugin"
	"meshvianex/core/resources"
)

const maxSubscriptionBytes int64 = 4 << 20

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.subscription.http", Name: "HTTP Subscription Source", Version: "0.1.0", PluginTypes: []string{"subscription"}, RequiresCapabilities: []string{"socket.tcp", "config.read"}, Provides: []string{"subscription.http", "subscription.raw"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Subscription Source"}}
}

func (Factory) Create(context.Context, plugin.InstanceContext, json.RawMessage) (plugin.Instance, error) {
	return Instance{}, nil
}

type Instance struct{}

func (Instance) Start(context.Context) error { return nil }
func (Instance) Stop(context.Context) error  { return nil }

func (Instance) Fetch(ctx context.Context, sub resources.Subscription) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sourceType := strings.ToLower(strings.TrimSpace(sub.SourceType))
	if sourceType == "raw" || sourceType == "local" || sourceType == "content" || sourceType == "" && !isHTTPSource(sub.Source) {
		if sub.Source == "" {
			return "", fmt.Errorf("subscription source is empty")
		}
		if int64(len(sub.Source)) > maxSubscriptionBytes {
			return "", fmt.Errorf("subscription content exceeds %d bytes", maxSubscriptionBytes)
		}
		return sub.Source, nil
	}
	u, err := url.Parse(sub.Source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("subscription source must be a valid http(s) URL")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	redirects := 0
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		redirects++
		if redirects > 3 {
			return fmt.Errorf("subscription redirect limit exceeded")
		}
		if req.URL == nil || req.URL.Host == "" || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return fmt.Errorf("subscription redirect target must be an http(s) URL")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("subscription fetch failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxSubscriptionBytes {
		return "", fmt.Errorf("subscription response exceeds %d bytes", maxSubscriptionBytes)
	}
	return string(data), nil
}

func isHTTPSource(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
