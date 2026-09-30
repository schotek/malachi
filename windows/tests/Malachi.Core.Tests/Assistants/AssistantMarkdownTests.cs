// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantMarkdownTests.swift, the
// counterpart of ui/internal/assistant/markdown_test.go (TestMarkdownBlocks,
// TestMarkdownInline, TestMarkdownLinksAreWebURLs,
// TestMarkdownLinkTargetWithParens, TestMarkdownIsLinear): the Markdown
// subset of the panel's answers. A C# string holds no invalid UTF-8 (a lone
// surrogate is U+FFFD once encoded), so Go's cases of it are left out, as
// Swift leaves them out (U+FFFD stands in for them in the linearity inputs).
// IsLinear plays Swift's inputs (the four link-target ones among them are
// Swift's own) and the three of Go's that Swift lacks ("openers, one
// paren2", "openers, text paren", "openers, no scheme"; Go's "openers, one
// paren" is Swift's "link targets to one parenthesis"), under Swift's bounds:
// 3 s, and spans of at most three times the input's bytes.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Text;
using Malachi.Core.Assistants;
using Xunit;
using static Malachi.Core.Assistants.MarkdownBlockKind;

namespace Malachi.Core.Tests.Assistants;

public sealed class AssistantMarkdownTests
{
    private const int Mb = 1 << 20;

