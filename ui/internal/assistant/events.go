// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// What the panel's Claude Code writes on stdout with --output-format
// stream-json (one JSON object per line) as the few events the panel
// shows, and the draft a create_draft result names. The lines carry mail
// content and model output: they are parsed defensively, a field of an
// unexpected type reads as its zero value, unknown types are ignored and
// nothing is interpreted beyond the fields named below.
//
// The shapes (Claude Code 2.1.178): system/init (tools, mcp_servers
// [{name, status}], model, session_id, …), system/status and others,
// rate_limit_event, stream_event (event content_block_delta with delta
// text_delta {text}; thinking_delta and signature_delta ignored),
// assistant (message.content: thinking, text {text}, tool_use {id, name,
// input}; error when Claude Code wrote the message itself because the API
// refused the turn: authentication_failed, billing_error, rate_limit,
// invalid_request, server_error, unknown), user (message.content:
// tool_result {tool_use_id, is_error, content: a string or [{type: text,
// text}]}) and result (subtype success or error_*, is_error, result,
// structured_output, permission_denials [{tool_name}], total_cost_usd,
// usage).
//
// This file holds no translatable text.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

// EventKind is what an Event is.
type EventKind int

// The kinds of events.
const (
	// EventOther is anything else: status, rate limits, a delta that is
	// not text, a type this client does not know.
	EventOther EventKind = iota
	// EventInit is system/init, at the start of every turn.
	EventInit
	// EventTextDelta is a piece of the answer as it streams.
	EventTextDelta
	// EventText is one whole text block of an assistant message,
	// authoritative over the deltas that preceded it.
	EventText
	// EventToolUse is a tool being called.
	EventToolUse
	// EventToolResult is a tool's answer.
	EventToolResult
	// EventResult ends the turn.
	EventResult
	// EventFailure is an assistant message Claude Code wrote itself: the
	// API refused the turn. Its text is no answer; the result that follows
	// repeats it.
	EventFailure
)

// authenticationFailed is the Failure of a turn the API refused because
// Claude Code is not signed in, or its sign-in is no longer accepted.
const authenticationFailed = "authentication_failed"

// Event is one thing the panel reacts to; only the fields of its Kind are
// set.
type Event struct {
	Kind EventKind
	// EventInit: whether the MCP server "malachi" reported "connected",
	// and the names of the tools Claude Code offers, as reported.
	BridgeConnected bool
	Tools           []string
	// EventTextDelta and EventText: the text.
	Text string
	// EventToolUse: the tool without the "mcp__malachi__" prefix (another
	// tool keeps its name) and the call's id; EventToolResult: the id of
	// the call it answers.
	Tool, ToolUseID string
	// EventToolResult: whether the tool failed, and its text blocks
	// joined with "\n" (or its string content); EventResult: the turn's
	// result text, and for a failure without one its subtype
	// ("error_max_turns", …).
	IsError    bool
	ResultText string
	// EventResult: subtype "success" and not is_error; the tools the
	// permission mode denied (prefix stripped), in order; the cost as
	// Claude Code reports it (logged, never shown); structured_output as
	// raw JSON, nil when absent or null.
	Success    bool
	Denied     []string
	CostUSD    float64
	Structured json.RawMessage
	// EventFailure: what Claude Code calls the failure, its message's
	// error ("authentication_failed", "rate_limit", …).
	Failure string
}

// NotSignedIn says whether the event is the failure of a turn for want of
// a sign-in the API accepts: Claude Code is signed out, or its sign-in has
// expired or was revoked (claude auth status may still say loggedIn then).
func (e Event) NotSignedIn() bool {
	return e.Kind == EventFailure && e.Failure == authenticationFailed
}

// ParseEvents reads one stdout line (without its newline): one event per
// text or tool_use block of an assistant message and per tool_result
// block of a user message, in order (thinking and other blocks yield
// nothing, so such a message may yield none), one EventFailure alone for
// an assistant message with an error, one EventInit for
// system/init, one EventTextDelta for a text delta, one EventResult for a
// result, and one EventOther for any other line. It is an error when the
// line is not JSON or not a JSON object.
func ParseEvents(line []byte) ([]Event, error) {
	trimmed := bytes.TrimSpace(line)
	if !json.Valid(trimmed) {
		return nil, errMalformed
	}
	if trimmed[0] != '{' {
		return nil, errNotObject
	}
	var o object
	if err := json.Unmarshal(trimmed, &o); err != nil {
		return nil, errMalformed
	}
	switch o.str("type") {
	case "system":
		if o.str("subtype") == "init" {
			return []Event{parseInit(o)}, nil
		}
	case "stream_event":
		ev := o.obj("event")
		delta := ev.obj("delta")
		if ev.str("type") == "content_block_delta" && delta.str("type") == "text_delta" {
			return []Event{{Kind: EventTextDelta, Text: delta.str("text")}}, nil
		}
	case "assistant":
		return parseAssistant(o), nil
	case "user":
		return parseUser(o), nil
	case "result":
		return []Event{parseResult(o)}, nil
	}
	return []Event{{Kind: EventOther}}, nil
}

func parseInit(o object) Event {
	e := Event{Kind: EventInit}
	for _, t := range o.array("tools") {
		if s, ok := str(t); ok {
			e.Tools = append(e.Tools, s)
		}
	}
	for _, s := range o.array("mcp_servers") {
		srv := objectOf(s)
		if srv.str("name") == bridgeServer && srv.str("status") == "connected" {
			e.BridgeConnected = true
		}
	}
	return e
}

