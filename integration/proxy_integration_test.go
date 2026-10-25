package integration

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/core/realm"
	"meshvianex/plugins/direct"
	"meshvianex/plugins/http_upstream"
	"meshvianex/plugins/socks5_outbound"
)

// This test is intentionally opt-in so normal CI never depends on a user's
// local proxy. It exercises a real Realm runtime and the selected outbound,
// including TLS/HTTP bytes after the proxy handshake.
func TestConfiguredProxyRuntime(t *testing.T) {
	target := os.Getenv("MESHVIANEX_TEST_TARGET")
	if target == "" {
		target = "example.com:443"
	}
	cases := []struct {
		name, env, plugin string
	}{
		{name: "http", env: "MESHVIANEX_TEST_HTTP_PROXY", plugin: "io.meshvianex.outbound.http"},
		{name: "socks", env: "MESHVIANEX_TEST_SOCKS_PROXY", plugin: "io.meshvianex.outbound.socks5"},
	}
	for _, tc := range cases {
		proxy := os.Getenv(tc.env)
		if proxy == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			runProxyRuntime(t, tc.plugin, proxy, target)
		})
	}
	if os.Getenv("MESHVIANEX_TEST_DIRECT") != "" {
		t.Run("direct", func(t *testing.T) { runProxyRuntime(t, "io.meshvianex.direct", "", target) })
	}
	if len(testsEnabled()) == 0 {
		t.Skip("set MESHVIANEX_TEST_HTTP_PROXY, MESHVIANEX_TEST_SOCKS_PROXY, or MESHVIANEX_TEST_DIRECT=1 to run external connectivity integration")
	}
}

func testsEnabled() []string {
	values := []string{}
	for _, key := range []string{"MESHVIANEX_TEST_HTTP_PROXY", "MESHVIANEX_TEST_SOCKS_PROXY", "MESHVIANEX_TEST_DIRECT"} {
		if os.Getenv(key) != "" {
			values = append(values, key)
		}
	}
	return values
}

func runProxyRuntime(t *testing.T, pluginID, proxyURL, target string) {
	t.Helper()
	grants := plugin.CapabilitySet{"socket.tcp": true, "config.read": true}
	registry := plugin.NewRegistry(grants)
	for _, factory := range []plugin.Factory{direct.Factory{}, http_upstream.Factory{}, socks5_outbound.Factory{}} {
		if err := registry.Register(factory); err != nil {
			t.Fatal(err)
		}
	}
	config := json.RawMessage(`{}`)
	if pluginID == "io.meshvianex.outbound.http" {
		config = json.RawMessage(fmt.Sprintf(`{"url":%q,"timeout_seconds":30}`, proxyURL))
	} else if pluginID == "io.meshvianex.outbound.socks5" {
		config = json.RawMessage(fmt.Sprintf(`{"url":%q,"timeout_seconds":30}`, proxyURL))
	}
	rt := realm.NewRuntime(realm.Config{ID: "integration", Name: "Integration", SchemaVersion: 1,
		Outbounds: []realm.ComponentRef{{InstanceID: "selected", Plugin: pluginID, Enabled: true, Config: config}}, DefaultOutbound: "selected"}, registry, nil)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := rt.DialContext(ctx, netx.NewFlow("integration", "tcp", target, "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	host, _, _ := net.SplitHostPort(target)
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(tlsConn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 500 {
		t.Fatalf("unexpected external response: %s", response.Status)
	}
}
