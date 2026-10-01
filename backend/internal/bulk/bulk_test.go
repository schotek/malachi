// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package bulk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testdata = "../../testdata/mime"

func fixtureHeaders(t *testing.T, name string) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, h := mime.ParseHeaderFields(f, mime.DefaultLimits())
	return h
}

func TestClassify(t *testing.T) {
	bidi := string(rune(0x202E))
	zw := string(rune(0x200B))
	cases := []struct {
		name string
		h    map[string]string
		want Result
	}{
		{"nothing", nil, Result{}},
		{"unsubscribe https", map[string]string{"List-Unsubscribe": "<https://a.example/u>"}, Result{Kind: api.BulkNewsletter}},
		{"unsubscribe mailto", map[string]string{"List-Unsubscribe": "<mailto:u@a.example>"}, Result{Kind: api.BulkNewsletter}},
		{"unsubscribe http only is not usable", map[string]string{"List-Unsubscribe": "<http://a.example/u>"}, Result{}},
		{"unsubscribe javascript", map[string]string{"List-Unsubscribe": "<javascript:alert(1)>"}, Result{}},
		{"unsubscribe data", map[string]string{"List-Unsubscribe": "<data:text/html,x>"}, Result{}},
		{"unsubscribe no brackets", map[string]string{"List-Unsubscribe": "https://a.example/u"}, Result{}},
		{"list", map[string]string{"List-Id": "Go <go.example.org>", "List-Post": "<mailto:go@example.org>"},
			Result{Kind: api.BulkList, ListID: "go.example.org"}},
		{"list beats unsubscribe", map[string]string{"List-Id": "<go.example.org>", "List-Post": "<mailto:go@example.org>", "List-Unsubscribe": "<https://a.example/u>"},
			Result{Kind: api.BulkList, ListID: "go.example.org"}},
		{"list post NO", map[string]string{"List-Id": "<a.example.org>", "List-Post": "NO"}, Result{}},
		{"list post NO with unsubscribe", map[string]string{"List-Id": "<a.example.org>", "List-Post": "NO", "List-Unsubscribe": "<https://a.example/u>"},
			Result{Kind: api.BulkNewsletter, ListID: "a.example.org"}},
		{"list post https is not a post address", map[string]string{"List-Id": "<a.example.org>", "List-Post": "<https://a.example/post>"}, Result{}},
		{"list post without list id", map[string]string{"List-Post": "<mailto:go@example.org>"}, Result{}},
		{"bulk with list id", map[string]string{"List-Id": "<a.example.org>", "Precedence": "bulk"}, Result{Kind: api.BulkNewsletter, ListID: "a.example.org"}},
		{"bulk without list id", map[string]string{"Precedence": "Bulk"}, Result{Kind: api.BulkAutomated}},
		{"precedence junk", map[string]string{"Precedence": " junk "}, Result{Kind: api.BulkAutomated}},
		{"precedence list", map[string]string{"Precedence": "list"}, Result{Kind: api.BulkAutomated}},
		{"precedence first", map[string]string{"Precedence": "first-class"}, Result{}},
		{"auto-submitted", map[string]string{"Auto-Submitted": "auto-generated; type=receipt"}, Result{Kind: api.BulkAutomated}},
		{"auto-submitted no", map[string]string{"Auto-Submitted": "no"}, Result{}},
		{"sendgrid fingerprint", map[string]string{"X-SG-EID": "u001.abc", "Return-Path": "bounces@sg.example.com"}, Result{Kind: api.BulkAutomated}},
		{"feedback id lower case", map[string]string{"feedback-id": "1:2:3:ses"}, Result{Kind: api.BulkAutomated}},
		{"empty fingerprint", map[string]string{"X-SG-EID": "  "}, Result{}},
		{"fingerprint with unsubscribe stays newsletter", map[string]string{"X-SG-EID": "x", "List-Unsubscribe": "<https://a.example/u>"},
			Result{Kind: api.BulkNewsletter}},
		{"fingerprint with list stays list", map[string]string{"Feedback-ID": "x", "List-Id": "<go.example.org>", "List-Post": "<mailto:go@example.org>"},
			Result{Kind: api.BulkList, ListID: "go.example.org"}},
		{"auto-submitted NO param", map[string]string{"Auto-Submitted": "No ; x=y"}, Result{}},
		{"auto-submitted empty", map[string]string{"Auto-Submitted": ""}, Result{}},
		{"case-insensitive keys", map[string]string{"list-unsubscribe": "<mailto:u@a.example>"}, Result{Kind: api.BulkNewsletter}},
		{"list id uppercase", map[string]string{"List-Id": "<Go.Example.ORG>", "List-Unsubscribe": "<mailto:u@a.example>"},
			Result{Kind: api.BulkNewsletter, ListID: "go.example.org"}},
		{"list id with bidi is dropped", map[string]string{"List-Id": "<" + bidi + "evil.example>", "List-Unsubscribe": "<mailto:u@a.example>"},
			Result{Kind: api.BulkNewsletter}},
		{"list id with zero width is dropped", map[string]string{"List-Id": "<a" + zw + "b.example>", "List-Post": "<mailto:x@a.example>"},
			Result{Kind: api.BulkList}},
		{"unclosed list id", map[string]string{"List-Id": "Broken <a.example", "List-Unsubscribe": "<mailto:u@a.example>"},
			Result{Kind: api.BulkNewsletter}},
		{"bare list id", map[string]string{"List-Id": "a.example.org", "List-Post": "<mailto:x@a.example>"},
			Result{Kind: api.BulkList, ListID: "a.example.org"}},
		{"whitespace values", map[string]string{"List-Id": "  ", "List-Unsubscribe": " ", "Precedence": " ", "Auto-Submitted": " "}, Result{}},
		{"empty values", map[string]string{"List-Id": "", "List-Unsubscribe": ""}, Result{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.h); got != c.want {
				t.Errorf("Classify = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestListIDBounded(t *testing.T) {
	long := "<" + strings.Repeat("é", 400) + ".example>"
	id := ListID(long)
	if len(id) > MaxListIDBytes || id == "" {
		t.Fatalf("len %d", len(id))
	}
	if !strings.HasPrefix(long[1:], id) {
		t.Errorf("cut inside a character")
	}
	if got := ListID(strings.Repeat("<", 100000)); got != "" {
		t.Errorf("many brackets = %q", got)
	}
	if got := ListID("<a.example><b.example>"); got != "a.example" {
		t.Errorf("first id = %q", got)
	}
}

func TestURIs(t *testing.T) {
	bidi := string(rune(0x202E))
	cases := []struct {
		name, in string
		want     []string
	}{
		{"empty", "", nil},
		{"https and mailto", "<mailto:u@a.example?subject=x>, <https://a.example/u?id=1>",
			[]string{"mailto:u@a.example?subject=x", "https://a.example/u?id=1"}},
		{"uppercase scheme and host", "<HTTPS://News.Example.COM/U>", []string{"HTTPS://News.Example.COM/U"}},
		{"http ignored", "<http://a.example/u>", nil},
		{"javascript ignored", "<javascript:alert(1)>, <JavaScript:x>", nil},
		{"data ignored", "<data:text/html;base64,AAAA>", nil},
		{"https without host", "<https://>, <https:///path>, <https:>", nil},
		{"credentials ignored", "<https://user:pw@a.example/>", nil},
		{"unicode host ignored", "<https://bücher.example/>", nil},
		{"punycode host kept", "<https://xn--bcher-kva.example/>", []string{"https://xn--bcher-kva.example/"}},
		{"bidi ignored", "<https://a" + bidi + ".example/>", nil},
		{"space inside ignored", "<https://a.example/x y>", nil},
		{"control inside ignored", "<https://a.example/x\x00y>", nil},
		{"unclosed ignored", "<https://a.example/u", nil},
		{"unclosed then valid", "<https://a.example/x <mailto:u@a.example>", []string{"mailto:u@a.example"}},
		{"double brackets", "<<mailto:u@a.example>>", []string{"mailto:u@a.example"}},
		{"mailto without address", "<mailto:>, <mailto:?subject=x>, <mailto:not an address>", nil},
		{"mailto two addresses first wins", "<mailto:a@x.example,b@y.example>", []string{"mailto:a@x.example,b@y.example"}},
		{"mailto percent encoded", "<mailto:u%40a.example>", []string{"mailto:u%40a.example"}},
		{"mailto bad percent", "<mailto:u%zz@a.example>", nil},
		{"no scheme", "<example.com/u>", nil},
		{"text around", "see <https://a.example/u> now", []string{"https://a.example/u"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, u := range URIs(c.in) {
				got = append(got, u.Raw)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("URIs = %q, want %q", got, c.want)
			}
		})
	}
}

func TestURITargets(t *testing.T) {
	us := URIs("<HTTPS://News.Example.COM:8443/u>, <mailto:u%40a.example?subject=x>")
	if len(us) != 2 || us[0].Target != "news.example.com" || us[1].Target != "u@a.example" {
		t.Fatalf("%+v", us)
	}
}

func TestURILimits(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("<javascript:x>")
	}
	b.WriteString("<https://a.example/late>")
	if got := URIs(b.String()); len(got) != 0 {
		t.Errorf("a URI past the eighth item was kept: %+v", got)
	}
	var c strings.Builder
	for i := 0; i < 8; i++ {
		c.WriteString("<https://a.example/" + strings.Repeat("x", i) + ">")
	}
	c.WriteString("<https://a.example/ninth>")
	if got := URIs(c.String()); len(got) != MaxURIs {
		t.Errorf("kept %d, want %d", len(got), MaxURIs)
	}
	ok := "<https://a.example/" + strings.Repeat("a", MaxURIBytes-len("https://a.example/")) + ">"
	if len(URIs(ok)) != 1 {
		t.Error("a URI of exactly the limit is refused")
	}
	tooLong := "<https://a.example/" + strings.Repeat("a", MaxURIBytes) + ">"
	if len(URIs(tooLong)) != 0 {
		t.Error("a URI over the limit is kept")
	}
	huge := strings.Repeat("<", 1<<20) + "<https://a.example/>"
	if got := URIs(huge); len(got) != 0 {
		t.Errorf("huge value: %+v", got)
	}
	if got := URIs(strings.Repeat(" ", 1<<20) + "<https://a.example/>"); len(got) != 0 {
		t.Errorf("value past the scan limit was read: %+v", got)
	}
}

func TestChoose(t *testing.T) {
	both := map[string]string{
		"List-Unsubscribe":      "<https://a.example/u>, <mailto:u@b.example>",
		"List-Unsubscribe-Post": " list-unsubscribe=ONE-CLICK ",
	}
	cases := []struct {
		name   string
		h      map[string]string
		kind   api.BulkKind
		method api.UnsubscribeMethod
		target string
	}{
		{"newsletter one-click wins over mailto", both, api.BulkNewsletter, api.UnsubscribeOneClick, "a.example"},
		{"list prefers mailto", both, api.BulkList, api.UnsubscribeMailto, "u@b.example"},
		{"newsletter without post header: mailto", map[string]string{"List-Unsubscribe": both["List-Unsubscribe"]}, api.BulkNewsletter, api.UnsubscribeMailto, "u@b.example"},
		{"wrong post value: url", map[string]string{"List-Unsubscribe": "<https://a.example/u>", "List-Unsubscribe-Post": "List-Unsubscribe=Other"}, api.BulkNewsletter, api.UnsubscribeURL, "a.example"},
		{"https only no post: url", map[string]string{"List-Unsubscribe": "<https://a.example/u>"}, api.BulkNewsletter, api.UnsubscribeURL, "a.example"},
		{"list one-click without mailto", map[string]string{"List-Unsubscribe": "<https://a.example/u>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}, api.BulkList, api.UnsubscribeOneClick, "a.example"},
		{"post header without https", map[string]string{"List-Unsubscribe": "<mailto:u@b.example>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}, api.BulkNewsletter, api.UnsubscribeMailto, "u@b.example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := Choose(c.h, c.kind)
			if o == nil || o.Method != c.method || o.Target != c.target {
				t.Fatalf("Choose = %+v, want %s %s", o, c.method, c.target)
			}
			if o.URI == "" {
				t.Error("empty URI")
			}
		})
	}
	for _, h := range []map[string]string{nil, {"List-Unsubscribe": "<http://a.example/>"}, {"List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}} {
		if o := Choose(h, api.BulkNewsletter); o != nil {
			t.Errorf("Choose(%v) = %+v, want nil", h, o)
		}
	}
}

func TestDomainAndAligned(t *testing.T) {
	for in, want := range map[string]string{
		"a@News.Example.COM": "news.example.com", "a@x.example.": "x.example", "noat": "", "a@": "",
		"a@b" + string(rune(0x202E)) + "c.example": "", "a@b c.example": "",
	} {
		if got := Domain(in); got != want {
			t.Errorf("Domain(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct {
		d, from string
		want    bool
	}{
		{"example.com", "mail.example.com", true},
		{"mail.example.com", "example.com", true},
		{"EXAMPLE.com.", "example.com", true},
		{"example.com", "example.org", false},
		{"co.uk", "co.uk", false},
		{"a.co.uk", "b.co.uk", false},
		{"a.example.co.uk", "b.example.co.uk", true},
		{"", "example.com", false},
		{"example.com", "", false},
		{"com", "example.com", false},
	} {
		if got := Aligned(c.d, c.from); got != c.want {
			t.Errorf("Aligned(%q, %q) = %v, want %v", c.d, c.from, got, c.want)
		}
	}
}

func TestInfo(t *testing.T) {
	if Info("", "", "a@b.example") != nil || Info("none", "", "a@b.example") != nil || Info("bogus", "", "a@b.example") != nil {
		t.Error("Info returned a value for an unclassified row")
	}
	got := Info("list", "x.example", "A@News.Example")
	if got == nil || got.Kind != api.BulkList || got.ListID != "x.example" || got.Domain != "news.example" {
		t.Errorf("%+v", got)
	}
	if k := RememberKey("x.example", "a@b"); k != "list:x.example" {
		t.Error(k)
	}
	if k := RememberKey("", " A@B.Example "); k != "from:a@b.example" {
		t.Error(k)
	}
}

func TestFixtures(t *testing.T) {
	cases := []struct {
		file   string
		want   Result
		method api.UnsubscribeMethod // "" = no offer
		target string
	}{
		{"bulk-newsletter-oneclick.eml", Result{Kind: api.BulkNewsletter}, api.UnsubscribeOneClick, "news.example.com"},
		{"bulk-list-golang.eml", Result{Kind: api.BulkList, ListID: "golang-nuts.googlegroups.com"}, api.UnsubscribeMailto, "golang-nuts+unsubscribe@googlegroups.com"},
		{"bulk-automated.eml", Result{Kind: api.BulkAutomated}, "", ""},
		{"bulk-post-no.eml", Result{Kind: api.BulkNewsletter, ListID: "announce.example.com"}, api.UnsubscribeURL, "news.example.com"},
		{"bulk-hostile-unsubscribe.eml", Result{}, "", ""},
		{"bulk-broken-brackets.eml", Result{Kind: api.BulkNewsletter}, api.UnsubscribeMailto, "unsub@news.example.com"},
		{"bulk-huge-unsubscribe.eml", Result{Kind: api.BulkNewsletter}, api.UnsubscribeMailto, "unsub@news.example.com"},
		{"bulk-listid-hostile.eml", Result{Kind: api.BulkList}, "", ""},
		{"bulk-empty-values.eml", Result{}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			h := fixtureHeaders(t, c.file)
			res := Classify(h)
			if res != c.want {
				t.Fatalf("Classify = %+v, want %+v (headers %q)", res, c.want, h)
			}
			o := Choose(h, res.Kind)
			if c.method == "" {
				if o != nil {
					t.Fatalf("offer %+v, want none", o)
				}
				return
			}
			if o == nil || o.Method != c.method || o.Target != c.target {
				t.Fatalf("offer %+v, want %s %s", o, c.method, c.target)
			}
			for _, r := range o.URI + o.Target {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					t.Errorf("offer holds %U", r)
				}
			}
		})
	}
}
