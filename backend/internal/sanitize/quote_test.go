// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// trimmed sanitises src for the view with TrimQuoted.
func trimmed(t testing.TB, src string) Output {
	t.Helper()
	in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: fuzzKnown, TrimQuoted: true}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatalf("sanitise: %v", err)
	}
	checkClean(t, in, out)
	return out
}

func TestTrimQuotedHTML(t *testing.T) {
	cases := []struct {
		name string
		src  string
		trim bool
		keep []string // in the output text
		gone []string // not in the output HTML
	}{
		{
			name: "gmail",
			src:  `<div dir="ltr">See you.</div><br><div class="gmail_quote"><div dir="ltr" class="gmail_attr">On Wed, Bob &lt;b@x&gt; wrote:<br></div><blockquote class="gmail_quote">Lunch?</blockquote></div>`,
			trim: true, keep: []string{"See you."}, gone: []string{"Lunch?", "wrote", "<br/>"},
		},
		{
			name: "gmail container",
			src:  `<div>Ok.</div><div class="gmail_quote gmail_quote_container"><div class="gmail_attr">Am Mi., 1. Okt. 2026 um 10:00 Uhr schrieb Bob &lt;b@x&gt;:</div><blockquote class="gmail_quote">Frage</blockquote></div>`,
			trim: true, keep: []string{"Ok."}, gone: []string{"Frage", "schrieb"},
		},
		{
			name: "gmail attr outside the quote div",
			src:  `<p>Done.</p><div class="gmail_attr">Dne st 1. 10. 2026 v 10:00 odesílatel Bob &lt;b@x&gt; napsal:</div><div class="gmail_quote">Hotovo?</div>`,
			trim: true, keep: []string{"Done."}, gone: []string{"napsal", "Hotovo"},
		},
		{
			name: "apple mail",
			src:  `Thanks!<div><br></div><div>Carol<div><br><blockquote type="cite"><div>On 30. 9. 2026, at 20:00, Dave wrote:</div><div>Photos</div></blockquote></div><br></div>`,
			trim: true, keep: []string{"Thanks!", "Carol"}, gone: []string{"Photos", "Dave"},
		},
		{
			name: "thunderbird",
			src:  `<p>Fine by me.</p><div class="moz-cite-prefix">Dne 30. 09. 26 v 10:00 Bob napsal(a):<br></div><blockquote type="cite" cite="mid:x@y">Proposal</blockquote><br>`,
			trim: true, keep: []string{"Fine by me."}, gone: []string{"napsal", "Proposal"},
		},
		{
			name: "inline attribution text",
			src:  `<div>Agreed.<br><br>On Tue, Bob &lt;<a href="mailto:b@x">b@x</a>&gt; wrote:<br><blockquote type="cite">Plan</blockquote></div>`,
			trim: true, keep: []string{"Agreed."}, gone: []string{"wrote", "Plan", "mailto"},
		},
		{
			name: "plain blockquote after attribution",
			src:  `<p>Yes.</p><p>Le mar. 30 sept. 2026 à 10:00, Bob a écrit :</p><blockquote style="margin:0 0 0 .8ex">Question</blockquote>`,
			trim: true, keep: []string{"Yes."}, gone: []string{"écrit", "Question"},
		},
		{
			name: "plain blockquote without attribution",
			src:  `<p>As the poet says:</p><blockquote>Words.</blockquote>`,
			trim: false, keep: []string{"Words."},
		},
		{
			name: "owa",
			src:  `<div>Thursday works.</div><div id="appendonsend"></div><hr style="display:inline-block;width:98%"><div id="divRplyFwdMsg"><b>From:</b> F<br><b>Sent:</b> Wed<br><b>To:</b> E</div><div>Can we meet?</div>`,
			trim: true, keep: []string{"Thursday works."}, gone: []string{"Can we meet", "<hr", "From:"},
		},
		{
			name: "owa without appendonsend",
			src:  `<div>Ok.</div><hr><div id="divRplyFwdMsg">From: F</div><div>Older</div>`,
			trim: true, keep: []string{"Ok."}, gone: []string{"Older", "<hr"},
		},
		{
			name: "new outlook container",
			src:  `<div>Ano.</div><div id="mail-editor-reference-message-container"><div>Od: X</div><div>Starší</div></div>`,
			trim: true, keep: []string{"Ano."}, gone: []string{"Starší"},
		},
		{
			name: "outlook desktop cs, border-top div",
			src: `<div class="WordSection1"><p class="MsoNormal">Díky.<o:p></o:p></p><p class="MsoNormal"><o:p>&nbsp;</o:p></p>` +
				`<div><div style="border:none;border-top:solid #E1E1E1 1.0pt;padding:3.0pt 0cm 0cm 0cm"><p class="MsoNormal"><b>Od:</b> Petr<br><b>Odesláno:</b> středa<br><b>Komu:</b> Jan<br><b>Předmět:</b> Faktura</p></div></div>` +
				`<p class="MsoNormal">&nbsp;</p><p class="MsoNormal">Původní text</p></div>`,
			trim: true, keep: []string{"Díky."}, gone: []string{"Původní text", "Odesláno", "border-top", "&nbsp;"},
		},
		{
			name: "outlook desktop de, hr",
			src: `<p>Danke.</p><div class="MsoNormal" align="center" style="text-align:center"><hr size="2" width="100%" align="center"></div>` +
				`<p class="MsoNormal"><b><span>Von:</span></b><span> A<br><b>Gesendet:</b> Mittwoch<br><b>An:</b> B<br><b>Betreff:</b> C</span></p><p>Alt</p>`,
			trim: true, keep: []string{"Danke."}, gone: []string{"Alt", "Gesendet", "<hr"},
		},
		{
			name: "outlook fr labels with a space before the colon",
			src:  `<p>Merci.</p><hr><p><b>De :</b> A<br><b>Envoyé :</b> mercredi<br><b>À :</b> B<br><b>Objet :</b> C</p><p>Ancien</p>`,
			trim: true, keep: []string{"Merci."}, gone: []string{"Ancien"},
		},
		{
			name: "outlook es, it, nl, pl, sk",
			src:  `<p>Gracias.</p><hr><p><b>De:</b> A<br><b>Enviado el:</b> miércoles<br><b>Para:</b> B<br><b>Asunto:</b> C</p><p>Viejo</p>`,
			trim: true, keep: []string{"Gracias."}, gone: []string{"Viejo"},
		},
		{
			name: "outlook header with bold by style",
			src:  `<p>Dzięki.</p><div style="border-top:solid #B5C4DF 1pt"><p><span style="font-weight:bold">Od:</span> A<br><span style="font-weight:bold">Wysłano:</span> środa<br><span style="font-weight:bold">Do:</span> B<br><span style="font-weight:bold">Temat:</span> C</p></div><p>Stare</p>`,
			trim: true, keep: []string{"Dzięki."}, gone: []string{"Stare"},
		},
		{
			name: "hr without a header block",
			src:  `<p>Part one</p><hr><p>Part two</p>`,
			trim: false, keep: []string{"Part two"},
		},
		{
			name: "header without bold labels",
			src:  `<p>Notes</p><hr><p>From: A<br>Sent: B<br>To: C</p><p>More</p>`,
			trim: false, keep: []string{"More"},
		},
		{
			name: "header lacking sent",
			src:  `<p>Top</p><hr><p><b>From:</b> A<br><b>To:</b> C<br><b>Subject:</b> D</p><p>More</p>`,
			trim: false, keep: []string{"More"},
		},
		{
			name: "header not starting with from",
			src:  `<p>Top</p><hr><p><b>Sent:</b> A<br><b>From:</b> B<br><b>To:</b> C</p><p>More</p>`,
			trim: false, keep: []string{"More"},
		},
		{
			name: "original message separator",
			src:  `<p>Ok, I'll do it.</p><p>-----Original Message-----<br>From: A<br>Sent: B</p><p>Old</p>`,
			trim: true, keep: []string{"Ok, I'll do it."}, gone: []string{"Original", "Old"},
		},
		{
			name: "separator in the same block as the reply",
			src:  `<div>Ano.<br><br>-----Ursprüngliche Nachricht-----<br>Von: A<br>Alt</div>`,
			trim: true, keep: []string{"Ano."}, gone: []string{"Nachricht", "Alt"},
		},
		{
			name: "separator czech, bold",
			src:  `<p>Díky.</p><p><b>-----Původní zpráva-----</b></p><p>Od: A</p>`,
			trim: true, keep: []string{"Díky."}, gone: []string{"Původní"},
		},
		{
			name: "interleaved reply",
			src:  `<div>Answers inline.</div><div class="gmail_quote"><blockquote class="gmail_quote">Q1</blockquote><div>A1</div><blockquote class="gmail_quote">Q2</blockquote><div>A2</div></div>`,
			trim: false, keep: []string{"Q1", "A2"},
		},
		{
			name: "reply below the quote",
			src:  `<blockquote type="cite">Question</blockquote><p>Answer below.</p>`,
			trim: false, keep: []string{"Question", "Answer below."},
		},
		{
			name: "interleaved, then a trailing quote",
			src:  `<p>Hi</p><blockquote type="cite">Q1</blockquote><p>A1</p><blockquote type="cite">Q2</blockquote><br><p>&nbsp;</p>`,
			trim: true, keep: []string{"Q1", "A1"}, gone: []string{"Q2"},
		},
		{
			name: "signature after the quote",
			src:  `<p>Yes</p><blockquote type="cite">Q</blockquote><p>-- <br>Jan</p>`,
			trim: false, keep: []string{"Q", "Jan"},
		},
		{
			name: "empty top",
			src:  `<div><br></div><p>&nbsp;</p><blockquote type="cite">Only the quote</blockquote>`,
			trim: false, keep: []string{"Only the quote"},
		},
		{
			name: "top of invisible characters only",
			src:  "<p>" + string(rune(zeroWidthSpace)) + string(rune(0xA0)) + string(rune(byteOrderMark)) + "</p><blockquote type=\"cite\">Quote</blockquote>",
			trim: false, keep: []string{"Quote"},
		},
		{
			name: "top is a picture",
			src:  `<img src="cid:known@x" alt="pic"><blockquote type="cite">Quote</blockquote>`,
			trim: true, gone: []string{"Quote"},
		},
		{
			name: "top is a tracking pixel only",
			src:  `<img src="https://t.example/p.gif" width="1" height="1"><blockquote type="cite">Quote</blockquote>`,
			trim: false, keep: []string{"Quote"},
		},
		{
			name: "top is text the walk drops",
			src:  `<script>var s = "visible?";</script><form><button>Click</button></form><blockquote type="cite">Quote</blockquote>`,
			trim: false, keep: []string{"Quote"},
		},
		{
			name: "forward with nothing above",
			src:  `<div dir="ltr"><br><br><div class="gmail_quote"><div class="gmail_attr">---------- Forwarded message ---------<br>From: M</div><div>Report</div></div></div>`,
			trim: false, keep: []string{"Report"},
		},
		{
			name: "forward with a note above",
			src:  `<div dir="ltr">FYI<br><br><div class="gmail_quote"><div class="gmail_attr">---------- Forwarded message ---------<br>From: M</div><div>Report</div></div></div>`,
			trim: true, keep: []string{"FYI"}, gone: []string{"Report"},
		},
		{
			name: "outlook forward with nothing above",
			src:  `<p>&nbsp;</p><div style="border:none;border-top:solid #E1E1E1 1.0pt"><p><b>From:</b> A<br><b>Sent:</b> B<br><b>To:</b> C</p></div><p>Body</p>`,
			trim: false, keep: []string{"Body"},
		},
		{
			name: "nested quotes",
			src:  `<p>Top</p><blockquote type="cite">One<blockquote type="cite">Two<blockquote type="cite">Three</blockquote></blockquote></blockquote>`,
			trim: true, keep: []string{"Top"}, gone: []string{"One", "Two", "Three"},
		},
		{
			name: "markers inside the quote",
			src:  `<p>Top</p><div class="gmail_quote">Q<div id="divRplyFwdMsg">x</div><hr><p><b>From:</b> A<br><b>Sent:</b> B<br><b>To:</b> C</p></div>`,
			trim: true, keep: []string{"Top"}, gone: []string{"Q", "divRplyFwdMsg"},
		},
		{
			name: "empty appendonsend, nothing after",
			src:  `<div>Just a message</div><div id="appendonsend"></div>`,
			trim: false, keep: []string{"Just a message"},
		},
		{
			name: "attributes in odd case",
			src:  `<P>Top</P><BLOCKQUOTE TYPE=" Cite ">Q</BLOCKQUOTE>`,
			trim: true, keep: []string{"Top"}, gone: []string{"Q"},
		},
		{
			name: "class in odd case",
			src:  `<p>Top</p><div class="x GMAIL_QUOTE y">Q</div>`,
			trim: true, keep: []string{"Top"}, gone: []string{"Q"},
		},
		{
			name: "marker inside a style element",
			src:  `<style>/* <blockquote type="cite"> */ p { color: red }</style><p>Top</p><p>Not a quote</p>`,
			trim: false, keep: []string{"Not a quote"},
		},
		{
			name: "marker inside svg",
			src:  `<p>Top</p><svg><foreignObject><blockquote type="cite">Q</blockquote></foreignObject></svg>`,
			trim: false, keep: []string{"Top"},
		},
		{
			name: "marker inside a title and a template",
			src:  `<title><div class="gmail_quote">x</div></title><p>Top</p><template><div class="gmail_quote">t</div></template><p>Rest</p>`,
			trim: false, keep: []string{"Rest"},
		},
		{
			name: "quote in a table cell with a reply in the next cell",
			src:  `<p>Top</p><table><tr><td><blockquote type="cite">Q</blockquote></td><td>Reply</td></tr></table>`,
			trim: false, keep: []string{"Q", "Reply"},
		},
		{
			name: "quote in the last table cell",
			src:  `<table><tr><td>Reply</td><td><blockquote type="cite">Q</blockquote></td></tr></table>`,
			trim: true, keep: []string{"Reply"}, gone: []string{">Q<"},
		},
		{
			name: "outlook header in a table, reply in the row above",
			src:  `<table><tr><td>Reply</td></tr><tr><td><hr><b>From:</b> A<br><b>Sent:</b> B<br><b>To:</b> C<br>old</td></tr></table>`,
			trim: true, keep: []string{"Reply"}, gone: []string{"old"},
		},
		{
			name: "malformed: unclosed quote swallows the rest",
			src:  `<p>Top<blockquote type=cite>Q<p>more<div>and more`,
			trim: true, keep: []string{"Top"}, gone: []string{"more"},
		},
		{
			name: "malformed: stray end tags",
			src:  `</blockquote></div><p>Top</p></div><blockquote type="cite">Q</blockquote></p></td>`,
			trim: true, keep: []string{"Top"}, gone: []string{">Q<"},
		},
		{
			name: "hidden content after the quote counts",
			src:  `<p>Top</p><blockquote type="cite">Q</blockquote><div style="display:none">hidden</div>`,
			trim: false, keep: []string{"Q"},
		},
		{
			name: "comment and whitespace after the quote",
			src:  "<p>Top</p><blockquote type=\"cite\">Q</blockquote>\n<!-- x -->\n<p>" + string(rune(0xA0)) + "</p><div><br></div>",
			trim: true, keep: []string{"Top"}, gone: []string{">Q<", "<br/>"},
		},
		{
			name: "attribution that is too long stays",
			src:  `<p>Top</p><div>` + strings.Repeat("words ", 100) + `wrote:</div><blockquote type="cite">Q</blockquote>`,
			trim: true, keep: []string{"Top", "wrote:"}, gone: []string{">Q<"},
		},
		{
			name: "attribution with an image stays",
			src:  `<p>Top</p><div><img src="cid:known@x" alt="face"> Bob wrote:</div><blockquote type="cite">Q</blockquote>`,
			trim: true, keep: []string{"Top", "Bob wrote:"}, gone: []string{">Q<"},
		},
		{
			name: "no marker",
			src:  `<p>Just a message.</p>`,
			trim: false, keep: []string{"Just a message."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := trimmed(t, c.src)
			if out.QuotedTrimmed != c.trim {
				t.Fatalf("quotedTrimmed = %v, want %v\nhtml %q", out.QuotedTrimmed, c.trim, out.HTML)
			}
			for _, s := range c.keep {
				if !strings.Contains(out.Text, s) {
					t.Errorf("text lacks %q: %q", s, out.Text)
				}
			}
			for _, s := range c.gone {
				if strings.Contains(out.HTML, s) {
					t.Errorf("html keeps %q: %q", s, out.HTML)
				}
			}
			if !c.trim {
				whole, err := Sanitize(Input{HTML: c.src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: fuzzKnown})
				if err != nil {
					t.Fatal(err)
				}
				if whole.HTML != out.HTML || whole.Text != out.Text || whole.Blocked != out.Blocked {
					t.Errorf("untrimmed output differs from the whole body:\n got %q\nwant %q", out.HTML, whole.HTML)
				}
			}
		})
	}
}

