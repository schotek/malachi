// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func init() {
	// A panic Clean would swallow in production fails the tests.
	onPanic = func(v any) { panic(v) }
}

// Invisible and look-alike characters, spelled out so that none of them
// sits raw in the source.
const (
	rlo   = string(rune(0x202e)) // RIGHT-TO-LEFT OVERRIDE
	pdf   = string(rune(0x202c)) // POP DIRECTIONAL FORMATTING
	lrm   = string(rune(0x200e)) // LEFT-TO-RIGHT MARK
	zwsp  = string(rune(0x200b)) // ZERO WIDTH SPACE
	bom   = string(rune(0xfeff)) // ZERO WIDTH NO-BREAK SPACE
	shy   = string(rune(0x00ad)) // SOFT HYPHEN
	cyrO  = string(rune(0x043e)) // CYRILLIC SMALL LETTER O, looks like "o"
	arab1 = string(rune(0x0661)) // ARABIC-INDIC DIGIT ONE
)

const issueSync = "Issue Sync – Synchronization for Jira"

var fallback = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

// The prototype's defaults.
func defaultRules() Rules {
	return Rules{
		BotNames:        []string{issueSync},
		MetadataFilters: []string{`^Remote comment create date:.*$`},
	}
}

func mustCompile(t testing.TB, r Rules) *Compiled {
	t.Helper()
	c, errs := Compile(r)
	if len(errs) > 0 {
		t.Fatalf("Compile: %v", errs)
	}
	return c
}

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// header is a relayed comment's header line.
func header(key, author, date string) string {
	return key + " " + author + " added comment - " + date
}

type cleanCase struct {
	name     string
	rules    *Rules    // nil = defaultRules
	author   string    // the comment's Jira author
	in       string    // its rendered body
	fallback time.Time // zero = fallback

	author2 string    // want AuthorName
	created time.Time // want Created
	via     string    // want Via
	text    string    // want Text
	html    string    // want HTML; "" = the input
	changed bool      // want Changed
}

