// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardCase.swift (Cap, cleanLine,
// cleanBlock, budget, kept, dropped, capped); GTK: ui/internal/board/case.go
// (the cap constants, CleanLine, CleanBlock, budget, kept, dropped, capped,
// graphemeBoundary, extends, regionalIndicator, hangulKind, hangulJoins,
// joinerState).
//
// The cleaning every string of a case goes through before it reaches a view
// model. The cut follows Go: Swift asks the string for its grapheme
// boundaries, Go (and this port) checks the rules of UAX #29 cleaned text
// can still meet (marks, emoji modifiers, Hangul syllables, regional
// indicator pairs), so a cut keeps a little more before a Prepend character
// or inside an Indic conjunct. A lone surrogate is Go's invalid UTF-8 and
// Swift's replacement character: dropped. White space is Go's
// unicode.IsSpace (Assistant.IsSpace), the dropped categories Cc, Cf, Zl
// and Zp, except a joiner (ZWJ, ZWNJ) between two kept characters
// (JoinerState, the daemon's board.CleanText rule).

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Assistants;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>The caps of the cleaned strings, in UTF-8 bytes (or a count).</summary>
    public static class Cap
    {
        /// <summary>A person's name.</summary>
        public const int Person = 200;

        /// <summary>A title or a subject.</summary>
        public const int Title = 300;

        /// <summary>A row's snippet.</summary>
        public const int Snippet = 400;

        /// <summary>The reason of the state.</summary>
        public const int Reason = 400;

        /// <summary>An account's name.</summary>
        public const int Account = 120;

        /// <summary>An account's kind capsule.</summary>
        public const int Badge = 64;

        /// <summary>An issue's key.</summary>
        public const int IssueKey = 64;

        /// <summary>An issue's status.</summary>
        public const int Status = 64;

        /// <summary>A quoted sentence.</summary>
        public const int Quote = 300;

        /// <summary>A task.</summary>
        public const int Task = 300;

        /// <summary>The tasks shown.</summary>
        public const int Tasks = 20;

        /// <summary>The tasks looked at to find <see cref="Tasks"/> non-empty ones.</summary>
        public const int TaskScan = 200;

        /// <summary>The commitments shown.</summary>
        public const int Commitments = 100;

        /// <summary>A commitment's text.</summary>
        public const int Commitment = 300;

        /// <summary>The assistant's model name.</summary>
        public const int Model = 64;

        /// <summary>A run's note.</summary>
        public const int Note = 300;

        /// <summary>The assistant's summary.</summary>
        public const int Summary = 2000;

        /// <summary>A suggested reply's text.</summary>
        public const int Draft = 4000;

        /// <summary>An annotation's source.</summary>
        public const int Source = 64;

        /// <summary>A message's text: <c>board.get</c>'s cap (api.MaxBoardMessageTextBytes, a whole ordinary mail).</summary>
        public const int Message = BoardLimits.MaxBoardMessageTextBytes;

        /// <summary>The messages of the conversation shown.</summary>
        public const int Messages = 50;
    }

    /// <summary>
    /// One line of display text made safe: drops invalid UTF-16 (a lone
    /// surrogate) and the replacement character, control and format
    /// characters (Cc, Cf: NUL, bidirectional overrides such as U+202E,
    /// zero-width characters) except a joiner between two kept characters,
    /// turns every whitespace (line breaks, tabs,
    /// U+2028, U+2029) into a space, collapses runs of spaces, trims, and
    /// caps the result at <paramref name="max"/> UTF-8 bytes on a character
    /// boundary (no grapheme cluster the cut broke is kept). Stops reading
    /// once the cap is passed, or after 8 × <paramref name="max"/> input
    /// characters when most of them are dropped, so a huge input costs no
    /// more than a short one.
    /// </summary>
    public static string CleanLine(string? s, int max)
    {
        if (max <= 0 || string.IsNullOrEmpty(s))
        {
            return "";
        }
        var output = new List<Rune>();
        var bytes = 0;
        var limit = max;
        var space = false;
        var joiner = new JoinerState();
        var left = Budget(max);
        for (var i = 0; i < s.Length;)
        {
            var r = Next(s, ref i);
            if (left == 0)
            {
                // Out of budget: the text ends here. r goes along past the
                // limit only so the cut can tell whether it broke a character.
                limit = int.Min(limit, bytes);
                if (!space && Kept(r))
                {
                    output.Add(r);
                }
                break;
            }
            left--;
            if (r == Rune.ReplacementChar)
            {
                continue;
            }
            if (Assistant.IsSpace(r.Value))
            {
                space = output.Count > 0;
                joiner.Reset();
                continue;
            }
            if (joiner.Take(r, output.Count == 0 || space))
            {
                continue;
            }
            if (Dropped(r))
            {
                continue;
            }
            if (space)
            {
                output.Add(new Rune(' '));
                bytes++;
                space = false;
            }
            bytes += joiner.Flush(output);
            output.Add(r);
            bytes += r.Utf8SequenceLength;
            if (bytes > max)
            {
                break;
            }
        }
        return Capped(output, limit);
    }

    /// <summary>
    /// A block of display text made safe: like <see cref="CleanLine"/>, but
    /// line breaks stay (CR LF, CR, NEL, VT, FF, U+2028 and U+2029 become
    /// "\n"), other whitespace becomes a space, spaces at the end of a line
    /// go, at most one empty line is kept in a row, and the block is trimmed.
    /// </summary>
    public static string CleanBlock(string? s, int max)
    {
        if (max <= 0 || string.IsNullOrEmpty(s))
        {
            return "";
        }
        var output = new List<Rune>();
        var bytes = 0;
        var limit = max;
        var spaces = 0;
        var breaks = 0;
        var afterCR = false;
        var joiner = new JoinerState();
        var left = Budget(max);
        for (var i = 0; i < s.Length;)
        {
            var r = Next(s, ref i);
            if (left == 0)
            {
                // Out of budget, as in CleanLine.
                limit = int.Min(limit, bytes);
                if (spaces == 0 && breaks == 0 && Kept(r))
                {
                    output.Add(r);
                }
                break;
            }
            left--;
            var cr = afterCR;
            afterCR = false;
            if (r == Rune.ReplacementChar)
            {
                continue;
            }
            if (Assistant.IsSpace(r.Value))
            {
                switch (r.Value)
                {
                    case '\n':
                        if (!cr)
                        {
                            breaks++;
                        }
                        spaces = 0;
                        break;
                    case '\r':
                        breaks++;
                        spaces = 0;
                        afterCR = true;
                        break;
                    case '\v' or '\f' or 0x85 or 0x2028 or 0x2029:
                        breaks++;
                        spaces = 0;
                        break;
                    default:
                        spaces++;
                        break;
                }
                joiner.Reset();
                continue;
            }
            if (joiner.Take(r, output.Count == 0 || spaces > 0 || breaks > 0))
            {
                continue;
            }
            if (Dropped(r))
            {
                continue;
            }
            if (output.Count > 0)
            {
                // A run of whitespace is bounded by the cap: it is cut anyway.
                var lines = int.Min(breaks, 2);
                var pad = int.Min(spaces, max);
                for (var k = 0; k < lines; k++)
                {
                    output.Add(new Rune('\n'));
                }
                for (var k = 0; k < pad; k++)
                {
                    output.Add(new Rune(' '));
                }
                bytes += lines + pad;
            }
            breaks = 0;
            spaces = 0;
            bytes += joiner.Flush(output);
            output.Add(r);
            bytes += r.Utf8SequenceLength;
            if (bytes > max)
            {
                break;
            }
        }
        return Capped(output, limit);
    }

    // The scalar at s[i], i moved past it; a lone surrogate is the
    // replacement character (Go's utf8.RuneError for invalid UTF-8).
    private static Rune Next(string s, ref int i)
    {
        var status = Rune.DecodeFromUtf16(s.AsSpan(i), out var r, out var used);
        i += int.Max(used, 1);
        return status == OperationStatus.Done ? r : Rune.ReplacementChar;
    }

    // How many input characters the cleaners read for a cap of max bytes:
    // what they keep stops them at the cap, so only dropped characters and
    // whitespace run into this.
    private static int Budget(int max) => max > int.MaxValue / 8 ? int.MaxValue : max * 8;

    // Whether the cleaners keep r as it is (not whitespace, not dropped).
    private static bool Kept(Rune r) => r != Rune.ReplacementChar && !Assistant.IsSpace(r.Value) && !Dropped(r);

    // The control and format characters the cleaners drop (Cc, Cf, Zl, Zp).
    private static bool Dropped(Rune r) => Rune.GetUnicodeCategory(r) is UnicodeCategory.Control
        or UnicodeCategory.Format or UnicodeCategory.LineSeparator or UnicodeCategory.ParagraphSeparator;

    // output cut to at most n UTF-8 bytes on a character boundary, the
    // whitespace before the cut trimmed. A cut inside a grapheme cluster (a
    // letter whose combining mark is past it, one regional indicator of a
    // flag) drops the whole cluster. What follows the cut is still in
    // output, so the boundary is that of the whole text.
    private static string Capped(List<Rune> output, int n)
    {
        var end = 0;
        var bytes = 0;
        while (end < output.Count && bytes + output[end].Utf8SequenceLength <= n)
        {
            bytes += output[end].Utf8SequenceLength;
            end++;
        }
        while (end > 0 && !GraphemeBoundary(output, end))
        {
            end--;
        }
        // Whitespace before the cut goes, and so does a joiner the cut left
        // last: it joins nothing.
        while (end > 0 && output[end - 1].Value is ' ' or '\n' or Zwj or Zwnj)
        {
            end--;
        }
        var b = new StringBuilder(end);
        for (var k = 0; k < end; k++)
        {
            b.Append(output[k].ToString());
        }
        return b.ToString();
    }

    // Whether a cut before output[i] falls between two grapheme clusters, by
    // the rules of UAX #29 that cleaned text can still meet: no break before
    // a combining or spacing mark or an emoji modifier (GB9, GB9a), inside a
    // Hangul syllable (GB6–GB8) or inside a pair of regional indicators
    // (GB12, GB13); always one at a line break. Prepend characters and Indic
    // conjuncts are not considered (a cut there keeps a little more).
    private static bool GraphemeBoundary(List<Rune> output, int i)
    {
        if (i <= 0 || i >= output.Count)
        {
            return true;
        }
        var prev = output[i - 1].Value;
        var next = output[i].Value;
        if (prev == '\n' || next == '\n')
        {
            return true;
        }
        if (HangulJoins(HangulKind(prev), HangulKind(next)))
        {
            return false;
        }
        if (Extends(output[i]))
        {
            return false;
        }
        if (RegionalIndicator(prev) && RegionalIndicator(next))
        {
            var count = 0;
            for (var j = i - 1; j >= 0 && RegionalIndicator(output[j].Value); j--)
            {
                count++;
            }
            return count % 2 == 0;
        }
        return true;
    }

    // A character that belongs to the cluster before it: a mark (Mn, Me, Mc
    // and Other_Grapheme_Extend), an emoji modifier, or one of the two
    // spacing marks that are letters (Thai and Lao AM), or a joiner the
    // cleaners kept.
    private static bool Extends(Rune r) =>
        r.Value is Zwj or Zwnj
        || Rune.GetUnicodeCategory(r) is UnicodeCategory.NonSpacingMark or UnicodeCategory.EnclosingMark
            or UnicodeCategory.SpacingCombiningMark
        || OtherGraphemeExtend(r.Value) || r.Value is (>= 0x1F3FB and <= 0x1F3FF) or 0x0E33 or 0x0EB3;

    // Other_Grapheme_Extend (Unicode 15) beyond the marks: the halfwidth
    // katakana sound marks, ZWNJ and the tags (the tags are Cf and dropped
    // before they get here; a kept ZWNJ is Extends' own case).
    private static bool OtherGraphemeExtend(int r) => r is 0x09BE or 0x09D7 or 0x0B3E or 0x0B57 or 0x0BBE or 0x0BD7
        or 0x0CC2 or 0x0CD5 or 0x0CD6 or 0x0D3E or 0x0D57 or 0x0DCF or 0x0DDF or 0x1B35 or 0x200C or 0x302E or 0x302F
        or 0xFF9E or 0xFF9F or 0x1133E or 0x11357 or 0x114B0 or 0x114BD or 0x115AF or 0x11930 or 0x1D165
        or (>= 0x1D16E and <= 0x1D172) or (>= 0xE0020 and <= 0xE007F);

    private static bool RegionalIndicator(int r) => r is >= 0x1F1E6 and <= 0x1F1FF;

    // The Hangul syllable types of UAX #29.
    private const int HangulNone = 0;
    private const int HangulL = 1;
    private const int HangulV = 2;
    private const int HangulT = 3;
    private const int HangulLV = 4;
    private const int HangulLVT = 5;

    private static int HangulKind(int r) => r switch
    {
        (>= 0x1100 and <= 0x115F) or (>= 0xA960 and <= 0xA97C) => HangulL,
        (>= 0x1160 and <= 0x11A7) or (>= 0xD7B0 and <= 0xD7C6) => HangulV,
        (>= 0x11A8 and <= 0x11FF) or (>= 0xD7CB and <= 0xD7FB) => HangulT,
        >= 0xAC00 and <= 0xD7A3 => (r - 0xAC00) % 28 == 0 ? HangulLV : HangulLVT,
        _ => HangulNone,
    };

    private static bool HangulJoins(int p, int n) => p switch
    {
        HangulL => n is HangulL or HangulV or HangulLV or HangulLVT,
        HangulLV or HangulV => n is HangulV or HangulT,
        HangulLVT or HangulT => n == HangulT,
        _ => false,
    };

    // The joiners and the variation selectors the joiner rule looks at.
    private const int Zwnj = 0x200C;
    private const int Zwj = 0x200D;
    private const int Vs15 = 0xFE0E;
    private const int Vs16 = 0xFE0F;

    // A joiner (ZWJ, ZWNJ) the cleaners hold back until they see what
    // follows it. The rule is the daemon's (board.CleanText): a joiner is
    // kept only when, once the dropped characters are gone, the characters
    // right before and right after it are kept characters that are neither
    // whitespace nor a joiner. A joiner at the start or end of a line, next
    // to whitespace, or in a run of joiners goes; a variation selector right
    // after a held joiner goes too. Emoji ZWJ sequences and Persian or
    // Indic words keep theirs.
    private struct JoinerState
    {
        // The joiner held back, 0 when none.
        private int joiner;

        // The held joiner goes whatever follows (nothing kept before it,
        // whitespace before it, or a run of joiners).
        private bool stray;

        // Whether r was consumed by the joiner rule: a joiner (held, or
        // marking a run), or a variation selector after a held joiner.
        // atStart: nothing kept is before r on its line, or whitespace is.
        public bool Take(Rune r, bool atStart)
        {
            if (r.Value is Zwnj or Zwj)
            {
                if (joiner != 0)
                {
                    stray = true;
                }
                else
                {
                    joiner = r.Value;
                    stray = atStart;
                }
                return true;
            }
            return r.Value is Vs15 or Vs16 && joiner != 0;
        }

        // Writes the held joiner before a kept character, unless it is
        // stray, and forgets it; the UTF-8 bytes written.
        public int Flush(List<Rune> output)
        {
            var written = 0;
            if (joiner != 0 && !stray)
            {
                var j = new Rune(joiner);
                output.Add(j);
                written = j.Utf8SequenceLength;
            }
            Reset();
            return written;
        }

        // Forgets a held joiner (whitespace followed it).
        public void Reset()
        {
            joiner = 0;
            stray = false;
        }
    }
}
