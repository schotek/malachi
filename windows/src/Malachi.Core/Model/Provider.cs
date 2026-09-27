// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Provider.swift (signInKind,
// accountProvider, providerName, providerIconName, providerIcon); GTK:
// ui/internal/signin/signin.go (KindOf, Provider, ProviderName) and
// ui/internal/widget/provider.go (providerIconName, ProviderIcon).
//
// Swift's free function signInKind is SignInKindOf here (signin.KindOf):
// a member cannot share the name of the type it returns. This is the one
// implementation of signin.KindOf, Provider and ProviderName in the client:
// Malachi.Core.Wizard.SignIn (the discovery and failure rules of signin.go)
// and the wizard use it, as Swift's Wizard/SignIn.swift uses Provider.swift.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// Providers whose accounts sign in with OAuth2, as <c>account.linked</c>
/// names them (<see cref="LinkedProvider"/>): through GNOME Online Accounts,
/// or through the daemon's own sign-in in the browser. An account of theirs
/// has no password to ask for and no servers of the user's to edit. GNOME
/// Online Accounts does not exist on Windows, but a profile may carry such
/// an account, so the classification is kept.
/// </summary>
public static class Provider
{
    /// <summary>
    /// The generic icon: the fallback of <see cref="ProviderIcon"/>, also for
    /// a password account.
    /// </summary>
    public const string GenericIcon = "mail-unread-symbolic";

    /// <summary>
    /// signin.KindOf: a Graph account signs in through GNOME Online Accounts
    /// when <c>graph.source</c> is <c>goa</c> and through the daemon's own
    /// sign-in otherwise; an account with an <c>oauth2</c> block likewise by
    /// its source; anything else with a password.
    /// </summary>
    public static SignInKind SignInKindOf(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        if (cfg.ProtocolKind == AccountKind.Graph)
        {
            return cfg.Graph?.Source == GraphSource.Goa ? SignInKind.Goa : SignInKind.OAuth;
        }
        if (cfg.OAuth2 is { } oauth2)
        {
            return oauth2.Source == OAuth2Source.Goa ? SignInKind.Goa : SignInKind.OAuth;
        }
        return SignInKind.Password;
    }

    /// <summary>
    /// Which provider an account signs in with (signin.Provider): Microsoft
    /// 365 for a Graph account, the <c>oauth2</c> provider otherwise
    /// (<c>office365</c> is Microsoft 365), null for a password account or a
    /// provider this client does not know.
    /// </summary>
    public static LinkedProvider? AccountProvider(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        if (cfg.ProtocolKind == AccountKind.Graph)
        {
            return new LinkedProvider(LinkedProvider.Microsoft365);
        }
        return cfg.OAuth2?.Provider.Value switch
        {
            OAuth2Provider.Google => new LinkedProvider(LinkedProvider.Google),
            OAuth2Provider.Office365 => new LinkedProvider(LinkedProvider.Microsoft365),
            _ => (LinkedProvider?)null,
        };
    }

    /// <summary>
    /// The provider's name as shown to the user (signin.ProviderName). These
    /// are brand names and are not translated.
    /// </summary>
    public static string ProviderName(LinkedProvider? provider) => provider?.Value switch
    {
        LinkedProvider.Microsoft365 => "Microsoft 365",
        LinkedProvider.Google => "Google",
        _ => "",
    };

    /// <summary>
    /// The icon GNOME Online Accounts installs for the provider, null for an
    /// unknown one (provider.go <c>providerIconName</c>). The GTK icon name;
    /// the WinUI layer maps it to a glyph of its own.
    /// </summary>
    public static string? ProviderIconName(LinkedProvider? provider) => provider?.Value switch
    {
        LinkedProvider.Microsoft365 => "goa-account-ms365-symbolic",
        LinkedProvider.Google => "goa-account-google-symbolic",
        _ => null,
    };

    /// <summary>
    /// The provider's icon name, the generic mail icon otherwise (also for a
    /// password account; provider.go <c>ProviderIcon</c>). GTK falls back as
    /// well when the theme lacks the icon; on Windows every name maps to the
    /// generic icon (docs/windows-port.md §3.1, U6: no brand icons).
    /// </summary>
    public static string ProviderIcon(LinkedProvider? provider) => ProviderIconName(provider) ?? GenericIcon;
}
