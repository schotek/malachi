// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The stub must never echo its input. This test stays valid for the real
// implementation: a <script> tag must never survive.
func TestStubFailsClosed(t *testing.T) {
	in := Input{HTML: `<script>alert(1)</script><img src="https://x/1.png">`, Policy: api.RemoteBlock}
	out, err := Sanitize(in)
	if strings.Contains(out.HTML, "<script") {
		t.Fatalf("script survived sanitisation: %q", out.HTML)
	}
	if err == nil && out.HTML == in.HTML {
		t.Fatalf("sanitiser returned its input unchanged")
	}
}

// Composed HTML is hostile too. Compose mode must never echo its input, must
// leave Text empty when it fails, and must never keep an https: image even
// if a caller wrongly passes RemoteAllow.
func TestComposeModeFailsClosed(t *testing.T) {
	in := Input{HTML: `<script>x()</script><img src="https://x/1.png"><p>hi</p>`, Mode: ModeCompose, Policy: api.RemoteAllow}
	out, err := Sanitize(in)
	if strings.Contains(out.HTML, "<script") || strings.Contains(out.HTML, "https://x/1.png") {
		t.Fatalf("compose mode kept hostile content: %q", out.HTML)
	}
	if err != nil && out.Text != "" {
		t.Fatalf("failed sanitisation must not produce text: %q", out.Text)
	}
}

// The sanitiser only understands the two-state block/allow decision. A
// policy it does not know (knownSenders is resolved by internal/core before
// the call, anything else is a bug) must never keep remote references.
func TestUnknownPolicyFailsClosed(t *testing.T) {
	for _, pol := range []api.RemoteContentPolicy{api.RemoteKnownSenders, "", "whatever"} {
		in := Input{HTML: `<img src="https://x/1.png">`, Policy: pol}
		out, _ := Sanitize(in)
		if strings.Contains(out.HTML, "https://x/1.png") {
			t.Errorf("policy %q kept a remote reference: %q", pol, out.HTML)
		}
	}
}
