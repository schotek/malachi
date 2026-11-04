// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantMarkdown.swift
// (Assistant.markdown, maxListLevel, cleanMarkdownText, inlineSpans,
// hasWebScheme, isWebURL, MarkdownParser, Closers, Inliner, URLScan, Finder,
// goURLHostname and the rest of Go's url.Parse); GTK:
// ui/internal/assistant/markdown.go (Markdown, maxListLevel, cleanText,
// mdParser, splitIndent, heading, listItem, closers, inliner, inlineSpans,
// opensAt, spaceBefore, wordBefore, wordAt, hasWebScheme, bareURL,
// isWebURL).
//
// The In App target: the small Markdown subset the panel shows the model's
// answers in (the system prompt asks for it). An answer is hostile input
// like mail: it may quote a message verbatim. So this is a linear scanner
// over a fixed set of constructs: no HTML ever, no nesting beyond bold and
// italic around text, links only to http and https, every control character
// but the newline and the tab dropped. The client turns the blocks into its
// own text; nothing here knows about rendering.
//
// Linear: every opening marker finds its closing one through a cursor over
// the closing positions of its kind, which only moves forward (markers are
// met left to right, and one kind never nests in itself), so a megabyte of
// asterisks or brackets costs what a megabyte of letters does. The port works
// on the UTF-8 bytes with byte offsets, as Go and Swift do, with Go's
// character classes (Assistant.Unicode.cs), and IsWebUrl follows Go's
// url.Parse (as a module on go 1.25 runs it: the last colon of a host starts
// its port), never System.Uri, whose rules differ. As in Swift, one step
// beyond the Go source: a URL, a link's target or a bare one, is checked in
// place in its block's text through cursors (UrlScan) that remember how far
// each search (for "#", "?", "/", a bad escape, a refused character) got, so
// that the URLs read left to right cost one pass.
//
// Windows: the blocks and spans are the top-level MarkdownBlock,
// MarkdownBlockKind and MarkdownSpan (Swift's Assistant.Block, BlockKind and
// Span). The cursors are classes, not Swift's mutable structs, so none is
// ever copied by accident. The inliner reads a copy of its block's text
// (a class cannot hold a span into the cleaned text, where Swift rebases a
// pointer); a block of several lines is a new array in Swift as well (its
// lines joined). IsWebUrl(string) is public for the tests (Swift's is
// internal); the other helpers Swift keeps internal are private, as nothing
// else uses them and the tests see no internals, and Swift's inline(_:),
// which no test calls, is not ported. Two savings of allocations Go and
// Swift do without (a table makes an inliner or two for every row): text
// with no byte the inliner acts on is one plain span without an inliner, and
// the inliner makes its URL cursors when it first needs them. A C# string
// holds no invalid UTF-8 for Go's U+FFFD to replace; a lone surrogate is
// U+FFFD once encoded (Utf8). This file holds no translatable text.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Runtime.InteropServices;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>maxListLevel: the deepest list nesting kept; deeper items stay at it.</summary>
    private const int MaxListLevel = 3;

    /// <summary>
    /// assistant.Markdown: the blocks of <paramref name="text"/>. Lines are
    /// separated by "\n", "\r\n" or "\r". A blank line ends a paragraph or
    /// list item; a paragraph's lines stay separate lines ("\n" in the text),
    /// and a line that starts nothing continues the open paragraph or list
    /// item. After at most three spaces: "# ", "## " and "### " start a
    /// heading; a line of three backticks a code block up to the next such
    /// line (or the end: an answer that is still streaming). After any
    /// indentation: "- " and "* " start a bullet, 1 to 9 digits and ". " a
    /// numbered item, nested by indentation (an item indented more than the
    /// one before goes one level deeper, one indented as an earlier one
    /// returns to its level). A heading or list marker with no text after it
    /// is text. Inline: **bold**, *italic* and _italic_ (an underscore only at
    /// a word's edges, so create_draft stays as it is), `code`,
    /// [text](http(s)://…) and bare http(s):// URLs at a word's start. A
    /// marker without its closing half, and a link to anything but http or
    /// https, stays literal text.
    /// <para>
    /// A table (a paragraph's line of cells between "|", a delimiter row of
    /// as many ---, :---, ---: or :---: cells, then rows) is read as one
    /// bullet per row, for the narrow panel: its first cell in bold, then
    /// every other cell that is not empty on a line of its own as "header:
    /// cell". The pipes at a row's edges are optional, "\|" is a pipe inside
    /// a cell, cells beyond the header's are dropped and missing ones are
    /// empty; a blank line, a line without a pipe, a heading or a fence ends
    /// the table, and a table without rows shows nothing. The header labels
    /// cost at most what the rows themselves do: once they would outweigh the
    /// rows so far, the cells go without them.
    /// </para>
    /// </summary>
    public static IReadOnlyList<MarkdownBlock> Markdown(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var s = CleanMarkdownText(text);
        var parser = new MarkdownParser(s);
        var start = 0;
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == 0x0A)
            {
                parser.Line(start, i);
                start = i + 1;
            }
        }
        parser.Line(start, s.Length);
        parser.End();
        return parser.Blocks;
    }

    /// <summary>
    /// cleanText: "\r\n" and "\r" become "\n", every control character but
    /// "\n" and "\t" is dropped (a C# string has no invalid UTF-8 for Go's
    /// U+FFFD to replace; <see cref="Utf8"/> writes a lone surrogate as it).
    /// </summary>
    private static byte[] CleanMarkdownText(string text)
    {
        var b = Utf8(text);
        var w = 0;
        var i = 0;
        while (i < b.Length)
        {
            var c = b[i];
            switch (c)
            {
                case 0x0D:
                    b[w] = 0x0A;
                    w++;
                    if (i + 1 < b.Length && b[i + 1] == 0x0A)
                    {
                        i++;
                    }
                    break;
                case 0x0A or 0x09:
                    b[w] = c;
                    w++;
                    break;
                case < 0x20 or 0x7F:
                    break;
                case 0xC2 when i + 1 < b.Length && b[i + 1] is >= 0x80 and <= 0x9F:
                    i++; // a C1 control
                    break;
                default:
                    b[w] = c;
                    w++;
                    break;
            }
            i++;
        }
        Array.Resize(ref b, w);
        return b;
    }

    // Inline

    // The bytes Inliner.Run acts on: `, *, _, [ and the h or H a bare URL
    // starts with.
    private static readonly SearchValues<byte> InlineMarkers = SearchValues.Create("`*_[hH"u8);

    /// <summary>inlineSpans: the spans of a block's text.</summary>
    private static List<MarkdownSpan> InlineSpans(byte[] s)
    {
        if (s.Length == 0)
        {
            return [];
        }
        if (s.AsSpan().IndexOfAny(InlineMarkers) < 0)
        {
            // What Run makes of text without any of them: one plain span.
            return [new MarkdownSpan { Text = FromUtf8(s) }];
        }
        var inl = new Inliner(s);
        inl.Run(0, s.Length, false, false);
        inl.CloseSpan();
        return inl.Spans;
    }

    /// <summary>
    /// hasWebScheme: whether <paramref name="s"/>[<paramref name="i"/>..<paramref name="hi"/>)
    /// starts with http:// or https://, in any case.
    /// </summary>
    private static bool HasWebScheme(ReadOnlySpan<byte> s, int i, int hi)
    {
        if (!(At(s, i, hi, 0, 0x68) && At(s, i, hi, 1, 0x74) && At(s, i, hi, 2, 0x74) && At(s, i, hi, 3, 0x70))) // http
        {
            return false;
        }
        if (At(s, i, hi, 4, 0x3A))
        {
            return At(s, i, hi, 5, 0x2F) && At(s, i, hi, 6, 0x2F); // ://
        }
        return At(s, i, hi, 4, 0x73) && At(s, i, hi, 5, 0x3A) && At(s, i, hi, 6, 0x2F) && At(s, i, hi, 7, 0x2F); // s://

        static bool At(ReadOnlySpan<byte> s, int i, int hi, int k, byte c)
        {
            if (i + k >= hi)
            {
                return false;
            }
            var b = s[i + k];
            return b == c || (c is >= 0x61 and <= 0x7A && b == c - 0x20);
        }
    }

    /// <summary>
    /// isWebURL for a whole string (the tests' check of every link). Public
    /// for the tests (Swift's is internal).
    /// </summary>
    public static bool IsWebUrl(string u)
    {
        ArgumentNullException.ThrowIfNull(u);
        var b = Utf8(u);
        return IsWebUrl(b, 0, b.Length, new UrlScan());
    }

    /// <summary>
    /// isWebURL: whether <paramref name="s"/>[<paramref name="lo"/>..<paramref name="hi"/>)
    /// is an absolute http or https URL with a host and nothing a URL does
    /// not carry literally (no white space, control characters, quotes, angle
    /// brackets, backticks or backslashes), as Go's url.Parse reads it.
    /// </summary>
    private static bool IsWebUrl(ReadOnlySpan<byte> s, int lo, int hi, UrlScan scan) =>
        HasWebScheme(s, lo, hi) && scan.Bad.Next(s, lo) >= hi && GoUrlHostname(s, lo, hi, scan);

    // Blocks

    /// <summary>mdParser: reads the lines of Markdown, which are ranges of <c>s</c>.</summary>
    private sealed class MarkdownParser(byte[] s)
    {
        // inCode while a code block is open; code its lines.
        private readonly List<(int Lo, int Hi)> code = [];

        // The open paragraph or list item, if any, and its lines (trimmed).
        private readonly List<(int Lo, int Hi)> lines = [];

        // The indentations of the open list's levels, outermost first.
        private readonly List<int> indents = [];

        private bool inCode;
        private MarkdownBlock? open;

        // The open table's header cells, null when no table is open; budget
        // what the rows so far leave for the header labels, in bytes.
        private List<byte[]>? header;
        private int budget;

        // Scratch the table rows reuse: the bytes of a cell being read, a
        // row's cells and the text of its fields.
        private readonly List<byte> cellBytes = [];
        private readonly List<byte[]> rowCells = [];
        private readonly ArrayBufferWriter<byte> rowText = new();

        public List<MarkdownBlock> Blocks { get; } = [];

        public void Line(int lo, int hi)
        {
            // splitIndent: the columns of the leading spaces and tabs (a tab
            // to the next multiple of 4); the rest starts at r.
            var indent = 0;
            var r = lo;
            while (r < hi)
            {
                if (s[r] == 0x20)
                {
                    indent++;
                }
                else if (s[r] == 0x09)
                {
                    indent += 4 - (indent % 4);
                }
                else
                {
                    break;
                }
                r++;
            }
            if (inCode)
            {
                if (indent <= 3 && IsFence(r, hi))
                {
                    CloseCode();
                    return;
                }
                code.Add((lo, hi));
                return;
            }
            if (header is not null)
            {
                var fenceOrHeading = indent <= 3 && (IsFence(r, hi) || Heading(r, hi, out _, out _));
                if (!fenceOrHeading && HasPipe(r, hi))
                {
                    TableRow(r, hi);
                    return;
                }
                header = null;
            }
            if (r == hi) // nothing but spaces and tabs
            {
                Flush();
                return;
            }
            if (indent <= 3 && IsFence(r, hi))
            {
                Flush();
                indents.Clear();
                inCode = true;
                return;
            }
            if (indent <= 3 && Heading(r, hi, out var level, out var title))
            {
                Flush();
                indents.Clear();
                Blocks.Add(new MarkdownBlock { Kind = MarkdownBlockKind.Heading, Level = level, Spans = InlineSpans(s[title.Lo..title.Hi]) });
                return;
            }
            if (indent <= 3 && open is { Kind: MarkdownBlockKind.Paragraph } && StartTable(r, hi))
            {
                return;
            }
            if (ListItem(r, hi, out var kind, out var number, out var item))
            {
                Flush();
                open = new MarkdownBlock { Kind = kind, Level = ListLevel(indent), Number = number };
                lines.Add(item);
                return;
            }
            var text = TrimST(r, hi);
            if (open is not null)
            {
                lines.Add(text);
                return;
            }
            indents.Clear();
            open = new MarkdownBlock { Kind = MarkdownBlockKind.Paragraph };
            lines.Add(text);
        }

        public void End()
        {
            if (inCode)
            {
                CloseCode();
            }
            Flush();
        }

        // startTable: reads s[r..hi) as a delimiter row under the open
        // paragraph's last line; when that line is a header of as many cells,
        // it leaves the paragraph (the rest of it is flushed) and a table
        // opens.
        private bool StartTable(int r, int hi)
        {
            if (lines.Count == 0)
            {
                return false;
            }
            var last = lines[^1];
            if (!HasPipe(r, hi) || !HasPipe(last.Lo, last.Hi))
            {
                return false;
            }
            var delimiter = TableCells(r, hi, rowCells);
            if (!delimiter.TrueForAll(IsDelimiterCell))
            {
                return false;
            }
            var cells = TableCells(last.Lo, last.Hi, []);
            if (cells.Count != delimiter.Count)
            {
                return false;
            }
            lines.RemoveAt(lines.Count - 1);
            if (lines.Count > 0)
            {
                Flush();
            }
            open = null;
            lines.Clear();
            indents.Clear();
            header = cells;
            budget = 0;
            return true;
        }

        // tableRow: a row of the open table as a bullet: the first cell in
        // bold, then "header: cell" for every other cell that is not empty,
        // each on its own line. A row with no text adds nothing.
        private void TableRow(int r, int hi)
        {
            if (header is null)
            {
                return;
            }
            budget += hi - r;
            var cells = TableCells(r, hi, rowCells);
            var spans = new List<MarkdownSpan>();
            // The fields are written as they come, each on its own line (a
            // newline after the first cell's spans, as the first cell comes
            // first).
            rowText.ResetWrittenCount();
            var fields = 0;
            for (var i = 0; i < cells.Count && i < header.Count; i++)
            {
                var c = cells[i];
                if (c.Length == 0)
                {
                    continue;
                }
                if (i == 0)
                {
                    AddInlineSpans(spans, c, bold: true);
                    continue;
                }
                if (fields > 0 || spans.Count > 0)
                {
                    rowText.Write("\n"u8);
                }
                fields++;
                if (header[i].Length > 0 && header[i].Length + 2 <= budget)
                {
                    budget -= header[i].Length + 2;
                    rowText.Write(header[i]);
                    rowText.Write(": "u8);
                }
                rowText.Write(c);
            }
            if (fields > 0)
            {
                AddInlineSpans(spans, rowText.WrittenSpan, bold: false);
            }
            if (spans.Count > 0)
            {
                Blocks.Add(new MarkdownBlock { Kind = MarkdownBlockKind.Bullet, Spans = spans });
            }
        }

        // hasPipe: whether s[lo..hi) has a "|" that is not escaped, as
        // TableCells reads it.
        private bool HasPipe(int lo, int hi)
        {
            var i = lo;
            while (i < hi)
            {
                if (s[i] == 0x5C && i + 1 < hi && s[i + 1] == 0x7C)
                {
                    i += 2;
                    continue;
                }
                if (s[i] == 0x7C)
                {
                    return true;
                }
                i++;
            }
            return false;
        }

        // tableCells: the cells of a table row, the text between its
        // unescaped pipes, one pipe at each edge dropped, "\|" read as "|",
        // every cell trimmed of spaces and tabs; written into cells, which
        // is returned.
        private List<byte[]> TableCells(int lo, int hi, List<byte[]> cells)
        {
            cells.Clear();
            (lo, hi) = TrimST(lo, hi);
            if (lo < hi && s[lo] == 0x7C)
            {
                lo++;
            }
            if (hi > lo && s[hi - 1] == 0x7C && !(hi - lo >= 2 && s[hi - 2] == 0x5C))
            {
                hi--;
            }
            var cell = cellBytes;
            cell.Clear();
            var i = lo;
            while (i < hi)
            {
                if (s[i] == 0x5C && i + 1 < hi && s[i + 1] == 0x7C)
                {
                    cell.Add(0x7C);
                    i += 2;
                    continue;
                }
                if (s[i] == 0x7C)
                {
                    cells.Add(TrimCell(cell));
                    cell.Clear();
                }
                else
                {
                    cell.Add(s[i]);
                }
                i++;
            }
            cells.Add(TrimCell(cell));
            return cells;
        }

        // The spans of text (inlineSpans) added to spans, each made bold
        // when bold is.
        private static void AddInlineSpans(List<MarkdownSpan> spans, ReadOnlySpan<byte> text, bool bold)
        {
            if (text.IsEmpty)
            {
                return;
            }
            if (text.IndexOfAny(InlineMarkers) < 0)
            {
                spans.Add(new MarkdownSpan { Text = FromUtf8(text), Bold = bold });
                return;
            }
            foreach (var span in InlineSpans(text.ToArray()))
            {
                spans.Add(bold ? span with { Bold = true } : span);
            }
        }

        // strings.Trim(cell, " \t").
        private static byte[] TrimCell(List<byte> b)
        {
            var lo = 0;
            var hi = b.Count;
            while (lo < hi && (b[lo] == 0x20 || b[lo] == 0x09))
            {
                lo++;
            }
            while (hi > lo && (b[hi - 1] == 0x20 || b[hi - 1] == 0x09))
            {
                hi--;
            }
            return CollectionsMarshal.AsSpan(b)[lo..hi].ToArray();
        }

        // delimiterCell: whether c is a delimiter row's cell: dashes, with a
        // colon at either end or both.
        private static bool IsDelimiterCell(byte[] c)
        {
            var lo = 0;
            var hi = c.Length;
            if (lo < hi && c[lo] == 0x3A)
            {
                lo++;
            }
            if (hi > lo && c[hi - 1] == 0x3A)
            {
                hi--;
            }
            return lo < hi && Array.TrueForAll(c[lo..hi], b => b == 0x2D);
        }

        // Ends the open paragraph or list item.
        private void Flush()
        {
            if (open is null)
            {
                return;
            }
            var text = lines.Count == 1 ? s[lines[0].Lo..lines[0].Hi] : Joined(lines);
            Blocks.Add(open with { Spans = InlineSpans(text) });
            open = null;
            lines.Clear();
        }

        private void CloseCode()
        {
            var b = new MarkdownBlock { Kind = MarkdownBlockKind.Code };
            var text = Joined(code);
            if (text.Length > 0)
            {
                b = b with { Spans = [new MarkdownSpan { Text = FromUtf8(text), Code = true }] };
            }
            Blocks.Add(b);
            inCode = false;
            code.Clear();
        }

        // The lines joined with "\n".
        private byte[] Joined(List<(int Lo, int Hi)> ranges)
        {
            var size = 0;
            foreach (var (lo, hi) in ranges)
            {
                size += hi - lo + 1;
            }
            var output = new byte[Math.Max(size - 1, 0)];
            var w = 0;
            for (var k = 0; k < ranges.Count; k++)
            {
                if (k > 0)
                {
                    output[w] = 0x0A;
                    w++;
                }
                var (lo, hi) = ranges[k];
                s.AsSpan(lo, hi - lo).CopyTo(output.AsSpan(w));
                w += hi - lo;
            }
            return output;
        }

        // listLevel: the level of a list item indented by indent columns.
        private int ListLevel(int indent)
        {
            while (indents.Count > 0 && indents[^1] > indent)
            {
                indents.RemoveAt(indents.Count - 1);
            }
            if (indents.Count == 0 || indents[^1] < indent)
            {
                indents.Add(indent);
            }
            return Math.Min(indents.Count - 1, MaxListLevel);
        }

        // A line of three backticks from r on.
        private bool IsFence(int r, int hi) => hi - r >= 3 && s[r] == 0x60 && s[r + 1] == 0x60 && s[r + 2] == 0x60;

        // strings.Trim(s[lo..hi), " \t").
        private (int Lo, int Hi) TrimST(int lo, int hi)
        {
            while (lo < hi && (s[lo] == 0x20 || s[lo] == 0x09))
            {
                lo++;
            }
            while (hi > lo && (s[hi - 1] == 0x20 || s[hi - 1] == 0x09))
            {
                hi--;
            }
            return (lo, hi);
        }

        // heading: "#", "##" or "###", a space or a tab and a text.
        private bool Heading(int r, int hi, out int level, out (int Lo, int Hi) text)
        {
            var n = 0;
            while (r + n < hi && s[r + n] == 0x23)
            {
                n++;
            }
            level = n;
            text = default;
            if (!(n >= 1 && n <= 3 && r + n < hi && (s[r + n] == 0x20 || s[r + n] == 0x09)))
            {
                return false;
            }
            text = TrimST(r + n, hi);
            return text.Hi > text.Lo;
        }

        // listItem: "- ", "* " or 1 to 9 digits and ". ", and a text.
        private bool ListItem(int r, int hi, out MarkdownBlockKind kind, out int number, out (int Lo, int Hi) text)
        {
            kind = MarkdownBlockKind.Bullet;
            number = 0;
            text = default;
            if (hi - r >= 2 && (s[r] == 0x2D || s[r] == 0x2A) && s[r + 1] == 0x20)
            {
                text = TrimST(r + 2, hi);
                return text.Hi > text.Lo;
            }
            var n = 0;
            var digits = 0;
            while (r + digits < hi && s[r + digits] is >= 0x30 and <= 0x39)
            {
                if (digits == 9)
                {
                    return false;
                }
                n = (n * 10) + (s[r + digits] - 0x30);
                digits++;
            }
            if (!(digits > 0 && r + digits + 1 < hi && s[r + digits] == 0x2E && s[r + digits + 1] == 0x20))
            {
                return false;
            }
            kind = MarkdownBlockKind.Numbered;
            number = n;
            text = TrimST(r + digits + 2, hi);
            return text.Hi > text.Lo;
        }
    }

    // Inline

    /// <summary>
    /// closers: the positions of one kind of closing marker in a block's
    /// text, ascending, with a cursor that only moves forward.
    /// </summary>
    private sealed class Closers
    {
        private int k;

        public List<int> Pos { get; } = [];

        // The first position at or after q; -1 when there is none.
        // Successive calls must not ask for a smaller q.
        public int Next(int q)
        {
            while (k < Pos.Count && Pos[k] < q)
            {
                k++;
            }
            return k < Pos.Count ? Pos[k] : -1;
        }
    }

    /// <summary>inliner: reads the inline constructs of one block's text.</summary>
    private sealed class Inliner
    {
        private readonly byte[] s;
        private readonly int n;

        // The closing markers: `, **, *, _, ] and ); opens are the "(".
        private readonly Closers ticks = new();
        private readonly Closers bolds = new();
        private readonly Closers stars = new();
        private readonly Closers unders = new();
        private readonly Closers brackets = new();
        private readonly Closers parens = new();
        private readonly Closers opens = new();

        // The cursors of the link targets and of the bare URLs, made when
        // the first of their kind is read.
        private UrlScan? linkScan;
        private UrlScan? bareScan;

        // The open span's text.
        private readonly ArrayBufferWriter<byte> buf = new();

        // The cached target of the last "](": the ] it follows, the ) that
        // ends it (-1 for none) and whether the URL between is a web URL.
        private int linkAt = -1;
        private int linkEnd = -1;
        private bool linkOK;

        // The last newline before nlScan.
        private int nlScan;
        private int nlLast = -1;

        // The open span's style (its text is in buf), if has.
        private MarkdownSpan cur = new() { Text = "" };
        private bool has;

        public Inliner(byte[] s)
        {
            this.s = s;
            n = s.Length;
            for (var j = 0; j < n; j++)
            {
                switch (s[j])
                {
                    case 0x60: // `
                        ticks.Pos.Add(j);
                        break;
                    case 0x2A: // *
                        var next = j + 1 < n ? s[j + 1] : 0;
                        if (next == 0x2A && j > 0 && !SpaceBefore(j))
                        {
                            bolds.Pos.Add(j);
                        }
                        if (j > 0 && s[j - 1] != 0x2A && next != 0x2A && !SpaceBefore(j))
                        {
                            stars.Pos.Add(j);
                        }
                        break;
                    case 0x5F: // _
                        if (j > 0 && s[j - 1] != 0x5F && !SpaceBefore(j) && (j + 1 == n || (s[j + 1] != 0x5F && !WordAt(j + 1))))
                        {
                            unders.Pos.Add(j);
                        }
                        break;
                    case 0x5D: // ]
                        brackets.Pos.Add(j);
                        break;
                    case 0x29: // )
                        parens.Pos.Add(j);
                        break;
                    case 0x28: // (
                        opens.Pos.Add(j);
                        break;
                    default:
                        break;
                }
            }
        }

        // The finished spans.
        public List<MarkdownSpan> Spans { get; } = [];

        // run: reads s[lo..hi) with the style bold and italic around it.
        public void Run(int lo, int hi, bool bold, bool italic)
        {
            var lit = lo;
            var i = lo;
            while (i < hi)
            {
                var next = -1;
                switch (s[i])
                {
                    case 0x60: // `
                        {
                            var j = ticks.Next(i + 1);
                            if (j > i + 1 && j < hi)
                            {
                                Text(lit, i, bold, italic);
                                Add(i + 1, j, bold, italic, code: true, link: "");
                                next = j + 1;
                            }
                            break;
                        }
                    case 0x2A: // *
                        if (i + 1 < hi && s[i + 1] == 0x2A)
                        {
                            if (!bold && OpensAt(i + 2, hi))
                            {
                                var j = bolds.Next(i + 3);
                                if (j >= 0 && j + 2 <= hi)
                                {
                                    Text(lit, i, bold, italic);
                                    Run(i + 2, j, true, italic);
                                    next = j + 2;
                                }
                            }
                            if (next < 0)
                            {
                                i += 2; // a literal "**" stays a pair
                                continue;
                            }
                        }
                        else if (!italic && OpensAt(i + 1, hi))
                        {
                            var j = stars.Next(i + 2);
                            if (j >= 0 && j < hi)
                            {
                                Text(lit, i, bold, italic);
                                Run(i + 1, j, bold, true);
                                next = j + 1;
                            }
                        }
                        break;
                    case 0x5F: // _
                        if (!italic && i + 1 < hi && s[i + 1] != 0x5F && OpensAt(i + 1, hi) && !WordBefore(i))
                        {
                            var j = unders.Next(i + 2);
                            if (j >= 0 && j < hi)
                            {
                                Text(lit, i, bold, italic);
                                Run(i + 1, j, bold, true);
                                next = j + 1;
                            }
                        }
                        break;
                    case 0x5B: // [
                        if (Link(i, hi, out var close, out var end))
                        {
                            Text(lit, i, bold, italic);
                            Add(i + 1, close, bold, italic, code: false, link: StringOf(close + 2, end));
                            next = end + 1;
                        }
                        break;
                    case 0x68 or 0x48: // h H
                        if (!WordBefore(i) && HasWebScheme(s, i, hi))
                        {
                            var (runEnd, u) = BareUrl(i, hi);
                            if (u < 0)
                            {
                                i = runEnd; // not a URL: literal text, never read again
                                continue;
                            }
                            Text(lit, i, bold, italic);
                            Add(i, u, bold, italic, code: false, link: StringOf(i, u));
                            next = u;
                        }
                        break;
                    default:
                        break;
                }
                if (next >= 0)
                {
                    lit = next;
                    i = next;
                }
                else
                {
                    i++;
                }
            }
            Text(lit, hi, bold, italic);
        }

        public void CloseSpan()
        {
            if (!has)
            {
                return;
            }
            Spans.Add(cur with { Text = FromUtf8(buf.WrittenSpan) });
            buf.ResetWrittenCount();
            has = false;
        }

        // link: reads "[text](url)" at i within hi: the positions of "]("
        // (close) and ")" (end), when it is a link (text non-empty and on one
        // line, url a web URL without a "(" of its own, as in Go: every later
        // "](" that ends at the same ")" puts its "(" inside this target, so
        // the targets read as URLs never overlap).
        private bool Link(int i, int hi, out int close, out int end)
        {
            var j = brackets.Next(i + 1);
            close = j;
            end = -1;
            if (!(j > i + 1 && j + 1 < hi && s[j + 1] == 0x28))
            {
                return false;
            }
            if (j != linkAt)
            {
                linkAt = j;
                linkEnd = parens.Next(j + 2);
                var open = opens.Next(j + 2);
                linkOK = linkEnd >= 0 && (open < 0 || open > linkEnd) && IsWebUrl(s, j + 2, linkEnd, linkScan ??= new());
            }
            if (!(linkOK && linkEnd < hi && NewlineBefore(j) <= i))
            {
                return false;
            }
            end = linkEnd;
            return true;
        }

        // newlineBefore: the position of the last "\n" before j, -1 when
        // there is none. Successive calls must not ask for a smaller j.
        private int NewlineBefore(int j)
        {
            while (nlScan < j)
            {
                if (s[nlScan] == 0x0A)
                {
                    nlLast = nlScan;
                }
                nlScan++;
            }
            return nlLast;
        }

        // bareURL: reads the bare URL at i within hi: where its run of URL
        // characters ends (white space, a control character or one of
        // <>"`[]{}|\^ end it), and the end of the URL without trailing
        // punctuation (-1 when that is not a web URL).
        private (int End, int Url) BareUrl(int i, int hi)
        {
            var end = i;
            var opens = 0;
            var closes = 0;
            while (end < hi)
            {
                var c = s[end];
                if (c < 0x80)
                {
                    if (c is 0x3C or 0x3E or 0x22 or 0x60 or 0x5B or 0x5D or 0x7B or 0x7D or 0x7C or 0x5C or 0x5E) // <>"`[]{}|\^
                    {
                        break;
                    }
                    if (c == 0x28)
                    {
                        opens++;
                    }
                    else if (c == 0x29)
                    {
                        closes++;
                    }
                }
                var (r, w) = DecodeRune(s, end, n);
                if (IsSpace(r) || IsControl(r))
                {
                    break;
                }
                end += w;
            }
            var j = end;
            while (j > i)
            {
                var c = s[j - 1];
                if (c is 0x2E or 0x2C or 0x3B or 0x3A or 0x21 or 0x3F or 0x27 or 0x2A or 0x5F) // .,;:!?'*_
                {
                    j--;
                    continue;
                }
                if (c == 0x29 && closes > opens)
                {
                    closes--;
                    j--;
                    continue;
                }
                break;
            }
            return (end, IsWebUrl(s, i, j, bareScan ??= new()) ? j : -1);
        }

        private string StringOf(int lo, int hi) => FromUtf8(s.AsSpan(lo, hi - lo));

        // text: literal text in the style bold and italic.
        private void Text(int lo, int hi, bool bold, bool italic) => Add(lo, hi, bold, italic, code: false, link: "");

        // add: s[lo..hi) in a style, merged into the span before it when
        // their styles are the same; empty text adds nothing.
        private void Add(int lo, int hi, bool bold, bool italic, bool code, string link)
        {
            if (hi <= lo)
            {
                return;
            }
            if (!(has && cur.Bold == bold && cur.Italic == italic && cur.Code == code && cur.Link == link))
            {
                CloseSpan();
                cur = new MarkdownSpan { Text = "", Bold = bold, Italic = italic, Code = code, Link = link };
                has = true;
            }
            buf.Write(s.AsSpan(lo, hi - lo));
        }

        // opensAt: whether an opening marker can end before k: k is within
        // hi and no space follows the marker.
        private bool OpensAt(int k, int hi)
        {
            if (k >= hi)
            {
                return false;
            }
            var c = s[k];
            if (c < 0x80)
            {
                return !(c == 0x20 || c is >= 0x09 and <= 0x0D);
            }
            return !IsSpace(DecodeRune(s, k, n).Rune);
        }

        // spaceBefore: whether the character before j is a space.
        private bool SpaceBefore(int j)
        {
            var c = s[j - 1];
            if (c < 0x80)
            {
                return c == 0x20 || c is >= 0x09 and <= 0x0D;
            }
            return IsSpace(DecodeLastRune(s, 0, j).Rune);
        }

        // wordBefore: whether a letter or digit comes right before i.
        private bool WordBefore(int i)
        {
            if (i <= 0)
            {
                return false;
            }
            var c = s[i - 1];
            if (c < 0x80)
            {
                return AsciiWord(c);
            }
            var r = DecodeLastRune(s, 0, i).Rune;
            return IsLetter(r) || IsDigit(r);
        }

        // wordAt: whether a letter or digit starts at i.
        private bool WordAt(int i)
        {
            if (i >= n)
            {
                return false;
            }
            var c = s[i];
            if (c < 0x80)
            {
                return AsciiWord(c);
            }
            var r = DecodeRune(s, i, n).Rune;
            return IsLetter(r) || IsDigit(r);
        }

        private static bool AsciiWord(byte c) => c is (>= 0x30 and <= 0x39) or (>= 0x41 and <= 0x5A) or (>= 0x61 and <= 0x7A);
    }

    // Go's url.Parse, as far as isWebURL asks

    /// <summary>
    /// URLScan: cursors over one text for the URLs read in it. Each answers
    /// "the first position at or after q" of one kind and remembers the
    /// answer, so that URLs read left to right, overlapping or not, cost one
    /// pass. Each cursor serves one kind of question, whose positions grow
    /// from one URL to the next; any order is still answered correctly, only
    /// more slowly.
    /// </summary>
    private sealed class UrlScan
    {
        /// <summary>A character isWebURL refuses: white space, a control character, ", &lt;, &gt;, ` or \.</summary>
        public Finder Bad { get; } = new(FinderKind.Bad);

        public Finder Hash { get; } = new(FinderKind.Byte, 0x23);

        public Finder Question { get; } = new(FinderKind.Byte, 0x3F);

        public Finder Slash { get; } = new(FinderKind.Byte, 0x2F);

        /// <summary>A "%" without two hex digits after it, from the path.</summary>
        public Finder PathPercent { get; } = new(FinderKind.BadPercent);

        /// <summary>A "%" without two hex digits after it, from the fragment.</summary>
        public Finder FragmentPercent { get; } = new(FinderKind.BadPercent);
    }

    /// <summary>What a <see cref="Finder"/> looks for (Swift's Finder.Kind).</summary>
    private enum FinderKind
    {
        /// <summary>A character isWebURL refuses.</summary>
        Bad,

        /// <summary>One byte.</summary>
        Byte,

        /// <summary>A "%" without two hex digits after it.</summary>
        BadPercent,
    }

    /// <summary>One cursor of a <see cref="UrlScan"/>.</summary>
    private sealed class Finder(FinderKind kind, byte b = 0)
    {
        // Nothing of the kind in [from, at); at is one, or the end.
        private int from = int.MaxValue;
        private int at = int.MaxValue;

        // The first position at or after q (on a character boundary) that
        // is of the kind; the text's length when there is none.
        public int Next(ReadOnlySpan<byte> s, int q)
        {
            if (q >= from && q <= at)
            {
                return at;
            }
            var i = q;
            while (i < s.Length)
            {
                if (i >= from && i <= at)
                {
                    i = at;
                    break;
                }
                var (hit, w) = Matches(s, i);
                if (hit)
                {
                    break;
                }
                i += w;
            }
            from = q;
            at = i;
            return i;
        }

        private (bool Hit, int Width) Matches(ReadOnlySpan<byte> s, int i)
        {
            var c = s[i];
            switch (kind)
            {
                case FinderKind.Byte:
                    return (c == b, 1);
                case FinderKind.BadPercent:
                    if (c != 0x25)
                    {
                        return (false, 1);
                    }
                    return (!(i + 2 < s.Length && IsHexDigit(s[i + 1]) && IsHexDigit(s[i + 2])), 1);
                default:
                    if (c < 0x80)
                    {
                        return (c <= 0x20 || c is 0x7F or 0x22 or 0x3C or 0x3E or 0x60 or 0x5C, 1);
                    }
                    var (r, w) = DecodeRune(s, i, s.Length);
                    return (IsSpace(r) || IsControl(r), w);
            }
        }
    }

    private static bool IsHexDigit(byte c) => c is (>= 0x30 and <= 0x39) or (>= 0x41 and <= 0x46) or (>= 0x61 and <= 0x66);

    private static byte HexValue(byte c) => c switch
    {
        >= 0x30 and <= 0x39 => (byte)(c - 0x30),
        >= 0x41 and <= 0x46 => (byte)(c - 0x41 + 10),
        _ => (byte)(c - 0x61 + 10),
    };

    /// <summary>
    /// shouldEscape(c, encodeHost) == false: what an ASCII byte of a host may
    /// be: a letter, a digit or one of !$&amp;'()*+,;=:[]&lt;&gt;"-_.~
    /// </summary>
    private static bool HostByte(byte c) =>
        c is (>= 0x30 and <= 0x39) or (>= 0x41 and <= 0x5A) or (>= 0x61 and <= 0x7A)
            or 0x21 or 0x24 or 0x26 or 0x27 or 0x28 or 0x29 or 0x2A or 0x2B or 0x2C or 0x3B or 0x3D or 0x3A or 0x5B
            or 0x5D or 0x3C or 0x3E or 0x22 or 0x2D or 0x5F or 0x2E or 0x7E;

    /// <summary>validUserinfo: a letter, a digit or one of -._:~!$&amp;'()*+,;=%@</summary>
    private static bool UserinfoByte(byte c) =>
        c is (>= 0x30 and <= 0x39) or (>= 0x41 and <= 0x5A) or (>= 0x61 and <= 0x7A)
            or 0x2D or 0x2E or 0x5F or 0x3A or 0x7E or 0x21 or 0x24 or 0x26 or 0x27 or 0x28 or 0x29 or 0x2A or 0x2B
            or 0x2C or 0x3B or 0x3D or 0x25 or 0x40;

    /// <summary>
    /// Whether url.Parse reads <paramref name="s"/>[<paramref name="lo"/>..<paramref name="hi"/>)
    /// (an http or https URL without the characters isWebURL refuses) and
    /// its Hostname() is not empty.
    /// </summary>
    private static bool GoUrlHostname(ReadOnlySpan<byte> s, int lo, int hi, UrlScan scan)
    {
        // Parse: "#fragment" cut off first, its escapes checked last.
        var hash = Math.Min(scan.Hash.Next(s, lo), hi);
        // parse: the scheme ("http" or "https") and its ":", then the
        // query cut off at the first "?", unchecked.
        var restStart = lo;
        while (s[restStart] != 0x3A)
        {
            restStart++;
        }
        restStart++;
        var restEnd = Math.Min(scan.Question.Next(s, restStart), hash);
        // "//authority/path": the authority ends at the first "/".
        var authStart = restStart + 2;
        if (authStart > restEnd)
        {
            return false;
        }
        var authEnd = Math.Min(scan.Slash.Next(s, authStart), restEnd);
        var host = ParseAuthority(s, authStart, authEnd);
        if (host is null)
        {
            return false;
        }
        // setPath and setFragment: only the escapes can fail.
        if (!EscapesOk(s, authEnd, restEnd, scan.PathPercent))
        {
            return false;
        }
        if (hash < hi && !EscapesOk(s, hash + 1, hi, scan.FragmentPercent))
        {
            return false;
        }
        return !Hostname(CollectionsMarshal.AsSpan(host)).IsEmpty;
    }

    /// <summary>
    /// unescape of <paramref name="s"/>[<paramref name="lo"/>..<paramref name="hi"/>)
    /// in a mode without character rules (path, fragment): every "%" has two
    /// hex digits after it within the range.
    /// </summary>
    private static bool EscapesOk(ReadOnlySpan<byte> s, int lo, int hi, Finder badPercent)
    {
        if (lo >= hi)
        {
            return true;
        }
        if (badPercent.Next(s, lo) < hi)
        {
            return false;
        }
        // A "%" in the last two bytes lacks its digits within the range.
        return !(s[hi - 1] == 0x25 || (hi - 2 >= lo && s[hi - 2] == 0x25));
    }

    /// <summary>Escapes checked directly (the userinfo, which is short).</summary>
    private static bool EscapesOk(ReadOnlySpan<byte> s, int lo, int hi)
    {
        var i = lo;
        while (i < hi)
        {
            if (s[i] == 0x25)
            {
                if (!(i + 2 < hi && IsHexDigit(s[i + 1]) && IsHexDigit(s[i + 2])))
                {
                    return false;
                }
                i += 3;
            }
            else
            {
                i++;
            }
        }
        return true;
    }

    /// <summary>The last position of <paramref name="c"/> in <paramref name="s"/>[<paramref name="lo"/>..<paramref name="hi"/>); -1 for none.</summary>
    private static int LastIndex(ReadOnlySpan<byte> s, byte c, int lo, int hi)
    {
        for (var k = hi - 1; k >= lo; k--)
        {
            if (s[k] == c)
            {
                return k;
            }
        }
        return -1;
    }

    /// <summary>parseAuthority: the host (unescaped), null on an error of the host or the userinfo.</summary>
    private static List<byte>? ParseAuthority(ReadOnlySpan<byte> s, int lo, int hi)
    {
        var at = LastIndex(s, 0x40, lo, hi);
        var host = ParseHost(s, at >= 0 ? at + 1 : lo, hi);
        if (host is null || at < 0)
        {
            return host;
        }
        // validUserinfo (ASCII only), then the escapes of the user and the
        // password.
        for (var i = lo; i < at; i++)
        {
            if (!UserinfoByte(s[i]))
            {
                return null;
            }
        }
        var colon = lo;
        while (colon < at && s[colon] != 0x3A)
        {
            colon++;
        }
        if (colon < at)
        {
            if (!(EscapesOk(s, lo, colon) && EscapesOk(s, colon + 1, at)))
            {
                return null;
            }
        }
        else if (!EscapesOk(s, lo, at))
        {
            return null;
        }
        return host;
    }

    /// <summary>parseHost: the host (unescaped), null when url.Parse refuses it.</summary>
    private static List<byte>? ParseHost(ReadOnlySpan<byte> s, int lo, int hi)
    {
        var lastOpen = LastIndex(s, 0x5B, lo, hi);
        if (lastOpen > lo)
        {
            return null; // invalid IP-literal
        }
        if (lastOpen == lo)
        {
            var close = LastIndex(s, 0x5D, lo, hi);
            if (close < 0 || !ValidOptionalPort(s, close + 1, hi))
            {
                return null;
            }
            var nameLo = lo + 1;
            var nameHi = close;
            var zone = nameLo;
            while (zone + 2 < nameHi && !(s[zone] == 0x25 && s[zone + 1] == 0x32 && s[zone + 2] == 0x35))
            {
                zone++;
            }
            List<byte>? unescaped;
            if (zone + 2 < nameHi)
            {
                unescaped = UnescapeHost(s, nameLo, zone, isZone: false);
                var z = unescaped is null ? null : UnescapeHost(s, zone, nameHi, isZone: true);
                if (unescaped is null || z is null)
                {
                    return null;
                }
                unescaped.AddRange(z);
            }
            else
            {
                unescaped = UnescapeHost(s, nameLo, nameHi, isZone: false);
                if (unescaped is null)
                {
                    return null;
                }
            }
            if (!Ipv6Literal(CollectionsMarshal.AsSpan(unescaped)))
            {
                return null;
            }
            unescaped.Insert(0, 0x5B);
            unescaped.Add(0x5D);
            unescaped.AddRange(s[(close + 1)..hi]);
            return unescaped;
        }
        // Not strict about colons (urlstrictcolons=0, a go 1.25 module):
        // the last one starts the port.
        var colon = LastIndex(s, 0x3A, lo, hi);
        if (colon >= 0 && !ValidOptionalPort(s, colon, hi))
        {
            return null;
        }
        return UnescapeHost(s, lo, hi, isZone: false);
    }

    /// <summary>validOptionalPort: "" or ":" and digits.</summary>
    private static bool ValidOptionalPort(ReadOnlySpan<byte> s, int lo, int hi)
    {
        if (lo >= hi)
        {
            return true;
        }
        if (s[lo] != 0x3A)
        {
            return false;
        }
        for (var i = lo + 1; i < hi; i++)
        {
            if (s[i] is < 0x30 or > 0x39)
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>
    /// unescape in the modes encodeHost and encodeZone: a "%" needs two hex
    /// digits and may stand only for a byte from 0x80 up (the host), or for a
    /// host byte or a space (the zone), "%25" always; an ASCII byte must be a
    /// host byte.
    /// </summary>
    private static List<byte>? UnescapeHost(ReadOnlySpan<byte> s, int lo, int hi, bool isZone)
    {
        var output = new List<byte>(hi - lo);
        var i = lo;
        while (i < hi)
        {
            var c = s[i];
            if (c == 0x25)
            {
                if (!(i + 2 < hi && IsHexDigit(s[i + 1]) && IsHexDigit(s[i + 2])))
                {
                    return null;
                }
                var v = (byte)((HexValue(s[i + 1]) << 4) | HexValue(s[i + 2]));
                var pct25 = s[i + 1] == 0x32 && s[i + 2] == 0x35;
                if (!isZone && HexValue(s[i + 1]) < 8 && !pct25)
                {
                    return null;
                }
                if (isZone && !pct25 && v != 0x20 && !(v < 0x80 && HostByte(v)))
                {
                    return null;
                }
                output.Add(v);
                i += 3;
                continue;
            }
            if (c < 0x80 && !HostByte(c))
            {
                return null;
            }
            output.Add(c);
            i++;
        }
        return output;
    }

    /// <summary>Hostname: the host without a valid ":port" and without the brackets of an IPv6 literal.</summary>
    private static ReadOnlySpan<byte> Hostname(ReadOnlySpan<byte> host)
    {
        var h = host;
        var colon = h.LastIndexOf((byte)0x3A);
        if (colon >= 0 && !h[(colon + 1)..].ContainsAnyExceptInRange((byte)0x30, (byte)0x39))
        {
            h = h[..colon];
        }
        if (h.Length >= 2 && h[0] == 0x5B && h[^1] == 0x5D)
        {
            h = h[1..^1];
        }
        return h;
    }

    /// <summary>netip.ParseAddr accepting only IPv6 (with an optional zone): what an IP literal in brackets must be.</summary>
    private static bool Ipv6Literal(ReadOnlySpan<byte> a)
    {
        foreach (var c in a)
        {
            switch (c)
            {
                case 0x2E:
                    return false; // IPv4, or nothing
                case 0x3A:
                    return ParseIpv6(a);
                case 0x25:
                    return false;
                default:
                    continue;
            }
        }
        return false;
    }

    /// <summary>netip's parseIPv6: whether <paramref name="input"/> is an IPv6 address, an embedded IPv4 and a zone allowed.</summary>
    private static bool ParseIpv6(ReadOnlySpan<byte> input)
    {
        var s = input;
        var pct = input.IndexOf((byte)0x25);
        if (pct >= 0)
        {
            if (pct + 1 >= input.Length)
            {
                return false; // an empty zone
            }
            s = input[..pct];
        }
        var ellipsis = -1;
        if (s.Length >= 2 && s[0] == 0x3A && s[1] == 0x3A)
        {
            ellipsis = 0;
            s = s[2..];
            if (s.IsEmpty)
            {
                return true;
            }
        }
        var i = 0;
        while (i < 16)
        {
            var off = 0;
            var acc = 0u;
            while (off < s.Length)
            {
                var c = s[off];
                if (!IsHexDigit(c))
                {
                    break;
                }
                acc = (acc << 4) + HexValue(c);
                if (off > 3 || acc > 0xFFFF)
                {
                    return false;
                }
                off++;
            }
            if (off == 0)
            {
                return false;
            }
            if (off < s.Length && s[off] == 0x2E)
            {
                if (ellipsis < 0 && i != 12)
                {
                    return false;
                }
                if (i + 4 > 16)
                {
                    return false;
                }
                if (!Ipv4Fields(s))
                {
                    return false;
                }
                s = [];
                i += 4;
                break;
            }
            i += 2;
            s = s[off..];
            if (s.IsEmpty)
            {
                break;
            }
            if (s[0] != 0x3A || s.Length <= 1)
            {
                return false;
            }
            s = s[1..];
            if (s[0] == 0x3A)
            {
                if (ellipsis >= 0)
                {
                    return false;
                }
                ellipsis = i;
                s = s[1..];
                if (s.IsEmpty)
                {
                    break;
                }
            }
        }
        if (!s.IsEmpty)
        {
            return false;
        }
        if (i < 16)
        {
            return ellipsis >= 0;
        }
        return ellipsis < 0;
    }

    /// <summary>netip's parseIPv4Fields: four decimal fields 0–255 without leading zeros.</summary>
    private static bool Ipv4Fields(ReadOnlySpan<byte> s)
    {
        var val = 0;
        var pos = 0;
        var digLen = 0;
        for (var i = 0; i < s.Length; i++)
        {
            var c = s[i];
            if (c is >= 0x30 and <= 0x39)
            {
                if (digLen == 1 && val == 0)
                {
                    return false;
                }
                val = (val * 10) + (c - 0x30);
                digLen++;
                if (val > 255)
                {
                    return false;
                }
            }
            else if (c == 0x2E)
            {
                if (i == 0 || i == s.Length - 1 || s[i - 1] == 0x2E)
                {
                    return false;
                }
                if (pos == 3)
                {
                    return false;
                }
                pos++;
                val = 0;
                digLen = 0;
            }
            else
            {
                return false;
            }
        }
        return pos >= 3;
    }
}
