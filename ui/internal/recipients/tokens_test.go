// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package recipients

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func labels(t *Tokens) []string {
	out := []string{}
	for _, tok := range t.Items() {
		out = append(out, tok.Label())
	}
	return out
}

func TestNewTokens(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		labels  []string
		text    string
		invalid bool
	}{
		{"empty", "", []string{}, "", false},
		{"blank", " \t ", []string{}, "", false},
		{"plain", "a@b.cz, c@d.cz", []string{"a@b.cz", "c@d.cz"}, "a@b.cz, c@d.cz", false},
		{"semicolons", "a@b.cz; c@d.cz;", []string{"a@b.cz", "c@d.cz"}, "a@b.cz, c@d.cz", false},
		{"quoted comma", `"Doe, John" <j@x.cz>, a@b.cz`, []string{"Doe, John", "a@b.cz"}, `"Doe, John" <j@x.cz>, a@b.cz`, false},
		{"name", "Jörg Müller <j@x.cz>", []string{"Jörg Müller"}, "Jörg Müller <j@x.cz>", false},
		{"bare name is invalid", "Radek, a@b.cz", []string{"Radek", "a@b.cz"}, "Radek, a@b.cz", true},
		{"unclosed angle swallows the rest", "Radek <x@y.cz, a@b.cz", []string{"Radek <x@y.cz, a@b.cz"}, "Radek <x@y.cz, a@b.cz", true},
		{"unclosed quote swallows the rest", `"Radek <x@y.cz>, a@b.cz`, []string{`"Radek <x@y.cz>, a@b.cz`}, `"Radek <x@y.cz>, a@b.cz`, true},
		{"empty entries", ", ,a@b.cz,,", []string{"a@b.cz"}, "a@b.cz", false},
		{"controls", "a@b.cz\x00\x07, c@\x1fd.cz ", []string{"a@b.cz", "c@d.cz"}, "a@b.cz, c@d.cz", false},
		{"rtl override", "\u202eevil <a@b.cz>", []string{"evil"}, "\u202eevil <a@b.cz>", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk := NewTokens(tc.in)
			if got := labels(tk); !reflect.DeepEqual(got, tc.labels) {
				t.Errorf("labels = %q, want %q", got, tc.labels)
			}
			if got := tk.Text(); got != tc.text {
				t.Errorf("text = %q, want %q", got, tc.text)
			}
			if tk.HasInvalid() != tc.invalid {
				t.Errorf("hasInvalid = %v, want %v", tk.HasInvalid(), tc.invalid)
			}
			if tk.Pending() != "" {
				t.Errorf("pending = %q, want empty", tk.Pending())
			}
		})
	}
}

func TestTokenParts(t *testing.T) {
	tk := NewTokens(`"Doe, John" <j@x.cz>, a@b.cz, nobody`)
	items := tk.Items()
	if len(items) != 3 {
		t.Fatalf("%d tokens", len(items))
	}
	if items[0].Label() != "Doe, John" || items[0].Tooltip() != `"Doe, John" <j@x.cz>` || !items[0].Valid() {
		t.Errorf("token 0: %q %q", items[0].Label(), items[0].Tooltip())
	}
	if items[1].Label() != "a@b.cz" || items[1].Tooltip() != "a@b.cz" {
		t.Errorf("token 1: %q %q", items[1].Label(), items[1].Tooltip())
	}
	if items[2].Valid() || items[2].Label() != "nobody" || items[2].Tooltip() != "nobody" || items[2].Raw != "nobody" {
		t.Errorf("token 2: %+v", items[2])
	}
}

func TestRoundTrip(t *testing.T) {
	list := []api.Address{
		{Name: "Alice", Address: "alice@example.invalid"},
		{Address: "bob@example.invalid"},
		{Name: `Doe, Jane "JD"`, Address: "jane@example.invalid"},
		{Name: "Jörg; Müller", Address: "j@example.invalid"},
	}
	text := formatList(list)
	tk := NewTokens(text)
	if tk.Text() != text {
		t.Errorf("text = %q, want %q", tk.Text(), text)
	}
	for i, tok := range tk.Items() {
		if !tok.Valid() || *tok.Address != list[i] {
			t.Errorf("token %d = %+v, want %+v", i, tok.Address, list[i])
		}
	}
}