    public static TheoryData<string, string, MarkdownBlock[]> BlockCases => new()
    {
        { "empty", "", [] },
        { "only blank lines", "\n  \n\t\n", [] },
        { "a paragraph", "Hello world", [Para("Hello world")] },
        { "lines stay lines, blank lines split", "a\n  b  \n\n\nc", [Para("a\nb"), Para("c")] },
        { "line endings", "a\r\nb\rc\n\r\nd", [Para("a\nb\nc"), Para("d")] },
        {
            "headings", "# One\n## Two\n### Three",
            [B(Heading, Plain("One"), level: 1), B(Heading, Plain("Two"), level: 2), B(Heading, Plain("Three"), level: 3)]
        },
        { "what is no heading", "#### Four\n#NoSpace\n# \n    # indented four", [Para("#### Four\n#NoSpace\n#\n# indented four")] },
        { "a heading indented three spaces, with a tab", "   ##\tTitle  ", [B(Heading, Plain("Title"), level: 2)] },
        { "a heading ends a paragraph", "text\n# Head\nmore", [Para("text"), B(Heading, Plain("Head"), level: 1), Para("more")] },
        { "bullets", "- one\n* two", [B(Bullet, Plain("one")), B(Bullet, Plain("two"))] },
        {
            "numbered", "1. First\n2. Second\n10. Tenth\n007. Bond",
            [
                B(Numbered, Plain("First"), number: 1),
                B(Numbered, Plain("Second"), number: 2),
                B(Numbered, Plain("Tenth"), number: 10),
                B(Numbered, Plain("Bond"), number: 7),
            ]
        },
        {
            "nesting", "- a\n  - b\n    - c\n  - d\n- e\n\t- f",
            [
                B(Bullet, Plain("a"), level: 0),
                B(Bullet, Plain("b"), level: 1),
                B(Bullet, Plain("c"), level: 2),
                B(Bullet, Plain("d"), level: 1),
                B(Bullet, Plain("e"), level: 0),
                B(Bullet, Plain("f"), level: 1),
            ]
        },
        {
            "nested under a numbered item by three or four spaces", "1. a\n   - b\n2. c\n    - d",
            [
                B(Numbered, Plain("a"), number: 1),
                B(Bullet, Plain("b"), level: 1),
                B(Numbered, Plain("c"), number: 2),
                B(Bullet, Plain("d"), level: 1),
            ]
        },
        {
            "nesting stops at the limit", "- 0\n - 1\n  - 2\n   - 3\n    - 4\n     - 5\n - back",
            [
                B(Bullet, Plain("0"), level: 0),
                B(Bullet, Plain("1"), level: 1),
                B(Bullet, Plain("2"), level: 2),
                B(Bullet, Plain("3"), level: 3),
                B(Bullet, Plain("4"), level: 3),
                B(Bullet, Plain("5"), level: 3),
                B(Bullet, Plain("back"), level: 1),
            ]
        },
        { "a list survives a blank line", "- a\n\n  - b", [B(Bullet, Plain("a"), level: 0), B(Bullet, Plain("b"), level: 1)] },
        {
            "a paragraph starts the list afresh", "- a\n\ntext\n\n  - b",
            [B(Bullet, Plain("a"), level: 0), Para("text"), B(Bullet, Plain("b"), level: 0)]
        },
        { "an item continues", "- item\ncontinued\n  more", [B(Bullet, Plain("item\ncontinued\nmore"))] },
        { "an item ends a paragraph", "text\n- item", [Para("text"), B(Bullet, Plain("item"))] },
        {
            "markers without text, and what is no marker",
            "- \n* \n1. \n-no\n1.5 million\n1234567890. ten digits\n+ plus\n1) paren",
            [Para("-\n*\n1.\n-no\n1.5 million\n1234567890. ten digits\n+ plus\n1) paren")]
        },
        {
            "a code block", "```go\nfunc main() {\n\t**not bold** <b>\n}\n```\nafter",
            [B(Code, [S("func main() {\n\t**not bold** <b>\n}", code: true)]), Para("after")]
        },
        { "a code block keeps blank lines and indentation", "```\n  a\n\n  b\n```", [B(Code, [S("  a\n\n  b", code: true)])] },
        { "a fence ends a paragraph", "para\n```\nx\n```", [Para("para"), B(Code, [S("x", code: true)])] },
        {
            "an unclosed fence runs to the end", "text\n```\ncode *x*\n# not a heading",
            [Para("text"), B(Code, [S("code *x*\n# not a heading", code: true)])]
        },
        { "an empty code block", "```\n```", [B(Code, [])] },
        { "a fence indented four spaces is text", "    ```\n    x", [Para("```\nx")] },
        {
            "a table: a bullet per row",
            "| Co | Kdo | Do kdy |\n|---|---|---|\n| Odpovědět Radkovi | Vladislav (vy) | neuvedeno |\n| Rozhodnout o **Pro** | Radek | – |",
            [
                B(Bullet, [S("Odpovědět Radkovi", bold: true), S("\nKdo: Vladislav (vy)\nDo kdy: neuvedeno")]),
                B(Bullet, [S("Rozhodnout o ", bold: true), S("Pro", bold: true), S("\nKdo: Radek\nDo kdy: –")]),
            ]
        },
        {
            "a table among text",
            "Úkoly:\nCo | Kdo\n:--- | ---:\n`a\\|b` | **Radek**\n|  | jen kdo |\n| x |\n| y | z | extra |\nkonec",
            [
                Para("Úkoly:"),
                B(Bullet, [S("a|b", bold: true, code: true), S("\nKdo: "), S("Radek", bold: true)]),
                B(Bullet, Plain("Kdo: jen kdo")),
                B(Bullet, [S("x", bold: true)]),
                B(Bullet, [S("y", bold: true), S("\nKdo: z")]),
                Para("konec"),
            ]
        },
        { "a delimiter of another count is text", "| a | b |\n|---|\n| c | d |", [Para("| a | b |\n|---|\n| c | d |")] },
        { "a header without a pipe is text", "Title\n---|---", [Para("Title\n---|---")] },
        { "rows without a delimiter are text", "| a | b |\n| x | y |", [Para("| a | b |\n| x | y |")] },
        {
            "a table without rows; one ended by a heading",
            "| a | b |\n|-|-|\n\n| c | d |\n|:-:|-|\n| e | f |\n# Head | x",
            [B(Bullet, [S("e", bold: true), S("\nd: f")]), B(Heading, Plain("Head | x"), level: 1)]
        },
        {
            "labels no longer than the rows",
            "| t | " + new string('h', 20) + " |\n|-|-|\n| a | b |\n| c | d |",
            [B(Bullet, [S("a", bold: true), S("\nb")]), B(Bullet, [S("c", bold: true), S("\nd")])]
        },
    };

