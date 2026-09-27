// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/SignIn.swift (classifyDiscovery)
// and the rules OAuth.swift takes from signin (signedInAs, isBrowserURL,
// isSignInProblem); GTK: ui/internal/signin/signin.go (NeedsBrowserSignIn,
// Path, ClassifyDiscovery, isPasswordAccount, Failure, ClassifyFailure,
// signedInAsOf, IsClientMissing, TestNeedsSignIn, BrowserURL).
//
// Which way the wizard takes after account.discover, and how a browser
// sign-in failed. How an account signs in (signin.Kind, KindOf, Provider,
// ProviderName) is Malachi.Core.Model.Provider with SignInKind, where macOS
// keeps it (Model/Provider.swift: SignInKind, signInKind, accountProvider,
// providerName); this file uses that one implementation. Failed calls are
// read through RpcErrorText.Classify (the transport's exceptions, a
// timeout, a cancellation), the counterpart of Go's errors.Is/As and
// Swift's type checks. The browser's address is judged by the port of Go's
// url.Parse (UrlSyntax), never by System.Uri.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Wizard;

/// <summary>The signin package: the way after <c>account.discover</c>, and why a browser sign-in failed.</summary>
public static class SignIn
{
    // signin.maxAddressLen: bounds data.signedInAs (RFC 5321 path limit).
    private const int MaxAddressLen = 254;

    /// <summary>signin.Path (Swift <c>Discovery.Path</c>): the page the wizard continues on.</summary>
    public enum Path
    {
        /// <summary>
        /// Servers and a password: the connection test when the discovery has
        /// a config, the Servers page with a guess otherwise.
        /// </summary>
        Password,

        /// <summary>Signed in through GNOME Online Accounts: the daemon's account is complete; test it.</summary>
        Goa,

        /// <summary>
        /// An address of a GNOME Online Accounts provider the desktop is not
        /// signed in to yet: the hint page (with the browser as a way out when
        /// <see cref="Discovery.OAuthAlt"/> is set).
        /// </summary>
        GoaHint,

        /// <summary>The daemon's own sign-in in the browser.</summary>
        OAuth,
    }

    /// <summary>signin.Failure: why a browser sign-in ended without an account.</summary>
    public enum Failure
    {
        /// <summary>Anything else; the caller shows the generic sentence of <see cref="RpcErrorText"/>.</summary>
        Other,

        /// <summary>Access was denied in the browser, or the session was cancelled.</summary>
        Cancelled,

        /// <summary>The provider refused the sign-in (authFailed).</summary>
        Refused,

        /// <summary>The session expired, or the call to the daemon timed out.</summary>
        Timeout,

        /// <summary>The browser signed in to another mailbox.</summary>
        WrongAccount,
    }

    /// <summary>
    /// signin.NeedsBrowserSignIn: an account that waits for the user to sign
    /// in again through the daemon's own sign-in.
    /// </summary>
    public static bool NeedsBrowserSignIn(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return a.State.Status == SyncStatus.AuthRequired && Provider.SignInKindOf(a.Config) == SignInKind.OAuth;
    }

    /// <summary>
    /// signin.ClassifyDiscovery: a GNOME Online Accounts config with an
    /// account id goes to the test, one without to the hint (with the first
    /// browser sign-in and the first app-password account among the
    /// alternatives); a daemon config goes to the browser sign-in (with the
    /// first app-password account); anything else, a failure
    /// (<paramref name="error"/> set) or an answer without a config included,
    /// is the password path.
    /// </summary>
    public static Discovery ClassifyDiscovery(AccountDiscoverResult? result, Exception? error = null)
    {
        if (error is not null || result?.Config is not { } cfg)
        {
            return new Discovery { Path = Path.Password };
        }
        var provider = Provider.AccountProvider(cfg);
        switch (Provider.SignInKindOf(cfg))
        {
            case SignInKind.Goa:
                if (Linked.LinkedAccountId(cfg) is not null)
                {
                    return new Discovery { Path = Path.Goa, Config = cfg, Provider = provider };
                }
                return new Discovery
                {
                    Path = Path.GoaHint,
                    Config = cfg,
                    OAuthAlt = First(result.Alternatives, c => Provider.SignInKindOf(c) == SignInKind.OAuth),
                    PasswordAlt = First(result.Alternatives, IsPasswordAccount),
                    Provider = provider,
                };
            case SignInKind.OAuth:
                return new Discovery
                {
                    Path = Path.OAuth,
                    Config = cfg,
                    PasswordAlt = First(result.Alternatives, IsPasswordAccount),
                    Provider = provider,
                };
            default:
                return new Discovery { Path = Path.Password, Config = cfg };
        }
    }

