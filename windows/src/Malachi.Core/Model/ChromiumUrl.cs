// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only, no Swift or Go counterpart: the canonical form WebView2
// (Chromium's URL canonicaliser, the WHATWG URL standard for http and
// https) gives a link's href, for LinkDecision to find the link a
// navigation came from when the href attribute itself could not be read
// (docs/windows-port.md §6.3, §6.4). macOS compares the attribute as
// written; WebKit's resolved URL rarely equals it, and neither does
// Chromium's.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace Malachi.Core.Model;

/// <summary>
/// The address Chromium navigates to for an http or https href, computed
/// only where the result is certain. Every step is one Chromium and the URL
/// standard agree on: the scheme and an ASCII host in lower case, an
/// internationalised host in punycode, the default port dropped, an empty
/// path made <c>/</c>, backslashes read as slashes, <c>.</c> and <c>..</c>
/// resolved, and space, <c>"</c>, <c>&lt;</c>, <c>&gt;</c> and every
/// non-ASCII character of the path and query percent-encoded as UTF-8. Where
/// the engines have differed, or the rules are subtle (user information,
/// IPv6 and numeric hosts, percent-escapes of unreserved characters or in
/// lower case, the characters some versions escape and others do not, the
/// deviation characters of IDNA), the answer is null: no address rather
/// than a guess, so that a comparison never pairs two links Chromium keeps
/// apart.
/// </summary>
public static class ChromiumUrl
{
    // RFC 3986 unreserved characters and sub-delimiters, ":" and "@": what
    // every engine leaves as it is in a path.
    private const string PathPass = "-._~!$&'()*+,;=:@";

    // In a query and a fragment "/" and "?" too; "'" is left out, which the
    // URL standard escapes in the query of http and https and Chromium has
    // not always.
    private const string QueryPass = "-._~!$&()*+,;=:@/?";

    /// <summary>
    /// The canonical form of <paramref name="url"/>, an absolute http or
    /// https address, as Chromium resolves it in a document; null when
    /// <paramref name="url"/> is anything else, or where the result is not
    /// certain.
    /// </summary>
    public static string? Canonicalize(string? url)
    {
        var s = Stripped(url);
        if (s is null)
        {
            return null;
        }
        var colon = s.IndexOf(':', StringComparison.Ordinal);
        if (colon <= 0)
        {
            return null;
        }
        var scheme = s[..colon].ToLowerInvariant();
        var defaultPort = scheme switch
        {
            "http" => 80,
            "https" => 443,
            _ => -1,
        };
        if (defaultPort < 0)
        {
            return null;
        }
        // A special scheme takes any number of slashes and backslashes
        // before the authority ("https:host", "https:\\\\host").
        var i = colon + 1;
        while (i < s.Length && s[i] is '/' or '\\')
        {
            i++;
        }
        var authorityEnd = s.IndexOfAny(['/', '\\', '?', '#'], i);
        if (authorityEnd < 0)
        {
            authorityEnd = s.Length;
        }
        var authority = Authority(s[i..authorityEnd], defaultPort);
        if (authority is null)
        {
            return null;
        }

        var rest = s[authorityEnd..];
        string? fragment = null;
        var hash = rest.IndexOf('#', StringComparison.Ordinal);
        if (hash >= 0)
        {
            fragment = rest[(hash + 1)..];
            rest = rest[..hash];
        }
        string? query = null;
        var question = rest.IndexOf('?', StringComparison.Ordinal);
        if (question >= 0)
        {
            query = rest[(question + 1)..];
            rest = rest[..question];
        }

        var output = new StringBuilder(s.Length + 8);
        output.Append(scheme).Append("://").Append(authority);
        if (!Path(rest, output))
        {
            return null;
        }
        if (query is not null && !Escaped(query, QueryPass, output.Append('?')))
        {
            return null;
        }
        if (fragment is not null && !Escaped(fragment, QueryPass, output.Append('#'), escape: false))
        {
            return null;
        }
        return output.ToString();
    }

