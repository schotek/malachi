// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/editor/bridge.go: the script that runs in the compose
// editor's page and the messages it posts.

import Foundation

/// editor.bridgeJS, byte for byte, with two additions for WKWebView: the
/// keydown shortcut test accepts the Command key as well as Control, and
/// `window.malachi.exec(cmd, arg)` runs an editing command from Swift
/// (WKWebView has no native editing-command API; GTK calls WebKit's). It
/// runs in a content world of its own (`WKContentWorld.defaultClient`,
/// sharing the DOM) after the document is parsed, only
/// observes (content changes, selection formatting) and handles the
/// Ctrl/Cmd+B/I/U keys; every other formatting command comes from Swift.
/// Messages to Swift are JSON strings posted to the "malachi" script
/// message handler.
public let bridgeJS = #"""
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
    if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
    const cmd = {b: 'bold', i: 'italic', u: 'underline'}[e.key.toLowerCase()];
    if (cmd) { e.preventDefault(); document.execCommand(cmd); state(); }
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
  window.malachi.exec = (c, a) => { document.execCommand(c, false, a == null ? null : a); state(); };
  post({type: 'ready'});
})();
"""#

/// editor.State: the formatting at the caret, for toolbar toggles.
public struct EditorState: Sendable, Equatable, Codable {
    public var bold: Bool
    public var italic: Bool
    public var underline: Bool
    public var strike: Bool
    public var ul: Bool
    public var ol: Bool
    public var link: Bool
    /// "p", "h1", "blockquote", …
    public var block: String
    /// left | center | right
    public var align: String

    public init(
        bold: Bool = false, italic: Bool = false, underline: Bool = false, strike: Bool = false,
        ul: Bool = false, ol: Bool = false, link: Bool = false, block: String = "", align: String = ""
    ) {
        self.bold = bold
        self.italic = italic
        self.underline = underline
        self.strike = strike
        self.ul = ul
        self.ol = ol
        self.link = link
        self.block = block
        self.align = align
    }

    private enum CodingKeys: String, CodingKey {
        case bold, italic, underline, strike, ul, ol, link, block, align
    }

    /// A missing field is its zero value, as encoding/json leaves it.
    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        bold = try c.decodeIfPresent(Bool.self, forKey: .bold) ?? false
        italic = try c.decodeIfPresent(Bool.self, forKey: .italic) ?? false
        underline = try c.decodeIfPresent(Bool.self, forKey: .underline) ?? false
        strike = try c.decodeIfPresent(Bool.self, forKey: .strike) ?? false
        ul = try c.decodeIfPresent(Bool.self, forKey: .ul) ?? false
        ol = try c.decodeIfPresent(Bool.self, forKey: .ol) ?? false
        link = try c.decodeIfPresent(Bool.self, forKey: .link) ?? false
        block = try c.decodeIfPresent(String.self, forKey: .block) ?? ""
        align = try c.decodeIfPresent(String.self, forKey: .align) ?? ""
    }
}

/// editor.bridgeMessage: what the page posts: `ready`, `changed` (with
/// `seq`, `html`, `text`) or `state` (with the formatting, flattened into
/// the same object as Go embeds it).
public struct BridgeMessage: Sendable, Equatable, Codable {
    public var type: String
    public var seq: Int
    public var html: String
    public var text: String
    public var state: EditorState

    public init(type: String, seq: Int = 0, html: String = "", text: String = "", state: EditorState = EditorState()) {
        self.type = type
        self.seq = seq
        self.html = html
        self.text = text
        self.state = state
    }

    private enum CodingKeys: String, CodingKey {
        case type, seq, html, text
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decodeIfPresent(String.self, forKey: .type) ?? ""
        seq = try c.decodeIfPresent(Int.self, forKey: .seq) ?? 0
        html = try c.decodeIfPresent(String.self, forKey: .html) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        state = try EditorState(from: decoder)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(type, forKey: .type)
        try c.encode(seq, forKey: .seq)
        try c.encode(html, forKey: .html)
        try c.encode(text, forKey: .text)
        try state.encode(to: encoder)
    }

    /// editor.decodeMessage: one posted JSON string.
    public static func decode(_ raw: String) throws -> BridgeMessage {
        try JSONDecoder().decode(BridgeMessage.self, from: Data(raw.utf8))
    }
}

/// editor.jsString: `s` as a JavaScript string literal, escaped the way
/// encoding/json does it: quotes, backslashes, control characters, `<>&`
/// and U+2028/2029 as escapes, so the result is safe to splice into a
/// script.
public func jsString(_ s: String) -> String {
    var out = "\""
    for u in s.unicodeScalars {
        switch u {
        case "\"": out += "\\\""
        case "\\": out += "\\\\"
        case "\u{08}": out += "\\b"
        case "\u{0C}": out += "\\f"
        case "\n": out += "\\n"
        case "\r": out += "\\r"
        case "\t": out += "\\t"
        case "<": out += "\\u003c"
        case ">": out += "\\u003e"
        case "&": out += "\\u0026"
        case "\u{2028}": out += "\\u2028"
        case "\u{2029}": out += "\\u2029"
        default:
            if u.value < 0x20 {
                out += "\\u00" + String(u.value, radix: 16).leftPadded(to: 2)
            } else {
                out.unicodeScalars.append(u)
            }
        }
    }
    return out + "\""
}

private extension String {
    func leftPadded(to width: Int) -> String {
        count >= width ? self : String(repeating: "0", count: width - count) + self
    }
}
