// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/Links.swift; GTK:
// ui/internal/htmlview/links.go (AllowedLink, Masked, hostOfText,
// looksLikeHost, sameSite).
//
// Pure helpers around links, testable without a view. Ordinal (byte)
// semantics for an href, as in Go: a combining mark after a separator must
// not change what a prefix check sees. URLs are read by the port of Go's
// net/url (UrlSyntax), never by System.Uri.
//
// Stricter than GTK (windows/README.md): IsMasked fails closed where Go's
// parser finds no host or finds userinfo, because the browser reads such an
// href its own way (Chromium and System.Uri accept a userinfo with a space,
// "%", "[" or a soft hyphen that Go refuses, and go to the host after the
// "@"); and LeadsElsewhere judges the address the launcher really opens.
//
// The text side fails closed as well (HostsOfText, where GTK has
// hostOfText). GTK reads "" (no address, so the link opens) for many texts
// a reader takes for the bank's address: one with a soft hyphen, a
// zero-width character or a bidi control in its host, a trailing dot, a
// Cyrillic letter, a space the daemon put around an inline element
// ("https://www.moje banka .example"), a backslash or a fullwidth slash
// before the path, and it takes the host after the "@" of a text with
// userinfo. Here the text is read as it is seen: what is invisible goes,
// the compatibility form (NFKC) reads fullwidth letters, dots and slashes
// as ASCII, an internationalised host is compared in punycode (as the
// launcher hands it to the browser), a trailing dot is no part of a site;
// a text that reads as an address but whose host cannot be read names a
// host no link leads to; and every address in the text counts, not only
// one that is the whole text. The text is compared, never shown: the
// question quotes it as the mail wrote it.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
using Malachi.Core.Compose;

namespace Malachi.Core.Html;

/// <summary>What a link of a message may do, and whether its text lies about it.</summary>
public static class Links
{
    // What a reader takes for a slash between a scheme and a host, or
    // before a path, besides "/" and "\" and the fullwidth and small forms
    // that NFKC turns into them: fraction slash, division slash, set minus,
    // the box-drawing and mathematical diagonals, reverse solidus operator,
    // big solidus and big reverse solidus.
    private const string SlashLookalikes = "\u2044\u2215\u2216\u2571\u2572\u27CB\u27CD\u29F5\u29F8\u29F9";

    // What a reader takes for the colon after a scheme that NFKC leaves
    // alone: modifier letter triangular colon, raised colon, Armenian full
    // stop, Hebrew sof pasuq, two dot punctuation, ratio, Latin colon.
    private const string ColonLookalikes = "\u02D0\u02F8\u0589\u05C3\u205A\u2236\uA789";

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
    /// is never masked, nor is a <c>mailto:</c> link, which goes to the
    /// composer. Unlike GTK, which then opens, an href whose host Go's
    /// parser cannot tell (an error) or that has none is masked under such
    /// a text, and so is one that carries userinfo: what Go cannot read,
    /// the browser still reads, to the host after the last "@"
    /// ("https:// bank.example@evil.example/"), and a host spelled in the
    /// userinfo is itself the disguise. The text is read by
    /// <see cref="HostsOfText"/>: every host it names must be on the
    /// href's site, and one it names but that cannot be read never is.
    /// </summary>
    public static bool IsMasked(string text, string href)
    {
        ArgumentNullException.ThrowIfNull(text);
        ArgumentNullException.ThrowIfNull(href);
        var shown = HostsOfText(text);
        if (shown.Count == 0)
        {
            return false;
        }
        var trimmed = href.Trim();
        // Lowered as AllowedLink lowers it, so that what is a mailto: link
        // here is one for the decision too.
        if (trimmed.ToLowerInvariant().StartsWith("mailto:", StringComparison.Ordinal))
        {
            return false;
        }
        if (UrlSyntax.Parse(trimmed) is not { } parsed || parsed.Rest.HasUserinfo)
        {
            return true;
        }
        return !AllOnSite(shown, AsciiHost(UrlSyntax.Hostname(trimmed)));
    }

