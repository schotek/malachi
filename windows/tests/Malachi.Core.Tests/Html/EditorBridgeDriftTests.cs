// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The drift test of docs/windows-port.md §6.5: the Windows bridge is a third
// copy of ui/internal/editor/bridge.go (beside the Swift one), so this test
// reads bridge.go from the repository, applies the documented deltas of
// EditorBridge.cs, and requires the result to be the Windows script exactly.
// A change of the GTK script either breaks a delta's anchor (the delta names
// what it expected) or shows up as a difference: then the Windows copy, and
// this list, follow the change. The documents of both views are checked
// against ui/internal/editor/document.go and ui/internal/htmlview/document.go
// the same way.

using System;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.RegularExpressions;
using Malachi.Core.Html;
using Malachi.Core.Tests.I18n;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class EditorBridgeDriftTests
{
    // Delta 1 (EditorBridge.cs): the guard, and delta 2: the accessors
    // captured before any content exists, and delta 3: the webview captured.
    private const string Prologue =
        "  if (window !== window.top || !String(window.location.href).startsWith('malachi-doc://editor/')) return;\n"
        + "  const D = Document.prototype, E = EventTarget.prototype, N = Node.prototype;\n"
        + "  const getBody = Object.getOwnPropertyDescriptor(D, 'body').get;\n"
        + "  const getParent = Object.getOwnPropertyDescriptor(N, 'parentElement').get;\n"
        + "  const getParentNode = Object.getOwnPropertyDescriptor(N, 'parentNode').get;\n"
        + "  const getPrevious = Object.getOwnPropertyDescriptor(N, 'previousSibling').get;\n"
        + "  const getNodeType = Object.getOwnPropertyDescriptor(N, 'nodeType').get;\n"
        + "  const getChildNodes = Object.getOwnPropertyDescriptor(N, 'childNodes').get;\n"
        + "  const getSelection = D.getSelection, queryCommandState = D.queryCommandState, queryCommandValue = D.queryCommandValue;\n"
        + "  const execCommand = D.execCommand, createRange = D.createRange, addEventListener = E.addEventListener;\n"
        + "  const closest = Element.prototype.closest;\n"
        + "  const webview = window.chrome.webview, postMessage = webview.postMessage, postWith = webview.postMessageWithAdditionalObjects;\n"
        + "  const body = () => getBody.call(document);\n"
        + "  const selection = () => getSelection.call(document);\n"
        + "  const parent = n => getParent.call(n);\n"
        + "  const on = (type, f, options) => addEventListener.call(document, type, f, options);\n";

    // Delta 1: GTK's body runs at DOMContentLoaded, one level deeper.
    private const string RunOpen = "  const run = () => {\n";

    private const string RunClose =
        "\n  };\n"
        + "  if (document.readyState === 'loading') on('DOMContentLoaded', run, {once: true}); else run();\n";

    // The deltas inside GTK's body, in order: what bridge.go says, what the
    // Windows bridge says instead, how often it occurs, and why.
    private static readonly (string Go, string Windows, int Count, string Why)[] Deltas =
    [
        ("const post = m => window.webkit.messageHandlers.malachi.postMessage(JSON.stringify(m));",
            "const post = m => postMessage.call(webview, JSON.stringify(m));", 1,
            "3: chrome.webview's postMessage, captured"),
        ("document.getSelection().anchorNode.parentElement.closest('a')",
            "closest.call(parent(selection().anchorNode), 'a')", 1,
            "2: Node.parentElement and Element.closest through the prototypes"),
        ("document.getSelection().anchorNode.parentElement", "parent(selection().anchorNode)", 1,
            "2: Node.parentElement through the prototype"),
        ("document.getSelection()", "selection()", 4, "2: Document.getSelection captured"),
        ("document.body", "body()", 10, "2: the Document.body getter captured"),
        ("document.queryCommandState(c)", "queryCommandState.call(document, c)", 1, "2: Document.queryCommandState captured"),
        ("document.queryCommandValue('formatBlock')", "queryCommandValue.call(document, 'formatBlock')", 1,
            "2: Document.queryCommandValue captured"),
        ("document.execCommand(", "execCommand.call(document, ", 3, "2: Document.execCommand captured"),
        ("document.createRange()", "createRange.call(document)", 2, "2: Document.createRange captured"),
        ("document.addEventListener(", "on(", 3, "2: EventTarget.addEventListener captured"),
        // The rewrite walks from the attribution's div up and back through
        // nodes of the content, any of which may be a <form>, whose named
        // controls override its own properties as the document's do.
        ("prev.previousSibling", "getPrevious.call(prev)", 2, "2: Node.previousSibling through the prototype"),
        ("prev.parentNode", "getParentNode.call(prev)", 1, "2: Node.parentNode through the prototype"),
        ("prev.nodeType", "getNodeType.call(prev)", 1, "2: Node.nodeType through the prototype"),
        ("prev.childNodes", "getChildNodes.call(prev)", 1, "2: Node.childNodes through the prototype"),
        ("    if (!e.ctrlKey || e.altKey || e.metaKey) return;\n",
            "    if (e.key === 'Escape' && !e.ctrlKey && !e.altKey && !e.metaKey && !e.shiftKey && !e.isComposing) {\n"
            + "      e.preventDefault(); post({type: 'key', key: 'escape'}); return;\n"
            + "    }\n"
            + "    if (!e.ctrlKey || e.altKey || e.metaKey) return;\n"
            + "    if (e.key.toLowerCase() === 'k' && !e.shiftKey) { e.preventDefault(); post({type: 'key', key: 'link'}); return; }\n", 1,
            "4: Escape and Ctrl+K for the window"),
        ("  window.malachi = {\n",
            "  const hasFiles = e => !!e.dataTransfer && Array.prototype.includes.call(e.dataTransfer.types || [], 'Files');\n"
            + "  on('dragover', e => { if (hasFiles(e)) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; } }, true);\n"
            + "  on('drop', e => {\n"
            + "    if (!hasFiles(e) || !e.dataTransfer.files.length) return;\n"
            + "    e.preventDefault(); e.stopPropagation();\n"
            + "    postWith.call(webview, JSON.stringify({type: 'drop'}), e.dataTransfer.files);\n"
            + "  }, true);\n"
            + "  window.malachi = {\n", 1,
            "5: dropped files go to the host"),
        ("  post({type: 'ready'});",
            "  window.malachi.exec = (c, a) => { execCommand.call(document, c, false, a == null ? null : a); state(); };\n"
            + "  post({type: 'ready'});", 1,
            "6: macOS's window.malachi.exec"),
    ];

    [Fact]
    public void BridgeIsGtksWithTheDocumentedDeltas()
    {
        var go = GoRawString(Read("ui", "internal", "editor", "bridge.go"), "const bridgeJS = `");
        Assert.StartsWith("(() => {\n", go, StringComparison.Ordinal);
        Assert.EndsWith("\n})();", go, StringComparison.Ordinal);
        var body = go["(() => {\n".Length..^"\n})();".Length];
        foreach (var (goText, windows, count, why) in Deltas)
        {
            var found = Occurrences(body, goText);
            Assert.True(found == count, $"delta \"{why}\": bridge.go has {found} of «{goText}», the Windows bridge expects {count}; update EditorBridge.Script and this list");
            body = body.Replace(goText, windows, StringComparison.Ordinal);
        }
        var indented = string.Join("\n", body.Split('\n').Select(l => l.Length == 0 ? l : "  " + l));
        var expected = "(() => {\n" + Prologue + RunOpen + indented + RunClose + "})();";
        AssertSameText(expected, EditorBridge.Script);
    }

    // The editor document is document.go's template with the fixed title.
    [Fact]
    public void EditorDocumentIsGtksWithTheTitle()
    {
        var template = GoRawString(Read("ui", "internal", "editor", "document.go"), "const documentTemplate = `");
        const string body = "<p>a &amp; b 100% %s</p>";
        var gtk = template.Replace("%s", "@@BODY@@", StringComparison.Ordinal).Replace("%%", "%", StringComparison.Ordinal)
            .Replace("@@BODY@@", body, StringComparison.Ordinal);
        var windows = EditorDocument.Document(body);
        const string title = "<title>" + EditorDocument.Title + "</title>\n";
        Assert.Equal(1, Occurrences(windows, title));
        AssertSameText(gtk, windows.Replace(title, "", StringComparison.Ordinal));
        Assert.Contains("content=\"" + EditorDocument.Csp + "\"", gtk, StringComparison.Ordinal);
    }

    // The viewer document is htmlview.Document with the fixed title: the
    // sheet is htmlview.columnCSS with the reader's padding (baseCSS), the
    // page htmlview.document around it.
    [Fact]
    public void ViewerDocumentIsGtksWithTheTitle()
    {
        var source = Read("ui", "internal", "htmlview", "document.go");
        var csp = Regex.Match(source, "const CSP = \"([^\"]*)\"").Groups[1].Value;
        var baseCss = GtkColumnCss(source, "baseCSS");
        Assert.Equal(ViewerDocument.Csp, csp);
        Assert.Equal(ViewerDocument.BaseCss, baseCss);
        Assert.Contains("return document(body, baseCSS)", source, StringComparison.Ordinal);
        const string body = "<p>x</p>";
        var gtk = GoJoin(GoReturn(source, "func document(body, css string) string {"), name => name switch
        {
            "CSP" => csp,
            "css" => baseCss,
            "body" => body,
            _ => throw new InvalidOperationException("unexpected name in htmlview.document: " + name),
        });
        var windows = ViewerDocument.Document(body);
        const string title = "<title>" + ViewerDocument.Title + "</title>";
        Assert.Equal(1, Occurrences(windows, title));
        AssertSameText(gtk, windows.Replace(title, "", StringComparison.Ordinal));
    }

    // The card of the conversation view: htmlview.CompactDocument, the same
    // page with the card's padding (compactCSS), and the fixed title.
    [Fact]
    public void CompactDocumentIsGtksWithTheTitle()
    {
        var source = Read("ui", "internal", "htmlview", "document.go");
        var csp = Regex.Match(source, "const CSP = \"([^\"]*)\"").Groups[1].Value;
        var compactCss = GtkColumnCss(source, "compactCSS");
        Assert.Equal(ViewerDocument.CompactCss, compactCss);
        Assert.Contains("return document(body, compactCSS)", source, StringComparison.Ordinal);
        const string body = "<p>x</p>";
        var gtk = GoJoin(GoReturn(source, "func document(body, css string) string {"), name => name switch
        {
            "CSP" => csp,
            "css" => compactCss,
            "body" => body,
            _ => throw new InvalidOperationException("unexpected name in htmlview.document: " + name),
        });
        var windows = ViewerDocument.CompactDocument(body);
        const string title = "<title>" + ViewerDocument.Title + "</title>";
        Assert.Equal(1, Occurrences(windows, title));
        AssertSameText(gtk, windows.Replace(title, "", StringComparison.Ordinal));
    }

    // htmlview.columnCSS with the padding the Go variable `name` passes it.
    private static string GtkColumnCss(string source, string name)
    {
        var padding = Regex.Match(source, "var " + name + " = columnCSS\\(\"([^\"]*)\"\\)");
        Assert.True(padding.Success, $"«var {name} = columnCSS(…)» not found");
        return GoJoin(GoReturn(source, "func columnCSS(padding string) string {"), n =>
            n == "padding" ? padding.Groups[1].Value : throw new InvalidOperationException("unexpected name in htmlview.columnCSS: " + n));
    }

    // The expression the first return of the Go function `signature` opens
    // returns.
    private static string GoReturn(string source, string signature)
    {
        var start = source.IndexOf(signature, StringComparison.Ordinal);
        Assert.True(start >= 0, $"«{signature}» not found");
        var from = source.IndexOf("return ", start, StringComparison.Ordinal) + "return ".Length;
        return source[from..source.IndexOf("\n}", start, StringComparison.Ordinal)];
    }

    // A Go expression of raw strings and names joined by +, each name
    // read through value.
    private static string GoJoin(string expression, Func<string, string> value)
    {
        var joined = new StringBuilder();
        foreach (Match token in Regex.Matches(expression, "`([^`]*)`|([A-Za-z]+)"))
        {
            joined.Append(token.Groups[1].Success ? token.Groups[1].Value : value(token.Groups[2].Value));
        }
        return joined.ToString();
    }

    private static string Read(params string[] parts) =>
        File.ReadAllText(Path.Combine([RepositoryPo.Root, .. parts])).Replace("\r\n", "\n", StringComparison.Ordinal);

    // The Go raw string that starts right after `opening`.
    private static string GoRawString(string source, string opening)
    {
        var start = source.IndexOf(opening, StringComparison.Ordinal);
        Assert.True(start >= 0, $"«{opening}» not found");
        start += opening.Length;
        var end = source.IndexOf('`', start);
        Assert.True(end > start, $"the raw string after «{opening}» does not end");
        return source[start..end];
    }

    private static int Occurrences(string s, string what)
    {
        var n = 0;
        for (var i = s.IndexOf(what, StringComparison.Ordinal); i >= 0; i = s.IndexOf(what, i + what.Length, StringComparison.Ordinal))
        {
            n++;
        }
        return n;
    }

    // Equal, and if not, the first line that differs.
    private static void AssertSameText(string expected, string actual)
    {
        if (string.Equals(expected, actual, StringComparison.Ordinal))
        {
            return;
        }
        var e = expected.Split('\n');
        var a = actual.Split('\n');
        for (var i = 0; i < Math.Max(e.Length, a.Length); i++)
        {
            var el = i < e.Length ? e[i] : "<end>";
            var al = i < a.Length ? a[i] : "<end>";
            Assert.True(el == al, $"line {i + 1} differs:\nexpected «{el}»\nactual   «{al}»");
        }
        Assert.Equal(expected, actual);
    }
}
