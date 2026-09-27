// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/Links.swift; GTK:
// ui/internal/htmlview/links.go (AllowedLink, Masked, hostOfText,
// looksLikeHost, sameSite).
//
// Pure helpers around links, testable without a view. Ordinal (byte)
// semantics throughout, as in Go: a combining mark after a separator must
// not change what a prefix check sees. URLs are read by the port of Go's
// net/url (UrlSyntax), never by System.Uri.

using System;
using Malachi.Core.Compose;

namespace Malachi.Core.Html;

/// <summary>What a link of a message may do, and whether its text lies about it.</summary>
public static class Links
{
    /// <summary>
    /// htmlview.AllowedLink: whether a link target may be handed to the
    /// desktop or the composer: http, https or mailto, nothing else. The
    /// sanitiser only lets those through, so this is a second look, not the
    /// first.
    /// </summary>
    public static bool AllowedLink(string uri)
    {
        ArgumentNullException.ThrowIfNull(uri);
        var lower = uri.ToLowerInvariant();
        return lower.StartsWith("http://", StringComparison.Ordinal) || lower.StartsWith("https://", StringComparison.Ordinal)
            || lower.StartsWith("mailto:", StringComparison.Ordinal);
    }

    /// <summary>
    /// htmlview.Masked: whether a link's visible text reads as a web address
    /// of a different site than the link really leads to
    /// ("https://bank.example" over a link to evil.example), which is the
    /// shape of a phishing link. Text that is not an address ("click here")
    /// is never masked.
    /// </summary>
    public static bool IsMasked(string text, string href)
    {
        ArgumentNullException.ThrowIfNull(text);
        ArgumentNullException.ThrowIfNull(href);
        var shown = HostOfText(text);
        if (shown.Length == 0)
        {
            return false;
        }
        if (UrlSyntax.Hostname(href.Trim()) is not { } target)
        {
            return false;
        }
        var real = target.ToLowerInvariant();
        return real.Length != 0 && !SameSite(shown, real);
    }

    /// <summary>
    /// htmlview.hostOfText: the host name the text claims, "" when the text
    /// is not an address: a scheme, a www. prefix, or a bare host with a dot
    /// and an alphabetic top-level label.
    /// </summary>
    public static string HostOfText(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var t = text.Trim().ToLowerInvariant();
        if (t.Length == 0 || t.AsSpan().IndexOfAny(" \t\n") >= 0)
        {
            return "";
        }
        if (!t.Contains("://", StringComparison.Ordinal))
        {
            if (t.Contains('@', StringComparison.Ordinal))
            {
                return ""; // an e-mail address, not a web address
            }
            t = "http://" + t;
        }
        return UrlSyntax.Hostname(t) is { } host && LooksLikeHost(host) ? host : "";
    }

    /// <summary>
    /// htmlview.looksLikeHost: at least two non-empty labels of
    /// <c>[a-z0-9-]</c>, the last one alphabetic and at least two long.
    /// </summary>
    public static bool LooksLikeHost(string h)
    {
        ArgumentNullException.ThrowIfNull(h);
        var labels = h.Split('.');
        if (labels.Length < 2)
        {
            return false;
        }
        foreach (var label in labels)
        {
            if (label.Length == 0)
            {
                return false;
            }
            foreach (var c in label)
            {
                if (c is not ((>= 'a' and <= 'z') or (>= '0' and <= '9') or '-'))
                {
                    return false;
                }
            }
        }
        var tld = labels[^1];
        if (tld.Length < 2)
        {
            return false;
        }
        foreach (var c in tld)
        {
            if (c is < 'a' or > 'z')
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>htmlview.sameSite: a host and its subdomains are one site, www. aside.</summary>
    public static bool SameSite(string a, string b)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(b);
        a = TrimPrefix(a, "www.");
        b = TrimPrefix(b, "www.");
        return string.Equals(a, b, StringComparison.Ordinal) || a.EndsWith("." + b, StringComparison.Ordinal)
            || b.EndsWith("." + a, StringComparison.Ordinal);
    }

    private static string TrimPrefix(string s, string prefix) =>
        s.StartsWith(prefix, StringComparison.Ordinal) ? s[prefix.Length..] : s;
}