    /// <summary>
    /// Windows: whether a link's visible text names a site other than the
    /// one <paramref name="target"/> leads to, where
    /// <paramref name="target"/> is the address the launcher hands the
    /// browser for the link (<c>ILauncher.LinkTarget</c>: escaped, the host
    /// as DNS gets it, no userinfo), null when it refuses the link. This
    /// judges what is opened rather than how Go reads the href, so a
    /// parser that reads the href otherwise cannot open a link without
    /// the question. A target that is refused or has no host leads
    /// nowhere the text could name; text that is not an address never
    /// leads elsewhere (<see cref="HostsOfText"/>).
    /// </summary>
    public static bool LeadsElsewhere(string text, string? target)
    {
        ArgumentNullException.ThrowIfNull(text);
        var shown = HostsOfText(text);
        if (shown.Count == 0)
        {
            return false;
        }
        return !AllOnSite(shown, AsciiHost(target is null ? null : UrlSyntax.Hostname(target)));
    }

    /// <summary>
    /// htmlview.hostOfText as a reader sees the text (Windows): the hosts
    /// the text names, empty when it names none. Every address in the text
    /// counts: one that begins with a scheme, its colon and a slash (http
    /// and https need no slash, as Chromium reads them without), with two
    /// slashes or with "www.", at the start or after anything but a letter
    /// or a digit ("Log in at https://…", "&lt;https://…&gt;",
    /// "Login:https://…"); and, when there is none, the whole text as a bare
    /// host, as in GTK (a dot and an alphabetic top-level label, no space,
    /// no "@"). A host is read up to its path, query or fragment, to a
    /// punctuation mark or symbol, and to the next space, except in an
    /// address the text begins with, which must be one whole: a space
    /// inside its host (the daemon puts one around each inline element of a
    /// link's text: "https://www.moje banka .example/login"), userinfo, an
    /// escape, a port that is no number, or a host that
    /// <see cref="LooksLikeHost"/> refuses make it "", a host no link leads
    /// to. A host comes in ASCII (punycode), lower case, without a trailing
    /// dot. Before any of it the text loses what a reader does not see (the
    /// format characters, among them the soft hyphen, the zero-width
    /// characters, the word joiner, the byte order mark and the bidi
    /// controls; the other default-ignorable code points; the control
    /// characters), and is read in its compatibility form (NFKC: fullwidth
    /// letters, dots, colons and slashes as ASCII) with the ideographic full
    /// stop as a dot.
    /// </summary>
    public static IReadOnlyList<string> HostsOfText(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var t = Readable(text);
        var hosts = new List<string>();
        var p = 0;
        while (p < t.Length)
        {
            var start = p > 0 && IsWordChar(t[p - 1]) ? -1 : AddressHost(t, p);
            if (start < 0)
            {
                p++;
                continue;
            }
            var (host, end) = ReadHost(t, start, BeginsText(t, p));
            hosts.Add(host);
            // The rest of the address, its path, names no more hosts.
            p = end;
            while (p < t.Length && !char.IsWhiteSpace(t[p]))
            {
                p++;
            }
        }
        if (hosts.Count == 0 && BareHost(t) is { } bare)
        {
            hosts.Add(bare);
        }
        return hosts;
    }

    /// <summary>
    /// htmlview.looksLikeHost: at least two non-empty labels of
    /// <c>[a-z0-9-]</c>, the last one alphabetic and at least two long, or
    /// (Windows, for an internationalised host read in punycode) a
    /// top-level label of punycode ("xn--p1ai").
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
        if (tld.StartsWith("xn--", StringComparison.Ordinal))
        {
            return tld.Length > "xn--".Length;
        }
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

