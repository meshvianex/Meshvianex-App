package dns_system

import (
	"context"
	"encoding/json"
	"net"
	"meshvianex/core/plugin"
)

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.dns.system", Name: "System DNS", Version: "0.1.0", PluginTypes: []string{"dns"}, RequiresCapabilities: []string{"dns.resolve"}, Provides: []string{"dns.resolver"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "System DNS"}}
}
func (Factory) Create(context.Context, plugin.InstanceContext, json.RawMessage) (plugin.Instance, error) {
	return &Instance{}, nil
}

type Instance struct{}

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }
func (*Instance) LookupHost(ctx context.Context, h string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, h)
}
