// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package prompttest is a stand-in for the daemon's assistant.prompt in
// the tests of the clients' assistant packages (assistantpanel,
// boardtriage, boardreply, chatgpt, the window): a Prompter that records
// its calls and answers with Build, a fixed, computable shape of what the
// daemon builds (backend/internal/assistant: the same context lines and
// bridge arguments, invented system prompts and one-shot messages, the
// same refusals of an empty or too long input). Nothing here is used
// outside tests.
package prompttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The limits the daemon's prompts keep (backend/internal/assistant).
const (
	MaxPassage     = 20000
	MaxSearchWords = 500
)

// SearchSchema is the daemon's schema of the search's answer.
const SearchSchema = `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`

// The bare tools of each purpose (backend/internal/assistant tools.go).
var (
	PanelTools = []string{"list_accounts", "list_folders", "list_messages", "search_messages", "read_message",
		"get_attachment", "create_draft", "list_calendars", "list_events", "get_event", "find_free_time",
		"list_invitations", "get_user_profile", "propose_profile_note"}
	TriageTools = []string{"list_accounts", "list_folders", "list_messages", "search_messages", "read_message",
		"get_attachment", "create_draft", "list_triage_queue", "annotate_case", "add_commitment"}
	ReplyTools = []string{"read_message", "list_messages", "create_draft"}
)

// Prompter records every call and answers with Build, or with Err when
// set. It may be called from any goroutine.
type Prompter struct {
	mu    sync.Mutex
	calls []api.AssistantPromptParams
	// Err, when set, fails every call.
	Err error
}

// Prompt is assistantpanel.Prompter.
func (p *Prompter) Prompt(_ context.Context, params api.AssistantPromptParams) (api.PromptResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, params)
	err := p.Err
	p.mu.Unlock()
	if err != nil {
		return api.PromptResult{}, err
	}
	return Build(params)
}

// Calls are the calls so far, in order (a copy).
func (p *Prompter) Calls() []api.AssistantPromptParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// Last is the last call; false before one.
func (p *Prompter) Last() (api.AssistantPromptParams, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return api.AssistantPromptParams{}, false
	}
	return p.calls[len(p.calls)-1], true
}

// SystemPrompt is the invented system prompt of params: the purpose, the
// language and, for a purpose that appends an account's note, the
// account ("SYS rewrite cs note:acc_1"); the search never has one.
func SystemPrompt(params api.AssistantPromptParams) string {
	s := "SYS " + string(params.Purpose) + " " + params.Language
	note := params.AccountID
	if params.Purpose == api.PurposeReply && params.Input != nil && params.Input.Reply != nil {
		note = params.Input.Reply.AccountID
	}
	if note != "" && params.Purpose != api.PurposeSearch {
		s += " note:" + string(note)
	}
	return s
}

// Must is Build without the error (the test fails otherwise through the
// panic).
func Must(params api.AssistantPromptParams) api.PromptResult {
	r, err := Build(params)
	if err != nil {
		panic(err)
	}
	return r
}

// Build is what the fake daemon builds for params (see the package
// comment).
func Build(params api.AssistantPromptParams) (api.PromptResult, error) {
	r := api.PromptResult{SystemPrompt: SystemPrompt(params)}
	in := params.Input
	switch params.Purpose {
	case api.PurposePanel:
		r.Tools = slices.Clone(PanelTools)
		r.BridgeArgs = []string{"--in-app"}
		if in == nil {
			return r, nil
		}
		if strings.TrimSpace(in.Text) == "" {
			return api.PromptResult{}, invalid("an empty text")
		}
		r.Message = PanelMessage(in.Context, in.Text)
	case api.PurposeRewrite:
		if in == nil || in.Rewrite == nil {
			return api.PromptResult{}, invalid("rewrite needs its rewrite input")
		}
		m, err := RewriteMessage(*in.Rewrite)
		if err != nil {
			return api.PromptResult{}, err
		}
		r.Message = m
	case api.PurposeWrite:
		if in == nil || in.Write == nil {
			return api.PromptResult{}, invalid("write needs its write input")
		}
		m, err := WriteMessage(*in.Write)
		if err != nil {
			return api.PromptResult{}, err
		}
		r.Message = m
	case api.PurposeSearch:
		if in == nil || in.Search == nil {
			return api.PromptResult{}, invalid("search needs its search input")
		}
		m, err := SearchMessage(in.Search.Words)
		if err != nil {
			return api.PromptResult{}, err
		}
		r.Message = m
		r.JSONSchema = json.RawMessage(SearchSchema)
	case api.PurposeTriage:
		if in == nil || in.Triage == nil {
			return api.PromptResult{}, invalid("triage needs its triage input")
		}
		if params.RunID == "" {
			return api.PromptResult{}, invalid("a triage needs the run id")
		}
		drafts := in.Triage.Drafts && params.Trigger != api.TriggerAuto
		n := min(max(in.Triage.MaxCases, 1), 200)
		r.Message = TriageMessage(n, drafts)
		r.Tools = triageTools(drafts)
		r.BridgeArgs = TriageBridgeArgs(params.RunID, n)
	case api.PurposeReply:
		if in == nil || in.Reply == nil {
			return api.PromptResult{}, invalid("reply needs its reply input")
		}
		rp := in.Reply
		if rp.AccountID == "" || rp.MessageID == "" {
			return api.PromptResult{}, invalid("reply.accountId or reply.messageId is empty")
		}
		r.Message = ReplyMessage(*rp)
		r.Tools = slices.Clone(ReplyTools)
		r.BridgeArgs = []string{"--in-app", "--reply-only", string(rp.MessageID)}
	default:
		return api.PromptResult{}, invalid("unknown purpose %q", params.Purpose)
	}
	return r, nil
}