    /// <summary>
    /// htmlview.sameSite: a host and its subdomains are one site, www.
    /// aside. Windows: a trailing dot is no part of a host
    /// ("www.bank.example." is "www.bank.example"), an empty host is no
    /// site, and a host of a single label (a top-level domain such as "cz",
    /// an intranet name) is the site of no host under it, so that
    /// "www.mojebanka.cz" over a link to "https://cz/" is asked about, where
    /// GTK opens it. Parent domains of more labels are still one site with
    /// their subdomains: without a public-suffix list "co.uk" is not known
    /// for what it is.
    /// </summary>
    public static bool SameSite(string a, string b)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(b);
        a = TrimPrefix(TrimDot(a), "www.");
        b = TrimPrefix(TrimDot(b), "www.");
        if (a.Length == 0 || b.Length == 0)
        {
            return false;
        }
        return string.Equals(a, b, StringComparison.Ordinal) || IsParent(b, a) || IsParent(a, b);
    }

    // Whether parent is a parent domain of child; a single label never is.
    private static bool IsParent(string parent, string child) =>
        parent.Contains('.', StringComparison.Ordinal) && child.EndsWith("." + parent, StringComparison.Ordinal);

    // Whether every host a text names is on real's site: a host that
    // cannot be read ("") never is, and nothing is on no host.
    private static bool AllOnSite(IReadOnlyList<string> shown, string real)
    {
        if (real.Length == 0)
        {
            return false;
        }
        foreach (var host in shown)
        {
            if (host.Length == 0 || !SameSite(host, real))
            {
                return false;
            }
        }
        return true;
    }

    // The text as a reader sees it, for reading hosts: without what is not
    // seen, in its compatibility form, the ideographic full stop (to which
    // NFKC also turns the halfwidth one) as a dot, lower case, trimmed.
    // What EnumerateRunes makes of a lone surrogate is U+FFFD, so the
    // normalisation has nothing invalid to refuse.
    private static string Readable(string text)
    {
        var b = new StringBuilder(text.Length);
        foreach (var r in text.EnumerateRunes())
        {
            if (!Invisible(r))
            {
                b.Append(r.ToString());
            }
        }
        var s = b.ToString();
        try
        {
            s = s.Normalize(NormalizationForm.FormKC);
        }
        catch (ArgumentException)
        {
            // Not normalised: read as it is.
        }
        return s.Replace('\u3002', '.').ToLowerInvariant().Trim();
    }

    // What a reader does not see: a format character (Cf: the soft hyphen,
    // the zero-width space and joiners, the word joiner, the byte order
    // mark, the bidi marks, embeddings, overrides and isolates), a control
    // character other than white space, and the other default-ignorable
    // code points (the combining grapheme joiner, the Hangul fillers, the
    // Khmer inherent vowels, the Mongolian variation selectors and vowel
    // separator, the variation selectors, U+2065 and U+FFF0 to U+FFF8, the
    // tags block).
    private static bool Invisible(Rune r)
    {
        switch (Rune.GetUnicodeCategory(r))
        {
            case UnicodeCategory.Format:
                return true;
            case UnicodeCategory.Control:
                return !Rune.IsWhiteSpace(r);
            default:
                break;
        }
        return r.Value is 0x034F or 0x115F or 0x1160 or 0x17B4 or 0x17B5 or (>= 0x180B and <= 0x180F) or (>= 0x2060 and <= 0x206F)
            or 0x3164 or (>= 0xFE00 and <= 0xFE0F) or 0xFFA0 or (>= 0xFFF0 and <= 0xFFF8) or (>= 0xE0000 and <= 0xE0FFF);
    }

    // Where the host of an address that starts at p begins, or -1 when no
    // address starts there: "www." (the host begins with it), two slashes,
    // or a scheme (a letter, then letters, digits, marks, "+", "-" or ".")
    // with its colon and a slash, or http and https with their colon
    // alone; the slashes are skipped.
    private static int AddressHost(string t, int p)
    {
        if (t.AsSpan(p).StartsWith("www.", StringComparison.Ordinal))
        {
            return p;
        }
        if (p + 1 < t.Length && IsSlash(t[p]) && IsSlash(t[p + 1]))
        {
            return SkipSlashes(t, p);
        }
        if (!char.IsLetter(t[p]))
        {
            return -1;
        }
        var colon = p + 1;
        while (colon < t.Length && (char.IsLetterOrDigit(t[colon]) || IsMark(t[colon]) || t[colon] is '+' or '-' or '.'))
        {
            colon++;
        }
        if (colon == t.Length || !IsColon(t[colon]))
        {
            return -1;
        }
        var after = colon + 1;
        var scheme = t.AsSpan(p, colon - p);
        if ((after < t.Length && IsSlash(t[after])) || scheme.SequenceEqual("http") || scheme.SequenceEqual("https"))
        {
            return SkipSlashes(t, after);
        }
        return -1;
    }

    // The host of an address in t, which begins at start, and where it
    // ends; "" when it cannot be read. In an address the text begins with
    // (whole), a space does not end the host but makes it unreadable.
    private static (string Host, int End) ReadHost(string t, int start, bool whole)
    {
        var end = start;
        while (end < t.Length && !EndsHost(t[end], whole))
        {
            end++;
        }
        var part = t.AsSpan(start, end - start).Trim();
        foreach (var c in part)
        {
            if (char.IsWhiteSpace(c) || c == '@')
            {
                return ("", end); // a space inside, or userinfo
            }
        }
        var colon = part.LastIndexOf(':');
        if (colon >= 0)
        {
            if (part[(colon + 1)..].ContainsAnyExceptInRange('0', '9'))
            {
                return ("", end); // a port that is no number
            }
            part = part[..colon];
        }
        var host = AsciiHost(part.ToString());
        return (host.Length > 0 && LooksLikeHost(host) ? host : "", end);
    }

    // Whether c ends the host of an address in a link's text: a space
    // (unless the text begins with the address), a slash, "?" or "#", the
    // ASCII punctuation a host, its port and userinfo do not carry, and
    // any other punctuation mark, symbol or separator. What stays in the
    // host and is not a host's ("_", "~", "%", "@", a colon that is not a
    // port's) makes it unreadable.
    private static bool EndsHost(char c, bool whole)
    {
        if (char.IsWhiteSpace(c))
        {
            return !whole;
        }
        if (IsSlash(c) || c is '?' or '#')
        {
            return true;
        }
        if (char.IsAscii(c))
        {
            return !(char.IsAsciiLetterOrDigit(c) || c is '-' or '.' or '_' or '~' or '%' or '@' or ':');
        }
        return !(char.IsLetterOrDigit(c) || char.IsNumber(c) || IsMark(c) || char.IsSurrogate(c));
    }

    // GTK's reading of a text that holds no address: the whole text as a
    // host ("bank.example.org", "mojebanka.example/login"), when it has no
    // space and no "@" (an e-mail address), from its first letter or digit
    // on; null when it names none.
    private static string? BareHost(string t)
    {
        if (t.Length == 0 || t.Contains('@', StringComparison.Ordinal))
        {
            return null;
        }
        foreach (var c in t)
        {
            if (char.IsWhiteSpace(c))
            {
                return null;
            }
        }
        var start = 0;
        while (start < t.Length && !IsWordChar(t[start]))
        {
            start++;
        }
        var (host, _) = ReadHost(t, start, whole: false);
        return host.Length > 0 ? host : null;
    }

    // A host as DNS gets it, which is how the launcher hands it to the
    // browser (System.Uri.IdnHost; Launcher.WebLinkTarget): lower case,
    // an internationalised one in punycode by IDNA (IdnMapping, which also
    // maps the ideographic full stop to a dot and fullwidth letters to
    // ASCII), without a trailing dot; "" for none, or for one IDNA refuses.
    private static string AsciiHost(string? host)
    {
        var h = TrimDot((host ?? "").ToLowerInvariant());
        if (h.Length == 0 || Ascii.IsValid(h))
        {
            return h;
        }
        try
        {
            // A mapping per call: its instance members are not documented
            // as thread-safe.
            return TrimDot(new IdnMapping { AllowUnassigned = false, UseStd3AsciiRules = false }.GetAscii(h).ToLowerInvariant());
        }
        catch (ArgumentException)
        {
            return "";
        }
    }

    // Whether c, right before what reads as an address, makes that part of
    // a word ("xhttps://…", "mywww.…") rather than an address of its own.
    private static bool IsWordChar(char c) => char.IsLetterOrDigit(c) || IsMark(c);

    // Whether the text begins with the address at p: nothing but spaces,
    // punctuation and symbols come before it.
    private static bool BeginsText(string t, int p)
    {
        for (var i = 0; i < p; i++)
        {
            if (char.IsLetterOrDigit(t[i]))
            {
                return false;
            }
        }
        return true;
    }

    private static bool IsMark(char c) =>
        CharUnicodeInfo.GetUnicodeCategory(c) is UnicodeCategory.NonSpacingMark or UnicodeCategory.SpacingCombiningMark
            or UnicodeCategory.EnclosingMark;

    private static bool IsSlash(char c) => c is '/' or '\\' || SlashLookalikes.Contains(c, StringComparison.Ordinal);

    private static bool IsColon(char c) => c == ':' || ColonLookalikes.Contains(c, StringComparison.Ordinal);

    private static int SkipSlashes(string t, int i)
    {
        while (i < t.Length && IsSlash(t[i]))
        {
            i++;
        }
        return i;
    }

    private static string TrimDot(string s) => s.EndsWith('.') ? s[..^1] : s;

    private static string TrimPrefix(string s, string prefix) =>
        s.StartsWith(prefix, StringComparison.Ordinal) ? s[prefix.Length..] : s;
}