// What the output reports describes the trimmed body: the links, the
// blocked counts and the pictures of the quote are gone with it.
func TestTrimQuotedDerivedFields(t *testing.T) {
	src := `<p>Reply <a href="https://example.org/top">top</a> <img src="cid:known@x" alt="k"></p>` +
		`<blockquote type="cite">Quoted <a href="https://example.org/old">old</a> <script>x()</script>` +
		`<img src="https://remote.invalid/q.png"><img src="cid:other@x"><span onclick="y()">e</span></blockquote>`
	known := map[string]string{"known@x": "acc/msg/1.2", "other@x": "acc/msg/1.3"}
	in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: known}
	whole, err := Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	in.TrimQuoted = true
	cut, err := Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	if whole.QuotedTrimmed || !cut.QuotedTrimmed {
		t.Fatalf("quotedTrimmed whole %v, cut %v", whole.QuotedTrimmed, cut.QuotedTrimmed)
	}
	if len(whole.Links) != 2 || len(cut.Links) != 1 || cut.Links[0].Href != "https://example.org/top" {
		t.Errorf("links: whole %+v, cut %+v", whole.Links, cut.Links)
	}
	if (whole.Blocked != api.BlockedContent{RemoteImages: 1, Scripts: 1, EventHandlers: 1}) || (cut.Blocked != api.BlockedContent{}) {
		t.Errorf("blocked: whole %+v, cut %+v", whole.Blocked, cut.Blocked)
	}
	if strings.Join(whole.CIDs, ",") != "known@x,other@x" || strings.Join(cut.CIDs, ",") != "known@x" {
		t.Errorf("cids: whole %v, cut %v", whole.CIDs, cut.CIDs)
	}
	if strings.Contains(cut.Text, "Quoted") || !strings.Contains(cut.Text, "Reply") {
		t.Errorf("text %q", cut.Text)
	}

	// Under allow, only the pictures of the trimmed body are asked for.
	var asked []string
	in.Policy = api.RemoteAllow
	in.RemoteImage = func(u string) (string, []byte, bool) {
		asked = append(asked, u)
		return "image/png", []byte("PNG"), true
	}
	if _, err := Sanitize(in); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 {
		t.Errorf("fetched %v for a trimmed body", asked)
	}
}

