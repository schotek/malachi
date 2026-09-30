// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
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
	WizardTexts(tr)
	for _, d := range []api.JiraDeployment{api.JiraCloud, api.JiraDataCenter} {
		CredentialFields(d, tr)
		for _, step := range []Step{StepDetect, StepSpaces, StepSave} {
			for c := ErrOther; c <= ErrConflict; c++ {
				FailureOf(step, c, d, false, tr)
				FailureOf(step, c, d, true, tr)
			}
		}
	}
	CheckSiteInput("ftp://x", tr)
	Detected(api.AccountDetectSiteResult{Deployment: api.JiraDataCenter, Version: "9"}, tr)
	Detected(api.AccountDetectSiteResult{}, tr)
	ApproxCount(1, tr)
	SpacesProblem(0, tr)
	SpacesProblem(api.MaxJiraSpaces+1, tr)
	OfflineChoiceLabels(tr)
	for _, v := range []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualWatching, api.VirtualOpen} {
		VirtualFolderTitle(v, tr)
	}
	IssueCard(api.IssueInfo{}, &api.MessageIssue{Item: api.IssueItemComment, Visibility: api.CommentInternal, Via: "bot", Edited: true}, tr)
	EventText([]api.IssueChange{{Field: api.IssueFieldStatus}, {Field: api.IssueFieldAssignee}}, tr)
	AuthBannerText(api.AccountJira, api.CodeAuthRequired, "a", tr)
	AuthBannerText(api.AccountJira, api.CodeAuthFailed, "a", tr)
	VisibilityOptions(api.IssueInfo{CommentVisibilities: []api.CommentVisibility{api.CommentPublic, api.CommentInternal}}, tr)
	CommentTitle("K-1", tr)
	SendProblem("", tr)
	CommentQueued(tr)
	ReplyLabel(true, tr)
	ReplyLabel(false, tr)
	exerciseSettings(tr)
	exerciseTransitions(tr)
}

// exerciseTransitions is exercise for transitions.go.
func exerciseTransitions(tr Translator) {
	Transitions(api.IssueTransitionsResult{Transitions: []api.IssueTransition{{ID: "1", Name: "a", NeedsInput: true}}}, tr)
	ChangeStatusLabel(tr)
	TransitionsLoading(tr)
	NoTransitions(tr)
	LoadTransitionsAction(tr)
	TransitionAction(tr)
	StatusChanged(TransitionItem{Target: "a"}, api.IssueInfo{}, tr)
	TransitionFailed(api.CodeServerError, "a", "", tr)
}

// exerciseSettings is exercise for settings.go.
func exerciseSettings(tr Translator) {
	SettingsTexts(tr)
	NotificationModeLabels(tr)
	NotificationHint(api.NotificationMailHide, tr)
	for _, c := range statusCategories {
		StatusCategoryTitle(c, tr)
	}
	StatusesProblem(make([]api.StatusRef, api.MaxJiraStatuses+1), tr)
	CheckEntry(ListBotNames, strings.Repeat("a", api.MaxJiraPatternBytes+1), nil, tr)
	CheckEntry(ListBotNames, "a\tb", nil, tr)
	CheckEntry(ListBotNames, "ab", nil, tr)
	CheckEntry(ListMetadataFilters, "(", nil, tr)
	CheckEntry(ListSenders, "nobody", nil, tr)
	CheckEntry(ListAuthorPrefixes, "a", []string{"A"}, tr)
	CheckEntry(ListAuthorPrefixes, "a", make([]string, api.MaxJiraListEntries), tr)
	Suggestions(ListBotNames, nil, tr)
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
			ours = ours || strings.Contains(line, "ui/internal/jira/")
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
// machine without make lint: every msgid it translates is in the template
// with its context and plural, and every entry that names a file of the
// package is one it translates.
func TestMsgidsInTemplate(t *testing.T) {
	used := recorder{}
	exercise(used)
	pot := template(t)
	for k := range used {
		if _, ok := pot[k]; !ok {
			t.Errorf("po/malachi.pot lacks msgid %q (context %q, plural %q)", k.msgid, k.ctx, k.plural)
		}
	}
	for k, ours := range pot {
		if ours && !used[k] {
			t.Errorf("po/malachi.pot names ui/internal/jira for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
}
