// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift; GTK:
// ui/internal/accountwizard/fields.go (securityChoices, DefaultPort,
// PortForSecurityChange, indexOfSecurity, securityAt, ValidateEmail, Domain,
// SuggestAccountName, GuessConfig, MergeIdentity, ValidateIdentity,
// ValidateServers, BuildConfig, credentialsFor).
//
// The field logic of the account wizard. The daemon discovers, tests and
// validates; this only checks field syntax, fills port defaults and
// assembles the configuration. It is the one place that sets the
// authentication method of an IMAP account the wizard builds.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Compose;

namespace Malachi.Core.Wizard;

/// <summary>The wizard's fields: defaults, checks and the configuration they make.</summary>
public static class Fields
{
    /// <summary>accountwizard.securityChoices: the order of the "Security" choices in account_wizard.blp.</summary>
    public static IReadOnlyList<Security> SecurityChoices { get; } = [Security.Tls, Security.Starttls, Security.None];

    /// <summary>accountwizard.DefaultPort: the conventional port for a security mode.</summary>
    public static int DefaultPort(Endpoint kind, Security security) => kind switch
    {
        Endpoint.Imap => security == Security.Tls ? 993 : 143,
        _ => security == Security.Tls ? 465 : 587,
    };

    /// <summary>
    /// accountwizard.PortForSecurityChange: keeps a custom port and only swaps
    /// the default of the previous mode for the default of the new one.
    /// </summary>
    public static int PortForSecurityChange(Endpoint kind, int port, Security from, Security to)
    {
        if (from == to)
        {
            return port;
        }
        if (port == 0 || port == DefaultPort(kind, from))
        {
            return DefaultPort(kind, to);
        }
        return port;
    }

    /// <summary>accountwizard.indexOfSecurity: the row of a mode in <see cref="SecurityChoices"/>, 0 for an unknown one.</summary>
    public static int IndexOfSecurity(Security security)
    {
        for (var i = 0; i < SecurityChoices.Count; i++)
        {
            if (SecurityChoices[i] == security)
            {
                return i;
            }
        }
        return 0;
    }

    /// <summary>accountwizard.securityAt: the mode of a row, TLS out of range.</summary>
    public static Security SecurityAt(int index) =>
        index >= 0 && index < SecurityChoices.Count ? SecurityChoices[index] : Security.Tls;

    /// <summary>
    /// accountwizard.ValidateEmail: trims and accepts only a bare address: no
    /// display name, no comments. The daemon validates authoritatively; this
    /// is immediate feedback. Null when refused.
    /// </summary>
    public static string? ValidateEmail(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var trimmed = s.Trim();
        if (trimmed.Length == 0)
        {
            return null;
        }
        return AddressList.ParseAddress(trimmed) is { Name: null } a && a.Email == trimmed ? trimmed : null;
    }

    /// <summary>accountwizard.Domain: the lower-cased part after the last '@', or "".</summary>
    public static string Domain(string email)
    {
        ArgumentNullException.ThrowIfNull(email);
        var i = email.LastIndexOf('@');
        return i < 0 || i == email.Length - 1 ? "" : email[(i + 1)..].ToLowerInvariant();
    }

    /// <summary>accountwizard.SuggestAccountName: the default account name: the address's domain.</summary>
    public static string SuggestAccountName(string email)
    {
        var d = Domain(email);
        return d.Length == 0 ? email : d;
    }

    /// <summary>accountwizard.GuessConfig: the fallback when discovery finds nothing.</summary>
    public static AccountConfig GuessConfig(string email)
    {
        ArgumentNullException.ThrowIfNull(email);
        var (imap, smtp) = GuessServers(email);
        return new AccountConfig { Name = "", Email = email, Kind = AccountKind.Imap, Imap = imap, Smtp = smtp };
    }

