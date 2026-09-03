// Package sanitize turns hostile e-mail HTML into something a
// JavaScript-disabled webview can render safely. This is the single most
// security-critical package in the backend; see docs/security.md.
//
// Contract: message.body returns ONLY the output of this package. There is
// no code path that returns stored HTML unsanitised, no debug flag, no
// test-only shortcut. The stub below therefore fails closed: it returns an
// error and an empty body, never its input.
//
// Requirements the implementation must meet:
//   - remove <script>, <iframe>, <object>, <embed>, <applet>, <form>,
//     <input>, <button>, <meta>, <link>, <base>, <svg> scripting, MathML;
//   - strip every on* attribute and any attribute whose value parses as a
//     javascript:, vbscript: or data:text/html URL, including obfuscated
//     variants (whitespace, entities, control characters);
//   - remote references (img/src, srcset, CSS url(), @import, @font-face,
//     background attributes, <video>/<audio>, favicons, <link rel=…>) are
//     removed under RemoteBlock and, under RemoteAllow, kept only for https:
//     images; counts are reported in api.BlockedContent;
//   - inline <style> and style="" are parsed and re-emitted through an
//     allow-list of properties (no position:fixed overlays, no url(),
//     no expression(), no @import, no ::before content tricks);
//   - links keep only http(s): and mailto:; every link gets
//     rel="noopener noreferrer" and target removed; the real href is
//     exported in api.Link for display;
//   - cid: references are rewritten to a scheme the webview resolves
//     locally, and only to parts that exist in the message;
//   - output is re-serialised from the parsed tree (never regex on the
//     source), with a size cap and a nesting-depth cap;
//   - the ruleset carries a version string returned as SanitizerVersion.
//
// Library candidates (decision pending; evaluate against the requirements):
//   - github.com/microcosm-cc/bluemonday — allow-list sanitiser built on
//     golang.org/x/net/html; mature, widely used. Weak on CSS (style
//     attributes are mostly dropped, no CSS parser); may be a base with a
//     separate CSS pass.
//   - golang.org/x/net/html directly + own policy — most control, most
//     responsibility; the tokenizer is the same one bluemonday uses.
//   - github.com/aymerick/douceur — CSS parser that could handle the style
//     pass alongside either of the above.
//   - Porting the approach of Thunderbird's or Geary's sanitisers (both
//     tree-based allow-lists) as a design reference, not as code.
//
// Whatever is chosen must be fuzzed (go test -fuzz) with the corpus in
// backend/testdata/mime and with generated pathological HTML.
package sanitize

import "github.com/schotek/malachi/backend/pkg/api"

// Version identifies the current ruleset. Bump on any behavioural change so
// cached bodies can be invalidated.
const Version = "0-stub"

// Mode selects which direction the HTML is travelling.
type Mode int

const (
	// ModeView: incoming mail for display. cid: references are rewritten to
	// the webview's local scheme; the remote policy comes from the caller.
	ModeView Mode = iota
	// ModeCompose: HTML written in the editor, on its way into a draft.
	// Policy must be RemoteBlock (anything else is an error); cid: is kept
	// verbatim only when present in KnownCIDs; data: URLs are removed.
	ModeCompose
)

// Input is what the MIME layer (ModeView) or draft.save (ModeCompose)
// hands over.
type Input struct {
	HTML          string
	Mode          Mode
	Policy        api.RemoteContentPolicy
	KnownCIDs     map[string]string // Content-ID → PartID (view) / attachment ID (compose)
	MaxOutputSize int               // bytes; 0 = default
}

// Output is the only form of HTML that may cross the API.
type Output struct {
	HTML string
	// Text is the plain-text rendering of the sanitised tree: the text
	// alternative of a composed message and the "text derived from html" of
	// message.body.
	Text    string
	Blocked api.BlockedContent
	Links   []api.Link
	// CIDs lists the cid: references that survived (ModeCompose uses it to
	// keep only referenced inline attachments bound).
	CIDs    []string
	Version string
}

// Sanitize is the bootstrap stub. It fails closed.
func Sanitize(in Input) (Output, error) {
	_ = in // deliberately unused: the input must never leak into Output.
	return Output{Version: Version}, api.NewError(api.CodeSanitizeFailed, "sanitiser not implemented")
}
