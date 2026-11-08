// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// InAppArg is the bridge's first argument whenever Malachi Mail itself
// starts it, from a client or from the daemon: the prompts built here
// carry the user's profile (WithProfile), so the bridge keeps it out of
// its own instructions. Every BridgeArgs of Build starts with it.
const InAppArg = "--in-app"

// Options are what Build needs beside the input.
type Options struct {
	// Language is the client's UI language as a BCP 47 tag ("cs",
	// "en-GB"; a POSIX locale passes too), the language the model answers
	// in and writes the triage's notes in. LanguageName turns it into the
	// English name the prompts say; a language without a catalog, and
	// "", is English.
	Language string
	// Today is the user's local date as YYYY-MM-DD ("2026-11-07",
	// time.Time.Format("2006-01-02")), as the prompts show it.
	Today string
	// Trigger is what starts the triage run (triage only): manual, auto,
	// or "" for a run the input describes fully. An automatic run makes
	// no drafts whatever the input says (TriageAutomaticDrafts).
	Trigger api.BoardTrigger
	// RunID is the board run of the triage (triage only; board.runStart),
	// which the bridge's arguments carry; required.
	RunID string
	// Profile is the user's stored profile (assistant.profileGet) and
	// Accounts the accounts that name its notes (account.list).
	Profile  api.AssistantProfile
	Accounts []api.Account
	// AccountID is the account whose profile note is appended for the
	// one-shot purposes of a compose window (rewrite, write): the account
	// the window writes from. A reply uses its input's account instead.
	AccountID api.AccountID
}

// Build is what the model reads for purpose: the system prompt with the
// user's profile appended as each purpose takes it, the message of the
// turn when in is given, the bare names of the bridge tools the model may
// call, the arguments the bridge is started with (InAppArg first) and the
// JSON schema of the answer (search). It composes exactly what the
// clients composed themselves before the prompts moved to the daemon
// (the golden file of ui/internal/assistant).
//
// in may be nil for the panel (the system prompt of a new conversation,
// no message) and must be given for the other purposes. Every free text
// of the input is cleaned first: invalid UTF-8 becomes U+FFFD and the
// control characters other than "\n" and "\t" are dropped. An unknown
// purpose, a field of another purpose, a missing or empty input (an
// empty text, passage or words), an unknown preset or mode, a text
// longer than api.MaxPromptTextBytes, more than api.MaxPromptMessageIDs
// ids, an id with white space, a triage without a run id or with a
// trigger other than manual or auto, and a reply without an account or a
// message are invalidArgument.
func Build(purpose api.PromptPurpose, in *api.PromptInput, o Options) (api.PromptResult, error) {
	if !purpose.Valid() {
		return api.PromptResult{}, invalid("unknown purpose %q", purpose)
	}
	if in == nil && purpose != api.PurposePanel {
		return api.PromptResult{}, invalid("%s needs an input", purpose)
	}
	if in != nil {
		if err := onlyOf(purpose, in); err != nil {
			return api.PromptResult{}, err
		}
	}
	profile := ProfileFrom(o.Profile, o.Accounts)
	language := LanguageName(o.Language)
	switch purpose {
	case api.PurposePanel:
		return buildPanel(in, o, profile, language)
	case api.PurposeRewrite:
		return buildRewrite(in.Rewrite, o, profile)
	case api.PurposeWrite:
		return buildWrite(in.Write, o, profile)
	case api.PurposeSearch:
		return buildSearch(in.Search, o, profile)
	case api.PurposeTriage:
		return buildTriage(in.Triage, o, profile, language)
	case api.PurposeReply:
		return buildReply(in.Reply, profile)
	}
	return api.PromptResult{}, invalid("unknown purpose %q", purpose)
}

