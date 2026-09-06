// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVCardCorpus(t *testing.T) {
	cases := []struct {
		file   string
		name   string
		emails []string
		ok     bool
	}{
		{"simple.vcf", "Alice Example", []string{"alice@example.org"}, true},
		{"folded_crlf.vcf", "Alice Longname Wrapped", []string{"alice.long@example.org"}, true},
		{"multi_email.vcf", "Many Mails", []string{"two@example.org", "three@example.org", "one@example.org"}, true},
		{"no_fn.vcf", "Jan Novák", []string{"jan@example.cz"}, true},
		{"nickname_only.vcf", "Jenda", []string{"jenda@example.cz"}, true},
		{"qp_21.vcf", "Jan Novák", []string{"jan@example.cz", "jan.novak@example.cz"}, true},
		{"qp_softbreak.vcf", "Jan Novák", []string{"jan@example.cz"}, true},
		{"photo.vcf", "Photo Person", []string{"photo@example.org"}, true},
		{"invalid_utf8.vcf", "", []string{"badname@example.org"}, true},
		{"control.vcf", "", []string{"bell@example.org"}, true},
		{"escapes.vcf", `Example, Alice; Jr\`, []string{"escaped@example.org"}, true},
		{"grouped.vcf", "Grouped Person", []string{"grouped@example.org", "home@example.org"}, true},
		{"glued.vcf", "First Card", []string{"first@example.org"}, true},
		{"empty.vcf", "", nil, false},
		{"garbage.vcf", "", nil, false},
		{"bad_email.vcf", "", nil, false},
		{"dup_email.vcf", "Dup Mail", []string{"dup@example.org"}, true},
		{"n_family_only.vcf", "Novák", []string{"novak@example.cz"}, true},
		{"quoted_param.vcf", "Quoted Param", []string{"quoted@example.org"}, true},
		{"lowercase_props.vcf", "Lower Case", []string{"lower@example.org"}, true},
		{"many_emails.vcf", "Many Addresses", []string{"m1@example.org", "m2@example.org", "m3@example.org", "m4@example.org",
			"m5@example.org", "m6@example.org", "m7@example.org", "m8@example.org"}, true},
		{"no_email.vcf", "", nil, false},
		{"blank_fn.vcf", "", []string{"blank@example.org"}, true},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "vcard", c.file))
		if err != nil {
			t.Fatal(err)
		}
		name, emails, ok := parseVCard(string(raw))
		if ok != c.ok || name != c.name || strings.Join(emails, ",") != strings.Join(c.emails, ",") {
			t.Errorf("%s: name %q emails %v ok %v; want %q %v %v", c.file, name, emails, ok, c.name, c.emails, c.ok)
		}
	}
}

func TestParseVCardOversized(t *testing.T) {
	big := "BEGIN:VCARD\nFN:Big\nNOTE:" + strings.Repeat("x", maxVCardBytes) + "\nEMAIL:big@example.org\nEND:VCARD\n"
	if _, _, ok := parseVCard(big); ok {
		t.Error("oversized card accepted")
	}
	long := "BEGIN:VCARD\nFN:" + strings.Repeat("n", maxNameBytes+1) + "\nEMAIL:long@example.org\nEND:VCARD\n"
	if name, emails, ok := parseVCard(long); !ok || name != "" || len(emails) != 1 {
		t.Errorf("overlong name: %q %v %v", name, emails, ok)
	}
}

func TestUnescapeText(t *testing.T) {
	cases := map[string]string{
		`a\,b`:    "a,b",
		`a\;b`:    "a;b",
		`a\nb`:    "a\nb",
		`a\Nb`:    "a\nb",
		`a\\b`:    `a\b`,
		`a\xb`:    `a\xb`,
		`trail\`:  `trail\`,
		"plain":   "plain",
		`\,\;\\n`: `,;\n`,
	}
	for in, want := range cases {
		if got := unescapeText(in); got != want {
			t.Errorf("unescapeText(%q) = %q, want %q", in, got, want)
		}
	}
}
