package health_probe

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func TestConnectivityProbeTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage(`{"endpoint":"`+listener.Addr().String()+`","timeout_seconds":1}`))
	if err != nil {
		t.Fatal(err)
	}
	result := instance.(Instance).Probe(context.Background(), netx.NewFlow("", "tcp", listener.Addr().String(), "health_check"))
	if result.Status != "healthy" || result.Error != "" || result.ResolvedHost == "" {
		t.Fatalf("unexpected successful probe: %#v", result)
	}
}

func TestConnectivityProbeFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := listener.Addr().String()
	listener.Close()
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage(`{"endpoint":"`+endpoint+`","timeout_seconds":1}`))
	if err != nil {
		t.Fatal(err)
	}
	result := instance.(Instance).Probe(context.Background(), netx.NewFlow("", "tcp", endpoint, "health_check"))
	if result.Status != "failed" || result.Error == "" {
		t.Fatalf("unexpected failed probe: %#v", result)
	}
}
