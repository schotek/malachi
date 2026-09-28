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
//
// Also stricter than GTK and macOS: a listed link opens without the
// question only when the address the launcher would hand the browser
// (ILauncher.LinkTarget, passed in as launched) is on the site its text
// names, besides Go's reading of the href agreeing (Links.IsMasked, which
// fails closed). Where the parsers disagree, the browser goes where the
// launcher's address says, so that address is what is judged. And every
// listed link the activation matches is judged, where GTK's linkTextFor
// and macOS take the first with the href: an activation carries the href,
// not the anchor, so a body that lists one href twice, an empty anchor
// before the one that wears the bank's address, would otherwise open
// without the question. With the attribute, the listed links whose
// canonical form is the navigation's are judged as well, as without it:
// hrefs that differ only in case, a default port or escaping are one
// address, and the attribute may be read from another anchor than the
// one activated.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Text;

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
    /// attribute as written, not a URL WebView2 normalised. A listed link
    /// opens only when its text is not masked (<see cref="Links.IsMasked"/>)
    /// and does not name another site than <paramref name="launched"/> of it
    /// (<see cref="Links.LeadsElsewhere"/>). Every listed link with this
    /// href is judged, not the first alone as in GTK and macOS: the click
    /// reports the href, not the anchor, so an empty anchor listed before
    /// the one that wears the bank's address must not speak for it.
    /// </summary>
    /// <param name="href">The link.</param>
    /// <param name="links">The body's links as the daemon listed them.</param>
    /// <param name="launched">
    /// The address the browser would get for a link
    /// (<c>ILauncher.LinkTarget</c>), null when the launcher refuses it.
    /// </param>
    public static LinkDecision For(string href, IReadOnlyList<Link> links, Func<string, string?> launched)
    {
        ArgumentNullException.ThrowIfNull(href);
        ArgumentNullException.ThrowIfNull(links);
        ArgumentNullException.ThrowIfNull(launched);
        return ForAttribute(href, null, links, launched);
    }

    /// <summary>
    /// Decides an activated link. With its attribute, as
    /// <see cref="For(string, IReadOnlyList{Link}, Func{string, string})"/>
    /// does, unless the attribute leads somewhere else than the navigation
    /// (it was read from another element than the one activated); besides
    /// the listed links with that href, every one whose canonical form is
    /// the navigation's is judged too, against its own href and the
    /// address the attribute would be opened as, since hrefs that differ
    /// only in case, a default port or escaping lead to one address and the
    /// attribute may be another anchor's than the one activated. Without
    /// it, the resolved URL is compared with the canonical form of every
    /// listed href (<see cref="ChromiumUrl"/>): a match whose text is masked,
    /// by its href or by <paramref name="launched"/> of the resolved URL, is
    /// confirmed with that text; so is, with no text, every link of a body
    /// that carries a masked link at all, since a link whose canonical form
    /// is not certain could be the one activated; otherwise a match opens
    /// and anything else is confirmed. The answer names the resolved URL.
    /// </summary>
    /// <param name="link">The activated link.</param>
    /// <param name="links">The body's links as the daemon listed them.</param>
    /// <param name="launched">
    /// The address the browser would get for a link
    /// (<c>ILauncher.LinkTarget</c>), null when the launcher refuses it.
    /// </param>
    public static LinkDecision For(ActivatedLink link, IReadOnlyList<Link> links, Func<string, string?> launched)
    {
        ArgumentNullException.ThrowIfNull(link);
        ArgumentNullException.ThrowIfNull(links);
        ArgumentNullException.ThrowIfNull(launched);
        if (link.Raw is { } raw && Agrees(raw, link.Resolved))
        {
            return ForAttribute(raw, ChromiumUrl.Canonicalize(link.Resolved) ?? link.Resolved, links, launched);
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
        var target = launched(resolved);
        // A listed link that matches is masked by its own href as it would
        // be opened with its attribute, and also by this navigation's
        // address, which is what would be opened now.
        var (matched, misleading) = Judge(
            links,
            l => string.Equals(ChromiumUrl.Canonicalize(l.Href), key, StringComparison.Ordinal),
            l => Misleads(l, launched) || Links.LeadsElsewhere(l.Text, target));
        if (misleading is not null)
        {
            return new Confirm(misleading.Text, resolved);
        }
        if (!matched || links.Any(l => Misleads(l, launched)))
        {
            return new Confirm("", resolved);
        }
        return new Open(resolved);
    }

    // A link decided by its attribute (href as written): every listed link
    // with that href is judged and, where the navigation's canonical form
    // is known (key), so is every listed link whose canonical form it is,
    // also against what opening href would open (launched of href); only a
    // listed href opens without the question.
    private static LinkDecision ForAttribute(string href, string? key, IReadOnlyList<Link> links, Func<string, string?> launched)
    {
        if (!Links.AllowedLink(href))
        {
            return new Refused();
        }
        if (IsMailto(href))
        {
            return new Mailto(href);
        }
        var target = launched(href);
        var (_, misleading) = Judge(
            links,
            l => string.Equals(l.Href, href, StringComparison.Ordinal)
                || (key is not null && string.Equals(ChromiumUrl.Canonicalize(l.Href), key, StringComparison.Ordinal)),
            l => Misleads(l, launched) || (!IsMailto(l.Href) && Links.LeadsElsewhere(l.Text, target)));
        if (misleading is not null)
        {
            return new Confirm(misleading.Text, href);
        }
        return links.Any(l => string.Equals(l.Href, href, StringComparison.Ordinal)) ? new Open(href) : new Confirm("", href);
    }

    // The rule both paths share: every listed link the activation matches
    // is judged, since a body may list one href under several texts and
    // the activation cannot tell which anchor it came from; the first whose
    // text misleads is the one the question quotes, and one is enough.
    // Returns whether any link matched, and that first misleading one.
    private static (bool Matched, Link? Misleading) Judge(IReadOnlyList<Link> links, Func<Link, bool> matches, Func<Link, bool> misleads)
    {
        var matched = false;
        foreach (var l in links)
        {
            if (!matches(l))
            {
                continue;
            }
            matched = true;
            if (misleads(l))
            {
                return (true, l);
            }
        }
        return (matched, null);
    }

    // Whether a listed link's text pretends to lead elsewhere: by Go's
    // reading of its href, as GTK judges it but failing closed, or by the
    // host of the address that would be opened for it (launched of its
    // href, null when the launcher refuses it). A mailto: link goes to the
    // composer, which shows its address, so its text never misleads.
    private static bool Misleads(Link listed, Func<string, string?> launched) =>
        !IsMailto(listed.Href)
        && (Links.IsMasked(listed.Text, listed.Href) || Links.LeadsElsewhere(listed.Text, launched(listed.Href)));

    // Whether the attribute read by the viewer is the one of the navigation:
    // its canonical form is the resolved URL's, or it has none that is
    // certain (then it is trusted, as on macOS: the attribute is what is
    // judged and opened).
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
        /// <see cref="Href"/>, which carries no userinfo, so the host the
        /// browser goes to is what follows the scheme), beside the text the
        /// link wore; for an unlisted link the destination alone. GTK names
        /// the href as written, where "https://bank.example@evil.example/"
        /// reads as the bank. The text is the mail's and is isolated in the
        /// sentence (<see cref="DisplayText.Isolate"/>), so an override in it
        /// cannot draw the destination after it backwards, nor a
        /// right-to-left text move it.
        /// </summary>
        public string Body(string destination)
        {
            if (Text.Length == 0)
            {
                // TRANSLATORS: %s is the link's real destination.
                return L10n.T("This link leads to %s.", destination); // Windows-only string
            }
            // TRANSLATORS: %s are the link's visible text and its real destination.
            return L10n.T("The link is shown as “%s” but leads to %s.", DisplayText.Isolate(Text), destination);
        }
    }
}
