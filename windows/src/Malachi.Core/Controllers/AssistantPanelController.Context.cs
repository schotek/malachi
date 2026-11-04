// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Context: init, conversation, overlaps); GTK:
// ui/internal/assistantpanel/controller.go (Context, NewContext,
// Conversation, Overlaps, equal). A record, compared by value as the Swift
// struct is (the selection by its ids); the controller replaces it with a
// `with` expression where Swift mutates it. The constructor keeps the
// count at least the number of ids, as Swift's init and GTK's NewContext
// do; an `init` of Count does not.

using System;
using System.Linq;
using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>
    /// What the panel works on: the selected message, or a conversation of
    /// <see cref="Count"/> messages (<see cref="Selection"/> newest first;
    /// with <see cref="Partial"/> only its newest message is known yet).
    /// </summary>
    /// <param name="Selection">The account and the messages, newest first.</param>
    /// <param name="Count">How many messages; at least the number of ids.</param>
    /// <param name="Partial">Only the newest message of a folded conversation is known yet.</param>
    /// <param name="Subject">
    /// The message's subject, or the conversation's: what the chip names once
    /// a conversation is about it. Mail text, shown as one line
    /// (<see cref="Assistant.ConversationLabel"/>), never sent to the model.
    /// </param>
    /// <param name="ThreadId">The conversation (thread) it belongs to; "" when not known.</param>
    public sealed record Context(AssistantSelection Selection, int Count = 1, bool Partial = false, string Subject = "", string ThreadId = "")
    {
        /// <summary>The account and the messages, newest first.</summary>
        public AssistantSelection Selection { get; init; } = Selection ?? throw new ArgumentNullException(nameof(Selection));

        /// <summary>How many messages: at least the number of ids when constructed.</summary>
        public int Count { get; init; } = Math.Max(Count, Selection?.MessageIds.Count ?? 0);

        /// <summary>The message's or the conversation's subject (mail text).</summary>
        public string Subject { get; init; } = Subject ?? throw new ArgumentNullException(nameof(Subject));

        /// <summary>The conversation (thread) it belongs to; "" when not known.</summary>
        public string ThreadId { get; init; } = ThreadId ?? throw new ArgumentNullException(nameof(ThreadId));

        /// <summary>Whether it is a conversation (more than one message).</summary>
        public bool Conversation => Count > 1;

        /// <summary>
        /// Whether <paramref name="other"/> is part of this context, or this
        /// of it: the same account and a message in common, or, when either
        /// is a conversation, the same thread (a folded conversation knows
        /// only its newest message until its members are resolved).
        /// </summary>
        public bool Overlaps(Context other)
        {
            ArgumentNullException.ThrowIfNull(other);
            if (Selection.AccountId.Length == 0 || !string.Equals(Selection.AccountId, other.Selection.AccountId, StringComparison.Ordinal))
            {
                return false;
            }
            if (Selection.MessageIds.Any(id => id.Length > 0 && other.Selection.MessageIds.Contains(id, StringComparer.Ordinal)))
            {
                return true;
            }
            return (Conversation || other.Conversation) && ThreadId.Length > 0
                && string.Equals(ThreadId, other.ThreadId, StringComparison.Ordinal);
        }
    }
}
