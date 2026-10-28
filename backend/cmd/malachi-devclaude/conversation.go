// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// kind is which of the clients' requests a conversation is, read from
// the start of the system prompt (ui/internal/assistant: SystemPrompt,
// TriageSystemPrompt, SuggestReplySystemPromptFor, RewriteSystemPrompt,
// WriteSystemPrompt, SearchSystemPrompt).
type kind int

const (
	kindPanel kind = iota
	kindTriage
	kindReply
	kindFollowUp
	kindRewrite
	kindWrite
	kindSearch
)

// request is what the system prompt says about the conversation.
type request struct {
	kind kind
	// lang is the language the prompt asks for ("en", "cs"), "" when it
	// names none (the one-shot requests: the message decides).
	lang string
}

func classify(system string) request {
	var r request
	switch {
	case strings.HasPrefix(system, "You triage the user's board"):
		r.kind = kindTriage
	case strings.HasPrefix(system, "You write one suggested follow-up"):
		r.kind = kindFollowUp
	case strings.HasPrefix(system, "You write one suggested reply"):
		r.kind = kindReply
	case strings.HasPrefix(system, "You rewrite a passage"):
		r.kind = kindRewrite
	case strings.HasPrefix(system, "Help the user compose an email"):
		r.kind = kindWrite
	case strings.HasPrefix(system, "You turn what the user wants to find"):
		r.kind = kindSearch
	default:
		r.kind = kindPanel
	}
	// "Answer in Czech unless …", "… reasons and tasks in Czech."
	if strings.Contains(system, " in Czech") {
		r.lang = "cs"
	} else if strings.Contains(system, " in English") {
		r.lang = "en"
	}
	return r
}

// session is one conversation: the request, the bridge and the answers.
type session struct {
	opts    options
	req     request
	out     *emitter
	bridge  *bridge
	stderr  io.Writer
	answers map[string]*answerSet
	turns   int
}

// set is the answers of a language, loaded once.
func (s *session) set(lang string) *answerSet {
	if lang != "cs" {
		lang = "en"
	}
	if s.answers == nil {
		s.answers = map[string]*answerSet{}
	}
	if a, ok := s.answers[lang]; ok {
		return a
	}
	a, err := loadAnswers(lang)
	if err != nil {
		fmt.Fprintf(s.stderr, "malachi-devclaude: %v\n", err)
		a = &answerSet{}
	}
	s.answers[lang] = a
	return a
}

// sets are the answers in the order a one-shot request looks for a
// match: the language of the message first, the other language second
// (a passage or a subject matches whichever set knows it), the
// fallback texts from the first.
func (s *session) sets(lang string) []*answerSet {
	if lang == "cs" {
		return []*answerSet{s.set("cs"), s.set("en")}
	}
	return []*answerSet{s.set("en"), s.set("cs")}
}

// language is the language of a turn: the system prompt's, else the
// message's.
func (s *session) language(text string) string {
	if s.req.lang != "" {
		return s.req.lang
	}
	if czech(text) {
		return "cs"
	}
	return "en"
}

// turn answers one user message.
func (s *session) turn(ctx context.Context, text string) {
	s.turns++
	lang := s.language(text)
	set := s.set(lang)
	var tools []string
	if s.bridge != nil {
		tools = s.bridge.tools
	}
	s.out.beginTurn(s.bridge != nil, tools, s.opts.systemPrompt, text)
	switch s.req.kind {
	case kindTriage:
		s.out.result(s.triage(ctx, set), nil, s.turns)
	case kindReply, kindFollowUp:
		s.out.result(s.suggestReply(ctx, set, text, s.req.kind == kindFollowUp), nil, s.turns)
	case kindRewrite:
		answer := s.rewrite(s.sets(lang), text)
		s.out.stream(answer)
		s.out.result(answer, nil, s.turns)
	case kindWrite:
		answer := s.write(s.sets(lang), text)
		s.out.stream(answer)
		s.out.result(answer, nil, s.turns)
	case kindSearch:
		query := s.search(s.sets(lang), text)
		structured := map[string]string{"query": query}
		b, _ := json.Marshal(structured)
		s.out.text(string(b))
		s.out.result(string(b), structured, s.turns)
	default:
		answer := s.panel(ctx, set, text)
		s.out.stream(answer)
		s.out.result(answer, nil, s.turns)
	}
}

