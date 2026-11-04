// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorBridge.swift (bridgeJS,
// jsString, rewriteInsertion, rewriteApplyScript, RewriteTarget.script) and
// of the scripts macos/Sources/MalachiMail/Compose/ComposeEditorView.swift
// evaluates; GTK: ui/internal/editor/bridge.go (bridgeJS, jsString,
// RewriteInsertion) and editor.go (Flush, Exec, FocusStart, RewriteTarget,
// ApplyRewrite).
//
// The script that runs in the compose editor's page (docs/windows-port.md
// §6.5), a third copy beside bridge.go and the Swift one. The assistant's
// rewrite (rewriteTarget, rewriteApply) is GTK's: the passage is posted as a
// "rewrite" message. WebView2's ExecuteScriptAsync could return it, as
// macOS's bridge does, but staying in GTK's shape keeps the drift from
// bridge.go to the deltas below. So is the paste of Markdown: the page posts
// a "paste" message, the host answers with pasted(id, html | null). It is
// GTK's script with these deltas, each checked by EditorBridgeDriftTests
// against ui/internal/editor/bridge.go:
//   1. It is injected with AddScriptToExecuteOnDocumentCreatedAsync, so it
//      runs in every frame and on every navigation, before the document
//      exists: it returns unless it is the top frame on a document under
//      DocumentUrlPrefix, and GTK's body (indented one level) runs in `run`
//      at DOMContentLoaded.
//   2. WebView2 has no isolated world: the bridge shares the page's, whose
//      named elements shadow document properties (a pasted <img
//      name="body"> makes document.body that image, and flush() would post
//      its empty innerHTML as the draft). A <form> does the same to its own
//      properties with its named controls (Document and HTMLFormElement are
//      the interfaces whose named properties override built-ins), and the
//      nodes the bridge walks may be one: the caret's parent, and the
//      rewrite's way up and back from the attribution's div (a <form><input
//      name="parentNode"> would send it elsewhere). The window's named
//      properties (an element's id) come before EventTarget.prototype too,
//      so the sized mode's load listener goes through the captured
//      addEventListener as well. So every Document and
//      EventTarget accessor the bridge uses (body, documentElement,
//      getSelection, queryCommandState, queryCommandValue, execCommand,
//      createRange, addEventListener) and every accessor it reads of such a node
//      (parentElement, parentNode, previousSibling, nodeType, childNodes,
//      Element.closest) is captured from the prototypes before any content
//      exists and called on the document or the node. What it reads of the
//      body element, of a div, a text node, a range or the selection is
//      called directly: none of them has named properties.
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
using Malachi.Core.Api;
using Malachi.Core.Compose;

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
    /// Two functions serve the assistant's rewrite:
    /// <c>rewriteTarget(attribution)</c> notes the passage to rewrite and
    /// posts it (<c>rewrite</c>: <c>selected</c>, <c>text</c>), the selection
    /// when it holds more than white space, otherwise the user's own text,
    /// everything before the first div whose text is the attribution line the
    /// compose window put above the quoted original (white space compared
    /// collapsed), or the whole body when there is none;
    /// <c>rewriteApply(below, cmd, arg)</c> selects that passage again (its
    /// end when <c>below</c>) and runs one editing command there
    /// (<see cref="RewriteInsertion"/>), which the page's undo takes back as
    /// one step, and reports the change as typing does.
    /// A paste of plain text that looks like Markdown, with no rich HTML
    /// flavour beside it (and no files), is taken from Chromium and posted as
    /// <c>paste</c> (<c>id</c>, <c>text</c>); the host asks the daemon
    /// (<c>draft.markdown</c>) and answers with <c>pasted(id, html)</c>, or
    /// <c>pasted(id, null)</c> for the plain text (<see cref="PastedScript"/>),
    /// which inserts it where the caret was, as typing is reported. Only the
    /// latest paste is answered.
    /// A ResizeObserver on the document element, debounced (100 ms), posts
    /// the document's height (<c>height</c>: <c>h</c>, CSS pixels) whenever
    /// it changes, on every input, paste and resize: the sized mode of the
    /// board's inline reply (<see cref="EditorChannel.SizeReported"/>, raised
    /// only by a channel made sized). It only reads the layout.
    /// </summary>
    public const string Script = """
        (() => {
          if (window !== window.top || !String(window.location.href).startsWith('malachi-doc://editor/')) return;
          const D = Document.prototype, E = EventTarget.prototype, N = Node.prototype;
          const getBody = Object.getOwnPropertyDescriptor(D, 'body').get;
          const getRoot = Object.getOwnPropertyDescriptor(D, 'documentElement').get;
          const getParent = Object.getOwnPropertyDescriptor(N, 'parentElement').get;
          const getParentNode = Object.getOwnPropertyDescriptor(N, 'parentNode').get;
          const getPrevious = Object.getOwnPropertyDescriptor(N, 'previousSibling').get;
          const getNodeType = Object.getOwnPropertyDescriptor(N, 'nodeType').get;
          const getChildNodes = Object.getOwnPropertyDescriptor(N, 'childNodes').get;
          const getSelection = D.getSelection, queryCommandState = D.queryCommandState, queryCommandValue = D.queryCommandValue;
          const execCommand = D.execCommand, createRange = D.createRange, addEventListener = E.addEventListener;
          const closest = Element.prototype.closest;
          const webview = window.chrome.webview, postMessage = webview.postMessage, postWith = webview.postMessageWithAdditionalObjects;
          const body = () => getBody.call(document);
          const root = () => getRoot.call(document);
          const selection = () => getSelection.call(document);
          const parent = n => getParent.call(n);
          const on = (type, f, options) => addEventListener.call(document, type, f, options);
          const run = () => {
            const post = m => postMessage.call(webview, JSON.stringify(m));
            let seq = 0, timer = null;
            let lastHeight = -1, heightTimer = null;
            const postHeight = () => {
              const h = root().scrollHeight;
              if (h !== lastHeight) { lastHeight = h; post({type: 'height', h: h}); }
            };
            const scheduleHeight = () => { if (heightTimer) clearTimeout(heightTimer); heightTimer = setTimeout(postHeight, 100); };
            try {
              new ResizeObserver(scheduleHeight).observe(root());
            } catch (e) {}
            addEventListener.call(window, 'load', scheduleHeight);
            const flush = () => {
              if (timer) { clearTimeout(timer); timer = null; }
              post({type: 'changed', seq: ++seq, html: body().innerHTML, text: body().innerText});
              return seq;
            };
            const schedule = () => { if (timer) clearTimeout(timer); timer = setTimeout(flush, 250); scheduleHeight(); };
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
            let pending = null, pasteSeq = 0;
            const markdownHint = /^ {0,3}(#{1,6} |[-*+] |\d{1,9}[.)] |>|\x60{3}|~~~|\|)|\*\*|__|~~|\x60[^\x60\n]+\x60|\]\(|^ {0,3}(-{3,}|\*{3,}|_{3,}) *$/m;
            const richHTML = /<(h[1-6]|ul|ol|li|b|strong|i|em|a|table|blockquote)[\s>]/i;
            on('paste', e => {
              const d = e.clipboardData;
              if (!d || Array.from(d.types || []).includes('Files')) return;
              const text = d.getData('text/plain');
              if (!text || !markdownHint.test(text) || richHTML.test(d.getData('text/html') || '')) return;
              e.preventDefault();
              const sel = selection();
              pending = {id: ++pasteSeq, text, range: sel.rangeCount ? sel.getRangeAt(0).cloneRange() : null};
              post({type: 'paste', id: pending.id, text});
            });
            let passage = null;
            const collapsed = s => String(s || '').replace(/\s+/g, ' ').trim();
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
              },
              rewriteTarget(attribution) {
                const sel = selection();
                passage = null;
                if (sel.rangeCount && !sel.isCollapsed && body().contains(sel.getRangeAt(0).commonAncestorContainer) &&
                    sel.toString().trim()) {
                  passage = sel.getRangeAt(0).cloneRange();
                  post({type: 'rewrite', selected: true, text: sel.toString()});
                  return;
                }
                const r = createRange.call(document);
                r.selectNodeContents(body());
                const want = collapsed(attribution);
                const mark = want && Array.from(body().querySelectorAll('div')).find(d => collapsed(d.innerText) === want);
                if (mark) {
                  let prev = mark;
                  while (prev !== body() && !getPrevious.call(prev)) prev = getParentNode.call(prev);
                  prev = prev === body() ? null : getPrevious.call(prev);
                  if (prev) r.setEnd(prev, getNodeType.call(prev) === Node.TEXT_NODE ? prev.length : getChildNodes.call(prev).length);
                  else r.collapse(true);
                }
                const saved = sel.rangeCount ? sel.getRangeAt(0).cloneRange() : null;
                sel.removeAllRanges();
                sel.addRange(r);
                const text = sel.toString();
                sel.removeAllRanges();
                if (saved) sel.addRange(saved);
                passage = r;
                post({type: 'rewrite', selected: false, text});
              },
              rewriteApply(below, c, a) {
                if (!passage) return;
                const r = passage.cloneRange();
                passage = null;
                if (below) r.collapse(false);
                body().focus();
                const sel = selection();
                sel.removeAllRanges();
                sel.addRange(r);
                execCommand.call(document, c, false, a);
                schedule();
                state();
              },
              pasted(id, h) {
                if (!pending || pending.id !== id) return;
                const p = pending;
                pending = null;
                body().focus();
                const sel = selection();
                if (p.range) { sel.removeAllRanges(); sel.addRange(p.range); }
                execCommand.call(document, h ? 'insertHTML' : 'insertText', false, h || p.text);
                schedule();
                state();
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
    /// editor.RewriteTarget (macOS <c>RewriteTarget.script</c>): the script
    /// that notes the passage and posts it, the selection or the text before
    /// the div of <paramref name="attribution"/> (the whole body when "").
    /// The attribution is mail data (a sender's name): a string literal.
    /// </summary>
    public static string RewriteTargetScript(string attribution)
    {
        ArgumentNullException.ThrowIfNull(attribution);
        return "window.malachi.rewriteTarget(" + JsString(attribution) + ")";
    }

    /// <summary>
    /// editor.RewriteInsertion (macOS <c>rewriteInsertion</c>): the editing
    /// command that puts the rewrite's answer into the message as plain text:
    /// in place of the passage one line with <c>insertText</c>, several lines
    /// as escaped HTML (each line break a <c>&lt;br&gt;</c>) with
    /// <c>insertHTML</c>; below the passage (<paramref name="below"/>) always
    /// the latter, on a line of its own (a <c>&lt;br&gt;</c> before it, and
    /// one after it that the page shows only when the passage's line goes
    /// on). Nothing of <paramref name="text"/> is ever markup: the escaping
    /// is html.EscapeString's (<see cref="Prefill.EscapeText"/>).
    /// </summary>
    public static (string Command, string Argument) RewriteInsertion(string text, bool below)
    {
        ArgumentNullException.ThrowIfNull(text);
        text = text.Replace("\r\n", "\n", StringComparison.Ordinal);
        var escaped = Prefill.EscapeText(text);
        if (below)
        {
            return ("insertHTML", "<br>" + escaped + "<br>");
        }
        if (!text.Contains('\n', StringComparison.Ordinal))
        {
            return ("insertText", text);
        }
        return ("insertHTML", escaped);
    }

    /// <summary>
    /// editor.ApplyRewrite (macOS <c>rewriteApplyScript</c>): the script that
    /// puts the rewrite's answer in place of the passage
    /// <c>rewriteTarget</c> noted, or below it (<see cref="RewriteInsertion"/>).
    /// </summary>
    public static string RewriteApplyScript(string text, bool below)
    {
        var (command, argument) = RewriteInsertion(text, below);
        return "window.malachi.rewriteApply(" + (below ? "true" : "false") + ", " + JsString(command) + ", " + JsString(argument) + ")";
    }

    /// <summary>
    /// The answer to the page's <c>paste</c> <paramref name="id"/>: the
    /// script that inserts <paramref name="html"/> (the daemon's sanitised
    /// rendering of the pasted Markdown) where the caret was, or the pasted
    /// text as it is when <paramref name="html"/> is null or empty. The HTML
    /// goes in as a string literal; a paste the page no longer waits for (a
    /// later one, a new document) is ignored by the page.
    /// </summary>
    public static string PastedScript(long id, string? html)
    {
        var argument = string.IsNullOrEmpty(html) ? "null" : JsString(html);
        return "window.malachi.pasted(" + id.ToString(CultureInfo.InvariantCulture) + ", " + argument + ")";
    }

    /// <summary>
    /// What <c>draft.markdown</c> answered, as <see cref="PastedScript"/>
    /// takes it: the HTML when the text reads as Markdown and the rendering
    /// is not empty; null (paste the text as it is) otherwise, and for no
    /// answer at all (an error, an older daemon, none running).
    /// </summary>
    public static string? PastedHtml(DraftMarkdownResult? result) =>
        result is { Markdown: true, Html: { Length: > 0 } html } ? html : null;

    /// <summary>
    /// Whether the pasted <paramref name="text"/> goes to <c>draft.markdown</c>
    /// at all: the daemon refuses more than
    /// <see cref="API.Limits.MaxDraftBodyBytes"/> of UTF-8, and the empty
    /// text has nothing to convert.
    /// </summary>
    public static bool AsksForMarkdown(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        return text.Length > 0 && Encoding.UTF8.GetByteCount(text) <= API.Limits.MaxDraftBodyBytes;
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
