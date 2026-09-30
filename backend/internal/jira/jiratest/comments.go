// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jiratest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

// Posted comments: POST /rest/api/{3,2}/issue/{id}/comment. The fake
// checks the body the way the sites do — cloud an ADF document (the subset
// internal/jira writes, against the schema's placement rules), dc a wiki
// markup string, either at most MaxCommentChars characters (an ADF
// document counted as its JSON) — stores the comment by the token's user
// with its properties, marks it jsdPublic false on a service-desk issue
// when sd.public.comment says internal, and answers 201 with the comment.
// A body the site would refuse is 400.

// MaxCommentChars is the sites' limit for a comment.
const MaxCommentChars = 32767

// CommentsOf returns copies of the issue's comments, oldest first.
func (f *Server) CommentsOf(issueID string) []Comment {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	out := make([]Comment, 0, len(is.Comments))
	for _, c := range is.Comments {
		cp := *c
		cp.Body = slices.Clone(c.Body)
		out = append(out, cp)
	}
	return out
}

func (f *Server) postComment(w http.ResponseWriter, ref string, body []byte, q url.Values) {
	is := f.issueByRef(ref)
	if is == nil {
		f.fail(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	var req struct {
		Body       json.RawMessage `json:"body"`
		Properties []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"properties"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		f.fail(w, http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}
	var rendered string
	var size int
	if f.mode == Cloud {
		if err := ValidateADF(req.Body); err != nil {
			f.fail(w, http.StatusBadRequest, "INVALID_INPUT: "+err.Error())
			return
		}
		var compact bytes.Buffer
		_ = json.Compact(&compact, req.Body)
		size = utf8.RuneCount(compact.Bytes())
		rendered = RenderADF(req.Body)
	} else {
		var wiki string
		if err := json.Unmarshal(req.Body, &wiki); err != nil || strings.TrimSpace(wiki) == "" {
			f.fail(w, http.StatusBadRequest, "Comment body can not be empty!")
			return
		}
		size = utf8.RuneCountInString(wiki)
		rendered = "<p>" + html.EscapeString(wiki) + "</p>"
	}
	if size > MaxCommentChars {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errorMessages": []string{},
			"errors": map[string]string{"comment": "The entered text is too long. It exceeds the allowed limit of 32,767 characters."}})
		return
	}
	props := map[string]any{}
	for _, p := range req.Properties {
		var v any
		if p.Key == "" || json.Unmarshal(p.Value, &v) != nil {
			f.fail(w, http.StatusBadRequest, "Invalid property")
			return
		}
		props[p.Key] = v
	}
	now := f.Now()
	c := &Comment{ID: f.nextID(), Author: f.Me, HTML: rendered, Created: now, Updated: now,
		Props: props, Body: slices.Clone(req.Body)}
	if p := f.projectLocked(is.Project); p != nil && p.ServiceDesk {
		internal := false
		if m, ok := props["sd.public.comment"].(map[string]any); ok {
			internal, _ = m["internal"].(bool)
		}
		public := !internal
		c.JsdPublic = &public
	}
	is.Comments = append(is.Comments, c)
	f.touchLocked(is, now)
	expand := strings.Split(q.Get("expand"), ",")
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(f.commentJSON(c, slices.Contains(expand, "renderedBody"), slices.Contains(expand, "properties")))
}

// adfNode is a node of an ADF document as the fake reads it.
type adfNode struct {
	Type    string          `json:"type"`
	Version *int            `json:"version,omitempty"`
	Attrs   json.RawMessage `json:"attrs,omitempty"`
	Content []adfNode       `json:"content,omitempty"`
	Text    *string         `json:"text,omitempty"`
	Marks   []struct {
		Type  string          `json:"type"`
		Attrs json.RawMessage `json:"attrs,omitempty"`
	} `json:"marks,omitempty"`
}

// adfChildren says what each node may contain ("inline" = text and
// hardBreak).
var adfChildren = map[string][]string{
	"doc":         {"paragraph", "heading", "bulletList", "orderedList", "blockquote", "codeBlock", "rule"},
	"paragraph":   {"text", "hardBreak"},
	"heading":     {"text", "hardBreak"},
	"bulletList":  {"listItem"},
	"orderedList": {"listItem"},
	"listItem":    {"paragraph", "codeBlock", "bulletList", "orderedList"},
	"blockquote":  {"paragraph", "bulletList", "orderedList", "codeBlock"},
	"codeBlock":   {"text"},
	"rule":        {},
	"hardBreak":   {},
}

// ValidateADF checks raw against the part of the ADF schema a comment of
// internal/jira may use: the document node, where each node may appear,
// non-empty text, known marks, code only with a link, a link with an href,
// list items starting with a paragraph or a code block, non-empty lists,
// quotes and list items.
func ValidateADF(raw json.RawMessage) error {
	var doc adfNode
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("not an ADF document: %v", err)
	}
	if doc.Type != "doc" || doc.Version == nil || *doc.Version != 1 {
		return errors.New("the root is no doc of version 1")
	}
	return validateADFNode(doc, 0)
}

