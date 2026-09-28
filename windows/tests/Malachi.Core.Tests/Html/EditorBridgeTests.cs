// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/EditorBridgeTests.swift, the
// counterpart of ui/internal/editor/editor_test.go. The bridge script's own
// test checks the Windows deltas; EditorBridgeDriftTests compares the whole
// script with bridge.go.

using System;
using System.Text.Json;
using Malachi.Core.Html;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class EditorBridgeTests
{
    private const string LineSeparator = "\x2028";
    private const string ParagraphSeparator = "\x2029";

    [Fact]
    public void Document()
    {
        const string body = "<p>hi &amp; bye</p>";
        var doc = EditorDocument.Document(body);
        Assert.True(doc.Contains(body, StringComparison.Ordinal), "body not embedded verbatim");
        Assert.Contains("default-src 'none'", doc, StringComparison.Ordinal);
        Assert.Contains("img-src cid: data:", doc, StringComparison.Ordinal);
        Assert.Contains("<body contenteditable=\"true\">", doc, StringComparison.Ordinal);
        Assert.False(doc.Contains("%!", StringComparison.Ordinal), "format verb leaked");
        Assert.Contains("img { max-width: 100%; }", doc, StringComparison.Ordinal);
        Assert.StartsWith("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n", doc, StringComparison.Ordinal);
        Assert.EndsWith("</body>\n</html>\n", doc, StringComparison.Ordinal);
        Assert.Contains("<meta http-equiv=\"Content-Security-Policy\" content=\"" + EditorDocument.Csp + "\">", doc, StringComparison.Ordinal);
        // A body with format verbs is inserted as it is.
        Assert.Contains("<body contenteditable=\"true\">100%s %d</body>", EditorDocument.Document("100%s %d"), StringComparison.Ordinal);
        // Windows: the fixed title right after the charset.
        Assert.Contains("<meta charset=\"utf-8\">\n<title>Malachi Mail</title>\n<meta http-equiv", doc, StringComparison.Ordinal);
    }

    [Fact]
    public void DecodeMessage()
    {
        var state = BridgeMessage.Decode("{\"type\":\"state\",\"bold\":true,\"block\":\"h1\",\"align\":\"center\"}");
        Assert.Equal("state", state.Type);
        Assert.True(state.State.Bold);
        Assert.False(state.State.Italic);
        Assert.Equal("h1", state.State.Block);
        Assert.Equal("center", state.State.Align);
        Assert.Equal(0, state.Seq);
        Assert.Equal("", state.Html);

        var changed = BridgeMessage.Decode("{\"type\":\"changed\",\"seq\":3,\"html\":\"<p>x</p>\",\"text\":\"x\"}");
        Assert.Equal(3, changed.Seq);
        Assert.Equal("<p>x</p>", changed.Html);
        Assert.Equal("x", changed.Text);
        Assert.Equal(new EditorState(), changed.State);

        var ready = BridgeMessage.Decode("{\"type\":\"ready\"}");
        Assert.Equal(new BridgeMessage { Type = "ready" }, ready);

        Assert.Throws<FormatException>(() => BridgeMessage.Decode("not json"));
        Assert.Throws<FormatException>(() => BridgeMessage.Decode("{\"type\":\"state\",\"bold\":\"yes\"}"));
    }

    // Windows: the shape is validated and nothing else: a message that is not
    // an object, or with a mistyped member, is refused (TryDecode: null); a
    // null or missing member is its zero value; an unknown member or kind
    // decodes; the last of a repeated member counts; the messages of the
    // Windows bridge decode.
    [Fact]
    public void DecodeValidatesShape()
    {
        foreach (var bad in new[] { "[]", "\"ready\"", "1", "null", "", "{\"type\":1}", "{\"seq\":\"3\"}", "{\"seq\":1.5}", "{\"html\":[]}", "{\"link\":0}", "{\"type\":\"changed\"" })
        {
            Assert.Null(BridgeMessage.TryDecode(bad));
        }
        Assert.Null(BridgeMessage.TryDecode(null));
        Assert.Equal(new BridgeMessage(), BridgeMessage.Decode("{\"type\":null,\"seq\":null,\"html\":null,\"bold\":null}"));
        Assert.Equal("brandNew", BridgeMessage.Decode("{\"type\":\"brandNew\",\"extra\":{\"a\":[1]}}").Type);
        Assert.Equal(2, BridgeMessage.Decode("{\"type\":\"changed\",\"seq\":1,\"seq\":2}").Seq);
        var key = BridgeMessage.Decode("{\"type\":\"key\",\"key\":\"escape\"}");
        Assert.Equal((BridgeMessage.Kinds.Key, "escape"), (key.Type, key.Key));
        Assert.Equal(BridgeMessage.Kinds.Drop, BridgeMessage.Decode("{\"type\":\"drop\"}").Type);
        // The exception never carries the page's content.
        var e = Assert.Throws<FormatException>(() => BridgeMessage.Decode("{\"html\":42,\"text\":\"secret\"}"));
        Assert.DoesNotContain("secret", e.Message, StringComparison.Ordinal);
    }

    // Windows: strings decode as encoding/json decodes them (each case checked
    // against Go's json.Unmarshal): an escaped surrogate that does not pair
    // with the escape right after it is U+FFFD, the rest of the string as it
    // is. Chromium keeps an unpaired surrogate a plain-text paste brought in,
    // and JSON.stringify posts it escaped: the message must decode.
    [Theory]
    [InlineData("""{"html":"\ud800"}""", "�")]
    [InlineData("""{"html":"a\udc00b"}""", "a�b")]
    [InlineData("""{"html":"a\ud800"}""", "a�")]
    [InlineData("""{"html":"a\ud800\n"}""", "a�\n")]
    [InlineData("""{"html":"\ud800𐀀"}""", "�\U00010000")]
    [InlineData("""{"html":"x\udc00\ud800"}""", "x��")]
    [InlineData("""{"html":"\ud800\\udc00"}""", "�\\udc00")]
    [InlineData("""{"html":"\\\ud800\\"}""", "\\�\\")]
    [InlineData("""{"html":"\ud800A"}""", "�A")]
    [InlineData("""{"html":"\ud800􏿿"}""", "�\U0010FFFF")]
    [InlineData("""{"html":"\"\\\/\b\f\n\r\tä\ud800"}""", "\"\\/\b\f\n\r\tä�")]
    [InlineData("""{"html":"😀"}""", "\U0001F600")]
    public void DecodeReplacesUnpairedSurrogates(string raw, string html)
    {
        Assert.Equal(html, BridgeMessage.Decode(raw).Html);
    }

    // Windows: the other places a surrogate can hide. In the posted string
    // itself (not JSON.stringify's output, but a message need not come from
    // it) it is U+FFFD too, one per UTF-16 unit; in a member name the member
    // is unknown and ignored, as Go ignores it; in the kind the kind is
    // unknown.
    [Fact]
    public void DecodeSurrogatesElsewhere()
    {
        var both = BridgeMessage.Decode("""{"type":"changed","seq":1,"html":"\ud800","text":"a\udc00b"}""");
        Assert.Equal(("changed", 1L, "�", "a�b"), (both.Type, both.Seq, both.Html, both.Text));
        var raw = BridgeMessage.Decode("{\"type\":\"changed\",\"html\":\"a" + (char)0xD800 + "b" + (char)0xDC00 + (char)0xDC00 + "\"}");
        Assert.Equal("a�b��", raw.Html);
        Assert.Equal(BridgeMessage.Kinds.Ready, BridgeMessage.Decode("""{"\ud800":1,"type":"ready"}""").Type);
        Assert.Equal(BridgeMessage.Kinds.Ready, BridgeMessage.Decode("{\"" + (char)0xDC00 + "\":[],\"type\":\"ready\"}").Type);
        Assert.Equal("�", BridgeMessage.Decode("""{"type":"\ud800"}""").Type);
        // A malformed escape is still no message.
        Assert.Null(BridgeMessage.TryDecode("""{"html":"\ud800\u"}"""));
        Assert.Null(BridgeMessage.TryDecode("""{"html":"\ud800\ud8"}"""));
    }

    // Windows: a message prints its kind and sizes, never the draft or any
    // other string of the page's (docs/windows-port.md §3.1).
    [Fact]
    public void MessagePrintsNoContent()
    {
        var changed = BridgeMessage.Decode("""{"type":"changed","seq":3,"html":"<p>secret</p>","text":"secret"}""");
        Assert.Equal("BridgeMessage(type: changed, seq: 3, html: 13 chars, text: 6 chars, key: 0 chars)", changed.ToString());
        Assert.Equal(
            "BridgeMessage(type: <other>, seq: 0, html: 0 chars, text: 0 chars, key: 0 chars)",
            BridgeMessage.Decode("""{"type":"secret"}""").ToString());
        Assert.Equal(
            "BridgeMessage(type: key, seq: 0, html: 0 chars, text: 0 chars, key: 4 chars)",
            BridgeMessage.Decode("""{"type":"key","key":"link"}""").ToString());
        Assert.Equal("BridgeMessage(type: \"\", seq: 0, html: 0 chars, text: 0 chars, key: 0 chars)", new BridgeMessage().ToString());
    }

    [Fact]
    public void JsStringLiteral()
    {
        var input = "a\"b\\c </script>" + LineSeparator;
        var got = EditorBridge.JsString(input);
        Assert.False(got.Contains(LineSeparator, StringComparison.Ordinal), "unsafe literal");
        Assert.DoesNotContain("<", got, StringComparison.Ordinal);
        Assert.StartsWith("\"", got, StringComparison.Ordinal);
        Assert.EndsWith("\"", got, StringComparison.Ordinal);
        Assert.Equal("\"a\\\"b\\\\c \\u003c/script\\u003e\\u2028\"", got);
        Assert.Equal("\"\"", EditorBridge.JsString(""));
        Assert.Equal("\"tab\\tnew\\nline\\r\\u0001\\u0026\\u2029\"", EditorBridge.JsString("tab\tnew\nline\r\x0001&" + ParagraphSeparator));
        // What the literal decodes to is the input, so it can be spliced into a script.
        using var decoded = JsonDocument.Parse("[" + got + "]");
        Assert.Equal(input, decoded.RootElement[0].GetString());
    }

    // editor_test.go TestJSString.
    [Fact]
    public void TestJsString()
    {
        var got = EditorBridge.JsString("a\"b\\c </script>");
        Assert.False(got.Contains(LineSeparator, StringComparison.Ordinal) || (got.Contains("\"b", StringComparison.Ordinal) && !got.Contains("\\\"b", StringComparison.Ordinal)), got);
        Assert.True(got.StartsWith('"') && got.EndsWith('"'));
    }

    // Windows: what Go's json.Marshal does beyond Swift's escaping: a lone
    // surrogate (invalid UTF-8 in Go) is \ufffd, a valid pair stays, DEL and
    // the rest of Unicode stay as they are.
    [Fact]
    public void JsStringSurrogatesAndUnicode()
    {
        Assert.Equal("\"a\\ufffdb\"", EditorBridge.JsString("a" + (char)0xD800 + "b"));
        Assert.Equal("\"\\ufffd\"", EditorBridge.JsString(((char)0xDC00).ToString()));
        var pair = char.ConvertFromUtf32(0x1F600);
        Assert.Equal("\"" + pair + "\"", EditorBridge.JsString(pair));
        Assert.Equal("\"\x007Fé\"", EditorBridge.JsString("\x007Fé"));
        Assert.Equal("window.malachi.exec(\"bold\", null)", EditorBridge.ExecScript("bold", null));
        Assert.Equal("window.malachi.exec(\"bold\", null)", EditorBridge.ExecScript("bold", ""));
        Assert.Equal("window.malachi.exec(\"createLink\", \"https://x/?a=1\\u0026b=\\u003c\")", EditorBridge.ExecScript("createLink", "https://x/?a=1&b=<"));
        Assert.Equal("window.malachi.flush()", EditorBridge.FlushScript);
        Assert.Equal("window.malachi.focusStart()", EditorBridge.FocusStartScript);
    }

    // The script is the GTK one with the Windows deltas (EditorBridge.cs).
    [Fact]
    public void BridgeScript()
    {
        var js = EditorBridge.Script;
        Assert.StartsWith("(() => {\n", js, StringComparison.Ordinal);
        Assert.EndsWith("\n})();", js, StringComparison.Ordinal);
        Assert.DoesNotContain("\r", js, StringComparison.Ordinal);
        // The channel: chrome.webview, captured.
        Assert.Contains("const webview = window.chrome.webview, postMessage = webview.postMessage, postWith = webview.postMessageWithAdditionalObjects;", js, StringComparison.Ordinal);
        Assert.Contains("const post = m => postMessage.call(webview, JSON.stringify(m));", js, StringComparison.Ordinal);
        Assert.DoesNotContain("webkit", js, StringComparison.Ordinal);
        // GTK's Ctrl-only test, not macOS's Command variant.
        Assert.Contains("if (!e.ctrlKey || e.altKey || e.metaKey) return;", js, StringComparison.Ordinal);
        Assert.DoesNotContain("e.ctrlKey || e.metaKey", js, StringComparison.Ordinal);
        // macOS's exec, through the captured execCommand.
        Assert.Contains("window.malachi.exec = (c, a) => { execCommand.call(document, c, false, a == null ? null : a); state(); };", js, StringComparison.Ordinal);
        Assert.Contains("post({type: 'ready'});", js, StringComparison.Ordinal);
        Assert.Contains("focusStart()", js, StringComparison.Ordinal);
        Assert.Contains("setTimeout(flush, 250)", js, StringComparison.Ordinal);
        // Top frame and our documents only.
        Assert.Contains("if (window !== window.top || !String(window.location.href).startsWith('" + EditorBridge.DocumentUrlPrefix + "')) return;", js, StringComparison.Ordinal);
        Assert.Equal("malachi-doc://editor/", EditorBridge.DocumentUrlPrefix);
        // No document property is read through the document once content
        // exists: every access goes through the captured prototypes.
        foreach (var clobberable in new[] { "document.body", "document.getSelection", "document.queryCommand", "document.execCommand", "document.createRange", "document.addEventListener", ".parentElement", ".closest(" })
        {
            Assert.False(js.Contains(clobberable, StringComparison.Ordinal), $"{clobberable} read through the document");
        }
        // Escape and Ctrl+K go to the window; files dropped go with the
        // message.
        Assert.Contains("post({type: 'key', key: 'escape'})", js, StringComparison.Ordinal);
        Assert.Contains("post({type: 'key', key: 'link'})", js, StringComparison.Ordinal);
        Assert.Contains("postWith.call(webview, JSON.stringify({type: 'drop'}), e.dataTransfer.files);", js, StringComparison.Ordinal);
    }
}
