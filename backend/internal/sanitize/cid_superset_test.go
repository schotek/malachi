// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The attachments-on-demand ingest keeps every part whose Content-ID
// mime.CIDReferences lists and may leave the others on the server. That is
// only safe while the list is a superset of what the view resolves; these
// tests hold the two packages to it.

// imageCIDs lists every Content-ID an <img src> of src names, found the way
// the sanitiser finds them (the same parser and options, classifyURL,
// contentID) but over every img element wherever it sits, so KnownCIDs made
// from it knows each identifier the sanitiser could look up. It shares no
// code with mime.CIDReferences, whose property it checks.
func imageCIDs(src string) []string {
	doc, err := html.ParseWithOptions(strings.NewReader(src), html.ParseOptionEnableScripting(false))
	if err != nil {
		return nil
	}
	var ids []string
	stack := []*html.Node{doc}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
		if n.Type != html.ElementNode || n.Data != "img" {
			continue
		}
		for _, a := range n.Attr {
			if strings.EqualFold(a.Key, "src") {
				if u, class := classifyURL(a.Val); class == urlCID {
					ids = append(ids, contentID(u))
				}
			}
		}
	}
	return ids
}

// knownFor maps each id to a part path spelled the way core's renderHTML
// spells it.
func knownFor(lists ...[]string) map[string]string {
	known := make(map[string]string)
	for _, ids := range lists {
		for _, id := range ids {
			if _, ok := known[id]; !ok {
				known[id] = "acc_0123456789abcdef0123456789abcdef/msg_0123456789abcdef0123456789abcdef/" + strconv.Itoa(len(known)+1)
			}
		}
	}
	return known
}

// checkCIDSuperset asserts that every Content-ID the view-mode sanitiser
// resolves in src is among mime.CIDReferences(src) whenever that list is
// complete, with every identifier the HTML could name known to the view
// (plus extra, the message's own Content-IDs). It returns how many
// references the sanitiser resolved, so a caller can tell a vacuous pass.
func checkCIDSuperset(t testing.TB, src string, extra []string) int {
	t.Helper()
	in := Input{HTML: src, Mode: ModeView, Policy: api.RemoteBlock, KnownCIDs: knownFor(imageCIDs(src), extra)}
	out, err := Sanitize(in)
	if err != nil {
		return 0 // nothing resolved, nothing shown
	}
	refs, complete := mime.CIDReferences(src)
	if !complete {
		return 0 // the ingest keeps every part with a Content-ID
	}
	for _, id := range out.CIDs {
		if !refs[mime.NormalizeCID(id)] {
			t.Errorf("the view resolves cid:%q, CIDReferences misses it\nhtml %q\nrefs %v", id, src, refs)
		}
	}
	return len(out.CIDs)
}

// cidTricks are spellings that a scanner of the raw text or a plain
// tokenizer would get wrong.
var cidTricks = []string{
	`<img src="CID:Upper@Example.org"><img src="&#99;id:entity@x"><img src="cid&colon;colon@x">`,
	`<img src="cid:pct%40x"><img src="cid:%3Cenc@x%3E"><img src="cid:<angle@x>">`,
	"<img src=\"c\tid:tab@x\"><img src=\" \x01cid:ctl@x \"><img src=\"cid:new\nline@x\">",
	`<img src=cid:unquoted@x alt=y><image src="cid:image@x"><IMG SRC='cid:single@x'>`,
	`<img src="cid:image(1).png"><img src="cid:a,b;c'd">`,
	`<noscript><img src="cid:ns@x"></noscript><noscript><img src="&#99;id:a&ampb"></noscript>`,
	`<noscript><a title="</noscript><!--"><img src="cid:tricky@x">-->`,
	`<svg><style><img src="cid:svg@x"></style></svg><math><mi><img src="cid:math@x"></mi></math>`,
	`<table><tr><td><img src="cid:table@x"></td></tr><img src="cid:foster@x"></table>`,
	`<textarea><img src="cid:rcdata@x"></textarea><title><img src="cid:title@x"></title>`,
	`<template><img src="cid:template@x"></template><select><img src="cid:select@x"></select>`,
	`<img src="cid:first@x" src="cid:second@x"><img src="malachi-cid:acc/msg/2">`,
}

func TestCIDReferencesCoverSanitiser(t *testing.T) {
	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	resolved := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".eml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(testdata, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
		if err != nil || !p.HasHTML {
			continue
		}
		var own []string
		for _, a := range p.Attachments {
			own = append(own, a.ContentID)
		}
		resolved += checkCIDSuperset(t, p.RawHTML, own)
	}
	for _, s := range append(append([]string{}, hostileSeeds...), cidTricks...) {
		resolved += checkCIDSuperset(t, s, nil)
	}
	if resolved < 25 {
		t.Errorf("only %d references resolved: the check proves little", resolved)
	}
}
