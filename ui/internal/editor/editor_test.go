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
