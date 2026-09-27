// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the navigation policies of macos/Sources/MalachiMail/WebViews/
// MessageWebView.swift and ComposeWebView.swift (decidePolicyFor,
// createWebViewWith); GTK: ui/internal/htmlview/view.go and
// ui/internal/editor/editor.go (decidePolicy).
//
// GTK and macOS allow the initial about:blank load and nothing else, and the
// viewer hands a link activation to its link handler. WebView2 cannot say
// which navigation is a link: IsUserInitiated is true for a click, a script
// click, a meta refresh and a host Navigate alike (measured), so the view
// allows exactly the one document it is loading, cancels everything else,
// and for the viewer asks the page afterwards which link was activated
// (LinkProbe, docs/windows-port.md §6.3). Cancelling only stops the view:
// the request itself is kept off the network by the gate and the
// environment's resolver rule. A new-window request (target=_blank, a
// middle, Ctrl or Shift click) opens nothing; in the viewer it is a link
// activation as in GTK, where decidePolicy treats NewWindowAction like a
// click, while macOS cancels auxclick in its script.

using System;
using Malachi.Core.Html;

namespace Malachi.Core.Presentation;

/// <summary>What a web view does with a navigation or a new-window request.</summary>
public static class NavigationPolicy
{
    /// <summary>
    /// NavigationStarting of the main frame: <see cref="NavigationAction.Allow"/>
    /// for the view's pending document (not a redirect), and only for it;
    /// in the viewer a navigation to a target <see cref="Links.AllowedLink"/>
    /// accepts may be a link the user activated
    /// (<see cref="NavigationAction.CancelAndProbe"/>); everything else,
    /// every navigation of the editor and the previewer included, is
    /// cancelled.
    /// </summary>
    public static NavigationAction Starting(WebViewKind kind, string uri, string? pendingDocument, bool isRedirected)
    {
        ArgumentNullException.ThrowIfNull(uri);
        if (pendingDocument is not null && !isRedirected && string.Equals(uri, pendingDocument, StringComparison.Ordinal))
        {
            return NavigationAction.Allow;
        }
        return kind == WebViewKind.Viewer && Links.AllowedLink(uri) ? NavigationAction.CancelAndProbe : NavigationAction.Cancel;
    }

    /// <summary>
    /// FrameNavigationStarting: no frame navigates (the sanitiser leaves
    /// none), except the one the view's own page embeds, once: the
    /// previewer's PDF (<see cref="RequestGate.ContentUri"/>).
    /// </summary>
    public static NavigationAction Frame(string uri, string? pendingContent)
    {
        ArgumentNullException.ThrowIfNull(uri);
        return pendingContent is not null && string.Equals(uri, pendingContent, StringComparison.Ordinal)
            ? NavigationAction.Allow
            : NavigationAction.Cancel;
    }

    /// <summary>
    /// NewWindowRequested: never a window. In the viewer a request the user
    /// started for a target <see cref="Links.AllowedLink"/> accepts is a link
    /// activation (<see cref="NavigationAction.CancelAndProbe"/>).
    /// </summary>
    public static NavigationAction NewWindow(WebViewKind kind, string uri, bool isUserInitiated)
    {
        ArgumentNullException.ThrowIfNull(uri);
        return kind == WebViewKind.Viewer && isUserInitiated && Links.AllowedLink(uri)
            ? NavigationAction.CancelAndProbe
            : NavigationAction.Cancel;
    }
}