func formatList(list []api.Address) string {
	parts := []string{}
	for _, a := range list {
		parts = append(parts, formatAddress(a))
	}
	return strings.Join(parts, ", ")
}

func TestSetPending(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		changed bool
		labels  []string
		pending string
	}{
		{"typing", "Rad", false, []string{}, "Rad"},
		{"comma completes", "a@b.cz,", true, []string{"a@b.cz"}, ""},
		{"comma keeps the rest", "a@b.cz, c@d", true, []string{"a@b.cz"}, "c@d"},
		{"semicolon", "a@b.cz; c@d", true, []string{"a@b.cz"}, "c@d"},
		{"several", "a@b.cz, c@d.cz, e", true, []string{"a@b.cz", "c@d.cz"}, "e"},
		{"quoted comma waits", `"Doe, Jo`, false, []string{}, `"Doe, Jo`},
		{"quoted comma then address", `"Doe, John" <j@x.cz>,`, true, []string{"Doe, John"}, ""},
		{"angle comma waits", "Radek <x@y.cz, ", false, []string{}, "Radek <x@y.cz, "},
		{"bare address then space", "bohmova@satomar.cz ", true, []string{"bohmova@satomar.cz"}, ""},
		{"bare address then tab", "bohmova@satomar.cz\t", true, []string{"bohmova@satomar.cz"}, ""},
		{"bare address no space", "bohmova@satomar.cz", false, []string{}, "bohmova@satomar.cz"},
		{"name then space", "Radek ", false, []string{}, "Radek "},
		{"half typed angle", "Radek Bábíček <x@y.cz ", false, []string{}, "Radek Bábíček <x@y.cz "},
		{"closed angle then space is not bare", "Radek <x@y.cz> ", false, []string{}, "Radek <x@y.cz> "},
		{"bare angle then space is not bare", "<x@y.cz> ", false, []string{}, "<x@y.cz> "},
		{"invalid entry before comma", "foo, a", true, []string{"foo"}, "a"},
		{"separator only", ",", true, []string{}, ""},
		{"newline is a space", "a@b.cz\n", true, []string{"a@b.cz"}, ""},
		{"controls dropped", "a\x00b", false, []string{}, "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk := New()
			if got := tk.SetPending(tc.in); got != tc.changed {
				t.Errorf("changed = %v, want %v", got, tc.changed)
			}
			if got := labels(tk); !reflect.DeepEqual(got, tc.labels) {
				t.Errorf("labels = %q, want %q", got, tc.labels)
			}
			if tk.Pending() != tc.pending {
				t.Errorf("pending = %q, want %q", tk.Pending(), tc.pending)
			}
		})
	}
}

func TestSetPendingKeepsEarlierTokens(t *testing.T) {
	tk := NewTokens("a@b.cz")
	tk.SetPending("c@d.cz, e")
	if got := labels(tk); !reflect.DeepEqual(got, []string{"a@b.cz", "c@d.cz"}) || tk.Pending() != "e" {
		t.Errorf("%q %q", got, tk.Pending())
	}
	if tk.Text() != "a@b.cz, c@d.cz, e" {
		t.Errorf("text = %q", tk.Text())
	}
	// Blank pending is not part of the value.
	tk.SetPending("   ")
	if tk.Text() != "a@b.cz, c@d.cz" {
		t.Errorf("text = %q", tk.Text())
	}
}

