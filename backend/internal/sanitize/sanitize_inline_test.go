// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// InlineCID: the pictures of an attached message go in as data: URIs, the
// way fetched remote images do; what the hook declines or what is not a
// safe picture is dropped like an unknown cid:.
func TestInlineCID(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	asked := map[string]int{}
	in := Input{
		HTML:   `<p><img src="cid:pic@x" alt="p"><img src="cid:gone@x"><img src="cid:svg@x"><img src="cid:<pic@x>"></p>`,
		Mode:   ModeView,
		Policy: api.RemoteBlock,
		// KnownCIDs is not consulted while the hook is set.
		KnownCIDs: map[string]string{"gone@x": "acc/msg/9"},
		InlineCID: func(id string) (string, []byte, bool) {
			asked[id]++
			switch id {
			case "pic@x":
				return "image/png", png, true
			case "svg@x":
				return "image/svg+xml", []byte("<svg/>"), true
			}
			return "", nil, false
		},
	}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(png) + `"`
	if strings.Count(out.HTML, want) != 2 {
		t.Errorf("html = %q, want the png inlined twice", out.HTML)
	}
	if strings.Contains(out.HTML, "cid:") || strings.Contains(out.HTML, "malachi-cid:") || strings.Contains(out.HTML, "svg") {
		t.Errorf("html = %q: a cid: reference or the svg survived", out.HTML)
	}
	if out.Blocked.DangerousURLs != 2 {
		t.Errorf("blocked = %+v, want 2 dangerous urls (declined, svg)", out.Blocked)
	}
	if len(out.CIDs) != 1 || out.CIDs[0] != "pic@x" {
		t.Errorf("cids = %v, want [pic@x]", out.CIDs)
	}
	if asked["gone@x"] != 1 || asked["svg@x"] != 1 || asked["pic@x"] != 2 {
		t.Errorf("hook calls = %v", asked)
	}

	// A draft never inlines: the hook is ignored and cid: stays for the
	// attachment KnownCIDs binds.
	draft := Input{
		HTML:      `<img src="cid:pic@x">`,
		Mode:      ModeCompose,
		Policy:    api.RemoteBlock,
		KnownCIDs: map[string]string{"pic@x": "att_1"},
		InlineCID: in.InlineCID,
	}
	out, err = Sanitize(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.HTML, `src="cid:pic@x"`) {
		t.Errorf("compose html = %q, want cid: kept", out.HTML)
	}
}

// RewriteCIDs: a quoted original's pictures were copied into the store
// under new ids, so the draft references the copies; every spelling of an
// id is rewritten, unlisted ids fall through to KnownCIDs (and are dropped
// when unknown there too), and the view ignores the map altogether.
func TestRewriteCIDs(t *testing.T) {
	src := `<p><img src="cid:old@x" alt="a"><img src="cid:old%40x" alt="b"><img src="cid:<old@x>" alt="c">` +
		`<img src="cid:gone@x" alt="d"><img src="cid:kept@x" alt="e"></p>`
	in := Input{
		HTML:        src,
		Mode:        ModeCompose,
		Policy:      api.RemoteBlock,
		KnownCIDs:   map[string]string{"kept@x": "att_k"},
		RewriteCIDs: map[string]string{"old@x": "new@malachi.local"},
	}
	out, err := Sanitize(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.HTML, `src="cid:new@malachi.local"`) != 3 {
		t.Errorf("html = %q, want the new id three times", out.HTML)
	}
	if strings.Contains(out.HTML, "old") || strings.Contains(out.HTML, "gone") {
		t.Errorf("html = %q: an old or unknown id survived", out.HTML)
	}
	if !strings.Contains(out.HTML, `src="cid:kept@x"`) {
		t.Errorf("html = %q, want the known id kept", out.HTML)
	}
	if out.Blocked.DangerousURLs != 1 {
		t.Errorf("blocked = %+v, want the unknown id counted once", out.Blocked)
	}
	if strings.Join(out.CIDs, ",") != "kept@x,new@malachi.local" {
		t.Errorf("cids = %v, want the ids as they appear in the output", out.CIDs)
	}

	// The output re-sanitised as a draft (what draft.save does) with the
	// new id known is the identity.
	again, err := Sanitize(Input{HTML: out.HTML, Mode: ModeCompose, Policy: api.RemoteBlock,
		KnownCIDs: map[string]string{"kept@x": "att_k", "new@malachi.local": "att_n"}})
	if err != nil {
		t.Fatal(err)
	}
	if again.HTML != out.HTML || again.Text != out.Text || again.Blocked != (api.BlockedContent{}) {
		t.Errorf("round trip changed the output:\n got %q\nwant %q\nblocked %+v", again.HTML, out.HTML, again.Blocked)
	}

	// The view never rewrites: the map is not its business.
	view := Input{HTML: `<img src="cid:old@x">`, Mode: ModeView, Policy: api.RemoteBlock,
		KnownCIDs: map[string]string{"old@x": "acc/msg/2"}, RewriteCIDs: in.RewriteCIDs}
	out, err = Sanitize(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.HTML, `src="malachi-cid:acc/msg/2"`) || len(out.CIDs) != 1 || out.CIDs[0] != "old@x" {
		t.Errorf("view html = %q cids = %v", out.HTML, out.CIDs)
	}
}
