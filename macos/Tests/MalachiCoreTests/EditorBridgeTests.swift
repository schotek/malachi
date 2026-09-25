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

    /// The script is the GTK one plus the two WKWebView additions.
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
    }
}
