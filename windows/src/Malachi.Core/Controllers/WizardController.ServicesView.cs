// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.ServicesView); GTK: ui/internal/accountwizard oauth.go
// (the group over oauth_sign_in, docs/plans/account-services.md §3.2).
// The rows compare by content, as Swift's Equatable struct does.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.AccountServices;

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>
    /// The sign-in prompt's group "Also Sync from This Account": a switch per
    /// service the new account can sync (<see cref="AccountSync.OfferedForNew"/>),
    /// checked by default; <see cref="MailOnly"/> offers "Try Again with Mail
    /// Only" after a sign-in that asked for one of them did not succeed.
    /// </summary>
    public sealed record ServicesView
    {
        /// <summary>The group's title.</summary>
        public required string Title { get; init; }

        /// <summary>A switch per offered service, in their order.</summary>
        public IReadOnlyList<ServiceRow> Rows { get; init => field = value ?? []; } = [];

        /// <summary>The note under the switches.</summary>
        public required string Note { get; init; }

        /// <summary>The label of the mail-only retry; null hides it.</summary>
        public string? MailOnly { get; init; }

        /// <summary>Whether both hold the same, the rows compared in order.</summary>
        public bool Equals(ServicesView? other) =>
            other is not null && Title == other.Title && Rows.SequenceEqual(other.Rows) && Note == other.Note && MailOnly == other.MailOnly;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Title, Rows.Count, Note, MailOnly);
    }

    /// <summary>A switch of <see cref="ServicesView"/> (Swift <c>ServicesView.Row</c>).</summary>
    /// <param name="Service">The service.</param>
    /// <param name="Title">Its label.</param>
    /// <param name="On">Whether it is checked.</param>
    public sealed record ServiceRow(AccountSync.Service Service, string Title, bool On);
}
