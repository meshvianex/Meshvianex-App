package config_parser

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

func uriName(u *url.URL) string {
	if value := strings.TrimSpace(u.Fragment); value != "" {
		if decoded, err := url.PathUnescape(value); err == nil {
			return strings.TrimSpace(decoded)
		}
		return value
	}
	return strings.TrimSpace(u.Query().Get("remark"))
}

func decodeSharePayload(value string) (string, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "/"))
	if decoded, ok := decodeBase64(value); ok {
		return decoded, true
	}
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil && strings.Contains(string(decoded), ":") {
			return string(decoded), true
		}
	}
	return "", false
}

func jsonDisplayName(raw json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, key := range []string{"name", "remark", "ps", "tag"} {
		if name, ok := value[key].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	if outbounds, ok := value["outbounds"].([]any); ok {
		for _, item := range outbounds {
			if entry, ok := item.(map[string]any); ok {
				if name, ok := entry["tag"].(string); ok && strings.TrimSpace(name) != "" {
					return strings.TrimSpace(name)
				}
			}
		}
	}
	return ""
}

func rawMessage(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func stringValue(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func numberValue(value map[string]any, key string) int {
	switch raw := value[key].(type) {
	case float64:
		return int(raw)
	case string:
		return portNumber(raw)
	default:
		return 0
	}
}

func portNumber(value string) int {
	port, _ := strconv.Atoi(strings.TrimSpace(value))
	return port
}

func validPort(value string) bool {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && port >= 1 && port <= 65535
}
