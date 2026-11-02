// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The fixtures are the owner's tool's own test data (admin-malachi-mail
// test/fixtures), signed with the test key "test-1": the daemon refuses
// exactly what the tool refuses.
const fixtures = "../../testdata/update"

type fixtureCase struct {
	File         string `json:"file"`
	Now          string `json:"now"`
	Expect       string `json:"expect"`
	Note         string `json:"note"`
	ErrorPath    string `json:"errorPath"`
	WarningPath  string `json:"warningPath"`
	LastIssuedAt string `json:"lastIssuedAt"`
}

// testKeys are the fixtures' key; never part of Keys.
func testKeys(t testing.TB) map[string]ed25519.PublicKey {
	t.Helper()
	var k struct {
		KeyID     string `json:"keyId"`
		PublicKey string `json:"publicKey"`
	}
	b, err := os.ReadFile(filepath.Join(fixtures, "test-key.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &k); err != nil {
		t.Fatal(err)
	}
	pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]ed25519.PublicKey{k.KeyID: ed25519.PublicKey(pub)}
}

func mustTime(t testing.TB, s string) time.Time {
	t.Helper()
	if s == "" {
		return time.Time{}
	}
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// check runs an envelope through Verify and Parse like the daemon does.
func check(envelope []byte, keys map[string]ed25519.PublicKey, now, last time.Time) (*Manifest, []string, error) {
	payload, err := Verify(envelope, keys)
	if err != nil {
		return nil, nil, err
	}
	return Parse(payload, now, last)
}

func TestFixtures(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(fixtures, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	keys := testKeys(t)
	seen := map[string]bool{"cases.json": true, "test-key.json": true, "live-2026-11-02.json": true}
	for _, c := range cases {
		seen[c.File] = true
		t.Run(strings.TrimSuffix(c.File, ".json"), func(t *testing.T) {
			env, err := os.ReadFile(filepath.Join(fixtures, c.File))
			if err != nil {
				t.Fatal(err)
			}
			m, warnings, err := check(env, keys, mustTime(t, c.Now), mustTime(t, c.LastIssuedAt))
			if c.Expect == "ok" {
				if err != nil {
					t.Fatalf("%s: refused: %v", c.Note, err)
				}
				if m == nil {
					t.Fatal("no manifest")
				}
				if c.WarningPath != "" && !slices.Contains(warnings, c.WarningPath) {
					t.Errorf("%s: warnings %q, want %q", c.Note, warnings, c.WarningPath)
				}
				if c.WarningPath == "" && len(warnings) > 0 {
					t.Errorf("%s: unexpected warnings %q", c.Note, warnings)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s: accepted, want %s", c.Note, c.Expect)
			}
			if e.Kind.String() != c.Expect {
				t.Fatalf("%s: %v, want %s", c.Note, err, c.Expect)
			}
			if c.Expect == "manifest" && e.Path != c.ErrorPath {
				t.Errorf("%s: path %q, want %q (%v)", c.Note, e.Path, c.ErrorPath, err)
			}
		})
	}
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !seen[e.Name()] {
			t.Errorf("%s has no case", e.Name())
		}
	}
}

// The production key verifies the manifest published on 2026-11-02, and
// the test key is not a production key.
func TestLiveManifest(t *testing.T) {
	if _, ok := Keys["test-1"]; ok {
		t.Fatal("the test key is among the production keys")
	}
	env, err := os.ReadFile(filepath.Join(fixtures, "live-2026-11-02.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := mustTime(t, "2026-11-02T08:00:00Z")
	m, warnings, err := check(env, Keys, now, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) > 0 {
		t.Errorf("warnings %q", warnings)
	}
	if !m.IssuedAt.Equal(mustTime(t, "2026-11-02T07:47:49Z")) || len(m.Releases) == 0 || m.Releases[0].Version.String() != "0.2.0" {
		t.Fatalf("manifest %+v", m)
	}
	for _, ch := range Channels {
		if _, ok := m.Releases[0].Packages[ch]; !ok {
			t.Errorf("0.2.0 has no %s", ch)
		}
	}
	old, _ := ParseVersion("0.1.2")
	if o := Select(m, "macos-dmg", old); o == nil || o.Release.Version.String() != "0.2.0" {
		t.Errorf("0.1.2 on macos-dmg is offered %+v", o)
	}
	if _, err := Verify(env, testKeys(t)); err == nil {
		t.Error("the test keys verify a production manifest")
	}
}

func TestVersion(t *testing.T) {
	for s, ok := range map[string]bool{
		"0.1.2": true, "10.0.0": true, "0.0.0": true,
		"v0.1.2": false, "0.1": false, "0.1.2.3": false, "01.1.2": false, "0.1.2-3-gabc1234": false,
		"dev": false, "": false, "0.1.": false, "1.2.+3": false, "1.2.3 ": false,
	} {
		if _, got := ParseVersion(s); got != ok {
			t.Errorf("ParseVersion(%q) = %v", s, got)
		}
	}
	a, _ := ParseVersion("0.10.0")
	b, _ := ParseVersion("0.9.12")
	if a.Compare(b) != 1 || b.Compare(a) != -1 || a.Compare(a) != 0 {
		t.Error("Compare")
	}
}

func manifestOf(t *testing.T, file string) *Manifest {
	t.Helper()
	env, err := os.ReadFile(filepath.Join(fixtures, file))
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := check(env, testKeys(t), mustTime(t, "2026-10-28T12:00:00Z"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSelect(t *testing.T) {
	m := manifestOf(t, "good.json") // 0.1.3 (security), 0.1.2, 0.1.1 (withdrawn)
	v := func(s string) Version { x, _ := ParseVersion(s); return x }
	for _, c := range []struct {
		channel, current, want string
		security               bool
	}{
		{"macos-dmg", "0.1.2", "0.1.3", true},
		{"macos-dmg", "0.1.3", "", false},
		{"windows-setup-arm64", "0.1.0", "0.1.2", false}, // 0.1.3 has no arm64 package: not skipped over
		{"windows-setup-x64", "0.1.0", "0.1.3", true},
		{"linux-deb-arm64", "0.1.0", "0.1.2", false}, // 0.1.1 withdrawn
		{"linux-deb-arm64", "0.1.1", "0.1.2", false},
		{"linux-flatpak-arm64", "0.1.0", "", false},
		{"solaris-pkg", "0.1.0", "", false},
	} {
		o := Select(m, c.channel, v(c.current))
		got := ""
		if o != nil {
			got = o.Release.Version.String()
		}
		if got != c.want || (o != nil && o.Security != c.security) {
			t.Errorf("%s from %s: %q %+v, want %q security %v", c.channel, c.current, got, o, c.want, c.security)
		}
	}
	if n := NoticeFor(m, v("0.1.2")); n == nil || n.ID != "n-2026-10-28" {
		t.Errorf("notice for 0.1.2: %+v", n)
	}
	if n := NoticeFor(m, v("0.1.3")); n != nil {
		t.Errorf("notice for 0.1.3: %+v", n)
	}
}

func TestText(t *testing.T) {
	texts := map[string]string{"en": "E", "cs": "C", "pt-BR": "P"}
	for loc, want := range map[string]string{
		"": "E", "cs": "C", "cs-CZ": "C", "cs_CZ.UTF-8": "C", "de": "E", "pt-br": "P", "pt_BR": "P", "pt": "E",
	} {
		if got := Text(texts, loc); got != want {
			t.Errorf("Text(%q) = %q, want %q", loc, got, want)
		}
	}
}

func TestAllowedURL(t *testing.T) {
	for s, ok := range map[string]bool{
		"https://github.com/schotek/malachi/releases/download/v0.2.0/x.dmg": true,
		"https://malachimail.app/":        true,
		"https://malachimail.app?x=1":     true,
		"http://malachimail.app/":         false,
		"https://malachimail.app:443/":    false,
		"https://u@malachimail.app/":      false,
		"https://malachimail.app/#x":      false,
		"https://MALACHIMAIL.app/":        false,
		"https://malachimail.app.evil/":   false,
		"https://evil/malachimail.app":    false,
		"https://malachimail.app/a b":     false,
		"https://malachimail.app\\@evil/": false,
		"https:malachimail.app":           false,
		"javascript:alert(1)":             false,
	} {
		if got := AllowedURL(s, downloadHosts); got != ok {
			t.Errorf("AllowedURL(%q) = %v", s, got)
		}
	}
}
