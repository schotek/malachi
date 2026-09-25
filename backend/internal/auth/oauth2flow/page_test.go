// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestPageTextsFromCleans(t *testing.T) {
	if got := PageTextsFrom(nil); got != (PageTexts{}) {
		t.Errorf("nil → %+v", got)
	}
	long := strings.Repeat("ž", 300)
	got := PageTextsFrom(&api.OAuthBrowserPage{
		SuccessTitle: "  Signed\nin\x00\x07 ",
		SuccessText:  long,
		FailureTitle: "bad \xff utf8",
		FailureText:  "tab\there\u0085end",
	})
	if got.SuccessTitle != "Signed in" {
		t.Errorf("SuccessTitle = %q", got.SuccessTitle)
	}
	if n := utf8.RuneCountInString(got.SuccessText); n != maxPageText {
		t.Errorf("SuccessText has %d runes", n)
	}
	if got.FailureTitle != "bad  utf8" || !utf8.ValidString(got.FailureTitle) {
		t.Errorf("FailureTitle = %q", got.FailureTitle)
	}
	if got.FailureText != "tab hereend" {
		t.Errorf("FailureText = %q", got.FailureText)
	}
}

func TestPageEscapesAndHasNoScript(t *testing.T) {
	texts := PageTexts{
		SuccessTitle: `<script>alert(1)</script>`,
		SuccessText:  `"><img src=x onerror=alert(1)>`,
		FailureTitle: `</title><script>x()</script>`,
		FailureText:  `<a href="javascript:x()">y</a>`,
	}
	for _, kind := range []pageKind{pageSuccess, pageFailure} {
		body := string(renderPage(kind, texts, "access_denied"))
		lower := strings.ToLower(body)
		// Escaped text may still read "src=x"; only raw markup matters.
		for _, bad := range []string{"<script", "<img", "<a ", "</title><"} {
			if strings.Contains(lower, bad) {
				t.Errorf("kind %d: page contains %q:\n%s", kind, bad, body)
			}
		}
		if !strings.Contains(body, "&lt;script&gt;") && !strings.Contains(body, "&lt;img") && !strings.Contains(body, "&lt;a") {
			t.Errorf("kind %d: texts not escaped:\n%s", kind, body)
		}
		if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
			t.Errorf("kind %d: external reference in page", kind)
		}
	}
}

func TestPageFallback(t *testing.T) {
	ok := string(renderPage(pageSuccess, PageTexts{}, ""))
	if !strings.Contains(ok, "<title>Malachi Mail</title>") || !strings.Contains(ok, "✓") {
		t.Errorf("success fallback:\n%s", ok)
	}
	if strings.Contains(ok, "<h1>") || strings.Contains(ok, "<p") {
		t.Errorf("success fallback has sentences:\n%s", ok)
	}
	fail := string(renderPage(pageFailure, PageTexts{}, "access_denied"))
	if !strings.Contains(fail, "<title>Malachi Mail</title>") || !strings.Contains(fail, "✗") || !strings.Contains(fail, "access_denied") {
		t.Errorf("failure fallback:\n%s", fail)
	}
	// A hostile error name is dropped, not shown.
	hostile := string(renderPage(pageFailure, PageTexts{}, "<b>x</b>"))
	if strings.Contains(hostile, "x</b>") || strings.Contains(hostile, "&lt;b&gt;") {
		t.Errorf("hostile error name shown:\n%s", hostile)
	}
	neutral := string(renderPage(pageNeutral, PageTexts{SuccessTitle: "S"}, "x"))
	if strings.Contains(neutral, "✓") || strings.Contains(neutral, "✗") || strings.Contains(neutral, ">S<") {
		t.Errorf("neutral page:\n%s", neutral)
	}
	titled := string(renderPage(pageSuccess, PageTexts{SuccessTitle: "Přihlášeno"}, ""))
	if !strings.Contains(titled, "<title>Přihlášeno</title>") || !strings.Contains(titled, "<h1>Přihlášeno</h1>") {
		t.Errorf("titled page:\n%s", titled)
	}
}

func TestPageHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	writePage(rec, http.StatusOK, pageSuccess, PageTexts{}, "")
	h := rec.Result().Header
	want := map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; form-action 'none'; base-uri 'none'",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}