// Without TrimQuoted nothing changes; with it a body without a quote comes
// out exactly as without it; compose refuses it.
func TestTrimQuotedOff(t *testing.T) {
	for name, h := range corpusHTML(t) {
		in := Input{HTML: h, Mode: ModeView, Policy: api.RemoteBlock}
		whole, err := Sanitize(in)
		in.TrimQuoted = true
		cut, err2 := Sanitize(in)
		if (err == nil) != (err2 == nil) {
			t.Errorf("%s: errors %v / %v", name, err, err2)
			continue
		}
		if err != nil || cut.QuotedTrimmed {
			continue
		}
		if whole.HTML != cut.HTML || whole.Text != cut.Text || whole.Blocked != cut.Blocked || len(whole.Links) != len(cut.Links) {
			t.Errorf("%s: untrimmed output differs", name)
		}
	}
	if _, err := Sanitize(Input{HTML: "<p>x</p>", Mode: ModeCompose, Policy: api.RemoteBlock, TrimQuoted: true}); err == nil {
		t.Error("compose accepted TrimQuoted")
	}
}

// The corpus: which of the quote fixtures are trimmed, and what is left.
func TestTrimQuotedCorpus(t *testing.T) {
	cases := []struct {
		file string
		trim bool
		keep []string
		gone []string
	}{
		{"quote-outlook-desktop-cs.eml", true, []string{"fakturu jsem zaplatil", "Jan"}, []string{"Odesláno", "posílám fakturu", "pay.example.org", "image002"}},
		{"quote-gmail.eml", true, []string{"Sounds good"}, []string{"Lunch tomorrow", "maps.example.org", "wrote"}},
		{"quote-apple-mail.eml", true, []string{"Thanks, they are lovely!", "Carol"}, []string{"Here are the photos", "Dave"}},
		{"quote-owa.eml", true, []string{"Thursday works"}, []string{"Can we meet", "rooms.example.org", "From:"}},
		{"quote-interleaved.eml", false, []string{"Not before Monday", "Is the build green"}, nil},
		{"quote-forward-only.eml", false, []string{"The report is attached"}, nil},
		{"quote-hostile.eml", true, []string{"Visible top", "cited in a cell", "reply in the next cell"}, []string{"quoted", "nested markers", "remote.invalid"}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(testdata, c.file))
			if err != nil {
				t.Fatal(err)
			}
			p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
			if err != nil || !p.HasHTML {
				t.Fatalf("parse: %v, html %v", err, p != nil && p.HasHTML)
			}
			out := trimmed(t, p.RawHTML)
			if out.QuotedTrimmed != c.trim {
				t.Fatalf("quotedTrimmed = %v, want %v: %q", out.QuotedTrimmed, c.trim, out.HTML)
			}
			for _, s := range c.keep {
				if !strings.Contains(out.Text, s) {
					t.Errorf("text lacks %q: %q", s, out.Text)
				}
			}
			for _, s := range c.gone {
				if strings.Contains(out.HTML, s) {
					t.Errorf("html keeps %q: %q", s, out.HTML)
				}
			}
		})
	}
}

