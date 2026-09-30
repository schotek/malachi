// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// notifyConfig is a cloud account of acme.atlassian.net with the spaces
// ITSD, WEB and MOB2 selected.
func notifyConfig() api.JiraConfig {
	return api.JiraConfig{
		SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "jana.dvorakova@acme.test",
		Spaces: []api.SpaceRef{{ID: "10000", Key: "ITSD"}, {ID: "10001", Key: "WEB"}, {ID: "10002", Key: "MOB2"}},
	}
}

var siteSender = []api.Address{{Name: "Petr Svoboda (Jira)", Address: "jira@acme.atlassian.net"}}

// expandSubject replaces the tokens of testdata/jira/subjects.txt.
func expandSubject(t *testing.T, s string) string {
	t.Helper()
	if s == "<empty>" {
		return ""
	}
	var b strings.Builder
	for len(s) > 0 {
		open := strings.IndexByte(s, '<')
		if open < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:open])
		s = s[open:]
		end := strings.IndexByte(s, '>')
		switch {
		case strings.HasPrefix(s, "<U+") && end > 0:
			cp, err := strconv.ParseUint(s[3:end], 16, 32)
			if err != nil {
				t.Fatalf("code point in %q: %v", s, err)
			}
			b.WriteRune(rune(cp))
			s = s[end+1:]
		case strings.HasPrefix(s, "<repeat:"):
			// The text may hold a ">" of its own: the token ends at the
			// last one of the line's first token only when it is alone,
			// so the file keeps such texts free of ">".
			if end < 0 {
				t.Fatalf("unterminated token in %q", s)
			}
			parts := strings.SplitN(s[len("<repeat:"):end], ":", 2)
			n, err := strconv.Atoi(parts[0])
			if err != nil || len(parts) != 2 {
				t.Fatalf("repeat in %q: %v", s, err)
			}
			b.WriteString(strings.Repeat(parts[1], n))
			s = s[end+1:]
		default:
			b.WriteByte('<')
			s = s[1:]
		}
	}
	return b.String()
}