func TestCommit(t *testing.T) {
	tk := New()
	tk.SetPending("Radek")
	if !tk.Commit() || tk.Pending() != "" || tk.Items()[0].Valid() || !tk.HasInvalid() {
		t.Errorf("invalid commit: %+v %q", tk.Items(), tk.Pending())
	}
	tk.SetPending("a@b.cz")
	if !tk.Commit() || len(tk.Items()) != 2 || !tk.Items()[1].Valid() {
		t.Errorf("valid commit: %+v", tk.Items())
	}
	tk.SetPending("   ")
	if tk.Commit() || tk.Pending() != "" || len(tk.Items()) != 2 {
		t.Errorf("blank commit: %+v %q", tk.Items(), tk.Pending())
	}
	if tk.Commit() {
		t.Error("empty commit changed")
	}
}

func TestAdd(t *testing.T) {
	tk := New()
	tk.SetPending("Ra")
	tk.Add(api.Address{Name: "Radek B.", Address: "r@x.cz"})
	tk.Add(api.Address{Address: "r@x.cz"})
	tk.Add(api.Address{Address: "r@x.cz"})
	if tk.Pending() != "" || !reflect.DeepEqual(labels(tk), []string{"Radek B.", "r@x.cz", "r@x.cz"}) {
		t.Errorf("%q %q", labels(tk), tk.Pending())
	}
	if tk.Text() != "Radek B. <r@x.cz>, r@x.cz, r@x.cz" {
		t.Errorf("text = %q", tk.Text())
	}
}

func TestRemove(t *testing.T) {
	tk := NewTokens("a@b.cz, c@d.cz, e@f.cz")
	tk.Remove(1)
	if !reflect.DeepEqual(labels(tk), []string{"a@b.cz", "e@f.cz"}) {
		t.Errorf("%q", labels(tk))
	}
	for _, i := range []int{-1, 2, 99} {
		tk.Remove(i)
		if len(tk.Items()) != 2 {
			t.Errorf("Remove(%d) changed the list", i)
		}
	}
}

func TestEdit(t *testing.T) {
	tk := NewTokens(`"Doe, John" <j@x.cz>, a@b.cz, nobody`)
	tk.SetPending("typed")
	if got := tk.Edit(0); got != `"Doe, John" <j@x.cz>` {
		t.Errorf("edit = %q", got)
	}
	// Pending was committed first, the edited token left the list.
	if !reflect.DeepEqual(labels(tk), []string{"a@b.cz", "nobody", "typed"}) || tk.Pending() != `"Doe, John" <j@x.cz>` {
		t.Errorf("%q %q", labels(tk), tk.Pending())
	}
	if got := tk.Edit(1); got != "nobody" || tk.Pending() != "nobody" {
		t.Errorf("edit invalid = %q", got)
	}
	// Out of range: nothing changes, not even the pending text.
	before := len(tk.Items())
	for _, i := range []int{-1, before, 99} {
		if got := tk.Edit(i); got != "nobody" || len(tk.Items()) != before {
			t.Errorf("Edit(%d) = %q, %d tokens", i, got, len(tk.Items()))
		}
	}
}

