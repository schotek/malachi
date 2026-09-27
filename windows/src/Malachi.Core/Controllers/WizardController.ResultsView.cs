// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.ResultsView); GTK: account_wizard.blp (the results
// status page) and ui/internal/accountwizard/wizard.go (showResults).

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>
    /// The results page of the connection test. A null row is hidden: an
    /// IMAP account shows <see cref="Imap"/> and <see cref="Smtp"/>, a Graph
    /// account <see cref="Graph"/>.
    /// </summary>
    public sealed record ResultsView
    {
        /// <summary>A GTK icon name: emblem-ok-symbolic or dialog-warning-symbolic.</summary>
        public required string Icon { get; init; }

        /// <summary>The page's title.</summary>
        public required string Title { get; init; }

        /// <summary>The page's description; null for none.</summary>
        public string? Description { get; init; }

        /// <summary>The IMAP row.</summary>
        public EndpointRow? Imap { get; init; }

        /// <summary>The SMTP row.</summary>
        public EndpointRow? Smtp { get; init; }

        /// <summary>The Graph mailbox's row.</summary>
        public EndpointRow? Graph { get; init; }

        /// <summary>The buttons offered.</summary>
        public required WizardButtons Buttons { get; init; }
    }
}
