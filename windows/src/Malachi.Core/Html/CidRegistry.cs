// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/CIDRegistry.swift and of
// macos/Sources/MalachiMail/WebViews/CIDSchemeHandler.swift (id(of:)); GTK:
// ui/internal/editor/cid.go (maxCIDBytes, fetchTimeout, RegisterCID,
// RegisterCIDFetcher, CIDRegistered, UnregisterCID, lookupCID, checkInline,
// serveCID's id).
//
// The pure part of the cid: scheme of the compose editor: the registry, the
// id a request names and the gate every served image passes. Serving a
// request (reading the file, calling the fetcher under the timeout,
// answering WebResourceRequested) is the WebView2 layer's. Swift keeps the
// registry on the main actor; Go guards it with a mutex, and so does this
// one, so the scheme handler may ask from any thread.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Html;

/// <summary>
/// The <c>cid:</c> scheme serves the inline images of drafts being composed:
/// files the window itself picked, and copies the daemon made of a quoted
/// original's pictures, which it hands over through <c>attachment.get</c>.
/// Only ids registered here are served, so a pasted
/// <c>&lt;img src="cid:../../etc/passwd"&gt;</c> yields an error. The message
/// viewer uses another scheme (<c>malachi-cid:</c>), so a displayed message
/// can never address compose attachments. Go keeps one registry per process;
/// <see cref="Shared"/> is that one.
/// </summary>
public sealed class CidRegistry
{
    /// <summary>
    /// editor.maxCIDBytes: caps what the <c>cid:</c> handler serves; inline
    /// images are attachments and share their limit.
    /// </summary>
    public const int MaxCidBytes = API.Limits.MaxAttachmentBytes;

    private readonly Dictionary<string, CidEntry> files = new(StringComparer.Ordinal);
    private readonly object gate = new();

    /// <summary>
    /// editor.fetchTimeout: bounds one fetcher call: the daemon reads a file
    /// of the attachment store, which is quick, but the request must not hang
    /// the view's image forever when the daemon is gone.
    /// </summary>
    public static TimeSpan FetchTimeout { get; } = TimeSpan.FromSeconds(60);

    /// <summary>The process-wide registry every editor shares.</summary>
    public static CidRegistry Shared { get; } = new();

    /// <summary>editor.RegisterCID: makes <c>cid:&lt;id&gt;</c> resolve to the file at <paramref name="path"/>.</summary>
    public void Register(string id, string path, string contentType)
    {
        ArgumentNullException.ThrowIfNull(id);
        lock (gate)
        {
            files[id] = new CidEntry.File(path, contentType);
        }
    }

    /// <summary>editor.RegisterCIDFetcher: makes <c>cid:&lt;id&gt;</c> resolve to what <paramref name="fetch"/> returns.</summary>
    public void RegisterFetcher(string id, CidFetcher fetch)
    {
        ArgumentNullException.ThrowIfNull(id);
        ArgumentNullException.ThrowIfNull(fetch);
        lock (gate)
        {
            files[id] = new CidEntry.Fetcher(fetch);
        }
    }

    /// <summary>editor.CIDRegistered: whether <paramref name="id"/> resolves to anything.</summary>
    public bool IsRegistered(string id) => Lookup(id) is not null;

    /// <summary>editor.UnregisterCID: forgets <paramref name="id"/>; an unknown id is not an error.</summary>
    public void Unregister(string id)
    {
        ArgumentNullException.ThrowIfNull(id);
        lock (gate)
        {
            files.Remove(id);
        }
    }

    /// <summary>editor.lookupCID: the entry of <paramref name="id"/>, matched exactly as registered.</summary>
    public CidEntry? Lookup(string id)
    {
        ArgumentNullException.ThrowIfNull(id);
        lock (gate)
        {
            return files.GetValueOrDefault(id);
        }
    }

    /// <summary>
    /// The id a <c>cid:</c> request names (macOS CIDSchemeHandler.id(of:),
    /// GTK serveCID): the path of the request's URI as it stands, escapes
    /// kept (past an authority, without the query and fragment); when it has
    /// none, everything after <c>cid:</c>. The id is only ever looked up,
    /// matched exactly as registered, so a hostile one resolves to nothing.
    /// </summary>
    public static string IdOf(string requestUri)
    {
        ArgumentNullException.ThrowIfNull(requestUri);
        var path = requestUri.AsSpan(SchemeLength(requestUri));
        var end = path.IndexOfAny('?', '#');
        if (end >= 0)
        {
            path = path[..end];
        }
        if (path.StartsWith("//", StringComparison.Ordinal))
        {
            var slash = path[2..].IndexOf('/');
            path = slash < 0 ? [] : path[(slash + 2)..];
        }
        if (!path.IsEmpty)
        {
            return path.ToString();
        }
        const string prefix = "cid:";
        return requestUri.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) ? requestUri[prefix.Length..] : requestUri;
    }

    // The length of the URI's scheme with its colon (RFC 3986: a letter,
    // then letters, digits, "+", "-" and "."); 0 without one.
    private static int SchemeLength(string uri)
    {
        for (var i = 0; i < uri.Length; i++)
        {
            var c = uri[i];
            if (char.IsAsciiLetter(c) || (i > 0 && (char.IsAsciiDigit(c) || c is '+' or '-' or '.')))
            {
                continue;
            }
            return c == ':' && i > 0 ? i + 1 : 0;
        }
        return 0;
    }

    /// <summary>
    /// editor.checkInline: the gate every served image passes: bytes present,
    /// within the cap, of a picture type the view may render (never SVG,
    /// which can script). Parameters are stripped and the type lower-cased.
    /// </summary>
    /// <exception cref="InlineImageException">The picture is refused.</exception>
    public static void CheckInline(ReadOnlySpan<byte> data, string contentType)
    {
        ArgumentNullException.ThrowIfNull(contentType);
        if (data.IsEmpty)
        {
            throw new InlineImageException(InlineImageError.Empty);
        }
        if (data.Length > MaxCidBytes)
        {
            throw new InlineImageException(InlineImageError.TooBig);
        }
        if (!PartPath.IsImageType(contentType))
        {
            throw new InlineImageException(InlineImageError.NotAPicture);
        }
    }
}
