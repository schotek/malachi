// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jiratest

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A small JQL evaluator for Server: enough of the language for the
// queries the package and its syncer build, strict about the rest (an
// unsupported clause is a 400, so a test notices a query the fake does
// not understand instead of passing on a wrong answer).
//
// Supported: AND, OR, NOT, parentheses; ORDER BY updated|created|id|key
// [ASC|DESC], ...; fields project, id, issuekey/key/issue, assignee,
// reporter, watcher, status, statusCategory, updated, created; operators
// = != > >= < <= in, not in, is [not] EMPTY; values: words, "quoted"
// strings, currentUser(), updatedBy(user, "-Nd") (issues with a comment
// or changelog entry of the user within the window), relative times
// "-Nm" "-Nh" "-Nd" "-Nw" and "yyyy-mm-dd[ hh:mm]" (UTC).

type jqlQuery struct {
	where jqlNode // nil = every issue
	order []jqlOrder
}

type jqlOrder struct {
	field string
	desc  bool
}

type jqlNode interface {
	eval(f *Server, is *Issue) bool
	// ids lists the ids an "id = / id in" clause names (a DC search that
	// validates its query fails on one that does not exist).
	ids() []string
}

type jqlAnd []jqlNode
type jqlOr []jqlNode
type jqlNot struct{ n jqlNode }

type jqlValue struct {
	text string
	fn   string // lower-cased function name; "" = a plain value
	args []jqlValue
}

type jqlClause struct {
	field  string // lower-cased
	op     string // lower-cased
	values []jqlValue
}

var jqlFields = map[string]bool{
	"project": true, "id": true, "issuekey": true, "key": true, "issue": true, "assignee": true,
	"reporter": true, "watcher": true, "status": true, "statuscategory": true, "updated": true, "created": true,
}

func (n jqlAnd) eval(f *Server, is *Issue) bool {
	for _, c := range n {
		if !c.eval(f, is) {
			return false
		}
	}
	return true
}

func (n jqlOr) eval(f *Server, is *Issue) bool {
	for _, c := range n {
		if c.eval(f, is) {
			return true
		}
	}
	return false
}

func (n jqlNot) eval(f *Server, is *Issue) bool { return !n.n.eval(f, is) }

func (n jqlAnd) ids() []string { return childIDs(n) }
func (n jqlOr) ids() []string  { return childIDs(n) }
func (n jqlNot) ids() []string { return n.n.ids() }

func childIDs(ns []jqlNode) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.ids()...)
	}
	return out
}

func (c *jqlClause) ids() []string {
	if c.field != "id" || (c.op != "=" && c.op != "in") {
		return nil
	}
	var out []string
	for _, v := range c.values {
		out = append(out, v.text)
	}
	return out
}

func (c *jqlClause) eval(f *Server, is *Issue) bool {
	switch c.field {
	case "updated", "created":
		t := is.Updated
		if c.field == "created" {
			t = is.Created
		}
		ref, _ := jqlTime(f, c.values[0])
		switch c.op {
		case ">=":
			return !t.Before(ref)
		case ">":
			return t.After(ref)
		case "<=":
			return !t.After(ref)
		case "<":
			return t.Before(ref)
		case "=":
			return t.Equal(ref)
		case "!=":
			return !t.Equal(ref)
		}
		return false
	}
	var own []string // the issue's values of the field; empty = EMPTY
	switch c.field {
	case "project":
		own = []string{is.Project}
		if p := f.projectLocked(is.Project); p != nil {
			own = append(own, strings.ToLower(p.Key))
		}
	case "id":
		own = []string{is.ID}
	case "issuekey", "key", "issue":
		own = []string{is.ID, strings.ToLower(is.Key)}
	case "assignee", "reporter":
		u := is.Assignee
		if c.field == "reporter" {
			u = is.Reporter
		}
		if u != "" {
			own = []string{strings.ToLower(u)}
			if fu := f.users[u]; fu != nil && fu.Name != "" {
				own = append(own, strings.ToLower(fu.Name))
			}
		}
	case "watcher":
		if is.Watching {
			own = []string{strings.ToLower(f.Me)}
		}
	case "status":
		st := f.statusLocked(is.Status)
		own = []string{st.ID, strings.ToLower(st.Name)}
	case "statuscategory":
		cat := f.statusLocked(is.Status).Category
		own = []string{cat, map[string]string{"new": "to do", "indeterminate": "in progress", "done": "done"}[cat]}
	}
	matches := func(v jqlValue) bool {
		if v.fn == "updatedby" {
			return jqlUpdatedBy(f, is, v)
		}
		want := strings.ToLower(v.text)
		if v.fn == "currentuser" {
			want = strings.ToLower(f.Me)
		}
		for _, o := range own {
			if o == want {
				return true
			}
		}
		return false
	}
	anyMatch := false
	for _, v := range c.values {
		if matches(v) {
			anyMatch = true
			break
		}
	}
	switch c.op {
	case "=", "in":
		return anyMatch
	case "!=", "not in":
		return !anyMatch && len(own) > 0
	case "is":
		return len(own) == 0
	case "is not":
		return len(own) > 0
	}
	return false
}