func buildPanel(in *api.PromptInput, o Options, profile Profile, language string) (api.PromptResult, error) {
	r := api.PromptResult{
		SystemPrompt: WithProfile(SystemPrompt(language, o.Today), profile),
		Tools:        slices.Clone(AllowedTools),
		BridgeArgs:   []string{InAppArg},
	}
	if in == nil {
		return r, nil
	}
	text, err := cleanText("text", in.Text)
	if err != nil {
		return api.PromptResult{}, err
	}
	if strings.TrimSpace(text) == "" {
		return api.PromptResult{}, invalid("an empty text")
	}
	if c := in.Context; c != nil {
		if err := checkID("context.accountId", string(c.AccountID), true); err != nil {
			return api.PromptResult{}, err
		}
		if err := checkIDs("context.messageIds", c.MessageIDs); err != nil {
			return api.PromptResult{}, err
		}
	}
	r.Message = PanelMessage(in.Context, text)
	return r, nil
}

func buildRewrite(rw *api.RewriteInput, o Options, profile Profile) (api.PromptResult, error) {
	instruction, err := cleanText("rewrite.instruction", rw.Instruction)
	if err != nil {
		return api.PromptResult{}, err
	}
	passage, err := cleanText("rewrite.passage", rw.Passage)
	if err != nil {
		return api.PromptResult{}, err
	}
	message, err := RewriteMessage(rw.Preset, instruction, passage)
	if err != nil {
		return api.PromptResult{}, invalid("%s", err)
	}
	return api.PromptResult{
		SystemPrompt: WithProfile(RewriteSystemPrompt(), profile.For(o.AccountID)),
		Message:      message,
	}, nil
}

func buildWrite(w *api.WriteInput, o Options, profile Profile) (api.PromptResult, error) {
	fields := [4]string{w.Instruction, w.Subject, w.Own, w.Quote}
	for i, name := range [4]string{"write.instruction", "write.subject", "write.own", "write.quote"} {
		s, err := cleanText(name, fields[i])
		if err != nil {
			return api.PromptResult{}, err
		}
		fields[i] = s
	}
	message, err := WriteMessage(w.Mode, fields[0], fields[1], fields[2], fields[3])
	if err != nil {
		return api.PromptResult{}, invalid("%s", err)
	}
	return api.PromptResult{
		SystemPrompt: WithProfile(WriteSystemPrompt(), profile.For(o.AccountID)),
		Message:      message,
	}, nil
}

func buildSearch(s *api.SearchInput, o Options, profile Profile) (api.PromptResult, error) {
	words, err := cleanText("search.words", s.Words)
	if err != nil {
		return api.PromptResult{}, err
	}
	message, err := SearchMessage(words)
	if err != nil {
		return api.PromptResult{}, invalid("%s", err)
	}
	return api.PromptResult{
		SystemPrompt: WithProfile(SearchSystemPrompt(o.Today), profile.Global()),
		Message:      message,
		JSONSchema:   json.RawMessage(SearchSchema),
	}, nil
}

func buildTriage(t *api.TriageInput, o Options, profile Profile, language string) (api.PromptResult, error) {
	if o.RunID == "" {
		return api.PromptResult{}, invalid("a triage needs the run id")
	}
	if len(o.RunID) > api.MaxAssistantRunIDBytes {
		return api.PromptResult{}, invalid("the run id is longer than %d bytes", api.MaxAssistantRunIDBytes)
	}
	if err := checkID("runId", o.RunID, false); err != nil {
		return api.PromptResult{}, err
	}
	switch o.Trigger {
	case "", api.TriggerManual, api.TriggerAuto:
	default:
		return api.PromptResult{}, invalid("a triage's trigger is manual or auto, not %q", o.Trigger)
	}
	drafts := t.Drafts && (o.Trigger != api.TriggerAuto || TriageAutomaticDrafts)
	n := ClampTriageMax(t.MaxCases)
	return api.PromptResult{
		SystemPrompt: WithProfile(TriageSystemPrompt(language, o.Today), profile),
		Message:      TriageMessage(n, drafts),
		Tools:        TriageToolsWith(drafts),
		BridgeArgs:   append([]string{InAppArg}, TriageBridgeArgs(o.RunID, n)...),
	}, nil
}

