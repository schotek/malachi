// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// fxOptions are the options of the Build tests: a Czech UI, a profile
// with a note for acc_1 and the names of two accounts.
func fxOptions() Options {
	return Options{
		Language: "cs",
		Today:    "2026-11-07",
		Profile: api.AssistantProfile{
			Text: "Píšu stručně.", Gender: api.GenderMasculine,
			Accounts: []api.ProfileAccountNote{{AccountID: "acc_1", Note: "Podepisuj jako studio."}},
		},
		Accounts: []api.Account{
			{ID: "acc_1", Config: api.AccountConfig{Name: "Studio", Email: "studio@example.org"}},
			{ID: "acc_2", Config: api.AccountConfig{Name: "Osobní", Email: "me@example.org"}},
		},
		AccountID: "acc_1",
	}
}

// invalidArgument says whether err is the invalidArgument error.
func invalidArgument(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && e.Code == api.CodeInvalidArgument
}

func TestBuildPanel(t *testing.T) {
	o := fxOptions()
	profile := ProfileFrom(o.Profile, o.Accounts)
	// A new conversation: the system prompt alone, with every note.
	r, err := Build(api.PurposePanel, nil, o)
	if err != nil {
		t.Fatal(err)
	}
	if want := WithProfile(SystemPrompt("Czech", "2026-11-07"), profile); r.SystemPrompt != want {
		t.Errorf("system prompt\n%q\nwant\n%q", r.SystemPrompt, want)
	}
	if !strings.Contains(r.SystemPrompt, "Answer in Czech") || !strings.Contains(r.SystemPrompt, `For the account "Studio" (studio@example.org):`) {
		t.Errorf("system prompt %q", r.SystemPrompt)
	}
	if r.Message != "" || !slices.Equal(r.Tools, AllowedTools) || !slices.Equal(r.BridgeArgs, []string{"--in-app"}) || r.JSONSchema != nil {
		t.Errorf("new conversation: %+v", r)
	}
	// The tools are a copy.
	r.Tools[0] = "x"
	if AllowedTools[0] == "x" {
		t.Error("Build shares AllowedTools")
	}
	// The first question about a selection, an added selection, and one
	// without a context.
	for _, tt := range []struct {
		name string
		in   *api.PromptInput
		want string
	}{
		{"first", &api.PromptInput{Text: "Shrň to.", Context: &api.PromptContext{AccountID: "acc_1", MessageIDs: []api.MessageID{"m_2", "m_1"}}},
			"Context: the user has selected a conversation with messages m_2, m_1 (newest first) in account acc_1.\n\nShrň to."},
		{"added", &api.PromptInput{Text: "A tohle?", Context: &api.PromptContext{AccountID: "acc_1", MessageIDs: []api.MessageID{"m_3"}, Added: true}},
			"Context: the user has also selected message m_3 in account acc_1; questions from now on may be about it too.\n\nA tohle?"},
		{"no context", &api.PromptInput{Text: "Kolik je hodin?"}, "Kolik je hodin?"},
		{"a context without ids", &api.PromptInput{Text: "Q", Context: &api.PromptContext{AccountID: "acc_1"}}, "Q"},
		{"cleaned", &api.PromptInput{Text: "a\x00b\x07\r\nc\td\xff"}, "a\nb\nc\td\uFFFD"[:0] + "ab\nc\td\uFFFD"},
	} {
		r, err := Build(api.PurposePanel, tt.in, o)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if r.Message != tt.want {
			t.Errorf("%s: message %q, want %q", tt.name, r.Message, tt.want)
		}
	}
	// An English UI, and one without a catalog.
	for _, lang := range []string{"", "en_GB.UTF-8", "de"} {
		o := o
		o.Language = lang
		r, err := Build(api.PurposePanel, nil, o)
		if err != nil || !strings.Contains(r.SystemPrompt, "Answer in English unless") {
			t.Errorf("%q: %v %q", lang, err, r.SystemPrompt)
		}
	}
}