func cleanCases() []cleanCase {
	hyphenBot := "Issue Sync - Synchronization for Jira"
	withPrefix := defaultRules()
	withPrefix.AuthorPrefixes = []string{"ACME"}
	hyphenRules := defaultRules()
	hyphenRules.BotNames = []string{hyphenBot}
	matchAll := defaultRules()
	matchAll.MetadataFilters = []string{`.*`}
	dropsBody := defaultRules()
	dropsBody.MetadataFilters = append(dropsBody.MetadataFilters, `^Dobrý den$`)
	dropsHeader := defaultRules()
	dropsHeader.MetadataFilters = []string{`^ITSD-1 .*$`}
	shortBot := defaultRules()
	shortBot.BotNames = []string{"Jo"}
	containedBot := defaultRules()
	containedBot.BotNames = []string{"Issue Sync"}
	wordBot := defaultRules()
	wordBot.BotNames = []string{"sync"}
	plus3 := time.Date(2026, 6, 11, 12, 0, 0, 0, time.FixedZone("", 3*3600))

	h1 := header("ITSD-19", "Jana Dvořáková", "10/06/26 10:54 AM CEST")
	hGMT := header("ITSD-1", "Jana Dvořáková", "10/06/26 10:00 GMT")
	jana1000 := utc(2026, 6, 10, 10, 0)

	return []cleanCase{
		// The prototype's cases (BotCommentParserTest.kt), with other
		// names, in HTML.
		{
			name: "P1 header parsed into author, time and body", author: issueSync,
			in:      "<p>" + h1 + "</p><p>Dobrý den, posílám podklady.</p>",
			author2: "Jana Dvořáková", created: utc(2026, 6, 10, 8, 54), via: "Issue Sync",
			text: "Dobrý den, posílám podklady.", html: "<p>Dobrý den, posílám podklady.</p>", changed: true,
		},
		{
			name: "P2 real sample: two spaces, GMT+2, hyphen, <br/> lines", rules: &hyphenRules, author: hyphenBot,
			in: "<p>ITSD-19 ACME Jana DVOŘÁKOVÁ added  comment - 10/06/26 14:39 GMT+2<br/>\nDobrý den,<br/>\n" +
				"moc děkujeme a schvalujeme.<br/>\nS pozdravem<br/>\nDvořáková</p>",
			author2: "ACME Jana DVOŘÁKOVÁ", created: utc(2026, 6, 10, 12, 39), via: "Issue Sync",
			text: "Dobrý den,\nmoc děkujeme a schvalujeme.\nS pozdravem\nDvořáková",
			html: "<p>Dobrý den,<br/>\nmoc děkujeme a schvalujeme.<br/>\nS pozdravem<br/>\nDvořáková</p>", changed: true,
		},
		{
			name: "P3 company prefix stripped", rules: &withPrefix, author: issueSync,
			in:      "<p>ITSD-19 ACME Jana DVOŘÁKOVÁ added  comment - 10/06/26 14:39 GMT+2</p><p>Dobrý den</p>",
			author2: "Jana DVOŘÁKOVÁ", created: utc(2026, 6, 10, 12, 39), via: "Issue Sync",
			text: "Dobrý den", html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "P4 prefix stripped only as a whole word", rules: &withPrefix, author: issueSync,
			in:      "<p>" + header("WEB-1", "ACMEX Tým", "10/06/26 10:00 AM CET") + "<br/>ahoj</p>",
			author2: "ACMEX Tým", created: utc(2026, 6, 10, 8, 0), via: "Issue Sync",
			text: "ahoj", html: "<p>ahoj</p>", changed: true,
		},
		{
			name: "P5 no prefixes: author whole", author: issueSync,
			in:      "<p>" + header("ITSD-19", "ACME Jana DVOŘÁKOVÁ", "10/06/26 14:39 GMT+2") + "</p><p>Dobrý den</p>",
			author2: "ACME Jana DVOŘÁKOVÁ", created: utc(2026, 6, 10, 12, 39), via: "Issue Sync",
			text: "Dobrý den", html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "P6 configured en dash matches an author with a hyphen", author: hyphenBot,
			in:      "<p>" + header("WEB-1", "Tomáš Král", "10/06/26 10:54 AM CEST") + "<br/>ahoj</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 8, 54), via: "Issue Sync",
			text: "ahoj", html: "<p>ahoj</p>", changed: true,
		},
		{
			name: "P7 a number over 12 swaps day and month; CET in summer", author: issueSync,
			in:      "<p>" + header("WEB-1", "Tomáš Král", "06/15/26 9:05 PM CET") + "</p><p>text</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 15, 19, 5), via: "Issue Sync",
			text: "text", html: "<p>text</p>", changed: true,
		},
		{
			name: "P8 unreadable date: fallback time, author taken", author: issueSync,
			in:      "<p>" + header("WEB-1", "Tomáš Král", "dnes ráno") + "</p><p>text zprávy</p>",
			author2: "Tomáš Král", created: fallback, via: "Issue Sync",
			text: "text zprávy", html: "<p>text zprávy</p>", changed: true,
		},
		{
			name: "P9 bot comment without a header unchanged", author: issueSync,
			in:   "<p>Obyčejná zpráva bota bez hlavičky.</p>",
			text: "Obyčejná zpráva bota bez hlavičky.",
		},
		{
			name: "P10 someone else's header-like comment unchanged", author: "Karel Veselý",
			in:   "<p>" + h1 + "<br/>ahoj</p>",
			text: h1 + "\nahoj",
		},
		{
			name: "P11 metadata line removed from everyone's comment", author: "Eva Horáková",
			in:   "<p>Příloha nyní prošla.<br/>Remote comment create date: 10/02/26 12:28 PM CET</p>",
			text: "Příloha nyní prošla.", html: "<p>Příloha nyní prošla.</p>", changed: true,
		},
		{
			// The prototype dropped a lone "[příloha]" marker line; in HTML a
			// picture is an element and no line is dropped for it.
			name: "P12 leading attachment-like line kept", author: "Karel Veselý",
			in:   "<p>[příloha]</p><p>Tady je dokument, o kterém jsme mluvili.</p>",
			text: "[příloha]\nTady je dokument, o kterém jsme mluvili.",
		},
		{
			name: "P12b leading picture kept", author: "Karel Veselý",
			in:   `<p><img src="cid:a"/></p><p>Tady je dokument, o kterém jsme mluvili.</p>`,
			text: "Tady je dokument, o kterém jsme mluvili.",
		},
		{
			name: "P13 marker inside a sentence kept", author: "Karel Veselý",
			in:   "<p>Viz [příloha] v předchozí zprávě.</p>",
			text: "Viz [příloha] v předchozí zprávě.",
		},
		{
			name: "P14 header and metadata cleaned together", author: issueSync,
			in: "<p>" + h1 + "</p><p>Dobrý den,<br/>v příloze posílám smlouvu.</p>" +
				"<p>Remote comment create date: 10/06/26 10:54 AM CEST</p>",
			author2: "Jana Dvořáková", created: utc(2026, 6, 10, 8, 54), via: "Issue Sync",
			text: "Dobrý den,\nv příloze posílám smlouvu.",
			html: "<p>Dobrý den,<br/>v příloze posílám smlouvu.</p>", changed: true,
		},
		{
			name: "P15 header only: kept, still re-attributed", author: issueSync,
			in:      "<p>" + h1 + "</p>",
			author2: "Jana Dvořáková", created: utc(2026, 6, 10, 8, 54), via: "Issue Sync",
			text: h1, changed: true,
		},
		// P16 (an invalid filter) is TestInvalidFilters.

		// Dates.
		{
			name: "lowercase pm and zone", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 9:05 pm cest") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 19, 5), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "AMT is an unknown zone, not AM: fallback location", author: issueSync, fallback: plus3,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:54 AMT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 7, 54), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "+530 is five and a half hours", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:54 +530") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 5, 24), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "UTC-5", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:54 UTC-5") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 15, 54), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "+02:00 and a four-digit year", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/2026 10:54 +02:00") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 8, 54), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "31/02 does not exist: fallback time", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "31/02/26 10:00 GMT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: fallback, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "05/06/26 is dd/MM", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "05/06/26 10:00 GMT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 5, 10, 0), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "31/31/99: fallback time", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "31/31/99 10:00") + "</p><p>x</p>",
			author2: "Tomáš Král", created: fallback, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "24:00: fallback time", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 24:00 GMT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: fallback, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "offset +19: fallback time", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:00 +19") + "</p><p>x</p>",
			author2: "Tomáš Král", created: fallback, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "13:00 PM stays 13:00", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 13:00 PM GMT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 13, 0), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "12:30 AM is past midnight", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 12:30 AM GMT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 0, 30), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "Europe/Prague is no abbreviation: fallback location", author: issueSync, fallback: plus3,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:54 Europe/Prague") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 7, 54), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "CET in winter is +1", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/02/26 12:28 PM CET") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 2, 10, 11, 28), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "CEST in winter is still +2", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/02/26 12:28 PM CEST") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 2, 10, 10, 28), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "PDT from the table", author: issueSync,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:00 AM PDT") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 17, 0), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "no zone: fallback location", author: issueSync, fallback: plus3,
			in:      "<p>" + header("WEB-2", "Tomáš Král", "10/06/26 10:00") + "</p><p>x</p>",
			author2: "Tomáš Král", created: utc(2026, 6, 10, 7, 0), via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},

		// Names and bots.
		{
			name: "blank author: no header", author: issueSync,
			in:   "<p>ITSD-1  added comment - 10/06/26 10:00 GMT</p><p>x</p>",
			text: "ITSD-1 added comment - 10/06/26 10:00 GMT\nx",
		},
		{
			name: "author of invisible characters only: no header", author: issueSync,
			in:   "<p>ITSD-1 " + zwsp + rlo + lrm + " added comment - 10/06/26 10:00 GMT</p><p>x</p>",
			text: "ITSD-1 added comment - 10/06/26 10:00 GMT\nx",
		},
		{
			name: "a person called Sync is no bot", author: "Sync",
			in: "<p>" + hGMT + "</p><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "a person called Jo is no bot", author: "Jo",
			in: "<p>" + hGMT + "</p><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "a short bot name matches only whole", rules: &shortBot, author: "Jo Nováková",
			in: "<p>" + hGMT + "</p><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "a short bot name matches itself", rules: &shortBot, author: "jo",
			in:      "<p>" + hGMT + "</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "jo", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "configured name contained in the author's", rules: &containedBot, author: issueSync + " (Acme)",
			in:      "<p>" + hGMT + "</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "containment only of whole words", rules: &wordBot, author: "Synchronizer Bot",
			in: "<p>" + hGMT + "</p><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "the bot's display name with zero-width and soft hyphen", author: "Issue" + zwsp + " Sync – Synchroni" + shy + "zation for Jira",
			in:      "<p>" + hGMT + "</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "RTL override in the name: author clean, body keeps it", author: issueSync,
			in:      "<p>ITSD-1 Jana " + rlo + "Dvořáková" + pdf + " added comment - 10/06/26 10:00 GMT</p><p>Dobrý den " + rlo + "abc</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync",
			text: "Dobrý den abc", html: "<p>Dobrý den " + rlo + "abc</p>", changed: true,
		},
		{
			name: "marks, zero-width and BOM do not break the header", author: issueSync,
			in:      "<p>" + bom + "ITSD-1 " + lrm + "Jana Dvořáková" + lrm + " added" + zwsp + " comment - 10/06/26 10:00 GMT</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "control characters dropped from the name", author: issueSync,
			in:      "<p>ITSD-1 Jana\x01\x7f Dvořáková added comment - 10/06/26 10:00 GMT</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "no-break spaces are spaces", author: issueSync,
			in:      "<p>ITSD-1&nbsp;Jana&nbsp;Dvořáková added&nbsp;comment&nbsp;-&nbsp;10/06/26&nbsp;10:00&nbsp;GMT</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "entities in the header", author: issueSync,
			in:      "<p>ITSD-1 Jana &amp; Tom&aacute;&scaron; added comment &#45; 10/06/26 10:00 GMT</p><p>x</p>",
			author2: "Jana & Tomáš", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "en dash before the date", author: issueSync,
			in:      "<p>ITSD-1 Jana Dvořáková added comment – 10/06/26 10:00 GMT</p><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x", html: "<p>x</p>", changed: true,
		},
		{
			name: "Cyrillic o in comment: no header", author: issueSync,
			in:   "<p>ITSD-1 Jana Dvořáková added c" + cyrO + "mment - 10/06/26 10:00 GMT</p><p>x</p>",
			text: "ITSD-1 Jana Dvořáková added c" + cyrO + "mment - 10/06/26 10:00 GMT\nx",
		},
		{
			name: "non-ASCII digit in the key: no header", author: issueSync,
			in:   "<p>ITSD-" + arab1 + " Jana Dvořáková added comment - 10/06/26 10:00 GMT</p><p>x</p>",
			text: "ITSD-" + arab1 + " Jana Dvořáková added comment - 10/06/26 10:00 GMT\nx",
		},
		{
			name: "lowercase key: no header", author: issueSync,
			in:   "<p>itsd-1 Jana Dvořáková added comment - 10/06/26 10:00 GMT</p><p>x</p>",
			text: "itsd-1 Jana Dvořáková added comment - 10/06/26 10:00 GMT\nx",
		},
		{
			name: "date part over 64 bytes: no header", author: issueSync,
			in:   "<p>" + header("ITSD-1", "Jana Dvořáková", "10/06/26 10:00 GMT "+strings.Repeat("a", 60)) + "</p><p>x</p>",
			text: header("ITSD-1", "Jana Dvořáková", "10/06/26 10:00 GMT "+strings.Repeat("a", 60)) + "\nx",
		},
		{
			name: "header on the second line: none", author: issueSync,
			in: "<p>Dobrý den</p><p>" + hGMT + "</p>", text: "Dobrý den\n" + hGMT,
		},

		// Where the header is.
		{
			name: "in <pre>: quoted, left alone", author: issueSync,
			in: "<pre>" + hGMT + "\nDobrý den</pre>", text: hGMT + "\nDobrý den",
		},
		{
			name: "in a table: left alone", author: issueSync,
			in: "<table><tbody><tr><td>" + hGMT + "</td></tr></tbody></table><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "in a list: left alone", author: issueSync,
			in: "<ul><li>" + hGMT + "</li></ul><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "in a quote: left alone", author: issueSync,
			in: "<blockquote><p>" + hGMT + "</p></blockquote><p>x</p>", text: hGMT + "\nx",
		},
		{
			name: "in panel divs", author: issueSync,
			in:      `<div class="panel"><div class="panelContent"><p>` + hGMT + `</p><p>Dobrý den</p></div></div>`,
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: `<div class="panel"><div class="panelContent"><p>Dobrý den</p></div></div>`, changed: true,
		},
		{
			name: "bold header across inline elements", author: issueSync,
			in:      `<p><strong>ITSD-1 <a href="https://acme.example/u/1">Jana Dvořáková</a> added comment</strong> - 10/06/26 10:00 GMT<br/>Dobrý den</p>`,
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "CRLF inside the text ends the line", author: issueSync,
			in:      "<p>" + hGMT + "\r\nDobrý den</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "a lone CR ends the line", author: issueSync,
			in:      "<p>" + hGMT + "\rDobrý den</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "picture before the header kept", author: issueSync,
			in:      `<p><img src="cid:x"/></p><p>` + hGMT + `</p><p>Dobrý den</p>`,
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: `<p><img src="cid:x"/></p><p>Dobrý den</p>`, changed: true,
		},
		{
			name: "picture in the header line kept", author: issueSync,
			in:      `<p><img src="cid:x"/>` + hGMT + `<br/>Dobrý den</p>`,
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: `<p><img src="cid:x"/>Dobrý den</p>`, changed: true,
		},
		{
			name: "style and comment nodes stay", author: issueSync,
			in:      "<style>p{color:red}</style><!-- sync:42 --><p>" + hGMT + "</p><p>Dobrý den</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<style>p{color:red}</style><!-- sync:42 --><p>Dobrý den</p>", changed: true,
		},
		{
			name: "a style is not content: header-only comment kept", author: issueSync,
			in:      "<style>p{}</style><p>" + hGMT + "</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: hGMT, changed: true,
		},
		{
			name: "whitespace between blocks trimmed with the removed lines", author: issueSync,
			in:      "<p>" + h1 + "</p>\n\n<p>Dobrý den</p>\n\n<p>Remote comment create date: 10/06/26 10:54 AM CEST</p>\n",
			author2: "Jana Dvořáková", created: utc(2026, 6, 10, 8, 54), via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "blank lines around removed ones in a paragraph", author: issueSync,
			in:      "<p>" + hGMT + "<br/><br/>Dobrý den<br/><br/>Remote comment create date: x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "a blank paragraph after the header goes", author: issueSync,
			in:      "<p>" + hGMT + "</p><p>&nbsp;</p><p><br/></p><p>Dobrý den</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "metadata between two lines leaves the lines apart", author: "Karel Veselý",
			in:   "<p>A<br/><br/>Remote comment create date: x<br/><br/>B</p>",
			text: "A\nB", html: "<p>A<br/><br/><br/>B</p>", changed: true,
		},
		{
			name: "metadata in a table is content", author: "Karel Veselý",
			in:   "<table><tbody><tr><td>Remote comment create date: x</td></tr></tbody></table><p>A</p>",
			text: "Remote comment create date: x\nA",
		},

		// Never empty.
		{
			name: "a filter matching everything removes nothing", rules: &matchAll, author: "Karel Veselý",
			in: "<p>a</p><p>b</p>", text: "a\nb",
		},
		{
			name: "a filter matching everything still lets the header go", rules: &matchAll, author: issueSync,
			in:      "<p>" + hGMT + "</p><p>Dobrý den</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "Dobrý den",
			html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "filtering the only body line keeps the header", rules: &dropsBody, author: issueSync,
			in:      "<p>" + hGMT + "</p><p>Dobrý den</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: hGMT,
			html: "<p>" + hGMT + "</p>", changed: true,
		},
		{
			name: "filters run first: a filtered header is no header", rules: &dropsHeader, author: issueSync,
			in:   "<p>" + hGMT + "</p><p>Dobrý den</p>",
			text: "Dobrý den", html: "<p>Dobrý den</p>", changed: true,
		},
		{
			name: "only a picture left is content", author: issueSync,
			in:      `<p>` + hGMT + `</p><p><img src="cid:x"/></p>`,
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync",
			html: `<p><img src="cid:x"/></p>`, changed: true,
		},

		// Odd input.
		{name: "empty input", author: issueSync, in: ""},
		{name: "only a comment node", author: issueSync, in: "<!-- x -->"},
		{
			name: "document tags ignored", author: issueSync,
			in:      "<html><head><title>t</title></head><body><p>" + hGMT + "</p><p>x</p></body></html>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "x",
			html: "<title>t</title><p>x</p>", changed: true,
		},
		{
			name: "noscript content stays as it came", author: issueSync,
			in:      "<p>" + hGMT + "</p><noscript>&lt;b&gt;x</noscript><p>x</p>",
			author2: "Jana Dvořáková", created: jana1000, via: "Issue Sync", text: "&lt;b&gt;x\nx",
			html: "<noscript>&lt;b&gt;x</noscript><p>x</p>", changed: true,
		},
		{
			name: "svg is refused", author: issueSync,
			in: "<p>" + hGMT + "</p><svg><text>x</text></svg><p>x</p>",
		},
	}
}

