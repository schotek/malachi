// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// msgKey is a msgid of the template with its context and plural.
type msgKey struct{ ctx, msgid, plural string }

// recorder is a translator that notes every msgid asked for.
type recorder map[msgKey]bool

func (r recorder) T(msgid string) string { r[msgKey{msgid: msgid}] = true; return msgid }

func (r recorder) N(msgid, plural string, n int) string {
	r[msgKey{msgid: msgid, plural: plural}] = true
	if n == 1 {
		return msgid
	}
	return plural
}

func (r recorder) C(ctx, msgid string) string { r[msgKey{ctx: ctx, msgid: msgid}] = true; return msgid }

// exercise calls every function of the package that translates, down
// every branch that picks a msgid.
func exercise(tr Translator) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	Mail(tr)
	BoardName(tr)
	for _, s := range States {
		StateName(s, tr)
		ColumnEmpty(s, tr)
		FilterTitle(Filter{Kind: FilterState, State: s}, tr)
	}
	FilterTitle(Filter{Kind: FilterAll}, tr)
	FilterTitle(Filter{Kind: FilterDone}, tr)
	AllAccounts(tr)
	CaseCount(2, tr)
	MessageCount(2, tr)
	TodoPhrase(0, tr)
	TodoPhrase(2, tr)
	AndMore(2, tr)
	for k := DueOverdue; k <= DueLater; k++ {
		DueGroupTitle(k, tr)
	}
	for k := SourceRules; k <= SourceAssistantOff; k++ {
		SourceText(Source{Kind: k}, "", tr)
		SourceText(Source{Kind: k}, "m", tr)
	}
	StatusLine(false, "", "", tr)
	StatusLine(true, "", "", tr)
	StatusLine(true, "m", "n", tr)
	NoSelectionTitle(tr)
	NoSelectionBody(tr)
	SectionEmpty(tr)
	FromAssistant(tr)
	SummaryHeading(tr)
	TasksHeading(tr)
	DraftHeading(tr)
	DraftNote(tr)
	ReplyLoading(tr)
	ReplyLoadFailed(tr)
	ReplyRemoved(tr)
	ReplyNotSaved(tr)
	ReplyNotSent("t", tr)
	QuitUnsavedHeading(tr)
	QuitUnsavedBody(tr)
	QuitAnyway(tr)
	Unstar(tr)
	OpenDraft(tr)
	Discard(tr)
	WhyLink(tr)
	NotDone(tr)
	Remind(tr)
	Reply(tr)
	Close(tr)
	Triage(tr)
	Later(tr)
	for s := StyleList; s <= StyleToday; s++ {
		StyleTitle(s, tr)
		StyleMenuTitle(s, tr)
	}
	DefaultStyleSetting(tr)
	You(tr)
	Conversation(2, tr)
	Deadlines(tr)
	// view.go: a case without a subject (the msgid is shared with the message list).
	_ = viewContext{tr: tr}.subject(Case{})
	DueEmpty(tr)
	CalendarTitle(tr)
	CalendarBody(tr)
	Commitments(tr)
	SpokenDue("d", tr)
	SpokenRemind("d", tr)
	SpokenAttachments(tr)
	SpokenUnread(tr)
	for _, c := range reasonCodes {
		Reason(c, tr)
	}
	Reason("x", tr)
	for p := PhaseLoading; p <= PhaseOff; p++ {
		EmptyTitleOf(p, tr)
		EmptyBodyOf(p, tr)
		Notice(p, true, tr)
	}
	StaleNotes(tr)
	MessagesLoading(tr)
	MessagesFailed(tr)
	TryAgain(tr)
	SpokenAssistant("t", tr)
	ShowInMail(tr)
	ShowInMailGone(tr)
	ShowInMailFailed(tr)
	Archive(tr)
	Snoozed(tr)
	SnoozedUntil("d", tr)
	for k := RemindLaterToday; k <= RemindNextWeek; k++ {
		RemindPreset(k, tr)
	}
	RemindNoMore(tr)
	Archived(2, false, tr)
	Archived(0, false, tr)
	Archived(0, true, tr)
	for _, a := range Actions {
		for w := WhyNone; w <= WhyDraftGone; w++ {
			Failed(a, w, tr)
		}
	}
	MoveTo(tr)
	MarkAsDone(tr)
	RemindMe(tr)
	StateLabel(tr)
	AccountsCaption(tr)
	MarkPromiseDone(tr)
	PromiseCount(2, tr)
	Quoted("q", tr)
	exerciseTriage(tr, now)
	exerciseReply(tr)
}