    public static TheoryData<string, string, MarkdownSpan[]> InlineCases => new()
    {
        { "bold", "a **b** c", [S("a "), S("b", bold: true), S(" c")] },
        { "italic", "*it* and _it_", [S("it", italic: true), S(" and "), S("it", italic: true)] },
        { "snake_case stays", "create_draft and read_message_x", Plain("create_draft and read_message_x") },
        { "an underscore closes at a word's edge only", "_a_b c_", [S("a_b c", italic: true)] },
        {
            "code", "use `a **b** [x](https://x.org)` here",
            [S("use "), S("a **b** [x](https://x.org)", code: true), S(" here")]
        },
        {
            "bold around italic and code", "**bold *it* `c`**",
            [S("bold ", bold: true), S("it", bold: true, italic: true), S(" ", bold: true), S("c", bold: true, code: true)]
        },
        {
            "italic around bold", "*it **b** x*",
            [S("it ", italic: true), S("b", bold: true, italic: true), S(" x", italic: true)]
        },
        { "no bold in bold", "**a **b** c**", [S("a **b", bold: true), S(" c**")] },
        { "no italic in italic", "*a _b_ c*", [S("a _b_ c", italic: true)] },
        { "bold over lines", "**one\ntwo**", [S("one\ntwo", bold: true)] },
        { "adjacent code spans merge", "`a``b`", [S("ab", code: true)] },
        { "an empty code span is text", "`` x", Plain("`` x") },
        { "unclosed bold", "**open", Plain("**open") },
        { "unclosed italic", "*open and _open", Plain("*open and _open") },
        { "unclosed code", "`open", Plain("`open") },
        { "a spaced opener", "** spaced** and * spaced*", Plain("** spaced** and * spaced*") },
        { "a spaced closer", "**a **", Plain("**a **") },
        { "arithmetic", "2 * 3 * 4 and a * b", Plain("2 * 3 * 4 and a * b") },
        { "empty markers", "**** and ** and __", Plain("**** and ** and __") },
        {
            "a link", "see [Malachi](https://github.com/schotek/malachi).",
            [S("see "), S("Malachi", link: "https://github.com/schotek/malachi"), S(".")]
        },
        { "a link in bold", "**[a](http://x.org)**", [S("a", bold: true, link: "http://x.org")] },
        { "an upper-case scheme", "[a](HTTPS://Example.org/P?q=1#f)", [S("a", link: "HTTPS://Example.org/P?q=1#f")] },
        { "link text is literal", "[**a** `b`](https://x.org)", [S("**a** `b`", link: "https://x.org")] },
        { "javascript stays text", "[click](javascript:alert(1))", Plain("[click](javascript:alert(1))") },
        { "file stays text", "[x](file:///etc/passwd)", Plain("[x](file:///etc/passwd)") },
        { "mailto stays text", "[x](mailto:a@b.cz)", Plain("[x](mailto:a@b.cz)") },
        { "data stays text", "[x](data:text/html,<b>hi</b>)", Plain("[x](data:text/html,<b>hi</b>)") },
        { "scheme-relative stays text", "[x](//evil.org)", Plain("[x](//evil.org)") },
        { "no host stays text", "[x](http://) [y](https:///path)", Plain("[x](http://) [y](https:///path)") },
        // Not links; the bare URL in them is, up to the space or quote.
        {
            "a space in the URL", "[x](https://exa mple.org)",
            [S("[x]("), S("https://exa", link: "https://exa"), S(" mple.org)")]
        },
        {
            "a quote in the URL", "[x](https://x.org/\"onmouseover=)",
            [S("[x]("), S("https://x.org/", link: "https://x.org/"), S("\"onmouseover=)")]
        },
        { "no link text", "[](https://x.org)", [S("[]("), S("https://x.org", link: "https://x.org"), S(")")] },
        {
            "link text over lines", "[a\nb](https://x.org)",
            [S("[a\nb]("), S("https://x.org", link: "https://x.org"), S(")")]
        },
        {
            "an unclosed link", "[text](https://x.org and [more]",
            [S("[text]("), S("https://x.org", link: "https://x.org"), S(" and [more]")]
        },
        { "brackets without a link", "[1] and [a] (b)", Plain("[1] and [a] (b)") },
        {
            "bare URLs", "see https://example.org/a_b_(c), and http://x.cz.",
            [
                S("see "), S("https://example.org/a_b_(c)", link: "https://example.org/a_b_(c)"),
                S(", and "), S("http://x.cz", link: "http://x.cz"), S("."),
            ]
        },
        {
            "a bare URL in parentheses", "(see https://x.org/y)",
            [S("(see "), S("https://x.org/y", link: "https://x.org/y"), S(")")]
        },
        {
            "a bare URL ends at a bracket or quote", "\"https://x.org/a\"<",
            [S("\""), S("https://x.org/a", link: "https://x.org/a"), S("\"<")]
        },
        { "a bare URL in bold", "**https://x.org**", [S("https://x.org", bold: true, link: "https://x.org")] },
        { "no bare URL inside a word", "xhttps://x.org and 1http://y.org", Plain("xhttps://x.org and 1http://y.org") },
        { "a scheme alone", "https:// and http://.", Plain("https:// and http://.") },
        {
            "other schemes are text", "javascript:alert(1) ftp://x.org www.x.org",
            Plain("javascript:alert(1) ftp://x.org www.x.org")
        },
        {
            "no HTML", "<b>bold</b> <script>alert(1)</script> &amp; <a href=\"https://x.org\">x</a>",
            [
                S("<b>bold</b> <script>alert(1)</script> &amp; <a href=\""), S("https://x.org", link: "https://x.org"),
                S("\">x</a>"),
            ]
        },
        { "control characters", "a\x0000b\x001Bc\x007Fd\x0085e\tf", Plain("abcde\tf") },
        { "Czech", "Příliš **žluťoučký** kůň", [S("Příliš "), S("žluťoučký", bold: true), S(" kůň")] },
        { "underscores at Czech word edges", "_čau_ a x_č_y", [S("čau", italic: true), S(" a x_č_y")] },
        // Swift only: url.Parse's corners, as Go reads them.
        { "a port after the last colon", "http://a:1:2/x", [S("http://a:1:2/x", link: "http://a:1:2/x")] },
        { "a bad port", "http://x.org:8o/", Plain("http://x.org:8o/") },
        {
            "an IPv6 literal", "[v6](http://[::1]:80/) [zone](http://[fe80::1%25en0]/) [v4](http://[1.2.3.4]/)",
            [
                S("v6", link: "http://[::1]:80/"), S(" "), S("zone", link: "http://[fe80::1%25en0]/"),
                S(" [v4](http://[1.2.3.4]/)"),
            ]
        },
        {
            "a bad escape in the path or the fragment",
            "[a](https://x.org/%zz) [b](https://x.org/#%4) [c](https://x.org/?%zz)",
            [S("[a](https://x.org/%zz) [b](https://x.org/#%4) "), S("c", link: "https://x.org/?%zz")]
        },
        {
            "an escape in the host", "[a](http://%41.org) [b](http://%c3%a9.org)",
            [S("[a](http://%41.org) "), S("b", link: "http://%c3%a9.org")]
        },
        {
            "userinfo", "[a](http://u:p@x.org) [b](http://u{s@x.org)",
            [S("a", link: "http://u:p@x.org"), S(" [b]("), S("http://u", link: "http://u"), S("{s@x.org)")]
        },
    };

