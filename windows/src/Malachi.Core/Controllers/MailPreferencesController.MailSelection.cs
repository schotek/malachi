// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailPreferencesController.swift
// (MailPreferencesController.MailSelection); GTK:
// ui/internal/window/preferences.go (apply: nearestInterval, indexOfPolicy,
// indexOfRetention).

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

public sealed partial class MailPreferencesController
{
    /// <summary>
    /// The pop-up positions of a preference set, in the order of the
    /// StringLists of preferences.blp (<see cref="PreferenceChoices.IntervalChoices"/>,
    /// <see cref="PreferenceChoices.RemoteChoices"/>,
    /// <see cref="PreferenceChoices.RetentionChoices"/>).
    /// </summary>
    /// <param name="Interval">Check for New Mail.</param>
    /// <param name="RemoteContent">Load Remote Images.</param>
    /// <param name="Retention">Keep Mail Offline For.</param>
    public sealed record MailSelection(int Interval, int RemoteContent, int Retention)
    {
        /// <summary>The nearest positions of <paramref name="p"/> (preferences.go <c>apply</c>).</summary>
        public MailSelection(Preferences p)
            : this(
                PreferenceChoices.NearestInterval((p ?? throw new ArgumentNullException(nameof(p))).SyncIntervalSeconds),
                PreferenceChoices.IndexOfPolicy(p.RemoteContent),
                PreferenceChoices.IndexOfRetention(p.OfflineDays))
        {
        }
    }
}