// exerciseReply is exercise for reply.go.
func exerciseReply(tr Translator) {
	SuggestReply(tr)
	SuggestReplyPlaceholder(tr)
	SuggestReplyRunning(tr)
	SuggestReplyElsewhere(tr)
	for _, f := range SuggestReplyFailures {
		SuggestReplyFailed(f, tr)
	}
}

// exerciseTriage is exercise for triage.go.
func exerciseTriage(tr Translator, now time.Time) {
	TriageToolTip(tr)
	TriageStopToolTip(tr)
	TriageNeedsClaudeCode(tr)
	TriageStarting(tr)
	TriageProgress(1, 2, tr)
	TriageProgress(0, 0, tr)
	TriageRunningLine(1, 2, tr)
	TriageRunningLine(0, 0, tr)
	TriageFinished(0, 0, tr)
	TriageFinished(2, 2, tr)
	TriagedToday(2, tr)
	TriageWaiting(2, tr)
	TriageStopped(tr)
	for _, f := range TriageFailures {
		TriageFailed(f, tr)
	}
	TriageStatusLine(false, nil, now, tr)
	TriageStatusLine(true, nil, now, tr)
	TriageStatusLine(true, &now, now, tr)
	for k := PauseFailed; k <= PauseNoConsent; k++ {
		AutoTriagePaused(Pause{Kind: k, Until: now}, now, tr)
	}
	TriageConsentHeading(tr)
	TriageConsentBody(tr)
	TriageSettingsConsent(tr)
	TriageSettingsConsentSubtitle(tr)
	TriageSettingsAutomatic(tr)
	TriageSettingsInterval(tr)
	TriageSettingsDaily(tr)
	TriageSettingsNeedsClaudeCode(tr)
	TriageSettingsNeedsSignIn(tr)
	TriageSettingsNoTools(tr)
	TriageSettingsNoBackend(tr)
	TriageInterval(15, tr)
	TriageInterval(120, tr)
	TriageDailyCap(0, tr)
	TriageDailyCap(60, tr)
	TriageSettingsUsage(tr)
	TriageUsageNone(tr)
	TriageUsageSplit("1", "2", "3", "4", tr)
	TriageUsageRuns(2, tr)
	TriageUsageToolTip(tr)
	for _, ago := range []time.Duration{0, time.Minute * 5, time.Hour * 2, time.Hour * 30, time.Hour * 72} {
		RelativeTime(now.Add(-ago), now, tr)
		RelativeFuture(now.Add(ago), now, tr)
	}
}

// template reads po/malachi.pot: every entry, and whether it names a file
// of this package among its references.
func template(t *testing.T) map[msgKey]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "po", "malachi.pot"))
	if err != nil {
		t.Fatalf("open the template: %v", err)
	}
	defer f.Close()
	field := regexp.MustCompile(`^(msgctxt|msgid_plural|msgid|msgstr(?:\[\d\])?) (".*")$`)
	out := map[msgKey]bool{}
	var cur msgKey
	var ours bool
	var last *string
	flush := func() {
		if cur.msgid != "" {
			out[cur] = ours
		}
		cur, ours, last = msgKey{}, false, nil
	}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "#:"):
			ours = ours || strings.Contains(line, "ui/internal/board/")
		case strings.HasPrefix(line, "\""):
			if last != nil {
				*last += unquote(t, line)
			}
		default:
			m := field.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			last = nil
			switch m[1] {
			case "msgctxt":
				last = &cur.ctx
			case "msgid":
				last = &cur.msgid
			case "msgid_plural":
				last = &cur.plural
			}
			if last != nil {
				*last = unquote(t, m[2])
			}
		}
	}
	flush()
	if err := s.Err(); err != nil {
		t.Fatalf("read the template: %v", err)
	}
	return out
}

func unquote(t *testing.T, s string) string {
	t.Helper()
	u, err := strconv.Unquote(s)
	if err != nil {
		t.Fatalf("template string %s: %v", s, err)
	}
	return u
}

// TestMsgidsInTemplate keeps po/malachi.pot in step with the package on a
// machine without make lint: every msgid it translates (its own and the
// ones it reuses) is in the template with its context and plural, and
// every entry that names a file of the package is one it translates.
func TestMsgidsInTemplate(t *testing.T) {
	used := recorder{}
	exercise(used)
	pot := template(t)
	for k := range used {
		if _, ok := pot[k]; !ok {
			t.Errorf("po/malachi.pot lacks msgid %q (context %q, plural %q)", k.msgid, k.ctx, k.plural)
		}
	}
	own := 0
	for k, ours := range pot {
		if !ours {
			continue
		}
		own++
		if !used[k] {
			t.Errorf("po/malachi.pot names ui/internal/board for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
	if own == 0 {
		t.Error("po/malachi.pot names no file of ui/internal/board")
	}
}
