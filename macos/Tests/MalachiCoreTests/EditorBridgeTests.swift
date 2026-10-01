// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/editor/editor_test.go.
struct EditorBridgeTests {
    @Test func document() {
        let body = "<p>hi &amp; bye</p>"
        let doc = editorDocument(body: body)
        #expect(doc.contains(body), "body not embedded verbatim")
        #expect(doc.contains("default-src 'none'"))
        #expect(doc.contains("img-src cid: data:"))
        #expect(doc.contains("<body contenteditable=\"true\">"))
        #expect(!doc.contains("%!"), "format verb leaked")
        #expect(doc.contains("img { max-width: 100%; }"))
        #expect(doc.hasPrefix("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n"))
        #expect(doc.hasSuffix("</body>\n</html>\n"))
        #expect(doc.contains("<meta http-equiv=\"Content-Security-Policy\" content=\"\(editorCSP)\">"))
        // A body with format verbs is inserted as it is.
        #expect(editorDocument(body: "100%s %d").contains("<body contenteditable=\"true\">100%s %d</body>"))
    }

    @Test func decodeMessage() throws {
        let state = try BridgeMessage.decode("{\"type\":\"state\",\"bold\":true,\"block\":\"h1\",\"align\":\"center\"}")
        #expect(state.type == "state")
        #expect(state.state.bold)
        #expect(!state.state.italic)
        #expect(state.state.block == "h1")
        #expect(state.state.align == "center")
        #expect(state.seq == 0)
        #expect(state.html == "")

        let changed = try BridgeMessage.decode("{\"type\":\"changed\",\"seq\":3,\"html\":\"<p>x</p>\",\"text\":\"x\"}")
        #expect(changed.seq == 3)
        #expect(changed.html == "<p>x</p>")
        #expect(changed.text == "x")
        #expect(changed.state == EditorState())

        let ready = try BridgeMessage.decode("{\"type\":\"ready\"}")
        #expect(ready == BridgeMessage(type: "ready"))

        let paste = try BridgeMessage.decode(##"{"type":"paste","id":7,"text":"# Plan\n- a"}"##)
        #expect(paste == BridgeMessage(type: "paste", text: "# Plan\n- a", id: 7))

        #expect(throws: (any Error).self) {
            try BridgeMessage.decode("not json")
        }
        #expect(throws: (any Error).self) {
            try BridgeMessage.decode("{\"type\":\"state\",\"bold\":\"yes\"}")
        }
    }

    @Test func jsStringLiteral() {
        let got = jsString("a\"b\\c </script>\u{2028}")
        #expect(!got.contains("\u{2028}"), "unsafe literal")
        #expect(!got.contains("<"))
        #expect(got.hasPrefix("\""))
        #expect(got.hasSuffix("\""))
        #expect(got == "\"a\\\"b\\\\c \\u003c/script\\u003e\\u2028\"")
        #expect(jsString("") == "\"\"")
        #expect(jsString("tab\tnew\nline\r\u{01}&\u{2029}") == "\"tab\\tnew\\nline\\r\\u0001\\u0026\\u2029\"")
        // What the literal decodes to is the input, so it can be spliced into a script.
        let decoded = try? JSONDecoder().decode([String].self, from: Data(("[" + got + "]").utf8))
        #expect(decoded == ["a\"b\\c </script>\u{2028}"])
    }

    /// The script is the GTK one plus the three WKWebView additions: the
    /// GTK script's lines are all there, in order, and what is not the
    /// GTK's is the documented additions.
    @Test func bridgeScriptIsGTKsPlusTheAdditions() {
        // ui/internal/editor/bridge.go's bridgeJS, the keydown line as GTK
        // has it.
        let gtk = #"""
        (() => {
          const post = m => window.webkit.messageHandlers.malachi.postMessage(JSON.stringify(m));
          let seq = 0, timer = null;
          const flush = () => {
            if (timer) { clearTimeout(timer); timer = null; }
            post({type: 'changed', seq: ++seq, html: document.body.innerHTML, text: document.body.innerText});
            return seq;
          };
          const schedule = () => { if (timer) clearTimeout(timer); timer = setTimeout(flush, 250); };
          const q = c => { try { return document.queryCommandState(c); } catch (e) { return false; } };
          const state = () => post({
            type: 'state',
            bold: q('bold'), italic: q('italic'), underline: q('underline'), strike: q('strikethrough'),
            ul: q('insertUnorderedList'), ol: q('insertOrderedList'),
            block: String(document.queryCommandValue('formatBlock') || '').toLowerCase(),
            align: q('justifyCenter') ? 'center' : q('justifyRight') ? 'right' : 'left',
            link: !!(document.getSelection().anchorNode && document.getSelection().anchorNode.parentElement &&
                     document.getSelection().anchorNode.parentElement.closest('a'))
          });
          document.execCommand('styleWithCSS', false, false);
          document.addEventListener('input', () => { schedule(); state(); });
          document.addEventListener('selectionchange', state);
          document.addEventListener('keydown', e => {
            if (!e.ctrlKey || e.altKey || e.metaKey) return;
            const cmd = {b: 'bold', i: 'italic', u: 'underline'}[e.key.toLowerCase()];
            if (cmd) { e.preventDefault(); document.execCommand(cmd); state(); }
          });
          let pending = null, pasteSeq = 0;
          const markdownHint = /^ {0,3}(#{1,6} |[-*+] |\d{1,9}[.)] |>|\x60{3}|~~~|\|)|\*\*|__|~~|\x60[^\x60\n]+\x60|\]\(|^ {0,3}(-{3,}|\*{3,}|_{3,}) *$/m;
          const richHTML = /<(h[1-6]|ul|ol|li|b|strong|i|em|a|table|blockquote)[\s>]/i;
          document.addEventListener('paste', e => {
            const d = e.clipboardData;
            if (!d || Array.from(d.types || []).includes('Files')) return;
            const text = d.getData('text/plain');
            if (!text || !markdownHint.test(text) || richHTML.test(d.getData('text/html') || '')) return;
            e.preventDefault();
            const sel = document.getSelection();
            pending = {id: ++pasteSeq, text, range: sel.rangeCount ? sel.getRangeAt(0).cloneRange() : null};
            post({type: 'paste', id: pending.id, text});
          });
          window.malachi = {
            flush,
            focusStart() {
              document.body.focus();
              const sel = document.getSelection();
              sel.removeAllRanges();
              const r = document.createRange();
              r.setStart(document.body, 0);
              r.collapse(true);
              sel.addRange(r);
            }
          };
          post({type: 'ready'});
        })();
        """#
        let keydownGTK = "    if (!e.ctrlKey || e.altKey || e.metaKey) return;"
        let keydownMac = "    if (!(e.ctrlKey || e.metaKey) || e.altKey) return;"
        let mine = bridgeJS.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        var extra: [String] = []
        var i = 0
        for line in mine {
            let want = gtk.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
            if i < want.count, line == want[i] || (want[i] == keydownGTK && line == keydownMac) {
                i += 1
            } else {
                extra.append(line)
            }
        }
        #expect(i == gtk.split(separator: "\n", omittingEmptySubsequences: false).count, "a GTK line is missing or out of order")
        // The additions: exec, then the rewrite's two functions and the
        // paste's answer (members of GTK's `window.malachi` object).
        #expect(extra.first == "  window.malachi.exec = (c, a) => { document.execCommand(c, false, a == null ? null : a); state(); };")
        #expect(extra.dropFirst().first == "  let passage = null;")
        #expect(extra.contains("  window.malachi.rewriteTarget = attribution => {"))
        #expect(extra.contains("  window.malachi.rewriteApply = (below, c, a) => {"))
        #expect(extra.contains("  window.malachi.pasted = (id, h) => {"))
        #expect(extra.last == "  };")
    }

    /// The script is the GTK one plus the WKWebView additions.
    @Test func bridgeScript() {
        #expect(bridgeJS.hasPrefix("(() => {\n"))
        #expect(bridgeJS.hasSuffix("\n})();"))
        #expect(bridgeJS.contains("window.webkit.messageHandlers.malachi.postMessage"))
        #expect(bridgeJS.contains("if (!(e.ctrlKey || e.metaKey) || e.altKey) return;"))
        #expect(!bridgeJS.contains("if (!e.ctrlKey || e.altKey || e.metaKey) return;"))
        #expect(bridgeJS.contains("window.malachi.exec = (c, a) => { document.execCommand(c, false, a == null ? null : a); state(); };"))
        #expect(bridgeJS.contains("post({type: 'ready'});"))
        #expect(bridgeJS.contains("focusStart()"))
        #expect(bridgeJS.contains("setTimeout(flush, 250)"))
        // The rewrite: the selection when it holds more than white space,
        // the text before the attribution's div, or the whole body; its
        // text read through the selection (line breaks as the page renders
        // them), which is put back; the answer by one editing command,
        // reported like typing.
        #expect(bridgeJS.contains("sel.toString().trim()"))
        #expect(bridgeJS.contains("document.body.querySelectorAll('div')).find(d => collapsed(d.innerText) === want)"))
        #expect(bridgeJS.contains("r.selectNodeContents(document.body);"))
        #expect(bridgeJS.contains("if (saved) sel.addRange(saved);"))
        #expect(bridgeJS.contains("return JSON.stringify({selected: false, text});"))
        #expect(bridgeJS.contains("if (below) r.collapse(false);"))
        #expect(bridgeJS.contains("document.execCommand(c, false, a);\n    schedule();\n    state();"))
        #expect(!bridgeJS.contains("innerHTML ="), "the page's HTML is never written by the bridge")
        // The paste: only plain text that looks like Markdown, without
        // files or rich HTML on the clipboard, goes to the app; the answer
        // goes in where the paste was, the HTML or the kept text.
        #expect(bridgeJS.contains(#"\x60{3}"#), "the backtick stays an escape, as in GTK")
        #expect(bridgeJS.contains("if (!d || Array.from(d.types || []).includes('Files')) return;"))
        #expect(bridgeJS.contains("post({type: 'paste', id: pending.id, text});"))
        #expect(bridgeJS.contains("if (!pending || pending.id !== id) return;"))
        #expect(bridgeJS.contains("document.execCommand(h ? 'insertHTML' : 'insertText', false, h || p.text);"))
    }

    /// The answer to a paste: the daemon's HTML as a string literal, or
    /// null for the plain text.
    @Test func pastedScripts() {
        #expect(pastedScript(id: 3, html: "<h1>Plan</h1>") == "window.malachi.pasted(3, \"\\u003ch1\\u003ePlan\\u003c/h1\\u003e\")")
        #expect(pastedScript(id: 4, html: nil) == "window.malachi.pasted(4, null)")
        #expect(pastedScript(id: 5, html: "") == "window.malachi.pasted(5, null)")
        #expect(pastedScript(id: 6, html: "a\")</script>\u{2028}") == "window.malachi.pasted(6, \"a\\\")\\u003c/script\\u003e\\u2028\")")
    }

    /// The answer goes in as plain text: one line with insertText, several
    /// as escaped HTML with line breaks, below the passage on a line of its
    /// own; never markup of its own.
    @Test func rewriteInsertion() {
        #expect(MalachiCore.rewriteInsertion("Dobrý den.", below: false) == ("insertText", "Dobrý den."))
        #expect(MalachiCore.rewriteInsertion("<b>x</b> & 'y'", below: false) == ("insertText", "<b>x</b> & 'y'"))
        #expect(MalachiCore.rewriteInsertion("a\n\nb <i>", below: false) == ("insertHTML", "a<br><br>b &lt;i&gt;"))
        #expect(MalachiCore.rewriteInsertion("a\r\nb", below: false) == ("insertHTML", "a<br>b"))
        #expect(MalachiCore.rewriteInsertion("x & y", below: true) == ("insertHTML", "<br>x &amp; y<br>"))
        #expect(MalachiCore.rewriteInsertion("a\nb", below: true) == ("insertHTML", "<br>a<br>b<br>"))
        #expect(MalachiCore.rewriteInsertion("</script>\"", below: true) == ("insertHTML", "<br>&lt;/script&gt;&#34;<br>"))
    }

    @Test func rewriteScripts() {
        #expect(RewriteTarget.script(attribution: "On 1 May, Jana wrote:") == "window.malachi.rewriteTarget(\"On 1 May, Jana wrote:\")")
        #expect(RewriteTarget.script(attribution: "") == "window.malachi.rewriteTarget(\"\")")
        // The attribution is mail data (a sender's name): a string literal.
        #expect(RewriteTarget.script(attribution: "a\")</script>\n") == "window.malachi.rewriteTarget(\"a\\\")\\u003c/script\\u003e\\n\")")
        #expect(rewriteApplyScript("Hi", below: false) == "window.malachi.rewriteApply(false, \"insertText\", \"Hi\")")
        #expect(rewriteApplyScript("a\nb", below: true) == "window.malachi.rewriteApply(true, \"insertHTML\", \"\\u003cbr\\u003ea\\u003cbr\\u003eb\\u003cbr\\u003e\")")
    }

    /// draft.markdown decides what a paste puts in: its HTML when the text
    /// is Markdown, otherwise (plain text, an error, a daemon without the
    /// method, text over the limit) nil for the text as it is.
    @Test func markdownPasteAsksTheDaemon() async throws {
        let fake = try FakeDaemon()
        await fake.on(API.DraftMarkdown.name) { params in
            let p = try JSONDecoder().decode(DraftMarkdownParams.self, from: params)
            switch p.text {
            case "# Plan": return json(#"{"markdown":true,"html":"<h1>Plan</h1>"}"#)
            case "boom": throw RPCError(code: .invalidArgument, message: "too big")
            default: return json(#"{"markdown":false}"#)
            }
        }
        try await fake.start()
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        #expect(await markdownPaste("# Plan", client: client) == "<h1>Plan</h1>")
        #expect(await markdownPaste("just text", client: client) == nil)
        #expect(await markdownPaste("boom", client: client) == nil)
        #expect(await markdownPaste("", client: client) == nil)
        #expect(await markdownPaste(String(repeating: "#", count: API.Limits.maxDraftBodyBytes + 1), client: client) == nil)
        #expect(await fake.calls.filter { $0 == API.DraftMarkdown.name }.count == 3, "nothing sent for empty or oversized text")
        await client.close()
        await fake.stop()

        // An older daemon answers methodNotFound.
        let old = try FakeDaemon()
        try await old.start()
        let oldClient = RPCClient(socketPath: old.path)
        try await oldClient.connect()
        #expect(await markdownPaste("# Plan", client: oldClient) == nil)
        await oldClient.close()
        await old.stop()
    }

    @Test func rewriteTargetDecode() {
        #expect(RewriteTarget.decode(#"{"selected":true,"text":"a\nb"}"#) == RewriteTarget(selected: true, text: "a\nb"))
        #expect(RewriteTarget.decode(#"{"selected":false,"text":""}"#) == RewriteTarget(selected: false, text: ""))
        #expect(RewriteTarget.decode(#"{"text":"x"}"#) == RewriteTarget(selected: false, text: "x"))
        #expect(RewriteTarget.decode("not json") == nil)
        #expect(RewriteTarget.decode(#"{"selected":"yes"}"#) == nil)
        #expect(RewriteTarget.decode(nil) == nil)
        #expect(RewriteTarget.decode(42) == nil)
    }
}
