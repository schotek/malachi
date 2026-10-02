// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"fmt"
	"reflect"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Prague for the time zone cases, on any machine

	"github.com/schotek/malachi/backend/pkg/api"
)

// The fixtures of the board's tests (macOS BoardFixture in
// BoardModelTests.swift): a fixed now, a UTC calendar and English dates, the
// msgids coming back verbatim (tr of text_test.go).

// testDates writes dates as the English UI does, in loc.
type testDates struct{ loc *time.Location }

func (d testDates) Date(t, now time.Time) string {
	t, now = t.In(d.loc), now.In(d.loc)
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	switch {
	case ty == ny && tm == nm && td == nd:
		return d.Time(t)
	case ty == ny:
		return fmt.Sprintf("%d %s", td, t.Format("Jan"))
	}
	return t.Format("2006-01-02")
}

func (d testDates) Time(t time.Time) string { return t.In(d.loc).Format("15:04") }
func (d testDates) DateTime(t time.Time) string {
	return t.In(d.loc).Format("Mon, 2 Jan 2006 at 15:04")
}
func (d testDates) Weekday(t time.Time) string { return t.In(d.loc).Format("Mon") }

// envIn is the test environment in loc.
func envIn(loc *time.Location) Env { return Env{Tr: tr, Dates: testDates{loc}, Loc: loc} }

var testEnv = envIn(time.UTC)

// day is a day of October 2026 in UTC (the 15th is a Thursday).
func day(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }

var testNow = day(15, 12, 0)

// ago is hours before testNow.
func ago(hours float64) time.Time { return testNow.Add(-time.Duration(hours * float64(time.Hour))) }

const (
	accountA api.AccountID = "a"
	accountB api.AccountID = "b"
)

var testAccounts = []AccountInfo{
	{ID: accountA, Name: "Alpha", Badge: "IMAP", CanReply: true},
	{ID: accountB, Name: "Beta", Badge: "JIRA", CanReply: true},
}

// caseOpt changes a case mk makes.
type caseOpt func(*Case)

func withAccount(a api.AccountID) caseOpt  { return func(c *Case) { c.Account = a } }
func withHours(h float64) caseOpt          { return func(c *Case) { c.Date = ago(h) } }
func withSubject(s string) caseOpt         { return func(c *Case) { c.Subject = s } }
func withSnippet(s string) caseOpt         { return func(c *Case) { c.Snippet = s } }
func withCount(n int) caseOpt              { return func(c *Case) { c.MessageCount = n } }
func withAnnotation(a Annotation) caseOpt  { return func(c *Case) { c.Annotation = &a } }
func withUser(s State) caseOpt             { return func(c *Case) { c.UserState = statePtr(s) } }
func withIssue(i IssueInfo) caseOpt        { return func(c *Case) { c.Issue = &i } }
func withVisibility(v Visibility) caseOpt  { return func(c *Case) { c.Visibility = v } }
func withVersion(v int64) caseOpt          { return func(c *Case) { c.Version = v } }
func withReason(r api.BoardReason) caseOpt { return func(c *Case) { c.RuleReason = r } }

func withDone() caseOpt { return func(c *Case) { c.Visibility = Visibility{Kind: VisibleDone} } }

func withMessages(ms ...CaseMessage) caseOpt {
	return func(c *Case) { c.Messages, c.MessagesLoaded = ms, true }
}

// notLoaded is a case whose conversation has not been loaded.
func notLoaded() caseOpt { return func(c *Case) { c.Messages, c.MessagesLoaded = nil, false } }

// withDraft links the draft "d_<n>" with text.
func withDraft(text string) caseOpt {
	return func(c *Case) { c.Draft = &DraftLink{ID: api.DraftID("d_" + string(c.ID)), Text: text} }
}

// mk is case n in state: account A, an hour ago, "Subject n", one message,
// the rule "rule n" (no code this client knows), its (empty) conversation
// loaded.
func mk(n string, state State, opts ...caseOpt) Case {
	c := Case{
		ID: CaseID(n), Account: accountA, Person: "P " + n, Date: ago(1), Subject: "Subject " + n,
		MessageCount: 1, RuleState: state, RuleReason: api.BoardReason("rule " + n), MessagesLoaded: true,
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// setup is what a test view is built from besides its cases.
type setup struct {
	annotated   bool
	commitments []Commitment
	configure   func(*ViewState)
}

type viewOpt func(*setup)

func annotatedOn() viewOpt                     { return func(s *setup) { s.annotated = true } }
func withCommitments(ks ...Commitment) viewOpt { return func(s *setup) { s.commitments = ks } }
func configured(f func(v *ViewState)) viewOpt  { return func(s *setup) { s.configure = f } }

func filterState(s State) Filter { return Filter{Kind: FilterState, State: s} }

var doneFilter = Filter{Kind: FilterDone}

// view is the view model of cases with testAccounts at testNow, the
// assistant off unless annotatedOn, the run of model "M".
func view(cases []Case, opts ...viewOpt) ViewModel {
	var su setup
	for _, o := range opts {
		o(&su)
	}
	v := NewViewState()
	if su.configure != nil {
		su.configure(&v)
	}
	s := Snapshot{
		Accounts: testAccounts, Cases: cases, Commitments: su.commitments, Annotated: su.annotated,
		Run: &Run{Model: "M", Date: testNow}, Phase: PhaseReady,
	}
	return View(s, v, testNow, testEnv)
}

// testSnapshot is cases with testAccounts, ready, the assistant off.
func testSnapshot(cases ...Case) Snapshot {
	return Snapshot{Accounts: testAccounts, Cases: cases, Phase: PhaseReady}
}

// stateWith is NewViewState changed by f.
func stateWith(f func(*ViewState)) ViewState {
	v := NewViewState()
	f(&v)
	return v
}

// idsOf is the ids of rows.
func idsOf(rows []Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, string(r.ID))
	}
	return out
}

// rowsOf is the ids of every row of the list, section after section.
func rowsOf(v ViewModel) []string {
	out := []string{}
	for _, s := range v.Sections {
		out = append(out, idsOf(s.Rows)...)
	}
	return out
}

// sectionKinds names the list's sections: "hot", "you", "them", "info",
// "snoozed", "done".
func sectionKinds(v ViewModel) []string {
	out := []string{}
	for _, s := range v.Sections {
		switch s.Kind {
		case SectionSnoozed:
			out = append(out, "snoozed")
		case SectionDone:
			out = append(out, "done")
		default:
			out = append(out, string(s.State.API()))
		}
	}
	return out
}

// eq fails the test when got is not want (reflect.DeepEqual).
func eq[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

// check fails the test when ok is false.
func check(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}
