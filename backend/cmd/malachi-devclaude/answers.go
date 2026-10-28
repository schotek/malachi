// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// The prepared answers, one set per language, written for the sample
// world of malachi-devmail (its samples/en.json and samples/cs.json) and
// chosen by what a request asks for and the subject of the mail it names.
//
//go:embed answers/en.json answers/cs.json
var answerFiles embed.FS

// answerSet is one language's answers.
type answerSet struct {
	Panel struct {
		// Unread is the answer of Summarize Unread in This Folder.
		Unread string `json:"unread"`
		// Summarize, Tasks and DraftReply are chosen by the subject of the
		// mail read; Ask and Free answer any question.
		Summarize  []subjectAnswer `json:"summarize"`
		Tasks      []subjectAnswer `json:"tasks"`
		DraftReply []draftAnswer   `json:"draftReply"`
		Ask        string          `json:"ask"`
		Free       string          `json:"free"`
		// DraftDone is the text after a draft was created when the subject
		// is unknown; %s is the user's words.
		DraftDone string `json:"draftDone"`
		// Unavailable is the answer when a request names mail and there
		// is no bridge to read it with.
		Unavailable string `json:"unavailable"`
	} `json:"panel"`
	Triage struct {
		Cases []triageNote `json:"cases"`
		// The fallback note's why by rule state (hot, you, them, info).
		Why map[string]string `json:"why"`
		// Done is the final line of a run; %d the cases annotated.
		Done string `json:"done"`
	} `json:"triage"`
	Reply struct {
		Cases []replyAnswer `json:"cases"`
		// Generic and FollowUp are the fallbacks; Done the final line.
		Generic  string `json:"generic"`
		FollowUp string `json:"followUp"`
		Done     string `json:"done"`
	} `json:"reply"`
	Rewrite map[string][]subjectAnswer `json:"rewrite"`
	Write   []subjectAnswer            `json:"write"`
	Search  []searchAnswer             `json:"search"`
}

// subjectAnswer is an answer for the mail whose subject (or, for a
// rewrite, whose passage) contains one of Match.
type subjectAnswer struct {
	Match []string `json:"match"`
	Text  string   `json:"text"`
}

// draftAnswer is a reply draft for the mail whose subject contains one of
// Match: Body is the draft, Text what the panel then says.
type draftAnswer struct {
	Match []string `json:"match"`
	Body  string   `json:"body"`
	Text  string   `json:"text"`
}

// replyAnswer is a suggested reply (or, for a case that waits on the
// other side, a follow-up) for the mail whose subject contains one of
// Match.
type replyAnswer struct {
	Match    []string `json:"match"`
	Body     string   `json:"body"`
	FollowUp string   `json:"followUp"`
}

// searchAnswer is the query for the words the user typed.
type searchAnswer struct {
	Words string `json:"words"`
	Query string `json:"query"`
}

// triageNote is the assistant's note on one case of the board: matched by
// the subject (Match) or the issue key, with an optional deadline and the
// user's commitments, both quoted verbatim from the case's messages.
type triageNote struct {
	Match    []string `json:"match"`
	IssueKey string   `json:"issueKey"`
	State    string   `json:"state"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Why      string   `json:"why"`
	Tasks    []string `json:"tasks"`
	Due      *quoted  `json:"due"`
	Commits  []commit `json:"commitments"`
}

// quoted is a sentence of a message that states a date: the weekday the
// sentence names (the first on or after the message's day) at Hour.
type quoted struct {
	Quote   string `json:"quote"`
	Weekday string `json:"weekday"`
	Hour    int    `json:"hour"`
	// Mine: the sentence is in one of the user's own messages.
	Mine bool `json:"mine"`
}

// commit is a promise of the user's own message.
type commit struct {
	quoted
	Text string `json:"text"`
}

// loadAnswers reads the set of a language ("en", "cs").
func loadAnswers(lang string) (*answerSet, error) {
	b, err := answerFiles.ReadFile("answers/" + lang + ".json")
	if err != nil {
		return nil, err
	}
	var s answerSet
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("answers/%s.json: %w", lang, err)
	}
	return &s, nil
}

// containsFold says whether s contains needle, ignoring case.
func containsFold(s, needle string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(needle))
}

// matchesAny says whether any of needles is in s (case-insensitively).
func matchesAny(s string, needles []string) bool {
	for _, n := range needles {
		if n != "" && containsFold(s, n) {
			return true
		}
	}
	return false
}

// czech says whether text reads as Czech rather than English: the
// common short words of each language are counted (a name with a háček
// in an English sentence does not make it Czech), and when neither side
// wins, the letters with a háček, a kroužek or a čárka decide.
func czech(text string) bool {
	cs, en := 0, 0
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,;:!?\"'„“”()")
		if czechWords[w] {
			cs++
		}
		if englishWords[w] {
			en++
		}
	}
	if cs != en {
		return cs > en
	}
	for _, r := range text {
		if strings.ContainsRune("ěščřžůďťňĚŠČŘŽŮĎŤŇ", r) {
			return true
		}
	}
	return false
}

var czechWords = map[string]bool{
	"je": true, "se": true, "na": true, "v": true, "ve": true, "pro": true, "že": true, "jsem": true, "jste": true,
	"mám": true, "máte": true, "prosím": true, "děkuji": true, "dobrý": true, "den": true, "od": true, "ale": true,
	"jak": true, "nebo": true, "bych": true, "byste": true, "mi": true, "vám": true, "nám": true, "za": true,
	"před": true, "po": true, "jinak": true, "pošlete": true, "čas": true, "příští": true, "týden": true,
	"také": true, "ještě": true, "ano": true, "ne": true, "já": true, "vy": true, "si": true, "už": true,
}

var englishWords = map[string]bool{
	"the": true, "and": true, "you": true, "is": true, "to": true, "for": true, "with": true, "of": true, "in": true,
	"on": true, "at": true, "my": true, "your": true, "i": true, "me": true, "it": true, "this": true, "that": true,
	"please": true, "thank": true, "thanks": true, "hello": true, "hi": true, "dear": true, "regards": true,
	"from": true, "about": true, "before": true, "there": true, "have": true, "send": true, "time": true, "next": true,
	"week": true, "or": true, "not": true, "be": true, "are": true, "will": true, "can": true, "could": true,
}

// oneLine is s as one line: every run of white space one space, control
// characters left out, trimmed.
func oneLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// firstSentences is the first n sentences of text as one line, at most
// limit bytes.
func firstSentences(text string, n, limit int) string {
	s := oneLine(text)
	count := 0
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			count++
			if count == n {
				s = s[:i+1]
				break
			}
		}
	}
	if len(s) > limit {
		cut := limit
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		s = strings.TrimSpace(s[:cut]) + "…"
	}
	return s
}
