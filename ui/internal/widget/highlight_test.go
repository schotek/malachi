// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestValidRanges(t *testing.T) {
	text := "posílám přílohy k faktuře" // "í" and "ř" are two bytes each
	r := func(s, e int) api.MatchRange { return api.MatchRange{Start: s, End: e} }
	tests := []struct {
		name string
		in   []api.MatchRange
		want []api.MatchRange
	}{
		{"good", []api.MatchRange{r(10, 19)}, []api.MatchRange{r(10, 19)}},
		{"sorted", []api.MatchRange{r(22, 30), r(10, 19)}, []api.MatchRange{r(10, 19), r(22, 30)}},
		{"end of text", []api.MatchRange{r(22, len(text))}, []api.MatchRange{r(22, len(text))}},
		{"overlap dropped", []api.MatchRange{r(10, 19), r(11, 20)}, []api.MatchRange{r(10, 19)}},
		{"mid-character start", []api.MatchRange{r(4, 8)}, []api.MatchRange{}},
		{"mid-character end", []api.MatchRange{r(0, 4)}, []api.MatchRange{}},
		{"outside", []api.MatchRange{r(-1, 3), r(10, 99), r(5, 5), r(8, 6)}, []api.MatchRange{}},
	}
	for _, tc := range tests {
		if got := validRanges(text, tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	many := make([]api.MatchRange, 0, 50)
	for i := 0; i < 50; i++ {
		many = append(many, r(i, i+1))
	}
	if got := validRanges(string(make([]byte, 60)), many); len(got) != maxHighlights {
		t.Errorf("cap: %d ranges", len(got))
	}
}
