// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.2, §6.3, §6.5, §6.6): the headers of
// what the request gate serves, in the CRLF-separated form
// CoreWebView2Environment.CreateWebResourceResponse takes. The CSP goes out
// as a response header as well as the documents' <meta> (the header also
// binds what comes before the <meta> and a document without one, such as a
// PDF); nosniff keeps Chromium from reading bytes as another type than the
// one the gate chose; no-store keeps nothing in a cache (the views are
// InPrivate anyway); no-referrer leaves nothing for a request that should
// never have happened. The picture handlers' headers are macOS's
// (CIDSchemeHandler.respond: the type without parameters, the length,
// no-store).

using System;
using System.Globalization;
using Malachi.Core.Html;

namespace Malachi.Core.Presentation;

/// <summary>The response headers of the request gate.</summary>
public static class ResponseHeaders
{
    /// <summary>A document the view generated or the bytes the previewer shows, with its CSP.</summary>
    public static string Document(string mediaType, string csp)
    {
        ArgumentNullException.ThrowIfNull(mediaType);
        ArgumentNullException.ThrowIfNull(csp);
        return "Content-Type: " + mediaType
            + "\r\nContent-Security-Policy: " + csp
            + "\r\nX-Content-Type-Options: nosniff"
            + "\r\nCache-Control: no-store"
            + "\r\nReferrer-Policy: no-referrer";
    }

    /// <summary>
    /// A picture: its claimed <paramref name="contentType"/> without
    /// parameters, trimmed and lower-cased (PartSchemeHandler.mediaType,
    /// CIDSchemeHandler.mediaType), and its length.
    /// </summary>
    public static string Picture(string contentType, long length)
    {
        ArgumentNullException.ThrowIfNull(contentType);
        ArgumentOutOfRangeException.ThrowIfNegative(length);
        return "Content-Type: " + PartPath.BareMediaType(contentType)
            + "\r\nContent-Length: " + length.ToString(CultureInfo.InvariantCulture)
            + "\r\nX-Content-Type-Options: nosniff"
            + "\r\nCache-Control: no-store";
    }

    /// <summary>An error response: no body, nothing cached.</summary>
    public const string Refused = "Cache-Control: no-store";
}
