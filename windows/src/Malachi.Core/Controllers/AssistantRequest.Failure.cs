// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantRequest.swift
// (AssistantRequest.Failure, text, reason); GTK:
// ui/internal/assistantpanel/oneshot.go (Failure, FailureKind, Text,
// ReasonText). Swift's enum with an associated value is a closed record
// hierarchy, compared by value as the enum is.

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantRequest
{
    /// <summary>Why a request brought no answer.</summary>
    public abstract record Failure
    {
        // Only the cases below derive from it.
        private Failure()
        {
        }

        /// <summary>
        /// The line where the panel's errors are shown (the compose window's
        /// popover): the panel's texts, and for a missing sign-in where to
        /// sign in (a request has no Sign In… of its own).
        /// </summary>
        public string Text => this switch
        {
            NotFound => Assistant.PanelTexts().NotFound,
            NotSignedIn => Assistant.SignInTexts().Hint,
            Stopped s => Assistant.StoppedText(s.Detail),
            _ => Assistant.StoppedText(""),
        };

        /// <summary>The reason inside another sentence ("The search could not be converted: %s").</summary>
        public string Reason => this switch
        {
            NotFound => Assistant.PanelTexts().NotFound,
            NotSignedIn => Assistant.SignInTexts().Hint,
            Stopped s => s.Detail,
            _ => "",
        };

        /// <summary>Claude Code was not found on this computer.</summary>
        public sealed record NotFound : Failure;

        /// <summary>Claude Code says it is not signed in, or the API refused its sign-in.</summary>
        public sealed record NotSignedIn : Failure;

        /// <summary>
        /// It ended badly; the reason is technical (the result's text or
        /// subtype, stderr's first line, the timeout, a launch failure).
        /// </summary>
        /// <param name="Detail">The technical reason.</param>
        public sealed record Stopped(string Detail) : Failure;
    }
}
