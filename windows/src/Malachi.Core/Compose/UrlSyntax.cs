// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/URLSyntax.swift; GTK: the slice
// of Go's net/url (url.Parse, Hostname, PathUnescape, ParseQuery) that
// ui/internal/compose/mailto.go, ui/internal/htmlview/links.go and
// ui/internal/signin (BrowserURL) rely on.
//
// Reproduced byte for byte, as the Swift port reproduces it, so that
// ParseMailto, IsMasked and IsBrowserUrl read a URI exactly as the GTK UI
// does. System.Uri is stricter in places (spaces, some escapes) and looser
// in others (it normalises, accepts backslashes, fills in a scheme's
// default authority), and the phishing check must not drift. The input is
// the string's UTF-8 bytes. These are Go 1.25's rules, the GTK module's go
// version, the IPv6 zone of a bracketed host ("[fe80::1%25en0]", RFC 6874)
// included: its escapes may stand for a space or any byte a host may
// contain, but not for a byte beyond ASCII (macOS's URLSyntax decodes a
// zone as the rest of the host, the other way round; GTK is the
// reference). Checked against Go's url.Parse on a corpus of hostile URLs,
// they differ in two ways only. Go 1.26 made bracketed hosts stricter
// whatever the module declares (the literal must be an IPv6 address, with a
// non-empty zone, and a "[" may not appear later in the host), which
// neither the GTK UI built with Go 1.25 nor macOS applies. And decoded bytes
// that are not UTF-8 become U+FFFD here, as Swift and .NET read them,
// where Go keeps the bytes ("https://%80/" has the host "\x80" in Go).

using System;
using System.Collections.Generic;
using System.Text;

namespace Malachi.Core.Compose;

/// <summary>Go's net/url, as far as the clients read URIs.</summary>
internal static class UrlSyntax
{
    /// <summary>
    /// Which component is being unescaped (net/url's encoding); only the
    /// host, the zone of an IPv6 host and a query component have rules of
    /// their own.
    /// </summary>
    public enum Mode
    {
        /// <summary>A path, a path segment, a fragment or userinfo.</summary>
        Path,

        /// <summary>A key or value of a query.</summary>
        QueryComponent,

        /// <summary>A host.</summary>
        Host,

        /// <summary>The zone of a bracketed IPv6 host, from its "%25" to the "]" (encodeZone).</summary>
        Zone,
    }

    /// <summary>What url.Parse makes of the part after the scheme.</summary>
    /// <param name="Host">The host with its port; "" without an authority.</param>
    /// <param name="HasUserinfo">Whether the authority carries userinfo (Go: <c>u.User != nil</c>).</param>
    /// <param name="Opaque">The opaque part of a rootless URI with a scheme; empty otherwise.</param>
    /// <param name="RawQuery">The query without its "?".</param>
    public readonly record struct Rest(string Host, bool HasUserinfo, ReadOnlyMemory<byte> Opaque, ReadOnlyMemory<byte> RawQuery);

    /// <summary>A whole URI as url.Parse reads it (the fields the clients use).</summary>
    /// <param name="Scheme">The scheme, lower-cased; "" without one.</param>
    /// <param name="Rest">What followed it.</param>
    public readonly record struct Parsed(string Scheme, Rest Rest);

    /// <summary>The UTF-8 bytes of a string, as Go holds it.</summary>
    public static byte[] Bytes(string s) => Encoding.UTF8.GetBytes(s);

    /// <summary>
    /// url.Parse: the scheme and the parts the clients use; null where
    /// url.Parse reports an error (a control byte, a colon before any scheme,
    /// a malformed authority or escape, a malformed fragment escape).
    /// </summary>
    public static Parsed? Parse(string raw)
    {
        var bytes = Bytes(raw).AsMemory();
        var (u, fragment, _) = Cut(bytes, (byte)'#');
        if (HasControlByte(u.Span) || Scheme(u) is not { } s || ParseRest(s.Remainder, s.Scheme) is not { } rest)
        {
            return null;
        }
        // setFragment: its escapes must be well-formed.
        if (!fragment.IsEmpty && Unescape(fragment.Span, Mode.Path) is null)
        {
            return null;
        }
        return new Parsed(s.Scheme, rest);
    }