// call runs a tool through the bridge, with its tool_use and tool_result
// events; the text is "" when there is no bridge.
func (s *session) call(ctx context.Context, tool string, args map[string]any) (text string, failed bool) {
	if s.bridge == nil {
		return "", true
	}
	id := s.out.toolUse(tool, args)
	text, isErr, err := s.bridge.call(ctx, tool, args)
	if err != nil {
		text, isErr = "tool call failed: "+err.Error(), true
	}
	s.out.toolResult(id, text, isErr)
	return text, isErr
}

// The prompts of ui/internal/assistant, English and Czech (po/cs.po), as
// far as the ids in them go.
var (
	idList          = `([A-Za-z0-9_-]+(?:, [A-Za-z0-9_-]+)*)`
	messagePromptRE = regexp.MustCompile(`(?:messages?|zprávu|zprávy) ` + idList + ` (?:in account|v účtu) ([A-Za-z0-9_-]+)`)
	replyTargetRE   = regexp.MustCompile(`(?:reply to message|odpověď na zprávu) ([A-Za-z0-9_-]+)`)
	unreadPromptRE  = regexp.MustCompile(`(?:folder|ve složce) ([A-Za-z0-9_-]+) (?:of account|účtu) ([A-Za-z0-9_-]+)`)
	attachmentRE    = regexp.MustCompile(`(?:attachment|přílohu) ([A-Za-z0-9_-]+) (?:of message|zprávy) ([A-Za-z0-9_-]+) (?:in account|v účtu) ([A-Za-z0-9_-]+)`)
	contextRE       = regexp.MustCompile(`Context: the user has (?:also )?selected (?:message|a conversation with messages) ` + idList + ` (?:\(newest first\) )?in account ([A-Za-z0-9_-]+)`)
	subjectLineRE   = regexp.MustCompile(`(?m)^subject: (.*)$`)
	listedIDRE      = regexp.MustCompile(`"id":\s*"([^"]+)"`)
)

// panel answers a turn of the panel: a quick action's prompt, a message
// action's, an attachment's question, or a free question.
func (s *session) panel(ctx context.Context, set *answerSet, text string) string {
	if m := unreadPromptRE.FindStringSubmatch(text); m != nil && strings.Contains(text, "list_messages") {
		return s.unread(ctx, set, m[2], m[1])
	}
	if m := attachmentRE.FindStringSubmatch(text); m != nil && strings.Contains(text, "get_attachment") {
		if s.bridge == nil {
			return set.Panel.Unavailable
		}
		s.call(ctx, "get_attachment", map[string]any{"accountId": m[3], "messageId": m[2], "partId": m[1]})
		return set.Panel.Ask
	}
	if m := messagePromptRE.FindStringSubmatch(text); m != nil {
		ids := strings.Split(m[1], ", ")
		account := m[2]
		subjects := s.read(ctx, account, ids)
		if s.bridge == nil {
			return set.Panel.Unavailable
		}
		switch {
		case strings.Contains(text, "create_draft"):
			target := ids[0]
			if t := replyTargetRE.FindStringSubmatch(text); t != nil {
				target = t[1]
			}
			return s.draftReply(ctx, set, account, target, subjects, wordsAfter(text, "The reply should say:", "Odpověď má říct:"))
		case strings.Contains(text, "tasks and deadlines") || strings.Contains(text, "úkoly a termíny"):
			return pick(set.Panel.Tasks, subjects, set.Panel.Ask)
		case strings.Contains(text, "My question:") || strings.Contains(text, "Moje otázka:"):
			return set.Panel.Ask
		}
		return pick(set.Panel.Summarize, subjects, set.Panel.Ask)
	}
	// A free question, maybe with the context line in front of it.
	if m := contextRE.FindStringSubmatch(text); m != nil {
		s.read(ctx, m[2], strings.Split(m[1], ", "))
	}
	return set.Panel.Free
}

