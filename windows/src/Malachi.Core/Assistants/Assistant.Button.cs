// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantButton.swift
// (buttonOpensPanel, panelActions); GTK: ui/internal/assistant/button.go
// (ButtonOpensPanel, PanelActions). What the Assistant button of a window
// does, and the panel's quick actions.

using System.Collections.Generic;
using Malachi.Core.Settings;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// Whether the Assistant button of a window opens the panel instead of
    /// its menu (assistant.ButtonOpensPanel): with In App chosen (an unknown
    /// value read as Desktop), in the window that has the panel (the main
    /// window). The button then unfolds the panel, the main window brought
    /// forward and the question field focused, whether the panel can run or
    /// not (the panel itself says what is missing and offers Sign In… or Get
    /// Claude Code…); an open panel stays open, the panel's own toggle folds
    /// it. A message window, which has no panel, keeps the menu: its actions
    /// name its message and open the main window's panel with it. With the
    /// Claude apps the button is the menu. The menus elsewhere (an
    /// attachment's Ask the Assistant…) do not change.
    /// </summary>
    public static bool ButtonOpensPanel(AssistantTarget t, bool hasPanel) => hasPanel && t == AssistantTarget.App;

    /// <summary>
    /// The panel's quick actions, in order (assistant.PanelActions): the
    /// message actions without Ask About This Message… (the panel's own
    /// field asks), then Summarize Unread in This Folder, on the folder
    /// selected in the sidebar, as the main window's Assistant menu has it
    /// (the panel's button then stands in for that menu). Their labels are
    /// <see cref="Label"/>'s.
    /// </summary>
    public static IReadOnlyList<AssistantAction> PanelActions { get; } =
        [AssistantAction.Summarize, AssistantAction.DraftReply, AssistantAction.Tasks, AssistantAction.Unread];
}
