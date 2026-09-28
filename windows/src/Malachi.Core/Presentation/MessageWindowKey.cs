// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Windows/MessageWindows.swift (Key);
// GTK: the keys of window.go openMessages (a message id) and openEmbedded
// (embedded.go embeddedKey: the message and the part).

using Malachi.Core.Api;

namespace Malachi.Core.Presentation;

/// <summary>Which message a window shows: one window per key.</summary>
public abstract record MessageWindowKey
{
    private MessageWindowKey()
    {
    }

    /// <summary>The message the window shows, or the one its attached message was opened from.</summary>
    public abstract MessageId Id { get; }

    /// <summary>A message in its own window.</summary>
    /// <param name="Id">The message.</param>
    public sealed record Message(MessageId Id) : MessageWindowKey
    {
        /// <inheritdoc/>
        public override MessageId Id { get; } = Id;
    }

    /// <summary>A message attached to another, in its own window.</summary>
    /// <param name="Id">The message it is attached to.</param>
    /// <param name="Part">The part.</param>
    public sealed record Embedded(MessageId Id, string Part) : MessageWindowKey
    {
        /// <inheritdoc/>
        public override MessageId Id { get; } = Id;
    }
}
