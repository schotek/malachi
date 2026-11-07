// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// openai is the OpenAI Responses API (POST /responses under the "/v1"
// base URL, streamed; GET /models): Authorization: Bearer, the system
// prompt as instructions, the history as input items (messages,
// function_call and function_call_output), tools as function tools,
// store false, a JSON schema as text.format.
type openai struct {
	*client
}

func (o *openai) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + o.cfg.Key}
}

// Wire shapes of the request.
type (
	openaiRequest struct {
		Model           string           `json:"model"`
		Instructions    string           `json:"instructions,omitempty"`
		Input           []openaiItem     `json:"input"`
		Tools           []openaiTool     `json:"tools,omitempty"`
		Stream          bool             `json:"stream"`
		Store           bool             `json:"store"`
		MaxOutputTokens int              `json:"max_output_tokens"`
		Text            *openaiTextParam `json:"text,omitempty"`
	}
	openaiItem struct {
		Type      string  `json:"type,omitempty"`
		Role      string  `json:"role,omitempty"`
		Content   any     `json:"content,omitempty"`
		CallID    string  `json:"call_id,omitempty"`
		Name      string  `json:"name,omitempty"`
		Arguments *string `json:"arguments,omitempty"`
		Output    *string `json:"output,omitempty"`
	}
	openaiContentPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	openaiTool struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      bool            `json:"strict"`
	}
	openaiTextParam struct {
		Format openaiTextFormat `json:"format"`
	}
	openaiTextFormat struct {
		Type   string          `json:"type"`
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	}
)

// body builds the request body of req.
func (o *openai) body(req Request) ([]byte, error) {
	w := openaiRequest{
		Model:           req.Model,
		Instructions:    req.System,
		Input:           []openaiItem{},
		Stream:          true,
		Store:           false,
		MaxOutputTokens: maxOutputTokens(req),
	}
	if len(req.JSONSchema) > 0 {
		w.Text = &openaiTextParam{Format: openaiTextFormat{Type: "json_schema", Name: "answer", Schema: req.JSONSchema, Strict: true}}
	}
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			switch b.Kind {
			case BlockText:
				if b.Text == "" {
					continue
				}
				if m.Role == RoleAssistant {
					w.Input = append(w.Input, openaiItem{Role: "assistant", Content: []openaiContentPart{{Type: "output_text", Text: b.Text}}})
				} else {
					w.Input = append(w.Input, openaiItem{Role: "user", Content: b.Text})
				}
			case BlockToolUse:
				args := string(schemaOr(b.Input, "{}"))
				w.Input = append(w.Input, openaiItem{Type: "function_call", CallID: b.ID, Name: b.Name, Arguments: &args})
			case BlockToolResult:
				out := b.Text
				if b.IsError {
					out = "ERROR: " + out
				}
				w.Input = append(w.Input, openaiItem{Type: "function_call_output", CallID: b.ID, Output: &out})
			}
		}
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, openaiTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: toolSchema(t.InputSchema), Strict: false})
	}
	return encodeJSON(w)
}

// classify maps an HTTP error of the Responses API.
func (o *openai) classify(resp *http.Response, body []byte) *Error {
	typ, msg := providerError(body)
	e := httpError(resp, typ, msg)
	if e.Class == api.FailureInvalidRequest || e.Class == api.FailureRateLimit {
		if c, ok := openaiClass(typ); ok {
			e.Class = c
		}
	}
	return e
}

// openaiClass maps the API's error types and codes where they say more
// than the status.
func openaiClass(typ string) (api.AssistantFailureClass, bool) {
	switch typ {
	case "invalid_api_key", "authentication_error":
		return api.FailureAuth, true
	case "insufficient_quota":
		return api.FailureQuota, true
	case "rate_limit_exceeded", "rate_limit_error":
		return api.FailureRateLimit, true
	case "server_error", "overloaded_error":
		return api.FailureOverloaded, true
	}
	return "", false
}

// Stream implements Provider.
func (o *openai) Stream(ctx context.Context, req Request, emit func(Event) error) error {
	body, err := o.body(req)
	if err != nil {
		return err
	}
	resp, err := o.send(ctx, func() (*http.Request, error) {
		return o.newRequest(ctx, http.MethodPost, "/responses", body, "text/event-stream", o.headers())
	}, o.classify)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if mediaType(resp) != "text/event-stream" {
		return protocolError("the provider did not answer with an event stream")
	}
	st := &openaiStream{emit: emit, calls: map[string]*openaiCallState{}}
	err = readSSE(resp.Body, st.event)
	switch {
	case err != nil:
		return readError(ctx, err)
	case !st.stopped:
		return streamEnded()
	}
	return nil
}

// openaiStream assembles the events of one streamed response.
type openaiStream struct {
	emit    func(Event) error
	calls   map[string]*openaiCallState // by item id
	order   []string
	emitted int
	refusal strings.Builder
	refused bool
	stopped bool
}

type openaiCallState struct {
	callID, name string
	args         strings.Builder
	done         bool
}

