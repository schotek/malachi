// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package bulkmail

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// plain returns the msgid (the singular for n == 1).
type plain struct{}

func (plain) T(msgid string) string { return msgid }
func (plain) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (plain) C(_, msgid string) string { return msgid }

var tr plain

func date(t time.Time) string { return t.UTC().Format("2006-01-02") }

func msg(b *api.BulkInfo, o *api.UnsubscribeOffer) *api.Message {
	m := &api.Message{Unsubscribe: o}
	m.Bulk = b
	return m
}

func TestTag(t *testing.T) {
	for _, c := range []struct {
		b    *api.BulkInfo
		want string
	}{
		{nil, ""},
		{&api.BulkInfo{Kind: api.BulkNewsletter}, "Bulk"},
		{&api.BulkInfo{Kind: api.BulkList}, "Mailing List"},
		{&api.BulkInfo{Kind: api.BulkAutomated}, "Automated"},
		{&api.BulkInfo{Kind: "weird"}, ""},
		{&api.BulkInfo{}, ""},
	} {
		if got := Tag(c.b, tr); got != c.want {
			t.Errorf("Tag(%v) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestStripFor(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	news := &api.BulkInfo{Kind: api.BulkNewsletter, Domain: "shop.example", ListID: "news.shop.example"}
	list := &api.BulkInfo{Kind: api.BulkList, ListID: "golang-nuts.example", Domain: "example.org"}
	listNoID := &api.BulkInfo{Kind: api.BulkList, Domain: "example.org"}
	auto := &api.BulkInfo{Kind: api.BulkAutomated, Domain: "bank.example"}
	one := &api.UnsubscribeOffer{Method: api.UnsubscribeOneClick, Target: "shop.example"}
	mailto := &api.UnsubscribeOffer{Method: api.UnsubscribeMailto, Target: "u@shop.example"}
	page := &api.UnsubscribeOffer{Method: api.UnsubscribeURL, Target: "shop.example", URL: "https://shop.example/u"}
	done := &api.UnsubscribeOffer{Method: api.UnsubscribeOneClick, Target: "shop.example", UnsubscribedAt: &at}
	cases := []struct {
		name string
		m    *api.Message
		role api.FolderRole
		want Strip
	}{
		{"nil message", nil, api.RoleInbox, Strip{}},
		{"nil bulk", msg(nil, one), api.RoleInbox, Strip{}},
		{"unknown kind", msg(&api.BulkInfo{Kind: "x"}, one), api.RoleInbox, Strip{}},
		{"junk newsletter", msg(news, one), api.RoleJunk,
			Strip{Kind: StripJunk, Text: "Unsubscribing would confirm to the sender that your address exists.", Warning: true}},
		{"junk list", msg(list, nil), api.RoleJunk,
			Strip{Kind: StripJunk, Text: "Unsubscribing would confirm to the sender that your address exists.", Warning: true}},
		{"junk role string", msg(news, one), "junk", Strip{Kind: StripJunk, Text: "Unsubscribing would confirm to the sender that your address exists.", Warning: true}},
		{"junk automated", msg(auto, nil), api.RoleJunk, Strip{Kind: StripAutomated, Text: "Automated message"}},
		{"automated", msg(auto, one), api.RoleInbox, Strip{Kind: StripAutomated, Text: "Automated message"}},
		{"junk-like role", msg(news, one), api.FolderRole("junkish"), Strip{Kind: StripNewsletter, Text: "Bulk message from shop.example", Action: "_Unsubscribe"}},
		{"unsubscribed", msg(news, done), api.RoleInbox, Strip{Kind: StripUnsubscribed, Text: "Unsubscribed on 2026-09-30"}},
		{"unsubscribed list", msg(list, done), api.RoleInbox, Strip{Kind: StripUnsubscribed, Text: "Unsubscribed on 2026-09-30"}},
		{"newsletter one click", msg(news, one), api.RoleInbox, Strip{Kind: StripNewsletter, Text: "Bulk message from shop.example", Action: "_Unsubscribe"}},
		{"newsletter mailto", msg(news, mailto), api.RoleInbox, Strip{Kind: StripNewsletter, Text: "Bulk message from shop.example", Action: "_Unsubscribe"}},
		{"newsletter url", msg(news, page), api.RoleInbox, Strip{Kind: StripNewsletter, Text: "Bulk message from shop.example", Action: "_Unsubscribe…"}},
		{"newsletter no offer", msg(news, nil), api.RoleInbox, Strip{Kind: StripNewsletter, Text: "Bulk message from shop.example"}},
		{"newsletter no domain", msg(&api.BulkInfo{Kind: api.BulkNewsletter, ListID: "l.example"}, nil), api.RoleInbox,
			Strip{Kind: StripNewsletter, Text: "Bulk message from l.example"}},
		{"list mailto", msg(list, mailto), api.RoleInbox, Strip{Kind: StripList, Text: "Message from mailing list golang-nuts.example", Action: "_Leave List"}},
		{"list url", msg(list, page), api.RoleInbox, Strip{Kind: StripList, Text: "Message from mailing list golang-nuts.example", Action: "_Leave List…"}},
		{"list without list id falls back to domain", msg(listNoID, one), api.RoleInbox,
			Strip{Kind: StripList, Text: "Message from mailing list example.org", Action: "_Leave List"}},
		{"list no offer", msg(list, nil), api.RoleInbox, Strip{Kind: StripList, Text: "Message from mailing list golang-nuts.example"}},
	}
	for _, c := range cases {
		got := StripFor(c.m, c.role, date, tr)
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
		if got.Visible() != (c.want.Kind != StripNone) {
			t.Errorf("%s: Visible = %v", c.name, got.Visible())
		}
	}
	// A nil date formatter must not panic.
	StripFor(msg(news, done), api.RoleInbox, nil, tr)
}

func TestConfirm(t *testing.T) {
	news := &api.BulkInfo{Kind: api.BulkNewsletter, Domain: "shop.example"}
	list := &api.BulkInfo{Kind: api.BulkList, ListID: "l.example", Domain: "example.org"}
	one := &api.UnsubscribeOffer{Method: api.UnsubscribeOneClick, Target: "shop.example"}
	mailto := &api.UnsubscribeOffer{Method: api.UnsubscribeMailto, Target: "u@shop.example"}
	page := &api.UnsubscribeOffer{Method: api.UnsubscribeURL, Target: "shop.example", URL: "https://shop.example/u?x=1"}
	for _, c := range []struct {
		name string
		m    *api.Message
		want Confirmation
		ok   bool
	}{
		{"nil", nil, Confirmation{}, false},
		{"no offer", msg(news, nil), Confirmation{}, false},
		{"unknown method", msg(news, &api.UnsubscribeOffer{Method: "x"}), Confirmation{}, false},
		{"one click newsletter", msg(news, one), Confirmation{
			Heading: "Unsubscribe from shop.example?",
			Body:    "Malachi Mail will ask shop.example to stop sending these messages. The sender may still send a few more over the next days.",
			Confirm: "_Unsubscribe"}, true},
		{"one click list", msg(list, one), Confirmation{
			Heading: "Unsubscribe from l.example?",
			Body:    "Malachi Mail will ask shop.example to stop sending these messages. The sender may still send a few more over the next days.",
			Confirm: "_Unsubscribe"}, true},
		{"one click nil bulk", msg(nil, one), Confirmation{
			Heading: "Unsubscribe from shop.example?",
			Body:    "Malachi Mail will ask shop.example to stop sending these messages. The sender may still send a few more over the next days.",
			Confirm: "_Unsubscribe"}, true},
		{"mailto newsletter", msg(news, mailto), Confirmation{
			Heading: "Unsubscribe from shop.example?",
			Body:    "Malachi Mail will send an unsubscribe request to u@shop.example from your account. It will appear in Sent.",
			Confirm: "_Send Request"}, true},
		{"mailto list", msg(list, mailto), Confirmation{
			Heading: "Leave the mailing list l.example?",
			Body:    "Malachi Mail will send an unsubscribe request to u@shop.example from your account. It will appear in Sent.",
			Confirm: "_Send Request"}, true},
		{"url", msg(news, page), Confirmation{
			Heading: "Open the unsubscribe page?",
			Body:    "The sender does not offer unsubscribing in one step. This page opens in your browser:\nhttps://shop.example/u?x=1",
			Confirm: "_Open in Browser"}, true},
	} {
		got, ok := Confirm(c.m, tr)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestFallback(t *testing.T) {
	m := msg(&api.BulkInfo{Kind: api.BulkNewsletter, Domain: "shop.example"},
		&api.UnsubscribeOffer{Method: api.UnsubscribeOneClick, Target: "t.example"})
	res := api.MessageUnsubscribeResult{Outcome: api.UnsubscribeOpenURL, URL: "https://shop.example/u", Unverified: true}
	want := Confirmation{
		Heading: "The sender could not be verified",
		Body:    "Malachi Mail sent nothing because the message is not signed by shop.example. You can unsubscribe on the sender's page instead:\nhttps://shop.example/u",
		Confirm: "_Open in Browser",
	}
	if got := Fallback(m, res, tr); got != want {
		t.Errorf("got %+v", got)
	}
	// Without a domain the offer's target stands in; nil message is safe.
	m.Bulk = nil
	if got := Fallback(m, res, tr); !strings.Contains(got.Body, "signed by t.example.") {
		t.Errorf("target fallback: %q", got.Body)
	}
	if got := Fallback(nil, res, tr); got.Heading == "" {
		t.Error("nil message: no heading")
	}
}

func TestApplied(t *testing.T) {
	at := time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)
	offer := &api.UnsubscribeOffer{Method: api.UnsubscribeMailto, Target: "u@x.example"}
	if Applied(nil, api.MessageUnsubscribeResult{Outcome: api.UnsubscribeDone}) != nil {
		t.Error("nil offer must stay nil")
	}
	for _, o := range []api.UnsubscribeOutcome{api.UnsubscribeDone, api.UnsubscribeQueued} {
		got := Applied(offer, api.MessageUnsubscribeResult{Outcome: o, UnsubscribedAt: &at})
		if got == offer || got.UnsubscribedAt == nil || !got.UnsubscribedAt.Equal(at) {
			t.Errorf("%s: %+v", o, got)
		}
		if got.Method != offer.Method || got.Target != offer.Target {
			t.Errorf("%s: fields lost: %+v", o, got)
		}
		if offer.UnsubscribedAt != nil {
			t.Fatal("the original offer was modified")
		}
	}
	// A result without a time still marks the offer as done.
	if got := Applied(offer, api.MessageUnsubscribeResult{Outcome: api.UnsubscribeQueued}); got.UnsubscribedAt == nil {
		t.Error("no time: not marked")
	}
	if got := Applied(offer, api.MessageUnsubscribeResult{Outcome: api.UnsubscribeOpenURL, UnsubscribedAt: &at}); got.UnsubscribedAt != nil {
		t.Error("openUrl must not mark the offer")
	}
}

func TestTexts(t *testing.T) {
	if Queued(tr) != "Unsubscribe request queued" || ErrorWhat(tr) != "Unsubscribing" ||
		Refused(tr) != "The sender's server refused the request." {
		t.Error("texts changed")
	}
}

func TestOpenableURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://shop.example/u?x=1":           true,
		"HTTPS://shop.example/":                true,
		"http://shop.example/":                 false,
		"javascript:alert(1)":                  false,
		"data:text/html,x":                     false,
		"https:///path":                        false,
		"/relative":                            false,
		"":                                     false,
		"https://shop.example/a b":             false,
		"https://shop.example/\x00":            false,
		"https://shop.example/\u202e":          false,
		"https://" + strings.Repeat("a", 3000): false,
	} {
		if _, got := OpenableURL(raw); got != ok {
			t.Errorf("OpenableURL(%.40q) = %v, want %v", raw, got, ok)
		}
	}
}

// Hostile strings are shown as plain text by the view; here they must only
// pass through without a panic or a rewrite.
func TestHostileStrings(t *testing.T) {
	huge := strings.Repeat("a", 1<<20)
	bidi := "\u202eevil\u2066.example\x00%s%d<b>"
	for _, s := range []string{huge, bidi, "", "%", "%!s(MISSING)"} {
		b := &api.BulkInfo{Kind: api.BulkList, ListID: s, Domain: s}
		o := &api.UnsubscribeOffer{Method: api.UnsubscribeURL, Target: s, URL: s}
		m := msg(b, o)
		st := StripFor(m, api.RoleInbox, date, tr)
		if !strings.Contains(st.Text, s) && s != "" {
			t.Errorf("list id was rewritten for %.20q", s)
		}
		Confirm(m, tr)
		Fallback(m, api.MessageUnsubscribeResult{URL: s}, tr)
		Tag(b, tr)
	}
}
