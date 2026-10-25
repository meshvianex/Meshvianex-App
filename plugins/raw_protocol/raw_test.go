package raw_protocol

import (
	"context"
	"io"
	"net"
	"testing"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type testOpener struct{ conn net.Conn }

func (o testOpener) OpenStream(context.Context, netx.Flow) (netx.Stream, error) { return o.conn, nil }

func TestRawProtocolPassesThroughTransportStream(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := instance.(*Instance).OpenSession(context.Background(), netx.NewFlow("r", "tcp", "example.com:443", "user_traffic"), testOpener{conn: left})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = right.Write([]byte("ok")) }()
	response := make([]byte, 2)
	if _, err = io.ReadFull(stream, response); err != nil || string(response) != "ok" {
		t.Fatalf("response=%q err=%v", response, err)
	}
}