// A hostile tree cannot make the search expensive or deep: it gives up and
// the body is sanitised (or refused) as without trimming.
func TestTrimQuotedPathological(t *testing.T) {
	cases := map[string]string{
		"deep":          "<p>Top</p>" + strings.Repeat("<div>", 5000) + `<blockquote type="cite">Q</blockquote>` + strings.Repeat("</div>", 5000),
		"many markers":  "<p>Top</p>" + strings.Repeat(`<blockquote type="cite">Q</blockquote><p>A</p>`, 5000),
		"many hr":       "<p>Top</p>" + strings.Repeat(`<hr><b>From:</b> x<br>`, 5000),
		"many empties":  "<p>Top</p>" + strings.Repeat("<div><br></div>", 20000) + `<blockquote type="cite">Q</blockquote>`,
		"wide header":   "<p>Top</p><hr><b>From:</b> " + strings.Repeat("x", 100000) + "<br><b>Sent:</b> y<br><b>To:</b> z<p>Old</p>",
		"many spans":    "<p>Top</p>" + strings.Repeat("<span>wrote:</span>", 20000) + `<blockquote>Q</blockquote>`,
		"many attribs":  "<p>Top</p>" + strings.Repeat("<p>On Mon Bob wrote:</p><blockquote>Q</blockquote><p>A</p>", 3000),
		"many cells":    "<table>" + strings.Repeat(`<tr><td><blockquote type="cite">Q</blockquote></td><td>R</td></tr>`, 5000) + "</table>",
		"null and junk": "<p>Top\x00</p><blockquote type=\"cite\x00\">Q</blockquote><div class=\"gmail_quote\x00\">G</div>",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, TrimQuoted: true}
			out, err := Sanitize(in)
			if err != nil {
				if out.HTML != "" || out.Text != "" {
					t.Fatalf("refused body leaked output")
				}
				return
			}
			checkClean(t, in, out)
		})
	}
}

