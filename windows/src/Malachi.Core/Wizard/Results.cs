// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Results.swift; GTK:
// ui/internal/accountwizard/results.go (Classify, reported, EndpointSummary,
// PasswordMissing, passwordBannerText).
//
// What the test page makes of an account.test result.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Wizard;

/// <summary>The connection test's outcome and its rows.</summary>
public static class Results
{
    /// <summary>
    /// accountwizard.Classify: reduces a test result to an outcome over the
    /// endpoints the daemon reported (imap and smtp, or graph). A result
    /// without any endpoint is a failure.
    /// </summary>
    public static Outcome Classify(AccountTestResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var endpoints = Reported(res);
        if (endpoints.Count == 0)
        {
            return Outcome.Failed;
        }
        var ok = true;
        foreach (var r in endpoints)
        {
            if (!r.Ok)
            {
                ok = false;
            }
            if (r.Error is { } e && e.Code == ErrorCode.AuthFailed)
            {
                return Outcome.AuthFailed;
            }
        }
        return ok ? Outcome.Ok : Outcome.Failed;
    }

    /// <summary>
    /// accountwizard.PasswordMissing: <c>account.test</c> failed as a whole
    /// with authRequired for an account that signs in with a password
    /// (<paramref name="linked"/> false): no password is stored and none was
    /// typed. The wizard asks for it on the identity page, as for a refused
    /// one.
    /// </summary>
    public static bool PasswordMissing(Exception? error, bool linked) =>
        !linked && RpcErrorText.DaemonError(error) is { } e && e.Code == ErrorCode.AuthRequired;

    /// <summary>
    /// accountwizard.passwordBannerText: the identity page's banner when the
    /// password is what to fix: authRequired means none is stored, anything
    /// else (authFailed) that the server refused it. imapHost is the incoming
    /// server of the account: Google's refuses the password of the account
    /// itself and takes an app password only, which the banner then says.
    /// </summary>
    public static string PasswordBannerText(ErrorCode reason, string imapHost)
    {
        if (reason == ErrorCode.AuthRequired)
        {
            // TRANSLATORS: wizard banner on the identity page
            return L10n.T("No password is stored for this account. Enter it to continue.");
        }
        if (GoogleHost(imapHost))
        {
            return L10n.T("Google rejected the sign-in. It does not accept the account's password here: create an app password in your Google Account and enter that one. For a work or school account, the administrator has to allow app passwords and IMAP.");
        }
        return L10n.T("The server rejected the user name or password");
    }

    /// <summary>
    /// accountwizard.GoogleHost: a mail server of Gmail and Google Workspace,
    /// as typed or discovered: the case and the spaces around it do not matter.
    /// </summary>
    public static bool GoogleHost(string? host)
    {
        var h = (host ?? "").Trim().ToLowerInvariant();
        if (h.EndsWith('.'))
        {
            h = h[..^1];
        }
        return h is "imap.gmail.com" or "smtp.gmail.com" or "imap.googlemail.com" or "smtp.googlemail.com";
    }

    /// <summary>
    /// accountwizard.EndpointSummary: the row icon (a GTK icon name, which the
    /// view maps to its own glyph) and subtitle for one endpoint; null is an
    /// endpoint the daemon did not test. The server's capabilities are
    /// hostile data and deliberately not shown.
    /// </summary>
    public static (string Icon, string Text) EndpointSummary(EndpointTestResult? r)
    {
        if (r is null)
        {
            return ("dialog-question-symbolic", L10n.T("Not tested"));
        }
        if (r.Ok)
        {
            // TRANSLATORS: %d is the connection latency in milliseconds.
            return ("emblem-ok-symbolic", L10n.T("Connected in %d ms", r.LatencyMs));
        }
        return ("dialog-error-symbolic", RpcErrorText.EndpointErrorText(r.Error));
    }

    // accountwizard.reported: the endpoints the daemon reported.
    private static List<EndpointTestResult> Reported(AccountTestResult res)
    {
        var output = new List<EndpointTestResult>(3);
        foreach (var r in (ReadOnlySpan<EndpointTestResult?>)[res.Imap, res.Smtp, res.Graph])
        {
            if (r is not null)
            {
                output.Add(r);
            }
        }
        return output;
    }
}