// read reads the messages through the bridge (at most five) and returns
// their subjects, as the bridge's trusted "subject:" line reports them.
func (s *session) read(ctx context.Context, account string, ids []string) []string {
	var subjects []string
	for i, id := range ids {
		if i == 5 {
			break
		}
		text, failed := s.call(ctx, "read_message", map[string]any{"accountId": account, "messageId": id})
		if failed {
			continue
		}
		if m := subjectLineRE.FindStringSubmatch(text); m != nil {
			subjects = append(subjects, strings.TrimSpace(m[1]))
		}
	}
	return subjects
}

// pick is the answer whose match is in one of the subjects, else the
// fallback.
func pick(answers []subjectAnswer, subjects []string, fallback string) string {
	for _, a := range answers {
		for _, subj := range subjects {
			if matchesAny(subj, a.Match) {
				return a.Text
			}
		}
	}
	return fallback
}

// wordsAfter is the text after the last of the markers, trimmed; "" when
// none is there.
func wordsAfter(text string, markers ...string) string {
	for _, m := range markers {
		if i := strings.LastIndex(text, m); i >= 0 {
			return strings.TrimSpace(text[i+len(m):])
		}
	}
	return ""
}

// unread is Summarize Unread in This Folder: the unread messages listed
// and read, then the prepared answer.
func (s *session) unread(ctx context.Context, set *answerSet, account, folder string) string {
	if s.bridge == nil {
		return set.Panel.Unavailable
	}
	listing, failed := s.call(ctx, "list_messages", map[string]any{"accountId": account, "folderId": folder, "filter": "unread", "limit": 20})
	if failed {
		return set.Panel.Unavailable
	}
	var ids []string
	for _, m := range listedIDRE.FindAllStringSubmatch(listing, -1) {
		if strings.HasPrefix(m[1], "m_") {
			ids = append(ids, m[1])
		}
	}
	if len(ids) > 8 {
		ids = ids[:8]
	}
	// The whole listing is read (the answer sorts every unread message).
	for _, id := range ids {
		s.call(ctx, "read_message", map[string]any{"accountId": account, "messageId": id})
	}
	return set.Panel.Unread
}

// draftReply creates the reply draft the panel's Draft a Reply… asks for
// and says so; the body is the prepared one for the subject, else a plain
// reply built from the user's words.
func (s *session) draftReply(ctx context.Context, set *answerSet, account, target string, subjects []string, words string) string {
	var answer draftAnswer
	for _, a := range set.Panel.DraftReply {
		for _, subj := range subjects {
			if matchesAny(subj, a.Match) {
				answer = a
			}
		}
	}
	if answer.Body == "" {
		answer.Body = words
		answer.Text = fmt.Sprintf(set.Panel.DraftDone, oneLine(words))
	}
	res, failed := s.call(ctx, "create_draft", map[string]any{"accountId": account, "mode": "reply", "messageId": target, "body": answer.Body})
	if failed {
		return strings.TrimSpace(res)
	}
	return answer.Text
}

// rewrite answers the compose window's rewrite: the preset of the
// instruction line, the passage between the markers, the prepared text
// for the passage, else a plain change of it.
func (s *session) rewrite(sets []*answerSet, text string) string {
	instruction, rest, _ := strings.Cut(text, "\n")
	passage := rest
	if _, after, ok := strings.Cut(rest, "<<<\n"); ok {
		passage = after
		if before, _, ok := strings.Cut(passage, "\n>>>"); ok {
			passage = before
		}
	}
	passage = strings.TrimSpace(passage)
	preset := "custom"
	switch {
	case strings.Contains(instruction, "more polite"):
		preset = "politer"
	case strings.Contains(instruction, "shorter"):
		preset = "shorter"
	case strings.HasPrefix(instruction, "Fix spelling"):
		preset = "fix"
	case strings.Contains(instruction, "into English"):
		preset = "english"
	}
	for _, set := range sets {
		if answer := pick(set.Rewrite[preset], []string{passage}, ""); answer != "" {
			return answer
		}
	}
	switch preset {
	case "shorter":
		return firstSentences(passage, 2, 400)
	case "politer":
		if czech(passage) {
			return "Dobrý den,\n\n" + passage + "\n\nDěkuji a přeji hezký den."
		}
		return "Hello,\n\n" + passage + "\n\nThank you, and have a good day."
	}
	return passage
}

