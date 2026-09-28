// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorDocument.swift; GTK:
// ui/internal/editor/document.go (documentTemplate, Document).
//
// The page the compose editor loads, byte for byte GTK's and macOS's with
// one addition: a fixed <title> line after the charset (docs/windows-port.md
// §6.2; a pasted <title> comes after it, and only the first counts). Built
// by concatenation: the CSS braces and "100%" would break a format string.
// The WebView2 layer sends the CSP as a response header as well.

using System;

namespace Malachi.Core.Html;

/// <summary>The contenteditable document of the compose editor.</summary>
public static class EditorDocument
{
    /// <summary>
    /// The editor document's Content-Security-Policy: the editor stays
    /// offline; no remote images, scripts, fonts or frames load whatever gets
    /// pasted; only inline (cid:) and data: images render. Page scripts,
    /// handlers and javascript: URLs are blocked by it (script-src falls back
    /// to default-src); the bridge, injected by the host, still runs.
    /// </summary>
    public const string Csp = "default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:";

    /// <summary>The document's fixed title (a brand name, not translated).</summary>
    public const string Title = "Malachi Mail"; // Windows-only string

    // editor.documentTemplate up to the body's content.
    private const string Head =
        "<!doctype html>\n"
        + "<html>\n"
        + "<head>\n"
        + "<meta charset=\"utf-8\">\n"
        + "<title>" + Title + "</title>\n"
        + "<meta http-equiv=\"Content-Security-Policy\" content=\"" + Csp + "\">\n"
        + "<style>\n"
        + " body { margin: 12px; font-family: sans-serif; font-size: 15px; line-height: 1.4; }\n"
        + " blockquote[type=cite] { margin: 0 0 0 .8ex; border-left: 2px solid #999; padding-left: 1ex; color: #555; }\n"
        + " img { max-width: 100%; }\n"
        + " a { color: #1c71d8; }\n"
        + "</style>\n"
        + "</head>\n"
        + "<body contenteditable=\"true\">";

    // editor.documentTemplate after the body's content.
    private const string Tail = "</body>\n</html>\n";

    /// <summary>
    /// editor.Document: renders the editor page around <paramref name="body"/>.
    /// The body is inserted verbatim: callers own escaping. The only producers
    /// are <see cref="Compose.Prefill"/> and <see cref="Compose.Mailto"/> (both
    /// escape), draft HTML the daemon returned, and the editor's own previous
    /// content on reload.
    /// </summary>
    public static string Document(string body)
    {
        ArgumentNullException.ThrowIfNull(body);
        return Head + body + Tail;
    }
}
