// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/board"
)

func TestTodayItems(t *testing.T) {
	t.Run("empty sections show their column's empty text", func(t *testing.T) {
		vm := board.ViewModel{
			Columns:           []board.Column{{State: board.StateHot, EmptyText: "Nothing burning."}, {State: board.StateYou, EmptyText: "Empty."}},
			SectionsEmptyText: "Nothing here.",
		}
		got := todayItems(vm)
		want := []todayItemKind{todayItemSection, todayItemEmptyText, todayItemSection, todayItemEmptyText}
		assertKinds(t, got, want)
		if got[1].emptyText != "Nothing burning." {
			t.Errorf("Hot's empty text = %q", got[1].emptyText)
		}
		if got[3].emptyText != "Empty." {
			t.Errorf("You's empty text = %q", got[3].emptyText)
		}
	})

	t.Run("a column missing from Columns falls back to SectionsEmptyText", func(t *testing.T) {
		vm := board.ViewModel{SectionsEmptyText: "Nothing here."}
		got := todayItems(vm)
		if got[1].emptyText != "Nothing here." || got[3].emptyText != "Nothing here." {
			t.Errorf("fallback empty text: %+v", got)
		}
	})

	t.Run("rows, and N more, then the commitments under a heading", func(t *testing.T) {
		vm := board.ViewModel{
			Today: board.TodayPage{
				Hot:         []board.Row{{ID: "c_1"}},
				You:         []board.Row{{ID: "c_2"}, {ID: "c_3"}},
				YouMore:     4,
				Commitments: []board.CommitmentRow{{ID: "k_1", CaseID: "c_2"}},
			},
		}
		got := todayItems(vm)
		want := []todayItemKind{
			todayItemSection, todayItemCase, // Hot
			todayItemSection, todayItemCase, todayItemCase, todayItemMore, // Waiting for You
			todayItemCommitmentsHeading, todayItemCommitment,
		}
		assertKinds(t, got, want)
		if got[1].row.ID != "c_1" {
			t.Errorf("Hot row = %+v", got[1])
		}
		if got[5].more != 4 {
			t.Errorf("and N more = %+v, want more=4", got[5])
		}
		if got[0].count != 1 {
			t.Errorf("Hot section count = %d, want 1", got[0].count)
		}
		if got[2].count != 6 { // len(You) + YouMore
			t.Errorf("You section count = %d, want 6", got[2].count)
		}
	})

	t.Run("no and N more below zero", func(t *testing.T) {
		vm := board.ViewModel{Today: board.TodayPage{Hot: []board.Row{{ID: "c_1"}}, You: []board.Row{{ID: "c_2"}}}}
		got := todayItems(vm)
		for _, it := range got {
			if it.kind == todayItemMore {
				t.Fatalf("an 'and N more' row with YouMore == 0: %+v", got)
			}
		}
	})

	t.Run("no commitments heading without commitments", func(t *testing.T) {
		vm := board.ViewModel{Today: board.TodayPage{Hot: []board.Row{{ID: "c_1"}}}}
		got := todayItems(vm)
		for _, it := range got {
			if it.kind == todayItemCommitmentsHeading || it.kind == todayItemCommitment {
				t.Fatalf("a commitments row without any commitments: %+v", got)
			}
		}
	})
}

func assertKinds(t *testing.T, got []todayItem, want []todayItemKind) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d items, want %d: %+v", len(got), len(want), got)
	}
	for i, k := range want {
		if got[i].kind != k {
			t.Errorf("item %d kind = %v, want %v", i, got[i].kind, k)
		}
	}
}

func TestTodayItemCaseID(t *testing.T) {
	cases := []struct {
		name   string
		item   todayItem
		wantID board.CaseID
		wantOK bool
	}{
		{"a case", todayItem{kind: todayItemCase, row: board.Row{ID: "c_1"}}, "c_1", true},
		{"a commitment", todayItem{kind: todayItemCommitment, commitment: board.CommitmentRow{CaseID: "c_2"}}, "c_2", true},
		{"a section header selects nothing", todayItem{kind: todayItemSection}, "", false},
		{"and N more selects nothing (it switches the style)", todayItem{kind: todayItemMore, more: 3}, "", false},
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

func TestBoardColumnEmptyText(t *testing.T) {
	vm := board.ViewModel{
		Columns:           []board.Column{{State: board.StateHot, EmptyText: "Nothing burning."}},
		SectionsEmptyText: "Nothing here.",
	}
	if got := boardColumnEmptyText(vm, board.StateHot); got != "Nothing burning." {
		t.Errorf("boardColumnEmptyText(found) = %q", got)
	}
	if got := boardColumnEmptyText(vm, board.StateThem); got != "Nothing here." {
		t.Errorf("boardColumnEmptyText(fallback) = %q", got)
	}
}
