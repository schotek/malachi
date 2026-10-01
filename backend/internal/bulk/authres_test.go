// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package bulk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/mime"
)

const (
	exchangeAR = "spf=pass (sender IP is 192.0.2.10) smtp.mailfrom=bounce.esp.test; dkim=pass (signature was verified) header.d=news.example;dmarc=pass action=none header.from=news.example;compauth=pass reason=100"
	goodSig    = "v=1; a=rsa-sha256; d=news.example; s=s1; h=From:To:List-Unsubscribe:List-Unsubscribe-Post; bh=AA; b=AA"
)

func TestParseAuthenticationResults(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want []AuthResult
	}{
		{"exchange field without authserv-id", exchangeAR, true, []AuthResult{
			{"spf", "pass", ""}, {"dkim", "pass", "news.example"}, {"dmarc", "pass", ""}, {"compauth", "pass", ""}}},
		{"with authserv-id and version", "mx.example.com 1; dkim=pass header.d=A.Example", true, []AuthResult{{"dkim", "pass", "a.example"}}},
		{"none", "mx.example.com; none", true, nil},
		{"folded and odd whitespace", "dkim=pass\r\n\t header.d=news.example  \r\n ;  spf=fail", true, []AuthResult{{"dkim", "pass", "news.example"}, {"spf", "fail", ""}}},
		{"quoted header.d", `dkim=pass header.d="news.example"`, true, []AuthResult{{"dkim", "pass", "news.example"}}},
		{"nested comments", "dkim=pass (a (b (c)) d) header.d=news.example", true, []AuthResult{{"dkim", "pass", "news.example"}}},
		{"comment hides a fake result", "dkim=fail (dkim=pass header.d=news.example)", true, []AuthResult{{"dkim", "fail", ""}}},
		{"escaped paren in comment", `dkim=pass (a \) b) header.d=news.example`, true, []AuthResult{{"dkim", "pass", "news.example"}}},
		{"unbalanced open", "dkim=pass (header.d=news.example", false, nil},
		{"unbalanced close", "dkim=pass) header.d=news.example", false, nil},
		{"too deep", "dkim=pass " + strings.Repeat("(", 20) + strings.Repeat(")", 20), false, nil},
		{"repeated header.d", "dkim=pass header.d=a.example header.d=b.example", false, nil},
		{"header.d with bidi", "dkim=pass header.d=news" + string(rune(0x202E)) + ".example", false, nil},
		{"empty method", "=pass", false, nil},
		{"junk segment", "dkim=pass header.d=a.example; garbage", false, nil},
		{"too many results", strings.Repeat("spf=pass; ", 40), false, nil},
		{"huge", "dkim=pass header.d=" + strings.Repeat("a", maxAuthBytes), false, nil},
		{"empty", "", true, nil},
		{"invalid utf-8 in header.d", "dkim=pass header.d=a\xff.example", false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseAuthenticationResults(c.in)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (%v)", ok, c.ok, got)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("result %d = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestParseDKIMSignature(t *testing.T) {
	good, ok := ParseDKIMSignature("v=1; d=News.Example.; h = From : List-Unsubscribe :\r\n List-Unsubscribe-Post ;s=x")
	if !ok || good.Domain != "news.example" || len(good.Headers) != 3 || !good.Signs("LIST-UNSUBSCRIBE-POST") || good.Signs("subject") {
		t.Fatalf("%+v %v", good, ok)
	}
	for name, in := range map[string]string{
		"no d":            "v=1; h=from",
		"no h":            "v=1; d=a.example",
		"empty h":         "v=1; d=a.example; h=::",
		"repeated d":      "d=a.example; d=b.example; h=from",
		"repeated h":      "d=a.example; h=from; h=list-unsubscribe",
		"garbage tag":     "d=a.example; h=from; junk",
		"no equals":       "hello",
		"bidi in d":       "d=a" + string(rune(0x202E)) + ".example; h=from",
		"huge":            "d=a.example; h=from; b=" + strings.Repeat("A", maxAuthBytes),
		"empty":           "",
		"only semicolons": ";;;",
	} {
		if sig, ok := ParseDKIMSignature(in); ok {
			t.Errorf("%s: accepted %+v", name, sig)
		}
	}
}

func TestExchangeVerified(t *testing.T) {
	other := "v=1; d=other.example; s=s1; h=From:List-Unsubscribe:List-Unsubscribe-Post; bh=AA; b=AA"
	noPost := "v=1; d=news.example; s=s1; h=From:List-Unsubscribe; bh=AA; b=AA"
	noURL := "v=1; d=news.example; s=s1; h=From:List-Unsubscribe-Post; bh=AA; b=AA"
	noFrom := "v=1; d=news.example; s=s1; h=To:List-Unsubscribe:List-Unsubscribe-Post; bh=AA; b=AA"
	subSig := "v=1; d=mail.news.example; s=s1; h=From:List-Unsubscribe:List-Unsubscribe-Post; bh=AA; b=AA"
	cases := []struct {
		name string
		ar   []string
		sigs []string
		from string
		want bool
	}{
		{"the Exchange case", []string{exchangeAR}, []string{goodSig}, "news.example", true},
		{"signature case and order do not matter", []string{exchangeAR}, []string{other, strings.ToUpper(goodSig)}, "news.example", true},
		{"from a subdomain of the signer", []string{exchangeAR}, []string{goodSig}, "mail.news.example", true},
		{"signature of a subdomain, same d as the result", []string{"dkim=pass header.d=mail.news.example"}, []string{subSig}, "news.example", true},
		{"only the topmost field counts", []string{"dkim=fail header.d=news.example", exchangeAR}, []string{goodSig}, "news.example", false},
		{"a forged field below does not help", []string{"spf=pass", "dkim=pass header.d=news.example"}, []string{goodSig}, "news.example", false},
		{"no field", nil, []string{goodSig}, "news.example", false},
		{"dkim=fail", []string{"dkim=fail header.d=news.example"}, []string{goodSig}, "news.example", false},
		{"dkim=none", []string{"dkim=none"}, []string{goodSig}, "news.example", false},
		{"dkim=neutral", []string{"dkim=neutral header.d=news.example"}, []string{goodSig}, "news.example", false},
		{"pass of another method", []string{"spf=pass header.d=news.example"}, []string{goodSig}, "news.example", false},
		{"missing header.d", []string{"dkim=pass"}, []string{goodSig}, "news.example", false},
		{"header.d of another organisation", []string{"dkim=pass header.d=other.example"}, []string{goodSig, other}, "news.example", false},
		{"from of another organisation", []string{exchangeAR}, []string{goodSig}, "victim.example", false},
		{"empty from", []string{exchangeAR}, []string{goodSig}, "", false},
		{"signature of another d", []string{exchangeAR}, []string{other}, "news.example", false},
		{"signature without the post", []string{exchangeAR}, []string{noPost}, "news.example", false},
		{"signature without the url", []string{exchangeAR}, []string{noURL}, "news.example", false},
		{"signature without from", []string{exchangeAR}, []string{noFrom}, "news.example", false},
		{"no signature", []string{exchangeAR}, nil, "news.example", false},
		{"unreadable signature", []string{exchangeAR}, []string{"d=news.example"}, "news.example", false},
		{"unreadable field", []string{"dkim=pass (header.d=news.example"}, []string{goodSig}, "news.example", false},
		{"pass hidden in a comment", []string{"dkim=fail (dkim=pass header.d=news.example)"}, []string{goodSig}, "news.example", false},
		{"huge field", []string{exchangeAR + strings.Repeat(" ", maxAuthBytes)}, []string{goodSig}, "news.example", false},
		{"public suffix as header.d", []string{"dkim=pass header.d=co.uk"}, []string{"d=co.uk; h=from:list-unsubscribe:list-unsubscribe-post"}, "a.co.uk", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExchangeVerified(c.ar, c.sigs, c.from); got != c.want {
				t.Errorf("= %v, want %v", got, c.want)
			}
		})
	}
	// Many signatures: only the first few are read.
	many := make([]string, 100)
	for i := range many {
		many[i] = other
	}
	many = append(many, goodSig)
	if ExchangeVerified([]string{exchangeAR}, many, "news.example") {
		t.Error("a signature past the cap counted")
	}
}

func TestExchangeVerifiedFixture(t *testing.T) {
	f, err := os.Open(filepath.Join(testdata, "bulk-graph-exchange.eml"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	v := mime.HeaderValues(f, mime.DefaultLimits(), "Authentication-Results", "DKIM-Signature")
	if len(v["Authentication-Results"]) != 2 || len(v["DKIM-Signature"]) != 1 {
		t.Fatalf("%v", v)
	}
	if !ExchangeVerified(v["Authentication-Results"], v["DKIM-Signature"], "news.example") {
		t.Error("the Exchange fixture is not verified")
	}
	// The forged field below Exchange's is not the one read.
	swapped := []string{v["Authentication-Results"][1], v["Authentication-Results"][0]}
	if ExchangeVerified(swapped, v["DKIM-Signature"], "news.example") {
		t.Error("a field that is not the topmost counted")
	}
}
