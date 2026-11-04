// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The page the browser shows after the provider redirected back. Its
// sentences come from the UI (PageTexts); without them it shows the
// product name, a symbol and, on failure, a technical error name. It runs
// no script and loads nothing.

// maxPageText caps each text, in characters.
const maxPageText = 200

// maxErrorName caps the technical error name shown on a failure page.
const maxErrorName = 64

// pageTitle is the product name, the title of a page without texts.
const pageTitle = "Malachi Mail"

func pageTextsFrom(p *api.OAuthBrowserPage) PageTexts {
	if p == nil {
		return PageTexts{}
	}
	return cleanTexts(PageTexts{
		SuccessTitle: p.SuccessTitle,
		SuccessText:  p.SuccessText,
		FailureTitle: p.FailureTitle,
		FailureText:  p.FailureText,
	})
}

func cleanTexts(p PageTexts) PageTexts {
	return PageTexts{
		SuccessTitle: cleanPageText(p.SuccessTitle),
		SuccessText:  cleanPageText(p.SuccessText),
		FailureTitle: cleanPageText(p.FailureTitle),
		FailureText:  cleanPageText(p.FailureText),
	}
}

// cleanPageText: valid UTF-8, line breaks and tabs become spaces, other
// control characters go, at most maxPageText characters.
func cleanPageText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxPageText {
		s = strings.TrimSpace(string([]rune(s)[:maxPageText]))
	}
	return s
}

// errorName keeps what may be shown as a technical error name: letters,
// digits, '_', '-', '.', at most maxErrorName bytes; anything else yields "".
func errorName(s string) string {
	if s == "" || len(s) > maxErrorName {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return ""
		}
	}
	return s
}

type pageKind int

const (
	pageNeutral pageKind = iota
	pageSuccess
	pageFailure
)

type pageData struct {
	Title   string
	Glyph   string
	Class   string
	Heading string
	Text    string
	Detail  string
}

// pageTemplate is escaped by html/template; every value is plain text.
var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; }
body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
       font-family: system-ui, -apple-system, "Cantarell", "Segoe UI", sans-serif;
       background: Canvas; color: CanvasText; }
main { max-width: 32rem; padding: 2rem 1.5rem; text-align: center; }
.glyph { font-size: 5rem; line-height: 1; margin-bottom: 1rem; }
.ok { color: #2ec27e; }
.fail { color: #e01b24; }
h1 { font-size: 1.5rem; margin: 0 0 .75rem; }
p { font-size: 1rem; line-height: 1.5; margin: 0 0 .75rem; }
.detail { font-family: ui-monospace, monospace; font-size: .875rem; opacity: .7; }
</style>
</head>
<body>
<main>
{{- if .Glyph}}
<div class="glyph {{.Class}}" aria-hidden="true">{{.Glyph}}</div>
{{- end}}
{{- if .Heading}}
<h1>{{.Heading}}</h1>
{{- end}}
{{- if .Text}}
<p>{{.Text}}</p>
{{- end}}
{{- if .Detail}}
<p class="detail">{{.Detail}}</p>
{{- end}}
</main>
</body>
</html>
`))

// renderPage builds the page; detail is the technical error name of a
// failure (cleaned again here).
func renderPage(kind pageKind, texts PageTexts, detail string) []byte {
	d := pageData{Title: pageTitle}
	switch kind {
	case pageSuccess:
		d.Glyph, d.Class = "✓", "ok"
		d.Heading, d.Text = texts.SuccessTitle, texts.SuccessText
	case pageFailure:
		d.Glyph, d.Class = "✗", "fail"
		d.Heading, d.Text = texts.FailureTitle, texts.FailureText
		d.Detail = errorName(detail)
	}
	if d.Heading != "" {
		d.Title = d.Heading
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, d); err != nil {
		// Cannot happen with string fields; answer something neutral.
		return []byte("<!DOCTYPE html><title>" + pageTitle + "</title>")
	}
	return buf.Bytes()
}

// pageCSP allows the inline style and nothing else: no script, no load,
// no framing, no form submission, no <base>.
const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; form-action 'none'; base-uri 'none'"

// writePage sends a page with the hardening headers.
func writePage(w http.ResponseWriter, status int, kind pageKind, texts PageTexts, detail string) {
	body := renderPage(kind, texts, detail)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", pageCSP)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
