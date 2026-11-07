// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// anthropicVersion is the Messages API version the package speaks.
const anthropicVersion = "2023-06-01"

// anthropic is the Anthropic Messages API (POST /v1/messages, streamed;
// GET /v1/models): x-api-key, anthropic-version, text and tool_use
// content blocks, tool results as tool_result blocks of a user message.
// A JSON schema is asked for in the system prompt (no structured-output
// feature of the API is used; the caller validates).
type anthropic struct {
	*client
}

func (a *anthropic) headers() map[string]string {
	return map[string]string{
		"x-api-key":         a.cfg.Key,
		"anthropic-version": anthropicVersion,
	}
}

// Wire shapes of the request.
type (
	anthropicRequest struct {
		Model     string             `json:"model"`
		MaxTokens int                `json:"max_tokens"`
		System    string             `json:"system,omitempty"`
		Messages  []anthropicMessage `json:"messages"`
		Tools     []anthropicTool    `json:"tools,omitempty"`
		Stream    bool               `json:"stream"`
	}
	anthropicMessage struct {
		Role    string           `json:"role"`
		Content []anthropicBlock `json:"content"`
	}
	anthropicBlock struct {
		Type      string          `json:"type"`
		Text      string          `json:"text,omitempty"`
		ID        string          `json:"id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Input     json.RawMessage `json:"input,omitempty"`
		ToolUseID string          `json:"tool_use_id,omitempty"`
		Content   *string         `json:"content,omitempty"`
		IsError   bool            `json:"is_error,omitempty"`
	}
	anthropicTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
)

// body builds the request body of req.
func (a *anthropic) body(req Request) ([]byte, error) {
	w := anthropicRequest{
		Model:     req.Model,
		MaxTokens: maxOutputTokens(req),
		System:    req.System,
		Stream:    true,
	}
	if len(req.JSONSchema) > 0 {
		w.System = schemaInstruction(req.System, req.JSONSchema)
	}
	for _, m := range req.Messages {
		var blocks []anthropicBlock
		for _, b := range m.Blocks {
			switch b.Kind {
			case BlockText:
				if b.Text == "" {
					continue // the API refuses an empty text block
				}
				blocks = append(blocks, anthropicBlock{Type: "text", Text: b.Text})
			case BlockToolUse:
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: schemaOr(b.Input, "{}")})
			case BlockToolResult:
				text := b.Text
				blocks = append(blocks, anthropicBlock{Type: "tool_result", ToolUseID: b.ID, Content: &text, IsError: b.IsError})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		w.Messages = append(w.Messages, anthropicMessage{Role: string(m.Role), Content: blocks})
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: toolSchema(t.InputSchema)})
	}
	return encodeJSON(w)
}

// classify maps an HTTP error of the Messages API: the body's error
// type refines the status.
func (a *anthropic) classify(resp *http.Response, body []byte) *Error {
	typ, msg := providerError(body)
	e := httpError(resp, typ, msg)
	if e.Class == api.FailureInvalidRequest {
		if c, ok := anthropicClass(typ); ok {
			e.Class = c
		}
	}
	return e
}

// anthropicClass maps the API's error types; false for one it does not
// know.
func anthropicClass(typ string) (api.AssistantFailureClass, bool) {
	switch typ {
	case "overloaded_error":
		return api.FailureOverloaded, true
	case "rate_limit_error":
		return api.FailureRateLimit, true
	case "authentication_error":
		return api.FailureAuth, true
	case "permission_error":
		return api.FailurePermission, true
	case "billing_error":
		return api.FailureQuota, true
	case "invalid_request_error", "not_found_error", "request_too_large":
		return api.FailureInvalidRequest, true
	}
	return "", false
}

// Stream implements Provider.
func (a *anthropic) Stream(ctx context.Context, req Request, emit func(Event) error) error {
	body, err := a.body(req)
	if err != nil {
		return err
	}
	resp, err := a.send(ctx, func() (*http.Request, error) {
		return a.newRequest(ctx, http.MethodPost, "/v1/messages", body, "text/event-stream", a.headers())
	}, a.classify)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if mediaType(resp) != "text/event-stream" {
		return protocolError("the provider did not answer with an event stream")
	}
	st := &anthropicStream{emit: emit, blocks: map[int]*anthropicBlockState{}}
	err = readSSE(resp.Body, st.event)
	switch {
	case err != nil:
		return readError(ctx, err)
	case !st.stopped:
		return streamEnded()
	}
	return nil
}

// anthropicStream assembles the events of one streamed message.
type anthropicStream struct {
	emit    func(Event) error
	blocks  map[int]*anthropicBlockState
	usage   api.AssistantUsage
	stop    StopReason
	stopped bool
}

type anthropicBlockState struct {
	kind     string
	id, name string
	text     strings.Builder
	args     strings.Builder
}

// anthropicEvent is the union of the stream's data objects.
type anthropicEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// event handles one server-sent event.
func (s *anthropicStream) event(name, data string) error {
	if s.stopped {
		return nil
	}
	var ev anthropicEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return protocolError("an event of the stream is not JSON")
	}
	typ := ev.Type
	if typ == "" {
		typ = name
	}
	switch typ {
	case "message_start":
		s.usage = api.AssistantUsage{
			InputTokens:              ev.Message.Usage.InputTokens,
			OutputTokens:             ev.Message.Usage.OutputTokens,
			CacheCreationInputTokens: ev.Message.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     ev.Message.Usage.CacheReadInputTokens,
		}
	case "content_block_start":
		b := &anthropicBlockState{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		if b.kind == "text" && ev.ContentBlock.Text != "" {
			b.text.WriteString(ev.ContentBlock.Text)
			if err := s.emit(Event{Kind: EventTextDelta, Text: ev.ContentBlock.Text}); err != nil {
				return err
			}
		}
		s.blocks[ev.Index] = b
	case "content_block_delta":
		b := s.blocks[ev.Index]
		if b == nil {
			return protocolError("a delta of the stream names no block")
		}
		switch ev.Delta.Type {
		case "text_delta":
			if b.kind != "text" {
				return nil
			}
			b.text.WriteString(ev.Delta.Text)
			if ev.Delta.Text != "" {
				return s.emit(Event{Kind: EventTextDelta, Text: ev.Delta.Text})
			}
		case "input_json_delta":
			if b.kind == "tool_use" {
				b.args.WriteString(ev.Delta.PartialJSON)
			}
		}
	case "content_block_stop":
		b := s.blocks[ev.Index]
		if b == nil {
			return nil
		}
		delete(s.blocks, ev.Index)
		switch b.kind {
		case "text":
			return s.emit(Event{Kind: EventTextDone, Text: b.text.String()})
		case "tool_use":
			return s.emit(parseToolInput(b.id, b.name, b.args.String()))
		}
	case "message_delta":
		if ev.Delta.StopReason != "" {
			s.stop = anthropicStop(ev.Delta.StopReason)
		}
		u := s.usage
		u.OutputTokens = ev.Usage.OutputTokens
		if ev.Usage.InputTokens != 0 {
			u.InputTokens = ev.Usage.InputTokens
		}
		if ev.Usage.CacheCreationInputTokens != 0 {
			u.CacheCreationInputTokens = ev.Usage.CacheCreationInputTokens
		}
		if ev.Usage.CacheReadInputTokens != 0 {
			u.CacheReadInputTokens = ev.Usage.CacheReadInputTokens
		}
		s.usage = u
		return s.emit(Event{Kind: EventUsage, Usage: &u})
	case "message_stop":
		s.stopped = true
		stop := s.stop
		if stop == "" {
			stop = StopOther
		}
		return s.emit(Event{Kind: EventStop, Stop: stop})
	case "error":
		c, ok := anthropicClass(ev.Error.Type)
		switch {
		case ok && c == api.FailureInvalidRequest && mentionsQuota(ev.Error.Message):
			c = api.FailureQuota
		case !ok:
			c = api.FailureProtocol
		}
		return &Error{Class: c, Code: cleanCode(ev.Error.Type), Message: cleanMessage(ev.Error.Message)}
	case "ping":
	}
	return nil
}

// anthropicStop normalises the API's stop reasons.
func anthropicStop(r string) StopReason {
	switch r {
	case "end_turn", "stop_sequence":
		return StopEndTurn
	case "tool_use":
		return StopToolUse
	case "max_tokens":
		return StopMaxTokens
	case "refusal":
		return StopRefusal
	}
	return StopOther
}

// Models implements Provider: GET /v1/models?limit=100, following
// after_id while has_more, at most maxModelPages pages or MaxModels
// models.
func (a *anthropic) Models(ctx context.Context) ([]api.AIModel, error) {
	ctx, cancel := withModelsTimeout(ctx)
	defer cancel()
	var out []api.AIModel
	after := ""
	for page := 0; page < maxModelPages; page++ {
		q := url.Values{"limit": {"100"}}
		if after != "" {
			q.Set("after_id", after)
		}
		path := "/v1/models?" + q.Encode()
		resp, err := a.send(ctx, func() (*http.Request, error) {
			return a.newRequest(ctx, http.MethodGet, path, nil, "application/json", a.headers())
		}, a.classify)
		if err != nil {
			return nil, err
		}
		body, err := boundedBody(resp.Body, maxModelsBytes)
		resp.Body.Close()
		if err != nil {
			return nil, readError(ctx, err)
		}
		var list struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &list); err != nil || list.Data == nil {
			return nil, protocolError("the list of models could not be read")
		}
		for _, m := range list.Data {
			if cm, ok := cleanModel(m.ID, m.DisplayName); ok {
				out = append(out, cm)
				if len(out) >= MaxModels {
					return out, nil
				}
			}
		}
		if !list.HasMore || list.LastID == "" {
			break
		}
		after = list.LastID
	}
	return out, nil
}
