// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "github.com/schotek/malachi/backend/pkg/api"

// CanUnstar reports whether a case offers Unstar, for the places that have
// a case rather than its detail (the context menu of a row or a card): the
// case is on the board because of a star (rule reason hot.flagged,
// whatever state the user gave it) and is not done. Detail.CanUnstar says
// the same for the selected case. (macOS: Board.canUnstar,
// BoardUnstar.swift.)
func CanUnstar(c Case) bool {
	return c.RuleReason == api.BoardReasonHotFlagged && !c.Visibility.IsDone()
}
