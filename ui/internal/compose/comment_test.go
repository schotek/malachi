// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The comment mode of the compose window, its decisions without a display
// (macOS JiraComposeControllerTests.swift): a comment draft from
// draft.create opens it (FromDraft), it is sent without recipients but not
// empty, draft.save carries the chosen visibility, and there is no Save
// Draft: closing asks whether to discard, and the copy the autosave kept
// goes with the window.

const jiraAccount api.AccountID = "j"

// request is a service-desk request: its comments may be public or
// internal.
var request = api.IssueInfo{
	Key: "ITSD-42", URL: "https://acme.atlassian.net/browse/ITSD-42", Summary: "The printer on the third floor",
	Status: "Waiting for Support", CommentVisibilities: []api.CommentVisibility{api.CommentPublic, api.CommentInternal},
}

// issue is an ordinary issue: public comments only.
var issue = api.IssueInfo{
	Key: "WEB-7", URL: "https://acme.atlassian.net/browse/WEB-7", Summary: "Logo on the front page", Status: "To Do",
}

// commentDraft is the comment draft draft.create returns for a reply to
// message w1 of info.
func commentDraft(info api.IssueInfo, v api.CommentVisibility) api.Draft {
	return api.Draft{
		AccountID: jiraAccount, Subject: info.Key + ": " + info.Summary, InReplyTo: "w1",
		Comment: &api.DraftComment{Issue: info, Visibility: v},
	}
}

func TestFromDraftCarriesTheComment(t *testing.T) {
	d := commentDraft(request, api.CommentInternal)
	p := FromDraft(KindReply, d, api.BlockedContent{})
	if p.Comment == nil || !reflect.DeepEqual(*p.Comment, api.DraftComment{Issue: request, Visibility: api.CommentInternal}) {
		t.Errorf("Comment = %+v", p.Comment)
	}
	if p.AccountID != jiraAccount || p.InReplyTo != "w1" || p.Forwarding != "" {
		t.Errorf("meta: %+v", p)
	}
	if len(p.To)+len(p.CC)+len(p.BCC) != 0 {
		t.Errorf("recipients: %+v %+v %+v", p.To, p.CC, p.BCC)
	}
	if p.Subject != "ITSD-42: The printer on the third floor" {
		t.Errorf("Subject = %q", p.Subject)
	}
	if p.BodyHTML != "" {
		t.Errorf("a comment starts empty, got %q", p.BodyHTML)
	}

	// A mail draft opens no comment mode.
	mail := FromDraft(KindReply, api.Draft{AccountID: "a", Subject: "Re: x", InReplyTo: "m1"}, api.BlockedContent{})
	if mail.Comment != nil {
		t.Errorf("mail draft: Comment = %+v", mail.Comment)
	}
	if (Params{Kind: KindReply}).Comment != nil {
		t.Error("Params default to an e-mail")
	}
}

func TestWireCommentCarriesTheVisibility(t *testing.T) {
	c := &api.DraftComment{Issue: request}
	got := wireComment(c, api.CommentInternal)
	if got == nil || !reflect.DeepEqual(*got, api.DraftComment{Issue: request, Visibility: api.CommentInternal}) {
		t.Errorf("internal: %+v", got)
	}
	if got == c {
		t.Error("the window's comment is sent as a copy")
	}
	if c.Visibility != "" {
		t.Errorf("choosing changed the draft's comment: %q", c.Visibility)
	}
	if got := wireComment(c, api.CommentPublic); got.Visibility != api.CommentPublic {
		t.Errorf("public: %+v", got)
	}
	// An e-mail window sends no comment, whatever is chosen.
	if got := wireComment(nil, api.CommentInternal); got != nil {
		t.Errorf("mail: %+v", got)
	}
}

