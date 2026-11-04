// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/Mailto.swift; GTK:
// ui/internal/compose/mailto.go (ParseMailto). The URI is read by the port
// of Go's net/url (UrlSyntax), never by System.Uri.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>A <c>mailto:</c> URI as compose parameters.</summary>
public static class Mailto
{
    /// <summary>
    /// compose.ParseMailto: turns a <c>mailto:</c> URI into compose
    /// parameters. Only the standard fields are honoured (to, cc, bcc,
    /// subject, body; keys compared case-insensitively, the first value of
    /// each); the body is escaped for the editor. This is the "split the
    /// query string" the registration promises: no interpretation beyond
    /// that.
    /// </summary>
    /// <exception cref="MailtoException">The string is not a URI url.Parse accepts, or not a mailto: one.</exception>
    public static ComposeParams ParseMailto(string uri)
    {
        ArgumentNullException.ThrowIfNull(uri);
        if (UrlSyntax.Parse(uri) is not { } parsed)
        {
            throw new MailtoException(MailtoError.InvalidUri);
        }
        if (parsed.Scheme != "mailto")
        {
            throw new MailtoException(MailtoError.NotMailto);
        }
        var to = new List<Address>();
        if (UrlSyntax.Unescape(parsed.Rest.Opaque.Span, UrlSyntax.Mode.Path) is { } opaque)
        {
            to.AddRange(AddressList.Parse(opaque).Addresses);
        }
        IReadOnlyList<Address> cc = [];
        IReadOnlyList<Address> bcc = [];
        var subject = "";
        var body = "";
        foreach (var (key, value) in FirstValues(UrlSyntax.ParseQuery(parsed.Rest.RawQuery)))
        {
            switch (key.ToLowerInvariant())
            {
                case "to":
                    to.AddRange(AddressList.Parse(value).Addresses);
                    break;
                case "cc":
                    cc = AddressList.Parse(value).Addresses;
                    break;
                case "bcc":
                    bcc = AddressList.Parse(value).Addresses;
                    break;
                case "subject":
                    subject = value.Trim();
                    break;
                case "body":
                    body = Prefill.EscapeText(value);
                    break;
                default:
                    break;
            }
        }
        return new ComposeParams { Kind = ComposeKind.New, To = to, Cc = cc, Bcc = bcc, Subject = subject, BodyHtml = body };
    }

    // url.Values as ParseMailto reads it: the first value of every key, keys
    // in the order of their first appearance.
    private static List<(string Key, string Value)> FirstValues(IReadOnlyList<(string Key, string Value)> pairs)
    {
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var output = new List<(string, string)>();
        foreach (var (key, value) in pairs)
        {
            if (seen.Add(key))
            {
                output.Add((key, value));
            }
        }
        return output;
    }
}