    /// <summary>
    /// signin.ClassifyFailure: sorts an <c>account.oauthWait</c> failure;
    /// <c>SignedInAs</c> is the mailbox the browser signed in to, set for
    /// <see cref="Failure.WrongAccount"/> only.
    /// </summary>
    public static (Failure Failure, string SignedInAs) ClassifyFailure(Exception? error)
    {
        var (kind, e) = RpcErrorText.Classify(error);
        if (kind == RpcErrorText.FailureKind.TimedOut)
        {
            return (Failure.Timeout, "");
        }
        if (e is null)
        {
            return (Failure.Other, "");
        }
        switch (e.Code.Value)
        {
            case ErrorCode.Cancelled:
                return (Failure.Cancelled, "");
            case ErrorCode.AuthFailed:
                return (Failure.Refused, "");
            case ErrorCode.ServerTimeout:
                return (Failure.Timeout, "");
            case ErrorCode.InvalidArgument when SignedInAs(e) is { } who:
                return (Failure.WrongAccount, who);
            default:
                return (Failure.Other, "");
        }
    }

    /// <summary>
    /// signin.signedInAsOf: the mailbox the browser signed in to, from an
    /// invalidArgument's <c>data.signedInAs</c>; null unless it is a plausible
    /// address (trimmed of White_Space, at most 254 UTF-8 bytes, no lone
    /// surrogate, no control characters and no invisible format characters
    /// such as U+202E, which reverses what follows). It comes from the
    /// provider through the daemon and is only ever shown as plain text.
    /// </summary>
    public static string? SignedInAs(RpcError e)
    {
        ArgumentNullException.ThrowIfNull(e);
        if (e.Data is not { ValueKind: JsonValueKind.Object } data || !data.TryGetProperty("signedInAs", out var v)
            || v.ValueKind != JsonValueKind.String)
        {
            return null;
        }
        string raw;
        try
        {
            raw = v.GetString() ?? "";
        }
        catch (InvalidOperationException)
        {
            return null;
        }
        var s = raw.Trim();
        if (s.Length == 0 || Encoding.UTF8.GetByteCount(s) > MaxAddressLen)
        {
            return null;
        }
        for (var i = 0; i < s.Length;)
        {
            if (Rune.DecodeFromUtf16(s.AsSpan(i), out var r, out var n) != System.Buffers.OperationStatus.Done)
            {
                return null; // Go: not valid UTF-8
            }
            if (Rune.GetUnicodeCategory(r) is UnicodeCategory.Control or UnicodeCategory.Format)
            {
                return null;
            }
            i += n;
        }
        return s;
    }

    /// <summary>
    /// signin.IsClientMissing: the <c>oauthClientMissing</c> error of
    /// <c>account.oauthStart</c>: no OAuth client is configured for the
    /// provider.
    /// </summary>
    public static bool IsClientMissing(Exception? error) =>
        RpcErrorText.DaemonError(error) is { } e && e.Code == ErrorCode.OAuthClientMissing;

    /// <summary>
    /// signin.TestNeedsSignIn (Swift <c>isSignInProblem</c>): an
    /// <c>account.test</c> outcome that only a new sign-in can fix: the call
    /// (<paramref name="error"/>) or an endpoint failed with authFailed or
    /// authRequired. The results then offer "Sign In Again".
    /// </summary>
    public static bool TestNeedsSignIn(AccountTestResult? result, Exception? error)
    {
        if (RpcErrorText.DaemonError(error) is { } e)
        {
            return IsSignInCode(e.Code);
        }
        if (result is null)
        {
            return false;
        }
        foreach (var r in (ReadOnlySpan<EndpointTestResult?>)[result.Imap, result.Smtp, result.Graph])
        {
            if (r?.Error is { } re && IsSignInCode(re.Code))
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>
    /// signin.BrowserURL (Swift <c>isBrowserURL</c>): only an absolute https
    /// address with a host and without user information, as the daemon builds
    /// them, is opened in the browser.
    /// </summary>
    public static bool BrowserUrl(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return UrlSyntax.Parse(s) is { } u && u.Scheme == "https" && u.Rest.Host.Length > 0 && !u.Rest.HasUserinfo
            && u.Rest.Opaque.IsEmpty;
    }

    private static bool IsSignInCode(ErrorCode c) => c == ErrorCode.AuthFailed || c == ErrorCode.AuthRequired;

    // signin.isPasswordAccount: an IMAP/SMTP account whose endpoints both
    // sign in with a password (Google's app-password alternative).
    private static bool IsPasswordAccount(AccountConfig c) =>
        Provider.SignInKindOf(c) == SignInKind.Password && c.ProtocolKind == AccountKind.Imap
        && c.Imap is { } imap && imap.AuthMethod == AuthMethod.Password
        && c.Smtp is { } smtp && smtp.AuthMethod == AuthMethod.Password;

    // signin.firstAlternative: the first alternative that matches, null when
    // none does.
    private static AccountConfig? First(IReadOnlyList<AccountConfig> alternatives, Func<AccountConfig, bool> match)
    {
        foreach (var c in alternatives)
        {
            if (match(c))
            {
                return c;
            }
        }
        return null;
    }
}
