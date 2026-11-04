// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.WizardPage and its rank); GTK:
// ui/internal/accountwizard/wizard.go (tagIdentity, tagServers, tagGOA,
// tagOAuth, tagTesting).

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>
    /// The navigation page tags of account_wizard.blp. The numeric value is
    /// the order the pages appear in, for the direction of a transition
    /// (Swift <c>rank</c>, <see cref="Rank"/>).
    /// </summary>
    public enum WizardPage
    {
        /// <summary>The name, address and password.</summary>
        Identity = 0,

        /// <summary>The IMAP and SMTP endpoints.</summary>
        Servers = 1,

        /// <summary>The sign-in hint for an address of a GNOME Online Accounts provider.</summary>
        Goa = 2,

        /// <summary>The daemon's own sign-in in the browser.</summary>
        OAuth = 3,

        /// <summary>The connection test and its results.</summary>
        Testing = 4,
    }

    /// <summary>The order <paramref name="page"/> appears in (Swift <c>WizardPage.rank</c>).</summary>
    public static int Rank(WizardPage page) => (int)page;
}