func TestChosenVisibility(t *testing.T) {
	both := jira.VisibilityOptions(request, testTr{})
	cases := []struct {
		options []jira.VisibilityOption
		active  string
		want    api.CommentVisibility
	}{
		{both, "internal", api.CommentInternal},
		{both, "public", api.CommentPublic},
		{both, "", api.CommentPublic},
		{both, "partners", api.CommentPublic},
		// An ordinary issue: no choice, public.
		{nil, "internal", api.CommentPublic},
		{nil, "", api.CommentPublic},
	}
	for _, c := range cases {
		if got := chosenVisibility(c.options, c.active); got != c.want {
			t.Errorf("chosenVisibility(%d options, %q) = %q, want %q", len(c.options), c.active, got, c.want)
		}
	}

	// What the window shows first is the draft's choice (jira.SelectedVisibility),
	// found among the options by name.
	for _, d := range []api.Draft{commentDraft(request, api.CommentInternal), commentDraft(request, ""), commentDraft(issue, api.CommentInternal)} {
		cw, ok := jira.CommentCompose(d, testTr{})
		if !ok {
			t.Fatal("a comment draft has a comment mode")
		}
		if got := chosenVisibility(cw.Visibilities, string(cw.Visibility)); got != cw.Visibility {
			t.Errorf("%s: shown %q, chosen %q", d.Comment.Issue.Key, cw.Visibility, got)
		}
	}
}

