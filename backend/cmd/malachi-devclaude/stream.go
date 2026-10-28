// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"
	"unicode/utf8"
)

// emitter writes the stream-json events of Claude Code's --output-format
// stream-json, one JSON object per line, in the shapes ui/internal/assistant
// ParseEvents reads (and its Swift and C# ports): system/init at the start
// of a turn, stream_event text deltas, assistant messages with text or
// tool_use blocks (each with the message's id and usage), user messages
// with tool_result blocks, and the result that ends the turn, with the
// whole turn's usage. The usage is made up from the sizes of what went in
// and out; it is only there so that the clients' tallies have something
// to add up.
type emitter struct {
	mu        sync.Mutex
	w         io.Writer
	model     string
	sessionID string
	msgN      int
	toolN     int
	// The turn's counters.
	input, output int64
	// delay is the pause between two deltas (the answer streams as a
	// model's would, a little); the tests set it to 0.
	delay time.Duration
}

func newEmitter(w io.Writer, model string) *emitter {
	return &emitter{w: w, model: model, sessionID: randomID(), delay: 12 * time.Millisecond}
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// line writes one event.
func (e *emitter) line(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write(append(b, '\n'))
}

// usage is Claude Code's usage object.
type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// tokens is a rough token count of text: four bytes a token.
func tokens(s string) int64 {
	return int64(len(s)+3) / 4
}

// beginTurn resets the counters and writes system/init: the bridge
// connected (or absent) and its tools.
func (e *emitter) beginTurn(bridgeConnected bool, tools []string, systemPrompt, message string) {
	e.input = tokens(systemPrompt) + tokens(message) + 200
	e.output = 0
	servers := []map[string]string{}
	if bridgeConnected {
		servers = append(servers, map[string]string{"name": "malachi", "status": "connected"})
	}
	if tools == nil {
		tools = []string{}
	}
	e.line(map[string]any{
		"type": "system", "subtype": "init", "cwd": "", "session_id": e.sessionID,
		"tools": tools, "mcp_servers": servers, "model": e.model, "permissionMode": "dontAsk",
		"apiKeySource": "none", "claude_code_version": "2.1.285",
	})
}

// delta streams a piece of the answer.
func (e *emitter) delta(text string) {
	e.line(map[string]any{
		"type":       "stream_event",
		"event":      map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": text}},
		"session_id": e.sessionID,
	})
}

// stream writes text as deltas of a few dozen bytes each, then the whole
// text as an assistant message, and counts it as output.
func (e *emitter) stream(text string) {
	const piece = 28
	for start := 0; start < len(text); {
		end := start + piece
		if end >= len(text) {
			end = len(text)
		} else {
			for end > start && !utf8.RuneStart(text[end]) {
				end--
			}
			if end == start {
				end = start + piece
			}
		}
		e.delta(text[start:end])
		start = end
		if e.delay > 0 {
			time.Sleep(e.delay)
		}
	}
	e.text(text)
}

// messageUsage is the usage of one assistant message: the input so far,
// and a placeholder output as Claude Code reports at a message's start.
func (e *emitter) messageUsage() usage {
	return usage{InputTokens: e.input, OutputTokens: 1}
}

func (e *emitter) nextMessageID() string {
	e.msgN++
	return "msg_devclaude_" + e.sessionID[:8] + "_" + itoa(e.msgN)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// text writes one whole text block as an assistant message.
func (e *emitter) text(text string) {
	e.output += tokens(text)
	e.line(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": e.nextMessageID(), "type": "message", "role": "assistant", "model": e.model,
			"content":     []map[string]string{{"type": "text", "text": text}},
			"stop_reason": "end_turn", "usage": e.messageUsage(),
		},
		"parent_tool_use_id": nil, "session_id": e.sessionID,
	})
}

// toolUse writes a tool call and returns its id.
func (e *emitter) toolUse(tool string, input any) string {
	e.toolN++
	id := "toolu_devclaude_" + itoa(e.toolN)
	if input == nil {
		input = map[string]any{}
	}
	e.output += 30
	e.line(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": e.nextMessageID(), "type": "message", "role": "assistant", "model": e.model,
			"content":     []map[string]any{{"type": "tool_use", "id": id, "name": "mcp__malachi__" + tool, "input": input}},
			"stop_reason": "tool_use", "usage": e.messageUsage(),
		},
		"parent_tool_use_id": nil, "session_id": e.sessionID,
	})
	return id
}

// toolResult writes a tool's answer and counts it as input.
func (e *emitter) toolResult(id, text string, isError bool) {
	e.input += tokens(text)
	e.line(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type": "tool_result", "tool_use_id": id, "is_error": isError,
				"content": []map[string]string{{"type": "text", "text": text}},
			}},
		},
		"parent_tool_use_id": nil, "session_id": e.sessionID,
	})
}

// result ends the turn: the text, the structured output when the request
// had a schema, and the turn's usage and cost.
func (e *emitter) result(text string, structured any, turns int) {
	u := usage{InputTokens: e.input, OutputTokens: e.output}
	cost := float64(u.InputTokens)*3e-6 + float64(u.OutputTokens)*15e-6
	r := map[string]any{
		"type": "result", "subtype": "success", "is_error": false,
		"duration_ms": 1200, "duration_api_ms": 900, "num_turns": turns,
		"result": text, "session_id": e.sessionID, "total_cost_usd": cost, "usage": u,
		"permission_denials": []any{},
	}
	if structured != nil {
		r["structured_output"] = structured
	}
	e.line(r)
}
