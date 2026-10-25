package socks5_outbound

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type Config struct {
	URL            string `json:"url"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.outbound.socks5", Name: "SOCKS5 Outbound", Version: "0.1.0", PluginTypes: []string{"outbound"}, RequiresCapabilities: []string{"socket.tcp", "config.read"}, Provides: []string{"outbound.socks5", "outbound.socks5h"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "SOCKS5 Proxy", Fields: []plugin.ConfigField{{ID: "url", Type: "url", Label: "SOCKS URL", Placeholder: "socks5h://proxy.example:1080", Required: true}, {ID: "username", Type: "text", Label: "Username"}, {ID: "password", Type: "secret", Label: "Password"}, {ID: "timeout_seconds", Type: "number", Label: "Timeout (seconds)", Default: 15}}}}
}

func (Factory) Create(_ context.Context, _ plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("socks5 config: %w", err)
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("socks5 proxy url is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || !isSOCKSScheme(u.Scheme) || u.Hostname() == "" {
		return nil, fmt.Errorf("socks5 proxy url must use socks://, socks5://, or socks5h:// and include a host")
	}
	if u.Port() == "" {
		return nil, fmt.Errorf("socks5 proxy url requires a port")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 15
	}
	if cfg.Username == "" && u.User != nil {
		cfg.Username = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	if len(cfg.Username) > 255 || len(cfg.Password) > 255 {
		return nil, fmt.Errorf("socks5 credentials exceed protocol limits")
	}
	return &Instance{cfg: cfg, proxy: net.JoinHostPort(u.Hostname(), u.Port())}, nil
}

type Instance struct {
	cfg   Config
	proxy string
}

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }

func (i *Instance) DialContext(ctx context.Context, flow netx.Flow) (net.Conn, error) {
	network := flow.Network
	if network == "" {
		network = "tcp"
	}
	dialer := net.Dialer{Timeout: time.Duration(i.cfg.TimeoutSeconds) * time.Second}
	conn, err := dialer.DialContext(ctx, network, i.proxy)
	if err != nil {
		return nil, fmt.Errorf("socks5 connect to proxy: %w", err)
	}
	stopDeadline := armDeadline(ctx, conn, time.Duration(i.cfg.TimeoutSeconds)*time.Second)
	defer stopDeadline()
	if err := i.handshake(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := writeConnectRequest(conn, flow); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := readConnectReply(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (i *Instance) handshake(conn net.Conn) error {
	methods := []byte{0x00}
	if i.cfg.Username != "" {
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return fmt.Errorf("socks5 method negotiation: %w", err)
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		return fmt.Errorf("socks5 method response: %w", err)
	}
	if response[0] != 0x05 {
		return fmt.Errorf("socks5 proxy returned unsupported version %d", response[0])
	}
	switch response[1] {
	case 0x00:
		return nil
	case 0x02:
		if i.cfg.Username == "" {
			return fmt.Errorf("socks5 proxy requires authentication")
		}
		user, pass := []byte(i.cfg.Username), []byte(i.cfg.Password)
		request := append([]byte{0x01, byte(len(user))}, user...)
		request = append(request, byte(len(pass)))
		request = append(request, pass...)
		if _, err := conn.Write(request); err != nil {
			return fmt.Errorf("socks5 authentication: %w", err)
		}
		var authResponse [2]byte
		if _, err := io.ReadFull(conn, authResponse[:]); err != nil {
			return fmt.Errorf("socks5 authentication response: %w", err)
		}
		if authResponse[0] != 0x01 || authResponse[1] != 0x00 {
			return fmt.Errorf("socks5 authentication failed")
		}
		return nil
	case 0xff:
		return fmt.Errorf("socks5 proxy rejected all authentication methods")
	default:
		return fmt.Errorf("socks5 proxy selected unsupported authentication method 0x%02x", response[1])
	}
}

func writeConnectRequest(conn net.Conn, flow netx.Flow) error {
	host, port := flow.Host, flow.Port
	if host == "" || port == 0 {
		host, _, _ = net.SplitHostPort(flow.Address)
		if port == 0 {
			if _, portText, err := net.SplitHostPort(flow.Address); err == nil {
				port, _ = strconv.Atoi(portText)
			}
		}
	}
	if host == "" || port <= 0 || port > 65535 {
		return fmt.Errorf("socks5 destination must include a valid host and port")
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			request = append(request, 0x01)
			request = append(request, v4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("socks5 destination hostname is too long")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = append(request, byte(port>>8), byte(port))
	_, err := conn.Write(request)
	if err != nil {
		return fmt.Errorf("socks5 CONNECT request: %w", err)
	}
	return nil
}

func readConnectReply(conn net.Conn) error {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return fmt.Errorf("socks5 CONNECT response: %w", err)
	}
	if header[0] != 0x05 {
		return fmt.Errorf("socks5 CONNECT response has version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("socks5 CONNECT failed: %s", replyDescription(header[1]))
	}
	var addressLength int
	switch header[3] {
	case 0x01:
		addressLength = 4
	case 0x04:
		addressLength = 16
	case 0x03:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return fmt.Errorf("socks5 bound address: %w", err)
		}
		addressLength = int(length[0])
	default:
		return fmt.Errorf("socks5 CONNECT response has invalid address type 0x%02x", header[3])
	}
	bound := make([]byte, addressLength+2)
	if _, err := io.ReadFull(conn, bound); err != nil {
		return fmt.Errorf("socks5 bound address: %w", err)
	}
	return nil
}

func armDeadline(ctx context.Context, conn net.Conn, timeout time.Duration) func() {
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func isSOCKSScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "socks", "socks5", "socks5h":
		return true
	default:
		return false
	}
}

func replyDescription(code byte) string {
	descriptions := map[byte]string{1: "general SOCKS server failure", 2: "connection not allowed", 3: "network unreachable", 4: "host unreachable", 5: "connection refused", 6: "TTL expired", 7: "command not supported", 8: "address type not supported"}
	if value, ok := descriptions[code]; ok {
		return value
	}
	return fmt.Sprintf("reply 0x%02x", code)
}
