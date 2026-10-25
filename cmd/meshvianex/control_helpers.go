package main

import (
	"encoding/json"
	"fmt"
)

func decodePayload(payload json.RawMessage, target any) error {
	if len(payload) == 0 {
		return fmt.Errorf("payload is required")
	}
	var encoded string
	if len(payload) > 0 && payload[0] == '"' {
		if err := json.Unmarshal(payload, &encoded); err != nil {
			return err
		}
		payload = json.RawMessage(encoded)
	}
	return json.Unmarshal(payload, target)
}

func normalizeRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		if candidate := json.RawMessage(encoded); json.Valid(candidate) {
			return candidate
		}
	}
	return raw
}
