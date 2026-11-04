// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"encoding/json"
)

// The Atlassian Document Format (version 1) of a comment for Jira Cloud,
// written from the document model of comment.go. Only the nodes and marks
// the schema allows where they are written: a heading or a rule exists at
// the top level only (inside a quote or a list item a heading becomes a
// bold paragraph and a rule goes), quotes do not nest and are not put in
// list items (their content is), a list item starts with a paragraph or a
// code block, text nodes are never empty, and the code mark goes with no
// mark but a link. Text is text: ADF has no markup inside it.

// adfNode is a node of an ADF document.
type adfNode struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []adfNode      `json:"content,omitempty"`
	Text    string         `json:"text,omitempty"`
	Marks   []adfMark      `json:"marks,omitempty"`
}

// adfMark is a mark of a text node.
type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// adfPlace is where blocks are written: what the schema allows there.
type adfPlace int

const (
	adfTop   adfPlace = iota // the document
	adfQuote                 // a blockquote: paragraphs, lists, code blocks
	adfItem                  // a list item: paragraphs, lists, code blocks
)

// adfMarkNames are the marks in the order they are written.
var adfMarkNames = []struct {
	bit  runMarks
	name string
}{
	{markStrong, "strong"}, {markEm, "em"}, {markUnderline, "underline"}, {markStrike, "strike"}, {markCode, "code"},
}

// adfDocument is the JSON of the ADF document of blocks (compact, HTML
// characters not escaped, so its length is what the site counts).
func adfDocument(blocks []docBlock) (json.RawMessage, error) {
	doc := struct {
		Version int       `json:"version"`
		Type    string    `json:"type"`
		Content []adfNode `json:"content"`
	}{Version: 1, Type: "doc", Content: adfBlocks(blocks, adfTop)}
	if doc.Content == nil {
		doc.Content = []adfNode{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSpace(buf.Bytes())), nil
}

func adfBlocks(blocks []docBlock, place adfPlace) []adfNode {
	var out []adfNode
	for _, b := range blocks {
		switch b.kind {
		case docPara:
			out = append(out, adfNode{Type: "paragraph", Content: adfRuns(b.runs, 0)})
		case docHeading:
			if place == adfTop {
				out = append(out, adfNode{Type: "heading", Attrs: map[string]any{"level": b.level}, Content: adfRuns(b.runs, 0)})
			} else {
				out = append(out, adfNode{Type: "paragraph", Content: adfRuns(b.runs, markStrong)})
			}
		case docCode:
			out = append(out, adfNode{Type: "codeBlock", Content: []adfNode{{Type: "text", Text: b.code}}})
		case docRule:
			if place == adfTop {
				out = append(out, adfNode{Type: "rule"})
			}
		case docQuote:
			if place == adfTop {
				if inner := adfBlocks(b.blocks, adfQuote); len(inner) > 0 {
					out = append(out, adfNode{Type: "blockquote", Content: inner})
				}
			} else {
				out = append(out, adfBlocks(b.blocks, place)...)
			}
		case docList:
			typ := "bulletList"
			if b.ordered {
				typ = "orderedList"
			}
			list := adfNode{Type: typ}
			for _, it := range b.items {
				content := adfBlocks(it, adfItem)
				if len(content) == 0 || (content[0].Type != "paragraph" && content[0].Type != "codeBlock") {
					content = append([]adfNode{{Type: "paragraph"}}, content...)
				}
				list.Content = append(list.Content, adfNode{Type: "listItem", Content: content})
			}
			if len(list.Content) > 0 {
				out = append(out, list)
			}
		}
	}
	return out
}

// adfRuns writes a paragraph's runs; extra marks every run that is not
// code.
func adfRuns(runs []docRun, extra runMarks) []adfNode {
	out := make([]adfNode, 0, len(runs))
	for _, r := range runs {
		if r.br {
			out = append(out, adfNode{Type: "hardBreak"})
			continue
		}
		m := r.marks
		if m&markCode == 0 {
			m |= extra
		}
		var marks []adfMark
		for _, x := range adfMarkNames {
			if m&x.bit != 0 {
				marks = append(marks, adfMark{Type: x.name})
			}
		}
		if r.href != "" {
			marks = append(marks, adfMark{Type: "link", Attrs: map[string]any{"href": r.href}})
		}
		out = append(out, adfNode{Type: "text", Text: r.text, Marks: marks})
	}
	return out
}