    // A megabyte of every pathological shape, built when its case runs.
    private static readonly Dictionary<string, Func<string>> LinearInputs = new(StringComparer.Ordinal)
    {
        ["asterisks"] = () => Repeat("*", Mb),
        ["asterisk pairs"] = () => Repeat("**a", Mb / 3),
        ["stars and spaces"] = () => Repeat("* ", Mb / 2),
        ["underscores"] = () => Repeat("_a", Mb / 2),
        ["backticks"] = () => Repeat("`", Mb),
        ["code spans"] = () => Repeat("`a`", Mb / 3),
        ["open brackets"] = () => Repeat("[", Mb),
        ["bracket pairs"] = () => Repeat("[]", Mb / 2),
        ["link openers"] = () => Repeat("[a](", Mb / 4),
        ["bad links"] = () => Repeat("[a", Mb / 4) + "](javascript:" + Repeat("x", Mb / 2) + ")",
        ["one link, many ["] = () => Repeat("[", Mb / 2) + "a](https://x.org)",
        ["schemes"] = () => Repeat("http://", Mb / 7),
        ["schemes and dots"] = () => Repeat("http://.", Mb / 8),
        ["a long URL"] = () => "https://x.org/" + Repeat("a", Mb),
        ["hashes"] = () => Repeat("#", Mb),
        ["digits"] = () => Repeat("1", Mb) + ". x",
        ["nested lists"] = NestedLists,
        ["fences"] = () => Repeat("```\n", Mb / 4),
        ["lines"] = () => Repeat("a\n", Mb / 2),
        ["mixed"] = () => Repeat("**_`[*h](", Mb / 9),
        ["control bytes"] = () => Repeat("\0\r", Mb / 2),
        ["replacement characters"] = () => Repeat("\xFFFD", Mb / 3),
        ["bold over a block"] = () => "**" + Repeat("a *b* _c_ `d` ", Mb / 14) + "**",
        // Swift only: link targets that share one closing parenthesis.
        ["link targets to one parenthesis"] = () => Repeat("[a](http://", Mb / 11) + ")",
        ["link targets with a query and a fragment"] = () => Repeat("[a](http:///", Mb / 12) + "?%#y)",
        ["link targets with bad escapes"] = () => Repeat("[a](http://x/%", Mb / 14) + ")",
        ["link targets with at signs"] = () => Repeat("[a](http://u@", Mb / 13) + ")",
        // Go only: many link openers sharing one ")", each target would hold
        // the rest of the text.
        ["openers, one paren2"] = () => Repeat("[a](https://x.org/", Mb / 18) + ")",
        ["openers, text paren"] = () => Repeat("[a](http://x.org/b ", Mb / 19) + ")",
        ["openers, no scheme"] = () => Repeat("[a](x", Mb / 5) + ")",
        // Tables: rows, a header label repeated for every row, one very wide
        // table.
        ["table rows"] = () => "a|b\n-|-\n" + Repeat("x|y\n", Mb / 4),
        ["a long header"] = () => "a|" + Repeat("h", Mb / 2) + "\n-|-\n" + Repeat("x|y\n", Mb / 8),
        ["a wide table"] = () => Repeat("|", Mb / 4) + "\n|" + Repeat("-|", (Mb / 4) - 1) + "\n" + Repeat("|a|b|\n", Mb / 12),
    };

