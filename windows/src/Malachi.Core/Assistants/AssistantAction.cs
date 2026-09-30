// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/Assistant.swift
// (Assistant.Action); GTK: ui/internal/assistant (Action). Swift and Go keep
// the action as a string so that an unknown one exists; a C# enum is open
// the same way (any other value is unknown: Label is "" for it, Prompt
// refuses it), and Assistant.ActionNick gives Go's string.

namespace Malachi.Core.Assistants;

/// <summary>One thing the Assistant menu asks Claude to do; <see cref="Unread"/> works on a folder, the others on messages.</summary>
public enum AssistantAction
{
    /// <summary><c>summarize</c>.</summary>
    Summarize,

    /// <summary><c>draft-reply</c>.</summary>
    DraftReply,

    /// <summary><c>tasks</c>.</summary>
    Tasks,

    /// <summary><c>ask</c>.</summary>
    Ask,

    /// <summary><c>unread</c>: Summarize Unread in This Folder.</summary>
    Unread,
}
