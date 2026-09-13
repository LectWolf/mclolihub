package codebuddy

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const defaultSystemRole = "system"

// EnsureLeadingSystemMessage moves an existing system message to the front, or
// inserts the default assistant prompt when the Chat Completions body has none.
func EnsureLeadingSystemMessage(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("parse chat completions body: %w", err)
	}
	rawMessages, ok := root["messages"]
	if !ok {
		return body, nil
	}
	var messages []map[string]any
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must be a non-empty array")
	}
	if roleOf(messages[0]) == defaultSystemRole {
		return body, nil
	}
	systemIndex := -1
	for i, message := range messages {
		if roleOf(message) == defaultSystemRole {
			systemIndex = i
			break
		}
	}
	if systemIndex < 0 {
		messages = append([]map[string]any{{
			"role":    defaultSystemRole,
			"content": DefaultSystemMessage,
		}}, messages...)
	} else {
		system := messages[systemIndex]
		rest := append(append([]map[string]any{}, messages[:systemIndex]...), messages[systemIndex+1:]...)
		messages = append([]map[string]any{system}, rest...)
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	root["messages"] = encoded
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(out, body) {
		return body, nil
	}
	return out, nil
}

func roleOf(message map[string]any) string {
	role, _ := message["role"].(string)
	return role
}
