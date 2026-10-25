package http_inbound

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Listen      string `json:"listen"`
	AllowRemote bool   `json:"allow_remote,omitempty"`
}
type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.inbound.http", Name: "Local HTTP Proxy Inbound", Version: "0.1.0", PluginTypes: []string{"inbound"}, RequiresCapabilities: []string{"socket.listen", "stream.read", "stream.write"}, Provides: []string{"inbound.http_proxy"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Local HTTP Proxy", Fields: []plugin.ConfigField{{ID: "listen", Type: "host_port", Label: "Listen address", Placeholder: "127.0.0.1:8080", Default: "127.0.0.1:8080", Required: true}, {ID: "allow_remote", Type: "boolean", Label: "Allow remote clients", Default: false}}}}
}
func (Factory) Create(_ context.Context, ictx plugin.InstanceContext, b json.RawMessage) (plugin.Instance, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	svc, ok := ictx.Services["realm.dialer"]
	if !ok {
		return nil, fmt.Errorf("realm.dialer service unavailable")
	}
	d, ok := svc.(netx.Dialer)
	if !ok {
		return nil, fmt.Errorf("realm.dialer has wrong type")
	}
	if !c.AllowRemote && !isLoopbackListen(c.Listen) {
		return nil, fmt.Errorf("refusing non-loopback proxy listen without allow_remote=true")
	}
	return &Instance{cfg: c, dialer: d}, nil
}

type Instance struct {
	cfg    Config
	dialer netx.Dialer
	ln     net.Listener
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func (i *Instance) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", i.cfg.Listen)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	i.ln = ln
	i.cancel = cancel
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}
			i.wg.Add(1)
			go func() { defer i.wg.Done(); defer c.Close(); _ = i.handle(ctx, c) }()
		}
	}()
	return nil
}

func (i *Instance) ListenAddress() string {
	if i.ln == nil {
		return ""
	}
	return i.ln.Addr().String()
}

func (i *Instance) Stop(context.Context) error {
	if i.cancel != nil {
		i.cancel()
	}
	if i.ln != nil {
		_ = i.ln.Close()
	}
	i.wg.Wait()
	return nil
}
func (i *Instance) handle(ctx context.Context, client net.Conn) error {
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		return err
	}
	if req.Method == http.MethodConnect {
		return i.connect(ctx, client, br, req)
	}
	return i.forward(ctx, client, req)
}
func (i *Instance) connect(ctx context.Context, client net.Conn, clientReader io.Reader, req *http.Request) error {
	addr, err := proxyAddress(req.Host, "443")
	if err != nil {
		writeProxyError(client, http.StatusBadRequest)
		return err
	}
	flow := netx.NewFlow("", "tcp", addr, "user_traffic")
	up, err := i.dialer.DialContext(ctx, flow)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway)
		return err
	}
	defer up.Close()
	_ = client.SetDeadline(time.Time{})
	_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\nProxy-Agent: Meshvianex/0.1\r\n\r\n")
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, clientReader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); done <- struct{}{} }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = client.Close()
		_ = up.Close()
		return ctx.Err()
	}
}
func (i *Instance) forward(ctx context.Context, client net.Conn, req *http.Request) error {
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	host, err := proxyAddress(host, "80")
	if err != nil {
		writeProxyError(client, http.StatusBadRequest)
		return err
	}
	up, err := i.dialer.DialContext(ctx, netx.NewFlow("", "tcp", host, "user_traffic"))
	if err != nil {
		// Ordinary HTTP requests need an HTTP error response just like
		// CONNECT. Otherwise clients see an unexplained EOF when the
		// Local Source is disconnected or its outbound fails.
		writeProxyError(client, http.StatusBadGateway)
		return err
	}
	defer up.Close()
	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""
	req.Header.Del("Proxy-Connection")
	req.Header.Set("Connection", "close")
	if err = req.Write(up); err != nil {
		writeProxyError(client, http.StatusBadGateway)
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(up), req)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway)
		return err
	}
	defer resp.Body.Close()
	resp.Header.Set("Connection", "close")
	return resp.Write(client)
}
func writeProxyError(client net.Conn, status int) {
	_, _ = fmt.Fprintf(client, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", status, http.StatusText(status))
}

func isLoopbackListen(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

func proxyAddress(raw, defaultPort string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n \t") {
		return "", fmt.Errorf("proxy target is empty or malformed")
	}
	if !strings.Contains(raw, ":") {
		return net.JoinHostPort(raw, defaultPort), nil
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" {
		return "", fmt.Errorf("proxy target %q is malformed", raw)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("proxy target %q has an invalid port", raw)
	}
	return net.JoinHostPort(host, port), nil
}
