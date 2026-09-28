// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.2): where a web view's own documents
// live. GTK and macOS load a string at about:blank (LoadHtml,
// loadHTMLString); WebView2's NavigateToString refuses more than 1,572,834
// UTF-8 bytes and gives every document the same data: URL, so each view
// serves its document itself through the request gate, under a URL that
// names the view, the document's generation and a nonce. The gate serves
// that URL once; the nonce keeps an old document's URL, or one a page could
// guess, from ever resolving again.

using System;
using System.Globalization;
using System.Security.Cryptography;

namespace Malachi.Core.Presentation;

/// <summary>The <c>malachi-doc:</c> URLs the web views serve their documents under.</summary>
public static class DocumentAddress
{
    /// <summary>
    /// The scheme of the documents (registered with an authority and as
    /// secure, so a document's origin is <c>malachi-doc://&lt;view&gt;</c>).
    /// </summary>
    public const string Scheme = "malachi-doc";

    /// <summary>
    /// <c>malachi-doc://&lt;view&gt;/</c>: every document of
    /// <paramref name="kind"/> starts with it (the editor bridge installs
    /// itself on nothing else, <see cref="Html.EditorBridge.DocumentUrlPrefix"/>).
    /// </summary>
    public static string Prefix(WebViewKind kind) => Scheme + "://" + kind.ProfileName + "/";

    /// <summary>
    /// <c>malachi-doc://&lt;view&gt;/&lt;generation&gt;-&lt;nonce&gt;</c>, in the
    /// canonical form Chromium reports it in (a lower-case host, digits and
    /// lower-case hex), so the gate can compare it exactly.
    /// </summary>
    public static string For(WebViewKind kind, long generation, string nonce)
    {
        ArgumentNullException.ThrowIfNull(nonce);
        ArgumentOutOfRangeException.ThrowIfNegative(generation);
        return Prefix(kind) + generation.ToString(CultureInfo.InvariantCulture) + "-" + nonce;
    }

    /// <summary>A fresh nonce: 128 random bits as 32 lower-case hex digits.</summary>
    public static string NewNonce() => Convert.ToHexStringLower(RandomNumberGenerator.GetBytes(16));
}
