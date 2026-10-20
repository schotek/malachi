// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/composebody"
)

func msgItem(id string, mine, sent bool) Item {
	return Item{Kind: ItemMessage, Message: api.MessageSummary{ID: api.MessageID(id)}, Mine: mine, Sent: sent}
}

func TestQuickReplyTarget(t *testing.T) {
	event := Item{Kind: ItemEvent, Message: api.MessageSummary{ID: "e"}}
	more := Item{Kind: ItemTruncated, Text: "2 earlier messages"}
	for _, c := range []struct {
		name  string
		items []Item
		want  string
		ok    bool
	}{
		{"empty", nil, "", false},
		{"events and the row of older messages only", []Item{more, event}, "", false},
		{"the newest of someone else", []Item{more, msgItem("a", false, false), msgItem("b", false, false), msgItem("c", true, false)}, "b", true},
		{"a sent card is the user's", []Item{msgItem("a", false, false), msgItem("s", false, true)}, "a", true},
		{"an event after the message is skipped", []Item{msgItem("a", false, false), event}, "a", true},
		{"all the user's: the newest", []Item{msgItem("a", true, false), msgItem("s", false, true), msgItem("c", true, false), event}, "c", true},
		{"a sent card as the newest of the user's", []Item{msgItem("a", true, false), msgItem("s", false, true)}, "s", true},
	} {
		got, ok := QuickReplyTarget(Model{Items: c.items})
		if ok != c.ok || string(got.Message.ID) != c.want {
			t.Errorf("%s: %q %v, want %q %v", c.name, got.Message.ID, ok, c.want, c.ok)
		}
		if ok && got.Kind != ItemMessage {
			t.Errorf("%s: kind %v", c.name, got.Kind)
		}
	}
}

// The target of a model Build made: the user's reply in Sent and their
// own member are passed over for the newest message of someone else.
func TestQuickReplyTargetOfABuiltModel(t *testing.T) {
	own := mail("m3", 20, true)
	own.From = []api.Address{petr}
	members := []api.MessageSummary{mail("m1", 0, true), mail("m2", 10, false), own}
	m := Build(mailThread(3), members, []api.MessageSummary{reply("s1", 30)}, mailAccount, tr)
	got, ok := QuickReplyTarget(m)
	if !ok || got.Message.ID != "m2" {
		t.Fatalf("target %q %v, want m2", got.Message.ID, ok)
	}
}

