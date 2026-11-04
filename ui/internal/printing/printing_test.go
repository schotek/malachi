// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package printing

import (
	"reflect"
	"strings"
	"testing"
)

type ids struct{}

func (ids) T(m string) string           { return m }
func (ids) N(m, p string, n int) string { return m }
func (ids) C(context, m string) string  { return m }

func TestHeaderRowsLeaveOutEmptyFields(t *testing.T) {
	got := HeaderRows(ids{}, Header{
		From: "Ann <ann@example.com>", To: "bob@example.com", Cc: "  ", Date: "Wed, 4 Nov 2026 at 9:41",
		Subject: "Hello", Attachments: []string{"a.pdf (41.1 kB)", "b.png (2 MB)"},
	})
	want := []Row{
		{"Subject", "Hello", true}, {"From", "Ann <ann@example.com>", false}, {"To", "bob@example.com", false},
		{"Date", "Wed, 4 Nov 2026 at 9:41", false}, {"Attachments", "a.pdf (41.1 kB), b.png (2 MB)", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HeaderRows = %v", got)
	}
	if got := HeaderRows(ids{}, Header{Bcc: "me@example.com"}); !reflect.DeepEqual(got, []Row{{"Bcc", "me@example.com", false}}) {
		t.Errorf("Bcc = %v", got)
	}
}

func TestEscape(t *testing.T) {
	in := `<script>alert('x')</script> & "q" ✦`
	want := `&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt; &amp; &quot;q&quot; ✦`
	if got := Escape(in); got != want {
		t.Errorf("Escape = %q", got)
	}
}

func TestMessageEscapesTheHeaderAndKeepsTheSanitisedBody(t *testing.T) {
	rows := []Row{{"Subject", `<b>x</b>`, true}, {"From", `"Eve" <eve@evil.example>`, false}}
	got := Message(rows, Body{HTML: `<p>Hi <img src="malachi-cid:1"></p>`})
	if strings.Contains(got, "<b>x</b>") || strings.Contains(got, `<eve@`) {
		t.Errorf("header not escaped: %s", got)
	}
	if !strings.Contains(got, `<p>Hi <img src="malachi-cid:1"></p>`) {
		t.Errorf("body not verbatim: %s", got)
	}
	if !strings.Contains(got, `<tr class="malachi-print-subject"><th>Subject</th><td>&lt;b&gt;x&lt;/b&gt;</td></tr>`) {
		t.Errorf("subject row: %s", got)
	}
}

func TestMessageTextBodyIsEscapedInPre(t *testing.T) {
	got := Message(nil, Body{Text: "a < b\n</pre><script>"})
	want := `<table class="malachi-print-header"></table><div class="malachi-print-body">` +
		`<pre class="malachi-print-text">a &lt; b` + "\n" + `&lt;/pre&gt;&lt;script&gt;</pre></div>`
	if got != want {
		t.Errorf("Message(text) = %q", got)
	}
}

func TestFragmentLayouts(t *testing.T) {
	secs := []string{"A", "B", "C"}
	css := `<style>` + CSS + `</style>`
	if got := Fragment(secs, Conversation); got != css+`A<hr class="malachi-print-sep">B<hr class="malachi-print-sep">C` {
		t.Errorf("conversation = %q", got[len(css):])
	}
	if got := Fragment(secs, Pages); got != css+`A<div class="malachi-print-page"></div>B<div class="malachi-print-page"></div>C` {
		t.Errorf("pages = %q", got[len(css):])
	}
	if got := Fragment(secs[:1], Single); got != css+"A" {
		t.Errorf("single = %q", got[len(css):])
	}
}

func TestEventSection(t *testing.T) {
	got := EventSection(Event{
		Title: "Review <draft>", Lines: []string{"Wed 4 Nov, 9:00–10:00", "", "Room & Hall"},
		Attendees: []string{"Ann — Accepted"}, Notes: "line 1\n<i>2</i>",
	})
	want := `<h1 class="malachi-print-title">Review &lt;draft&gt;</h1>` +
		`<p class="malachi-print-line">Wed 4 Nov, 9:00–10:00</p>` +
		`<p class="malachi-print-line">Room &amp; Hall</p>` +
		`<p class="malachi-print-line">Ann — Accepted</p>` +
		`<div class="malachi-print-notes">line 1` + "\n" + `&lt;i&gt;2&lt;/i&gt;</div>`
	if got != want {
		t.Errorf("EventSection = %q", got)
	}
}

func TestDaySection(t *testing.T) {
	got := DaySection("Wednesday 4 November", []DayItem{
		{Time: "All Day", Title: "Holiday"}, {Time: "9:00–10:00", Title: "Review", Location: "<Room>"},
	}, "No events")
	want := `<h1 class="malachi-print-title">Wednesday 4 November</h1><table class="malachi-print-day">` +
		`<tr><td class="time">All Day</td><td>Holiday</td></tr>` +
		`<tr><td class="time">9:00–10:00</td><td>Review<br>&lt;Room&gt;</td></tr></table>`
	if got != want {
		t.Errorf("DaySection = %q", got)
	}
	if got := DaySection("Today", nil, "No events"); got != `<h1 class="malachi-print-title">Today</h1><p class="malachi-print-line">No events</p>` {
		t.Errorf("empty day = %q", got)
	}
}

func TestTexts(t *testing.T) {
	if MenuTitle(ids{}) != "_Print…" || ItemLabel(ids{}) != "Print" {
		t.Error("texts changed")
	}
}
