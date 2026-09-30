// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SearchConversion.swift
// (SearchConversion.Outcome); GTK: ui/internal/assistantpanel/oneshot.go
// (SearchOutcome, SearchOutcomeKind). Swift's enum with associated values
// is a closed record hierarchy, compared by value as the enum is.

namespace Malachi.Core.Controllers;

public sealed partial class SearchConversion
{
    /// <summary>How a search in the user's own words ended.</summary>
    public abstract record Outcome
    {
        // Only the cases below derive from it.
        private Outcome()
        {
        }

        /// <summary>The query to search for.</summary>
        /// <param name="Text">The query.</param>
        public sealed record Query(string Text) : Outcome;

        /// <summary>The toast.</summary>
        /// <param name="Text">"The search could not be converted: …".</param>
        public sealed record Failed(string Text) : Outcome;

        /// <summary>The user declined the consent question; nothing was sent.</summary>
        public sealed record Declined : Outcome;
    }
}
