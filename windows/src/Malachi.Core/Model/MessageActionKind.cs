// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ActionRules.swift
// (MessageActionKind); GTK: ui/internal/window/action_rules.go (the
// actions of actionState.supported).

using System;

namespace Malachi.Core.Model;

/// <summary>
/// A message action that an account may not offer at all
/// (Capabilities.Supported): the client hides it where it can and disables it
/// otherwise. Seen and flagged work on every account and are not among them.
/// Flags, where Swift has a Set, so that <see cref="ActionFlags"/> keeps its
/// value equality.
/// </summary>
[Flags]
public enum MessageActionKind
{
    /// <summary>No action.</summary>
    None = 0,

    /// <summary>Reply (or Comment).</summary>
    Reply = 1,

    /// <summary>Reply All.</summary>
    ReplyAll = 2,

    /// <summary>Forward.</summary>
    Forward = 4,

    /// <summary>Move to Trash.</summary>
    Trash = 8,

    /// <summary>Move to a folder (no control on Windows yet).</summary>
    Move = 16,

    /// <summary>Archive.</summary>
    Archive = 32,

    /// <summary>Mark as Junk.</summary>
    Junk = 64,
}
