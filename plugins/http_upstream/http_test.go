package http_upstream

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func TestHTTPProxyOutboundPerformsConnectAndCarriesStream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	seen := make(chan string, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		request, readErr := http.ReadRequest(bufio.NewReader(conn))
		if readErr != nil {
			return
		}
		seen <- request.Method + " " + request.Host
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_, _ = io.Copy(conn, conn)
	}()
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, []byte(`{"url":"http://`+listener.Addr().String()+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := instance.(*Instance).DialContext(context.Background(), netx.NewFlow("r", "tcp", "example.com:443", "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err = io.ReadFull(conn, response); err != nil || string(response) != "ok" {
		t.Fatalf("response=%q err=%v", response, err)
	}
	if request := <-seen; request != "CONNECT example.com:443" {
		t.Fatalf("request=%q", request)
	}
}
