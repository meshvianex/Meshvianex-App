package config_parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"meshvianex/core/resources"
	"strings"
)

func decodeBase64(content string) (string, bool) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, content)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(compact)
		if err != nil || len(decoded) == 0 {
			continue
		}
		text := string(decoded)
		trimmed := strings.TrimSpace(text)
		if strings.Contains(text, "://") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return text, true
		}
	}
	return "", false
}

func parseURI(value string) (resources.NormalizedConfig, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return resources.NormalizedConfig{}, fmt.Errorf("invalid URI: %v", err)
	}
	name := uriName(u)
	switch strings.ToLower(u.Scheme) {
	case "direct":
		if strings.TrimSpace(value) != "direct://" && strings.TrimSpace(value) != "direct:" {
			return resources.NormalizedConfig{}, fmt.Errorf("direct URI must be direct://")
		}
		return normalized(DirectOutbound, map[string]any{}, name), nil
	case "http", "https":
		if u.Hostname() == "" || !validPort(u.Port()) {
			return resources.NormalizedConfig{}, fmt.Errorf("%s proxy URI requires host and port", u.Scheme)
		}
		return normalized(HTTPOutbound, map[string]any{"url": value, "timeout_seconds": 15}, name), nil
	case "socks", "socks5", "socks5h":
		if u.Hostname() == "" || !validPort(u.Port()) {
			return resources.NormalizedConfig{}, fmt.Errorf("%s proxy URI requires host and port", u.Scheme)
		}
		return normalized(SOCKSOutbound, map[string]any{"url": value, "timeout_seconds": 15}, name), nil
	case "vmess":
		return parseVmess(u, name)
	case "vless":
		return parseVless(u, name)
	case "trojan":
		return parseTrojan(u, name)
	case "ss":
		return parseShadowsocks(u, name)
	default:
		return resources.NormalizedConfig{}, fmt.Errorf("unsupported URI scheme %q", u.Scheme)
	}
}

func parseVmess(u *url.URL, name string) (resources.NormalizedConfig, error) {
	payload, ok := decodeSharePayload(u.Host + u.Path)
	if !ok {
		return resources.NormalizedConfig{}, fmt.Errorf("vmess URI payload is not valid base64 JSON")
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return resources.NormalizedConfig{}, fmt.Errorf("vmess payload: %w", err)
	}
	if name == "" {
		name = jsonDisplayName(rawMessage(data))
	}
	address := stringValue(data, "add", "address")
	port := numberValue(data, "port")
	if address == "" || port < 1 || port > 65535 || stringValue(data, "id", "uuid") == "" {
		return resources.NormalizedConfig{}, fmt.Errorf("vmess payload requires address, port, and id")
	}
	streamValues := url.Values{}
	for _, key := range []string{"net", "network", "type", "tls", "security", "sni", "fp", "alpn", "allowInsecure", "path", "host", "serviceName", "mode", "pbk", "sid", "spx"} {
		if value := stringValue(data, key); value != "" {
			streamValues.Set(key, value)
		}
	}
	security := streamValues.Get("tls")
	if security == "" {
		security = streamValues.Get("security")
	}
	stream, err := streamSettings(streamValues, firstQuery(streamValues, "net", "network", "type"), security)
	if err != nil {
		return resources.NormalizedConfig{}, fmt.Errorf("vmess payload: %w", err)
	}
	outbound := map[string]any{"protocol": "vmess", "settings": map[string]any{"vnext": []any{map[string]any{"address": address, "port": port, "users": []any{map[string]any{"id": stringValue(data, "id", "uuid"), "alterId": numberValue(data, "aid"), "security": stringValue(data, "scy", "security")}}}}}}
	return xrayNormalized(name, outbound, stream), nil
}

