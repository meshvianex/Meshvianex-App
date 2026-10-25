package direct

import (
	"context"
	"encoding/json"
	"net"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"time"
)

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.direct", Name: "Direct Outbound", Version: "0.1.0", PluginTypes: []string{"outbound"}, RequiresCapabilities: []string{"socket.tcp"}, Provides: []string{"outbound.direct"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Direct Outbound", Fields: []plugin.ConfigField{{ID: "timeout_seconds", Type: "number", Label: "Timeout (seconds)", Default: 15, Required: true}}}}
}
func (Factory) Create(_ context.Context, _ plugin.InstanceContext, cfg json.RawMessage) (plugin.Instance, error) {
	var c struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	_ = json.Unmarshal(cfg, &c)
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 15
	}
	return &Instance{d: net.Dialer{Timeout: time.Duration(c.TimeoutSeconds) * time.Second}}, nil
}

type Instance struct{ d net.Dialer }

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }
func (i *Instance) DialContext(ctx context.Context, f netx.Flow) (net.Conn, error) {
	return i.d.DialContext(ctx, f.Network, f.Address)
}
