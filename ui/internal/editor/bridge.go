package editor

import "encoding/json"

// bridgeJS runs in the page's main world after the document is parsed. It
// only observes (content changes, selection formatting) and handles the
// Ctrl+B/I/U keys; every formatting command comes from Go through WebKit's
// native editing commands. Messages to Go are JSON strings posted to the
// "malachi" script message handler.
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
})();`

// bridgeMessage is what the page posts.
type bridgeMessage struct {
	Type string `json:"type"`
	Seq  int    `json:"seq"`
	HTML string `json:"html"`
	Text string `json:"text"`
	State
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