func TestTrimQuotedText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" = unchanged
	}{
		{"original message", "Ok.\n\n-----Original Message-----\nFrom: A\nSent: B\n\nOld\n", "Ok."},
		{"czech", "Díky.\r\n\r\n-----Původní zpráva-----\r\nOd: A\r\n", "Díky."},
		{"german", "Ja.\n-----Ursprüngliche Nachricht-----\nVon: A", "Ja."},
		{"french typographic apostrophe", "Oui.\n----- Message d" + string(rightQuote) + "origine -----\nDe : A", "Oui."},
		{"separator with nothing after", "Ok.\n-----Original Message-----\n\n", ""},
		{"separator with nothing above", "\n-----Original Message-----\nFrom: A\n", ""},
		{"underscores and header", "Yes.\n\n________________________________\nFrom: A\nSent: B\nTo: C\nSubject: D\n\nOld", "Yes."},
		{"underscores and czech header", "Ano.\n________________________________\nOd: A\nOdesláno: B\nKomu: C\n", "Ano."},
		{"underscores without a header", "Line\n__________\nMore", ""},
		{"attribution and quote", "Sure.\n\nOn Wed, Bob <b@x> wrote:\n> Q\n>\n> more\n\n", "Sure."},
		{"wrapped attribution", "Sure.\n\nOn Wed, 1 Oct 2026 at 10:00, Bob <b@x>\nwrote:\n\n> Q\n", "Sure."},
		{"czech attribution", "Platí.\n\nDne 30. 9. 2026 v 10:00 Bob napsal(a):\n> Otázka\n", "Platí."},
		{"german attribution", "Gut.\nAm Mi., 1. Okt. 2026 um 10:00 Uhr schrieb Bob <b@x>:\n> Frage", "Gut."},
		{"interleaved", "Hi.\nOn Wed, Bob wrote:\n> Q1\nA1\n> Q2\n", ""},
		{"quote without attribution", "Hi.\n> Q1\n> Q2\n", ""},
		{"attribution with nothing quoted", "Hi.\nOn Wed, Bob wrote:\n\n", ""},
		{"only a quote", "On Wed, Bob wrote:\n> Q\n", ""},
		{"signature after the quote", "Hi.\nOn Wed, Bob wrote:\n> Q\n-- \nJan\n", ""},
		{"no quote", "Just text.\nTwo lines.", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := TrimQuotedText(c.in)
			if c.want == "" {
				if ok || got != c.in {
					t.Fatalf("trimmed %q to %q", c.in, got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, ok, c.want)
			}
		})
	}

	// Pathological texts stay cheap.
	many := strings.Repeat("On Mon Bob wrote:\n", 50000) + "> q\n"
	if _, ok := TrimQuotedText(many); !ok {
		t.Error("a long run of attributions before a quote was not trimmed")
	}
	if _, ok := TrimQuotedText(strings.Repeat("__________\nFrom: a\n", 30000)); ok {
		t.Error("underscores without a full header were trimmed")
	}
}

func FuzzTrimQuoted(f *testing.F) {
	for _, h := range corpusHTML(f) {
		f.Add(h)
	}
	f.Add(`<p>Top</p><blockquote type="cite">Q</blockquote>`)
	f.Add(`<p>Top</p><hr><b>From:</b> a<br><b>Sent:</b> b<br><b>To:</b> c<p>Old</p>`)
	f.Fuzz(func(t *testing.T, src string) {
		in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: fuzzKnown, TrimQuoted: true}
		out, err := Sanitize(in)
		if err != nil {
			if out.HTML != "" || out.Text != "" || len(out.Links) != 0 || len(out.CIDs) != 0 {
				t.Fatalf("failed sanitisation leaked output: %+v", out)
			}
			return
		}
		checkClean(t, in, out)
		if out.QuotedTrimmed && blankText(out.Text) && !strings.Contains(out.HTML, "<img") {
			t.Fatalf("trimmed to nothing: %q", out.HTML)
		}
		if text, ok := TrimQuotedText(src); ok && blankText(text) {
			t.Fatalf("text trimmed to nothing: %q", src)
		}
	})
}
