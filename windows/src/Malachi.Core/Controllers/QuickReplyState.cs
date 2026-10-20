// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/QuickReplyController.swift
// (QuickReplyState); GTK: ui/internal/quickreply/controller.go (State). Go's
// zero Item is null. Swift's == compares the modes by value; so does Equals
// here (a record would compare the list by reference).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>quickreply.State: what the quick-reply bar shows, for the conversation shown (<see cref="Thread"/>).</summary>
public sealed record QuickReplyState
{
    /// <summary>The conversation shown; "" for none.</summary>
    public ThreadId Thread { get; init; } = new("");

    /// <summary>
    /// The chat layout is shown, the conversation has a target and its
    /// account answers it (Reply). The bar is hidden otherwise.
    /// </summary>
    public bool Visible { get; init; }

    /// <summary>
    /// The message a quick reply answers
    /// (<see cref="Conversation.QuickReplyTarget"/>); null without one. Its
    /// <see cref="ConversationItem.Sender"/> names it in the mode button's
    /// tooltip.
    /// </summary>
    public ConversationItem? Target { get; init; }

    /// <summary>
    /// The mode menu's entries (<see cref="Conversation.QuickReplyModes"/>);
    /// <see cref="QuickReplyMode.Forward"/> among them opens the window
    /// (<see cref="QuickReplyController.Forward"/>).
    /// </summary>
    public IReadOnlyList<QuickReplyMode> Modes { get; init => field = value ?? []; } = [];

    /// <summary>
    /// The chosen mode: <see cref="QuickReplyMode.Reply"/> or
    /// <see cref="QuickReplyMode.ReplyAll"/>, never
    /// <see cref="QuickReplyMode.Forward"/>; on an issue always Reply.
    /// </summary>
    public QuickReplyMode Mode { get; init; }

    /// <summary>What was typed in this conversation's bar.</summary>
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary><see cref="Text"/> has nothing to send (<see cref="Conversation.QuickReplyBlank"/>); Send is disabled.</summary>
    public bool Blank { get; init; } = true;

    /// <summary>A Send of this conversation is on its way; the bar is disabled until it ends.</summary>
    public bool Sending { get; init; }

    /// <summary>The reply is a comment on an issue (the placeholder and the Reply label say so).</summary>
    public bool Comment { get; init; }

    /// <inheritdoc/>
    public bool Equals(QuickReplyState? other) =>
        other is not null && Thread == other.Thread && Visible == other.Visible && Target == other.Target
        && Modes.SequenceEqual(other.Modes) && Mode == other.Mode
        && string.Equals(Text, other.Text, StringComparison.Ordinal)
        && Blank == other.Blank && Sending == other.Sending && Comment == other.Comment;

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Thread, Visible, Modes.Count, Mode, Text, Blank, Sending, Comment);

    /// <summary>
    /// The state without the text: never what was typed (docs/windows-port.md
    /// §3.1: no mail content in logs), nor the target's sender.
    /// </summary>
    public override string ToString() => string.Create(
        CultureInfo.InvariantCulture,
        $"QuickReplyState(thread: {Thread}, visible: {Visible}, target: {Target?.Id?.ToString() ?? "null"}, "
        + $"modes: [{string.Join(", ", Modes)}], mode: {Mode}, text: {Text.Length} chars, blank: {Blank}, "
        + $"sending: {Sending}, comment: {Comment})");
}