// jqlUpdatedBy is updatedBy(user, "-Nd"): the user commented or changed
// the issue within the window.
func jqlUpdatedBy(f *Server, is *Issue, v jqlValue) bool {
	if len(v.args) == 0 {
		return false
	}
	user := strings.ToLower(v.args[0].text)
	if v.args[0].fn == "currentuser" {
		user = strings.ToLower(f.Me)
	}
	since := time.Time{}
	if len(v.args) > 1 {
		since, _ = jqlTime(f, v.args[1])
	}
	for _, c := range is.Comments {
		if strings.ToLower(c.Author) == user && !c.Created.Before(since) {
			return true
		}
	}
	for _, h := range is.Histories {
		if strings.ToLower(h.Author) == user && !h.Created.Before(since) {
			return true
		}
	}
	return false
}

// jqlTime reads a relative ("-30d") or absolute ("2026-09-01 10:00") time.
func jqlTime(f *Server, v jqlValue) (time.Time, error) {
	s := strings.TrimSpace(v.text)
	if len(s) >= 2 && (s[0] == '-' || s[0] == '+') {
		n, err := strconv.Atoi(s[1 : len(s)-1])
		if err != nil {
			return time.Time{}, fmt.Errorf("bad relative time %q", s)
		}
		if s[0] == '-' {
			n = -n
		}
		unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
		if unit == 0 {
			return time.Time{}, fmt.Errorf("bad relative time %q", s)
		}
		return f.Now().Add(time.Duration(n) * unit), nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006/01/02 15:04", "2006-01-02", "2006/01/02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

func (q jqlQuery) sort(issues []*Issue) {
	order := q.order
	if len(order) == 0 {
		order = []jqlOrder{{field: "updated", desc: true}}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		for _, o := range order {
			var c int
			switch o.field {
			case "updated":
				c = a.Updated.Compare(b.Updated)
			case "created":
				c = a.Created.Compare(b.Created)
			case "key":
				c = strings.Compare(a.Key, b.Key)
			case "id":
				c = int(mustInt(a.ID) - mustInt(b.ID))
			}
			if c != 0 {
				return (c < 0) != o.desc
			}
		}
		return mustInt(a.ID) > mustInt(b.ID)
	})
}

// Parsing.

// jqlTokens splits a query; a quoted string becomes a token starting with
// '"' (so it is never a keyword).
func jqlTokens(s string) ([]string, error) {
	var toks []string
	isSpecial := func(c byte) bool { return strings.IndexByte(" \t\r\n(),=!<>~\"'", c) >= 0 }
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '(' || c == ')' || c == ',' || c == '=' || c == '~':
			toks = append(toks, string(c))
			i++
		case c == '!' || c == '<' || c == '>':
			if i+1 < len(s) && s[i+1] == '=' {
				toks = append(toks, s[i:i+2])
				i += 2
			} else if c == '!' {
				return nil, errors.New("stray '!'")
			} else {
				toks = append(toks, string(c))
				i++
			}
		case c == '"' || c == '\'':
			var b strings.Builder
			j := i + 1
			for ; j < len(s) && s[j] != c; j++ {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
			}
			if j >= len(s) {
				return nil, errors.New("unterminated string")
			}
			toks = append(toks, `"`+b.String())
			i = j + 1
		default:
			j := i
			for j < len(s) && !isSpecial(s[j]) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks, nil
}

type jqlParser struct {
	toks []string
	pos  int
}

func (p *jqlParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *jqlParser) next() string {
	t := p.peek()
	if t != "" {
		p.pos++
	}
	return t
}

// kw consumes the keyword k (case-insensitive, never a quoted string).
func (p *jqlParser) kw(k string) bool {
	if t := p.peek(); t != "" && t[0] != '"' && strings.EqualFold(t, k) {
		p.pos++
		return true
	}
	return false
}

func parseJQL(s string) (jqlQuery, error) {
	toks, err := jqlTokens(s)
	if err != nil {
		return jqlQuery{}, fmt.Errorf("fake JQL: %w", err)
	}
	p := &jqlParser{toks: toks}
	var q jqlQuery
	if t := p.peek(); t != "" && !strings.EqualFold(t, "order") {
		if q.where, err = p.or(); err != nil {
			return jqlQuery{}, err
		}
	}
	if p.kw("order") {
		if !p.kw("by") {
			return jqlQuery{}, errors.New("fake JQL: ORDER without BY")
		}
		for {
			field := strings.ToLower(p.next())
			switch field {
			case "updated", "created", "id", "key":
			default:
				return jqlQuery{}, fmt.Errorf("fake JQL: cannot order by %q", field)
			}
			o := jqlOrder{field: field}
			if p.kw("desc") {
				o.desc = true
			} else {
				p.kw("asc")
			}
			q.order = append(q.order, o)
			if p.peek() != "," {
				break
			}
			p.next()
		}
	}
	if t := p.peek(); t != "" {
		return jqlQuery{}, fmt.Errorf("fake JQL: unexpected %q", t)
	}
	return q, nil
}

func (p *jqlParser) or() (jqlNode, error) {
	n, err := p.and()
	if err != nil {
		return nil, err
	}
	or := jqlOr{n}
	for p.kw("or") {
		m, err := p.and()
		if err != nil {
			return nil, err
		}
		or = append(or, m)
	}
	if len(or) == 1 {
		return n, nil
	}
	return or, nil
}

func (p *jqlParser) and() (jqlNode, error) {
	n, err := p.unary()
	if err != nil {
		return nil, err
	}
	and := jqlAnd{n}
	for p.kw("and") {
		m, err := p.unary()
		if err != nil {
			return nil, err
		}
		and = append(and, m)
	}
	if len(and) == 1 {
		return n, nil
	}
	return and, nil
}

func (p *jqlParser) unary() (jqlNode, error) {
	if p.kw("not") {
		n, err := p.unary()
		if err != nil {
			return nil, err
		}
		return jqlNot{n}, nil
	}
	if p.peek() == "(" {
		p.next()
		n, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.next() != ")" {
			return nil, errors.New("fake JQL: missing ')'")
		}
		return n, nil
	}
	return p.clause()
}

func (p *jqlParser) clause() (jqlNode, error) {
	field := strings.ToLower(p.next())
	if !jqlFields[field] {
		return nil, fmt.Errorf("fake JQL: unsupported field %q", field)
	}
	var op string
	switch t := strings.ToLower(p.next()); t {
	case "=", "!=", ">=", "<=", ">", "<":
		op = t
	case "in":
		op = "in"
	case "not":
		if !p.kw("in") {
			return nil, errors.New("fake JQL: NOT without IN")
		}
		op = "not in"
	case "is":
		op = "is"
		if p.kw("not") {
			op = "is not"
		}
	default:
		return nil, fmt.Errorf("fake JQL: unsupported operator %q", t)
	}
	c := &jqlClause{field: field, op: op}
	if (op == "in" || op == "not in") && p.peek() == "(" {
		p.next()
		for p.peek() != ")" {
			v, err := p.operand()
			if err != nil {
				return nil, err
			}
			c.values = append(c.values, v)
			if p.peek() == "," {
				p.next()
			}
		}
		p.next()
		return c, nil
	}
	v, err := p.operand()
	if err != nil {
		return nil, err
	}
	if (field == "updated" || field == "created") && v.fn == "" {
		if _, err := jqlTime(&Server{Now: time.Now}, v); err != nil {
			return nil, fmt.Errorf("fake JQL: %w", err)
		}
	}
	c.values = []jqlValue{v}
	return c, nil
}

func (p *jqlParser) operand() (jqlValue, error) {
	t := p.next()
	switch {
	case t == "" || t == ")" || t == ",":
		return jqlValue{}, errors.New("fake JQL: missing value")
	case t[0] == '"':
		return jqlValue{text: t[1:]}, nil
	case p.peek() == "(":
		p.next()
		v := jqlValue{fn: strings.ToLower(t)}
		for p.peek() != ")" {
			a, err := p.operand()
			if err != nil {
				return jqlValue{}, err
			}
			v.args = append(v.args, a)
			if p.peek() == "," {
				p.next()
			}
		}
		p.next()
		switch v.fn {
		case "currentuser", "updatedby":
		default:
			return jqlValue{}, fmt.Errorf("fake JQL: unsupported function %s()", v.fn)
		}
		return v, nil
	}
	return jqlValue{text: t}, nil
}
