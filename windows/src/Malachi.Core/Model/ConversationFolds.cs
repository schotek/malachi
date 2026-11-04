// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationFold.swift
// (Conversation.Folds); GTK: ui/internal/conversation/fold.go (Folds).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// conversation.Folds: the fold state of the cards of the conversation on
/// show: the user's choices, by message id, over
/// <see cref="Conversation.DefaultFolds"/>. A new instance is ready.
/// </summary>
public sealed class ConversationFolds
{
    private readonly Dictionary<MessageId, bool> chosen = [];
    private ThreadId? thread;

    /// <summary>
    /// Called whenever the pane shows conversation <paramref name="thread"/>,
    /// built anew or updated: the choices made for another conversation are
    /// forgotten, those for this one kept.
    /// </summary>
    public void Show(ThreadId thread)
    {
        if (thread != this.thread)
        {
            this.thread = thread;
            chosen.Clear();
        }
    }

    /// <summary>Records the user's fold (true) or unfold of card <paramref name="id"/>.</summary>
    public void Set(MessageId id, bool folded)
    {
        if (id.Value.Length == 0)
        {
            return;
        }
        chosen[id] = folded;
    }

    /// <summary>
    /// Records <paramref name="folded"/> for every foldable card of
    /// <paramref name="items"/>: the button above the conversation
    /// (<see cref="Conversation.FoldAllOffer"/>). A card that arrives later
    /// starts as its default.
    /// </summary>
    public void SetAll(IReadOnlyList<ConversationItem> items, bool folded)
    {
        ArgumentNullException.ThrowIfNull(items);
        foreach (var it in items)
        {
            if (Conversation.Foldable(it))
            {
                Set(it.Message!.Id, folded);
            }
        }
    }

    /// <summary>
    /// Whether each foldable card of <paramref name="items"/> is folded, by
    /// message id: the user's choice, else the default
    /// (<see cref="Conversation.DefaultFolds"/> with <paramref name="opening"/>).
    /// </summary>
    public Dictionary<MessageId, bool> State(IReadOnlyList<ConversationItem> items, MessageId? opening)
    {
        var output = Conversation.DefaultFolds(items, opening);
        foreach (var (id, folded) in chosen)
        {
            if (output.ContainsKey(id))
            {
                output[id] = folded;
            }
        }
        return output;
    }
}