func TestClean(t *testing.T) {
	for _, tc := range cleanCases() {
		t.Run(tc.name, func(t *testing.T) {
			rules := defaultRules()
			if tc.rules != nil {
				rules = *tc.rules
			}
			fb := tc.fallback
			if fb.IsZero() {
				fb = fallback
			}
			got := mustCompile(t, rules).Clean(tc.author, tc.in, fb)
			if got.AuthorName != tc.author2 {
				t.Errorf("AuthorName = %q, want %q", got.AuthorName, tc.author2)
			}
			if !got.Created.Equal(tc.created) || got.Created.IsZero() != tc.created.IsZero() {
				t.Errorf("Created = %v, want %v", got.Created, tc.created)
			}
			if got.Via != tc.via {
				t.Errorf("Via = %q, want %q", got.Via, tc.via)
			}
			if got.Text != tc.text {
				t.Errorf("Text = %q, want %q", got.Text, tc.text)
			}
			want := tc.html
			if want == "" {
				want = tc.in
			}
			if got.HTML != want {
				t.Errorf("HTML = %q, want %q", got.HTML, want)
			}
			if got.Changed != tc.changed {
				t.Errorf("Changed = %v, want %v", got.Changed, tc.changed)
			}
		})
	}
}

// P16: an invalid filter is reported and skipped; the rest works.
func TestInvalidFilters(t *testing.T) {
	c, errs := Compile(Rules{
		BotNames: []string{issueSync},
		MetadataFilters: []string{
			"[invalid",
			`(?<=x)y`,   // lookbehind: Java, not RE2
			`(a)\1`,     // backreference
			`a{2,1}`,    // bad repeat
			`x++`,       // possessive
			`^Sent .*$`, // valid
			`\Qa.b`,     // valid: \Q to the end, a pattern ^(?:…)$ could not wrap
		},
	})
	if len(errs) != 5 {
		t.Fatalf("errors = %d (%v), want 5", len(errs), errs)
	}
	for i, err := range errs {
		if !strings.HasPrefix(err.Error(), "metadata filter ") {
			t.Errorf("error %d = %q", i, err)
		}
	}
	if got := c.Clean("Karel", "<p>text</p>", fallback); got.Changed || got.HTML != "<p>text</p>" || got.Text != "text" {
		t.Errorf("Clean = %+v, want the input", got)
	}
	got := c.Clean("Karel", "<p>text<br/>Sent from my phone<br/>a.b</p>", fallback)
	if got.HTML != "<p>text</p>" {
		t.Errorf("HTML = %q, want the two filtered lines gone", got.HTML)
	}
}

