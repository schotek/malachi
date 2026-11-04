// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/editor/document.go: the page the compose editor loads.

import Foundation

/// The editor document's Content-Security-Policy (editor.blp and the
/// <meta> below): the editor stays offline, no remote images, scripts,
/// fonts or frames can load whatever gets pasted; only inline (cid:) and
/// data: images render. User scripts are exempt from page CSP, so the
/// bridge still runs.
public let editorCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:"

/// editor.documentTemplate up to the body's content.
private let editorDocumentHead = """
<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="\(editorCSP)">
<style>
 body { margin: 12px; font-family: sans-serif; font-size: 15px; line-height: 1.4; }
 blockquote[type=cite] { margin: 0 0 0 .8ex; border-left: 2px solid #999; padding-left: 1ex; color: #555; }
 img { max-width: 100%; }
 a { color: #1c71d8; }
</style>
</head>
<body contenteditable="true">
"""

/// editor.documentTemplate after the body's content.
private let editorDocumentTail = "</body>\n</html>\n"

/// editor.Document: renders the editor page around `body`. The body is
/// inserted verbatim: callers own escaping. The only producers are
/// `prefill` and `parseMailto` (both `escapeText` based), backend-returned
/// draft HTML, and the editor's own previous content on reload.
public func editorDocument(body: String) -> String {
    editorDocumentHead + body + editorDocumentTail
}
