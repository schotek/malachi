// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package editor

import "fmt"

// documentTemplate wraps the body of a message being composed. The meta CSP
// keeps the editor offline: no remote images, scripts, fonts or frames can
// load whatever gets pasted; only inline (cid:) and data: images render.
// User scripts are exempt from page CSP, so the bridge still runs.
const documentTemplate = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:">
<style>
 body { margin: 12px; font-family: sans-serif; font-size: 15px; line-height: 1.4; }
 blockquote[type=cite] { margin: 0 0 0 .8ex; border-left: 2px solid #999; padding-left: 1ex; color: #555; }
 img { max-width: 100%%; }
 a { color: #1c71d8; }
</style>
</head>
<body contenteditable="true">%s</body>
</html>
`

// Document renders the editor page around bodyHTML. The body is inserted
// verbatim: callers own escaping. The only producers are compose.Prefill
// and compose.ParseMailto (both html.EscapeString based), backend-returned
// draft HTML, and the editor's own previous content on reload.
func Document(bodyHTML string) string {
	return fmt.Sprintf(documentTemplate, bodyHTML)
}