func TestPaste(t *testing.T) {
	tests := []struct {
		name     string
		pending  string
		in       string
		labels   []string
		pending2 string
	}{
		{"no separator is typing", "", "Rad", []string{}, "Rad"},
		{"appends to pending", "Ra", "dek", []string{}, "Radek"},
		{"bare address and space", "", "a@b.cz ", []string{"a@b.cz"}, ""},
		{"commas", "", "a@b.cz, c@d.cz", []string{"a@b.cz", "c@d.cz"}, ""},
		{"semicolons", "", "a@b.cz; c@d.cz; e@f.cz", []string{"a@b.cz", "c@d.cz", "e@f.cz"}, ""},
		{"newlines", "", "a@b.cz\nc@d.cz\r\ne@f.cz\n", []string{"a@b.cz", "c@d.cz", "e@f.cz"}, ""},
		{"tabs", "", "a@b.cz\tc@d.cz", []string{"a@b.cz", "c@d.cz"}, ""},
		{"last entry becomes a token", "", "a@b.cz, Radek", []string{"a@b.cz", "Radek"}, ""},
		{"pending joins the first entry", "x", "@y.cz, c@d.cz", []string{"x@y.cz", "c@d.cz"}, ""},
		{"quoted comma", "", `"Doe, John" <j@x.cz>; a@b.cz`, []string{"Doe, John", "a@b.cz"}, ""},
		{"quoted newline stays", "", "\"Doe,\nJohn\" <j@x.cz>\na@b.cz", []string{"Doe, John", "a@b.cz"}, ""},
		{"unclosed angle is no separator", "", "Radek <x@y.cz, a@b.cz", []string{}, "Radek <x@y.cz, a@b.cz"},
		{"whitespace only", "", " \n\t ", []string{}, ""},
		{"controls", "", "a@b.cz,\x00c@d.cz\x1b ", []string{"a@b.cz", "c@d.cz"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk := New()
			tk.SetPending(tc.pending)
			tk.Paste(tc.in)
			if got := labels(tk); !reflect.DeepEqual(got, tc.labels) {
				t.Errorf("labels = %q, want %q", got, tc.labels)
			}
			if tk.Pending() != tc.pending2 {
				t.Errorf("pending = %q, want %q", tk.Pending(), tc.pending2)
			}
		})
	}
}

func TestHostileSizes(t *testing.T) {
	// 5000 addresses: MaxTokens become tokens, the rest stays pending.
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "u%d@x.cz\n", i)
	}
	tk := New()
	tk.Paste(b.String())
	if n := len(tk.Items()); n != MaxTokens {
		t.Errorf("%d tokens, want %d", n, MaxTokens)
	}
	if tk.Items()[0].Raw != "u0@x.cz" || tk.Items()[MaxTokens-1].Raw != "u999@x.cz" {
		t.Errorf("first %q last %q", tk.Items()[0].Raw, tk.Items()[MaxTokens-1].Raw)
	}
	if !strings.HasPrefix(tk.Pending(), "u1000@x.cz, u1001@x.cz") {
		t.Errorf("pending %.40q", tk.Pending())
	}

	commaed := strings.ReplaceAll(strings.TrimSpace(b.String()), "\n", ",")
	tk2 := NewTokens(commaed)
	if len(tk2.Items()) != MaxTokens || !strings.HasPrefix(tk2.Pending(), "u1000@x.cz,u1001@x.cz") {
		t.Errorf("init: %d tokens, pending %.40q", len(tk2.Items()), tk2.Pending())
	}
	if addrs, bad := tk2.Resolved(); len(addrs) != 5000 || len(bad) != 0 {
		t.Errorf("resolved %d, %d invalid", len(addrs), len(bad))
	}
	if n := len(tk2.Items()); n != MaxTokens {
		t.Errorf("Resolved changed the model: %d tokens", n)
	}
	if got := strings.Count(tk2.Text(), "@"); got != 5000 {
		t.Errorf("text has %d addresses", got)
	}

	// Typing past the cap keeps the rest pending too.
	st := New()
	st.SetPending(commaed + ",")
	if len(st.Items()) != MaxTokens || st.Pending() == "" {
		t.Errorf("setPending: %d tokens, pending %d bytes", len(st.Items()), len(st.Pending()))
	}
	if addrs, _ := st.Resolved(); len(addrs) != 5000 {
		t.Errorf("setPending resolved %d", len(addrs))
	}
	// Commit and Add append beyond the cap.
	if !st.Commit() || len(st.Items()) != 5000 || st.Pending() != "" {
		t.Errorf("commit: %d tokens", len(st.Items()))
	}
	st.Add(api.Address{Address: "z@x.cz"})
	if len(st.Items()) != 5001 {
		t.Errorf("add: %d tokens", len(st.Items()))
	}

	// 1 MB without a separator: paste is cut at 64 KiB.
	big := New()
	big.Paste(strings.Repeat("a", 1<<20))
	if len(big.Pending()) != MaxInput || len(big.Items()) != 0 {
		t.Errorf("pending %d bytes, %d tokens", len(big.Pending()), len(big.Items()))
	}
	// Multi-byte paste is cut on a rune boundary.
	multi := New()
	multi.Paste(strings.Repeat("é", 1<<20))
	if len(multi.Pending()) != MaxInput || strings.ContainsRune(multi.Pending(), '�') {
		t.Errorf("pending %d bytes", len(multi.Pending()))
	}
	// Typed and initial text are never clipped.
	typed := New()
	typed.SetPending(strings.Repeat("é", 1<<20))
	if len(typed.Pending()) != 2<<20 {
		t.Errorf("typed pending %d bytes", len(typed.Pending()))
	}
	if got := NewTokens(strings.Repeat("a", 1<<20)).Items()[0].Raw; len(got) != 1<<20 {
		t.Errorf("init raw %d bytes", len(got))
	}
	// 1 MB of addresses pasted: input beyond 64 KiB is ignored.
	long := New()
	long.Paste(strings.Repeat("a@b.cz,", 1<<17))
	if n := len(long.Items()); n < 1 || n > MaxTokens {
		t.Errorf("%d tokens", n)
	}
}