func TestCompileLimits(t *testing.T) {
	many := make([]string, MaxEntries+3)
	for i := range many {
		many[i] = "bot " + strings.Repeat("x", i)
	}
	long := strings.Repeat("a", MaxEntryBytes+1)
	c, errs := Compile(Rules{
		BotNames:        append([]string{long}, many...),
		MetadataFilters: []string{long, "", "ok"},
		AuthorPrefixes:  []string{long, "  ", "ACME"},
	})
	if len(errs) != 4 {
		t.Fatalf("errors = %v, want 4 (too many bots, three too long)", errs)
	}
	if len(c.bots) != MaxEntries-1 { // the long one is among the first 32
		t.Errorf("bots = %d, want %d", len(c.bots), MaxEntries-1)
	}
	if len(c.filters) != 1 || len(c.prefixes) != 1 {
		t.Errorf("filters = %d, prefixes = %d, want 1 and 1", len(c.filters), len(c.prefixes))
	}
}

func TestKey(t *testing.T) {
	key := func(r Rules) string {
		c, _ := Compile(r)
		return c.Key()
	}
	base := Rules{
		BotNames:        []string{"Issue Sync", "Other Bot"},
		MetadataFilters: []string{"^a$", "^b$"},
		AuthorPrefixes:  []string{"ACME", "IT"},
	}
	k := key(base)
	if len(k) != 32 {
		t.Fatalf("Key = %q, want 32 hex digits", k)
	}
	same := []Rules{
		{BotNames: []string{"other  bot", "ISSUE SYNC", "Issue Sync"}, MetadataFilters: []string{"^b$", "^a$", "^a$", "[bad"}, AuthorPrefixes: []string{"ACME", "IT"}},
	}
	for _, r := range same {
		if key(r) != k {
			t.Errorf("Key(%+v) differs from an equivalent set", r)
		}
	}
	different := []Rules{
		{BotNames: base.BotNames, MetadataFilters: base.MetadataFilters, AuthorPrefixes: []string{"IT", "ACME"}},
		{BotNames: base.BotNames, MetadataFilters: []string{"^a$"}, AuthorPrefixes: base.AuthorPrefixes},
		{BotNames: []string{"Issue Sync"}, MetadataFilters: base.MetadataFilters, AuthorPrefixes: base.AuthorPrefixes},
		{},
	}
	for _, r := range different {
		if key(r) == k {
			t.Errorf("Key(%+v) equals the base set's", r)
		}
	}
	var nilRules *Compiled
	if nilRules.Key() != key(Rules{}) {
		t.Error("a nil *Compiled has another key than no rules")
	}
	if key(base) != k {
		t.Error("Key is not stable")
	}
}