    /// <summary>
    /// accountwizard.MergeIdentity: copies what the identity page knows into a
    /// discovered or guessed IMAP configuration. Missing endpoints are filled
    /// from the guess, so the Servers page always has both.
    /// </summary>
    public static AccountConfig MergeIdentity(AccountConfig config, Identity id)
    {
        ArgumentNullException.ThrowIfNull(config);
        ArgumentNullException.ThrowIfNull(id);
        var guess = GuessServers(id.Email);
        var imap = config.Imap ?? guess.Imap;
        var smtp = config.Smtp ?? guess.Smtp;
        if (imap.Username.Trim().Length == 0)
        {
            imap = imap with { Username = id.Email };
        }
        if (smtp.Username.Trim().Length == 0)
        {
            smtp = smtp with { Username = id.Email };
        }
        return config with
        {
            Email = id.Email,
            DisplayName = NonEmpty(id.DisplayName.Trim()),
            Name = config.Name.Trim().Length == 0 ? SuggestAccountName(id.Email) : config.Name,
            Kind = AccountKind.Imap,
            Graph = null,
            Imap = imap with { AuthMethod = AuthMethod.Password },
            Smtp = smtp with { AuthMethod = AuthMethod.Password },
        };
    }

    /// <summary>
    /// accountwizard.ValidateIdentity: checks address syntax and, when
    /// required, a non-empty password (editing an account may keep the stored
    /// one).
    /// </summary>
    public static IdentityProblems ValidateIdentity(Identity id, bool passwordRequired)
    {
        ArgumentNullException.ThrowIfNull(id);
        return new IdentityProblems { Email = ValidateEmail(id.Email) is null, Password = passwordRequired && id.Password.Length == 0 };
    }

    /// <summary>
    /// accountwizard.ValidateServers: flags empty hosts and user names. Ports
    /// are bounded by the number boxes; everything else is the daemon's call.
    /// </summary>
    public static ServerProblems ValidateServers(ServerFields imap, ServerFields smtp)
    {
        ArgumentNullException.ThrowIfNull(imap);
        ArgumentNullException.ThrowIfNull(smtp);
        return new ServerProblems
        {
            ImapHost = imap.Host.Trim().Length == 0,
            ImapUser = imap.Username.Trim().Length == 0,
            SmtpHost = smtp.Host.Trim().Length == 0,
            SmtpUser = smtp.Username.Trim().Length == 0,
        };
    }

    /// <summary>
    /// accountwizard.BuildConfig: assembles the wire configuration from the
    /// rows and their pins. It is the one place that sets the authentication
    /// method.
    /// </summary>
    public static AccountConfig BuildConfig(Identity identity, string name, ServerFields imap, ServerFields smtp)
    {
        ArgumentNullException.ThrowIfNull(identity);
        ArgumentNullException.ThrowIfNull(name);
        ArgumentNullException.ThrowIfNull(imap);
        ArgumentNullException.ThrowIfNull(smtp);
        var email = identity.Email.Trim();
        var accountName = name.Trim();
        if (accountName.Length == 0)
        {
            accountName = SuggestAccountName(email);
        }
        return new AccountConfig
        {
            Name = accountName,
            Email = email,
            DisplayName = NonEmpty(identity.DisplayName.Trim()),
            Kind = AccountKind.Imap,
            Imap = ServerConfigOf(imap),
            Smtp = ServerConfigOf(smtp),
        };
    }

    /// <summary>
    /// accountwizard.credentialsFor: the secret of the identity page; an empty
    /// password is no password (<c>account.update</c> then keeps the stored
    /// one).
    /// </summary>
    public static Credentials CredentialsFor(Identity id)
    {
        ArgumentNullException.ThrowIfNull(id);
        return new Credentials { Password = NonEmpty(id.Password) };
    }

    private static (ServerConfig Imap, ServerConfig Smtp) GuessServers(string email)
    {
        var d = Domain(email);
        return (
            new ServerConfig { Host = "imap." + d, Port = 993, Security = Security.Tls, Username = email, AuthMethod = AuthMethod.Password },
            new ServerConfig { Host = "smtp." + d, Port = 587, Security = Security.Starttls, Username = email, AuthMethod = AuthMethod.Password });
    }

    private static ServerConfig ServerConfigOf(ServerFields f) => new()
    {
        Host = f.Host.Trim(),
        Port = f.Port,
        Security = f.Security,
        Username = f.Username.Trim(),
        AuthMethod = AuthMethod.Password,
        CertificateSha256 = NonEmpty(f.CertificateSha256),
    };

    // Go's "" sentinel as null.
    private static string? NonEmpty(string s) => s.Length == 0 ? null : s;
}
