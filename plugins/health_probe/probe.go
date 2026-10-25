package health_probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"time"
)

type Config struct {
	Endpoint       string `json:"endpoint"`
	TLS            bool   `json:"tls,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.diagnostic.connectivity", Name: "Connectivity Health Probe", Version: "0.1.0", PluginTypes: []string{"diagnostic"}, RequiresCapabilities: []string{"socket.tcp", "dns.resolve"}, Provides: []string{"diagnostic.connectivity"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Connectivity Probe", Fields: []plugin.ConfigField{{ID: "endpoint", Type: "host_port", Label: "Endpoint", Required: true}, {ID: "tls", Type: "boolean", Label: "Use TLS", Default: false}, {ID: "timeout_seconds", Type: "number", Label: "Timeout (seconds)", Default: 5}}}}
}

func (Factory) Create(_ context.Context, ictx plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("connectivity probe endpoint is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("connectivity probe endpoint must be host:port")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 5
	}
	var resolver netx.Resolver
	if service, ok := ictx.Services["realm.resolver"]; ok {
		resolver, _ = service.(netx.Resolver)
	}
	return Instance{cfg: cfg, resolver: resolver}, nil
}

type Instance struct {
	cfg      Config
	resolver netx.Resolver
}

func (Instance) Start(context.Context) error { return nil }
func (Instance) Stop(context.Context) error  { return nil }

func (i Instance) Probe(ctx context.Context, flow netx.Flow) netx.ProbeResult {
	endpoint := i.cfg.Endpoint
	if endpoint == "" {
		endpoint = flow.Address
	}
	host, _, _ := net.SplitHostPort(endpoint)
	started := time.Now()
	var addresses []string
	var err error
	if i.resolver != nil {
		addresses, err = i.resolver.LookupHost(ctx, host)
	} else {
		addresses, err = net.DefaultResolver.LookupHost(ctx, host)
	}
	if err != nil {
		return netx.ProbeResult{Status: "failed", LatencyMS: elapsedMS(started), Error: fmt.Sprintf("DNS resolution failed: %v", err)}
	}
	resolvedHost := ""
	if len(addresses) > 0 {
		resolvedHost = addresses[0]
	}
	dialer := net.Dialer{Timeout: time.Duration(i.cfg.TimeoutSeconds) * time.Second}
	var conn net.Conn
	if i.cfg.TLS {
		serverName := host
		conn, err = (&tls.Dialer{NetDialer: &dialer, Config: &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", endpoint)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", endpoint)
	}
	if err != nil {
		return netx.ProbeResult{Status: "failed", LatencyMS: elapsedMS(started), ResolvedHost: resolvedHost, Error: fmt.Sprintf("endpoint reachability failed: %v", err)}
	}
	_ = conn.Close()
	return netx.ProbeResult{Status: "healthy", LatencyMS: elapsedMS(started), ResolvedHost: resolvedHost}
}

func elapsedMS(start time.Time) float64 { return float64(time.Since(start).Microseconds()) / 1000 }
