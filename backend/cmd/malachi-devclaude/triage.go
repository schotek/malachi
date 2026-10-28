// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The board's triage run and the suggested reply, through the real
// bridge: list_triage_queue until it hands out nothing, annotate_case for
// every case with the prepared note (or a plain one), add_commitment for
// the promises the note names, verbatim from the user's own messages;
// read_message and create_draft for a suggested reply.

// queueCase is one case of list_triage_queue: the trusted JSON and the
// fenced one (backend/cmd/malachi-mcp tools_triage.go queueCaseOut and
// queueCaseText).
type queueCase struct {
	CaseID         string `json:"caseId"`
	AccountID      string `json:"accountId"`
	InputKey       string `json:"inputKey"`
	RuleState      string `json:"ruleState"`
	RuleReason     string `json:"ruleReason"`
	ReplyMessageID string `json:"replyMessageId"`
	IssueKey       string `json:"issueKey"`
	HasDraft       bool   `json:"hasDraft"`
	Messages       []struct {
		MessageID string `json:"messageId"`
		Date      string `json:"date"`
		Mine      bool   `json:"mine"`
	} `json:"messages"`
	text queueText
}

type queueText struct {
	Subject     string `json:"subject"`
	IssueKey    string `json:"issueKey"`
	IssueStatus string `json:"issueStatus"`
	Messages    []struct {
		MessageID string `json:"messageId"`
		From      string `json:"from"`
		Text      string `json:"text"`
	} `json:"messages"`
}

var (
	caseHeadRE  = regexp.MustCompile(`^case \d+ of \d+:$`)
	fenceOpenRE = regexp.MustCompile(`^--- BEGIN UNTRUSTED MAIL CONTENT `)
	fenceEndRE  = regexp.MustCompile(`^--- END UNTRUSTED MAIL CONTENT `)
)

// parseQueue reads the cases of a list_triage_queue result: after each
// "case i of n:" line, the trusted JSON up to the fence, and the fenced
// JSON up to its end.
func parseQueue(text string) []queueCase {
	var cases []queueCase
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if !caseHeadRE.MatchString(lines[i]) {
			continue
		}
		var trusted, fenced []string
		j := i + 1
		for ; j < len(lines) && !fenceOpenRE.MatchString(lines[j]); j++ {
			if len(trusted) > 0 || strings.HasPrefix(lines[j], "{") {
				trusted = append(trusted, lines[j])
			}
		}
		for j++; j < len(lines) && !fenceEndRE.MatchString(lines[j]); j++ {
			fenced = append(fenced, lines[j])
		}
		var c queueCase
		if json.Unmarshal([]byte(strings.Join(trusted, "\n")), &c) != nil || c.CaseID == "" {
			continue
		}
		_ = json.Unmarshal([]byte(strings.Join(fenced, "\n")), &c.text)
		cases = append(cases, c)
		i = j
	}
	return cases
}

// note is the prepared note for a case, matched by the issue key or the
// subject; nil when the set has none.
func (set *answerSet) note(c queueCase) *triageNote {
	key := c.IssueKey
	if key == "" {
		key = c.text.IssueKey
	}
	for i := range set.Triage.Cases {
		n := &set.Triage.Cases[i]
		if n.IssueKey != "" && key != "" && strings.EqualFold(n.IssueKey, key) {
			return n
		}
		if n.IssueKey == "" && matchesAny(c.text.Subject, n.Match) {
			return n
		}
	}
	return nil
}

// plainNote is the note for a case the set does not know: the newest
// message's first sentences as the summary and the state's usual why.
func (set *answerSet) plainNote(c queueCase) triageNote {
	var n triageNote
	if len(c.text.Messages) > 0 {
		n.Summary = firstSentences(c.text.Messages[len(c.text.Messages)-1].Text, 2, 300)
	}
	n.Why = set.Triage.Why[c.RuleState]
	return n
}

