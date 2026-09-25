// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The remote-image bar and link lookup of ui/internal/window/remote.go, the
// pure parts. The daemon decides everything about the content; this only
// says what the bar shows.

/// What the bar shows (remote.go `remoteBarState`): nothing, how many
/// remote images were blocked with the buttons that load them, or the
/// notice that they are on their way.
public struct RemoteBarState: Sendable, Equatable {
    public var visible: Bool
    public var loading: Bool
    public var blocked: Int

    public init(visible: Bool = false, loading: Bool = false, blocked: Int = 0) {
        self.visible = visible
        self.loading = loading
        self.blocked = blocked
    }
}

/// Derives the bar from what is known about the message (remote.go
/// `remoteBarStateFor`). The bar shows the wait from the click until the
/// daemon answers, whatever the body says meanwhile.
@MainActor
public func remoteBarState(for lm: LoadedMessage?) -> RemoteBarState {
    guard let lm else {
        return RemoteBarState()
    }
    if lm.loadingImages {
        return RemoteBarState(visible: true, loading: true)
    }
    let n = loadableImages(lm.body)
    return RemoteBarState(visible: n > 0, blocked: n)
}

/// How many remote images of the body could still be shown by asking the
/// daemon again, which is what the bar offers (remote.go `loadableImages`).
///
/// Only the block policy leaves anything to load. Under allow the daemon
/// already fetched what it could, and the images the sanitiser still
/// counts are the ones it removes whatever the policy: CSS url(), srcset,
/// background attributes, plain http:, a download that failed. Tracking
/// pixels are counted separately and never loaded at all.
public func loadableImages(_ b: MessageBodyResult?) -> Int {
    guard let b, let html = b.html, !html.isEmpty, b.remoteContent == .block else {
        return 0
    }
    return b.blocked.remoteImages
}

/// The visible text of the first link in `links` with this target, "" when
/// the daemon listed none (remote.go `linkTextFor`).
public func linkTextFor(_ uri: String, _ links: [Link]) -> String {
    links.first { $0.href == uri }?.text ?? ""
}

/// A link the viewer reported activated (MessageWebView): `raw` is the
/// `href` attribute as written in the sanitiser's output, which is the
/// string the daemon lists in `links[].href`; `resolved` is the absolute
/// URL WebKit made of it (scheme and host lower-cased, an IDN host in
/// punycode, a trailing slash added, characters escaped), so the two
/// rarely compare equal. `raw` is nil when only the navigation policy saw
/// the activation, which knows the resolved URL alone.
public struct ActivatedLink: Sendable, Equatable {
    public var raw: String?
    public var resolved: String

    public init(raw: String?, resolved: String) {
        self.raw = raw
        self.resolved = resolved
    }

    /// What is matched against the daemon's list and, when allowed,
    /// opened: the attribute as written, or the resolved URL without it.
    public var href: String { raw ?? resolved }
}

/// What to do with an activated link (remote.go `openLink`), with one
/// difference from GTK: a link the daemon did not list is confirmed rather
/// than opened, because nothing is then known about the text it wore.
public enum LinkDecision: Sendable, Equatable {
    /// Not http(s) or mailto: nothing happens.
    case refused
    /// A mailto: link, for the composer.
    case mailto(String)
    /// Listed, and its text does not pretend to lead elsewhere: open.
    case open(String)
    /// Show the destination first: the visible text (`text`) reads as
    /// another site, or the daemon listed no such link (`text` is "").
    case confirm(text: String, href: String)
}

/// Decides `href` against the body's links as the daemon listed them.
/// `href` is compared exactly, like the daemon's own list is built, so it
/// must be the attribute as written (`ActivatedLink.href`), not a URL
/// WebKit normalised.
public func linkDecision(_ href: String, _ links: [Link]) -> LinkDecision {
    guard allowedLink(href) else {
        return .refused
    }
    if href.lowercased().utf8.starts(with: "mailto:".utf8) {
        return .mailto(href)
    }
    guard let listed = links.first(where: { $0.href == href }) else {
        return .confirm(text: "", href: href)
    }
    if isMasked(text: listed.text, href: href) {
        return .confirm(text: listed.text, href: href)
    }
    return .open(href)
}
