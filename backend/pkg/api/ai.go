// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"time"
)

// AI provider accounts (kind "ai", docs/api.md §4.1) and the assistant the
// daemon runs over them (§4.18; the design is docs/plans/ai-api.md). An
// ai account names a model API — Anthropic, OpenAI, or any server with
// the OpenAI-compatible chat completions interface (Ollama, llama.cpp,
// LM Studio, vLLM, a gateway) — and keeps its API key in the keyring as
// Credentials.Password. The daemon runs the assistant's sessions itself:
// it builds the prompts (assistant.prompt gives the same ones to the
// clients that run Claude Code or Codex), calls the model, streams the
// answer to the connection that started the session and executes the
// model's tool calls through the MCP bridge. Nothing the model writes is
// trusted: clients show it as plain text, as they do the bridge's.

// ---------------------------------------------------------------------------
// Limits. Exceeding an input limit is invalidArgument.
// ---------------------------------------------------------------------------

const (
	// AssistantConsentVersion is the version of the consent text an ai
	// account's user accepted (AIConsent.Version); a session starts only
	// with this version or a later one.
	AssistantConsentVersion = 1

	MaxAssistantSessionsPerConn = 4         // open sessions of one connection
	MaxAssistantSessions        = 16        // open sessions of the daemon
	MaxAssistantTurnTools       = 50        // tool rounds of one turn
	MaxAssistantToolResultBytes = 256 << 10 // a tool result kept in the history
	MaxAssistantHistoryBytes    = 1 << 20   // the session's history; beyond it the session ends
	MaxPromptTextBytes          = 64 << 10  // PromptInput.Text and every free text of an input
	MaxPromptMessageIDs         = 20        // PromptContext.MessageIDs, ReplyInput.Others
	MaxAssistantRunIDBytes      = 128       // AssistantSessionStartParams.RunID
	MaxAssistantUsageDays       = 400       // AssistantUsageParams.Days
	// AssistantSessionIdle ends a session nobody wrote to.
	AssistantSessionIdle = 30 * time.Minute
	// DefaultAssistantTimeout bounds one turn of a panel session, the
	// longest a triage session may ask for is MaxAssistantTimeout.
	DefaultAssistantTimeout = 5 * time.Minute
	MaxAssistantTimeout     = 15 * time.Minute
)

// AIProvider is the model API an ai account talks to.
type AIProvider string

const (
	// AIAnthropic is the Anthropic Messages API at AnthropicBaseURL.
	AIAnthropic AIProvider = "anthropic"
	// AIOpenAI is the OpenAI Responses API at OpenAIBaseURL.
	AIOpenAI AIProvider = "openai"
	// AIOpenAICompatible is a server with the OpenAI chat completions
	// interface at AIConfig.BaseURL (Ollama, llama.cpp, LM Studio, vLLM, a
	// gateway such as OpenRouter); the key is optional.
	AIOpenAICompatible AIProvider = "openaiCompatible"
)

// Valid reports one of the three values.
func (p AIProvider) Valid() bool {
	switch p {
	case AIAnthropic, AIOpenAI, AIOpenAICompatible:
		return true
	}
	return false
}

// The fixed base URLs of the two named providers; AIConfig.BaseURL is
// empty for them.
const (
	AnthropicBaseURL = "https://api.anthropic.com"
	OpenAIBaseURL    = "https://api.openai.com/v1"
)

// AIConfig is present only when Kind == AccountAI.
type AIConfig struct {
	Provider AIProvider `json:"provider"`
	// BaseURL is the server's base URL for AIOpenAICompatible ("…/v1";
	// normalised: no user info, query or fragment, no trailing slash; http
	// is allowed anywhere, a client warns outside loopback) and empty
	// for the named providers.
	BaseURL string `json:"baseUrl,omitempty"`
	// Model is the model id the assistant uses ("claude-opus-5-5",
	// "gpt-6.1-sol", "llama3.3"); required.
	Model string `json:"model"`
	// BoardModel is the model of the board's triage; empty = Model.
	BoardModel string `json:"boardModel,omitempty"`
	// Consent is the user's consent to sending mail to this provider
	// (§4.18); nil = not given, and no session starts.
	Consent *AIConsent `json:"consent,omitempty"`
	// CertificateSHA256 pins the leaf certificate of BaseURL's host
	// (https only), as ServerConfig.CertificateSHA256.
	CertificateSHA256 string `json:"certificateSha256,omitempty"`
}

