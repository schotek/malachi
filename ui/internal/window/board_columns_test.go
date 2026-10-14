// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/board"
)

func TestColumnItems(t *testing.T) {
	commitments := []board.CommitmentRow{{ID: "k_1", CaseID: "c_1", Text: "Send the deck"}}

	t.Run("empty column is its placeholder, never commitments without rows", func(t *testing.T) {
		col := board.Column{State: board.StateYou, EmptyText: "Empty."}
		got := columnItems(col, commitments, false)
		if len(got) != 1 || got[0].kind != columnItemPlaceholder || got[0].placeholder != "Empty." {
			t.Fatalf("columnItems(empty, not first) = %+v", got)
		}
	})

	t.Run("rows in order, no commitments when not first", func(t *testing.T) {
		rows := []board.Row{{ID: "c_1"}, {ID: "c_2"}}
		col := board.Column{State: board.StateYou, Rows: rows}
		got := columnItems(col, commitments, false)
		if len(got) != 2 || got[0].kind != columnItemCase || got[0].row.ID != "c_1" || got[1].row.ID != "c_2" {
			t.Fatalf("columnItems(rows, not first) = %+v", got)
		}
	})

	t.Run("the first column appends the commitments under a heading", func(t *testing.T) {
		rows := []board.Row{{ID: "c_1"}}
		col := board.Column{State: board.StateHot, Rows: rows}
		got := columnItems(col, commitments, true)
		want := []columnItemKind{columnItemCase, columnItemCommitmentsHeading, columnItemCommitment}
		if len(got) != len(want) {
			t.Fatalf("columnItems(first, with commitments) = %+v, want %d items", got, len(want))
		}
		for i, k := range want {
			if got[i].kind != k {
				t.Errorf("item %d kind = %v, want %v", i, got[i].kind, k)
			}
		}
		if got[2].commitment.ID != "k_1" {
			t.Errorf("commitment item = %+v", got[2])
		}
	})

	t.Run("the first column without commitments has none to append", func(t *testing.T) {
		col := board.Column{State: board.StateHot, Rows: []board.Row{{ID: "c_1"}}}
		got := columnItems(col, nil, true)
		if len(got) != 1 {
			t.Fatalf("columnItems(first, no commitments) = %+v", got)
		}
	})
}

func TestColumnItemCaseID(t *testing.T) {
	cases := []struct {
		name   string
		item   columnItem
		wantID board.CaseID
		wantOK bool
	}{
		{"a case", columnItem{kind: columnItemCase, row: board.Row{ID: "c_1"}}, "c_1", true},
		{"a commitment", columnItem{kind: columnItemCommitment, commitment: board.CommitmentRow{CaseID: "c_2"}}, "c_2", true},
		{"a placeholder selects nothing", columnItem{kind: columnItemPlaceholder}, "", false},
		{"a heading selects nothing", columnItem{kind: columnItemCommitmentsHeading}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, ok := c.item.caseID()
			if id != c.wantID || ok != c.wantOK {
				t.Errorf("caseID() = (%q, %v), want (%q, %v)", id, ok, c.wantID, c.wantOK)
			}
		})
	}
}

func TestIndexOfCase(t *testing.T) {
	items := []columnItem{
		{kind: columnItemPlaceholder},
		{kind: columnItemCase, row: board.Row{ID: "c_1"}},
		{kind: columnItemCommitmentsHeading},
		{kind: columnItemCommitment, commitment: board.CommitmentRow{CaseID: "c_2"}},
		{kind: columnItemCommitment, commitment: board.CommitmentRow{CaseID: "c_1"}},
	}
	t.Run("a card wins over a commitment of the same case", func(t *testing.T) {
		i, ok := indexOfCase(items, "c_1")
		if !ok || i != 1 {
			t.Errorf("indexOfCase(c_1) = (%d, %v), want (1, true)", i, ok)
		}
	})
	t.Run("a commitment when there is no card", func(t *testing.T) {
		i, ok := indexOfCase(items, "c_2")
		if !ok || i != 3 {
			t.Errorf("indexOfCase(c_2) = (%d, %v), want (3, true)", i, ok)
		}
	})
	t.Run("not shown at all", func(t *testing.T) {
		if _, ok := indexOfCase(items, "c_3"); ok {
			t.Error("indexOfCase(c_3) found a row that is not there")
		}
	})
}
