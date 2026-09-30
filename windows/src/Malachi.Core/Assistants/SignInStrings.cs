// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantSignIn.swift
// (Assistant.SignInStrings); GTK: ui/internal/assistant/assistant.go
// (SignInStrings).

namespace Malachi.Core.Assistants;

/// <summary>
/// The fixed texts of Claude Code's sign-in from the application
/// (<see cref="Assistant.SignInTexts"/>, target In App): the application runs
/// Claude Code's own <c>claude auth login</c>, which opens the browser, and
/// waits for it. It never sees a credential. A failure is
/// <see cref="Assistant.SignInFailedText"/>.
/// </summary>
public sealed record SignInStrings
{
    /// <summary>The button beside the panel's "Claude Code is not signed in" and on the settings' Claude Code row.</summary>
    public required string SignIn { get; init; }

    /// <summary>What both show while the browser is open (the panel as an activity line).</summary>
    public required string Waiting { get; init; }

    /// <summary>The error line when the browser brought no answer in time.</summary>
    public required string TimedOut { get; init; }

    /// <summary>
    /// The line of a request that has no button of its own (the compose
    /// window's rewrite, the search in the user's own words): where to sign
    /// in.
    /// </summary>
    public required string Hint { get; init; }

    /// <summary>
    /// The button beside "Claude Code was not found on this computer": it
    /// opens <see cref="Assistant.InstallUrl"/> in the browser.
    /// </summary>
    public required string GetClaudeCode { get; init; }
}