func TestResolved(t *testing.T) {
	// A name with a colon is a valid token, but the formatted text does not
	// read back; Resolved does not go through the text.
	tk := NewTokens(`"ACME: Support" <x@y.cz>, nobody`)
	addrs, bad := tk.Resolved()
	if len(addrs) != 1 || addrs[0] != (api.Address{Name: "ACME: Support", Address: "x@y.cz"}) || !reflect.DeepEqual(bad, []string{"nobody"}) {
		t.Errorf("%+v %q", addrs, bad)
	}

	// Two invalid tokens whose joined text would read as one address.
	tk = New()
	tk.SetPending(`"Joe`)
	tk.Commit()
	tk.SetPending(`a" <b@c.cz>`)
	tk.Commit()
	addrs, bad = tk.Resolved()
	if len(addrs) != 0 || !reflect.DeepEqual(bad, []string{`"Joe`, `a" <b@c.cz>`}) {
		t.Errorf("%+v %q", addrs, bad)
	}

	// Pending is evaluated like Commit, without changing the model.
	tk = NewTokens("a@b.cz")
	tk.SetPending("c@d.cz")
	addrs, bad = tk.Resolved()
	if len(addrs) != 2 || addrs[1].Address != "c@d.cz" || len(bad) != 0 {
		t.Errorf("%+v %q", addrs, bad)
	}
	if tk.Pending() != "c@d.cz" || len(tk.Items()) != 1 {
		t.Errorf("model changed: %q %d", tk.Pending(), len(tk.Items()))
	}
	// Blank pending adds nothing.
	tk.SetPending("  ")
	if addrs, bad = tk.Resolved(); len(addrs) != 1 || len(bad) != 0 {
		t.Errorf("%+v %q", addrs, bad)
	}
	// Pending with separators (left by the token cap) gives several entries.
	tk = New()
	tk.pending = "a@b.cz, junk, c@d.cz"
	addrs, bad = tk.Resolved()
	if len(addrs) != 2 || !reflect.DeepEqual(bad, []string{"junk"}) {
		t.Errorf("%+v %q", addrs, bad)
	}
}

func TestBidiStripped(t *testing.T) {
	bidi := string([]rune{0x202a, 0x202e, 0x2066, 0x2069, 0x200e, 0x200f, 0x061c})
	tk := NewTokens(bidi + "Eve" + bidi + " <e@x.cz>, " + bidi + "junk")
	items := tk.Items()
	if items[0].Label() != "Eve" || items[0].Tooltip() != "Eve <e@x.cz>" || items[1].Label() != "junk" || items[1].Tooltip() != "junk" {
		t.Errorf("%q %q %q %q", items[0].Label(), items[0].Tooltip(), items[1].Label(), items[1].Tooltip())
	}
	if items[0].Address.Name == "Eve" || items[1].Raw == "junk" {
		t.Error("raw and address must keep the original text")
	}
}