func parseAssistant(o object) []Event {
	if failure := o.str("error"); failure != "" {
		return []Event{{Kind: EventFailure, Failure: failure}}
	}
	var out []Event
	for _, raw := range o.obj("message").array("content") {
		b := objectOf(raw)
		switch b.str("type") {
		case "text":
			out = append(out, Event{Kind: EventText, Text: b.str("text")})
		case "tool_use":
			out = append(out, Event{Kind: EventToolUse, Tool: strings.TrimPrefix(b.str("name"), toolPrefix), ToolUseID: b.str("id")})
		}
	}
	return out
}

func parseUser(o object) []Event {
	var out []Event
	for _, raw := range o.obj("message").array("content") {
		b := objectOf(raw)
		if b.str("type") != "tool_result" {
			continue
		}
		out = append(out, Event{
			Kind:       EventToolResult,
			ToolUseID:  b.str("tool_use_id"),
			IsError:    b.boolean("is_error"),
			ResultText: resultText(b["content"]),
		})
	}
	return out
}

// resultText is a tool_result's content: a string, or the text blocks of
// an array joined with "\n" (images and other blocks left out).
func resultText(content json.RawMessage) string {
	if s, ok := str(content); ok {
		return s
	}
	var texts []string
	for _, raw := range arrayOf(content) {
		b := objectOf(raw)
		if b.str("type") == "text" {
			texts = append(texts, b.str("text"))
		}
	}
	return strings.Join(texts, "\n")
}

func parseResult(o object) Event {
	subtype := o.str("subtype")
	e := Event{
		Kind:       EventResult,
		IsError:    o.boolean("is_error"),
		ResultText: o.str("result"),
		CostUSD:    o.number("total_cost_usd"),
	}
	e.Success = subtype == "success" && !e.IsError
	if !e.Success && e.ResultText == "" {
		e.ResultText = subtype
	}
	for _, raw := range o.array("permission_denials") {
		if name := objectOf(raw).str("tool_name"); name != "" {
			e.Denied = append(e.Denied, strings.TrimPrefix(name, toolPrefix))
		}
	}
	if raw := bytes.TrimSpace(o["structured_output"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		e.Structured = append(json.RawMessage(nil), raw...)
	}
	return e
}

// object is a JSON object whose members are read by type: a member that is
// missing or of another type reads as the zero value.
type object map[string]json.RawMessage

// objectOf is raw as an object; nil when it is not one.
func objectOf(raw json.RawMessage) object {
	var o object
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	return o
}

// arrayOf is raw as an array; nil when it is not one.
func arrayOf(raw json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil {
		return nil
	}
	return a
}

// str is raw as a string, and whether it is one (null is not).
func str(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func (o object) str(key string) string {
	s, _ := str(o[key])
	return s
}

func (o object) obj(key string) object {
	return objectOf(o[key])
}

func (o object) array(key string) []json.RawMessage {
	return arrayOf(o[key])
}

func (o object) boolean(key string) bool {
	var b bool
	if json.Unmarshal(o[key], &b) != nil {
		return false
	}
	return b
}

func (o object) number(key string) float64 {
	raw := bytes.TrimSpace(o[key])
	if len(raw) == 0 || !(raw[0] == '-' || '0' <= raw[0] && raw[0] <= '9') {
		return 0
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0
	}
	return f
}

// DraftRef is a draft the bridge stored for the panel.
type DraftRef struct {
	AccountID, DraftID string
	Version            int
}

// The parts of the head of a create_draft result,
// backend/cmd/malachi-mcp/tools_write.go:
// "draft <id> (version <n>) stored in account <acc>; it is NOT sent."
const (
	draftHead    = "draft "
	draftVersion = " (version "
	draftAccount = ") stored in account "
	draftTail    = "; it is NOT sent."
)

// ParseDraftResult reads the draft a create_draft tool result names (the
// panel asks only for the results of its create_draft calls, by
// ToolUseID). Only the result's first line counts, and only when it starts
// exactly with the head the bridge itself writes,
// "draft <id> (version <n>) stored in account <acc>; it is NOT sent.",
// followed by the end of the line or a space (the bridge goes on with a
// sentence about sending). That line is the bridge's own text, outside
// the fence in which it quotes mail, so mail cannot forge it; the ids are
// non-empty and hold no whitespace or control characters, the version is
// 1 to 9 digits. The application still looks the draft up with draft.list
// before it offers to open it.
func ParseDraftResult(text string) (DraftRef, bool) {
	line, _, _ := strings.Cut(text, "\n")
	rest, ok := strings.CutPrefix(line, draftHead)
	if !ok {
		return DraftRef{}, false
	}
	id, rest, ok := strings.Cut(rest, draftVersion)
	if !ok || !validID(id) {
		return DraftRef{}, false
	}
	digits, rest, ok := strings.Cut(rest, draftAccount)
	if !ok || len(digits) == 0 || len(digits) > 9 || strings.TrimLeft(digits, "0123456789") != "" {
		return DraftRef{}, false
	}
	acc, rest, ok := strings.Cut(rest, draftTail)
	if !ok || !validID(acc) || (rest != "" && rest[0] != ' ') {
		return DraftRef{}, false
	}
	version, err := strconv.Atoi(digits)
	if err != nil {
		return DraftRef{}, false
	}
	return DraftRef{AccountID: acc, DraftID: id, Version: version}, true
}

// validID says whether s is an id of the bridge's head: non-empty, no
// whitespace or control characters.
func validID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
