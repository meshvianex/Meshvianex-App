package rules_basic

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"strings"
)

type Rule struct {
	DomainSuffix string `json:"domain_suffix,omitempty"`
	CIDR         string `json:"cidr,omitempty"`
	Port         int    `json:"port,omitempty"`
	Purpose      string `json:"purpose,omitempty"`
	Outbound     string `json:"outbound"`
	Reason       string `json:"reason,omitempty"`
	network      *net.IPNet
}
type Config struct {
	Default string `json:"default"`
	Rules   []Rule `json:"rules"`
}
type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.rules.basic", Name: "Basic Routing", Version: "0.1.0", PluginTypes: []string{"routing"}, RequiresCapabilities: []string{"config.read"}, Provides: []string{"routing.basic"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Basic Routing", Fields: []plugin.ConfigField{{ID: "default", Type: "text", Label: "Default outbound", Required: true}, {ID: "rules", Type: "array", Label: "Rules", ItemFields: []plugin.ConfigField{{ID: "domain_suffix", Type: "text", Label: "Domain suffix"}, {ID: "cidr", Type: "text", Label: "CIDR"}, {ID: "port", Type: "number", Label: "Port"}, {ID: "purpose", Type: "select", Label: "Purpose", Options: []plugin.ConfigOption{{Value: "", Label: "Any purpose"}, {Value: "user_traffic", Label: "User traffic"}, {Value: "plugin_download", Label: "Plugin download"}, {Value: "health_check", Label: "Health check"}}}, {ID: "outbound", Type: "text", Label: "Outbound", Required: true}, {ID: "reason", Type: "text", Label: "Reason"}}}}}}
}
func (Factory) Create(_ context.Context, _ plugin.InstanceContext, b json.RawMessage) (plugin.Instance, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Default == "" {
		return nil, fmt.Errorf("basic rules requires default outbound")
	}
	for x := range c.Rules {
		if c.Rules[x].CIDR != "" {
			_, n, err := net.ParseCIDR(c.Rules[x].CIDR)
			if err != nil {
				return nil, err
			}
			c.Rules[x].network = n
		}
	}
	return &Instance{cfg: c}, nil
}

type Instance struct{ cfg Config }

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }
func (i *Instance) Select(_ context.Context, f netx.Flow) (netx.RouteDecision, error) {
	for _, r := range i.cfg.Rules {
		if r.Purpose != "" && r.Purpose != f.Purpose {
			continue
		}
		if r.Port != 0 && r.Port != f.Port {
			continue
		}
		if r.DomainSuffix != "" && !strings.HasSuffix(strings.ToLower(f.Host), strings.ToLower(r.DomainSuffix)) {
			continue
		}
		if r.network != nil {
			ip := net.ParseIP(f.Host)
			if ip == nil || !r.network.Contains(ip) {
				continue
			}
		}
		reason := r.Reason
		if reason == "" {
			reason = "matched basic rule"
		}
		return netx.RouteDecision{Outbound: r.Outbound, Reason: reason}, nil
	}
	return netx.RouteDecision{Outbound: i.cfg.Default, Reason: "basic rules default"}, nil
}
