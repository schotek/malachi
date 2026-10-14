// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// TestCanUnstarAgreesWithTheDetail: CanUnstar (the context menu's Unstar)
// agrees with the detail's CanUnstar (macOS BoardUnstarRuleTests.swift).
func TestCanUnstarAgreesWithTheDetail(t *testing.T) {
	for _, reason := range []api.BoardReason{api.BoardReasonHotFlagged, api.BoardReasonHotImportant, api.BoardReasonYouAddressed} {
		for _, done := range []bool{false, true} {
			for _, user := range []*State{nil, optState(StateInfo)} {
				c := mk("c1", StateHot, withReason(reason))
				c.UserState = user
				if done {
					c.Visibility = Visibility{Kind: VisibleDone}
				}
				d := view(casesOf(c), configured(func(v *ViewState) {
					if done {
						v.Filter = doneFilter
					}
				})).Detail
				if d == nil {
					t.Fatalf("%s done %v: no detail", reason, done)
				}
				check(t, CanUnstar(c) == d.CanUnstar, "%s done %v user %v: %v, detail %v", reason, done, user, CanUnstar(c), d.CanUnstar)
				check(t, CanUnstar(c) == (reason == api.BoardReasonHotFlagged && !done), "%s done %v", reason, done)
			}
		}
	}
}
