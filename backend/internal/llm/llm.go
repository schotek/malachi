// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package llm calls the model APIs behind the daemon's ai accounts
// (docs/api.md §4.1 and §4.18; docs/security.md §7.6; the design is
// docs/plans/ai-api.md): the Anthropic Messages API, the OpenAI
// Responses API and the OpenAI-compatible chat completions interface of
// self-hosted servers and gateways. One Provider is one account's API;
// Stream runs one model call and reports what the model writes as it
// streams; Models lists what the provider serves.
//
// The package is a thin client over net/http and encoding/json with the
// daemon's rules for outbound HTTP: no redirects, bounded bodies and
// streams, the key only in the request's header, never in a log, an
// error or a URL. It knows nothing of mail: the text it sends and
// receives is whatever the agent (internal/agent) gives it.
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Limits of what the package reads from a provider.
const (
	// MaxLineBytes bounds one line of a server-sent event stream, and
	// MaxEventBytes one event (its data lines together).
	MaxLineBytes  = 1 << 20
	MaxEventBytes = 4 << 20
	// MaxStreamBytes bounds one whole answer, streamed or not.
	MaxStreamBytes = 32 << 20
	// MaxErrorBytes is how much of an error body is read for its class.
	MaxErrorBytes = 64 << 10
	// MaxModels bounds the models of a listing (GET /v1/models).
	MaxModels = 500
	// MaxModelIDBytes bounds a model id and display name after cleaning.
	MaxModelIDBytes = 256

	// Timeouts of one call.
	DialTimeout         = 15 * time.Second
	TLSHandshakeTimeout = 10 * time.Second
	HeaderTimeout       = 60 * time.Second // from the request to the response headers
	ModelsTimeout       = 30 * time.Second // a whole listing of models
	// MaxRetryAfter bounds how long a 429 or 529 is waited out for the one
	// retry the package makes.
	MaxRetryAfter = 30 * time.Second

	// DefaultMaxOutputTokens is Request.MaxOutputTokens when zero.
	DefaultMaxOutputTokens = 8192
)

// Provider is one ai account's model API.
type Provider interface {
	// Stream runs one model call: it sends the request, streams the
	// answer and calls emit for each event in order, ending with
	// EventStop (or returning an error, an *Error for a provider's
	// refusal or a failed connection, emit's own error when emit refused
	// an event, ctx.Err() when ctx ended). After an error no event
	// follows. The request's history is sent as it is: the caller owns
	// its size.
	Stream(ctx context.Context, req Request, emit func(Event) error) error
	// Models lists the models the provider serves (GET /v1/models),
	// cleaned and at most MaxModels; an *Error says why not.
	Models(ctx context.Context) ([]api.AIModel, error)
}

// Request is one model call.
type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []Tool
	// JSONSchema asks for an answer that is a JSON document of this
	// schema (a JSON object schema with additionalProperties false and
	// every property required, the shape every provider accepts); nil
	// asks for text.
	JSONSchema json.RawMessage
	// MaxOutputTokens bounds the answer; 0 = DefaultMaxOutputTokens.
	MaxOutputTokens int
}

// Role is who wrote a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of the history: the user's text, the assistant's
// answer with its tool calls, or the user's turn carrying the tool
// results (one BlockToolResult per call, in the call's order).
type Message struct {
	Role   Role
	Blocks []Block
}

// BlockKind is what a Block carries.
type BlockKind string

const (
	BlockText       BlockKind = "text"
	BlockToolUse    BlockKind = "toolUse"    // an assistant message: the model called a tool
	BlockToolResult BlockKind = "toolResult" // a user message: the tool's answer
)

// Block is one piece of a Message.
type Block struct {
	Kind BlockKind
	// Text is the text of BlockText, or the result of BlockToolResult.
	Text string
	// ID is the call's id (BlockToolUse, BlockToolResult), as the
	// provider named it.
	ID string
	// Name and Input are the tool and its arguments of BlockToolUse.
	Name  string
	Input json.RawMessage
	// IsError marks a BlockToolResult the tool refused.
	IsError bool
}

// Tool is a tool the model may call: the bridge's name, description and
// JSON schema of its input, as tools/list gave them.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// EventKind is what a streamed Event carries.
type EventKind string

const (
	// EventTextDelta is a piece of the answer's text as it arrives.
	EventTextDelta EventKind = "textDelta"
	// EventTextDone ends a text block: Text is the whole block.
	EventTextDone EventKind = "textDone"
	// EventToolCall is a complete tool call (its arguments assembled
	// and parsed as JSON; a call whose arguments are not valid JSON is
	// reported with Input nil and the raw text in Text, for the caller to
	// refuse).
	EventToolCall EventKind = "toolCall"
	// EventUsage reports the tokens of the call so far; the last one is
	// the call's whole usage. A provider that reports none sends none.
	EventUsage EventKind = "usage"
	// EventStop ends the call: Stop says why the model stopped.
	EventStop EventKind = "stop"
)

// Event is one streamed event.
type Event struct {
	Kind  EventKind
	Text  string
	Call  *ToolCall
	Usage *api.AssistantUsage
	Stop  StopReason
}

// ToolCall is a complete tool call of the model.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// StopReason is why the model stopped, normalised across providers.
type StopReason string

const (
	StopEndTurn   StopReason = "endTurn"   // the answer is complete
	StopToolUse   StopReason = "toolUse"   // the model wants its tool calls answered
	StopMaxTokens StopReason = "maxTokens" // the answer was cut at MaxOutputTokens
	StopRefusal   StopReason = "refusal"   // the provider's safety layer declined
	StopOther     StopReason = "other"
)

// Config says which API to call and how.
type Config struct {
	Provider api.AIProvider
	// BaseURL is the effective base URL (api.AIConfig.EffectiveBaseURL):
	// "https://api.anthropic.com", "https://api.openai.com/v1", or the
	// compatible server's "…/v1".
	BaseURL string
	// Key is the API key; empty only for an OpenAI-compatible server
	// without one.
	Key string
	// CertificateSHA256 pins the leaf certificate of BaseURL's host for
	// https (api.NormalizeCertificateSHA256 form); empty = system roots.
	CertificateSHA256 string
	// UserAgent is sent with every request ("MalachiMail/<version>").
	UserAgent string
	// Client, when set, replaces the package's own HTTP client (tests);
	// the package still never follows redirects through it.
	Client *http.Client
}