    // The value as an HTML URL parser reads it: surrounding C0 controls and
    // spaces removed, tab, LF and CR removed anywhere. Another control
    // character is refused (the sanitiser refuses it too).
    private static string? Stripped(string? url)
    {
        if (url is null)
        {
            return null;
        }
        var start = 0;
        var end = url.Length;
        while (start < end && url[start] <= ' ')
        {
            start++;
        }
        while (end > start && url[end - 1] <= ' ')
        {
            end--;
        }
        var b = new StringBuilder(end - start);
        for (var i = start; i < end; i++)
        {
            var c = url[i];
            if (c is '\t' or '\n' or '\r')
            {
                continue;
            }
            if (c < ' ' || c == '\x7F')
            {
                return null;
            }
            b.Append(c);
        }
        return b.ToString();
    }

    // host[:port] with the port dropped when it is the scheme's default;
    // null for user information, an IPv6 literal, an empty or out-of-range
    // port, or a host that is not certain.
    private static string? Authority(string authority, int defaultPort)
    {
        if (authority.Length == 0 || authority.Contains('@', StringComparison.Ordinal) || authority[0] == '[')
        {
            return null;
        }
        var hostPart = authority;
        var port = "";
        var colon = authority.LastIndexOf(':');
        if (colon >= 0)
        {
            hostPart = authority[..colon];
            var digits = authority[(colon + 1)..];
            if (digits.Length == 0 || digits.AsSpan().ContainsAnyExceptInRange('0', '9'))
            {
                return null;
            }
            digits = digits.TrimStart('0');
            if (digits.Length > 5)
            {
                return null;
            }
            var n = digits.Length == 0 ? 0 : int.Parse(digits, NumberStyles.None, CultureInfo.InvariantCulture);
            if (n > 65535)
            {
                return null;
            }
            if (n != defaultPort)
            {
                port = ":" + n.ToString(CultureInfo.InvariantCulture);
            }
        }
        var host = Host(hostPart);
        return host is null ? null : host + port;
    }

    // The host in lower case, an internationalised one in punycode. Labels
    // of letters, digits and hyphens only, none empty but for the one a
    // trailing dot leaves; a host that ends in a number is an IPv4 address,
    // kept only in its canonical dotted form.
    private static string? Host(string host)
    {
        if (host.Length == 0)
        {
            return null;
        }
        string ascii;
        if (Ascii(host))
        {
            ascii = host.ToLowerInvariant();
        }
        else
        {
            // ß, ς, ZWNJ and ZWJ map differently under transitional and
            // nontransitional processing; which one the platform's
            // IdnMapping applies is not certain.
            if (host.AsSpan().IndexOfAny("ßς‌‍") >= 0)
            {
                return null;
            }
            try
            {
                // A mapping per call: its instance members are not
                // documented as thread-safe.
                ascii = new IdnMapping { AllowUnassigned = false, UseStd3AsciiRules = false }.GetAscii(host).ToLowerInvariant();
            }
            catch (ArgumentException)
            {
                return null;
            }
        }
        var labels = ascii.Split('.');
        var count = labels.Length;
        if (count > 1 && labels[^1].Length == 0)
        {
            count--; // the trailing dot
        }
        for (var i = 0; i < count; i++)
        {
            var label = labels[i];
            if (label.Length == 0)
            {
                return null;
            }
            foreach (var c in label)
            {
                if (!(char.IsAsciiLetterLower(c) || char.IsAsciiDigit(c) || c == '-'))
                {
                    return null;
                }
            }
        }
        var last = labels[count - 1];
        if (!last.AsSpan().ContainsAnyExceptInRange('0', '9') || last.StartsWith("0x", StringComparison.Ordinal))
        {
            return CanonicalIPv4(labels, count) ? ascii : null;
        }
        return ascii;
    }

