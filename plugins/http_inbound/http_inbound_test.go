package http_inbound

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type fakeDialer struct {
	conn  net.Conn
	err   error
	flows chan netx.Flow
}

func (d *fakeDialer) DialContext(ctx context.Context, flow netx.Flow) (net.Conn, error) {
	if d.flows != nil {
		d.flows <- flow
	}
	if d.err != nil {
		return nil, d.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.conn, nil
}

func TestHTTPInboundConnectAndBidirectionalCopy(t *testing.T) {
	client, proxy := net.Pipe()
	up, peer := net.Pipe()
	dialer := &fakeDialer{conn: up, flows: make(chan netx.Flow, 1)}
	instance := &Instance{dialer: dialer}
	done := make(chan error, 1)
	go func() { done <- instance.handle(context.Background(), proxy) }()
	go func() {
		_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	}()
	response, err := httpResponse(bufio.NewReader(client))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT response=%v err=%v", response, err)
	}
	buf := make([]byte, 4)
	received := make(chan error, 1)
	go func() { _, err := io.ReadFull(peer, buf); received <- err }()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err != nil || string(buf) != "ping" {
		t.Fatalf("upstream received=%q err=%v", buf, err)
	}
	if _, err := peer.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != "pong" {
		t.Fatalf("client received=%q err=%v", buf, err)
	}
	_ = client.Close()
	_ = peer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CONNECT handler did not stop")
	}
	select {
	case flow := <-dialer.flows:
		if flow.Address != "example.test:443" {
			t.Fatalf("flow=%#v", flow)
		}
	case <-time.After(time.Second):
		t.Fatal("dialer was not called")
	}
}

func TestHTTPInboundForwardAndFailurePaths(t *testing.T) {
	client, proxy := net.Pipe()
	up, peer := net.Pipe()
	dialer := &fakeDialer{conn: up}
	instance := &Instance{dialer: dialer}
	done := make(chan error, 1)
	go func() { done <- instance.handle(context.Background(), proxy) }()
	go func() {
		req, err := http.ReadRequest(bufio.NewReader(peer))
		if err == nil && req.Host != "example.test" {
			err = fmt.Errorf("host=%s", req.Host)
		}
		if err == nil {
			_, _ = io.WriteString(peer, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		}
	}()
	_, _ = io.WriteString(client, "GET http://example.test/path HTTP/1.1\r\nHost: example.test\r\n\r\n")
	response, err := httpResponse(bufio.NewReader(client))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("forward response=%v err=%v", response, err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "ok" {
		t.Fatalf("forward body=%q err=%v", body, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("forward handler error: %v", err)
	}
	_ = client.Close()
	_ = peer.Close()

	badClient, badProxy := net.Pipe()
	badDialer := &fakeDialer{err: errors.New("unreachable")}
	badInstance := &Instance{dialer: badDialer}
	badDone := make(chan error, 1)
	go func() { badDone <- badInstance.handle(context.Background(), badProxy) }()
	_, _ = io.WriteString(badClient, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	badResponse, err := httpResponse(bufio.NewReader(badClient))
	if err != nil || badResponse.StatusCode != 502 {
		t.Fatalf("failure response=%v err=%v", badResponse, err)
	}
	_ = badClient.Close()
	if err := <-badDone; err == nil {
		t.Fatal("dial failure was not returned")
	}

	malformedClient, malformedProxy := net.Pipe()
	malformedDialer := &fakeDialer{conn: nil, flows: make(chan netx.Flow, 1)}
	malformedDone := make(chan error, 1)
	go func() {
		malformedDone <- (&Instance{dialer: malformedDialer}).handle(context.Background(), malformedProxy)
	}()
	_, _ = io.WriteString(malformedClient, "CONNECT :bad HTTP/1.1\r\nHost: :bad\r\n\r\n")
	if err := <-malformedDone; err == nil || len(malformedDialer.flows) != 0 {
		t.Fatalf("malformed target err=%v flows=%d", err, len(malformedDialer.flows))
	}
	_ = malformedClient.Close()
}

func TestHTTPInboundCancellationAndLoopbackPolicy(t *testing.T) {
	client, proxy := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- (&Instance{dialer: &fakeDialer{err: context.Canceled}}).handle(ctx, proxy) }()
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	response, err := httpResponse(bufio.NewReader(client))
	if err != nil || response.StatusCode != 502 {
		t.Fatalf("cancellation response=%v err=%v", response, err)
	}
	_ = client.Close()
	if err := <-done; err == nil {
		t.Fatal("cancellation did not propagate")
	}

	services := map[string]any{"realm.dialer": &fakeDialer{err: errors.New("unused")}}
	if _, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{Services: services}, json.RawMessage(`{"listen":"0.0.0.0:8080"}`)); err == nil {
		t.Fatal("non-loopback listen was accepted")
	}
	if _, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{Services: services}, json.RawMessage(`{"listen":"127.0.0.1:0"}`)); err != nil {
		t.Fatalf("loopback listen rejected: %v", err)
	}
}

func httpResponse(reader *bufio.Reader) (*http.Response, error) {
	return http.ReadResponse(reader, nil)
}

func TestHTTPInboundReturnsBadGatewayWhenOutboundFails(t *testing.T) {
	for _, method := range []string{"GET", "CONNECT"} {
		t.Run(method, func(t *testing.T) {
			client, proxy := net.Pipe()
			defer client.Close()
			instance := &Instance{dialer: &fakeDialer{err: errors.New("no Local Source Realm route")}}
			done := make(chan error, 1)
			go func() { done <- instance.handle(context.Background(), proxy); _ = proxy.Close() }()
			target := "http://example.test/path"
			if method == "CONNECT" {
				target = "example.test:443"
			}
			_, _ = fmt.Fprintf(client, "%s %s HTTP/1.1\r\nHost: example.test\r\n\r\n", method, target)
			resp, err := http.ReadResponse(bufio.NewReader(client), nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status %d, want 502", resp.StatusCode)
			}
			if err := <-done; err == nil {
				t.Fatal("missing outbound failure")
			}
		})
	}
}
