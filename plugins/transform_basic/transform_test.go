package transform_basic

import (
	"context"
	"encoding/json"
	"testing"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func TestMetadataTransformAnnotatesAndDrops(t *testing.T) {
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage(`{"set":{"source":"test"},"append":{"trace":"-a"}}`))
	if err != nil {
		t.Fatal(err)
	}
	packet, result, err := instance.(*Instance).Transform(context.Background(), netx.StreamData, netx.Packet{Metadata: map[string]string{"trace": "start"}}, netx.Flow{})
	if err != nil || result != netx.Continue || packet.Metadata["source"] != "test" || packet.Metadata["trace"] != "start-a" {
		t.Fatalf("packet=%#v result=%q err=%v", packet, result, err)
	}
	drop, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage(`{"drop":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_, result, err = drop.(*Instance).Transform(context.Background(), netx.StreamOpen, netx.Packet{}, netx.Flow{})
	if err != nil || result != netx.Drop {
		t.Fatalf("drop result=%q err=%v", result, err)
	}
}
