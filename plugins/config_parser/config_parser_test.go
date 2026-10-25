package config_parser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"meshvianex/core/resources"
	"strings"
	"testing"
)

func TestParseURIListBase64AndDeduplication(t *testing.T) {
	content := strings.Join([]string{
		"# office",
		"direct://",
		"direct://",
		"socks5://127.0.0.1:1080",
		"not-a-supported-entry",
	}, "\n")
	result, err := (Instance{}).Parse(context.Background(), content)
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "uri_list" || len(result.Configs) != 2 || len(result.Diagnostics) != 2 {
		t.Fatalf("unexpected URI result: %#v", result)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("direct://\n"))
	result, err = (Instance{}).Parse(context.Background(), encoded)
	if err != nil || result.Format != "base64" || len(result.Configs) != 1 {
		t.Fatalf("unexpected base64 result: %#v err=%v", result, err)
	}
}

func TestParseJSONMixedInvalidEmptyAndCancellation(t *testing.T) {
	result, err := (Instance{}).Parse(context.Background(), `[{"plugin_id":"io.meshvianex.direct","plugin_type":"outbound","config":{}},{"plugin_id":"","plugin_type":"outbound","config":{}},{"plugin_id":"io.meshvianex.direct"}]`)
	if err != nil || len(result.Configs) != 1 || len(result.Diagnostics) != 2 {
		t.Fatalf("unexpected mixed JSON result: %#v err=%v", result, err)
	}
	result, err = (Instance{}).Parse(context.Background(), "   ")
	if err != nil || result.Format != "empty" || len(result.Configs) != 0 || len(result.Diagnostics) != 1 {
		t.Fatalf("unexpected empty result: %#v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Instance{}).Parse(ctx, "direct://"); err == nil {
		t.Fatal("canceled parse unexpectedly succeeded")
	}
}

func TestParseInvalidJSONIsDiagnostic(t *testing.T) {
	result, err := (Instance{}).Parse(context.Background(), "{not-json")
	if err != nil || len(result.Configs) != 0 || len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "invalid JSON") {
		t.Fatalf("unexpected invalid JSON result: %#v err=%v", result, err)
	}
}

func TestParseRejectsInvalidProxyPorts(t *testing.T) {
	for _, value := range []string{"http://example.com:0", "socks5://example.com:70000", "vless://uuid@example.com:0", "trojan://secret@example.com:70000"} {
		result, err := (Instance{}).Parse(context.Background(), value)
		if err != nil || len(result.Configs) != 0 || len(result.Diagnostics) != 1 {
			t.Fatalf("invalid port %q was accepted: %#v err=%v", value, result, err)
		}
	}
}

func TestParseShareLinksExtractNamesAndXrayProtocols(t *testing.T) {
	result, err := (Instance{}).Parse(context.Background(), "vless://uuid@example.com:443?security=tls#My%20Server")
	if err != nil || len(result.Configs) != 1 {
		t.Fatalf("unexpected VLESS result: %#v err=%v", result, err)
	}
	if result.Configs[0].Name != "My Server" || result.Configs[0].PluginID != XrayOutbound {
		t.Fatalf("VLESS metadata=%#v", result.Configs[0])
	}
	if !strings.Contains(string(result.Configs[0].Config), `"vless"`) {
		t.Fatalf("VLESS protocol was not preserved: %s", result.Configs[0].Config)
	}

	payload, _ := json.Marshal(map[string]any{"ps": "VMess Office", "add": "vmess.example", "port": 443, "id": "00000000-0000-0000-0000-000000000001"})
	vmess := "vmess://" + base64.RawStdEncoding.EncodeToString(payload)
	result, err = (Instance{}).Parse(context.Background(), vmess)
	if err != nil || len(result.Configs) != 1 || result.Configs[0].Name != "VMess Office" {
		t.Fatalf("unexpected VMess result: %#v err=%v", result, err)
	}
	ssPayload := base64.RawStdEncoding.EncodeToString([]byte("aes-256-gcm:secret"))
	result, err = (Instance{}).Parse(context.Background(), "ss://"+ssPayload+"@example.com:8388#SS%20Office")
	if err != nil || len(result.Configs) != 1 || result.Configs[0].Name != "SS Office" {
		t.Fatalf("unexpected Shadowsocks result: %#v err=%v", result, err)
	}
}