func parseVless(u *url.URL, name string) (resources.NormalizedConfig, error) {
	if u.Hostname() == "" || !validPort(u.Port()) || u.User == nil || u.User.Username() == "" {
		return resources.NormalizedConfig{}, fmt.Errorf("vless URI requires user, host, and port")
	}
	if name == "" {
		name = u.Query().Get("remark")
	}
	encryption := u.Query().Get("encryption")
	if encryption == "" {
		encryption = "none"
	}
	user := map[string]any{"id": u.User.Username(), "encryption": encryption}
	if flow := u.Query().Get("flow"); flow != "" {
		user["flow"] = flow
	}
	server := map[string]any{"address": u.Hostname(), "port": portNumber(u.Port()), "users": []any{user}}
	outbound := map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{server}}}
	stream, err := streamSettings(u.Query(), u.Query().Get("type"), u.Query().Get("security"))
	if err != nil {
		return resources.NormalizedConfig{}, fmt.Errorf("vless URI: %w", err)
	}
	return xrayNormalized(name, outbound, stream), nil
}

func parseTrojan(u *url.URL, name string) (resources.NormalizedConfig, error) {
	if u.Hostname() == "" || !validPort(u.Port()) || u.User == nil || u.User.Username() == "" {
		return resources.NormalizedConfig{}, fmt.Errorf("trojan URI requires password, host, and port")
	}
	if name == "" {
		name = u.Query().Get("remark")
	}
	server := map[string]any{"address": u.Hostname(), "port": portNumber(u.Port()), "password": u.User.Username()}
	security := u.Query().Get("security")
	if security == "" {
		security = "tls"
	}
	stream, err := streamSettings(u.Query(), u.Query().Get("type"), security)
	if err != nil {
		return resources.NormalizedConfig{}, fmt.Errorf("trojan URI: %w", err)
	}
	return xrayNormalized(name, map[string]any{"protocol": "trojan", "settings": map[string]any{"servers": []any{server}}}, stream), nil
}

func parseShadowsocks(u *url.URL, name string) (resources.NormalizedConfig, error) {
	if u.User == nil && u.Port() == "" {
		if payload, ok := decodeSharePayload(u.Host); ok {
			nested, err := url.Parse("ss://" + payload)
			if err != nil {
				return resources.NormalizedConfig{}, fmt.Errorf("shadowsocks URI payload: %w", err)
			}
			return parseShadowsocks(nested, name)
		}
	}
	if u.Hostname() == "" || !validPort(u.Port()) || u.User == nil {
		return resources.NormalizedConfig{}, fmt.Errorf("shadowsocks URI requires credentials, host, and port")
	}
	method, password := u.User.Username(), ""
	if parsedPassword, ok := u.User.Password(); ok {
		password = parsedPassword
	}
	credentials := method
	if decoded, ok := decodeSharePayload(credentials); ok && strings.Contains(decoded, ":") {
		method, password, _ = strings.Cut(decoded, ":")
	}
	if password == "" {
		method, password, _ = strings.Cut(credentials, ":")
	}
	if method == "" || password == "" {
		return resources.NormalizedConfig{}, fmt.Errorf("shadowsocks URI requires method and password")
	}
	if name == "" {
		name = u.Query().Get("remark")
	}
	server := map[string]any{"address": u.Hostname(), "port": portNumber(u.Port()), "method": method, "password": password}
	return xrayNormalized(name, map[string]any{"protocol": "shadowsocks", "settings": map[string]any{"servers": []any{server}}}, nil), nil
}

func xrayNormalized(name string, outbound map[string]any, stream map[string]any) resources.NormalizedConfig {
	if len(stream) > 0 {
		outbound["streamSettings"] = stream
	}
	config, _ := json.Marshal(map[string]any{"inline_config": map[string]any{"outbounds": []any{outbound}}})
	return resources.NormalizedConfig{Name: strings.TrimSpace(name), PluginID: XrayOutbound, PluginType: "outbound", Config: config}
}

func normalized(pluginID string, value any, name string) resources.NormalizedConfig {
	b, _ := json.Marshal(value)
	return resources.NormalizedConfig{Name: strings.TrimSpace(name), PluginID: pluginID, PluginType: "outbound", Config: b}
}