// PanelMessage is the daemon's message of a panel turn: its context line
// (the daemon's wording) and the text.
func PanelMessage(c *api.PromptContext, text string) string {
	if c == nil || c.AccountID == "" {
		return text
	}
	var ids []string
	for _, id := range c.MessageIDs {
		if id != "" && len(ids) < api.MaxPromptMessageIDs {
			ids = append(ids, string(id))
		}
	}
	var line string
	switch {
	case len(ids) == 0:
		return text
	case len(ids) == 1 && !c.Added:
		line = fmt.Sprintf("Context: the user has selected message %s in account %s.", ids[0], c.AccountID)
	case !c.Added:
		line = fmt.Sprintf("Context: the user has selected a conversation with messages %s (newest first) in account %s.", strings.Join(ids, ", "), c.AccountID)
	case len(ids) == 1:
		line = fmt.Sprintf("Context: the user has also selected message %s in account %s; questions from now on may be about it too.", ids[0], c.AccountID)
	default:
		line = fmt.Sprintf("Context: the user has also selected a conversation with messages %s (newest first) in account %s; questions from now on may be about it too.", strings.Join(ids, ", "), c.AccountID)
	}
	return line + "\n\n" + text
}

// RewriteMessage is the invented message of a rewrite: the preset, the
// instruction in brackets and the passage trimmed; the daemon's refusals.
func RewriteMessage(in api.RewriteInput) (string, error) {
	passage := strings.TrimSpace(in.Passage)
	if passage == "" {
		return "", invalid("an empty passage")
	}
	if n := utf8.RuneCountInString(passage); n > MaxPassage {
		return "", invalid("the passage is too long: %d characters, at most %d", n, MaxPassage)
	}
	if in.Preset == api.RewriteCustom && strings.TrimSpace(in.Instruction) == "" {
		return "", invalid("an empty instruction")
	}
	return "REWRITE " + string(in.Preset) + " [" + strings.TrimSpace(in.Instruction) + "]\n" + passage, nil
}

// WriteMessage is the invented message of the writing bar.
func WriteMessage(in api.WriteInput) (string, error) {
	if strings.TrimSpace(in.Instruction) == "" && in.Own == "" && in.Quote == "" {
		return "", invalid("an empty passage")
	}
	if n := utf8.RuneCountInString(in.Instruction + in.Subject + in.Own + in.Quote); n > MaxPassage {
		return "", invalid("the passage is too long: %d characters, at most %d", n, MaxPassage)
	}
	return "WRITE " + string(in.Mode) + " [" + in.Instruction + "] " + in.Subject + "|" + in.Own + "|" + in.Quote, nil
}

// SearchMessage is the message of the search: the words trimmed, as the
// daemon's.
func SearchMessage(words string) (string, error) {
	words = strings.TrimSpace(words)
	if words == "" {
		return "", invalid("no words to search for")
	}
	if n := utf8.RuneCountInString(words); n > MaxSearchWords {
		return "", invalid("the words are too long: %d characters, at most %d", n, MaxSearchWords)
	}
	return words, nil
}

// TriageMessage is the invented message of a triage run.
func TriageMessage(maxCases int, drafts bool) string {
	return "TRIAGE " + strconv.Itoa(maxCases) + " drafts=" + strconv.FormatBool(drafts)
}

// TriageBridgeArgs are the daemon's bridge arguments of a triage run.
func TriageBridgeArgs(runID string, maxCases int) []string {
	return []string{"--in-app", "--allow-triage", "--triage-run", runID, "--triage-max", strconv.Itoa(maxCases)}
}

func triageTools(drafts bool) []string {
	out := make([]string, 0, len(TriageTools))
	for _, t := range TriageTools {
		if drafts || t != "create_draft" {
			out = append(out, t)
		}
	}
	return out
}

// ReplyMessage is the invented message of a suggested reply.
func ReplyMessage(in api.ReplyInput) string {
	others := make([]string, len(in.Others))
	for i, id := range in.Others {
		others[i] = string(id)
	}
	s := "REPLY " + string(in.AccountID) + " " + string(in.MessageID) + " [" + strings.Join(others, ",") + "] " + strings.TrimSpace(in.Instruction)
	if in.FollowUp {
		s += " follow-up"
	}
	return s
}

// invalid is an invalidArgument error, as the daemon's.
func invalid(format string, args ...any) error {
	return api.NewError(api.CodeInvalidArgument, format, args...)
}

// ErrInvalid says whether err is the daemon's invalidArgument.
func ErrInvalid(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && e.Code == api.CodeInvalidArgument
}
