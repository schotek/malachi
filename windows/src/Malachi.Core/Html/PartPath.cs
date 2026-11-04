// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/PartPath.swift; GTK:
// ui/internal/htmlview/scheme.go (Scheme) and links.go (ParsePath,
// ImageType).
//
// The malachi-cid: scheme the sanitiser rewrites cid: references to, and
// the checks every part reference passes before it reaches the daemon. Byte
// semantics, as in Go: a string is checked as its UTF-8 bytes.

using System;
using System.Text;
using Malachi.Core.Api;

namespace Malachi.Core.Html;

/// <summary>The <c>malachi-cid:</c> scheme of the message viewer.</summary>
public static class PartPath
{
    /// <summary>
    /// htmlview.Scheme: the URL scheme of message parts in a rendered body:
    /// <c>malachi-cid:&lt;accountId&gt;/&lt;messageId&gt;/&lt;partId&gt;</c>.
    /// The path names the message, so the handler is stateless and switching
    /// messages while pictures still load cannot hand one message another
    /// one's part.
    /// </summary>
    public const string PartScheme = "malachi-cid";

    /// <summary>
    /// htmlview.ParsePath: splits the path of a <c>malachi-cid:</c> URL into
    /// the account, message and part it names. Every segment is a generated
    /// id or a part number; anything else is refused (null) before it reaches
    /// the daemon.
    /// </summary>
    public static PartReference? ParsePartPath(string p)
    {
        ArgumentNullException.ThrowIfNull(p);
        var seg = p.Split('/');
        if (seg.Length != 3)
        {
            return null;
        }
        // Account and message ids are a prefix and hex digits; no dots, so no
        // "..".
        for (var i = 0; i < 2; i++)
        {
            var s = seg[i];
            if (s.Length == 0 || Encoding.UTF8.GetByteCount(s) > 128)
            {
                return null;
            }
            foreach (var c in s)
            {
                if (!IsIdChar(c))
                {
                    return null;
                }
            }
        }
        // A part number: digits joined by single dots.
        var part = seg[2];
        if (Encoding.UTF8.GetByteCount(part) > 64)
        {
            return null;
        }
        foreach (var n in part.Split('.'))
        {
            if (n.Length == 0)
            {
                return null;
            }
            foreach (var c in n)
            {
                if (c is < '0' or > '9')
                {
                    return null;
                }
            }
        }
        return new PartReference(new AccountId(seg[0]), new MessageId(seg[1]), part);
    }

    /// <summary>
    /// htmlview.ImageType: whether a part's media type may be shown as a
    /// picture: image/* except SVG, which is a document with scripts of its
    /// own.
    /// </summary>
    public static bool IsImageType(string contentType)
    {
        ArgumentNullException.ThrowIfNull(contentType);
        var ct = BareMediaType(contentType);
        return ct.StartsWith("image/", StringComparison.Ordinal) && ct != "image/svg+xml";
    }

    /// <summary>
    /// The media type without its parameters, trimmed and lower-cased (the
    /// normalisation ImageType and checkInline share); cut at the first
    /// semicolon, as in Go.
    /// </summary>
    internal static string BareMediaType(string contentType)
    {
        var ct = contentType.Trim().ToLowerInvariant();
        var semi = ct.IndexOf(';', StringComparison.Ordinal);
        return semi >= 0 ? ct[..semi].Trim() : ct;
    }

    private static bool IsIdChar(char c) =>
        c is (>= 'a' and <= 'z') or (>= 'A' and <= 'Z') or (>= '0' and <= '9') or '_' or '-';
}
