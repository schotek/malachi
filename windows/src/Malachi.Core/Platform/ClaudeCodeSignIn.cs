// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/ClaudeCodeLocator.swift
// (ClaudeCodeLocator.SignInResult); GTK:
// ui/internal/assistantpanel/locator.go (SignInResult, SignInOutcome).
// Swift's enum with an associated value is a closed record hierarchy,
// compared by value as the enum is.

namespace Malachi.Core.Platform;

/// <summary>How Claude Code's sign-in ended (<see cref="ClaudeCodeLocator.SignInAsync"/>).</summary>
public abstract record ClaudeCodeSignIn
{
    // Only the cases below derive from it.
    private ClaudeCodeSignIn()
    {
    }

    /// <summary>SignInDone: <c>claude auth login</c> ended with status 0.</summary>
    public sealed record Done : ClaudeCodeSignIn;

    /// <summary>SignInFailed: it ended badly, or could not run.</summary>
    /// <param name="Reason">Technical: stderr's first line, else the exit status in words.</param>
    public sealed record Failed(string Reason) : ClaudeCodeSignIn;

    /// <summary>SignInTimedOut: the browser brought no answer within the timeout.</summary>
    public sealed record TimedOut : ClaudeCodeSignIn;

    /// <summary>SignInCancelled: it was cancelled, or another sign-in took its place.</summary>
    public sealed record Cancelled : ClaudeCodeSignIn;

    /// <summary>SignInNotFound: there is no Claude Code to sign in.</summary>
    public sealed record NotFound : ClaudeCodeSignIn;
}
