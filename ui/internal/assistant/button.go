// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// ButtonOpensPanel says whether the Assistant button of a window opens the
// panel instead of its menu: with In App chosen (t read as ParseTarget
// reads it), in the window that has the panel (the main window). The
// button then unfolds the panel, the main window brought forward and the
// question field focused, whether the panel can run or not (the panel
// itself says what is missing and offers Sign In… or Get Claude Code…);
// an open panel stays open, the panel's own toggle folds it. A message
// window, which has no panel, keeps the menu: its actions name its
// message and open the main window's panel with it. With the Claude apps
// the button is the menu. The menus elsewhere (the menu bar's, an
// attachment's Ask the Assistant…) do not change.
func ButtonOpensPanel(t Target, hasPanel bool) bool {
	return hasPanel && ParseTarget(string(t)) == App
}

// PanelActions are the panel's quick actions, in order: the message
// actions without Ask About This Message… (the panel's own field asks),
// then Summarize Unread in This Folder, on the folder selected in the
// sidebar, as the main window's Assistant menu has it (the panel's button
// then stands in for that menu). Their labels are Label's.
var PanelActions = []Action{Summarize, DraftReply, Tasks, Unread}
