// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readCorpus(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "vcard", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// corpusFiles lists the samples of this package (the other files of
// testdata/vcard belong to the EDS reader).
func corpusFiles(t testing.TB) []string {
	t.Helper()
	a, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "vcard", "*-*.vcf"))
	b, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "vcard", "pathological", "*.vcf"))
	all := append(a, b...)
	if len(all) < 14 {
		t.Fatalf("corpus has %d files", len(all))
	}
	return all
}

func mustParse(t testing.TB, raw []byte) *Card {
	t.Helper()
	c, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestCorpus(t *testing.T) {
	cases := []struct {
		file  string
		check func(t *testing.T, c *Card)
	}{
		{"apple-icloud-3.0.vcf", func(t *testing.T, c *Card) {
			want := Name{Given: "Jana", Family: "Nováková", Middle: "Marie", Prefix: "Ing.", Suffix: "Ph.D."}
			eq(t, "name", c.Name, want)
			eq(t, "org", c.Org, "Acme s.r.o.")
			eq(t, "emails", c.Emails, []Email{{"other", "jana@example.cz"}, {"work", "jana.novakova@acme.example"}, {"Klub", "jn@club.example"}})
			eq(t, "phones", c.Phones, []Phone{{"mobile", "+420 777 123 456"}, {"work", "+420 222 333 444"}, {"Main", "+420 602 000 111"}})
			eq(t, "address", c.Addresses, []Address{{Label: "home", Street: "Dlouhá 12\nByt 4", City: "Praha", Postcode: "110 00", Country: "Česká republika"}})
			eq(t, "urls", c.URLs, []URL{{"", "https://acme.example/jana"}})
			eq(t, "birthday", c.Birthday, Birthday{0, 3, 15})
			eq(t, "note", c.Note, "Poznámka, první řádek\nDruhý řádek")
			eq(t, "uid", c.UID, "4F2B1C3D-AAAA-BBBB-CCCC-1234567890AB")
		}},
		{"google-3.0.vcf", func(t *testing.T, c *Card) {
			eq(t, "phones", c.Phones, []Phone{{"mobile", "(555) 123-4567"}, {"work", "+1 555 765 4321"}, {"Pager", "+1 555 000 1111"}})
			eq(t, "urls", c.URLs, []URL{{"blog", "http://john.example.com"}})
			eq(t, "birthday", c.Birthday, Birthday{1980, 7, 4})
		}},
		{"nextcloud-4.0.vcf", func(t *testing.T, c *Card) {
			eq(t, "version", c.Version, "4.0")
			eq(t, "org", c.Org, "Firma")
			eq(t, "emails", c.Emails, []Email{{"work", "petr@firma.example"}, {"home", "petr@doma.example"}})
			eq(t, "phones", c.Phones, []Phone{{"mobile", "+420-601-222-333"}, {"fax", "+420 222 999 888"}})
			eq(t, "birthday", c.Birthday, Birthday{0, 2, 29})
		}},
		{"fastmail-4.0.vcf", func(t *testing.T, c *Card) {
			eq(t, "name", c.Name, Name{Given: "Ann", Family: "O'Neil", Prefix: "Dr."})
			eq(t, "note", c.Note, "Multi\nline, note")
			eq(t, "address", c.Addresses[0].Street, "10 Downing St")
		}},
		{"outlook-export-2.1.vcf", func(t *testing.T, c *Card) {
			eq(t, "name", c.Name, Name{Given: "Tomáš", Family: "Černý"})
			eq(t, "fn", c.DisplayName, "Černý Tomáš")
			eq(t, "title", c.Title, "Obchodní zástupce")
			eq(t, "phones", c.Phones, []Phone{{"work", "+420 123 456 789"}, {"mobile", "+420 987 654 321"}, {"fax", "+420 111 222 333"}})
			eq(t, "address", c.Addresses, []Address{{Label: "work", Street: "Hlavní 5\n2. patro", City: "Brno", Postcode: "602 00", Country: "Česko"}})
			eq(t, "note", c.Note, "First line\nSecond line with a fold")
		}},
		{"pathological/folded-utf8-split.vcf", func(t *testing.T, c *Card) {
			eq(t, "fn", c.DisplayName, "Žluťoučký kůň")
			eq(t, "family", c.Name.Family, "Kůn")
		}},
		{"pathological/huge-base64.vcf", func(t *testing.T, c *Card) {
			eq(t, "emails", c.Emails, []Email{{"", "big@photo.example"}})
		}},
		{"pathological/deep-escapes.vcf", func(t *testing.T, c *Card) {
			eq(t, "name", c.Name, Name{Family: `Fam;ily\`, Given: "Gi,ven", Suffix: `\`})
			eq(t, "email", c.Emails[0].Address, "weird;addr@example.org")
			if !strings.HasSuffix(c.Note, "end\\") {
				t.Errorf("note ends %q", c.Note[len(c.Note)-10:])
			}
		}},
		{"pathological/bad-charset.vcf", func(t *testing.T, c *Card) {
			eq(t, "fn", c.DisplayName, "Bad � name")
			eq(t, "note", c.Note, "あい �")
		}},
		{"pathological/no-end.vcf", func(t *testing.T, c *Card) {
			eq(t, "emails", c.Emails, []Email{{"", "noend@example.org"}})
		}},
		{"pathological/many-emails.vcf", func(t *testing.T, c *Card) {
			eq(t, "emails", len(c.Emails), MaxItems)
		}},
		{"pathological/nul-and-bidi.vcf", func(t *testing.T, c *Card) {
			eq(t, "fn", c.DisplayName, "Evil Name")
			eq(t, "name", c.Name, Name{Family: "Fam", Given: "Given"})
			eq(t, "emails", c.Emails, []Email{{"work", "evil@example.org"}, {"Label", "x@example.org"}})
		}},
		{"pathological/multiple-cards.vcf", func(t *testing.T, c *Card) {
			eq(t, "fn", c.DisplayName, "First")
			eq(t, "emails", c.Emails, []Email{{"", "first@example.org"}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			c := mustParse(t, readCorpus(t, tc.file))
			tc.check(t, c)
			if !reflect.DeepEqual(Clean(c), c) {
				t.Errorf("Clean of a parsed card changed it")
			}
		})
	}
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse(readCorpus(t, "pathological/many-props.vcf")); !errors.Is(err, ErrTooManyProperties) {
		t.Errorf("many props: %v", err)
	}
	big := append([]byte("BEGIN:VCARD\r\nNOTE:"), make([]byte, MaxCardBytes)...)
	if _, err := Parse(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big: %v", err)
	}
	for _, s := range []string{"", "garbage", "FN:x\r\nEND:VCARD", "BEGIN:VCALENDAR\r\nEND:VCALENDAR"} {
		if _, err := Parse([]byte(s)); !errors.Is(err, ErrNoCard) {
			t.Errorf("%q: %v", s, err)
		}
	}
}

func card(lines ...string) []byte {
	return []byte("BEGIN:VCARD\r\nVERSION:3.0\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCARD\r\n")
}

func TestParseTable(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want func(c *Card) any
		exp  any
	}{
		{"type list", card("EMAIL;TYPE=work,pref:a@x.example"), func(c *Card) any { return c.Emails }, []Email{{"work", "a@x.example"}}},
		{"type repeated", card("EMAIL;TYPE=WORK;TYPE=PREF:a@x.example"), func(c *Card) any { return c.Emails }, []Email{{"work", "a@x.example"}}},
		{"pref=1", card("EMAIL;PREF=1;TYPE=home:a@x.example"), func(c *Card) any { return c.Emails }, []Email{{"home", "a@x.example"}}},
		{"bare 2.1", card("TEL;HOME;FAX:1"), func(c *Card) any { return c.Phones }, []Phone{{"fax", "1"}}},
		{"quoted type", card(`TEL;TYPE="cell,voice":2`), func(c *Card) any { return c.Phones }, []Phone{{"mobile", "2"}}},
		{"iphone", card("TEL;type=IPHONE:3"), func(c *Card) any { return c.Phones }, []Phone{{"mobile", "3"}}},
		{"mailto", card("EMAIL:mailto:a@x.example"), func(c *Card) any { return c.Emails[0].Address }, "a@x.example"},
		{"tel uri", card("TEL;VALUE=uri:tel:+1-2"), func(c *Card) any { return c.Phones[0].Number }, "+1-2"},
		{"lowercase", card("fn:low", "email:l@x.example"), func(c *Card) any { return c.DisplayName + " " + c.Emails[0].Address }, "low l@x.example"},
		{"lf only", []byte("BEGIN:VCARD\nVERSION:4.0\nFN:L\n F\nEND:VCARD\n"), func(c *Card) any { return c.DisplayName }, "LF"},
		{"tab fold", card("FN:Ta\r\n\tb"), func(c *Card) any { return c.DisplayName }, "Tab"},
		{"escapes", card(`NOTE:a\,b\;c\\d\ne\Nf\x`), func(c *Card) any { return c.Note }, "a,b;c\\d\ne\nf\\x"},
		{"trailing backslash", card(`FN:x\`), func(c *Card) any { return c.DisplayName }, `x\`},
		{"empty email skipped", card("EMAIL:", "EMAIL: b@x.example "), func(c *Card) any { return c.Emails }, []Email{{"", "b@x.example"}}},
		{"first FN wins", card("FN:one", "FN:two"), func(c *Card) any { return c.DisplayName }, "one"},
		{"bday ext", card("BDAY:1985-04-12"), func(c *Card) any { return c.Birthday }, Birthday{1985, 4, 12}},
		{"bday basic", card("BDAY:19850412"), func(c *Card) any { return c.Birthday }, Birthday{1985, 4, 12}},
		{"bday time", card("BDAY:1985-04-12T00:00:00Z"), func(c *Card) any { return c.Birthday }, Birthday{1985, 4, 12}},
		{"bday --MMDD", card("BDAY:--0412"), func(c *Card) any { return c.Birthday }, Birthday{0, 4, 12}},
		{"bday --MM-DD", card("BDAY:--04-12"), func(c *Card) any { return c.Birthday }, Birthday{0, 4, 12}},
		{"bday 1604", card("BDAY:1604-04-12"), func(c *Card) any { return c.Birthday }, Birthday{0, 4, 12}},
		{"bday invalid", card("BDAY:1985-02-30"), func(c *Card) any { return c.Birthday }, Birthday{}},
		{"bday text", card("BDAY;VALUE=text:circa 1800"), func(c *Card) any { return c.Birthday }, Birthday{}},
		{"apple label", card("item1.TEL:5", "item1.X-ABLabel:_$!<HomeFAX>!$_"), func(c *Card) any { return c.Phones }, []Phone{{"fax", "5"}}},
		{"label before item", card("item7.X-ABLabel:Chata", "item7.TEL:6"), func(c *Card) any { return c.Phones }, []Phone{{"Chata", "6"}}},
		{"qp 3.0", card("NOTE;ENCODING=QUOTED-PRINTABLE;CHARSET=UTF-8:=C5=BDlut=C3=BD"), func(c *Card) any { return c.Note }, "Žlutý"},
		{"base64 text", card("NOTE;ENCODING=b:SGVsbG8="), func(c *Card) any { return c.Note }, "Hello"},
		{"bad line skipped", card("::::", ";;;", "X Y:z", "FN:ok"), func(c *Card) any { return c.DisplayName }, "ok"},
		{"colon in quoted param", card(`ADR;LABEL="a:b;c":;;S;C;;;`), func(c *Card) any { return c.Addresses[0].Street }, "S"},
		{"org units", card(`ORG:A\;B;Unit`), func(c *Card) any { return c.Org }, "A;B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eq(t, tc.name, tc.want(mustParse(t, tc.raw)), tc.exp)
		})
	}
}

func TestCorpusNeverFails(t *testing.T) {
	all, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "vcard", "*.vcf"))
	for _, f := range append(all, corpusFiles(t)...) {
		raw, _ := os.ReadFile(f)
		c, err := Parse(raw)
		if err != nil {
			continue
		}
		if !reflect.DeepEqual(Clean(c), c) {
			t.Errorf("%s: Clean changed a parsed card", f)
		}
	}
}
