// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package editor

import (
	"strings"
	"testing"
)

func TestDocument(t *testing.T) {
	body := `<p>hi &amp; bye</p>`
	doc := Document(body)
	if !strings.Contains(doc, body) {
		t.Error("body not embedded verbatim")
	}
	if !strings.Contains(doc, `default-src 'none'`) || !strings.Contains(doc, `img-src cid: data:`) {
		t.Error("CSP missing")
	}
	if !strings.Contains(doc, `<body contenteditable="true">`) {
		t.Error("body not editable")
	}
	if strings.Contains(doc, "%!") {
		t.Errorf("format verb leaked: %s", doc)
	}
}

func TestDecodeMessage(t *testing.T) {
	m, err := decodeMessage(`{"type":"state","bold":true,"block":"h1","align":"center"}`)
	if err != nil || m.Type != "state" || !m.Bold || m.Block != "h1" || m.Align != "center" {
		t.Errorf("state: %+v %v", m, err)
	}
	m, err = decodeMessage(`{"type":"changed","seq":3,"html":"<p>x</p>","text":"x"}`)
	if err != nil || m.Seq != 3 || m.HTML != "<p>x</p>" || m.Text != "x" {
		t.Errorf("changed: %+v %v", m, err)
	}
	if _, err := decodeMessage("not json"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestJSString(t *testing.T) {
	got := jsString("a\"b\\c </script>")
	if strings.Contains(got, " ") || strings.Contains(got, `"b`) && !strings.Contains(got, `\"b`) {
		t.Errorf("unsafe literal: %s", got)
	}
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Errorf("not a string literal: %s", got)
	}
}

// The rewrite's answer goes in as plain text: never markup (macOS
// EditorBridgeTests rewriteInsertion).
func TestRewriteInsertion(t *testing.T) {
	for _, c := range []struct {
		text     string
		below    bool
		cmd, arg string
	}{
		{"Dobrý den.", false, "insertText", "Dobrý den."},
		{"<b>x</b> & 'y'", false, "insertText", "<b>x</b> & 'y'"},
		{"a\n\nb <i>", false, "insertHTML", "a<br><br>b &lt;i&gt;"},
		{"a\r\nb", false, "insertHTML", "a<br>b"},
		{"x & y", true, "insertHTML", "<br>x &amp; y<br>"},
		{"a\nb", true, "insertHTML", "<br>a<br>b<br>"},
		{"</script>\"", true, "insertHTML", "<br>&lt;/script&gt;&#34;<br>"},
	} {
		cmd, arg := RewriteInsertion(c.text, c.below)
		if cmd != c.cmd || arg != c.arg {
			t.Errorf("RewriteInsertion(%q, %v) = (%q, %q), want (%q, %q)", c.text, c.below, cmd, arg, c.cmd, c.arg)
		}
	}
}

// The page posts the passage of a rewrite; the bridge has both functions.
func TestRewriteMessage(t *testing.T) {
	m, err := decodeMessage(`{"type":"rewrite","selected":true,"text":"a\nb"}`)
	if err != nil || m.Type != "rewrite" || !m.Selected || m.Text != "a\nb" {
		t.Errorf("rewrite: %+v %v", m, err)
	}
	for _, fn := range []string{"rewriteTarget(attribution)", "rewriteApply(below, c, a)", "post({type: 'rewrite'"} {
		if !strings.Contains(bridgeJS, fn) {
			t.Errorf("bridgeJS lacks %s", fn)
		}
	}
}