func TestBuildPanelErrors(t *testing.T) {
	o := fxOptions()
	for _, tt := range []struct {
		name string
		in   *api.PromptInput
	}{
		{"an empty text", &api.PromptInput{Text: " \n"}},
		{"a text of another purpose", &api.PromptInput{Text: "Q", Search: &api.SearchInput{Words: "x"}}},
		{"too many ids", &api.PromptInput{Text: "Q", Context: &api.PromptContext{AccountID: "acc_1", MessageIDs: ids(api.MaxPromptMessageIDs + 1)}}},
		{"an id with a space", &api.PromptInput{Text: "Q", Context: &api.PromptContext{AccountID: "acc_1", MessageIDs: []api.MessageID{"m 1"}}}},
		{"an account with a newline", &api.PromptInput{Text: "Q", Context: &api.PromptContext{AccountID: "acc\n1"}}},
		{"a long text", &api.PromptInput{Text: strings.Repeat("a", api.MaxPromptTextBytes+1)}},
	} {
		if _, err := Build(api.PurposePanel, tt.in, o); !invalidArgument(err) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	// At the limit it passes.
	if _, err := Build(api.PurposePanel, &api.PromptInput{Text: strings.Repeat("a", api.MaxPromptTextBytes)}, o); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	if _, err := Build(api.PromptPurpose("chat"), nil, o); !invalidArgument(err) {
		t.Errorf("an unknown purpose: %v", err)
	}
}

func TestBuildRewrite(t *testing.T) {
	o := fxOptions()
	profile := ProfileFrom(o.Profile, o.Accounts)
	in := &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteCustom, Instruction: " Pirátsky ", Passage: "Ahoj Jano,\n\nposílám fakturu."}}
	r, err := Build(api.PurposeRewrite, in, o)
	if err != nil {
		t.Fatal(err)
	}
	if want := WithProfile(RewriteSystemPrompt(), profile.For("acc_1")); r.SystemPrompt != want {
		t.Errorf("system prompt\n%q\nwant\n%q", r.SystemPrompt, want)
	}
	if want := "Follow this instruction: Pirátsky\n\nPassage:\n<<<\nAhoj Jano,\n\nposílám fakturu.\n>>>"; r.Message != want {
		t.Errorf("message %q, want %q", r.Message, want)
	}
	if r.Tools != nil || r.BridgeArgs != nil || r.JSONSchema != nil {
		t.Errorf("a rewrite has tools, bridge args or a schema: %+v", r)
	}
	// The note of another account is not appended; an account without a
	// note gets the text and the gender only.
	o.AccountID = "acc_2"
	r, err = Build(api.PurposeRewrite, in, o)
	if err != nil || strings.Contains(r.SystemPrompt, "For the account") || !strings.Contains(r.SystemPrompt, "Píšu stručně.") {
		t.Errorf("acc_2: %v %q", err, r.SystemPrompt)
	}
	// The passage is cleaned before the message is made.
	r, err = Build(api.PurposeRewrite, &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteFix, Passage: "a\x00b\xff"}}, o)
	if err != nil || !strings.Contains(r.Message, "<<<\nab\uFFFD\n>>>") {
		t.Errorf("cleaned: %v %q", err, r.Message)
	}
	for _, tt := range []struct {
		name string
		in   *api.PromptInput
	}{
		{"nil", nil},
		{"no rewrite", &api.PromptInput{}},
		{"a field of another purpose", &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteFix, Passage: "x"}, Text: "Q"}},
		{"an unknown preset", &api.PromptInput{Rewrite: &api.RewriteInput{Preset: "louder", Passage: "x"}}},
		{"an empty passage", &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteFix, Passage: " \n"}}},
		{"custom without an instruction", &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteCustom, Passage: "x"}}},
		{"a long passage", &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteFix, Passage: strings.Repeat("a", api.MaxPromptTextBytes+1)}}},
		{"a long instruction", &api.PromptInput{Rewrite: &api.RewriteInput{Preset: api.RewriteCustom, Instruction: strings.Repeat("a", api.MaxPromptTextBytes+1), Passage: "x"}}},
	} {
		if _, err := Build(api.PurposeRewrite, tt.in, o); !invalidArgument(err) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestBuildWrite(t *testing.T) {
	o := fxOptions()
	profile := ProfileFrom(o.Profile, o.Accounts)
	in := &api.PromptInput{Write: &api.WriteInput{Mode: api.WriteReply, Instruction: "Odmítni", Subject: "Schůzka", Quote: "Přijdete?"}}
	r, err := Build(api.PurposeWrite, in, o)
	if err != nil {
		t.Fatal(err)
	}
	if want := WithProfile(WriteSystemPrompt(), profile.For("acc_1")); r.SystemPrompt != want {
		t.Errorf("system prompt\n%q\nwant\n%q", r.SystemPrompt, want)
	}
	if want, _ := WriteMessage(api.WriteReply, "Odmítni", "Schůzka", "", "Přijdete?"); r.Message != want {
		t.Errorf("message %q, want %q", r.Message, want)
	}
	if r.Tools != nil || r.BridgeArgs != nil || r.JSONSchema != nil {
		t.Errorf("a write has tools, bridge args or a schema: %+v", r)
	}
	for _, tt := range []struct {
		name string
		in   *api.PromptInput
	}{
		{"nil", nil},
		{"no write", &api.PromptInput{}},
		{"an unknown mode", &api.PromptInput{Write: &api.WriteInput{Mode: "draft", Instruction: "x"}}},
		{"nothing to work from", &api.PromptInput{Write: &api.WriteInput{Mode: api.WriteNew, Instruction: " ", Subject: "S"}}},
		{"a long quote", &api.PromptInput{Write: &api.WriteInput{Mode: api.WriteReply, Quote: strings.Repeat("a", api.MaxPromptTextBytes+1)}}},
		{"a field of another purpose", &api.PromptInput{Write: &api.WriteInput{Mode: api.WriteNew, Instruction: "x"}, Reply: &api.ReplyInput{}}},
	} {
		if _, err := Build(api.PurposeWrite, tt.in, o); !invalidArgument(err) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestBuildSearch(t *testing.T) {
	o := fxOptions()
	profile := ProfileFrom(o.Profile, o.Accounts)
	r, err := Build(api.PurposeSearch, &api.PromptInput{Search: &api.SearchInput{Words: " faktury od Jany \n"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	// The search gets the profile without any account's note.
	if want := WithProfile(SearchSystemPrompt("2026-11-07"), profile.Global()); r.SystemPrompt != want || strings.Contains(r.SystemPrompt, "For the account") {
		t.Errorf("system prompt\n%q\nwant\n%q", r.SystemPrompt, want)
	}
	if r.Message != "faktury od Jany" || string(r.JSONSchema) != SearchSchema || r.Tools != nil || r.BridgeArgs != nil {
		t.Errorf("%+v", r)
	}
	for _, tt := range []struct {
		name string
		in   *api.PromptInput
	}{
		{"nil", nil},
		{"no search", &api.PromptInput{}},
		{"no words", &api.PromptInput{Search: &api.SearchInput{Words: " \t"}}},
		{"too many words", &api.PromptInput{Search: &api.SearchInput{Words: strings.Repeat("ž", MaxSearchWords+1)}}},
	} {
		if _, err := Build(api.PurposeSearch, tt.in, o); !invalidArgument(err) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestBuildTriage(t *testing.T) {
	o := fxOptions()
	o.Trigger, o.RunID = api.TriggerManual, "run_1"
	profile := ProfileFrom(o.Profile, o.Accounts)
	r, err := Build(api.PurposeTriage, &api.PromptInput{Triage: &api.TriageInput{MaxCases: 40, Drafts: true}}, o)
	if err != nil {
		t.Fatal(err)
	}
	// Every account's note, as the panel.
	if want := WithProfile(TriageSystemPrompt("Czech", "2026-11-07"), profile); r.SystemPrompt != want {
		t.Errorf("system prompt\n%q\nwant\n%q", r.SystemPrompt, want)
	}
	if r.Message != TriageMessage(40, true) || !slices.Equal(r.Tools, TriageTools) {
		t.Errorf("manual: %+v", r)
	}
	if want := []string{"--in-app", "--allow-triage", "--triage-run", "run_1", "--triage-max", "40"}; !slices.Equal(r.BridgeArgs, want) {
		t.Errorf("bridge args %q, want %q", r.BridgeArgs, want)
	}
	// An automatic run makes no drafts, whatever the input says; the
	// limit is clamped for the message and the bridge alike.
	o.Trigger, o.RunID = api.TriggerAuto, "run_2"
	r, err = Build(api.PurposeTriage, &api.PromptInput{Triage: &api.TriageInput{MaxCases: 999, Drafts: true}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Message != TriageMessage(200, false) || slices.Contains(r.Tools, TriageDraftTool) || !slices.Equal(r.Tools, TriageToolsWith(false)) {
		t.Errorf("auto: %+v", r)
	}
	if want := []string{"--in-app", "--allow-triage", "--triage-run", "run_2", "--triage-max", "200"}; !slices.Equal(r.BridgeArgs, want) {
		t.Errorf("bridge args %q, want %q", r.BridgeArgs, want)
	}
	// Without a trigger the input decides; 0 cases is 1.
	o.Trigger = ""
	r, err = Build(api.PurposeTriage, &api.PromptInput{Triage: &api.TriageInput{MaxCases: 0, Drafts: false}}, o)
	if err != nil || r.Message != TriageMessage(1, false) || r.BridgeArgs[len(r.BridgeArgs)-1] != "1" {
		t.Errorf("no trigger: %v %+v", err, r)
	}
	for _, tt := range []struct {
		name    string
		trigger api.BoardTrigger
		runID   string
		in      *api.PromptInput
	}{
		{"nil", api.TriggerManual, "run_1", nil},
		{"no triage", api.TriggerManual, "run_1", &api.PromptInput{}},
		{"no run id", api.TriggerManual, "", &api.PromptInput{Triage: &api.TriageInput{MaxCases: 1}}},
		{"a run id with a space", api.TriggerManual, "run 1", &api.PromptInput{Triage: &api.TriageInput{MaxCases: 1}}},
		{"a long run id", api.TriggerManual, strings.Repeat("r", api.MaxAssistantRunIDBytes+1), &api.PromptInput{Triage: &api.TriageInput{MaxCases: 1}}},
		{"an external trigger", api.TriggerExternal, "run_1", &api.PromptInput{Triage: &api.TriageInput{MaxCases: 1}}},
		{"an unknown trigger", api.BoardTrigger("cron"), "run_1", &api.PromptInput{Triage: &api.TriageInput{MaxCases: 1}}},
		{"a field of another purpose", api.TriggerManual, "run_1", &api.PromptInput{Triage: &api.TriageInput{MaxCases: 1}, Text: "Q"}},
	} {
		o := o
		o.Trigger, o.RunID = tt.trigger, tt.runID
		if _, err := Build(api.PurposeTriage, tt.in, o); !invalidArgument(err) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestBuildReply(t *testing.T) {
	o := fxOptions()
	o.AccountID = "acc_2" // the input's account counts, not the option
	profile := ProfileFrom(o.Profile, o.Accounts)
	in := &api.PromptInput{Reply: &api.ReplyInput{
		AccountID: "acc_1", MessageID: "m_9", Others: []api.MessageID{"m_1", "m_9", "", "m_2"}, Instruction: " Decline\npolitely ",
	}}
	r, err := Build(api.PurposeReply, in, o)
	if err != nil {
		t.Fatal(err)
	}
	if want := WithProfile(SuggestReplySystemPromptFor(false), profile.For("acc_1")); r.SystemPrompt != want || !strings.Contains(r.SystemPrompt, "Podepisuj jako studio.") {
		t.Errorf("system prompt\n%q\nwant\n%q", r.SystemPrompt, want)
	}
	want := "Write a suggested reply to message m_9 in account acc_1.\n" +
		"Other messages of the conversation, oldest first: m_1, m_2.\n" +
		"The user's instruction, written by the user:\n<<<\nDecline politely\n>>>"
	if r.Message != want {
		t.Errorf("message\n%q\nwant\n%q", r.Message, want)
	}
	if !slices.Equal(r.Tools, SuggestReplyTools) || !slices.Equal(r.BridgeArgs, []string{"--in-app", "--reply-only", "m_9"}) || r.JSONSchema != nil {
		t.Errorf("%+v", r)
	}
	// A follow-up, without an instruction, of an account without a note.
	r, err = Build(api.PurposeReply, &api.PromptInput{Reply: &api.ReplyInput{AccountID: "acc_2", MessageID: "m_1", FollowUp: true}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.SystemPrompt != WithProfile(SuggestReplySystemPromptFor(true), profile.For("acc_2")) || strings.Contains(r.SystemPrompt, "For the account") || !strings.Contains(r.SystemPrompt, "nudge") {
		t.Errorf("follow-up system prompt %q", r.SystemPrompt)
	}
	if r.Message != "Write a suggested reply to message m_1 in account acc_2.\nThe user gave no instruction." {
		t.Errorf("follow-up message %q", r.Message)
	}
	for _, tt := range []struct {
		name string
		in   *api.PromptInput
	}{
		{"nil", nil},
		{"no reply", &api.PromptInput{}},
		{"no account", &api.PromptInput{Reply: &api.ReplyInput{MessageID: "m_1"}}},
		{"no message", &api.PromptInput{Reply: &api.ReplyInput{AccountID: "acc_1"}}},
		{"a message id with a space", &api.PromptInput{Reply: &api.ReplyInput{AccountID: "acc_1", MessageID: "m 1"}}},
		{"too many others", &api.PromptInput{Reply: &api.ReplyInput{AccountID: "acc_1", MessageID: "m_1", Others: ids(api.MaxPromptMessageIDs + 1)}}},
		{"a long instruction", &api.PromptInput{Reply: &api.ReplyInput{AccountID: "acc_1", MessageID: "m_1", Instruction: strings.Repeat("a", api.MaxPromptTextBytes+1)}}},
		{"a field of another purpose", &api.PromptInput{Reply: &api.ReplyInput{AccountID: "acc_1", MessageID: "m_1"}, Triage: &api.TriageInput{}}},
	} {
		if _, err := Build(api.PurposeReply, tt.in, o); !invalidArgument(err) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"plain":             "plain",
		"a\r\nb":            "a\nb",
		"a\x00b\x1b[0m\x7f": "ab[0m",
		"tab\tand\nnew":     "tab\tand\nnew",
		"a\xff\xfeb":        "a\uFFFDb",
		"bidi\u202e":        "bidi\u202e",
		"nbsp\u00a0":        "nbsp\u00a0",
	} {
		got, err := cleanText("f", in)
		if err != nil || got != want {
			t.Errorf("cleanText(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