    public static TheoryData<string> LinearNames => [.. LinearInputs.Keys];

    [Theory]
    [MemberData(nameof(BlockCases))]
    public void Blocks(string name, string input, MarkdownBlock[] want)
    {
        var got = Assistant.Markdown(input);
        Assert.True(got.SequenceEqual(want), $"{name}: got [{string.Join(", ", got)}], want [{string.Join(", ", (object[])want)}]");
    }

    [Theory]
    [MemberData(nameof(InlineCases))]
    public void Inline(string name, string input, MarkdownSpan[] want)
    {
        var got = Assistant.Markdown(input);
        MarkdownBlock[] block = [B(Paragraph, want)];
        Assert.True(got.SequenceEqual(block), $"{name}: got [{string.Join(", ", got)}], want [{string.Join(", ", (object[])block)}]");
    }

    // Whatever the input, a span's link is an http or https URL.
    [Theory]
    [InlineData("[a](javascript:x) [b](https://ok.org) https://ok2.org/x) javascript://x.org")]
    [InlineData("[x](https://a.org\\@evil.org) http://a.org\\b [y](http://a.org`b)")]
    [InlineData("[x](https://\x2028evil.org) https://x.org\x00A0tail")]
    public void LinksAreWebUrls(string input)
    {
        foreach (var b in Assistant.Markdown(input))
        {
            foreach (var s in b.Spans.Where(s => s.Link.Length > 0))
            {
                Assert.True(Assistant.IsWebUrl(s.Link), $"{input}: link {s.Link}");
                Assert.False(s.Link.Any(" \\`\"<>".Contains), $"{input}: link {s.Link}");
            }
        }
    }

