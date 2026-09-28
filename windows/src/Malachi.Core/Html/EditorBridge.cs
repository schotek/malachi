// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorBridge.swift (bridgeJS,
// jsString) and of the scripts macos/Sources/MalachiMail/Compose/
// ComposeEditorView.swift evaluates; GTK: ui/internal/editor/bridge.go
// (bridgeJS, jsString) and editor.go (Flush, Exec, FocusStart).
//
// The script that runs in the compose editor's page (docs/windows-port.md
// §6.5), a third copy beside bridge.go and the Swift one. It is GTK's
// script with these deltas, each checked by EditorBridgeDriftTests against
// ui/internal/editor/bridge.go:
//   1. It is injected with AddScriptToExecuteOnDocumentCreatedAsync, so it
//      runs in every frame and on every navigation, before the document
//      exists: it returns unless it is the top frame on a document under
//      DocumentUrlPrefix, and GTK's body (indented one level) runs in `run`
//      at DOMContentLoaded.
//   2. WebView2 has no isolated world: the bridge shares the page's, whose
//      named elements shadow document properties (a pasted <img
//      name="body"> makes document.body that image, and flush() would post
//      its empty innerHTML as the draft). Every Document, EventTarget and
//      Node accessor the bridge uses (body, getSelection, queryCommandState,
//      queryCommandValue, execCommand, createRange, addEventListener,
//      parentElement, Element.closest) is captured from the prototypes
//      before any content exists and called on the document.
//   3. Messages go to chrome.webview.postMessage (captured with the
//      webview), not to WebKit's script message handler.
//   4. Keys (docs/windows-port.md §11.5): GTK's Ctrl-only test stays (the
//      Windows key must not count; AltGr is Ctrl+Alt and does not count
//      either), so Ctrl+Shift+I is italic as in GTK and Chromium never types
//      its Tab. Escape (outside an IME composition) and Ctrl+K are prevented
//      and posted as {type: 'key', key: 'escape' | 'link'} for the window,
//      which gets no key event while the WebView2 has focus.
//   5. Files dropped on the page are taken from Chromium in the capture
//      phase and posted with postMessageWithAdditionalObjects as {type:
//      'drop'}; other drops stay Chromium's editing (GTK's DropTarget takes
//      file lists only).
//   6. macOS's window.malachi.exec(command, argument): WebView2 has no
//      editing-command API of its own.

using System;
using System.Globalization;
using System.Text;

namespace Malachi.Core.Html;

/// <summary>The editor page's script and the scripts the host evaluates in it.</summary>
public static class EditorBridge
{
    private const char LineSeparator = (char)0x2028;

    private const char ParagraphSeparator = (char)0x2029;

    /// <summary>
    /// Where the editor's documents are served (docs/windows-port.md §6.2:
    /// <c>malachi-doc://editor/&lt;generation&gt;-&lt;nonce&gt;</c>); the bridge
    /// installs itself on nothing else.
    /// </summary>
    public const string DocumentUrlPrefix = "malachi-doc://editor/";

    /// <summary>
    /// <c>window.malachi.flush()</c>: posts a <c>changed</c> with the body's
    /// content and returns its <c>seq</c>.
    /// </summary>
    public const string FlushScript = "window.malachi.flush()";

    /// <summary><c>window.malachi.focusStart()</c>: puts the caret at the start of the body.</summary>
    public const string FocusStartScript = "window.malachi.focusStart()";

