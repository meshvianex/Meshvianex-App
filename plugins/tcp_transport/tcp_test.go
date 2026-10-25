package tcp_transport

import (
	"context"
	"io"
	"net"
	"testing"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func TestTCPTransportOpensStreamWithFlowAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	instance, err := (Factory{}).Create(context.Background(), pluginContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := instance.(*Instance).OpenStream(context.Background(), netx.NewFlow("r", "tcp", listener.Addr().String(), "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err = io.ReadFull(stream, response); err != nil || string(response) != "ok" {
		t.Fatalf("response=%q err=%v", response, err)
	}
}

// Keep the test independent of registry internals; the transport does not
// require any host service beyond its declared socket capability.
func pluginContext() plugin.InstanceContext { return plugin.InstanceContext{} }
