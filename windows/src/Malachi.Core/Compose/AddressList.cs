// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/AddressList.swift; GTK:
// ui/internal/compose/address.go (ParseAddressList, splitAddressRanges,
// FormatAddressList).
//
// The address-list helpers of the recipient rows: what they parse and show.
// The daemon validates authoritatively; this is only immediate feedback.
// .NET has no RFC 5322 parser either (MailAddress is far more lenient), so
// the subset net/mail.ParseAddress accepts for one mailbox is written out as
// Swift wrote it: comments, groups, domain literals and RFC 2047 encoded
// words are not supported and read as invalid (Go reads a trailing
// "(comment)" as the display name; the backend decides either way).
// Positions are UTF-16 indices on scalar boundaries (Swift String.Index).

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Text;
using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>Parses and formats the text of a recipient row.</summary>
public static class AddressList
{
    // The characters of a display name that Format quotes.
    private static readonly SearchValues<char> QuotedNameSpecials = SearchValues.Create(",;<>\"\\");

    /// <summary>
    /// compose.ParseAddressList: splits "Name &lt;a@b&gt;, c@d; e@f" into
    /// addresses. Tokens that do not parse are returned in
    /// <c>Invalid</c>, so the row can be flagged.
    /// </summary>
    public static (IReadOnlyList<Address> Addresses, IReadOnlyList<string> Invalid) Parse(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var addresses = new List<Address>();
        var invalid = new List<string>();
        foreach (var range in SplitRanges(s))
        {
            var token = range.Of(s).Trim();
            if (token.Length == 0)
            {
                continue;
            }
            if (ParseAddress(token) is { } a)
            {
                addresses.Add(a);
            }
            else
            {
                invalid.Add(token);
            }
        }
        return (addresses, invalid);
    }

    /// <summary>
    /// compose.splitAddressRanges: one range per token, split on commas and
    /// semicolons outside quotes and angle brackets (a backslash escapes only
    /// inside quotes), separators excluded, the last range running to the
    /// end of <paramref name="s"/>. The completion uses it to find the token
    /// under the caret with the rules the parser applies.
    /// </summary>
    public static IReadOnlyList<TextSpan> SplitRanges(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var output = new List<TextSpan>();
        var start = 0;
        var quoted = false;
        var angled = false;
        var escaped = false;
        for (var i = 0; i < s.Length;)
        {
            var r = ScalarText.At(s, i, out var length);
            if (escaped)
            {
                escaped = false;
            }
            else if (r == '\\' && quoted)
            {
                escaped = true;
            }
            else if (r == '"')
            {
                quoted = !quoted;
            }
            else if (quoted)
            {
                // Inside quotes nothing separates.
            }
            else if (r == '<')
            {
                angled = true;
            }
            else if (r == '>')
            {
                angled = false;
            }
            else if ((r == ',' || r == ';') && !angled)
            {
                output.Add(new TextSpan(start, i));
                start = i + length;
            }
            i += length;
        }
        output.Add(new TextSpan(start, s.Length));
        return output;
    }

    /// <summary>
    /// compose.FormatAddressList: the inverse of <see cref="Parse"/> for
    /// prefilled rows, "Name &lt;addr&gt;, addr". Names containing separators
    /// or quotes are quoted; non-ASCII names are left readable (this is UI
    /// text, not a header).
    /// </summary>
    public static string Format(IEnumerable<Address> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        var parts = new List<string>();
        foreach (var a in list)
        {
            var name = (a.Name ?? "").Trim();
            if (name.Length == 0)
            {
                parts.Add(a.Email);
            }
            else if (name.AsSpan().IndexOfAny(QuotedNameSpecials) >= 0)
            {
                var escaped = name.Replace("\\", "\\\\", StringComparison.Ordinal).Replace("\"", "\\\"", StringComparison.Ordinal);
                parts.Add("\"" + escaped + "\" <" + a.Email + ">");
            }
            else
            {
                parts.Add(name + " <" + a.Email + ">");
            }
        }
        return string.Join(", ", parts);
    }

    /// <summary>
    /// One mailbox as net/mail.ParseAddress reads it: an optional display
    /// name (atoms, or a quoted-string with backslash escapes) and an
    /// angle-addr, or a bare addr-spec; addr-spec is local@domain with
    /// dot-atoms (or a quoted local part). Only spaces and tabs may follow.
    /// A missing display name is null; null when <paramref name="s"/> is not
    /// such a mailbox.
    /// </summary>
    public static Address? ParseAddress(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return new MailboxParser(s).ParseSingle();
    }

    // The relevant part of net/mail's addrParser over scalars (Go iterates
    // runes). Positions are restored on failure the way the Go parser
    // restores p.s.
    private sealed class MailboxParser(string text)
    {
        private readonly int[] s = ScalarText.Of(text);
        private int i;

        private bool IsEmpty => i >= s.Length;

        private int Peek() => i < s.Length ? s[i] : ScalarText.Invalid;

        private bool Consume(char c)
        {
            if (IsEmpty || s[i] != c)
            {
                return false;
            }
            i++;
            return true;
        }