    // Four decimal numbers from 0 to 255 without leading zeros and without a
    // trailing dot: the only IPv4 form Chromium leaves as it is.
    private static bool CanonicalIPv4(string[] labels, int count)
    {
        if (count != 4 || labels.Length != 4)
        {
            return false;
        }
        foreach (var label in labels)
        {
            if (label.Length is 0 or > 3 || label.AsSpan().ContainsAnyExceptInRange('0', '9') || (label.Length > 1 && label[0] == '0')
                || int.Parse(label, NumberStyles.None, CultureInfo.InvariantCulture) > 255)
            {
                return false;
            }
        }
        return true;
    }

    // The path, from the separator after the authority: "/" when empty,
    // backslashes as slashes, "." and ".." resolved, each segment escaped.
    private static bool Path(string path, StringBuilder output)
    {
        if (path.Length == 0)
        {
            output.Append('/');
            return true;
        }
        var segments = path[1..].Split(['/', '\\']);
        var kept = new List<string>(segments.Length);
        for (var i = 0; i < segments.Length; i++)
        {
            var segment = segments[i];
            var last = i == segments.Length - 1;
            if (segment is "." or "..")
            {
                if (segment == ".." && kept.Count > 0)
                {
                    kept.RemoveAt(kept.Count - 1);
                }
                if (last)
                {
                    kept.Add("");
                }
                continue;
            }
            var escaped = new StringBuilder(segment.Length);
            if (!Escaped(segment, PathPass, escaped))
            {
                return false;
            }
            kept.Add(escaped.ToString());
        }
        output.Append('/').AppendJoin('/', kept);
        return true;
    }

    // Appends s with space, '"', '<' and '>' percent-encoded, and every
    // non-ASCII character as its UTF-8 bytes, when escape; false for any
    // other character outside pass (for all of them without escape: the
    // fragment, whose escaping Chromium changed over time), a lone
    // surrogate, or a percent-escape that is not certain to stay (lower-case
    // hex, an unreserved character, a '%' without two hex digits).
    private static bool Escaped(string s, string pass, StringBuilder output, bool escape = true)
    {
        Span<byte> utf8 = stackalloc byte[4];
        for (var i = 0; i < s.Length; i++)
        {
            var c = s[i];
            if (char.IsAsciiLetterOrDigit(c) || pass.Contains(c, StringComparison.Ordinal))
            {
                output.Append(c);
                continue;
            }
            if (c != '%' && !escape)
            {
                return false;
            }
            switch (c)
            {
                case ' ':
                    output.Append("%20");
                    continue;
                case '"':
                    output.Append("%22");
                    continue;
                case '<':
                    output.Append("%3C");
                    continue;
                case '>':
                    output.Append("%3E");
                    continue;
                case '%':
                    if (i + 2 >= s.Length || !UpperHex(s[i + 1]) || !UpperHex(s[i + 2])
                        || Unreserved((char)byte.Parse(s.AsSpan(i + 1, 2), NumberStyles.HexNumber, CultureInfo.InvariantCulture)))
                    {
                        return false;
                    }
                    output.Append(s, i, 3);
                    i += 2;
                    continue;
            }
            if (c < 0x80 || Rune.DecodeFromUtf16(s.AsSpan(i), out var rune, out var consumed) != System.Buffers.OperationStatus.Done)
            {
                return false;
            }
            var n = rune.EncodeToUtf8(utf8);
            foreach (var b in utf8[..n])
            {
                output.Append('%').Append(b.ToString("X2", CultureInfo.InvariantCulture));
            }
            i += consumed - 1;
        }
        return true;
    }

    private static bool UpperHex(char c) => char.IsAsciiDigit(c) || c is >= 'A' and <= 'F';

    private static bool Unreserved(char c) => char.IsAsciiLetterOrDigit(c) || c is '-' or '.' or '_' or '~';

    private static bool Ascii(string s)
    {
        foreach (var c in s)
        {
            if (c >= 0x80)
            {
                return false;
            }
        }
        return true;
    }
}