    /// <summary>
    /// The bridge (editor.bridgeJS with the deltas of this file's header). It
    /// only observes (content changes, the formatting at the caret) and
    /// handles the keys above; every other formatting command comes from the
    /// host through <c>window.malachi.exec</c>. Messages are JSON strings.
    /// </summary>
    public const string Script = """
        (() => {
          if (window !== window.top || !String(window.location.href).startsWith('malachi-doc://editor/')) return;
          const D = Document.prototype, E = EventTarget.prototype, N = Node.prototype;
          const getBody = Object.getOwnPropertyDescriptor(D, 'body').get;
          const getParent = Object.getOwnPropertyDescriptor(N, 'parentElement').get;
          const getSelection = D.getSelection, queryCommandState = D.queryCommandState, queryCommandValue = D.queryCommandValue;
          const execCommand = D.execCommand, createRange = D.createRange, addEventListener = E.addEventListener;
          const closest = Element.prototype.closest;
          const webview = window.chrome.webview, postMessage = webview.postMessage, postWith = webview.postMessageWithAdditionalObjects;
          const body = () => getBody.call(document);
          const selection = () => getSelection.call(document);
          const parent = n => getParent.call(n);
          const on = (type, f, options) => addEventListener.call(document, type, f, options);
          const run = () => {
            const post = m => postMessage.call(webview, JSON.stringify(m));
            let seq = 0, timer = null;
            const flush = () => {
              if (timer) { clearTimeout(timer); timer = null; }
              post({type: 'changed', seq: ++seq, html: body().innerHTML, text: body().innerText});
              return seq;
            };
            const schedule = () => { if (timer) clearTimeout(timer); timer = setTimeout(flush, 250); };
            const q = c => { try { return queryCommandState.call(document, c); } catch (e) { return false; } };
            const state = () => post({
              type: 'state',
              bold: q('bold'), italic: q('italic'), underline: q('underline'), strike: q('strikethrough'),
              ul: q('insertUnorderedList'), ol: q('insertOrderedList'),
              block: String(queryCommandValue.call(document, 'formatBlock') || '').toLowerCase(),
              align: q('justifyCenter') ? 'center' : q('justifyRight') ? 'right' : 'left',
              link: !!(selection().anchorNode && parent(selection().anchorNode) &&
                       closest.call(parent(selection().anchorNode), 'a'))
            });
            execCommand.call(document, 'styleWithCSS', false, false);
            on('input', () => { schedule(); state(); });
            on('selectionchange', state);
            on('keydown', e => {
              if (e.key === 'Escape' && !e.ctrlKey && !e.altKey && !e.metaKey && !e.shiftKey && !e.isComposing) {
                e.preventDefault(); post({type: 'key', key: 'escape'}); return;
              }
              if (!e.ctrlKey || e.altKey || e.metaKey) return;
              if (e.key.toLowerCase() === 'k' && !e.shiftKey) { e.preventDefault(); post({type: 'key', key: 'link'}); return; }
              const cmd = {b: 'bold', i: 'italic', u: 'underline'}[e.key.toLowerCase()];
              if (cmd) { e.preventDefault(); execCommand.call(document, cmd); state(); }
            });
            const hasFiles = e => !!e.dataTransfer && Array.prototype.includes.call(e.dataTransfer.types || [], 'Files');
            on('dragover', e => { if (hasFiles(e)) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; } }, true);
            on('drop', e => {
              if (!hasFiles(e) || !e.dataTransfer.files.length) return;
              e.preventDefault(); e.stopPropagation();
              postWith.call(webview, JSON.stringify({type: 'drop'}), e.dataTransfer.files);
            }, true);
            window.malachi = {
              flush,
              focusStart() {
                body().focus();
                const sel = selection();
                sel.removeAllRanges();
                const r = createRange.call(document);
                r.setStart(body(), 0);
                r.collapse(true);
                sel.addRange(r);
              }
            };
            window.malachi.exec = (c, a) => { execCommand.call(document, c, false, a == null ? null : a); state(); };
            post({type: 'ready'});
          };
          if (document.readyState === 'loading') on('DOMContentLoaded', run, {once: true}); else run();
        })();
        """;

    /// <summary>
    /// editor.Exec (macOS <c>exec</c>): the script that runs an editing
    /// command (<c>bold</c>, <c>formatBlock</c> with <c>h1</c>,
    /// <c>createLink</c>, <c>foreColor</c>, <c>insertImage</c>, …) on the
    /// current selection. An empty argument is no argument, as in GTK.
    /// </summary>
    public static string ExecScript(string command, string? argument)
    {
        ArgumentNullException.ThrowIfNull(command);
        var arg = string.IsNullOrEmpty(argument) ? "null" : JsString(argument);
        return "window.malachi.exec(" + JsString(command) + ", " + arg + ")";
    }

    /// <summary>
    /// editor.jsString: <paramref name="s"/> as a JavaScript string literal,
    /// escaped the way encoding/json does it: quotes, backslashes, control
    /// characters, <c>&lt;&gt;&amp;</c> and U+2028/2029 as escapes (a lone
    /// surrogate, Go's invalid UTF-8, as \ufffd), so the result is safe to
    /// splice into a script.
    /// </summary>
    public static string JsString(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var output = new StringBuilder(s.Length + 2);
        output.Append('"');
        for (var i = 0; i < s.Length; i++)
        {
            var c = s[i];
            switch (c)
            {
                case '"':
                    output.Append("\\\"");
                    break;
                case '\\':
                    output.Append("\\\\");
                    break;
                case '\b':
                    output.Append("\\b");
                    break;
                case '\f':
                    output.Append("\\f");
                    break;
                case '\n':
                    output.Append("\\n");
                    break;
                case '\r':
                    output.Append("\\r");
                    break;
                case '\t':
                    output.Append("\\t");
                    break;
                case '<':
                    output.Append("\\u003c");
                    break;
                case '>':
                    output.Append("\\u003e");
                    break;
                case '&':
                    output.Append("\\u0026");
                    break;
                case LineSeparator:
                    output.Append("\\u2028");
                    break;
                case ParagraphSeparator:
                    output.Append("\\u2029");
                    break;
                case < ' ':
                    output.Append("\\u00").Append(((int)c).ToString("x2", CultureInfo.InvariantCulture));
                    break;
                case >= (char)0xD800 and <= (char)0xDBFF when i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]):
                    output.Append(c).Append(s[i + 1]);
                    i++;
                    break;
                case >= (char)0xD800 and <= (char)0xDFFF:
                    output.Append("\\ufffd");
                    break;
                default:
                    output.Append(c);
                    break;
            }
        }
        return output.Append('"').ToString();
    }
}
