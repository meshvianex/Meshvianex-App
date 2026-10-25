// Package raw_protocol implements a protocol-neutral pass-through session.
// It is useful for composing a protocol component with a transport without
// making Core understand protocol-specific framing.
package raw_protocol

import (
	"context"
	"encoding/json"
	"fmt"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "io.meshvianex.protocol.passthrough", Name: "Passthrough Protocol", Version: "0.1.0",
		PluginTypes: []string{"protocol"}, RequiresCapabilities: []string{"stream.read", "stream.write"},
		Provides: []string{"protocol.passthrough"}, ABI: 1,
		ConfigSchema: &plugin.ConfigSchema{Title: "Passthrough Protocol"},
	}
}

func (Factory) Create(_ context.Context, _ plugin.InstanceContext, _ json.RawMessage) (plugin.Instance, error) {
	return &Instance{}, nil
}

type Instance struct{}

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }

func (i *Instance) OpenSession(ctx context.Context, flow netx.Flow, opener netx.StreamOpener) (netx.Stream, error) {
	if opener == nil {
		return nil, fmt.Errorf("raw protocol requires a transport")
	}
	stream, err := opener.OpenStream(ctx, flow)
	if err != nil {
		return nil, err
	}
	return stream, nil
}