func TestMatchNotificationSubjects(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "jira", "subjects.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg := notifyConfig()
	sc := bufio.NewScanner(f)
	cases, matches := 0, 0
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		want, raw, ok := strings.Cut(text, " | ")
		if !ok {
			t.Fatalf("line %d: no separator: %q", line, text)
		}
		subject := expandSubject(t, raw)
		cases++
		start := time.Now()
		key, found := MatchNotification(cfg, nil, siteSender, subject)
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("line %d: took %v", line, d)
		}
		switch {
		case want == "-" && (found || key != ""):
			t.Errorf("line %d: %q matched %q", line, raw, key)
		case want != "-" && (!found || key != want):
			t.Errorf("line %d: %q = %q, %v, want %q", line, raw, key, found, want)
		}
		if found {
			matches++
			if !utf8.ValidString(key) || !strings.Contains(subject, "("+key+")") && !strings.Contains(subject, "["+key+"]") {
				t.Errorf("line %d: key %q is not in the subject", line, key)
			}
			if cleanIssueKey(flexString(key)) != key {
				t.Errorf("line %d: the syncer would not take the key %q", line, key)
			}
		}
		// Whatever the subject says, a sender that is not the site's
		// makes it no notification.
		if key, found := MatchNotification(cfg, nil, []api.Address{{Address: "mallory@example.org"}}, subject); found || key != "" {
			t.Errorf("line %d: matched for a stranger", line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if cases < 100 || matches < 25 {
		t.Fatalf("%d cases, %d matches: the file was not read whole", cases, matches)
	}
}

func TestMatchNotificationSenders(t *testing.T) {
	const subject = "[JIRA] (ITSD-42) Printer on the 2nd floor"
	addr := func(a ...string) []api.Address {
		out := make([]api.Address, len(a))
		for i, s := range a {
			out[i] = api.Address{Address: s}
		}
		return out
	}
	kelvin := "jira@acme.atlassian.net"
	kelvin = strings.Replace(kelvin, "atlassian", "at"+string(rune(0x212A))+"assian", 1) // not a match for "l", and the Kelvin sign folds to k
	long := strings.Repeat("a", 400) + "@acme.atlassian.net"
	for _, c := range []struct {
		name    string
		senders []string // nil = the default of a cloud site
		from    []api.Address
		want    bool
	}{
		{"the site's address", nil, siteSender, true},
		{"any address of the site", nil, addr("automation@acme.atlassian.net"), true},
		{"capitals", nil, addr("JIRA@ACME.Atlassian.NET"), true},
		{"a trailing dot is the same host", nil, addr("jira@acme.atlassian.net."), true},
		{"two trailing dots are not", nil, addr("jira@acme.atlassian.net.."), false},
		{"no sender", nil, nil, false},
		{"an empty address", nil, addr(""), false},
		{"a stranger", nil, addr("mallory@example.org"), false},
		{"the site in the display name", nil, []api.Address{{Name: "jira@acme.atlassian.net", Address: "mallory@example.org"}}, false},
		{"the site's address in the display name", nil, []api.Address{{Name: "Jira <jira@acme.atlassian.net>", Address: "mallory@example.org"}}, false},
		{"the site in the local part", nil, addr("jira@acme.atlassian.net@example.org"), false},
		{"the site in a quoted local part", nil, addr(`"jira@acme.atlassian.net"@example.org`), false},
		{"a subdomain", nil, addr("jira@mail.acme.atlassian.net"), false},
		{"the parent domain", nil, addr("jira@atlassian.net"), false},
		{"another site of the provider", nil, addr("jira@evil.atlassian.net"), false},
		{"the site as a prefix", nil, addr("jira@acme.atlassian.net.example.org"), false},
		{"the site as a suffix", nil, addr("jira@notacme.atlassian.net"), false},
		{"a hyphen for a dot", nil, addr("jira@acme-atlassian.net"), false},
		{"a look-alike letter", nil, addr("jira@acm" + string(rune(0x0435)) + ".atlassian.net"), false}, // Cyrillic ie
		{"an accented letter", nil, addr("jira@acmé.atlassian.net"), false},
		{"a letter that folds to another", nil, addr(kelvin), false},
		{"an encoded host", nil, addr("jira@xn--acm-dma.atlassian.net"), false},
		{"a full-width dot", nil, addr("jira@acme.atlassian" + string(rune(0xFF0E)) + "net"), false},
		{"no host", nil, addr("jira@"), false},
		{"no at sign", nil, addr("acme.atlassian.net"), false},
		{"the host alone", nil, addr("@acme.atlassian.net"), false},
		{"a space after the address", nil, addr("jira@acme.atlassian.net "), false},
		{"a line break after the address", nil, addr("jira@acme.atlassian.net\r\n"), false},
		{"a NUL in the address", nil, addr("jira@acme.atlassian.net\x00.example.org"), false},
		{"a comment after the host", nil, addr("jira@acme.atlassian.net(x)"), false},
		{"angle brackets", nil, addr("<jira@acme.atlassian.net>"), false},
		{"a source route", nil, addr("@example.org:jira@acme.atlassian.net"), false},
		{"a port", nil, addr("jira@acme.atlassian.net:25"), false},
		{"too long an address", nil, addr(long), false},
		{"one of two senders is a stranger", nil, addr("jira@acme.atlassian.net", "mallory@example.org"), false},
		{"a stranger first", nil, addr("mallory@example.org", "jira@acme.atlassian.net"), false},
		{"two senders of the site", nil, addr("jira@acme.atlassian.net", "automation@acme.atlassian.net"), true},
		{"more senders than a notification has", nil, addr("a@acme.atlassian.net", "b@acme.atlassian.net", "c@acme.atlassian.net", "d@acme.atlassian.net", "e@acme.atlassian.net"), false},

		{"a configured address", []string{"jira@acme.example.org"}, addr("Jira@Acme.Example.org"), true},
		{"a configured address, another local part", []string{"jira@acme.example.org"}, addr("automation@acme.example.org"), false},
		{"a configured list replaces the default", []string{"jira@acme.example.org"}, siteSender, false},
		{"a configured host", []string{"@acme.example.org"}, addr("anything@acme.example.org"), true},
		{"a configured host in capitals with a dot", []string{"@ACME.Example.org."}, addr("jira@acme.example.org"), true},
		{"one of several", []string{"noreply@example.net", "@acme.example.org", "jira@acme.atlassian.net"}, siteSender, true},
		{"an address is no host", []string{"jira@acme.example.org"}, addr("x@jira@acme.example.org"), false},
		{"entries that are neither form", []string{"acme.example.org", "@", "jira@", "", " @acme.atlassian.net", "*@acme.atlassian.net"}, siteSender, false},
		{"a wildcard is text", []string{"@*.atlassian.net", "@%", "@_cme.atlassian.net"}, siteSender, false},
	} {
		cfg := notifyConfig()
		cfg.NotificationSenders = c.senders
		key, ok := MatchNotification(cfg, nil, c.from, subject)
		if ok != c.want || (ok && key != "ITSD-42") || (!ok && key != "") {
			t.Errorf("%s: %q, %v, want %v", c.name, key, ok, c.want)
		}
	}
}

func TestNotificationSendersDefault(t *testing.T) {
	cloud := notifyConfig()
	if got := NotificationSenders(cloud); !slices.Equal(got, []string{"@acme.atlassian.net"}) {
		t.Fatalf("cloud default: %v", got)
	}
	cloud.SiteURL = "https://Acme.Atlassian.NET:443/"
	if got := NotificationSenders(cloud); !slices.Equal(got, []string{"@acme.atlassian.net"}) {
		t.Fatalf("cloud default of %s: %v", cloud.SiteURL, got)
	}
	for _, site := range []string{"", "not a url", "https://", "mailto:jira@acme.atlassian.net"} {
		cloud.SiteURL = site
		if got := NotificationSenders(cloud); len(got) != 0 {
			t.Errorf("site %q: %v", site, got)
		}
		if _, ok := MatchNotification(cloud, nil, siteSender, "(ITSD-42)"); ok {
			t.Errorf("site %q matches", site)
		}
	}

	// A Data Center site sends through the organisation's own server:
	// nothing matches until the senders are configured.
	dc := notifyConfig()
	dc.SiteURL, dc.Deployment, dc.Login = "https://jira.acme.test/jira", api.JiraDataCenter, ""
	if got := NotificationSenders(dc); len(got) != 0 {
		t.Fatalf("datacenter default: %v", got)
	}
	for _, from := range []string{"jira@acme.atlassian.net", "jira@jira.acme.test", "jira@acme.test"} {
		if key, ok := MatchNotification(dc, nil, []api.Address{{Address: from}}, "[JIRA] (ITSD-42) Printer"); ok {
			t.Errorf("datacenter default matched %s: %q", from, key)
		}
	}
	dc.NotificationSenders = []string{"jira@acme.test"}
	if key, ok := MatchNotification(dc, nil, []api.Address{{Address: "jira@acme.test"}}, "[JIRA] (ITSD-42) Printer"); !ok || key != "ITSD-42" {
		t.Fatalf("datacenter with a sender: %q %v", key, ok)
	}
	// More entries than the configuration may hold are not read.
	many := notifyConfig()
	for i := range api.MaxJiraListEntries + 10 {
		many.NotificationSenders = append(many.NotificationSenders, "n"+strconv.Itoa(i)+"@example.org")
	}
	if got := NotificationSenders(many); len(got) != api.MaxJiraListEntries {
		t.Fatalf("%d senders read", len(got))
	}
}

func TestMatchNotificationSpaces(t *testing.T) {
	cfg := notifyConfig()
	match := func(cfg api.JiraConfig, spaces []store.IssueSpace, subject string) string {
		key, ok := MatchNotification(cfg, spaces, siteSender, subject)
		if !ok {
			return ""
		}
		return key
	}
	// The site's list: a space it renamed is known by its new key, one it
	// does not list by the configuration's, and one the configuration does
	// not select by none.
	listed := []store.IssueSpace{
		{SpaceID: "10000", Key: "HELP"}, {SpaceID: "10001", Key: "WEB"}, {SpaceID: "10009", Key: "OPS"},
	}
	for subject, want := range map[string]string{
		"(HELP-1)": "HELP-1", "(ITSD-1)": "", "(WEB-2)": "WEB-2", "(MOB2-3)": "MOB2-3", "(OPS-4)": "",
	} {
		if got := match(cfg, listed, subject); got != want {
			t.Errorf("%s with the site's list: %q, want %q", subject, got, want)
		}
	}
	// A space deselected in the configuration is gone at once, whatever
	// the stored list still says.
	fewer := cfg
	fewer.Spaces = cfg.Spaces[1:]
	if got := match(fewer, []store.IssueSpace{{SpaceID: "10000", Key: "ITSD"}, {SpaceID: "10001", Key: "WEB"}}, "(ITSD-1)"); got != "" {
		t.Errorf("a deselected space matched %q", got)
	}
	none := cfg
	none.Spaces = nil
	if got := match(none, listed, "(WEB-2)"); got != "" {
		t.Errorf("no selected space matched %q", got)
	}
	// Keys no subject can name are no keys: lower case, an underscore (Data
	// Center allows it), too long, empty.
	odd := cfg
	odd.Spaces = []api.SpaceRef{{ID: "1", Key: "itsd"}, {ID: "2", Key: "MY_KEY"}, {ID: "3", Key: "ABCDEFGHIJK"}, {ID: "4", Key: ""},
		{ID: "5", Key: "A"}, {ID: "6", Key: "1AB"}, {ID: "7", Key: "AB-1"}, {ID: "8", Key: "ABCDEFGHIJ"}}
	for subject, want := range map[string]string{
		"(itsd-1)": "", "(MY_KEY-1)": "", "(ABCDEFGHIJK-1)": "", "(-1)": "", "(A-1)": "", "(1AB-1)": "", "(AB-1-1)": "",
		"(ABCDEFGHIJ-1)": "ABCDEFGHIJ-1",
	} {
		if got := match(odd, nil, subject); got != want {
			t.Errorf("%s: %q, want %q", subject, got, want)
		}
	}
	// The mode: ignore matches nothing, the others do.
	for mode, want := range map[api.NotificationMailMode]string{
		"": "ITSD-42", api.NotificationMailSync: "ITSD-42", api.NotificationMailHide: "ITSD-42", api.NotificationMailIgnore: "",
	} {
		c := cfg
		c.NotificationMail = mode
		if got := match(c, nil, "[JIRA] (ITSD-42) Printer"); got != want {
			t.Errorf("mode %q: %q, want %q", mode, got, want)
		}
	}
}

// The matcher's work is bounded by what it reads, whatever the subject
// holds.
func TestMatchNotificationBounded(t *testing.T) {
	cfg := notifyConfig()
	for name, subject := range map[string]string{
		"brackets":         strings.Repeat("(", 8<<20),
		"keys without end": strings.Repeat("(ITSD-1", 1<<20),
		"unselected keys":  strings.Repeat("(OPS-1)", 1<<20),
		"digits":           "(ITSD-" + strings.Repeat("9", 8<<20) + ")",
		"letters":          "(" + strings.Repeat("A", 8<<20) + "-1)",
		"invalid UTF-8":    strings.Repeat("\xff(\xfe[", 1<<20),
	} {
		start := time.Now()
		for range 100 {
			if key, ok := MatchNotification(cfg, nil, siteSender, subject); ok {
				t.Fatalf("%s matched %q", name, key)
			}
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: 100 subjects took %v", name, d)
		}
	}
	allocs := testing.AllocsPerRun(50, func() {
		MatchNotification(cfg, nil, siteSender, "[JIRA] (ITSD-42) Printer on the 2nd floor")
	})
	if allocs > 20 {
		t.Errorf("%v allocations for one subject", allocs)
	}
}

func FuzzMatchNotification(f *testing.F) {
	for _, s := range []string{"[JIRA] (ITSD-42) Printer", "IT Service Desk: Printer (ITSD-20)", "(((", "[ITSD-0]", "(ITSD-42"} {
		f.Add("jira@acme.atlassian.net", s)
	}
	f.Add("mallory@example.org", "(ITSD-42)")
	cfg := notifyConfig()
	f.Fuzz(func(t *testing.T, from, subject string) {
		key, ok := MatchNotification(cfg, nil, []api.Address{{Address: from}}, subject)
		if !ok {
			if key != "" {
				t.Fatalf("a key without a match: %q", key)
			}
			return
		}
		if !strings.Contains(subject, "("+key+")") && !strings.Contains(subject, "["+key+"]") {
			t.Fatalf("key %q is not in %q", key, subject)
		}
		space, _, _ := strings.Cut(key, "-")
		if !slices.Contains([]string{"ITSD", "WEB", "MOB2"}, space) {
			t.Fatalf("key %q of a space that is not selected", key)
		}
		if !strings.HasSuffix(asciiLower(strings.TrimSuffix(from, ".")), "@acme.atlassian.net") {
			t.Fatalf("matched for %q", from)
		}
	})
}
