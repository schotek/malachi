// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/RemoteBar.swift (LinkDecision,
// linkDecision) and of the question texts of macos/Sources/MalachiMail/
// Actions/MessageActionsController.swift (openLink,
// openUnlistedLinkQuestion); GTK: ui/internal/window/remote.go (openLink).
//
// As on macOS and unlike GTK, a link the daemon did not list is confirmed,
// not opened (docs/windows-port.md §0, §6.4; windows/README.md). What
// Windows adds is For(ActivatedLink): where the viewer could not read the
// href attribute, the resolved URL is compared with the canonical form of
// the listed hrefs (ChromiumUrl) instead of failing every comparison.
// Swift's free function linkDecision is For(string, links).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Html;
using Malachi.Core.I18n;

namespace Malachi.Core.Model;

/// <summary>
/// What to do with an activated link (remote.go <c>openLink</c>), with one
/// difference from GTK: a link the daemon did not list is confirmed rather
/// than opened, because nothing is then known about the text it wore.
/// </summary>
public abstract record LinkDecision
{
    private LinkDecision()
    {
    }

    /// <summary>
    /// Decides <paramref name="href"/> against the body's links as the daemon
    /// listed them (Swift <c>linkDecision</c>). <paramref name="href"/> is
    /// compared exactly, as the daemon's own list is built, so it must be the
    /// attribute as written, not a URL WebView2 normalised.
    /// </summary>
    public static LinkDecision For(string href, IReadOnlyList<Link> links)
    {
        ArgumentNullException.ThrowIfNull(href);
        ArgumentNullException.ThrowIfNull(links);
        if (!Links.AllowedLink(href))
        {
            return new Refused();
        }
        if (IsMailto(href))
        {
            return new Mailto(href);
        }
        Link? listed = null;
        foreach (var l in links)
        {
            if (string.Equals(l.Href, href, StringComparison.Ordinal))
            {
                listed = l;
                break;
            }
        }
        if (listed is null)
        {
            return new Confirm("", href);
        }
        if (Links.IsMasked(listed.Text, href))
        {
            return new Confirm(listed.Text, href);
        }
        return new Open(href);
    }

    /// <summary>
    /// Decides an activated link. With its attribute, as
    /// <see cref="For(string, IReadOnlyList{Link})"/> does, unless the
    /// attribute leads somewhere else than the navigation (it was read from
    /// another element than the one activated). Without it, the resolved URL
    /// is compared with the canonical form of every listed href
    /// (<see cref="ChromiumUrl"/>): a match whose text is masked is
    /// confirmed with that text; so is, with no text, every link of a body
    /// that carries a masked link at all, since a link whose canonical form
    /// is not certain could be the one activated; otherwise a match opens
    /// and anything else is confirmed. The answer names the resolved URL.
    /// </summary>
    public static LinkDecision For(ActivatedLink link, IReadOnlyList<Link> links)
    {
        ArgumentNullException.ThrowIfNull(link);
        ArgumentNullException.ThrowIfNull(links);
        if (link.Raw is { } raw && Agrees(raw, link.Resolved))
        {
            return For(raw, links);
        }
        var resolved = link.Resolved;
        if (!Links.AllowedLink(resolved))
        {
            return new Refused();
        }
        if (IsMailto(resolved))
        {
            return new Mailto(resolved);
        }
        var key = ChromiumUrl.Canonicalize(resolved) ?? resolved;
        var matched = false;
        var anyMasked = false;
        Link? masked = null;
        foreach (var l in links)
        {
            var isMasked = Links.IsMasked(l.Text, l.Href);
            anyMasked |= isMasked;
            if (string.Equals(ChromiumUrl.Canonicalize(l.Href), key, StringComparison.Ordinal))
            {
                matched = true;
                if (isMasked && masked is null)
                {
                    masked = l;
                }
            }
        }
        if (masked is not null)
        {
            return new Confirm(masked.Text, resolved);
        }
        if (!matched || anyMasked)
        {
            return new Confirm("", resolved);
        }
        return new Open(resolved);
    }

    // Whether the attribute read by the viewer is the one of the navigation:
    // its canonical form is the resolved URL's, or it has none that is
    // certain (then it is trusted, as on macOS).
    private static bool Agrees(string raw, string resolved) =>
        ChromiumUrl.Canonicalize(raw) is not { } canonical
        || string.Equals(canonical, ChromiumUrl.Canonicalize(resolved) ?? resolved, StringComparison.Ordinal);

    // remote.go: strings.HasPrefix(strings.ToLower(uri), "mailto:"), lowered
    // as Links.AllowedLink lowers it.
    private static bool IsMailto(string href) => href.ToLowerInvariant().StartsWith("mailto:", StringComparison.Ordinal);

    /// <summary>Not http(s) or mailto: nothing happens.</summary>
    public sealed record Refused : LinkDecision;

    /// <summary>A mailto: link, for the composer.</summary>
    /// <param name="Href">The link.</param>
    public sealed record Mailto(string Href) : LinkDecision;

    /// <summary>Listed, and its text does not pretend to lead elsewhere: open.</summary>
    /// <param name="Href">The link.</param>
    public sealed record Open(string Href) : LinkDecision;

    /// <summary>
    /// Show the destination first: the visible text (<see cref="Text"/>)
    /// reads as another site, or the daemon listed no such link
    /// (<see cref="Text"/> is "").
    /// </summary>
    /// <param name="Text">What the link says; "" for an unlisted link.</param>
    /// <param name="Href">The link.</param>
    public sealed record Confirm(string Text, string Href) : LinkDecision
    {
        /// <summary>The question's title (remote.go <c>openLink</c>).</summary>
        public static string Title => L10n.T("Open This Link?");

        /// <summary>
        /// The question's body, naming <paramref name="destination"/>: where
        /// the browser really goes (<c>ILauncher.LinkTarget</c> of
        /// <see cref="Href"/>), beside the text the link wore; for an
        /// unlisted link the destination alone.
        /// </summary>
        public string Body(string destination)
        {
            if (Text.Length == 0)
            {
                // TRANSLATORS: %s is the link's real destination.
                return L10n.T("This link leads to %s.", destination); // Windows-only string
            }
            // TRANSLATORS: %s are the link's visible text and its real destination.
            return L10n.T("The link is shown as “%s” but leads to %s.", Text, destination);
        }
    }
}
