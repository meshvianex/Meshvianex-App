// Package tcp_transport provides the portable baseline transport used by the
// raw protocol stack. It owns only TCP connection mechanics; routing remains
// the Realm's responsibility.
package tcp_transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type Config struct {
	TimeoutSeconds   int  `json:"timeout_seconds,omitempty"`
	KeepAliveSeconds int  `json:"keep_alive_seconds,omitempty"`
	NoDelay          bool `json:"no_delay,omitempty"`
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "io.meshvianex.transport.raw", Name: "RAW Stream Transport", Version: "0.1.0",
		PluginTypes: []string{"transport"}, RequiresCapabilities: []string{"socket.tcp"},
		Provides: []string{"transport.raw"}, ABI: 1,
		ConfigSchema: &plugin.ConfigSchema{Title: "RAW Stream Transport", Fields: []plugin.ConfigField{
			{ID: "timeout_seconds", Type: "number", Label: "Dial timeout (seconds)", Default: 15},
			{ID: "keep_alive_seconds", Type: "number", Label: "Keep-alive (seconds)", Default: 30},
			{ID: "no_delay", Type: "boolean", Label: "TCP no-delay", Default: true},
		}},
	}
}

func (Factory) Create(_ context.Context, _ plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("tcp transport config: %w", err)
		}
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 15
	}
	if cfg.KeepAliveSeconds <= 0 {
		cfg.KeepAliveSeconds = 30
	}
	return &Instance{cfg: cfg}, nil
}

type Instance struct{ cfg Config }

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }

func (i *Instance) OpenStream(ctx context.Context, flow netx.Flow) (netx.Stream, error) {
	network := flow.Network
	if network == "" {
		network = "tcp"
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("tcp transport does not support network %q", network)
	}
	if flow.Address == "" {
		return nil, fmt.Errorf("tcp transport requires a destination address")
	}
	dialer := net.Dialer{Timeout: time.Duration(i.cfg.TimeoutSeconds) * time.Second, KeepAlive: time.Duration(i.cfg.KeepAliveSeconds) * time.Second}
	conn, err := dialer.DialContext(ctx, network, flow.Address)
	if err != nil {
		return nil, fmt.Errorf("tcp transport dial %s: %w", flow.Address, err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok && i.cfg.NoDelay {
		if err := tcp.SetNoDelay(true); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("tcp transport set no-delay: %w", err)
		}
	}
	return conn, nil
}
