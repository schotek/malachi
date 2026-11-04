// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the part of macos/Sources/MalachiMail/Board/BoardReplyEditorHost.swift
// BoardActions asks (editsInline, focusReply); GTK: focusBoardReply
// (window/board_reply_editor.go). The extension point of stage 5's inline
// reply editor: Reply on a case whose suggested reply it edits gives it the
// keyboard instead of opening a compose window.

using Malachi.Core.Api;

namespace Malachi.App.Boards;

/// <summary>The board's inline reply editor, as the case actions see it.</summary>
public interface IBoardInlineReply
{
    /// <summary>Whether the suggested reply of case <paramref name="id"/> is edited inline.</summary>
    bool EditsInline(BoardCaseId id);

    /// <summary>Gives the inline editor of case <paramref name="id"/> the keyboard (the case selected for it).</summary>
    void FocusReply(BoardCaseId id);
}
