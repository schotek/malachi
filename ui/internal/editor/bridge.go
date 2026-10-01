// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package editor

import (
	"encoding/json"
	"html"
	"strings"
)

// bridgeJS runs in the page's main world after the document is parsed. It
// only observes (content changes, selection formatting) and handles the
// Ctrl+B/I/U keys; every formatting command comes from Go through WebKit's
// native editing commands. Messages to Go are JSON strings posted to the
// "malachi" script message handler.
//
// Two functions serve the assistant's rewrite (ui/internal/assistant, the
// In App target; the macOS bridge has them too, returning its result):
// rewriteTarget(attribution) notes the passage to rewrite and posts it
// ("rewrite": selected, text), the selection when it holds more than white
// space, otherwise the user's own text, which is everything before the
// first div whose text is the attribution line the compose window put
// above the quoted original (white space compared collapsed), or the whole
// body when there is none; rewriteApply(below, cmd, arg) selects that
// passage again (its end when below) and runs one editing command there
// (RewriteInsertion), which the page's undo takes back as one step, and
// reports the change as typing does.
//
// A paste of plain text that looks like Markdown (markdownHint), with no
// rich HTML on the clipboard (richHTML), is held back and posted to Go
// ("paste": id, text), which asks the daemon (draft.markdown) and answers
// with pasted(id, html) to insert the rendered HTML where the paste went,
// or pasted(id, null) to paste the text as it is. Every other paste is
// WebKit's own. The regexes write the backtick as \x60.
const bridgeJS = `(() => {
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
  let passage = null;
  const collapsed = s => String(s || '').replace(/\s+/g, ' ').trim();
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
    },
    rewriteTarget(attribution) {
      const sel = document.getSelection();
      passage = null;
      if (sel.rangeCount && !sel.isCollapsed && document.body.contains(sel.getRangeAt(0).commonAncestorContainer) &&
          sel.toString().trim()) {
        passage = sel.getRangeAt(0).cloneRange();
        post({type: 'rewrite', selected: true, text: sel.toString()});
        return;
      }
      const r = document.createRange();
      r.selectNodeContents(document.body);
      const want = collapsed(attribution);
      const mark = want && Array.from(document.body.querySelectorAll('div')).find(d => collapsed(d.innerText) === want);
      if (mark) {
        let prev = mark;
        while (prev !== document.body && !prev.previousSibling) prev = prev.parentNode;
        prev = prev === document.body ? null : prev.previousSibling;
        if (prev) r.setEnd(prev, prev.nodeType === Node.TEXT_NODE ? prev.length : prev.childNodes.length);
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
      document.body.focus();
      const sel = document.getSelection();
      sel.removeAllRanges();
      sel.addRange(r);
      document.execCommand(c, false, a);
      schedule();
      state();
    },
    pasted(id, h) {
      if (!pending || pending.id !== id) return;
      const p = pending;
      pending = null;
      document.body.focus();
      const sel = document.getSelection();
      if (p.range) { sel.removeAllRanges(); sel.addRange(p.range); }
      document.execCommand(h ? 'insertHTML' : 'insertText', false, h || p.text);
      schedule();
      state();
    }
  };
  post({type: 'ready'});
})();`

// bridgeMessage is what the page posts.
type bridgeMessage struct {
	Type string `json:"type"`
	Seq  int    `json:"seq"`
	// ID is the "paste" message's: what pasted() answers to.
	ID   int    `json:"id"`
	HTML string `json:"html"`
	Text string `json:"text"`
	// Selected is the "rewrite" message's: the passage is the selection.
	Selected bool `json:"selected"`
	State
}

// RewriteTarget is what the compose window's rewrite works on, as the
// bridge reports it: the selection (Selected), or the user's own text
// above the quoted original, and its text as the page renders it
// (paragraphs and line breaks as newlines). Mail text: shown and sent only
// as plain text.
type RewriteTarget struct {
	Selected bool
	Text     string
}

// RewriteInsertion is the editing command that puts the rewrite's answer
// into the message as plain text: in place of the passage one line with
// insertText, several lines as escaped HTML (each line break a <br>) with
// insertHTML; below the passage (below) always the latter, on a line of
// its own (a <br> before it, and one after it that the page shows only
// when the passage's line goes on). Nothing of text is ever markup.
func RewriteInsertion(text string, below bool) (command, argument string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	escaped := strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	switch {
	case below:
		return "insertHTML", "<br>" + escaped + "<br>"
	case !strings.Contains(text, "\n"):
		return "insertText", text
	}
	return "insertHTML", escaped
}

// State is the formatting at the caret, for toolbar toggles.
type State struct {
	Bold      bool   `json:"bold"`
	Italic    bool   `json:"italic"`
	Underline bool   `json:"underline"`
	Strike    bool   `json:"strike"`
	UL        bool   `json:"ul"`
	OL        bool   `json:"ol"`
	Link      bool   `json:"link"`
	Block     string `json:"block"` // "p", "h1", "blockquote", …
	Align     string `json:"align"` // left | center | right
}

func decodeMessage(raw string) (bridgeMessage, error) {
	var m bridgeMessage
	err := json.Unmarshal([]byte(raw), &m)
	return m, err
}

// jsString renders s as a JavaScript string literal. json.Marshal escapes
// quotes, backslashes, control characters and U+2028/2029, so the result
// is safe to splice into a script.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
