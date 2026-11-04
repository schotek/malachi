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
// before the path, a colon another script draws or none ("https//…"), and
// it takes the host after the "@" of a text with userinfo. Here the text
// is the daemon's (links[].text: the anchor's text nodes joined with
// spaces and capped at 200 runes), cleaned of what is invisible in it and
// read in the compatibility form (NFKC: fullwidth letters, dots and
// slashes as ASCII); an internationalised host is compared in punycode (as
// the launcher hands it to the browser), a trailing dot is no part of a
// site; a text that reads as an address but whose host cannot be read
// names a host no link leads to; and every address in the text counts,
// not only one that is the whole text. What the view hides or draws
// otherwise (CSS, a <bdo>) and what the daemon cut off are not in that
// text: those limits are the daemon's to lift (docs/security.md §3.2).
// The text is compared, never shown: the question quotes it as the mail
// wrote it.

using System;
using System.Buffers;
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
    // big solidus and big reverse solidus, Philippine single punctuation,
    // the CJK stroke that falls to the left. (The double and triple solidus
    // operators stand for two and three slashes: Readable.)
    private const string SlashLookalikes = "\u2044\u2215\u2216\u2571\u2572\u27CB\u27CD\u29F5\u29F8\u29F9\u1735\u31D3";

    // What a reader takes for the colon after a scheme that NFKC leaves
    // alone: the modifier letters triangular colon and half triangular
    // colon, raised colon, Armenian full stop, Hebrew sof pasuq, Syriac
    // supralinear and sublinear colon, Devanagari and Gujarati sign
    // visarga, runic multiple punctuation, Ethiopic wordspace, two dot
    // punctuation, ratio, Lisu letter tone mya jeu, Latin colon. Some are
    // letters or marks, so a scheme ends at them before it could take them
    // in.
    private const string ColonLookalikes = "\u02D0\u02D1\u02F8\u0589\u05C3\u0703\u0704\u0903\u0A83\u16EC\u1361\u205A\u2236\uA4FD\uA789";

    // What a reader takes for a dot between the labels of a one-word text
    // that NFKC leaves alone (the ideographic full stop is read as a dot
    // anyway): Lisu letter tone mya ti, Arabic full stop, Syriac
    // supralinear and sublinear full stop, Vai full stop. Not the middle
    // dots, which sit above the line and are Catalan's "l·l" and the
    // Japanese "・" between words.
    private const string DotLookalikes = "\uA4F8\u06D4\u0701\u0702\uA60E";

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
    /// htmlview.hostOfText over a link's text as the daemon lists it
    /// (Windows): the hosts the text names, empty when it names none. Every
    /// address in the text counts: two slashes wherever they stand (after a
    /// letter or a mark they may follow a colon a reader sees in it, or
    /// none: "httpsꓽ//…", "https//…"; a scheme address that markup draws
    /// right to left begins there too, "…//:sptth"); and one that begins
    /// with a scheme, its colon (or a colon another script draws) and a
    /// slash (http and https need no slash, as Chromium reads them without)
    /// or with "www.", at the start or after anything but a letter, a digit
    /// or a mark ("Log in at https://…", "&lt;https://…&gt;",
    /// "Login:https://…"); such a start that the daemon's spaces split
    /// ("w ww.", "https :/ /") is read without them
    /// (<see cref="SpacedPrefix"/>). When there is none, the whole text is
    /// read as a bare host, as in GTK (a dot and an alphabetic top-level
    /// label, no space, no "@"; <see cref="BareHost"/>). A host is read up to its path,
    /// query or fragment, to a punctuation mark or symbol, and to the next
    /// space, except in an address the text begins with, where a space ends
    /// the host only where the host does not visibly go on after it (the
    /// daemon puts a space around each inline element of a link's text:
    /// "https://www.moje banka .example/login"; <see cref="HostGoesOn"/>),
    /// and otherwise is inside it. A space inside the host, userinfo, an
    /// escape, a port that is no number, or a host that
    /// <see cref="LooksLikeHost"/> refuses make it "", a host no link leads
    /// to. A host comes in ASCII (punycode), lower case, without a trailing
    /// dot. Before any of it the text loses what a reader does not see (the
    /// format characters, among them the soft hyphen, the zero-width
    /// characters, the word joiner, the byte order mark and the bidi
    /// controls; the other default-ignorable code points; the control
    /// characters), and is read in its compatibility form (NFKC: fullwidth
    /// letters, dots, colons and slashes as ASCII) with the ideographic full
    /// stop as a dot. What CSS hides or clips, what markup reorders and
    /// what the daemon cut off at 200 runes are beyond this text.
    /// </summary>
    public static IReadOnlyList<string> HostsOfText(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var t = Readable(text);
        var hosts = new List<string>();
        var p = 0;
        while (p < t.Length)
        {
            // Split slashes are read as two after a letter or a mark as well,
            // as two glued slashes are there (Address): "httpsঃ<b>/</b>/" is
            // listed as "httpsঃ/ /", and a scheme address that a <bdo>
            // draws right to left ends in "/ / :sptth".
            if ((p == 0 || !IsWordChar(t[p - 1]) || IsSlash(t[p])) && SpacedPrefix(t, p) is { } spaced)
            {
                // "w ww.", "https :/ /": the prefix as it is drawn.
                t = string.Concat(t.AsSpan(0, p), spaced.Prefix, t.AsSpan(spaced.End));
            }
            var (start, begins) = Address(t, p);
            if (start < 0)
            {
                p++;
                continue;
            }
            var (host, end) = ReadHost(t, start, BeginsText(t, begins));
            hosts.Add(host);
            // The rest of the address, its path, names no more hosts: an
            // address inside a path is what archive and redirect links show
            // ("https://web.archive.org/web/2020/https://example.com/").
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
    /// top-level label of punycode ("xn--p1ai"). Windows also takes "_"
    /// in the labels before the top-level one, as browsers and the launcher
    /// do ("my_shop.example"), so that such a host over itself is read, not
    /// taken for one that cannot be.
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
                if (c is not ((>= 'a' and <= 'z') or (>= '0' and <= '9') or '-' or '_'))
                {
                    return false;
                }
            }
        }
        var tld = labels[^1];
        if (tld.StartsWith("xn--", StringComparison.Ordinal))
        {
            return tld.Length > "xn--".Length && !tld.Contains('_', StringComparison.Ordinal);
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
    // NFKC also turns the halfwidth one) as a dot, the double and triple
    // solidus operators as the slashes they are drawn as, lower case,
    // trimmed.
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
        return s.Replace('\u3002', '.').Replace("\u2AFD", "//", StringComparison.Ordinal)
            .Replace("\u2AFB", "///", StringComparison.Ordinal).ToLowerInvariant().Trim();
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

    // Where the host of an address at p begins (-1 when no address starts
    // there) and where the address itself begins, for BeginsText. Two
    // slashes begin one wherever they stand: after a letter or a mark they
    // may follow a colon a reader sees in it, or none ("httpsꓽ//",
    // "https//"; the address then begins with that word), and a scheme
    // address drawn right to left by markup begins there as well
    // ("…//:sptth", whose host cannot be read). Otherwise an address
    // starts where no letter, digit or mark comes before it ("xhttps://"
    // and "mywww." are words): "www." (the host begins with it), or a
    // scheme (a letter, then letters, digits, marks, "+", "-" or ".", up to
    // a colon or what a reader takes for one) with its colon and a slash,
    // or http and https with their colon alone. The slashes are skipped.
    private static (int Host, int Begins) Address(string t, int p)
    {
        if (p + 1 < t.Length && IsSlash(t[p]) && IsSlash(t[p + 1]))
        {
            var word = p;
            while (word > 0 && IsWordChar(t[word - 1]))
            {
                word--;
            }
            return (SkipSlashes(t, p), word);
        }
        if (p > 0 && IsWordChar(t[p - 1]))
        {
            return (-1, p);
        }
        if (t.AsSpan(p).StartsWith("www.", StringComparison.Ordinal))
        {
            return (p, p);
        }
        if (!char.IsLetter(t[p]))
        {
            return (-1, p);
        }
        var colon = p + 1;
        while (colon < t.Length && !IsColon(t[colon])
            && (char.IsLetterOrDigit(t[colon]) || IsMark(t[colon]) || t[colon] is '+' or '-' or '.'))
        {
            colon++;
        }
        if (colon == t.Length || !IsColon(t[colon]))
        {
            return (-1, p);
        }
        var after = colon + 1;
        var scheme = t.AsSpan(p, colon - p);
        if ((after < t.Length && IsSlash(t[after])) || scheme.SequenceEqual("http") || scheme.SequenceEqual("https"))
        {
            return (SkipSlashes(t, after), p);
        }
        return (-1, p);
    }

    // The start of an address at p that the daemon's spaces split, where
    // an inline element begins or ends inside it ("<b>w</b>ww." is listed as
    // "w ww.", "https<b>:/</b>/" as "https :/ /"): "www.", two slashes or
    // more, or http and https with their colon (or one a reader sees) and
    // any slashes, or with two slashes and no colon, whose characters only
    // white space parts. Returns the prefix without that white space
    // (colons and slashes as ASCII) and where it ends in t, or null where
    // no such prefix with white space inside it starts at p; white space
    // after it (before the host) stays.
    private static (string Prefix, int End)? SpacedPrefix(string t, int p)
    {
        var prefix = "";
        var spaced = false;
        var i = p;
        while (i < t.Length)
        {
            var next = i;
            while (next < t.Length && char.IsWhiteSpace(t[next]))
            {
                next++;
            }
            if (next == t.Length)
            {
                break;
            }
            var c = IsSlash(t[next]) ? '/' : IsColon(t[next]) ? ':' : t[next];
            if (!IsAddressStart(prefix + c, complete: false))
            {
                break;
            }
            spaced |= next > i;
            prefix += c;
            i = next + 1;
        }
        return spaced && IsAddressStart(prefix, complete: true) ? (prefix, i) : null;
    }

    // Whether s begins an address as SpacedPrefix reads one (complete), or
    // may still grow into one: "www.", two slashes or more, "http" or
    // "https" with a colon and any slashes or with two slashes or more.
    private static bool IsAddressStart(string s, bool complete)
    {
        if ("www.".StartsWith(s, StringComparison.Ordinal))
        {
            return !complete || s.Length == 4;
        }
        if (s.TrimStart('/').Length == 0)
        {
            return !complete || s.Length >= 2;
        }
        var scheme = s.StartsWith("https", StringComparison.Ordinal) ? 5 : s.StartsWith("http", StringComparison.Ordinal) ? 4 : 0;
        if (scheme == 0)
        {
            return !complete && "https".StartsWith(s, StringComparison.Ordinal);
        }
        var rest = s[scheme..];
        if (rest.StartsWith(':'))
        {
            return rest[1..].TrimStart('/').Length == 0;
        }
        return rest.TrimStart('/').Length == 0 && (!complete || rest.Length >= 2);
    }

    // The host of an address in t, which begins at start, and where it
    // ends; "" when it cannot be read. Two dots in a row end it, as they
    // end a sentence ("www.shop.example… Shop now", "..." in NFKC).
    // White space ends the host, except in
    // an address the text begins with (whole): there white space before
    // the host goes, and a space after some of it ends it only where the
    // host does not visibly go on after the space (HostGoesOn); where it
    // does, the host has a space inside and cannot be read.
    private static (string Host, int End) ReadHost(string t, int start, bool whole)
    {
        var end = start;
        var begun = false;
        while (end < t.Length)
        {
            var c = t[end];
            if (char.IsWhiteSpace(c))
            {
                if (!whole || (begun && !HostGoesOn(t, end)))
                {
                    break;
                }
            }
            else if (EndsHost(c) || (c == '.' && end + 1 < t.Length && t[end + 1] == '.'))
            {
                break;
            }
            else
            {
                begun = true;
            }
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

    // Whether a host goes on after the white space at i, in an address a
    // link's text begins with: whether the space is one the daemon put
    // around an inline element ("www.<b>shop</b>.example" is listed as
    // "www. shop .example") rather than one between the address and words
    // ("www.shop.example for details", "- shop now", an image's alt the
    // daemon appends). It is where the word before the space ends with a
    // dot ("www. shop.example"), or the next word, up to its first slash,
    // begins with a dot that begins no ellipsis, whatever follows it
    // ("https://www.halifax.co<span>.</span>uk" is listed as
    // "https://www.halifax.co . uk"), has one before a letter inside
    // ("https://moje banka.example/login", or a hidden
    // "https://evil.example" before "mojebanka.example/login") or ends with
    // one; not where its dots only stand between digits or after one
    // another (a date, a price, an ellipsis). A dot another script draws
    // (DotLookalikes) counts as a dot.
    private static bool HostGoesOn(string t, int i)
    {
        if (i >= 2 && IsDot(t[i - 1]) && IsLabelChar(t[i - 2]))
        {
            return true;
        }
        var word = i;
        while (word < t.Length && char.IsWhiteSpace(t[word]))
        {
            word++;
        }
        var end = word;
        while (end < t.Length && !char.IsWhiteSpace(t[end]) && !IsSlash(t[end]) && t[end] is not ('?' or '#'))
        {
            end++;
        }
        if (word < end && IsDot(t[word]) && !(word + 1 < end && IsDot(t[word + 1])))
        {
            return true;
        }
        for (var k = word; k < end; k++)
        {
            if (!IsDot(t[k]))
            {
                continue;
            }
            if ((k + 1 < end && char.IsLetter(t[k + 1])) || (k + 1 == end && k > word && IsLabelChar(t[k - 1])))
            {
                return true;
            }
        }
        return false;
    }

    // Whether c ends the host of an address in a link's text, white space
    // aside (ReadHost): a slash, "?" or "#", the ASCII punctuation a host,
    // its port and userinfo do not carry, and any other punctuation mark,
    // symbol or separator. What stays in the host and is not a host's
    // ("~", "%", "@", a colon that is not a port's) makes it unreadable.
    private static bool EndsHost(char c)
    {
        if (IsSlash(c) || c is '?' or '#')
        {
            return true;
        }
        if (char.IsAscii(c))
        {
            return !(char.IsAsciiLetterOrDigit(c) || c is '-' or '.' or '_' or '~' or '%' or '@' or ':');
        }
        return !IsLabelChar(c);
    }

    // GTK's reading of a text that holds no address: the whole text as a
    // host ("bank.example.org", "mojebanka.example/login"), when it has no
    // space and no "@" (an e-mail address), from its first letter or digit
    // on (a mark before it is no part of it); null when it names none.
    // Windows: a colon that is no port's, or one another script draws, ends
    // a word before the host ("Login:mojebanka.example",
    // "Web∶shop.example"), or, after a host,
    // makes it one that cannot be read, as in an address
    // ("mojebanka.example:evil.example"); and a word a reader
    // takes for a host that is not written as one, with a dot another
    // script draws between its labels or more than one dot at its end
    // ("mojebankaꓸexample", "mojebanka۔example/login", "mojebanka.example…",
    // which is "mojebanka.example..." in NFKC), names a host that cannot be
    // read (""). A word whose dots only end it ("More...") names none.
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
        while (start < t.Length && !char.IsLetterOrDigit(t[start]))
        {
            start++;
        }
        var end = start;
        while (end < t.Length && (!EndsHost(t[end]) || DotLookalikes.Contains(t[end], StringComparison.Ordinal) || IsColon(t[end])))
        {
            end++;
        }
        var part = t[start..end];
        var colon = part.AsSpan().LastIndexOfAny(Colons);
        if (colon >= 0)
        {
            if (!part.AsSpan(colon + 1).ContainsAnyExceptInRange('0', '9'))
            {
                part = part[..colon]; // a port
            }
            else if (SeenHost(part[..colon]) is not null)
            {
                return ""; // a host with a port that is no number
            }
            else
            {
                part = part[(colon + 1)..]; // a word before the host
            }
        }
        return SeenHost(part);
    }

    // The host a word names as BareHost reads it, null for none: with the
    // dots another script draws as dots and without the dots at its end;
    // "" where it needed either (a lookalike, or more than one dot at the
    // end), since a host no link leads to is written so.
    private static string? SeenHost(string part)
    {
        var seen = part;
        foreach (var dot in DotLookalikes)
        {
            seen = seen.Replace(dot, '.');
        }
        var name = seen.TrimEnd('.');
        var host = AsciiHost(name);
        if (host.Length == 0 || !LooksLikeHost(host))
        {
            return null;
        }
        return string.Equals(seen, part, StringComparison.Ordinal) && seen.Length - name.Length < 2 ? host : "";
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

    // Whether c may stand in a label of a host a text names (in any
    // script: an internationalised host is read in punycode).
    private static bool IsLabelChar(char c) => char.IsLetterOrDigit(c) || char.IsNumber(c) || IsMark(c) || char.IsSurrogate(c);

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

    // A colon or one another script draws, for a search (BareHost).
    private static readonly SearchValues<char> Colons = SearchValues.Create(":" + ColonLookalikes);

    // A dot, or one another script draws between labels (DotLookalikes).
    private static bool IsDot(char c) => c == '.' || DotLookalikes.Contains(c, StringComparison.Ordinal);

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
