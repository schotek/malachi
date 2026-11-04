// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationQuoted.swift
// (QuotedReveal); Go: ui/internal/conversation/quoted.go (QuotedReveal).
// Swift-first.

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The messages whose quoted history the user revealed in one view, for what
/// that view shows now (a conversation, or a message alone): another
/// selection forgets them, an update of the same one keeps them. A body asked
/// for again (the cache let it go, the daemon rebuilt the message) comes back
/// as the user left it. A new instance is ready.
/// </summary>
public sealed class QuotedReveal
{
    private readonly HashSet<MessageId> revealed = [];
    private string? shown;

    /// <summary>
    /// Called whenever the view shows <paramref name="selection"/> (a thread
    /// or message id), anew or updated: the messages revealed for another
    /// selection are forgotten, those for this one kept.
    /// </summary>
    public void Show(string selection)
    {
        if (selection != shown)
        {
            shown = selection;
            revealed.Clear();
        }
    }

    /// <summary>Forgets the selection and what was revealed for it.</summary>
    public void Clear()
    {
        shown = null;
        revealed.Clear();
    }

    /// <summary>Whether the quoted history of message <paramref name="id"/> shows.</summary>
    public bool IsRevealed(MessageId id) => revealed.Contains(id);

    /// <summary>
    /// Records the user's Show Quoted Text (true) or Hide Quoted Text of
    /// message <paramref name="id"/>.
    /// </summary>
    public void Set(MessageId id, bool on)
    {
        if (id.Value.Length == 0)
        {
            return;
        }
        if (on)
        {
            revealed.Add(id);
        }
        else
        {
            revealed.Remove(id);
        }
    }
}
