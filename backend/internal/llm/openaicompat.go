// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// openaiCompat is the chat completions interface of self-hosted servers
// and gateways (POST /chat/completions under the "/v1" base URL, GET
// /models): Authorization: Bearer only with a key, the system prompt as
// the first message, tools as function tools, tool results as tool
// messages, stream_options.include_usage, a JSON schema as
// response_format — with one retry without it, asking in the prompt
// instead, when the server refuses the parameter. A server that answers
// a plain JSON completion instead of a stream is read too.
type openaiCompat struct {
	*client
}

func (o *openaiCompat) headers() map[string]string {
	if o.cfg.Key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + o.cfg.Key}
}

// Wire shapes of the request.
type (
	compatRequest struct {
		Model          string               `json:"model"`
		Messages       []compatMessage      `json:"messages"`
		Tools          []compatTool         `json:"tools,omitempty"`
		Stream         bool                 `json:"stream"`
		StreamOptions  *compatStreamOptions `json:"stream_options,omitempty"`
		MaxTokens      int                  `json:"max_tokens"`
		ResponseFormat *compatResponseFmt   `json:"response_format,omitempty"`
	}
	compatMessage struct {
		Role       string           `json:"role"`
		Content    *string          `json:"content"`
		ToolCalls  []compatToolCall `json:"tool_calls,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
	}
	compatToolCall struct {
		ID       string         `json:"id"`
		Type     string         `json:"type"`
		Function compatFunction `json:"function"`
	}
	compatFunction struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	compatTool struct {
		Type     string            `json:"type"`
		Function compatFunctionDef `json:"function"`
	}
	compatFunctionDef struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	compatStreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	compatResponseFmt struct {
		Type       string           `json:"type"`
		JSONSchema compatJSONSchema `json:"json_schema"`
	}
	compatJSONSchema struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	}
)

// body builds the request body of req; withFormat sends the JSON schema
// as response_format, else as an instruction of the system prompt.
func (o *openaiCompat) body(req Request, withFormat bool) ([]byte, error) {
	w := compatRequest{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: &compatStreamOptions{IncludeUsage: true},
		MaxTokens:     maxOutputTokens(req),
	}
	system := req.System
	if len(req.JSONSchema) > 0 {
		if withFormat {
			w.ResponseFormat = &compatResponseFmt{Type: "json_schema", JSONSchema: compatJSONSchema{Name: "answer", Schema: req.JSONSchema, Strict: true}}
		} else {
			system = schemaInstruction(system, req.JSONSchema)
		}
	}
	if system != "" {
		s := system
		w.Messages = append(w.Messages, compatMessage{Role: "system", Content: &s})
	}
	for _, m := range req.Messages {
		if m.Role == RoleAssistant {
			var text strings.Builder
			var calls []compatToolCall
			for _, b := range m.Blocks {
				switch b.Kind {
				case BlockText:
					text.WriteString(b.Text)
				case BlockToolUse:
					calls = append(calls, compatToolCall{ID: b.ID, Type: "function", Function: compatFunction{Name: b.Name, Arguments: string(schemaOr(b.Input, "{}"))}})
				}
			}
			if text.Len() == 0 && len(calls) == 0 {
				continue
			}
			t := text.String()
			w.Messages = append(w.Messages, compatMessage{Role: "assistant", Content: &t, ToolCalls: calls})
			continue
		}
		for _, b := range m.Blocks {
			switch b.Kind {
			case BlockText:
				if b.Text == "" {
					continue
				}
				t := b.Text
				w.Messages = append(w.Messages, compatMessage{Role: "user", Content: &t})
			case BlockToolResult:
				out := b.Text
				if b.IsError {
					out = "ERROR: " + out
				}
				w.Messages = append(w.Messages, compatMessage{Role: "tool", ToolCallID: b.ID, Content: &out})
			}
		}
	}
	if w.Messages == nil {
		w.Messages = []compatMessage{}
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, compatTool{Type: "function", Function: compatFunctionDef{Name: t.Name, Description: t.Description, Parameters: toolSchema(t.InputSchema)}})
	}
	return encodeJSON(w)
}

// classify maps an HTTP error of a compatible server.
func (o *openaiCompat) classify(resp *http.Response, body []byte) *Error {
	typ, msg := providerError(body)
	e := httpError(resp, typ, msg)
	if e.Class == api.FailureInvalidRequest || e.Class == api.FailureRateLimit {
		if c, ok := openaiClass(typ); ok {
			e.Class = c
		}
	}
	return e
}

// refusesFormat recognises the 400 of a server without response_format
// (json_schema).
func refusesFormat(e *Error) bool {
	if e == nil || e.Status != http.StatusBadRequest || e.Class == api.FailureToolsUnsupported {
		return false
	}
	m := strings.ToLower(e.Message)
	return strings.Contains(m, "response_format") || strings.Contains(m, "json_schema") || strings.Contains(m, "response format")
}

// Stream implements Provider.
func (o *openaiCompat) Stream(ctx context.Context, req Request, emit func(Event) error) error {
	resp, err := o.open(ctx, req, true)
	if err != nil {
		var e *Error
		if len(req.JSONSchema) > 0 && errors.As(err, &e) && refusesFormat(e) {
			resp, err = o.open(ctx, req, false)
		}
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	st := &compatStream{emit: emit, calls: map[int]*compatCallState{}}
	switch {
	case mediaType(resp) == "text/event-stream":
		if err := readSSE(resp.Body, st.event); err != nil {
			return readError(ctx, err)
		}
		if st.stopped {
			return nil
		}
		return st.end()
	case isJSON(resp):
		body, err := boundedBody(resp.Body, MaxStreamBytes)
		if err != nil {
			return readError(ctx, err)
		}
		return st.plain(body)
	}
	return protocolError("the server did not answer with an event stream or JSON")
}

// open sends the request and returns the open response.
func (o *openaiCompat) open(ctx context.Context, req Request, withFormat bool) (*http.Response, error) {
	body, err := o.body(req, withFormat)
	if err != nil {
		return nil, err
	}
	return o.send(ctx, func() (*http.Request, error) {
		return o.newRequest(ctx, http.MethodPost, "/chat/completions", body, "text/event-stream, application/json", o.headers())
	}, o.classify)
}

// compatStream assembles the chunks of one chat completion.
type compatStream struct {
	emit    func(Event) error
	text    strings.Builder
	hasText bool
	calls   map[int]*compatCallState
	emitted int
	finish  string
	stopped bool
}

type compatCallState struct {
	id, name string
	args     strings.Builder
}

// compatChunk is one streamed chunk or a whole completion.
type compatChunk struct {
	Choices []struct {
		Delta        *compatChoiceMessage `json:"delta"`
		Message      *compatChoiceMessage `json:"message"`
		FinishReason string               `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

type compatChoiceMessage struct {
	Content   *string `json:"content"`
	ToolCalls []struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// event handles one server-sent event: a chunk, or the [DONE] sentinel.
func (s *compatStream) event(_, data string) error {
	if s.stopped {
		return nil
	}
	if strings.TrimSpace(data) == "[DONE]" {
		return s.end()
	}
	var ch compatChunk
	if err := json.Unmarshal([]byte(data), &ch); err != nil {
		return protocolError("a chunk of the stream is not JSON")
	}
	if len(ch.Error) > 0 {
		return s.chunkError(ch.Error)
	}
	if len(ch.Choices) > 0 {
		c := ch.Choices[0]
		if c.Delta != nil {
			if err := s.delta(c.Delta); err != nil {
				return err
			}
		}
		if c.FinishReason != "" {
			s.finish = c.FinishReason
			if err := s.flushText(); err != nil {
				return err
			}
			if err := s.flushCalls(); err != nil {
				return err
			}
		}
	}
	if ch.Usage != nil {
		u := api.AssistantUsage{
			InputTokens:          ch.Usage.PromptTokens - ch.Usage.PromptTokensDetails.CachedTokens,
			OutputTokens:         ch.Usage.CompletionTokens,
			CacheReadInputTokens: ch.Usage.PromptTokensDetails.CachedTokens,
		}
		if u.InputTokens < 0 {
			u.InputTokens = 0
		}
		return s.emit(Event{Kind: EventUsage, Usage: &u})
	}
	return nil
}

// chunkError is an error object a server put into the stream.
func (s *compatStream) chunkError(raw json.RawMessage) error {
	typ, msg := providerError([]byte(`{"error":` + string(raw) + `}`))
	c, ok := openaiClass(typ)
	if !ok {
		c = api.FailureInvalidRequest
	}
	if mentionsToolsUnsupported(msg) {
		c = api.FailureToolsUnsupported
	}
	return &Error{Class: c, Message: cleanMessage(msg)}
}

// delta applies one choice delta.
func (s *compatStream) delta(d *compatChoiceMessage) error {
	if d.Content != nil && *d.Content != "" {
		s.text.WriteString(*d.Content)
		s.hasText = true
		if err := s.emit(Event{Kind: EventTextDelta, Text: *d.Content}); err != nil {
			return err
		}
	}
	for _, tc := range d.ToolCalls {
		idx := len(s.calls)
		if tc.Index != nil {
			idx = *tc.Index
		}
		c := s.calls[idx]
		if c == nil {
			if err := s.flushText(); err != nil {
				return err
			}
			c = &compatCallState{}
			s.calls[idx] = c
		}
		if tc.ID != "" {
			c.id = tc.ID
		}
		if tc.Function.Name != "" {
			c.name = tc.Function.Name
		}
		c.args.WriteString(tc.Function.Arguments)
	}
	return nil
}

// flushText ends the text block assembled so far.
func (s *compatStream) flushText() error {
	if !s.hasText {
		return nil
	}
	text := s.text.String()
	s.text.Reset()
	s.hasText = false
	return s.emit(Event{Kind: EventTextDone, Text: text})
}

// flushCalls emits the assembled tool calls in index order.
func (s *compatStream) flushCalls() error {
	if len(s.calls) == 0 {
		return nil
	}
	idx := make([]int, 0, len(s.calls))
	for i := range s.calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		c := s.calls[i]
		delete(s.calls, i)
		s.emitted++
		if err := s.emit(parseToolInput(c.id, c.name, c.args.String())); err != nil {
			return err
		}
	}
	return nil
}

// end finishes the completion at [DONE] or at the end of the stream.
func (s *compatStream) end() error {
	if s.stopped {
		return nil
	}
	s.stopped = true
	if err := s.flushText(); err != nil {
		return err
	}
	if err := s.flushCalls(); err != nil {
		return err
	}
	return s.emit(Event{Kind: EventStop, Stop: s.stop()})
}

// stop is the normalised finish reason; tool calls make it toolUse
// whatever the server said, unless the answer was cut.
func (s *compatStream) stop() StopReason {
	switch s.finish {
	case "length":
		return StopMaxTokens
	case "content_filter":
		return StopRefusal
	}
	if s.emitted > 0 {
		return StopToolUse
	}
	switch s.finish {
	case "stop", "tool_calls", "function_call":
		return StopEndTurn
	}
	return StopOther
}

// plain reads a completion a server answered without streaming.
func (s *compatStream) plain(body []byte) error {
	var ch compatChunk
	if err := json.Unmarshal(body, &ch); err != nil {
		return protocolError("the completion is not JSON")
	}
	if len(ch.Error) > 0 {
		return s.chunkError(ch.Error)
	}
	if len(ch.Choices) == 0 {
		return protocolError("the completion has no choices")
	}
	c := ch.Choices[0]
	msg := c.Message
	if msg == nil {
		msg = c.Delta
	}
	if msg != nil {
		if err := s.delta(msg); err != nil {
			return err
		}
	}
	s.finish = c.FinishReason
	if err := s.flushText(); err != nil {
		return err
	}
	if err := s.flushCalls(); err != nil {
		return err
	}
	if ch.Usage != nil {
		u := api.AssistantUsage{
			InputTokens:          ch.Usage.PromptTokens - ch.Usage.PromptTokensDetails.CachedTokens,
			OutputTokens:         ch.Usage.CompletionTokens,
			CacheReadInputTokens: ch.Usage.PromptTokensDetails.CachedTokens,
		}
		if u.InputTokens < 0 {
			u.InputTokens = 0
		}
		if err := s.emit(Event{Kind: EventUsage, Usage: &u}); err != nil {
			return err
		}
	}
	return s.end()
}

// Models implements Provider: GET /models; a server answering something
// else is a protocol error.
func (o *openaiCompat) Models(ctx context.Context) ([]api.AIModel, error) {
	ctx, cancel := withModelsTimeout(ctx)
	defer cancel()
	return o.client.listModels(ctx, o.headers(), o.classify)
}
