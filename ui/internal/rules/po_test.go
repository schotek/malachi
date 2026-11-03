// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	date := func(time.Time) string { return "date" }
	names := Names{Account: func(id api.AccountID) string { return string(id) },
		Folder: func(_ api.AccountID, id api.FolderID) string { return string(id) }}
	PageTitle(tr)
	PageDescription(tr)
	EmptyTitle(tr)
	EmptyDescription(tr)
	AddRule(tr)
	EditRule(tr)
	RemoveRule(tr)
	EnabledTooltip(tr)
	ScopeText("", names, tr)
	ScopeText("gone", Names{}, tr)
	for _, r := range []api.Rule{
		{},
		{Applied: 3, LastAppliedAt: &at, Skipped: 2},
		{Applied: 1, Skipped: 1},
	} {
		StatusLine(r, date, tr)
		StatusLine(r, nil, tr)
	}
	for _, p := range []api.RuleProblem{"", api.RuleProblemFolderGone, api.RuleProblemSendFailed} {
		ProblemOf(p, tr)
	}
	DeleteQuestion("r", tr)
	for _, acts := range [][]api.RuleAction{
		{{Kind: api.RuleActionForward, To: "a@b.cz"}, {Kind: api.RuleActionAutoReply, Text: "t"}},
		{{Kind: api.RuleActionForward, To: "a@b.cz"}},
		{{Kind: api.RuleActionAutoReply, Text: "t"}},
	} {
		OutgoingQuestion(api.RuleInput{Actions: acts}, tr)
	}
	EditorTitle(Form{}, tr)
	EditorTitle(Form{ID: "r"}, tr)
	NameLabel(tr)
	AccountLabel(tr)
	MatchLabel(tr)
	MatchText(api.RuleMatchAll, tr)
	MatchText(api.RuleMatchAny, tr)
	ActionsHeading(tr)
	AddCondition(tr)
	AddAction(tr)
	RemoveRow(tr)
	EnabledLabel(tr)
	StopLabel(tr)
	StopHint(tr)
	for _, f := range Fields {
		FieldText(f, tr)
		for _, op := range []api.RuleOperator{api.RuleOpContains, api.RuleOpNotContains, api.RuleOpIs,
			api.RuleOpIsNot, api.RuleOpStartsWith, api.RuleOpEndsWith, api.RuleOpDomainIs} {
			OperatorText(f, op, tr)
			ValuePlaceholder(f, op, tr)
		}
		for _, v := range append(append([]string{}, BulkValues...), ValueTrue, ValueFalse, "x") {
			ValueText(f, v, tr)
		}
	}
	for _, k := range ActionKinds {
		ActionText(k, tr)
		OutgoingHint(k, tr)
		ActionPhrase(api.RuleAction{Kind: k, FolderID: "f", To: "a@b.cz"}, "acc", names, tr)
		ActionPhrase(api.RuleAction{Kind: k}, "acc", Names{}, tr)
	}
	FolderPlaceholder(tr)
	ForwardPlaceholder(tr)
	ReplySubjectPlaceholder(tr)
	ReplyTextPlaceholder(tr)
	for k := ProblemNameMissing; k <= ProblemBadOperator; k++ {
		ProblemText(Problem{Kind: k}, tr)
	}
	for _, f := range []string{"name", "accountId", "confirmOutgoing", "conditions", "actions", "other"} {
		InvalidText(api.RuleInvalidData{Field: f}, tr)
	}
	NotFoundText(tr)
	SaveFailedText(tr)
	TestButton(tr)
	TestFolderLabel(tr)
	TestResultText(0, tr)
	TestResultText(2, tr)
	in := api.RuleInput{Match: api.RuleMatchAny, Stop: true,
		Conditions: []api.RuleCondition{{Field: api.RuleFieldFrom, Op: api.RuleOpIs, Value: "a"}, {Field: api.RuleFieldBulk, Op: api.RuleOpIs, Value: "any"}},
		Actions:    []api.RuleAction{{Kind: api.RuleActionMarkRead}}}
	ConditionsLine(in, tr)
	in.Match = api.RuleMatchAll
	ConditionsLine(in, tr)
	ActionsLine(in, "acc", names, tr)
	Rows([]api.Rule{{RuleInput: in}}, names, date, tr)
	PrefillName("x", tr)
	CreateFromMessageLabel(tr)
	ApplyLabel(tr)
	ApplyQuestion("f", tr)
	ApplyingText(tr)
	ApplyResultText(api.RulesApplyResult{}, tr)
	ApplyResultText(api.RulesApplyResult{Changed: 2}, tr)
	ApplyFailedText(tr)
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
			ours = ours || strings.Contains(line, "ui/internal/rules/")
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
// machine without make lint: every msgid it translates is in the template with its context and plural,
// and every entry that names a file of the package is one it translates.
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
			t.Errorf("po/malachi.pot names ui/internal/rules for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
	if own == 0 {
		t.Error("po/malachi.pot names no file of ui/internal/rules")
	}
}