// AIConsent records the consent text's version the user accepted and
// when (set by the daemon on account.add/update with a version).
type AIConsent struct {
	Version    int        `json:"version"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
}

// Consented reports whether the current consent text was accepted.
func (c AIConfig) Consented() bool {
	return c.Consent != nil && c.Consent.Version >= AssistantConsentVersion
}

// EffectiveBaseURL is the base URL the daemon calls: the fixed one of a
// named provider, else BaseURL.
func (c AIConfig) EffectiveBaseURL() string {
	switch c.Provider {
	case AIAnthropic:
		return AnthropicBaseURL
	case AIOpenAI:
		return OpenAIBaseURL
	}
	return c.BaseURL
}

// AIModel is a model the provider lists (GET /v1/models).
type AIModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

// AITestResult is account.test's outcome for an ai account: the probe
// (one GET /v1/models with the key) and the models it listed.
type AITestResult struct {
	EndpointTestResult
	Models []AIModel `json:"models,omitempty"`
}

// ---------------------------------------------------------------------------
// Prompts: what the daemon builds for a purpose, and the input of a turn.
// ---------------------------------------------------------------------------

// PromptPurpose names what an assistant session or prompt is for.
type PromptPurpose string

const (
	PurposePanel   PromptPurpose = "panel"   // the assistant panel: a conversation with tools
	PurposeRewrite PromptPurpose = "rewrite" // the compose window's rewrite of a passage, no tools
	PurposeWrite   PromptPurpose = "write"   // the compose window's writing bar, no tools
	PurposeSearch  PromptPurpose = "search"  // search in the user's own words, no tools, a schema
	PurposeTriage  PromptPurpose = "triage"  // a triage run of the board, with tools
	PurposeReply   PromptPurpose = "reply"   // a suggested reply of the board, with tools
)

// Valid reports one of the six values.
func (p PromptPurpose) Valid() bool {
	switch p {
	case PurposePanel, PurposeRewrite, PurposeWrite, PurposeSearch, PurposeTriage, PurposeReply:
		return true
	}
	return false
}

// RewritePreset is what the rewrite does with the passage.
type RewritePreset string

const (
	RewritePoliter   RewritePreset = "politer"
	RewriteShorter   RewritePreset = "shorter"
	RewriteFix       RewritePreset = "fix"
	RewriteToEnglish RewritePreset = "english"
	RewriteCustom    RewritePreset = "custom" // RewriteInput.Instruction says what
)

// WriteMode is the kind of compose window the writing bar writes for.
type WriteMode string

const (
	WriteNew      WriteMode = "new"
	WriteReply    WriteMode = "reply"
	WriteReplyAll WriteMode = "replyAll"
	WriteForward  WriteMode = "forward"
	WriteEdit     WriteMode = "edit"
)

// PromptContext is what a panel conversation is about: the selected
// messages of an account, or a folder. Added marks a context added to a
// running conversation.
type PromptContext struct {
	AccountID  AccountID   `json:"accountId"`
	MessageIDs []MessageID `json:"messageIds,omitempty"`
	FolderID   FolderID    `json:"folderId,omitempty"`
	Added      bool        `json:"added,omitempty"`
}

// RewriteInput is a compose rewrite: the preset (or Instruction with
// RewriteCustom) and the passage.
type RewriteInput struct {
	Preset      RewritePreset `json:"preset"`
	Instruction string        `json:"instruction,omitempty"`
	Passage     string        `json:"passage"`
}

// WriteInput is the writing bar's request: the kind of window, the
// user's optional instruction, the subject, the user's own text in the
// editor and, for a reply or forward, the text of the prepared original.
type WriteInput struct {
	Mode        WriteMode `json:"mode"`
	Instruction string    `json:"instruction,omitempty"`
	Subject     string    `json:"subject,omitempty"`
	Own         string    `json:"own,omitempty"`
	Quote       string    `json:"quote,omitempty"`
}

// SearchInput is the search in the user's own words.
type SearchInput struct {
	Words string `json:"words"`
}

// TriageInput asks for a triage of at most MaxCases cases; Drafts lets
// the model make suggested replies (a manual run).
type TriageInput struct {
	MaxCases int  `json:"maxCases"`
	Drafts   bool `json:"drafts"`
}

// ReplyInput asks for a suggested reply to MessageID of a board case of
// AccountID; Others are the case's other newest members (ids only),
// Instruction the user's optional line, FollowUp the "suggested
// follow-up" variant (the user waits for someone).
type ReplyInput struct {
	AccountID   AccountID   `json:"accountId"`
	MessageID   MessageID   `json:"messageId"`
	Others      []MessageID `json:"others,omitempty"`
	Instruction string      `json:"instruction,omitempty"`
	FollowUp    bool        `json:"followUp,omitempty"`
}

// PromptInput is the input of one turn: the user's text (panel) and the
// context it is about, or the structured input of a one-shot purpose.
// Exactly the fields of the purpose are set.
type PromptInput struct {
	Text    string         `json:"text,omitempty"`
	Context *PromptContext `json:"context,omitempty"`
	Rewrite *RewriteInput  `json:"rewrite,omitempty"`
	Write   *WriteInput    `json:"write,omitempty"`
	Search  *SearchInput   `json:"search,omitempty"`
	Triage  *TriageInput   `json:"triage,omitempty"`
	Reply   *ReplyInput    `json:"reply,omitempty"`
}

// PromptResult is what the daemon built: the system prompt (with the
// user's profile appended), the message of the turn when an input was
// given, the bridge tools the model may call (bare names), the
// arguments the bridge is started with, and the JSON schema of the
// answer (search).
type PromptResult struct {
	SystemPrompt string          `json:"systemPrompt"`
	Message      string          `json:"message,omitempty"`
	Tools        []string        `json:"tools,omitempty"`
	BridgeArgs   []string        `json:"bridgeArgs,omitempty"`
	JSONSchema   json.RawMessage `json:"jsonSchema,omitempty"`
}

// AssistantPromptParams asks for the prompt of a purpose. Language is the
// client's UI language (BCP 47) the model answers in; AccountID, for a
// one-shot purpose, the account whose profile note is appended; Trigger
// the triage run's trigger (manual or auto) and RunID its board run
// (board.runStart), which the bridge arguments carry; Input the turn's
// input (the message is built only with it).
type AssistantPromptParams struct {
	Purpose   PromptPurpose `json:"purpose"`
	Language  string        `json:"language"`
	AccountID AccountID     `json:"accountId,omitempty"`
	Trigger   BoardTrigger  `json:"trigger,omitempty"`
	RunID     string        `json:"runId,omitempty"`
	Input     *PromptInput  `json:"input,omitempty"`
}

// ---------------------------------------------------------------------------
// Sessions.
// ---------------------------------------------------------------------------

// AssistantSessionStartParams opens a session over an ai account. Bridge
// is the absolute path of the MCP bridge the daemon starts for the
// tools (purposes with tools); NoteAccountID the mail or Jira account
// whose profile note is appended (a compose window's account; absent =
// none); RunID the board run of a triage session (board.runStart);
// Input, when given, is sent as the first turn at once (a one-shot
// purpose always gives it; a reply's Input.Reply.MessageID is what the
// bridge is confined to); TimeoutSeconds bounds one turn (0 = the
// purpose's default).
type AssistantSessionStartParams struct {
	AccountID      AccountID     `json:"accountId"`
	Purpose        PromptPurpose `json:"purpose"`
	Language       string        `json:"language"`
	Bridge         string        `json:"bridge,omitempty"`
	NoteAccountID  AccountID     `json:"noteAccountId,omitempty"`
	Trigger        BoardTrigger  `json:"trigger,omitempty"`
	RunID          string        `json:"runId,omitempty"`
	Input          *PromptInput  `json:"input,omitempty"`
	TimeoutSeconds int           `json:"timeoutSeconds,omitempty"`
}

// AssistantSessionStartResult names the session and lists the tools the
// model has (bare names).
type AssistantSessionStartResult struct {
	SessionID string   `json:"sessionId"`
	Tools     []string `json:"tools,omitempty"`
}

// AssistantSessionSendParams sends a turn.
type AssistantSessionSendParams struct {
	SessionID string      `json:"sessionId"`
	Input     PromptInput `json:"input"`
}

type AssistantSessionSendResult struct{}

type AssistantSessionStopParams struct {
	SessionID string `json:"sessionId"`
}

type AssistantSessionStopResult struct{}

// AssistantProviderModelsParams lists the models of an ai account's
// provider; Refresh ignores the daemon's cache.
type AssistantProviderModelsParams struct {
	AccountID AccountID `json:"accountId"`
	Refresh   bool      `json:"refresh,omitempty"`
}

type AssistantProviderModelsResult struct {
	Models []AIModel `json:"models"`
}

// AssistantUsage is the token usage of a turn, a session or a day.
type AssistantUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
}

// Add returns the sum of u and o.
func (u AssistantUsage) Add(o AssistantUsage) AssistantUsage {
	return AssistantUsage{
		InputTokens:              u.InputTokens + o.InputTokens,
		OutputTokens:             u.OutputTokens + o.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens + o.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens + o.CacheCreationInputTokens,
	}
}

// AssistantUsageParams asks for the usage of the last Days local days
// (0 = 1: today), of one ai account or of all.
type AssistantUsageParams struct {
	AccountID AccountID `json:"accountId,omitempty"`
	Days      int       `json:"days,omitempty"`
}

// AssistantUsageDay is one account's usage on one local day
// ("2026-11-07"), summed over its models.
type AssistantUsageDay struct {
	Day       string    `json:"day"`
	AccountID AccountID `json:"accountId"`
	AssistantUsage
	Sessions int `json:"sessions"`
	Turns    int `json:"turns"`
}

type AssistantUsageResult struct {
	Days []AssistantUsageDay `json:"days"`
}

// ---------------------------------------------------------------------------
// Events: notify.assistantEvent, sent to the session's connection only.
// ---------------------------------------------------------------------------

// AssistantEventKind is what an event carries.
type AssistantEventKind string

const (
	// EventInit starts a turn: Tools lists the tools the model has.
	EventInit AssistantEventKind = "init"
	// EventTextDelta is a piece of the answer as it streams (Text).
	EventTextDelta AssistantEventKind = "textDelta"
	// EventText is a whole block of the answer (Text), after its deltas.
	EventText AssistantEventKind = "text"
	// EventToolUse: the model called Tool (ID is the call's id).
	EventToolUse AssistantEventKind = "toolUse"
	// EventToolResult: the bridge answered the call ID with Text, the
	// bridge's result verbatim (an error when IsError).
	EventToolResult AssistantEventKind = "toolResult"
	// EventResult ends a turn (Result).
	EventResult AssistantEventKind = "result"
	// EventFailure: the turn or the session failed (Failure); a session
	// failure ends the session.
	EventFailure AssistantEventKind = "failure"
	// EventUsage reports the tokens of the turn so far (Usage); the last
	// one, right before EventResult, is the turn's whole usage.
	EventUsage AssistantEventKind = "usage"
)

// AssistantFailureClass is why a turn or session failed; clients word
// it, the daemon's Message is not for display.
type AssistantFailureClass string

const (
	FailureAuth             AssistantFailureClass = "auth"             // the key was refused (401)
	FailurePermission       AssistantFailureClass = "permission"       // the key may not use this (403)
	FailureRateLimit        AssistantFailureClass = "rateLimit"        // too many requests (429); RetryAfter when known
	FailureOverloaded       AssistantFailureClass = "overloaded"       // the provider is busy (529, 503)
	FailureQuota            AssistantFailureClass = "quota"            // the account has no credit or used up its quota (Anthropic billing, OpenAI insufficient_quota); never retried
	FailureModel            AssistantFailureClass = "model"            // the server has no such model or could not load it (LM Studio "Failed to load model", Ollama "model not found")
	FailureInvalidRequest   AssistantFailureClass = "invalidRequest"   // the provider refused the request (400)
	FailureToolsUnsupported AssistantFailureClass = "toolsUnsupported" // the model cannot call tools
	FailureNetwork          AssistantFailureClass = "network"          // no connection, timeout, TLS
	FailureProtocol         AssistantFailureClass = "protocol"         // the provider's answer could not be read
	FailureContextFull      AssistantFailureClass = "contextFull"      // the history grew past MaxAssistantHistoryBytes
	FailureBridge           AssistantFailureClass = "bridge"           // the MCP bridge could not be started or died
	FailureTimeout          AssistantFailureClass = "timeout"          // the turn exceeded its timeout
	FailureLimit            AssistantFailureClass = "limit"            // the turn exceeded MaxAssistantTurnTools tool rounds
	FailureStopped          AssistantFailureClass = "stopped"          // assistant.sessionStop, the connection closed, the daemon stops
)

// AssistantResult is the end of a turn: whether the model finished
// (Success; false after a failure or a refusal), Structured the answer
// when a JSON schema was asked for and the answer is valid JSON, Reason
// the model's stop reason as a word ("endTurn", "maxTokens", "refusal").
type AssistantResult struct {
	Success    bool            `json:"success"`
	Structured json.RawMessage `json:"structured,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

// AssistantFailure is the class of a failure, RetryAfter seconds when
// the provider said so, and the daemon's technical message.
type AssistantFailure struct {
	Class      AssistantFailureClass `json:"class"`
	RetryAfter int                   `json:"retryAfter,omitempty"`
	Message    string                `json:"message,omitempty"`
}

// AssistantEvent is the params of notify.assistantEvent.
type AssistantEvent struct {
	SessionID string             `json:"sessionId"`
	Kind      AssistantEventKind `json:"kind"`
	Text      string             `json:"text,omitempty"`
	Tools     []string           `json:"tools,omitempty"`
	Tool      string             `json:"tool,omitempty"`
	ID        string             `json:"id,omitempty"`
	IsError   bool               `json:"isError,omitempty"`
	Result    *AssistantResult   `json:"result,omitempty"`
	Failure   *AssistantFailure  `json:"failure,omitempty"`
	Usage     *AssistantUsage    `json:"usage,omitempty"`
}

// AssistantUsageChangedNotification: an ai account's usage of today
// changed; clients run assistant.usage again.
type AssistantUsageChangedNotification struct {
	AccountID AccountID `json:"accountId"`
}