func TestNilCompiled(t *testing.T) {
	var c *Compiled
	in := "<p>" + header("ITSD-1", "Jana Dvořáková", "10/06/26 10:00 GMT") + "</p><p>x</p>"
	got := c.Clean(issueSync, in, fallback)
	if got.Changed || got.HTML != in || got.AuthorName != "" {
		t.Errorf("Clean with no rules = %+v", got)
	}
}

func TestStripPrefixes(t *testing.T) {
	c := mustCompile(t, Rules{AuthorPrefixes: []string{"acme", "  IT  Team ", "Ž"}})
	for in, want := range map[string]string{
		"ACME Jana":              "Jana",
		"Acme IT Team Jana":      "Jana",
		"ACMEX Jana":             "ACMEX Jana",
		"ACME":                   "ACME",
		"IT Team":                "IT Team",
		"ž Jana":                 "Jana",
		"Jana ACME":              "Jana ACME",
		"ACME ACME Jana":         "ACME Jana", // each prefix once, in order
		"ACME " + rlo + "Jana":   "Jana",      // names are in visible form before this
		"it team Tomáš Král":     "Tomáš Král",
		"IT Teamwork Tomáš Král": "IT Teamwork Tomáš Král",
	} {
		name, _ := visible(in, 0)
		if got := c.stripPrefixes(name); got != want {
			t.Errorf("stripPrefixes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortName(t *testing.T) {
	for in, want := range map[string]string{
		issueSync:                               "Issue Sync",
		"Issue Sync - Synchronization for Jira": "Issue Sync",
		"Issue Sync — Synchronization":          "Issue Sync",
		"Backbone Issue Sync (Acme)":            "Backbone Issue Sync",
		"(bot)":                                 "(bot)",
		" Exalate ":                             "Exalate",
		"Issue" + rlo + " Sync – x":             "Issue Sync",
	} {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Large and deep input: returned whole or cleaned, fast, never lost.
func TestHostileSizes(t *testing.T) {
	c := mustCompile(t, defaultRules())
	hdr := "<p>" + header("ITSD-1", "Jana Dvořáková", "10/06/26 10:00 GMT") + "</p>"
	jana := utc(2026, 6, 10, 10, 0)

	t.Run("over MaxInputBytes: unchanged", func(t *testing.T) {
		in := hdr + "<p>" + strings.Repeat("a", MaxInputBytes) + "</p>"
		got := c.Clean(issueSync, in, fallback)
		if got.Changed || got.HTML != in || got.Text != "" || got.AuthorName != "" {
			t.Errorf("Changed=%v AuthorName=%q len(Text)=%d", got.Changed, got.AuthorName, len(got.Text))
		}
	})
	t.Run("a line of almost 1 MiB after the header", func(t *testing.T) {
		long := strings.Repeat("a", MaxInputBytes-len(hdr)-100)
		in := hdr + "<p>" + long + "</p>"
		start := time.Now()
		got := c.Clean(issueSync, in, fallback)
		if got.HTML != "<p>"+long+"</p>" || got.AuthorName != "Jana Dvořáková" || !got.Created.Equal(jana) || got.Text != long {
			t.Errorf("AuthorName=%q Created=%v len(HTML)=%d", got.AuthorName, got.Created, len(got.HTML))
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("took %v", d)
		}
	})
	t.Run("a header-like first line of almost 1 MiB is no header", func(t *testing.T) {
		in := "<p>ITSD-1 " + strings.Repeat("Jana ", (MaxInputBytes-200)/5) + "added comment - 10/06/26 10:00 GMT</p><p>x</p>"
		got := c.Clean(issueSync, in, fallback)
		if got.Changed || got.HTML != in {
			t.Errorf("Changed=%v", got.Changed)
		}
	})
	t.Run("10 000 nested divs: refused by the parser, unchanged", func(t *testing.T) {
		in := strings.Repeat("<div>", 10_000) + hdr + "<p>x</p>" + strings.Repeat("</div>", 10_000)
		got := c.Clean(issueSync, in, fallback)
		if got.Changed || got.HTML != in || got.Text != "" {
			t.Errorf("Changed=%v len(Text)=%d", got.Changed, len(got.Text))
		}
	})
	t.Run("400 nested divs: cleaned", func(t *testing.T) {
		in := strings.Repeat("<div>", 400) + hdr + "<p>x</p>" + strings.Repeat("</div>", 400)
		got := c.Clean(issueSync, in, fallback)
		want := strings.Repeat("<div>", 400) + "<p>x</p>" + strings.Repeat("</div>", 400)
		if got.HTML != want || got.AuthorName != "Jana Dvořáková" {
			t.Errorf("AuthorName=%q HTML=%.80q…", got.AuthorName, got.HTML)
		}
	})
	t.Run("entity bomb", func(t *testing.T) {
		in := hdr + "<p>" + strings.Repeat("&amp;", 200_000) + strings.Repeat("&#x10FFFF;&#0;&#xD800;&nbsp", 1000) + "</p>"
		got := c.Clean(issueSync, in, fallback)
		if got.AuthorName != "Jana Dvořáková" || !strings.HasPrefix(got.Text, strings.Repeat("&", 200_000)) {
			t.Errorf("AuthorName=%q Text=%.40q…", got.AuthorName, got.Text)
		}
		if !utf8.ValidString(got.HTML) {
			t.Error("HTML is not UTF-8")
		}
	})
	t.Run("misnested formatting that would inflate: unchanged", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(hdr + "<p>")
		for i := 0; i < 400; i++ {
			b.WriteString("<b class=c" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + ">")
		}
		b.WriteString(strings.Repeat("x<p>", 5000))
		in := b.String()
		start := time.Now()
		got := c.Clean(issueSync, in, fallback)
		if got.Changed || got.HTML != in || got.Text != "" {
			t.Errorf("Changed=%v", got.Changed)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("took %v", d)
		}
	})
	t.Run("well-nested formatting is not refused", func(t *testing.T) {
		in := hdr + "<p>" + strings.Repeat("<b><i>x</i></b> <a href=\"https://acme.example/\">y</a><br/>", 5000) + "</p>"
		got := c.Clean(issueSync, in, fallback)
		if got.AuthorName != "Jana Dvořáková" || !got.Changed {
			t.Errorf("AuthorName=%q Changed=%v", got.AuthorName, got.Changed)
		}
	})
	t.Run("too many lines: unchanged, text given", func(t *testing.T) {
		in := hdr + "<p>" + strings.Repeat("a<br/>", maxLines+10) + "</p>"
		got := mustCompile(t, Rules{BotNames: []string{issueSync}, MetadataFilters: []string{"^a$"}}).Clean(issueSync, in, fallback)
		if got.Changed || got.HTML != in || strings.Count(got.Text, "\n") != maxLines+10 {
			t.Errorf("Changed=%v lines=%d", got.Changed, strings.Count(got.Text, "\n"))
		}
	})
	t.Run("many small lines", func(t *testing.T) {
		in := hdr + "<p>" + strings.Repeat("a<br/>Remote comment create date: x<br/>", 5000) + "</p>"
		start := time.Now()
		got := c.Clean(issueSync, in, fallback)
		if want := "<p>" + strings.Repeat("a<br/>", 4999) + "a</p>"; got.HTML != want {
			t.Errorf("HTML=%.80q…", got.HTML)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("took %v", d)
		}
	})
}

func TestInvalidUTF8(t *testing.T) {
	c := mustCompile(t, defaultRules())
	in := "<p>ITSD-1 Jana \xff\xfe Dvořáková added comment - 10/06/26 10:00 GMT</p><p>x\xc3</p>"
	got := c.Clean(issueSync, in, fallback)
	if !utf8.ValidString(got.AuthorName) || got.AuthorName == "" {
		t.Errorf("AuthorName = %q", got.AuthorName)
	}
	if got.HTML == "" {
		t.Error("HTML empty")
	}
}

// Clean is safe for concurrent use.
func TestConcurrent(t *testing.T) {
	c := mustCompile(t, defaultRules())
	in := "<p>" + header("ITSD-1", "Jana Dvořáková", "10/06/26 10:00 GMT") + "</p><p>x</p>"
	done := make(chan Result)
	for range 8 {
		go func() { done <- c.Clean(issueSync, in, fallback) }()
	}
	for range 8 {
		if got := <-done; got.HTML != "<p>x</p>" {
			t.Errorf("HTML = %q", got.HTML)
		}
	}
}

func TestInflates(t *testing.T) {
	var open strings.Builder
	for i := 0; i < 300; i++ {
		open.WriteString("<b id=b" + strings.Repeat("x", i) + ">")
	}
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"misnested formatting re-opened at every paragraph", "<p>" + open.String() + strings.Repeat("x<p>", 400), true},
		{"the same closed properly", "<p>" + open.String() + "x" + strings.Repeat("</b>", 300) + strings.Repeat("<p>x</p>", 400), false},
		{"well-nested formatting", strings.Repeat("<p><b><i>x</i></b> <a href=\"/\">y</a></p>", 5000), false},
		{"end tags inside a comment do not count", "<p>" + open.String() + "<!--" + strings.Repeat("</b>", 300) + "-->" + strings.Repeat("x<p>", 400), true},
		{"end tags inside a script do not count", "<p>" + open.String() + "<script>" + strings.Repeat("</b>", 300) + "</script>" + strings.Repeat("x<p>", 400), true},
		{"svg", "<p>x</p><svg></svg>", true},
		{"MATH in capitals", "<p>x</p><MATH></MATH>", true},
		{"svg as text only", "<p>x &lt;svg</p>", false},
		{"plain text", "Dobrý den", false},
		{"empty", "", false},
	} {
		if got := inflates(tc.in); got != tc.want {
			t.Errorf("%s: inflates = %v, want %v", tc.name, got, tc.want)
		}
	}
}
