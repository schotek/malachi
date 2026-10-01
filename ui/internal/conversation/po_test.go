// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

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
// every branch that picks a msgid (the jira texts it reuses included).
func exercise(tr Translator) {
	members := []api.MessageSummary{
		issueMsg("c1", 0, false, api.MessageIssue{
			Item: api.IssueItemComment, Visibility: api.CommentInternal, Via: "Issue Sync", Edited: true,
		}),
		issueMsg("e1", 10, false, api.MessageIssue{
			Item: api.IssueItemEvent,
			Changes: []api.IssueChange{
				{Field: api.IssueFieldStatus, From: "To Do", To: "Done"},
				{Field: api.IssueFieldAssignee},
			},
		}),
	}
	m := Build(jiraThread(3), members, nil, jiraAccount, tr)
	Merge(m, issueMsg("c2", 20, false, api.MessageIssue{Item: api.IssueItemComment}), jiraAccount, tr)
	Build(jiraThread(4), members, nil, jiraAccount, tr)
	FoldAllCollapse.Label(tr)
	FoldAllExpand.Label(tr)
	QuotedTextLabel(false, tr)
	QuotedTextLabel(true, tr)
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
			ours = ours || strings.Contains(line, "ui/internal/conversation/")
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
// jira texts it reuses) is in the template with its context and plural,
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
			t.Errorf("po/malachi.pot names ui/internal/conversation for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
	if own == 0 {
		t.Error("po/malachi.pot names no file of ui/internal/conversation")
	}
}