func buildReply(rp *api.ReplyInput, profile Profile) (api.PromptResult, error) {
	if err := checkID("reply.accountId", string(rp.AccountID), false); err != nil {
		return api.PromptResult{}, err
	}
	if err := checkID("reply.messageId", string(rp.MessageID), false); err != nil {
		return api.PromptResult{}, err
	}
	if err := checkIDs("reply.others", rp.Others); err != nil {
		return api.PromptResult{}, err
	}
	if len(rp.Instruction) > api.MaxPromptTextBytes {
		return api.PromptResult{}, invalid("reply.instruction is longer than %d bytes", api.MaxPromptTextBytes)
	}
	others := make([]string, len(rp.Others))
	for i, id := range rp.Others {
		others[i] = string(id)
	}
	// SuggestReplyMessage cleans the instruction itself
	// (CleanSuggestReplyInstruction), as the clients did.
	return api.PromptResult{
		SystemPrompt: WithProfile(SuggestReplySystemPromptFor(rp.FollowUp), profile.For(rp.AccountID)),
		Message:      SuggestReplyMessage(string(rp.AccountID), string(rp.MessageID), others, rp.Instruction),
		Tools:        slices.Clone(SuggestReplyTools),
		BridgeArgs:   append([]string{InAppArg}, SuggestReplyBridgeArgs(string(rp.MessageID))...),
	}, nil
}

// onlyOf is an error when in carries a field of another purpose than
// purpose, or lacks the one of a one-shot purpose.
func onlyOf(purpose api.PromptPurpose, in *api.PromptInput) error {
	fields := []struct {
		of  api.PromptPurpose
		set bool
	}{
		{api.PurposePanel, in.Text != "" || in.Context != nil},
		{api.PurposeRewrite, in.Rewrite != nil},
		{api.PurposeWrite, in.Write != nil},
		{api.PurposeSearch, in.Search != nil},
		{api.PurposeTriage, in.Triage != nil},
		{api.PurposeReply, in.Reply != nil},
	}
	for _, f := range fields {
		if f.set && f.of != purpose {
			return invalid("the input's %s field is not the %s's", f.of, purpose)
		}
		if !f.set && f.of == purpose && purpose != api.PurposePanel {
			return invalid("%s needs its %s input", purpose, purpose)
		}
	}
	return nil
}

// cleanText is s as a prompt takes it: valid UTF-8 (U+FFFD for what is
// not) without control characters other than "\n" and "\t"; an error
// when s is longer than api.MaxPromptTextBytes.
func cleanText(field, s string) (string, error) {
	if len(s) > api.MaxPromptTextBytes {
		return "", invalid("%s is longer than %d bytes", field, api.MaxPromptTextBytes)
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	if !strings.ContainsFunc(s, droppedControl) {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !droppedControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}

// droppedControl says whether r is a control character a prompt drops.
func droppedControl(r rune) bool {
	return unicode.IsControl(r) && r != '\n' && r != '\t'
}

// checkID is an error when id holds white space, a control character or
// invalid UTF-8, or is empty unless mayBeEmpty.
func checkID(field, id string, mayBeEmpty bool) error {
	if id == "" {
		if mayBeEmpty {
			return nil
		}
		return invalid("%s is empty", field)
	}
	if !utf8.ValidString(id) {
		return invalid("%s is not valid UTF-8", field)
	}
	for _, r := range id {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return invalid("%s holds white space or a control character", field)
		}
	}
	return nil
}

// checkIDs is an error when ids are more than api.MaxPromptMessageIDs or
// one of them fails checkID (an empty one is allowed and left out of the
// prompts).
func checkIDs(field string, ids []api.MessageID) error {
	if len(ids) > api.MaxPromptMessageIDs {
		return invalid("%s has %d ids, at most %d", field, len(ids), api.MaxPromptMessageIDs)
	}
	for _, id := range ids {
		if err := checkID(field, string(id), true); err != nil {
			return err
		}
	}
	return nil
}

// invalid is an invalidArgument error.
func invalid(format string, args ...any) error {
	return api.NewError(api.CodeInvalidArgument, format, args...)
}