// messageDate is the date of message id in the case, zero when unknown.
func (c queueCase) messageDate(id string) time.Time {
	for _, m := range c.Messages {
		if m.MessageID == id {
			if t, err := time.Parse(time.RFC3339, m.Date); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// mine says whether message id is the user's own.
func (c queueCase) mine(id string) bool {
	for _, m := range c.Messages {
		if m.MessageID == id {
			return m.Mine
		}
	}
	return false
}

// find is the id of the message whose text holds the quote (the newest
// first), with mine when the quote must be the user's own words; "".
func (c queueCase) find(quote string, mine bool) string {
	for i := len(c.text.Messages) - 1; i >= 0; i-- {
		m := c.text.Messages[i]
		if strings.Contains(m.Text, quote) && (!mine || c.mine(m.MessageID)) {
			return m.MessageID
		}
	}
	return ""
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

// dateOf is the date a quoted sentence names: the first weekday on or
// after the message's day (in this computer's zone) at the hour; "" when
// the weekday is unknown or the message's date is.
func dateOf(q quoted, messageDate time.Time) string {
	wd, ok := weekdays[strings.ToLower(q.Weekday)]
	if !ok || messageDate.IsZero() {
		return ""
	}
	t := messageDate.In(time.Local)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	for day.Weekday() != wd {
		day = day.AddDate(0, 0, 1)
	}
	hour := q.Hour
	if hour <= 0 {
		hour = 12
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, time.Local).Format(time.RFC3339)
}

// annotateArgs are the arguments of annotate_case for the note; the
// deadline only when its quote is found in a message of the case.
func annotateArgs(c queueCase, n triageNote) map[string]any {
	args := map[string]any{"caseId": c.CaseID, "inputKey": c.InputKey}
	if n.State != "" {
		args["state"] = n.State
	}
	if n.Title != "" {
		args["title"] = n.Title
	}
	if n.Summary != "" {
		args["summary"] = n.Summary
	}
	if n.Why != "" {
		args["why"] = n.Why
	}
	if len(n.Tasks) > 0 {
		args["tasks"] = n.Tasks
	}
	if n.Due != nil {
		if id := c.find(n.Due.Quote, n.Due.Mine); id != "" {
			if at := dateOf(*n.Due, c.messageDate(id)); at != "" {
				args["dueAt"], args["dueQuote"], args["dueMessageId"] = at, n.Due.Quote, id
			}
		}
	}
	return args
}

// triage runs the board's triage: the queue until it is empty, every case
// annotated, the commitments recorded; the final line names the count.
func (s *session) triage(ctx context.Context, set *answerSet) string {
	done := map[string]bool{}
	if s.bridge == nil {
		return set.Panel.Unavailable
	}
	for round := 0; round < 60; round++ {
		listing, failed := s.call(ctx, "list_triage_queue", map[string]any{"limit": 5})
		if failed {
			break
		}
		cases := parseQueue(listing)
		if len(cases) == 0 {
			break
		}
		for _, c := range cases {
			n := set.note(c)
			if n == nil {
				plain := set.plainNote(c)
				n = &plain
			}
			args := annotateArgs(c, *n)
			_, failed := s.call(ctx, "annotate_case", args)
			if failed && args["dueAt"] != nil {
				delete(args, "dueAt")
				delete(args, "dueQuote")
				delete(args, "dueMessageId")
				_, failed = s.call(ctx, "annotate_case", args)
			}
			if !failed {
				done[c.CaseID] = true
			}
			for _, k := range n.Commits {
				id := c.find(k.Quote, true)
				if id == "" {
					continue
				}
				commitArgs := map[string]any{"caseId": c.CaseID, "inputKey": c.InputKey, "messageId": id, "text": k.Text, "quote": k.Quote}
				if at := dateOf(k.quoted, c.messageDate(id)); at != "" {
					commitArgs["dueAt"] = at
				}
				s.call(ctx, "add_commitment", commitArgs)
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Sprintf(set.Triage.Done, len(done))
}

var (
	replyPromptRE = regexp.MustCompile(`Write a suggested reply to message ([A-Za-z0-9_-]+) in account ([A-Za-z0-9_-]+)\.`)
	othersRE      = regexp.MustCompile(`oldest first: ` + idList + `\.`)
)

// suggestReply is the board's Suggest Reply (or Suggest Follow-up): the
// message read, the prepared reply for its subject (else the plain one),
// created as the one draft the bridge allows.
func (s *session) suggestReply(ctx context.Context, set *answerSet, text string, followUp bool) string {
	m := replyPromptRE.FindStringSubmatch(text)
	if m == nil || s.bridge == nil {
		return set.Panel.Unavailable
	}
	target, account := m[1], m[2]
	ids := []string{target}
	if o := othersRE.FindStringSubmatch(text); o != nil {
		others := strings.Split(o[1], ", ")
		if len(others) > 2 {
			others = others[len(others)-2:]
		}
		ids = append(ids, others...)
	}
	subjects := s.read(ctx, account, ids)
	// The prompt names no language: the mail's decides, else the system
	// prompt's profile (written by the user in their language); the
	// subject is looked for in both sets, the fallback is the language's.
	lang := "en"
	if czech(strings.Join(subjects, " ")) || czech(s.opts.systemPrompt) {
		lang = "cs"
	}
	sets := s.sets(lang)
	set = sets[0]
	body := set.Reply.Generic
	if followUp {
		body = set.Reply.FollowUp
	}
	found := false
	for _, candidate := range sets {
		for _, a := range candidate.Reply.Cases {
			for _, subj := range subjects {
				if found || !matchesAny(subj, a.Match) {
					continue
				}
				if followUp && a.FollowUp != "" {
					body, found = a.FollowUp, true
				} else if !followUp && a.Body != "" {
					body, found = a.Body, true
				}
			}
		}
	}
	res, failed := s.call(ctx, "create_draft", map[string]any{"accountId": account, "mode": "reply", "messageId": target, "body": body})
	if failed {
		return strings.TrimSpace(res)
	}
	return set.Reply.Done
}