func TestQuickReplyModes(t *testing.T) {
	for _, c := range []struct {
		name string
		a    capabilities.Actions
		want []QuickReplyMode
	}{
		{"none", capabilities.Actions{}, nil},
		{"mail", capabilities.Actions{Reply: true, ReplyAll: true, Forward: true, Trash: true}, []QuickReplyMode{QuickReply, QuickReplyAll, QuickForward}},
		{"reply only", capabilities.Actions{Reply: true}, []QuickReplyMode{QuickReply}},
		{"forward only", capabilities.Actions{Forward: true}, []QuickReplyMode{QuickForward}},
		{"comment", capabilities.Actions{Reply: true, Forward: true, Comment: true}, []QuickReplyMode{QuickReply}},
	} {
		if got := QuickReplyModes(c.a); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for mode, want := range map[QuickReplyMode]api.ComposeMode{
		QuickReply: api.ComposeReply, QuickReplyAll: api.ComposeReplyAll, QuickForward: api.ComposeForward,
	} {
		if got := mode.ComposeMode(); got != want {
			t.Errorf("%d: %q, want %q", mode, got, want)
		}
	}
}

func TestQuickReplyHTML(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"empty", "", ""},
		{"blank", " \n\t\n\r\n", ""},
		{"one line", "Thanks", "<p>Thanks</p>"},
		{"lines", "Hi,\nsee you", "<p>Hi,<br>see you</p>"},
		{"paragraphs", "One\n\n\n\nTwo\nthree", "<p>One</p><p>Two<br>three</p>"},
		{"blank lines around", "\n\n  \nOne\n\n", "<p>One</p>"},
		{"a line of spaces splits", "One\n   \nTwo", "<p>One</p><p>Two</p>"},
		{"CRLF and CR", "a\r\nb\rc\r\n\r\nd", "<p>a<br>b<br>c</p><p>d</p>"},
		{"escaped", `<script>alert("x")</script> & 'y'`, "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; &#39;y&#39;</p>"},
		{"markup stays text", "<p>a</p><br><img src=x onerror=y>", "<p>&lt;p&gt;a&lt;/p&gt;&lt;br&gt;&lt;img src=x onerror=y&gt;</p>"},
		{"markdown is not read", "**bold** _x_\n- item", "<p>**bold** _x_<br>- item</p>"},
		// The user's own text: bidi and invisible characters pass as typed
		// (the daemon's sanitiser and the plain-text rules see them).
		{"bidi", "a\u202Eb\u2066c\u200B", "<p>a\u202Eb\u2066c\u200B</p>"},
		{"leading spaces kept", "  indented", "<p>  indented</p>"},
	} {
		if got := QuickReplyHTML(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// Never cut, even over the daemon's cap.
	big := strings.Repeat("x", api.MaxDraftBodyBytes+10)
	if got := QuickReplyHTML(big); got != "<p>"+big+"</p>" {
		t.Errorf("1 MiB: %d bytes", len(got))
	}
	many := strings.Repeat("<\n", 1<<16)
	got := QuickReplyHTML(many)
	if want := "<p>" + strings.TrimSuffix(strings.Repeat("&lt;<br>", 1<<16), "<br>") + "</p>"; got != want {
		t.Errorf("many lines: %d bytes, want %d", len(got), len(want))
	}
}

func TestQuickReplyBlank(t *testing.T) {
	for in, want := range map[string]bool{
		"":                   true,
		" \t\r\n":            true,
		"\u00A0\u3000":       true,
		"\u200B\u200E\uFEFF": true,
		"\x00\x07":           true,
		"\u202E \u2066":      true,
		"x":                  false,
		"  .  ":              false,
		"\u200Bx":            false,
	} {
		if got := QuickReplyBlank(in); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

// The body is byte for byte what the compose window saves with "Include"
// checked for the same own HTML.
func TestQuickReplyBody(t *testing.T) {
	original := `<p><br></p><div>A wrote:</div><blockquote type="cite"><p>Hi &amp; bye</p><img src="cid:i1"></blockquote>`
	d := api.Draft{ID: "d1", HTMLBody: original, TextBody: "A wrote: Hi & bye"}
	own := "Thanks!\n\nP."
	for _, mode := range []api.ComposeMode{api.ComposeReply, api.ComposeReplyAll, api.ComposeForward} {
		got := QuickReplyBody(mode, own, d)
		if want := "<p>Thanks!</p><p>P.</p>\n" + original; got != want {
			t.Errorf("%s: %q, want %q", mode, got, want)
		}
		window := composebody.New(mode, false, composebody.DraftBody(d)).HTML(QuickReplyHTML(own), true)
		if got != window {
			t.Errorf("%s: differs from the window's %q", mode, window)
		}
	}
	// A draft without HTML quotes its text, escaped.
	text := api.Draft{TextBody: "> <x>\r\n> y"}
	if got := QuickReplyBody(api.ComposeReply, "ok", text); got != "<p>ok</p>\n&gt; &lt;x&gt;<br>&gt; y" {
		t.Errorf("text draft: %q", got)
	}
	// A comment has no original apart: the own HTML alone.
	comment := api.Draft{HTMLBody: "<p>ignored</p>", Comment: &api.DraftComment{Visibility: api.CommentPublic}}
	if got := QuickReplyBody(api.ComposeReply, "a <b>", comment); got != "<p>a &lt;b&gt;</p>" {
		t.Errorf("comment: %q", got)
	}
	// No original at all.
	if got := QuickReplyBody(api.ComposeReply, "ok", api.Draft{}); got != "<p>ok</p>" {
		t.Errorf("no body: %q", got)
	}
}

func TestQuickReplyTexts(t *testing.T) {
	if got := QuickReplyPlaceholder(false, tr); got != "Write a reply…" {
		t.Errorf("placeholder %q", got)
	}
	if got := QuickReplyPlaceholder(true, tr); got != "Write a comment…" {
		t.Errorf("comment placeholder %q", got)
	}
	if got := QuickReplyTargetTooltip(QuickReply, "Jana Nováková", tr); got != "Reply to: Jana Nováková" {
		t.Errorf("reply tooltip %q", got)
	}
	if got := QuickReplyTargetTooltip(QuickReplyAll, "Jana", tr); got != "Reply all to: Jana" {
		t.Errorf("reply-all tooltip %q", got)
	}
	// A sender with a percent sign stays as it is (Sprintf's argument).
	if got := QuickReplyTargetTooltip(QuickReply, "100%s", tr); got != "Reply to: 100%s" {
		t.Errorf("percent %q", got)
	}
	for _, c := range []struct {
		mode   QuickReplyMode
		sender string
	}{{QuickForward, "Jana"}, {QuickReply, ""}, {QuickReplyAll, ""}} {
		if got := QuickReplyTargetTooltip(c.mode, c.sender, tr); got != "" {
			t.Errorf("%d %q: %q", c.mode, c.sender, got)
		}
	}
	if got := QuickReplyOpenWindowLabel(tr); got != "Open in a Window" {
		t.Errorf("open %q", got)
	}
	if got := QuickReplyForwardLabel(tr); got != "Forward…" {
		t.Errorf("forward %q", got)
	}
}