func TestParseXrayJSONDropsUserInboundsAndKeepsStableIdentity(t *testing.T) {
	content := `{"name":"first","inbounds":[{"listen":"0.0.0.0","port":1080}],"outbounds":[{"tag":"Office","protocol":"freedom"}]}`
	result, err := (Instance{}).Parse(context.Background(), content)
	if err != nil || len(result.Configs) != 1 || result.Configs[0].Name != "first" {
		t.Fatalf("unexpected Xray JSON result: %#v err=%v", result, err)
	}
	if strings.Contains(string(result.Configs[0].Config), "inbounds") {
		t.Fatalf("user inbound was retained: %s", result.Configs[0].Config)
	}
	first := result.Configs[0]
	second := first
	second.Name = "renamed"
	if resources.StableKey(first) != resources.StableKey(second) {
		t.Fatal("display name changed stable identity")
	}
}

func TestParseShareLinksPreservesSupportedTransportSettings(t *testing.T) {
	result, err := (Instance{}).Parse(context.Background(), "vless://uuid@example.com:443?type=ws&security=tls&sni=cdn.example&path=%2Ffoo%3Fx%3D1&host=cdn.example&alpn=h2%2Chttp%2F1.1&allowInsecure=1#TLS%20WS")
	if err != nil || len(result.Configs) != 1 {
		t.Fatalf("unexpected VLESS result: %#v err=%v", result, err)
	}
	var vless map[string]any
	if err := json.Unmarshal(result.Configs[0].Config, &vless); err != nil {
		t.Fatal(err)
	}
	vlessOutbound := vless["inline_config"].(map[string]any)["outbounds"].([]any)[0].(map[string]any)
	if got := vlessOutbound["protocol"]; got != "vless" {
		t.Fatalf("unexpected VLESS protocol: %#v", got)
	}
	stream := vlessOutbound["streamSettings"].(map[string]any)
	if stream["network"] != "ws" || stream["security"] != "tls" {
		t.Fatalf("transport settings were not preserved: %#v", stream)
	}
	if got := stream["wsSettings"].(map[string]any)["path"]; got != "/foo?x=1" {
		t.Fatalf("unexpected websocket path: %#v", got)
	}
	tls := stream["tlsSettings"].(map[string]any)
	if tls["serverName"] != "cdn.example" || tls["allowInsecure"] != true {
		t.Fatalf("unexpected TLS settings: %#v", tls)
	}

	result, err = (Instance{}).Parse(context.Background(), "trojan://p%40ss@example.com:443?security=tls&sni=trojan.example#Trojan")
	if err != nil || len(result.Configs) != 1 || !strings.Contains(string(result.Configs[0].Config), `"password":"p@ss"`) {
		t.Fatalf("escaped Trojan password was not preserved: %#v err=%v", result, err)
	}
}

func TestParseVmessTransportSettingsAndRejectsUnsupportedNetwork(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"ps":            "VMess WS",
		"add":           "vmess.example",
		"port":          443,
		"id":            "00000000-0000-0000-0000-000000000001",
		"net":           "ws",
		"tls":           "tls",
		"sni":           "cdn.example",
		"path":          "/vmess",
		"host":          "cdn.example",
		"allowInsecure": "0",
	})
	result, err := (Instance{}).Parse(context.Background(), "vmess://"+base64.RawStdEncoding.EncodeToString(payload))
	if err != nil || len(result.Configs) != 1 {
		t.Fatalf("unexpected VMess transport result: %#v err=%v", result, err)
	}
	if !strings.Contains(string(result.Configs[0].Config), `"network":"ws"`) || !strings.Contains(string(result.Configs[0].Config), `"serverName":"cdn.example"`) {
		t.Fatalf("VMess transport settings were not preserved: %s", result.Configs[0].Config)
	}

	payload, _ = json.Marshal(map[string]any{
		"add":  "vmess.example",
		"port": 443,
		"id":   "00000000-0000-0000-0000-000000000001",
		"net":  "quic",
	})
	result, err = (Instance{}).Parse(context.Background(), "vmess://"+base64.RawStdEncoding.EncodeToString(payload))
	if err != nil || len(result.Configs) != 0 || len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "unsupported transport network") {
		t.Fatalf("unsupported VMess network was accepted: %#v err=%v", result, err)
	}
}
