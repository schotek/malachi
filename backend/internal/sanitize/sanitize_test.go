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