func validateADFNode(n adfNode, depth int) error {
	if depth > 64 {
		return errors.New("nested too deep")
	}
	allowed, known := adfChildren[n.Type]
	if !known && n.Type != "text" {
		return fmt.Errorf("unknown node %q", n.Type)
	}
	if n.Type != "doc" && n.Version != nil {
		return fmt.Errorf("%s has a version", n.Type)
	}
	switch n.Type {
	case "text":
		if n.Text == nil || *n.Text == "" {
			return errors.New("empty text node")
		}
		if len(n.Content) > 0 {
			return errors.New("text with content")
		}
		seen := map[string]bool{}
		for _, m := range n.Marks {
			switch m.Type {
			case "strong", "em", "underline", "strike", "code":
			case "link":
				var a struct {
					Href string `json:"href"`
				}
				if json.Unmarshal(m.Attrs, &a) != nil || a.Href == "" {
					return errors.New("link without href")
				}
			default:
				return fmt.Errorf("unknown mark %q", m.Type)
			}
			if seen[m.Type] {
				return fmt.Errorf("mark %q twice", m.Type)
			}
			seen[m.Type] = true
		}
		if seen["code"] && len(seen) > 2 || seen["code"] && len(seen) == 2 && !seen["link"] {
			return errors.New("code combined with a mark other than link")
		}
		return nil
	case "heading":
		var a struct {
			Level int `json:"level"`
		}
		if json.Unmarshal(n.Attrs, &a) != nil || a.Level < 1 || a.Level > 6 {
			return errors.New("heading without a level")
		}
	case "bulletList", "orderedList", "blockquote", "listItem", "codeBlock":
		if len(n.Content) == 0 {
			return fmt.Errorf("empty %s", n.Type)
		}
	}
	if n.Text != nil {
		return fmt.Errorf("%s has text", n.Type)
	}
	if n.Type == "listItem" && n.Content[0].Type != "paragraph" && n.Content[0].Type != "codeBlock" {
		return errors.New("a list item starts with neither a paragraph nor a code block")
	}
	for _, c := range n.Content {
		if !slices.Contains(allowed, c.Type) {
			return fmt.Errorf("%s inside %s", c.Type, n.Type)
		}
		if n.Type == "codeBlock" && len(c.Marks) > 0 {
			return errors.New("marks inside a code block")
		}
		if err := validateADFNode(c, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// RenderADF is the HTML the fake site renders an ADF document as (the
// renderedBody of a posted comment); "" for what does not decode.
func RenderADF(raw json.RawMessage) string {
	var doc adfNode
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	var b strings.Builder
	renderADFNode(&b, doc)
	return b.String()
}

func renderADFNode(b *strings.Builder, n adfNode) {
	tags := map[string]string{
		"paragraph": "p", "bulletList": "ul", "orderedList": "ol", "listItem": "li",
		"blockquote": "blockquote", "codeBlock": "pre",
	}
	switch n.Type {
	case "text":
		s := html.EscapeString(*n.Text)
		for _, m := range n.Marks {
			switch m.Type {
			case "strong":
				s = "<b>" + s + "</b>"
			case "em":
				s = "<em>" + s + "</em>"
			case "code":
				s = "<code>" + s + "</code>"
			case "link":
				var a struct {
					Href string `json:"href"`
				}
				_ = json.Unmarshal(m.Attrs, &a)
				s = `<a href="` + html.EscapeString(a.Href) + `">` + s + "</a>"
			}
		}
		b.WriteString(s)
		return
	case "hardBreak":
		b.WriteString("<br/>")
		return
	case "rule":
		b.WriteString("<hr/>")
		return
	case "heading":
		var a struct {
			Level int `json:"level"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		fmt.Fprintf(b, "<h%d>", a.Level)
		for _, c := range n.Content {
			renderADFNode(b, c)
		}
		fmt.Fprintf(b, "</h%d>", a.Level)
		return
	}
	tag := tags[n.Type]
	if tag != "" {
		b.WriteString("<" + tag + ">")
	}
	for _, c := range n.Content {
		renderADFNode(b, c)
	}
	if tag != "" {
		b.WriteString("</" + tag + ">")
	}
}