    /// <summary>strings.Cut on bytes: the part before the first <paramref name="sep"/>, after it, and whether it was found.</summary>
    public static (ReadOnlyMemory<byte> Before, ReadOnlyMemory<byte> After, bool Found) Cut(ReadOnlyMemory<byte> b, byte sep)
    {
        var i = b.Span.IndexOf(sep);
        return i < 0 ? (b, ReadOnlyMemory<byte>.Empty, false) : (b[..i], b[(i + 1)..], true);
    }

    /// <summary>stringContainsCTLByte: url.Parse refuses these outright.</summary>
    public static bool HasControlByte(ReadOnlySpan<byte> b)
    {
        foreach (var c in b)
        {
            if (c < 0x20 || c == 0x7f)
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>
    /// getScheme: the scheme (lower-cased, as url.Parse stores it) and the
    /// rest; an empty scheme when the string has none. Null when a colon
    /// comes first, which url.Parse reports as an error.
    /// </summary>
    public static (string Scheme, ReadOnlyMemory<byte> Remainder)? Scheme(ReadOnlyMemory<byte> raw)
    {
        var s = raw.Span;
        for (var i = 0; i < s.Length; i++)
        {
            var c = s[i];
            switch (c)
            {
                case (>= (byte)'a' and <= (byte)'z') or (>= (byte)'A' and <= (byte)'Z'):
                    break;
                case (>= (byte)'0' and <= (byte)'9') or (byte)'+' or (byte)'-' or (byte)'.':
                    if (i == 0)
                    {
                        return ("", raw);
                    }
                    break;
                case (byte)':':
                    if (i == 0)
                    {
                        return null;
                    }
                    return (Encoding.UTF8.GetString(s[..i]).ToLowerInvariant(), raw[(i + 1)..]);
                default:
                    // An invalid character: there is no valid scheme.
                    return ("", raw);
            }
        }
        return ("", raw);
    }

    /// <summary>
    /// unescape: percent-decoding with net/url's checks. Null on a malformed
    /// escape, on an escape a host may not carry (only bytes beyond ASCII,
    /// and "%25") or a zone may not carry (anything but "%25", a space and
    /// the bytes a host may contain), and on a byte a host or zone may not
    /// contain. "+" becomes a space only in a query component.
    /// </summary>
    public static string? Unescape(ReadOnlySpan<byte> s, Mode mode) =>
        UnescapeBytes(s, mode) is { } bytes ? Encoding.UTF8.GetString(bytes) : null;

    // unescape on bytes, which parseHost joins before they are read as UTF-8,
    // as Go joins its strings.
    private static byte[]? UnescapeBytes(ReadOnlySpan<byte> s, Mode mode)
    {
        var n = 0;
        var hasPlus = false;
        var hostLike = mode is Mode.Host or Mode.Zone;
        for (var i = 0; i < s.Length;)
        {
            switch (s[i])
            {
                case (byte)'%':
                    n++;
                    if (i + 2 >= s.Length || !IsHex(s[i + 1]) || !IsHex(s[i + 2]))
                    {
                        return null;
                    }
                    var percent = s[i + 1] == (byte)'2' && s[i + 2] == (byte)'5';
                    if (mode == Mode.Host && Unhex(s[i + 1]) < 8 && !percent)
                    {
                        return null;
                    }
                    if (mode == Mode.Zone)
                    {
                        // RFC 6874 lets a zone escape anything; Go takes only
                        // what could be written unescaped, and a space
                        // (Windows names interfaces with spaces).
                        var v = (byte)((Unhex(s[i + 1]) << 4) | Unhex(s[i + 2]));
                        if (!percent && v != (byte)' ' && HostShouldEscape(v))
                        {
                            return null;
                        }
                    }
                    i += 3;
                    break;
                case (byte)'+':
                    hasPlus = mode == Mode.QueryComponent;
                    i++;
                    break;
                default:
                    if (hostLike && s[i] < 0x80 && HostShouldEscape(s[i]))
                    {
                        return null;
                    }
                    i++;
                    break;
            }
        }
        if (n == 0 && !hasPlus)
        {
            return s.ToArray();
        }
        var output = new List<byte>(s.Length - (2 * n));
        for (var i = 0; i < s.Length;)
        {
            switch (s[i])
            {
                case (byte)'%':
                    output.Add((byte)((Unhex(s[i + 1]) << 4) | Unhex(s[i + 2])));
                    i += 3;
                    break;
                case (byte)'+':
                    output.Add(mode == Mode.QueryComponent ? (byte)' ' : (byte)'+');
                    i++;
                    break;
                default:
                    output.Add(s[i]);
                    i++;
                    break;
            }
        }
        return [.. output];
    }

    /// <summary>
    /// url.ParseQuery as <c>Query()</c> exposes it: the pairs in order,
    /// malformed ones (a bad escape, a semicolon) skipped.
    /// </summary>
    public static IReadOnlyList<(string Key, string Value)> ParseQuery(ReadOnlyMemory<byte> query)
    {
        var output = new List<(string, string)>();
        var rest = query;
        while (!rest.IsEmpty)
        {
            var (pair, after, _) = Cut(rest, (byte)'&');
            rest = after;
            if (pair.IsEmpty || pair.Span.Contains((byte)';'))
            {
                continue;
            }
            var (rawKey, rawValue, _) = Cut(pair, (byte)'=');
            if (Unescape(rawKey.Span, Mode.QueryComponent) is not { } key
                || Unescape(rawValue.Span, Mode.QueryComponent) is not { } value)
            {
                continue;
            }
            output.Add((key, value));
        }
        return output;
    }

    /// <summary>
    /// What url.Parse makes of the part after the scheme: the host ("" without
    /// one; the whole rest is opaque when it has a scheme and no leading
    /// slash), whether it has userinfo, and the query. Null where url.Parse
    /// reports an error.
    /// </summary>
    public static Rest? ParseRest(ReadOnlyMemory<byte> rest0, string scheme)
    {
        var rest = rest0;
        var rawQuery = ReadOnlyMemory<byte>.Empty;
        var span = rest.Span;
        if (span.Length > 0 && span[^1] == (byte)'?' && span.Count((byte)'?') == 1)
        {
            rest = rest[..^1];
        }
        else
        {
            (rest, rawQuery, _) = Cut(rest, (byte)'?');
        }
        span = rest.Span;
        if (span.Length == 0 || span[0] != (byte)'/')
        {
            if (scheme.Length > 0)
            {
                // A rootless path with a scheme is opaque.
                return new Rest("", false, rest, rawQuery);
            }
            if (Cut(rest, (byte)'/').Before.Span.Contains((byte)':'))
            {
                return null;
            }
        }
        var host = "";
        var hasUserinfo = false;
        if ((scheme.Length > 0 || !span.StartsWith("///"u8)) && span.StartsWith("//"u8))
        {
            var authority = rest[2..];
            rest = ReadOnlyMemory<byte>.Empty;
            var slash = authority.Span.IndexOf((byte)'/');
            if (slash >= 0)
            {
                rest = authority[slash..];
                authority = authority[..slash];
            }
            if (ParseAuthority(authority.Span, out hasUserinfo) is not { } h)
            {
                return null;
            }
            host = h;
        }
        // setPath: the path's escapes must be well-formed.
        if (Unescape(rest.Span, Mode.Path) is null)
        {
            return null;
        }
        return new Rest(host, hasUserinfo, ReadOnlyMemory<byte>.Empty, rawQuery);
    }

    /// <summary>
    /// url.Parse(raw).Hostname(): null when Parse fails, "" when the URL has
    /// no host, otherwise the host without its port and brackets.
    /// </summary>
    public static string? Hostname(string raw) =>
        Parse(raw) is { } parsed ? HostnameWithoutPort(parsed.Rest.Host) : null;

    // parseAuthority: the host after the last "@"; the userinfo before it
    // must be valid.
    private static string? ParseAuthority(ReadOnlySpan<byte> authority, out bool hasUserinfo)
    {
        var at = authority.LastIndexOf((byte)'@');
        hasUserinfo = at >= 0;
        var hostPart = at >= 0 ? authority[(at + 1)..] : authority;
        if (ParseHost(hostPart) is not { } host)
        {
            return null;
        }
        if (at >= 0)
        {
            var userinfo = authority[..at];
            if (!ValidUserinfo(userinfo) || Unescape(userinfo, Mode.Path) is null)
            {
                return null;
            }
        }
        return host;
    }

    // parseHost: a bracketed literal must close and carry a valid port, and
    // the first "%25" before its last "]" starts a zone, unescaped by the
    // zone's rules between the host's; otherwise anything after the last
    // colon must be a valid port.
    private static string? ParseHost(ReadOnlySpan<byte> host)
    {
        if (host.Length > 0 && host[0] == (byte)'[')
        {
            var close = host.LastIndexOf((byte)']');
            if (close < 0 || !ValidOptionalPort(host[(close + 1)..]))
            {
                return null;
            }
            var zone = host[..close].IndexOf("%25"u8);
            if (zone >= 0)
            {
                if (UnescapeBytes(host[..zone], Mode.Host) is not { } address
                    || UnescapeBytes(host[zone..close], Mode.Zone) is not { } zoneId
                    || UnescapeBytes(host[close..], Mode.Host) is not { } port)
                {
                    return null;
                }
                return Encoding.UTF8.GetString([.. address, .. zoneId, .. port]);
            }
        }
        else
        {
            var colon = host.LastIndexOf((byte)':');
            if (colon >= 0 && !ValidOptionalPort(host[colon..]))
            {
                return null;
            }
        }
        return Unescape(host, Mode.Host);
    }

    // URL.Hostname: splitHostPort, then the brackets of an IPv6 literal.
    private static string HostnameWithoutPort(string host)
    {
        ReadOnlySpan<byte> h = Encoding.UTF8.GetBytes(host);
        var colon = h.LastIndexOf((byte)':');
        if (colon >= 0 && ValidOptionalPort(h[colon..]))
        {
            h = h[..colon];
        }
        if (h.Length >= 2 && h[0] == (byte)'[' && h[^1] == (byte)']')
        {
            h = h[1..^1];
        }
        return Encoding.UTF8.GetString(h);
    }

    // validOptionalPort: empty, or a colon followed by digits only.
    private static bool ValidOptionalPort(ReadOnlySpan<byte> port)
    {
        if (port.IsEmpty)
        {
            return true;
        }
        if (port[0] != (byte)':')
        {
            return false;
        }
        foreach (var c in port[1..])
        {
            if (c is < (byte)'0' or > (byte)'9')
            {
                return false;
            }
        }
        return true;
    }

    private static bool ValidUserinfo(ReadOnlySpan<byte> s)
    {
        foreach (var c in s)
        {
            if (IsAlnum(c))
            {
                continue;
            }
            switch (c)
            {
                case (byte)'-' or (byte)'.' or (byte)'_' or (byte)':' or (byte)'~' or (byte)'!' or (byte)'$'
                    or (byte)'&' or (byte)'\'' or (byte)'(' or (byte)')' or (byte)'*' or (byte)'+' or (byte)','
                    or (byte)';' or (byte)'=' or (byte)'%' or (byte)'@':
                    continue;
                default:
                    return false;
            }
        }
        return true;
    }

    // shouldEscape for a host: alphanumerics, the sub-delims plus :[]<>",
    // and -_.~ may appear; everything else may not.
    private static bool HostShouldEscape(byte c)
    {
        if (IsAlnum(c))
        {
            return false;
        }
        return c switch
        {
            (byte)'!' or (byte)'$' or (byte)'&' or (byte)'\'' or (byte)'(' or (byte)')' or (byte)'*' or (byte)'+'
                or (byte)',' or (byte)';' or (byte)'=' or (byte)':' or (byte)'[' or (byte)']' or (byte)'<' or (byte)'>'
                or (byte)'"' => false,
            (byte)'-' or (byte)'_' or (byte)'.' or (byte)'~' => false,
            _ => true,
        };
    }

    private static bool IsAlnum(byte c) => c is (>= (byte)'a' and <= (byte)'z') or (>= (byte)'A' and <= (byte)'Z') or (>= (byte)'0' and <= (byte)'9');

    private static bool IsHex(byte c) => c is (>= (byte)'0' and <= (byte)'9') or (>= (byte)'a' and <= (byte)'f') or (>= (byte)'A' and <= (byte)'F');

    private static int Unhex(byte c) => c switch
    {
        >= (byte)'0' and <= (byte)'9' => c - '0',
        >= (byte)'a' and <= (byte)'f' => c - 'a' + 10,
        >= (byte)'A' and <= (byte)'F' => c - 'A' + 10,
        _ => 0,
    };
}
