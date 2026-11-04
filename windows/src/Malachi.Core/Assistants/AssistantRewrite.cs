// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantRewrite.swift
// (Assistant.Rewrite); GTK: ui/internal/assistant/rewrite.go (Rewrite).
// Swift and Go keep the rewrite as a string so that an unknown one exists;
// a C# enum is open the same way (any other value is unknown:
// RewriteLabel is "" for it, RewriteMessage refuses it), and
// Assistant.RewriteNick gives Go's string.

namespace Malachi.Core.Assistants;

/// <summary>
/// What the compose window's assistant does with a passage: one of the
/// presets (<see cref="Assistant.Rewrites"/>), or <see cref="Custom"/>, the
/// user's own instruction.
/// </summary>
public enum AssistantRewrite
{
    /// <summary><c>politer</c>: more polite and friendly.</summary>
    Politer,

    /// <summary><c>shorter</c>: shorter and clearer.</summary>
    Shorter,

    /// <summary><c>fix</c>: spelling, grammar and punctuation only.</summary>
    Fix,

    /// <summary><c>english</c>: translated into English.</summary>
    ToEnglish,

    /// <summary><c>custom</c>: the user's own instruction (the popover's free field).</summary>
    Custom,
}
