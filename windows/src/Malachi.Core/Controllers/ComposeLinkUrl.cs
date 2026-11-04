// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeLinkURL.swift
// (composeLinkURL); GTK: ui/internal/compose/compose.go (wireToolbar, the
// insertLink closure). Swift's free function composeLinkURL(_:) is
// ComposeLinkUrl.Accept.

using System;
using System.Text;
using Malachi.Core.Compose;

namespace Malachi.Core.Controllers;

/// <summary>What the compose window's link popover accepts.</summary>
public static class ComposeLinkUrl
{
    /// <summary>
    /// compose.go <c>insertLink</c>: Go runs <c>url.Parse</c> on the trimmed
    /// text and requires an http, https or mailto scheme; the same reading of
    /// the string (<see cref="UrlSyntax"/>) is applied here so a link the GTK
    /// UI refuses is refused too. The result is the string handed to
    /// <c>CreateLink</c>: the scheme lower-cased as <c>url.Parse</c> stores
    /// it, the rest as typed (Go's <c>URL.String()</c> would also
    /// percent-escape a path; the editor copes with the raw form). Null when
    /// refused.
    /// </summary>
    public static string? Accept(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        // strings.TrimSpace: Unicode's White_Space, which is .NET's too.
        var bytes = UrlSyntax.Bytes(raw.Trim()).AsMemory();
        var (u, fragment, hasFragment) = UrlSyntax.Cut(bytes, (byte)'#');
        if (UrlSyntax.HasControlByte(u.Span) || UrlSyntax.Scheme(u) is not { } parsed
            || UrlSyntax.ParseRest(parsed.Remainder, parsed.Scheme) is null)
        {
            return null;
        }
        if (!fragment.IsEmpty && UrlSyntax.Unescape(fragment.Span, UrlSyntax.Mode.Path) is null)
        {
            return null;
        }
        if (parsed.Scheme is not ("http" or "https" or "mailto"))
        {
            return null;
        }
        var result = parsed.Scheme + ":" + Encoding.UTF8.GetString(parsed.Remainder.Span);
        if (hasFragment)
        {
            result += "#" + Encoding.UTF8.GetString(fragment.Span);
        }
        return result;
    }
}