    // A target with a "(" of its own is no link target; the bare URL in it
    // is found instead, with its balanced parentheses (markdown_test.go
    // TestMarkdownLinkTargetWithParens).
    [Fact]
    public void LinkTargetWithParens()
    {
        MarkdownBlock[] want = [B(Paragraph, [S("[x]("), S("http://h.org/Foo_(bar)", link: "http://h.org/Foo_(bar)"), S(") end")])];
        Assert.Equal(want, Assistant.Markdown("[x](http://h.org/Foo_(bar)) end"));
        // Without parentheses the target is a link as before.
        want = [B(Paragraph, [S("x", link: "http://h.org/a"), S(" end")])];
        Assert.Equal(want, Assistant.Markdown("[x](http://h.org/a) end"));
    }

    // A megabyte of every pathological shape costs what a megabyte of
    // letters does, and nothing is lost but markers.
    [Theory]
    [MemberData(nameof(LinearNames))]
    public void IsLinear(string name)
    {
        var input = LinearInputs[name]();
        var size = Encoding.UTF8.GetByteCount(input);
        var clock = Stopwatch.StartNew();
        var blocks = Assistant.Markdown(input);
        var took = clock.Elapsed;
        Assert.True(took < TimeSpan.FromSeconds(3), $"{name}: {size} bytes took {took}");
        var n = blocks.Sum(b => b.Spans.Sum(s => Encoding.UTF8.GetByteCount(s.Text)));
        Assert.True(n <= 3 * size, $"{name}: {n} bytes of spans from {size} bytes");
    }

    // S: a span, as Swift's Assistant.Span(text:bold:italic:code:link:).
    private static MarkdownSpan S(string text, bool bold = false, bool italic = false, bool code = false, string link = "") =>
        new() { Text = text, Bold = bold, Italic = italic, Code = code, Link = link };

    // B: a block, as Swift's Assistant.Block(kind:level:number:spans:).
    private static MarkdownBlock B(MarkdownBlockKind kind, MarkdownSpan[] spans, int level = 0, int number = 0) =>
        new() { Kind = kind, Level = level, Number = number, Spans = spans };

    // plain: one unstyled span.
    private static MarkdownSpan[] Plain(string s) => [S(s)];

    // para: a paragraph of one unstyled span.
    private static MarkdownBlock Para(string s) => B(Paragraph, Plain(s));

    private static string Repeat(string s, int count) => new StringBuilder(s.Length * count).Insert(0, s, count).ToString();

    private static string NestedLists()
    {
        var nested = new StringBuilder();
        for (var i = 0; nested.Length < Mb; i++)
        {
            nested.Append(' ', i % 400).Append("- x\n");
        }
        return nested.ToString();
    }
}