// openaiEvent is the union of the stream's data objects.
type openaiEvent struct {
	Type      string `json:"type"`
	ItemID    string `json:"item_id"`
	Delta     string `json:"delta"`
	Text      string `json:"text"`
	Arguments string `json:"arguments"`
	Refusal   string `json:"refusal"`
	Item      struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Response struct {
		Status            string `json:"status"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Error struct {
		Code    string `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// event handles one server-sent event of the Responses API.
func (s *openaiStream) event(name, data string) error {
	if s.stopped {
		return nil
	}
	var ev openaiEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return protocolError("an event of the stream is not JSON")
	}
	typ := ev.Type
	if typ == "" {
		typ = name
	}
	switch typ {
	case "response.output_text.delta":
		if ev.Delta != "" {
			return s.emit(Event{Kind: EventTextDelta, Text: ev.Delta})
		}
	case "response.output_text.done":
		return s.emit(Event{Kind: EventTextDone, Text: ev.Text})
	case "response.refusal.delta":
		s.refusal.WriteString(ev.Delta)
		s.refused = true
	case "response.refusal.done":
		s.refused = true
		text := ev.Refusal
		if text == "" {
			text = s.refusal.String()
		}
		s.refusal.Reset()
		if text != "" {
			return s.emit(Event{Kind: EventTextDone, Text: text})
		}
	case "response.output_item.added":
		if ev.Item.Type == "function_call" {
			c := &openaiCallState{callID: ev.Item.CallID, name: ev.Item.Name}
			c.args.WriteString(ev.Item.Arguments)
			s.calls[ev.Item.ID] = c
			s.order = append(s.order, ev.Item.ID)
		}
	case "response.function_call_arguments.delta":
		if c := s.calls[ev.ItemID]; c != nil && !c.done {
			c.args.WriteString(ev.Delta)
		}
	case "response.function_call_arguments.done":
		if c := s.calls[ev.ItemID]; c != nil && !c.done {
			if ev.Arguments != "" {
				c.args.Reset()
				c.args.WriteString(ev.Arguments)
			}
			return s.finish(c)
		}
	case "response.output_item.done":
		if ev.Item.Type == "function_call" {
			c := s.calls[ev.Item.ID]
			if c == nil {
				c = &openaiCallState{callID: ev.Item.CallID, name: ev.Item.Name}
				s.calls[ev.Item.ID] = c
			}
			if c.done {
				return nil
			}
			if ev.Item.Arguments != "" {
				c.args.Reset()
				c.args.WriteString(ev.Item.Arguments)
			}
			return s.finish(c)
		}
	case "response.completed", "response.incomplete":
		// A function call the stream never finished is reported as it
		// stands, for the caller to refuse.
		for _, id := range s.order {
			if c := s.calls[id]; c != nil && !c.done {
				if err := s.finish(c); err != nil {
					return err
				}
			}
		}
		u := ev.Response.Usage
		usage := api.AssistantUsage{
			InputTokens:          u.InputTokens - u.InputTokensDetails.CachedTokens,
			OutputTokens:         u.OutputTokens,
			CacheReadInputTokens: u.InputTokensDetails.CachedTokens,
		}
		if usage.InputTokens < 0 {
			usage.InputTokens = 0
		}
		if err := s.emit(Event{Kind: EventUsage, Usage: &usage}); err != nil {
			return err
		}
		s.stopped = true
		return s.emit(Event{Kind: EventStop, Stop: s.stop(ev)})
	case "response.failed":
		c, ok := openaiClass(ev.Response.Error.Code)
		if !ok {
			c = api.FailureInvalidRequest
		}
		return &Error{Class: c, Message: cleanMessage(ev.Response.Error.Message)}
	case "error":
		code, msg := ev.Error.Code, ev.Error.Message
		if code == "" {
			code = ev.Code
		}
		if msg == "" {
			msg = ev.Message
		}
		c, ok := openaiClass(code)
		if !ok {
			c = api.FailureInvalidRequest
		}
		return &Error{Class: c, Message: cleanMessage(msg)}
	}
	return nil
}

// finish emits an assembled function call.
func (s *openaiStream) finish(c *openaiCallState) error {
	c.done = true
	s.emitted++
	return s.emit(parseToolInput(c.callID, c.name, c.args.String()))
}

// stop is the normalised stop reason of a completed or incomplete
// response.
func (s *openaiStream) stop(ev openaiEvent) StopReason {
	switch ev.Response.IncompleteDetails.Reason {
	case "max_output_tokens":
		return StopMaxTokens
	case "content_filter":
		return StopRefusal
	}
	switch {
	case ev.Response.Status == "incomplete":
		return StopOther
	case s.emitted > 0:
		return StopToolUse
	case s.refused:
		return StopRefusal
	}
	return StopEndTurn
}

// Models implements Provider: GET /models.
func (o *openai) Models(ctx context.Context) ([]api.AIModel, error) {
	ctx, cancel := withModelsTimeout(ctx)
	defer cancel()
	return o.client.listModels(ctx, o.headers(), o.classify)
}

// listModels is the OpenAI-shaped listing: GET /models answering
// {"data":[{"id"}]}; anything else is a protocol error.
func (c *client) listModels(ctx context.Context, headers map[string]string, classify func(*http.Response, []byte) *Error) ([]api.AIModel, error) {
	resp, err := c.send(ctx, func() (*http.Request, error) {
		return c.newRequest(ctx, http.MethodGet, "/models", nil, "application/json", headers)
	}, classify)
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
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Data == nil {
		return nil, protocolError("the list of models could not be read")
	}
	var out []api.AIModel
	for _, m := range list.Data {
		if cm, ok := cleanModel(m.ID, ""); ok {
			out = append(out, cm)
			if len(out) >= MaxModels {
				break
			}
		}
	}
	return out, nil
}
