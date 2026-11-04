// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantRequest.swift
// (AssistantRequest.Outcome); GTK: ui/internal/assistantpanel/oneshot.go
// (Outcome, OutcomeKind). Swift's enum with associated values is a closed
// record hierarchy, compared by value as the enum is: the structured
// output byte for byte, as Data compares.

using System;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantRequest
{
    /// <summary>How a request ended.</summary>
    public abstract record Outcome
    {
        // Only the cases below derive from it.
        private Outcome()
        {
        }

        /// <summary>
        /// The result: its text (the streamed text when the result has none)
        /// and its <c>structured_output</c> as raw JSON (null without one).
        /// </summary>
        /// <param name="Text">The answer's text.</param>
        /// <param name="Structured">The raw JSON of <c>structured_output</c>, or null.</param>
        public sealed record Answered(string Text, byte[]? Structured) : Outcome
        {
            /// <inheritdoc/>
            public bool Equals(Answered? other) =>
                other is not null
                && string.Equals(Text, other.Text, StringComparison.Ordinal)
                && (Structured is null
                    ? other.Structured is null
                    : other.Structured is not null && Structured.AsSpan().SequenceEqual(other.Structured));

            /// <inheritdoc/>
            public override int GetHashCode()
            {
                var hash = new HashCode();
                hash.Add(Text, StringComparer.Ordinal);
                hash.Add(Structured is null);
                if (Structured is not null)
                {
                    hash.AddBytes(Structured);
                }
                return hash.ToHashCode();
            }
        }

        /// <summary>It brought no answer; <paramref name="Failure"/> says why.</summary>
        /// <param name="Failure">Why.</param>
        public sealed record Failed(Failure Failure) : Outcome;

        /// <summary>The user declined the consent question; nothing was sent.</summary>
        public sealed record Declined : Outcome;
    }
}
