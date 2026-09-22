package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoderproxy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// QoderGatewayService reverse-proxies OpenAI Chat Completions onto Qoder's
// agent SSE endpoint. The account credential is the user's own personal
// access token (pt-...), exchanged for a job token and signed with COSY.
type QoderGatewayService struct {
	tokens *qoderproxy.TokenCache
	client *http.Client
}

func NewQoderGatewayService() *QoderGatewayService {
	return &QoderGatewayService{
		tokens: qoderproxy.NewTokenCache(),
		client: &http.Client{},
	}
}

func shouldUseQoderProxy(account *Account) bool {
	return account != nil && account.IsQoder()
}

func (s *QoderGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	_ *ParsedRequest,
) (*ForwardResult, error) {
	if account == nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "account is required")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		model = "auto"
	}
	stream := gjson.GetBytes(body, "stream").Bool()
	messages, err := parseQoderChatMessages(body)
	if err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	if len(messages) == 0 {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "messages required")
	}
	pat := qoderPersonalToken(account)
	if pat == "" {
		return nil, s.writeError(c, http.StatusUnauthorized, "authentication_error", "qoder personal token is required")
	}

	identity, err := s.tokens.Resolve(ctx, s.client, pat, strings.TrimSpace(account.GetCredential("machine_id")))
	if err != nil {
		return nil, s.writeError(c, http.StatusUnauthorized, "authentication_error", err.Error())
	}
	maxTokens := int(gjson.GetBytes(body, "max_tokens").Int())
	if maxTokens <= 0 {
		maxTokens = int(gjson.GetBytes(body, "max_completion_tokens").Int())
	}
	thinking := strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	toolsNote := ""
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && tools.Raw != "null" && tools.Raw != "[]" {
		toolsNote = "Available tools (call them by name when needed):\n" + tools.Raw
	}
	payload, err := qoderproxy.BuildChatBody(qoderproxy.ChatInput{
		Model:            model,
		UserID:           identity.UserID,
		Messages:         messages,
		MaxTokens:        maxTokens,
		ThinkingEffort:   thinking,
		ToolsNote:        toolsNote,
		RequestID:        uuid.NewString(),
		BusinessID:       uuid.NewString(),
		BeginAtUnixMilli: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	resp, err := qoderproxy.OpenChat(ctx, s.client, identity, model, payload)
	if err != nil {
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.tokens.Invalidate(pat)
	}
	if resp.StatusCode != http.StatusOK {
		raw := qoderproxy.ReadErrorBody(resp.Body)
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", fmt.Sprintf("qoder HTTP %d: %s", resp.StatusCode, truncateRunes(raw, 240)))
	}
	return s.pump(c, resp.Body, model, stream)
}

func (s *QoderGatewayService) pump(c *gin.Context, body io.Reader, model string, stream bool) (*ForwardResult, error) {
	chatID := "chatcmpl-" + uuid.NewString()[:24]
	created := time.Now().Unix()
	var text strings.Builder
	var reasoning strings.Builder
	tools := map[int]*qoderTool{}
	var toolOrder []int
	var lastErr string
	sentRole := false

	emit := func(delta gin.H, finish string) {
		if !stream {
			return
		}
		choice := gin.H{"index": 0, "delta": delta}
		if finish != "" {
			choice = gin.H{"index": 0, "delta": gin.H{}, "finish_reason": finish}
		}
		raw, _ := json.Marshal(gin.H{
			"id":      chatID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []gin.H{choice},
		})
		_, _ = c.Writer.Write([]byte("data: " + string(raw) + "\n\n"))
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	if stream {
		MarkResponseCommitted(c)
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Status(http.StatusOK)
	}

	err := qoderproxy.ScanChat(body, func(delta qoderproxy.Delta) error {
		if delta.Err != nil && lastErr == "" {
			lastErr = delta.Err.Error()
		}
		if delta.Content != "" {
			text.WriteString(delta.Content)
			chunk := gin.H{"content": delta.Content}
			if !sentRole {
				chunk["role"] = "assistant"
				sentRole = true
			}
			emit(chunk, "")
		}
		if delta.Reasoning != "" {
			reasoning.WriteString(delta.Reasoning)
			emit(gin.H{"reasoning_content": delta.Reasoning}, "")
		}
		for _, call := range delta.ToolCalls {
			acc, ok := tools[call.Index]
			if !ok {
				acc = &qoderTool{Index: call.Index}
				tools[call.Index] = acc
				toolOrder = append(toolOrder, call.Index)
			}
			if call.ID != "" {
				acc.ID = call.ID
			}
			if call.Name != "" {
				acc.Name = call.Name
			}
			acc.Arguments += call.Arguments
			emit(gin.H{"tool_calls": []gin.H{{
				"index": call.Index,
				"id":    call.ID,
				"type":  "function",
				"function": gin.H{
					"name":      call.Name,
					"arguments": call.Arguments,
				},
			}}}, "")
		}
		return nil
	})
	if err != nil && lastErr == "" {
		lastErr = err.Error()
	}
	if lastErr != "" && text.Len() == 0 && reasoning.Len() == 0 && len(tools) == 0 {
		if stream {
			raw, _ := json.Marshal(gin.H{"error": gin.H{"message": lastErr, "type": "api_error"}})
			_, _ = c.Writer.Write([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return &ForwardResult{Model: model, Stream: true}, errors.New(lastErr)
		}
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", lastErr)
	}

	finish := "stop"
	if len(tools) > 0 {
		finish = "tool_calls"
	}
	if stream {
		emit(nil, finish)
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return &ForwardResult{Model: model, Stream: true}, nil
	}

	message := gin.H{"role": "assistant", "content": text.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(tools) > 0 {
		message["tool_calls"] = qoderToolList(toolOrder, tools)
	}
	MarkResponseCommitted(c)
	c.JSON(http.StatusOK, gin.H{
		"id":      chatID,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []gin.H{{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": gin.H{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
	return &ForwardResult{Model: model, Stream: false}, nil
}

type qoderTool struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

func qoderToolList(order []int, tools map[int]*qoderTool) []gin.H {
	out := make([]gin.H, 0, len(order))
	for _, idx := range order {
		call := tools[idx]
		out = append(out, gin.H{
			"id":   call.ID,
			"type": "function",
			"function": gin.H{
				"name":      call.Name,
				"arguments": call.Arguments,
			},
		})
	}
	return out
}

func (s *QoderGatewayService) writeError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    nil,
		},
	})
	return errors.New(message)
}

func qoderPersonalToken(account *Account) string {
	for _, key := range []string{"personal_token", "api_key", "pat"} {
		if token := strings.TrimSpace(account.GetCredential(key)); token != "" {
			return token
		}
	}
	return ""
}

func parseQoderChatMessages(body []byte) ([]qoderproxy.Message, error) {
	arr := gjson.GetBytes(body, "messages")
	if !arr.IsArray() {
		return nil, errors.New("messages must be an array")
	}
	out := make([]qoderproxy.Message, 0)
	arr.ForEach(func(_, value gjson.Result) bool {
		role := value.Get("role").String()
		content := flattenQoderContent(value.Get("content"))
		if content == "" {
			if calls := value.Get("tool_calls"); calls.IsArray() {
				var names []string
				calls.ForEach(func(_, call gjson.Result) bool {
					name := call.Get("function.name").String()
					if name != "" {
						names = append(names, name)
					}
					return true
				})
				if len(names) > 0 {
					content = "tool_calls: " + strings.Join(names, ", ")
				}
			}
		}
		if role == "" && content == "" {
			return true
		}
		out = append(out, qoderproxy.Message{Role: role, Content: content})
		return true
	})
	return out, nil
}

func flattenQoderContent(content gjson.Result) string {
	if !content.Exists() || content.Type == gjson.Null {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return content.Raw
	}
	var b strings.Builder
	content.ForEach(func(_, part gjson.Result) bool {
		if part.Type == gjson.String {
			b.WriteString(part.String())
			return true
		}
		text := part.Get("text").String()
		if text == "" {
			text = part.Get("content").String()
		}
		b.WriteString(text)
		return true
	})
	return b.String()
}
