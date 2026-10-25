package socks5_inbound

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type recordingDialer struct {
	target string
	flow   chan netx.Flow
}

func (d *recordingDialer) DialContext(ctx context.Context, flow netx.Flow) (net.Conn, error) {
	d.flow <- flow
	return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", d.target)
}

func TestSOCKS5InboundRoutesThroughRealmDialer(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, acceptErr := echo.Accept()
		if acceptErr == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := probe.Addr().String()
	_ = probe.Close()
	dialer := &recordingDialer{target: echo.Addr().String(), flow: make(chan netx.Flow, 1)}
	raw, _ := json.Marshal(Config{Listen: listen})
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{Services: map[string]any{"realm.dialer": netx.Dialer(dialer)}}, raw)
	if err != nil {
		t.Fatal(err)
	}
	inbound := instance.(*Instance)
	if err := inbound.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer inbound.Stop(context.Background())
	client, err := net.DialTimeout("tcp", listen, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, _ = client.Write([]byte{5, 1, 0})
	var selected [2]byte
	if _, err := io.ReadFull(client, selected[:]); err != nil || selected != [2]byte{5, 0} {
		t.Fatalf("method response=%v err=%v", selected, err)
	}
	host, portText, _ := net.SplitHostPort(echo.Addr().String())
	port := make([]byte, 2)
	portValue, _ := strconv.Atoi(portText)
	binary.BigEndian.PutUint16(port, uint16(portValue))
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, host...)
	request = append(request, port...)
	_, _ = client.Write(request)
	var reply [10]byte
	if _, err := io.ReadFull(client, reply[:]); err != nil || reply[1] != 0 {
		t.Fatalf("connect response=%v err=%v", reply, err)
	}
	select {
	case flow := <-dialer.flow:
		if flow.Host != host || flow.Port != portValue {
			t.Fatalf("flow=%#v", flow)
		}
	case <-time.After(time.Second):
		t.Fatal("realm dialer was not called")
	}
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("echo=%q err=%v", buf, err)
	}
}
