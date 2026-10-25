package socks5_outbound

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func TestSOCKS5ConnectWithUsernamePasswordAndDomain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer conn.Close()
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil {
			done <- err
			return
		}
		methods := make([]byte, greeting[1])
		if _, err := io.ReadFull(conn, methods); err != nil {
			done <- err
			return
		}
		if greeting[0] != 5 || len(methods) != 2 || methods[0] != 0 || methods[1] != 2 {
			done <- &testError{"unexpected greeting"}
			return
		}
		_, _ = conn.Write([]byte{5, 2})
		var authHeader [2]byte
		if _, err := io.ReadFull(conn, authHeader[:]); err != nil {
			done <- err
			return
		}
		user := make([]byte, authHeader[1])
		if _, err := io.ReadFull(conn, user); err != nil {
			done <- err
			return
		}
		var passLen [1]byte
		if _, err := io.ReadFull(conn, passLen[:]); err != nil {
			done <- err
			return
		}
		pass := make([]byte, passLen[0])
		if _, err := io.ReadFull(conn, pass); err != nil {
			done <- err
			return
		}
		if string(user) != "alice" || string(pass) != "secret" {
			done <- &testError{"unexpected credentials"}
			return
		}
		_, _ = conn.Write([]byte{1, 0})
		var request [4]byte
		if _, err := io.ReadFull(conn, request[:]); err != nil {
			done <- err
			return
		}
		if request[0] != 5 || request[1] != 1 || request[3] != 3 {
			done <- &testError{"unexpected CONNECT header"}
			return
		}
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			done <- err
			return
		}
		destination := make([]byte, length[0]+2)
		if _, err := io.ReadFull(conn, destination); err != nil {
			done <- err
			return
		}
		if string(destination[:length[0]]) != "example.com" || binary.BigEndian.Uint16(destination[length[0]:]) != 443 {
			done <- &testError{"unexpected destination"}
			return
		}
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
		done <- nil
	}()

	raw, _ := json.Marshal(Config{URL: "socks5h://alice:secret@" + listener.Addr().String(), TimeoutSeconds: 2})
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := instance.(*Instance).DialContext(context.Background(), netx.NewFlow("", "tcp", "example.com:443", "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5AuthenticationFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()
		var header [4]byte
		_, _ = io.ReadFull(conn, header[:2])
		methods := make([]byte, header[1])
		_, _ = io.ReadFull(conn, methods)
		_, _ = conn.Write([]byte{5, 2})
		_, _ = io.ReadFull(conn, header[:2])
		user := make([]byte, header[1])
		_, _ = io.ReadFull(conn, user)
		_, _ = io.ReadFull(conn, header[:1])
		pass := make([]byte, header[0])
		_, _ = io.ReadFull(conn, pass)
		_, _ = conn.Write([]byte{1, 1})
	}()
	raw, _ := json.Marshal(Config{URL: "socks5://alice:wrong@" + listener.Addr().String(), TimeoutSeconds: 2})
	instance, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.(*Instance).DialContext(context.Background(), netx.NewFlow("", "tcp", "example.com:443", "user_traffic"))
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("expected authentication error, got %v", err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }
