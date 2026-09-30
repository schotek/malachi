// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Capabilities.swift
// (Capabilities.Actions); GTK: ui/internal/capabilities/capabilities.go
// (Actions).

namespace Malachi.Core.Model;

/// <summary>capabilities.Actions: the message actions, each true when offered.</summary>
public readonly record struct CapabilityActions
{
    /// <summary>Reply (or Comment, see <see cref="Comment"/>).</summary>
    public bool Reply { get; init; }

    /// <summary>Reply All.</summary>
    public bool ReplyAll { get; init; }

    /// <summary>Forward.</summary>
    public bool Forward { get; init; }

    /// <summary>Move to a folder.</summary>
    public bool Move { get; init; }

    /// <summary>Move to Trash (or cancel a queued message).</summary>
    public bool Trash { get; init; }

    /// <summary>Archive.</summary>
    public bool Archive { get; init; }

    /// <summary>Mark as Junk.</summary>
    public bool Junk { get; init; }

    /// <summary>Reply writes a comment on the issue: the client labels it "Comment" (Jira.ReplyLabel).</summary>
    public bool Comment { get; init; }
}
