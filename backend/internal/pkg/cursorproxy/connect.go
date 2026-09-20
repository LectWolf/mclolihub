// Package cursorproxy talks to Cursor api2 using the Connect+JSON protocol.
//
// Two distinct upstreams are supported:
//   - Sand Stream: aiserver.v1.InferenceService/Stream, billed as Grok Bot / sand quota.
//     Auth is grokBotToken minted from SAND_INFERENCE_RENEWAL_CREDENTIAL.
//   - Cursor IDE: aiserver.v1.InferenceService/RunInference, billed as Cursor IDE usage.
//     Auth is a session JWT from Cursor CLI login (loginDeepControl); client-type=ide.
package cursorproxy

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	BackendHost = "api2.cursor.sh"
	BackendURL  = "https://api2.cursor.sh"

	StreamPath       = "/aiserver.v1.InferenceService/Stream"
	RunInferencePath = "/aiserver.v1.InferenceService/RunInference"
	RenewPath        = "/sand-box/inference-credential"
	CLILoginPage     = "https://www.cursor.com/loginDeepControl"
	CLILoginPollPath = "/auth/poll"

	SandClientType    = "sand"
	SandClientSource  = "sand-desktop"
	SandClientVersion = "0.53.0"
	SandNamespace     = "prod"

	IDEClientType    = "ide"
	IDEClientVersion = "3.21.12"

	connectContentType = "application/connect+json"
)

// Envelope encodes a Connect+JSON frame (flags + big-endian length + JSON).
func Envelope(obj any) ([]byte, error) {
	msg, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 5+len(msg))
	out[0] = 0
	binary.BigEndian.PutUint32(out[1:5], uint32(len(msg)))
	copy(out[5:], msg)
	return out, nil
}

// DecodeFrames parses Connect+JSON frames from buf and returns leftover bytes.
func DecodeFrames(buf []byte) (frames []map[string]any, rest []byte, err error) {
	i := 0
	for i+5 <= len(buf) {
		length := int(binary.BigEndian.Uint32(buf[i+1 : i+5]))
		if i+5+length > len(buf) {
			break
		}
		chunk := buf[i+5 : i+5+length]
		i += 5 + length
		if len(chunk) == 0 {
			continue
		}
		var obj map[string]any
		if uerr := json.Unmarshal(chunk, &obj); uerr != nil {
			obj = map[string]any{"_raw": string(chunk[:min(len(chunk), 200)])}
		}
		frames = append(frames, obj)
	}
	return frames, buf[i:], nil
}

func EncodeFrames(frames []map[string]any) ([]byte, error) {
	var out []byte
	for _, frame := range frames {
		env, err := Envelope(frame)
		if err != nil {
			return nil, err
		}
		out = append(out, env...)
	}
	return out, nil
}

// ReadAllFrames drains r and decodes every complete Connect frame.
func ReadAllFrames(r io.Reader) ([]map[string]any, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	frames, _, err := DecodeFrames(raw)
	return frames, err
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func firstMap(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if nested, ok := v.(map[string]any); ok {
				return nested
			}
		}
	}
	return nil
}

func extractConnectError(frame map[string]any) (code, message string) {
	if frame == nil {
		return "", ""
	}
	if s, ok := frame["code"].(string); ok && s != "" {
		code = s
	}
	if s, ok := frame["message"].(string); ok && s != "" {
		message = s
	}
	errObj, _ := frame["error"].(map[string]any)
	if errObj == nil {
		return code, message
	}
	if s, ok := errObj["code"].(string); ok && s != "" {
		code = s
	}
	if s, ok := errObj["message"].(string); ok && s != "" {
		message = s
	}
	details, _ := errObj["details"].([]any)
	if len(details) == 0 {
		return code, message
	}
	d0, _ := details[0].(map[string]any)
	if d0 == nil {
		return code, message
	}
	debug, _ := d0["debug"].(map[string]any)
	if debug == nil {
		return code, message
	}
	if s, ok := debug["error"].(string); ok && s != "" {
		code = s
	}
	inner, _ := debug["details"].(map[string]any)
	if inner != nil {
		if s, ok := inner["detail"].(string); ok && s != "" {
			message = s
		} else if s, ok := inner["title"].(string); ok && s != "" {
			message = s
		}
	}
	return code, message
}

func walkText(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x["text"].(string); ok && s != "" {
			return s
		}
		if s, ok := x["delta"].(string); ok && s != "" {
			return s
		}
		var out string
		for _, child := range x {
			out += walkText(child)
		}
		return out
	case []any:
		var out string
		for _, child := range x {
			out += walkText(child)
		}
		return out
	default:
		return ""
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func requireNonEmpty(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
