// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Linked.swift; GTK:
// ui/internal/accountwizard/linked.go (withIdentity, LinkedMatch,
// showGOAHint's text) and ui/internal/signin (goaAccountID).
//
// The pure part of accounts whose sign-in belongs to GNOME Online Accounts.
// The daemon reports none on Windows (account.linked is empty without GOA),
// but the logic is kept so the wizard mirrors the GTK one, as on macOS.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Wizard;

/// <summary>Accounts another desktop service signs in.</summary>
public static class Linked
{
    /// <summary>
    /// signin.goaAccountID (Swift <c>linkedAccountID</c>): the GNOME Online
    /// Accounts id an account signs in with, null when it has none yet (the
    /// sign-in hint of <c>account.discover</c>) or signs in otherwise.
    /// </summary>
    public static string? LinkedAccountId(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        if (cfg.Graph is { } graph && graph.Source == GraphSource.Goa)
        {
            return NonEmpty(graph.GoaAccountId);
        }
        if (cfg.OAuth2 is { } oauth2 && oauth2.Source == OAuth2Source.Goa)
        {
            return NonEmpty(oauth2.GoaAccountId);
        }
        return null;
    }

    /// <summary>
    /// accountwizard.withIdentity: the daemon-built account of a linked
    /// sign-in with what the identity page adds: the display name, and an
    /// account name derived from the address when the daemon left it at the
    /// address itself.
    /// </summary>
    /// <remarks>
    /// Go compares the name with the address by strings.EqualFold, Swift by
    /// caseInsensitiveCompare, and this by OrdinalIgnoreCase: the three agree
    /// on ASCII and may differ on rare letters beyond it (EqualFold's
    /// simple-fold orbits, such as "ϑ" and "ϴ"), where the name the user
    /// gave is kept rather than replaced; no pin or match depends on it.
    /// </remarks>
    public static AccountConfig WithIdentity(AccountConfig config, Identity id)
    {
        ArgumentNullException.ThrowIfNull(config);
        ArgumentNullException.ThrowIfNull(id);
        var displayName = id.DisplayName.Trim();
        var name = config.Name.Trim();
        return config with
        {
            DisplayName = displayName.Length == 0 ? null : displayName,
            Name = name.Length == 0 || string.Equals(name, config.Email, StringComparison.OrdinalIgnoreCase)
                ? Fields.SuggestAccountName(config.Email)
                : config.Name,
        };
    }

    /// <summary>accountwizard.LinkedMatch: the linked account with the address (case-insensitively), if any.</summary>
    public static LinkedAccount? LinkedMatch(IEnumerable<LinkedAccount> linked, string email)
    {
        ArgumentNullException.ThrowIfNull(linked);
        ArgumentNullException.ThrowIfNull(email);
        var want = email.Trim().ToLowerInvariant();
        foreach (var l in linked)
        {
            var key = l.Email.ToLowerInvariant();
            if (key == want)
            {
                return l;
            }
        }
        return null;
    }

    /// <summary>
    /// accountwizard.showGOAHint's description: for an address of the named
    /// provider that the desktop is not signed in to yet; Microsoft 365 when
    /// the provider has no name.
    /// </summary>
    public static string GoaHintText(string? providerName)
    {
        var name = providerName ?? "";
        if (name.Length == 0)
        {
            // signin.ProviderName(ProviderMicrosoft365): a brand name, not
            // translated.
            name = "Microsoft 365";
        }
        // TRANSLATORS: %s is a provider such as "Microsoft 365" or "Google".
        return L10n.T("This address belongs to a %s account. Add it under Settings → Online Accounts, then come back here.", name);
    }

    // Go's "" sentinel as null.
    private static string? NonEmpty(string? s) => string.IsNullOrEmpty(s) ? null : s;
}