func TestCloseAsksOnlyWithSomethingToLose(t *testing.T) {
	const text = "Replaced the toner."
	blank := " \n\t\u200b\ufeff\u00a0"
	cases := []struct {
		name    string
		d       draftState
		owner   Owner
		comment bool
		text    string
		want    bool
	}{
		{"mail untouched", draftState{}, OwnerWindow, false, "", true},
		{"mail dirty", draftState{dirty: true}, OwnerWindow, false, "", false},
		{"mail saving", draftState{saving: true}, OwnerWindow, false, "", false},
		{"mail discarded", draftState{dirty: true, discard: true}, OwnerWindow, false, "", true},
		// A mail window with text but nothing unsaved goes: the text is in
		// the Drafts folder.
		{"mail saved", draftState{draftID: "d1"}, OwnerWindow, false, text, true},
		// A comment with text asks, saved or not: no Drafts folder keeps it.
		{"comment with text", draftState{}, OwnerWindow, true, text, false},
		{"comment saved", draftState{draftID: "d1"}, OwnerWindow, true, text, false},
		// Nothing in it: it goes, unsaved edits and a save under way or not.
		{"comment emptied", draftState{dirty: true, draftID: "d1"}, OwnerWindow, true, blank, true},
		{"comment saving", draftState{saving: true}, OwnerWindow, true, "", true},
		{"comment sent", draftState{discard: true, draftID: "d1"}, OwnerWindow, true, text, true},
		// The board keeps its draft: closing never asks, whatever is in it
		// (ComposeDraftBoardOwnerTests closingNeverAsksAndNeverDeletes).
		{"board dirty mail", draftState{dirty: true}, OwnerBoard, false, "", true},
		{"board dirty comment", draftState{dirty: true}, OwnerBoard, true, text, true},
		{"board saving", draftState{saving: true}, OwnerBoard, false, "", true},
	}
	for _, c := range cases {
		if got := closesUnasked(&c.d, c.owner, c.comment, c.text); got != c.want {
			t.Errorf("%s: closesUnasked = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTheCopyOfACommentGoesWithTheWindow(t *testing.T) {
	cases := []struct {
		name    string
		d       draftState
		owner   Owner
		comment bool
		want    bool
	}{
		{"comment autosaved", draftState{draftID: "d1"}, OwnerWindow, true, true},
		{"comment closed while dirty", draftState{draftID: "d1", dirty: true}, OwnerWindow, true, true},
		// Sent (or deleted already by Discard): nothing left to delete.
		{"comment sent", draftState{draftID: "d1", discard: true}, OwnerWindow, true, false},
		{"comment never saved", draftState{}, OwnerWindow, true, false},
		// An e-mail's draft stays in Drafts.
		{"mail saved", draftState{draftID: "d1"}, OwnerWindow, false, false},
		// The board never deletes here, even a late save after a Close
		// answers (ComposeDraftBoardOwnerTests aLateSaveNeverDeletes); only
		// Discard does, through discardBoard, not cleanup.
		{"board comment autosaved", draftState{draftID: "d1"}, OwnerBoard, true, false},
		{"board mail saved", draftState{draftID: "d1"}, OwnerBoard, false, false},
	}
	for _, c := range cases {
		if got := deletesOnClose(&c.d, c.owner, c.comment); got != c.want {
			t.Errorf("%s: deletesOnClose = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestACommentHasNoDraftStatus(t *testing.T) {
	saved := time.Date(2026, 9, 30, 14, 3, 0, 0, time.Local)
	for _, d := range []draftState{{}, {dirty: true}, {saving: true}, {lastSaved: saved}} {
		if got := draftStatus(&d, true, true); got != "" {
			t.Errorf("comment %+v: status %q, want none", d, got)
		}
	}
	if got := draftStatus(&draftState{sending: true, dirty: true}, true, false); got != "Sending…" {
		t.Errorf("comment sending: %q", got)
	}

	// An e-mail window is unchanged.
	mail := []struct {
		d           draftState
		placeholder bool
		want        string
	}{
		{draftState{sending: true}, false, "Sending…"},
		{draftState{saving: true, dirty: true}, false, "Saving draft…"},
		{draftState{dirty: true}, false, "Unsaved changes"},
		{draftState{}, true, "Using placeholder account"},
		{draftState{}, false, ""},
	}
	for _, c := range mail {
		if got := draftStatus(&c.d, false, c.placeholder); got != c.want {
			t.Errorf("mail %+v: status %q, want %q", c.d, got, c.want)
		}
	}
	if got := draftStatus(&draftState{lastSaved: saved}, false, false); len(got) <= len("Draft saved ") || got[:len("Draft saved ")] != "Draft saved " {
		t.Errorf("mail saved: %q", got)
	}
}

func TestAFailedSaveOfACommentSaysSendingOrNothing(t *testing.T) {
	rejected := &api.Error{Code: api.CodeInvalidArgument, Message: "comment longer than 32767 characters"}
	if got := commentSaveFailure(saveExplicit, rejected); got != "Sending was rejected: comment longer than 32767 characters" {
		t.Errorf("explicit: %q", got)
	}
	disk := &api.Error{Code: api.CodeStorageError, Message: "disk"}
	if got := commentSaveFailure(saveAutosave, disk); got != "" {
		t.Errorf("autosave: %q, want nothing", got)
	}
}

func TestQueuedText(t *testing.T) {
	if got := queuedText(true); got != "Comment queued" {
		t.Errorf("comment: %q", got)
	}
	if got := queuedText(false); got != "Message queued for sending" {
		t.Errorf("mail: %q", got)
	}
}

// The GTK toolbar in compose.blp order: bold italic underline | paragraph
// style, alignment | bullets, numbers, quote | link, colour, image, clear.
var gtkToolbar = []toolItem{
	{format: jira.FormatBold}, {format: jira.FormatItalic}, {format: jira.FormatUnderline},
	{separator: true},
	{format: jira.FormatHeading}, {format: jira.FormatAlignment},
	{separator: true},
	{format: jira.FormatBulletList}, {format: jira.FormatNumberedList}, {format: jira.FormatQuote},
	{separator: true},
	{format: jira.FormatLink}, {format: jira.FormatColour}, {format: jira.FormatImage}, {format: jira.FormatClear},
}

func TestRestrictedToolbar(t *testing.T) {
	shown := restrictedToolbar(gtkToolbar)
	var got []string
	for i, on := range shown {
		if !on {
			continue
		}
		if gtkToolbar[i].separator {
			got = append(got, "|")
		} else {
			got = append(got, string(gtkToolbar[i].format))
		}
	}
	want := []string{"bold", "italic", "|", "bulletList", "numberedList", "quote", "|", "link", "clear"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("comment toolbar = %v, want %v", got, want)
	}

	sep := toolItem{separator: true}
	under := toolItem{format: jira.FormatUnderline}
	bold := toolItem{format: jira.FormatBold}
	other := toolItem{} // a control of no format stays
	cases := []struct {
		name  string
		items []toolItem
		want  []bool
	}{
		{"empty", nil, []bool{}},
		{"leading separator", []toolItem{sep, bold}, []bool{false, true}},
		{"trailing separator", []toolItem{bold, sep}, []bool{true, false}},
		{"emptied group at the end", []toolItem{bold, sep, under}, []bool{true, false, false}},
		{"emptied group at the start", []toolItem{under, sep, bold}, []bool{false, false, true}},
		{"two separators in a row", []toolItem{bold, sep, sep, bold}, []bool{true, true, false, true}},
		{"nothing kept", []toolItem{under, sep, under}, []bool{false, false, false}},
		{"control without format", []toolItem{other, sep, bold}, []bool{true, true, true}},
	}
	for _, c := range cases {
		if got := restrictedToolbar(c.items); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Every control of toolbarFormats is in the toolbar of compose_pane.blp,
// and every format a comment keeps has a control there except code, which
// the GTK toolbar does not offer (nor does the macOS one). The toolbar and
// header fields live in the pane (compose_pane.blp, extracted from
// compose.blp so the board can embed them inline too); attach_button is
// window chrome and stays in compose.blp.
func TestToolbarFormatsMatchTheBlueprint(t *testing.T) {
	raw, err := os.ReadFile("../../data/ui/compose_pane.blp")
	if err != nil {
		t.Fatal(err)
	}
	blp := string(raw)
	windowRaw, err := os.ReadFile("../../data/ui/compose.blp")
	if err != nil {
		t.Fatal(err)
	}
	windowBlp := string(windowRaw)
	has := map[jira.Format]bool{}
	for id, f := range toolbarFormats {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + ` \{`).MatchString(blp) {
			t.Errorf("%s is not an object of compose_pane.blp", id)
		}
		has[f] = true
	}
	for _, f := range jira.CommentFormats {
		if !has[f] && f != jira.FormatCode {
			t.Errorf("a comment keeps %s, but no toolbar control applies it", f)
		}
	}
	if !regexp.MustCompile(`\battach_button \{`).MatchString(windowBlp) {
		t.Error("attach_button is not an object of compose.blp")
	}
	for _, id := range []string{"header_rows", "comment_header", "comment_title", "comment_summary", "comment_visibility"} {
		if !regexp.MustCompile(`\b` + id + ` \{`).MatchString(blp) {
			t.Errorf("%s is not an object of compose_pane.blp", id)
		}
	}
}

func TestManagerAccounts(t *testing.T) {
	mail := api.Account{ID: "a", Enabled: true, Config: api.AccountConfig{Email: "me@example.invalid"}}
	jiraAcc := api.Account{
		ID: jiraAccount, Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira, Email: "jana@acme.example"},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition},
	}

	// Nothing known yet: the placeholder, and New Message is offered.
	m := &Manager{}
	if !m.Placeholder() || !reflect.DeepEqual(m.Accounts(), dummyAccounts) || !m.CanComposeNew() {
		t.Errorf("none known: placeholder %v, accounts %+v, new %v", m.Placeholder(), m.Accounts(), m.CanComposeNew())
	}

	// An issue tracker alone writes no mail: From has the placeholder, and
	// there is no New Message.
	m.accounts = []api.Account{jiraAcc}
	if !m.Placeholder() || !reflect.DeepEqual(m.Accounts(), dummyAccounts) {
		t.Errorf("jira only: placeholder %v, accounts %+v", m.Placeholder(), m.Accounts())
	}
	if m.CanComposeNew() {
		t.Error("jira only: New Message offered")
	}
	if a, ok := m.knownAccount(jiraAccount); !ok || a.ID != jiraAccount {
		t.Errorf("knownAccount = %+v, %v", a, ok)
	}

	// With a mail account From lists it alone.
	m.accounts = []api.Account{jiraAcc, mail}
	if m.Placeholder() || !reflect.DeepEqual(m.Accounts(), []api.Account{mail}) || !m.CanComposeNew() {
		t.Errorf("mail and jira: placeholder %v, accounts %+v, new %v", m.Placeholder(), m.Accounts(), m.CanComposeNew())
	}
	if got := m.SelfAddress(); got.Address != "me@example.invalid" {
		t.Errorf("SelfAddress = %+v", got)
	}

	// The comment window finds its account among all of them.
	if got := m.commentAccount(jiraAccount); !reflect.DeepEqual(got, jiraAcc) {
		t.Errorf("commentAccount = %+v", got)
	}
	// Until the list is there, one that carries the id.
	m.accounts = nil
	got := m.commentAccount(jiraAccount)
	if got.ID != jiraAccount || !got.Can(api.CapabilityComment) || got.Can(api.CapabilityCompose) {
		t.Errorf("stand-in = %+v", got)
	}
	if _, ok := m.knownAccount(jiraAccount); ok {
		t.Error("knownAccount before the list")
	}

	// A daemon from before capabilities lists mail accounts only.
	m.accounts = []api.Account{{ID: "old", Config: api.AccountConfig{Email: "old@example.invalid"}}}
	if m.Placeholder() || len(m.Accounts()) != 1 || !m.CanComposeNew() {
		t.Errorf("old daemon: placeholder %v, accounts %+v", m.Placeholder(), m.Accounts())
	}
}

// testTr returns the msgid (the singular for n == 1), as gettext does
// without a catalogue.
type testTr struct{}

func (testTr) T(msgid string) string { return msgid }
func (testTr) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (testTr) C(_, msgid string) string { return msgid }
