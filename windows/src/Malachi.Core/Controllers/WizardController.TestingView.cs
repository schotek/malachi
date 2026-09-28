// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.TestingView); GTK: account_wizard.blp (testing_stack).
// Swift's enum with associated values is a closed record hierarchy,
// compared by value as the enum is.

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>What the testing page shows (the <c>testing_stack</c> of the Blueprint).</summary>
    public abstract record TestingView
    {
        // Only the cases below derive from it.
        private TestingView()
        {
        }

        /// <summary>A call runs: the spinner with <paramref name="Title"/>.</summary>
        /// <param name="Title">"Testing Connection…", "Adding Account…" or "Saving Account…".</param>
        public sealed record Progress(string Title) : TestingView;

        /// <summary>The test answered.</summary>
        /// <param name="View">The results page.</param>
        public sealed record Results(ResultsView View) : TestingView;
    }
}
