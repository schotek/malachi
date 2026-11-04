// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeRewriteController.swift
// (ComposeRewriteController.State); GTK: ui/internal/assistantpanel/oneshot.go
// (RewriteState, RewriteStateKind). Swift's enum with associated values is
// a closed record hierarchy, compared by value as the enum is; it takes
// GTK's name, as a nested State would clash with the controller's State.

namespace Malachi.Core.Controllers;

public sealed partial class ComposeRewriteController
{
    /// <summary>Where the rewrite is (Swift <c>State</c>).</summary>
    public abstract record RewriteState
    {
        // Only the cases below derive from it.
        private RewriteState()
        {
        }

        /// <summary>Nothing asked, or the consent was declined.</summary>
        public sealed record Idle : RewriteState;

        /// <summary>Asked; the answer so far, cleaned.</summary>
        /// <param name="Preview">The answer so far.</param>
        public sealed record Running(string Preview) : RewriteState;

        /// <summary>The answer, cleaned and not empty.</summary>
        /// <param name="Text">The text to insert.</param>
        public sealed record Done(string Text) : RewriteState;

        /// <summary>What went wrong, as a line to show.</summary>
        /// <param name="Text">The line.</param>
        public sealed record Failed(string Text) : RewriteState;
    }
}
