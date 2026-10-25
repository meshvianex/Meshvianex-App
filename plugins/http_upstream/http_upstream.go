package http_upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"strings"
	"time"
)

type Config struct {
	URL                string `json:"url"`
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	TimeoutSeconds     int    `json:"timeout_seconds,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}
type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.outbound.http", Name: "HTTP Proxy Outbound", Version: "0.1.0", PluginTypes: []string{"outbound"}, RequiresCapabilities: []string{"socket.tcp", "config.read"}, Provides: []string{"outbound.http_proxy"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "HTTP Proxy", Fields: []plugin.ConfigField{{ID: "url", Type: "url", Label: "Proxy URL", Placeholder: "https://proxy.example:8443", Required: true}, {ID: "username", Type: "text", Label: "Username"}, {ID: "password", Type: "secret", Label: "Password"}, {ID: "timeout_seconds", Type: "number", Label: "Timeout (seconds)", Default: 15}, {ID: "insecure_skip_verify", Type: "boolean", Label: "Skip TLS verification", Default: false}}}}
}
func (Factory) Create(_ context.Context, _ plugin.InstanceContext, b json.RawMessage) (plugin.Instance, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("proxy url must use http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy url requires host")
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 15
	}
	return &Instance{cfg: c, u: u}, nil
}

type Instance struct {
	cfg Config
	u   *url.URL
}

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (i *Instance) DialContext(ctx context.Context, f netx.Flow) (net.Conn, error) {
	d := net.Dialer{Timeout: time.Duration(i.cfg.TimeoutSeconds) * time.Second}
	var c net.Conn
	var err error
	if i.u.Scheme == "https" {
		host := i.u.Hostname()
		td := tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: host, InsecureSkipVerify: i.cfg.InsecureSkipVerify}}
		c, err = td.DialContext(ctx, "tcp", hostPort(i.u))
	} else {
		c, err = d.DialContext(ctx, "tcp", hostPort(i.u))
	}
	if err != nil {
		return nil, err
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: f.Address}, Host: f.Address, Header: make(http.Header)}
	user := i.cfg.Username
	pass := i.cfg.Password
	if user == "" && i.u.User != nil {
		user = i.u.User.Username()
		pass, _ = i.u.User.Password()
	}
	if user != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	}
	if err = req.Write(c); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		c.Close()
		return nil, err
	}
	if resp.Body != nil {
		resp.Body.Close()
	}
	if resp.StatusCode/100 != 2 {
		c.Close()
		return nil, fmt.Errorf("upstream proxy CONNECT failed: %s", resp.Status)
	}
	return &bufferedConn{Conn: c, r: br}, nil
}
func hostPort(u *url.URL) string {
	if strings.Contains(u.Host, ":") {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Host, "443")
	}
	return net.JoinHostPort(u.Host, "80")
}
