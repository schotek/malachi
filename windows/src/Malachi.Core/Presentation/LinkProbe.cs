// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the link half of the viewer script of
// macos/Sources/MalachiMail/WebViews/MessageWebView.swift (viewerScript's
// click listener: {raw: getAttribute('href'), resolved: a.href}); GTK has
// no counterpart (WebKitGTK hands the resolved URI to htmlview.View.OnLink).
//
// The Windows viewer runs no script of its own in the page: with page script
// off no listener of an injected script ever fires (measured), so nothing
// can take the click as the macOS script does. What WebView2 offers instead:
// the clicked link has the focus, and a host script (ExecuteScriptAsync runs
// with page script off) reads it after the cancelled NavigationStarting or
// NewWindowRequested (docs/windows-port.md §6.3). The script reads only
// through the prototypes' own accessors, so a named element of the page
// cannot stand in for document.activeElement or an anchor's href. The
// attribute counts only for the navigation it explains: a form submit also
// arrives as a user-initiated NavigationStarting, and the focus then is on
// the form's button, not a link. A meta refresh is not user-initiated
// (SPIKES.md §2h) and is never probed (NavigationPolicy.Starting).

using System;
using System.Text.Json;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>Finds the link an activation came from.</summary>
public static class LinkProbe
{
    /// <summary>
    /// The host script: the focused element's tag and, when it is in a link,
    /// that link's <c>href</c> attribute as written and the URL it resolves
    /// to; null when nothing is focused.
    /// </summary>
    public const string Script = """
        (() => {
          try {
            const active = Object.getOwnPropertyDescriptor(Document.prototype, 'activeElement').get.call(document);
            if (!active) return null;
            const tag = String(Object.getOwnPropertyDescriptor(Element.prototype, 'localName').get.call(active));
            const link = Element.prototype.closest.call(active, 'a[href], area[href]');
            if (!link) return {tag: tag};
            const raw = Element.prototype.getAttribute.call(link, 'href');
            let resolved;
            if (link instanceof HTMLAnchorElement) resolved = Object.getOwnPropertyDescriptor(HTMLAnchorElement.prototype, 'href').get.call(link);
            else if (link instanceof HTMLAreaElement) resolved = Object.getOwnPropertyDescriptor(HTMLAreaElement.prototype, 'href').get.call(link);
            else resolved = new URL(raw, Object.getOwnPropertyDescriptor(Node.prototype, 'baseURI').get.call(link)).href;
            return {tag: tag, raw: raw, resolved: String(resolved)};
          } catch (e) {
            return null;
          }
        })()
        """;

    /// <summary>The focused elements a form is submitted from: never a link activation.</summary>
    private static readonly string[] FormControls = ["button", "input", "select", "textarea", "form", "option", "label"];

    /// <summary>
    /// The script's JSON result (ExecuteScriptAsync's answer); null for
    /// <c>null</c>, a failed evaluation or anything not of the shape. A tag
    /// is required, and a link has both its attribute and its URL.
    /// </summary>
    public static LinkProbeResult? Parse(string? json)
    {
        if (string.IsNullOrWhiteSpace(json))
        {
            return null;
        }
        try
        {
            using var document = JsonDocument.Parse(json);
            var root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Object
                || !root.TryGetProperty("tag", out var tag) || tag.ValueKind != JsonValueKind.String)
            {
                return null;
            }
            var raw = root.TryGetProperty("raw", out var r) && r.ValueKind == JsonValueKind.String ? r.GetString() : null;
            var resolved = root.TryGetProperty("resolved", out var u) && u.ValueKind == JsonValueKind.String ? u.GetString() : null;
            if (raw is null || resolved is null)
            {
                raw = null;
                resolved = null;
            }
            return new LinkProbeResult(tag.GetString() ?? "", raw, resolved);
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <summary>
    /// The link a cancelled navigation to <paramref name="uri"/> activated,
    /// or null when it was none: the focused link when it resolves to
    /// exactly <paramref name="uri"/> (its attribute as written then decides,
    /// as on macOS); otherwise, for a new-window request that no form
    /// control made (a middle click on a link the focus did not reach), the
    /// URL alone, which <see cref="LinkDecision"/> confirms unless it
    /// matches a listed link; for a navigation of the page itself (a meta
    /// refresh, a form) nothing.
    /// </summary>
    public static ActivatedLink? Activation(LinkProbeResult? probe, string uri, bool newWindow)
    {
        ArgumentNullException.ThrowIfNull(uri);
        if (probe?.Resolved is { } resolved && string.Equals(resolved, uri, StringComparison.Ordinal))
        {
            return new ActivatedLink(probe.Raw, uri);
        }
        if (newWindow && (probe is null || Array.IndexOf(FormControls, probe.Tag) < 0))
        {
            return new ActivatedLink(null, uri);
        }
        return null;
    }
}
