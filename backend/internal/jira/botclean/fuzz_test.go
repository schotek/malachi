// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzClean: never a panic (onPanic makes one fatal), never an empty
// comment from a non-empty one, the input back whenever nothing changed,
// clean author names.
func FuzzClean(f *testing.F) {
	c, _ := Compile(Rules{
		BotNames:        []string{issueSync, "bot"},
		MetadataFilters: []string{`^Remote comment create date:.*$`, `(?i).*secret.*`, `^\s*$`, "[invalid"},
		AuthorPrefixes:  []string{"ACME", "x"},
	})
	for _, tc := range cleanCases() {
		f.Add(tc.author, tc.in)
	}
	for _, name := range fixtureNames(f) {
		f.Add(issueSync, readFixture(f, name))
	}
	for _, s := range []string{
		"<p>ITSD-1 x added comment - 10/06/26 10:00 GMT</p>",
		"<p>ITSD-1 ACME x added comment - 10/06/26 10:00 GMT<br>secret</p><p>y</p>",
		"<b><p>ITSD-1 a added comment - 1/1/1 1:11</b>x</p>",
		"<table><p>ITSD-1 a added comment - x</p><td>y</table>",
		"ITSD-1 a added comment - x\n\n<br><br>y\r\n",
		"<p><plaintext>ITSD-1 a added comment - x\ny",
		"<select><option>ITSD-1 a added comment - x<br>y",
		"<template><p>ITSD-1 a added comment - x</p></template><p>y</p>",
		"<a><div><a>ITSD-1 a added comment - x<br>y</a></div></a>",
		"<p>" + rlo + "ITSD-1 a added comment - x</p><p>" + zwsp + "</p>",
	} {
		f.Add("bot", s)
	}

	f.Fuzz(func(t *testing.T, author, in string) {
		res := c.Clean(author, in, fallback)
		if in != "" && res.HTML == "" {
			t.Fatalf("Clean(%q, %q) emptied the comment", author, in)
		}
		if res.Changed != (res.HTML != in || res.AuthorName != "") {
			t.Fatalf("Clean(%q, %q): Changed = %v with HTML changed %v, author %q", author, in, res.Changed, res.HTML != in, res.AuthorName)
		}
		if (res.AuthorName == "") != (res.Via == "") {
			t.Fatalf("Clean(%q, %q): AuthorName %q with Via %q", author, in, res.AuthorName, res.Via)
		}
		if res.AuthorName != "" {
			if !utf8.ValidString(res.AuthorName) || strings.TrimSpace(res.AuthorName) != res.AuthorName {
				t.Fatalf("AuthorName %q", res.AuthorName)
			}
			for _, r := range res.AuthorName {
				if invisible(r) || r == '\n' {
					t.Fatalf("AuthorName %q has %U", res.AuthorName, r)
				}
			}
		}
		if res.HTML != in {
			// What came out is clean input as well.
			if again := c.Clean(author, res.HTML, fallback); again.HTML == "" {
				t.Fatalf("second pass over %q emptied it", res.HTML)
			}
		}
	})
}

func fixtureNames(t testing.TB) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(fixtureDir, "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range names {
		if !strings.HasSuffix(n, ".want.html") {
			out = append(out, filepath.Base(n))
		}
	}
	return out
}

const fixtureDir = "../../../testdata/jira/botclean"

func readFixture(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