// writeRequest is the JSON of the compose window's assistant bar
// (ui/internal/assistant WriteMessage).
type writeRequest struct {
	Mode            string `json:"mode"`
	Instruction     string `json:"instruction"`
	Subject         string `json:"subject"`
	ExistingText    string `json:"existingText"`
	OriginalMessage string `json:"originalMessage"`
}

// write answers the compose window's assistant bar: the prepared text for
// the subject or the original, else a plain message from the instruction.
func (s *session) write(sets []*answerSet, text string) string {
	var r writeRequest
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return sets[0].Panel.Ask
	}
	for _, set := range sets {
		if answer := pick(set.Write, []string{r.Subject, r.OriginalMessage}, ""); answer != "" {
			return answer
		}
	}
	cs := czech(r.Instruction + r.ExistingText + r.OriginalMessage)
	instruction := oneLine(r.Instruction)
	if instruction == "" {
		instruction = oneLine(r.ExistingText)
	}
	if cs {
		return "Dobrý den,\n\n" + instruction + "\n\nS pozdravem\n[jméno]"
	}
	return "Hello,\n\n" + instruction + "\n\nKind regards,\n[name]"
}

// search answers the search in the user's own words: the prepared query
// for the words, else one made from them.
func (s *session) search(sets []*answerSet, text string) string {
	words := strings.TrimSpace(text)
	for _, set := range sets {
		for _, a := range set.Search {
			if strings.EqualFold(oneLine(a.Words), oneLine(words)) {
				return a.Query
			}
		}
	}
	return madeQuery(words)
}

// stopWords are the words that only describe a search.
var stopWords = map[string]bool{
	"mail": true, "mails": true, "email": true, "emails": true, "e-mail": true, "e-mails": true,
	"message": true, "messages": true, "the": true, "a": true, "an": true, "about": true, "in": true,
	"my": true, "with": true, "of": true, "for": true, "and": true, "on": true, "find": true,
	"show": true, "me": true, "all": true, "to": true,
	"pošta": true, "poštu": true, "pošty": true, "zprávy": true, "zpráva": true, "zprávu": true,
	"maily": true, "mail,": true, "o": true, "v": true, "ve": true, "s": true, "se": true, "na": true,
	"mi": true, "najdi": true, "ukaž": true, "všechny": true, "co": true, "které": true, "kde": true,
}

// madeQuery is a query of the search syntax made from words: from:X for
// the word after from/od, is:unread and has:attachment for the usual
// words, the rest as plain words without the stop words, Czech words cut
// to a prefix (the search matches prefixes).
func madeQuery(words string) string {
	cs := czech(words)
	fields := strings.Fields(strings.ToLower(words))
	var out []string
	for i := 0; i < len(fields); i++ {
		w := strings.Trim(fields[i], ".,;:!?\"'„“”")
		switch {
		case w == "":
		case (w == "from" || w == "od") && i+1 < len(fields):
			i++
			name := strings.Trim(fields[i], ".,;:!?\"'")
			if cs && len([]rune(name)) > 4 {
				name = string([]rune(name)[:len([]rune(name))-1])
			}
			out = append(out, "from:"+name)
		case w == "unread" || w == "nepřečtené" || w == "nepřečtená":
			out = append(out, "is:unread")
		case w == "flagged" || w == "starred" || w == "hvězdičkou":
			out = append(out, "is:flagged")
		case strings.HasPrefix(w, "attachment") || strings.HasPrefix(w, "příloh"):
			out = append(out, "has:attachment")
		case stopWords[w]:
		default:
			if cs {
				if r := []rune(w); len(r) > 6 {
					w = string(r[:len(r)-2])
				}
			}
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return strings.TrimSpace(words)
	}
	return strings.Join(out, " ")
}
