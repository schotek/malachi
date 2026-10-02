// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/jira"
)

func TestBoardTitleText(t *testing.T) {
	if got := boardTitleText("Subject line", false); got != "Subject line" {
		t.Errorf("boardTitleText(subject) = %q", got)
	}
	if got := boardTitleText("", true); got != "" {
		t.Errorf("boardTitleText(empty assistant title) = %q, want empty (nothing to mark)", got)
	}
	if got := boardTitleText("Assistant title", true); got != board.AssistantMark+" Assistant title" {
		t.Errorf("boardTitleText(assistant) = %q", got)
	}
}

func TestBoardSnippetText(t *testing.T) {
	if got := boardSnippetText("plain snippet", false); got != "plain snippet" {
		t.Errorf("boardSnippetText(plain) = %q", got)
	}
	if got := boardSnippetText("assistant summary", true); got != board.AssistantMark+" assistant summary" {
		t.Errorf("boardSnippetText(assistant) = %q", got)
	}
}

func TestBoardStateDotClass(t *testing.T) {
	cases := []struct {
		state board.State
		want  string
	}{
		{board.StateHot, "board-state-dot-hot"},
		{board.StateYou, "board-state-dot-you"},
		{board.StateThem, "board-state-dot-them"},
		{board.StateInfo, "board-state-dot-info"},
	}
	for _, c := range cases {
		if got := boardStateDotClass(c.state); got != c.want {
			t.Errorf("boardStateDotClass(%v) = %q, want %q", c.state, got, c.want)
		}
	}
}

func TestBoardCountText(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{{0, ""}, {1, "1"}, {2, "2"}, {42, "42"}}
	for _, c := range cases {
		if got := boardCountText(c.n); got != c.want {
			t.Errorf("boardCountText(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestBoardRowData(t *testing.T) {
	r := board.Row{
		State: board.StateHot, Person: "Alice", Title: "Hello", TitleIsAssistant: true,
		Snippet: "Summary", SnippetIsAssistant: false, Time: "Today", Due: "Tomorrow",
		IssueKey: "KEY-1", IssueStatus: "Open", IssueStyle: jira.StatusPlain, Attachments: true,
		CountText: "3", Unread: true,
	}
	got := boardRowData(r)
	if got.StateClass != "board-state-dot-hot" {
		t.Errorf("StateClass = %q", got.StateClass)
	}
	if got.Title != board.AssistantMark+" Hello" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Snippet != "Summary" {
		t.Errorf("Snippet = %q", got.Snippet)
	}
	if got.Person != "Alice" || got.Date != "Today" || got.Due != "Tomorrow" {
		t.Errorf("Person/Date/Due wrong: %+v", got)
	}
	if got.IssueKey != "KEY-1" || got.IssueStatus != "Open" {
		t.Errorf("issue fields wrong: %+v", got)
	}
	if !got.Attachments || !got.Unread || got.CountText != "3" {
		t.Errorf("flags/count wrong: %+v", got)
	}
}

func TestBoardPageCaseIDAt(t *testing.T) {
	p := &boardPage{caseOrder: []board.CaseID{"c_1", "", "c_2", ""}}
	cases := []struct {
		index  int
		wantID board.CaseID
		wantOK bool
	}{
		{0, "c_1", true},
		{1, "", false}, // a section header's slot
		{2, "c_2", true},
		{3, "", false},
		{-1, "", false}, // out of range
		{4, "", false},  // out of range
	}
	for _, c := range cases {
		id, ok := p.caseIDAt(c.index)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("caseIDAt(%d) = (%q, %v), want (%q, %v)", c.index, id, ok, c.wantID, c.wantOK)
		}
	}
}
