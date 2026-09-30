// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantSignIn.swift
// (signInArgs, signInTexts, signInFailedText, installURL); GTK:
// ui/internal/assistant/claude.go (SignInArgs, SignInEnv) and assistant.go
// (SignInTexts, SignInFailedText, InstallURL).
//
// Claude Code's sign-in from the application: Claude Code has a sign-in of
// its own, apart from Claude Desktop's, and the panel cannot answer without
// it. The application runs Claude Code's own `claude auth login`, which
// opens the browser at claude.ai, waits for its answer on a local port and
// stores the sign-in itself; the application only waits for the process to
// end (status 0: signed in). It waits with its stdin closed too. What it
// prints is never logged or shown: the address to open by hand names the
// sign-in's session. Measured with Claude Code 2.1.285.
//
// Windows difference: there is no SignInEnv. Go's adds the variables of a
// Linux desktop session to ChildEnv, which opening the browser takes there;
// on Windows Claude Code opens the browser through the shell, and
// ChildEnvironment (SystemRoot, the profile, a PATH with System32) is what
// that takes.

using System;
using System.Collections.Generic;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// assistant.InstallURL: where "Get Claude Code…" leads, Anthropic's page
    /// with the installers for every system. The application downloads and
    /// runs nothing itself.
    /// </summary>
    public const string InstallUrl = "https://code.claude.com/docs/en/setup";

    /// <summary>assistant.SignInArgs: the arguments of Claude Code's own sign-in.</summary>
    public static IReadOnlyList<string> SignInArguments { get; } = ["auth", "login"];

    /// <summary>assistant.SignInTexts: the fixed texts of Claude Code's sign-in, translated.</summary>
    public static SignInStrings SignInTexts() => new()
    {
        SignIn = L10n.T("Sign In…"),
        Waiting = L10n.T("Waiting for the sign-in in your browser…"),
        TimedOut = L10n.T("The sign-in took too long; try again"),
        // TRANSLATORS: AI is the page of the preferences with the Claude Code row.
        Hint = L10n.T("Claude Code is not signed in. Sign in under AI in the preferences."),
        // TRANSLATORS: A button that opens the web page with Claude Code's installers.
        GetClaudeCode = L10n.T("Get Claude Code…"),
    };

    /// <summary>
    /// assistant.SignInFailedText: the error line when Claude Code's sign-in
    /// ended badly. <paramref name="reason"/> is technical (its stderr, or
    /// its exit status) and shown as data, as <see cref="StoppedText"/> shows
    /// it: its first non-empty line without control characters, at most 200
    /// bytes; "unknown" when nothing is left.
    /// </summary>
    public static string SignInFailedText(string reason)
    {
        ArgumentNullException.ThrowIfNull(reason);
        // TRANSLATORS: %s is a technical reason.
        return L10n.T("The sign-in failed: %s", FirstLine(reason, MaxReason));
    }
}
