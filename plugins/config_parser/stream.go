package config_parser

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func streamSettings(values url.Values, network, security string) (map[string]any, error) {
	network = strings.ToLower(strings.TrimSpace(network))
	if network == "" {
		network = "tcp"
	}
	if network == "h2" {
		network = "http"
	}
	if network != "tcp" && network != "ws" && network != "grpc" && network != "http" {
		return nil, fmt.Errorf("unsupported transport network %q; supported networks are tcp, ws, grpc, and http/h2", network)
	}
	security = strings.ToLower(strings.TrimSpace(security))
	if security == "none" {
		security = ""
	}
	settings := map[string]any{"network": network}
	if security != "" {
		switch security {
		case "tls":
			tls, err := tlsSettings(values, false)
			if err != nil {
				return nil, err
			}
			settings["security"], settings["tlsSettings"] = security, tls
		case "reality":
			tls, err := tlsSettings(values, true)
			if err != nil {
				return nil, err
			}
			settings["security"], settings["realitySettings"] = security, tls
		default:
			return nil, fmt.Errorf("unsupported stream security %q; supported security is tls, reality, or none", security)
		}
	}
	switch network {
	case "ws":
		ws := map[string]any{"path": values.Get("path")}
		if host := values.Get("host"); host != "" {
			ws["headers"] = map[string]string{"Host": host}
		}
		settings["wsSettings"] = ws
	case "grpc":
		grpc := map[string]any{"serviceName": values.Get("serviceName")}
		if mode := values.Get("mode"); mode != "" {
			grpc["multiMode"] = strings.EqualFold(mode, "multi")
		}
		settings["grpcSettings"] = grpc
	case "http":
		httpSettings := map[string]any{"path": values.Get("path")}
		if host := values.Get("host"); host != "" {
			httpSettings["host"] = strings.Split(host, ",")
		}
		settings["httpSettings"] = httpSettings
	}
	return settings, nil
}

func tlsSettings(values url.Values, reality bool) (map[string]any, error) {
	settings := map[string]any{}
	if serverName := firstQuery(values, "sni", "serverName"); serverName != "" {
		settings["serverName"] = serverName
	}
	if fingerprint := values.Get("fp"); fingerprint != "" {
		settings["fingerprint"] = fingerprint
	}
	if alpn := values.Get("alpn"); alpn != "" {
		settings["alpn"] = splitCSV(alpn)
	}
	if raw := firstQuery(values, "allowInsecure", "insecure"); raw != "" {
		parsed, err := parseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid allowInsecure value %q", raw)
		}
		settings["allowInsecure"] = parsed
	}
	if reality {
		settings["publicKey"] = values.Get("pbk")
		settings["shortId"] = values.Get("sid")
		settings["spiderX"] = values.Get("spx")
	}
	return settings, nil
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return strconv.ParseBool(value)
	}
}

func firstQuery(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
