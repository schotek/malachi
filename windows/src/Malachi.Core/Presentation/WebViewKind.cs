// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6): the three hardened WebView2 views
// of the client. macOS has two WKWebView subclasses (MessageWebView,
// ComposeWebView) and Quick Look; GTK has htmlview.View, editor.Editor and
// Sushi. The previewer is the decided replacement for Quick Look and Sushi
// (§0, §6.6).

namespace Malachi.Core.Presentation;

/// <summary>Which of the client's web views a rule is about.</summary>
public enum WebViewKind
{
    /// <summary>The message viewer (htmlview.View, MessageWebView.swift): script off.</summary>
    Viewer,

    /// <summary>The compose editor (editor.Editor, ComposeWebView.swift): script on under the editor CSP.</summary>
    Editor,

    /// <summary>The attachment previewer (docs/windows-port.md §6.6): script off, images, PDF and text.</summary>
    Preview,
}
