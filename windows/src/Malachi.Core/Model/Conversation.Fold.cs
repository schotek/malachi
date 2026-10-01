// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationFold.swift
// (Conversation.foldable, defaultFolds, foldAllOffer); GTK:
// ui/internal/conversation/fold.go (Foldable, DefaultFolds, FoldAllOffer).
//
// Every message card of the conversation folds to its header and a preview
// of its text (the summary's snippet) and opens again: its arrow, a click on
// the preview, or the one button above the conversation that folds or opens
// them all. A folded card holds no web view and asks for no body. Events and
// the row of older messages are no cards and do not fold.
//
// Each card starts as DefaultFolds says; what the user chose for a card
// (ConversationFolds) holds until another conversation is shown, through
// every update of the model: a card that arrives meanwhile starts as its
// default. Go's "" ids are null here (opening).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>conversation.Foldable: an item that folds: a message card.</summary>
    public static bool Foldable(ConversationItem it)
    {
        ArgumentNullException.ThrowIfNull(it);
        return it.Kind == ConversationItemKind.Message && it.Message is { } m && m.Id.Value.Length > 0;
    }

    /// <summary>
    /// conversation.DefaultFolds: how each foldable card of
    /// <paramref name="items"/> (any order) starts, by message id, true for
    /// folded. <paramref name="opening"/> is the card that opened the
    /// conversation (the pane's rule: the description of an issue, else the
    /// oldest member when no older one is left out; null for none). A sent
    /// card starts folded: the user wrote it and knows it. The opening card
    /// starts folded while another card that is not a sent card follows it
    /// (a conversation of one message and its status changes, or of one
    /// message and the user's replies, shows that message whole). Every other
    /// card starts open. Should that leave every card folded, the newest
    /// opens, so that a conversation never opens on headers alone.
    /// </summary>
    public static Dictionary<MessageId, bool> DefaultFolds(IReadOnlyList<ConversationItem> items, MessageId? opening)
    {
        ArgumentNullException.ThrowIfNull(items);
        // The cards, a repeated id once (its first card).
        var cards = new List<ConversationItem>(items.Count);
        var seen = new HashSet<MessageId>();
        var others = 0;
        foreach (var it in items)
        {
            if (!Foldable(it) || !seen.Add(it.Message!.Id))
            {
                continue;
            }
            cards.Add(it);
            if (!it.Sent && it.Message.Id != opening)
            {
                others++;
            }
        }
        var output = new Dictionary<MessageId, bool>(cards.Count);
        var open = false;
        var newest = -1;
        for (var i = 0; i < cards.Count; i++)
        {
            var s = cards[i].Message!;
            var folded = cards[i].Sent || (s.Id == opening && others > 0);
            output[s.Id] = folded;
            open = open || !folded;
            if (newest < 0 || Before(cards[newest].Message!, s))
            {
                newest = i;
            }
        }
        if (!open && newest >= 0)
        {
            output[cards[newest].Message!.Id] = false;
        }
        return output;
    }

    /// <summary>
    /// conversation.FoldAllOffer: the button for the fold state of the cards
    /// (<see cref="ConversationFolds.State"/>): none with fewer than two
    /// cards, Collapse All while at least one card is open, else Expand All.
    /// </summary>
    public static ConversationFoldAll FoldAllOffer(IReadOnlyDictionary<MessageId, bool> state)
    {
        ArgumentNullException.ThrowIfNull(state);
        if (state.Count < 2)
        {
            return ConversationFoldAll.None;
        }
        return state.Values.Any(folded => !folded) ? ConversationFoldAll.Collapse : ConversationFoldAll.Expand;
    }
}
