// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import "github.com/schotek/malachi/backend/pkg/api"

// WindowDays returns how many days a case in state s stays on the board
// under the preferences' windows; a value outside 1..api.MaxBoardWindowDays
// falls back to the default of its state. An unknown state gets the
// longest default.
func WindowDays(s api.BoardState, w api.BoardWindows) int {
	pick := func(v, def int) int {
		if v < 1 || v > api.MaxBoardWindowDays {
			return def
		}
		return v
	}
	switch s {
	case api.BoardHot:
		return pick(w.Hot, api.DefaultBoardHotDays)
	case api.BoardThem:
		return pick(w.Them, api.DefaultBoardThemDays)
	case api.BoardInfo:
		return pick(w.Info, api.DefaultBoardInfoDays)
	case api.BoardYou:
		return pick(w.You, api.DefaultBoardYouDays)
	}
	return max(api.DefaultBoardHotDays, api.DefaultBoardYouDays, api.DefaultBoardThemDays, api.DefaultBoardInfoDays)
}
