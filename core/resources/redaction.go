package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"meshvianex/core/plugin"
	"strings"
)

func canonicalizeConfig(item Config) Config {
	item.PluginID = plugin.CanonicalID(item.PluginID)
	item.PluginType = plugin.CanonicalType(item.PluginType)
	return item
}

func StableKey(config NormalizedConfig) string {
	config.PluginID = plugin.CanonicalID(config.PluginID)
	config.PluginType = plugin.CanonicalType(config.PluginType)
	h := sha256.New()
	h.Write([]byte(config.PluginID))
	h.Write([]byte{0})
	h.Write([]byte(config.PluginType))
	h.Write([]byte{0})
	h.Write(config.Config)
	return hex.EncodeToString(h.Sum(nil))
}

func RedactConfig(item Config) Config {
	item.Normalized = RedactJSON(item.Normalized)
	item.ValidationError = redactString(item.ValidationError)
	return item
}

// RedactString applies the presentation-only redaction policy to free-form
// diagnostics, activity details, and API errors.
func RedactString(value string) string { return redactString(value) }

// MergeRedactedJSON applies a UI/API edit to a stored JSON value without
// replacing secrets that were represented by the redaction marker. The
// presented value remains authoritative for all non-secret fields.
func MergeRedactedJSON(original, presented json.RawMessage) json.RawMessage {
	var oldValue, newValue any
	if json.Unmarshal(original, &oldValue) != nil || json.Unmarshal(presented, &newValue) != nil {
		return append(json.RawMessage(nil), presented...)
	}
	merged := mergeRedactedValue(oldValue, newValue)
	b, err := json.Marshal(merged)
	if err != nil {
		return append(json.RawMessage(nil), presented...)
	}
	return b
}

func MergeRedactedString(original, presented string) string {
	return mergeRedactedString(original, presented)
}

func mergeRedactedValue(original, presented any) any {
	switch value := presented.(type) {
	case string:
		if value == "[redacted]" {
			return original
		}
		if old, ok := original.(string); ok && containsRedactionMarker(value) {
			return mergeRedactedString(old, value)
		}
		return value
	case map[string]any:
		old, _ := original.(map[string]any)
		for key, child := range value {
			if sensitiveKey(key) && child == "[redacted]" {
				if oldValue, ok := old[key]; ok {
					value[key] = oldValue
				}
				continue
			}
			value[key] = mergeRedactedValue(old[key], child)
		}
		return value
	case []any:
		old, _ := original.([]any)
		for index, child := range value {
			var oldValue any
			if index < len(old) {
				oldValue = old[index]
			}
			value[index] = mergeRedactedValue(oldValue, child)
		}
		return value
	default:
		return presented
	}
}

func mergeRedactedString(original, presented string) string {
	if !containsRedactionMarker(presented) {
		return presented
	}
	originalURL, originalErr := url.Parse(original)
	presentedURL, presentedErr := url.Parse(presented)
	if originalErr != nil || presentedErr != nil || originalURL == nil || presentedURL == nil {
		return presented
	}
	changed := false
	if presentedURL.User != nil {
		username := presentedURL.User.Username()
		if password, ok := presentedURL.User.Password(); ok && password == "[redacted]" {
			if originalPassword, originalOK := originalURL.User.Password(); originalOK {
				presentedURL.User = url.UserPassword(username, originalPassword)
				changed = true
			}
		}
	}
	query := presentedURL.Query()
	originalQuery := originalURL.Query()
	for key := range query {
		if !sensitiveKey(key) {
			continue
		}
		values := query[key]
		redacted := false
		for _, value := range values {
			if value == "[redacted]" {
				redacted = true
				break
			}
		}
		if redacted {
			if oldValues, ok := originalQuery[key]; ok {
				query[key] = append([]string(nil), oldValues...)
				changed = true
			}
		}
	}
	if changed {
		presentedURL.RawQuery = query.Encode()
	}
	return presentedURL.String()
}

func containsRedactionMarker(value string) bool {
	return strings.Contains(value, "[redacted]") || strings.Contains(strings.ToLower(value), "%5bredacted%5d")
}

func RedactSubscription(item Subscription) Subscription {
	item.Source = redactString(item.Source)
	item.LastError = redactString(item.LastError)
	if item.Metadata != nil {
		metadata := make(map[string]string, len(item.Metadata))
		for key, value := range item.Metadata {
			if sensitiveKey(key) {
				metadata[key] = "[redacted]"
			} else {
				metadata[key] = redactString(value)
			}
		}
		item.Metadata = metadata
	}
	return item
}

func RedactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`"[redacted]"`)
	}
	if text, ok := value.(string); ok {
		value = redactString(text)
	} else {
		redactValue(value)
	}
	b, _ := json.Marshal(value)
	return b
}

func redactValue(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if sensitiveKey(key) {
				v[key] = "[redacted]"
				continue
			}
			if text, ok := child.(string); ok {
				v[key] = redactString(text)
			} else {
				redactValue(child)
			}
		}
	case []any:
		for i, child := range v {
			if text, ok := child.(string); ok {
				v[i] = redactString(text)
			} else {
				redactValue(child)
			}
		}
	}
}

func redactString(value string) string {
	u, err := url.Parse(value)
	if redacted, ok := redactURL(u, err); ok {
		return redacted
	}
	// Last errors can contain a URL embedded in diagnostic text. Redact each
	// URL-shaped token while preserving the surrounding error message.
	redacted := value
	for _, scheme := range []string{"http://", "https://", "socks://", "socks5://", "socks5h://"} {
		for offset := 0; ; {
			start := strings.Index(redacted[offset:], scheme)
			if start < 0 {
				break
			}
			start += offset
			end := start
			for end < len(redacted) && !strings.ContainsRune(" \t\r\n\"'<>", rune(redacted[end])) {
				end++
			}
			candidate := strings.TrimRight(redacted[start:end], ",;.)]}")
			parsed, parseErr := url.Parse(candidate)
			if replacement, ok := redactURL(parsed, parseErr); ok {
				redacted = redacted[:start] + replacement + redacted[start+len(candidate):]
				offset = start + len(replacement)
			} else {
				offset = end
			}
		}
	}
	return redacted
}

func redactURL(u *url.URL, err error) (string, bool) {
	if err != nil || u == nil {
		return "", false
	}
	changed := false
	if u.User != nil {
		u = cloneURL(u)
		username := u.User.Username()
		u.User = url.UserPassword(username, "[redacted]")
		changed = true
	}
	if query := u.Query(); len(query) > 0 {
		for key := range query {
			if sensitiveKey(key) {
				query.Set(key, "[redacted]")
				changed = true
			}
		}
		if changed {
			u = cloneURL(u)
			u.RawQuery = query.Encode()
		}
	}
	if !changed {
		return "", false
	}
	return u.String(), true
}

func cloneURL(value *url.URL) *url.URL {
	copy := *value
	return &copy
}

func sensitiveKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "token", "access_token", "api_key", "apikey", "key", "secret", "password", "passwd", "auth", "authorization", "credential", "credentials":
		return true
	default:
		return false
	}
}