        private void SkipSpace()
        {
            while (!IsEmpty && (s[i] == ' ' || s[i] == '\t'))
            {
                i++;
            }
        }

        // parseSingleAddress: one address, then nothing but white space.
        public Address? ParseSingle()
        {
            var a = ParseAddress();
            if (a is null)
            {
                return null;
            }
            SkipSpace();
            return IsEmpty ? a : null;
        }

        // parseAddress: addr-spec has a more restricted grammar than
        // name-addr, so it is tried first; then display-name? angle-addr.
        private Address? ParseAddress()
        {
            SkipSpace();
            if (IsEmpty)
            {
                return null;
            }
            var save = i;
            if (ConsumeAddrSpec() is { } spec)
            {
                return new Address { Email = spec };
            }
            i = save;
            string? displayName = null;
            if (Peek() != '<')
            {
                var phrase = ConsumePhrase();
                if (phrase is null)
                {
                    return null;
                }
                displayName = phrase.Length == 0 ? null : phrase;
            }
            SkipSpace();
            if (!Consume('<'))
            {
                return null;
            }
            var angle = ConsumeAddrSpec();
            if (angle is null || !Consume('>'))
            {
                return null;
            }
            return new Address { Name = displayName, Email = angle };
        }

        // consumeAddrSpec: local-part "@" domain.
        private string? ConsumeAddrSpec()
        {
            var save = i;
            var spec = AddrSpec();
            if (spec is null)
            {
                i = save;
            }
            return spec;
        }

        private string? AddrSpec()
        {
            SkipSpace();
            if (IsEmpty)
            {
                return null;
            }
            string localPart;
            if (Peek() == '"')
            {
                var q = ConsumeQuotedString();
                if (string.IsNullOrEmpty(q))
                {
                    return null;
                }
                localPart = q;
            }
            else
            {
                var a = ConsumeAtom(dot: true, permissive: false);
                if (a is null)
                {
                    return null;
                }
                localPart = a;
            }
            if (!Consume('@'))
            {
                return null;
            }
            SkipSpace();
            if (IsEmpty)
            {
                return null;
            }
            var domain = ConsumeAtom(dot: true, permissive: false);
            return domain is null ? null : localPart + "@" + domain;
        }

        // consumePhrase: words (atoms or quoted strings) joined by one
        // space. A word that fails to parse ends the phrase; the phrase
        // fails only when it has no word at all.
        private string? ConsumePhrase()
        {
            var words = new List<string>();
            while (true)
            {
                SkipSpace();
                if (IsEmpty)
                {
                    break;
                }
                var word = Peek() == '"' ? ConsumeQuotedString() : ConsumeAtom(dot: true, permissive: true);
                if (word is null)
                {
                    break;
                }
                words.Add(word);
            }
            return words.Count == 0 ? null : string.Join(" ", words);
        }

        // consumeQuotedString: the content between the quotes with
        // quoted-pairs resolved; the opening quote is at the position.
        private string? ConsumeQuotedString()
        {
            var j = i + 1;
            var output = new StringBuilder();
            var escaped = false;
            while (true)
            {
                if (j >= s.Length)
                {
                    return null;
                }
                var r = s[j];
                if (escaped)
                {
                    if (!IsVchar(r) && !IsWsp(r))
                    {
                        return null;
                    }
                    ScalarText.Append(output, r);
                    escaped = false;
                }
                else if (IsQtext(r) || IsWsp(r))
                {
                    ScalarText.Append(output, r);
                }
                else if (r == '"')
                {
                    break;
                }
                else if (r == '\\')
                {
                    escaped = true;
                }
                else
                {
                    return null;
                }
                j++;
            }
            i = j + 1;
            return output.ToString();
        }

        // consumeAtom: the longest run of atext; in strict mode (an
        // addr-spec) no leading, trailing or doubled dot.
        private string? ConsumeAtom(bool dot, bool permissive)
        {
            var j = i;
            while (j < s.Length && IsAtext(s[j], dot, permissive))
            {
                j++;
            }
            if (j == i)
            {
                return null;
            }
            var b = new StringBuilder();
            for (var k = i; k < j; k++)
            {
                ScalarText.Append(b, s[k]);
            }
            var atom = b.ToString();
            i = j;
            if (!permissive && (atom.StartsWith('.') || atom.Contains("..", StringComparison.Ordinal) || atom.EndsWith('.')))
            {
                return null;
            }
            return atom;
        }

        private static bool IsAtext(int r, bool dot, bool permissive) => r switch
        {
            '.' => dot,
            // RFC 5322 3.2.3 specials, tolerated in a display name.
            '(' or ')' or '[' or ']' or ';' or '@' or '\\' or ',' => permissive,
            '<' or '>' or '"' or ':' => false,
            _ => IsVchar(r),
        };

        // isVchar: printable US-ASCII, or anything beyond ASCII (RFC 6532);
        // never a lone surrogate (Go: invalid UTF-8 fails the address).
        private static bool IsVchar(int r) => (r >= 0x21 && r <= 0x7e) || r >= 0x80;

        private static bool IsQtext(int r) => r != '\\' && r != '"' && IsVchar(r);

        private static bool IsWsp(int r) => r == ' ' || r == '\t';
    }
}
