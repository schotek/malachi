// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The remote-image bar, the pictures bar and link lookup of
// ui/internal/window/remote.go, the pure parts. The daemon decides
// everything about the content; this only says what the bars show.

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

/// What the pictures bar shows (remote.go `picturesBarState`): nothing,
/// how many pictures of the HTML body are kept on the mail server only
/// (`remote`) with the button that downloads them, or the notice that they
/// are on their way. It sits below the remote-image bar; both may show.
public struct PicturesBarState: Sendable, Equatable {
    public var visible: Bool
    public var loading: Bool
    public var remote: Int

    public init(visible: Bool = false, loading: Bool = false, remote: Int = 0) {
        self.visible = visible
        self.loading = loading
        self.remote = remote
    }
}

/// Derives the pictures bar from what is known about the message
/// (remote.go `picturesBarStateFor`). The bar shows the wait from the
/// click until the body was asked for again, whatever the body says
/// meanwhile.
@MainActor
public func picturesBarState(for lm: LoadedMessage?) -> PicturesBarState {
    guard let lm else {
        return PicturesBarState()
    }
    if lm.loadingPictures {
        return PicturesBarState(visible: true, loading: true)
    }
    let n = remotePictures(lm.body)
    return PicturesBarState(visible: n > 0, remote: n)
}

/// How many pictures the HTML body shows are kept on the mail server only
/// and not on this device now (remote.go `remotePictures`): the daemon's
/// `remotePictures`, 0 unless the body goes into the HTML view.
public func remotePictures(_ b: MessageBodyResult?) -> Int {
    guard let b, showsHTML(b) else {
        return 0
    }
    return b.remotePictureCount
}

/// The policy message.body is asked with again after the pictures were
/// downloaded (remote.go `picturesPolicy`): allow when the remote images
/// are loaded or on their way, so the new body does not take them away
/// again; otherwise no override (the stored preference).
@MainActor
public func picturesPolicy(_ lm: LoadedMessage) -> RemoteContentPolicy? {
    if lm.loadingImages || lm.body?.remoteContent == .allow {
        return .allow
    }
    return nil
}

/// Whether a partNotDownloaded for picture `partID` says that the cached
/// body of `lm` is out of date (remote.go `recheckPictures`): the body
/// lists the part among the pictures it shows (`inlineParts`) yet counts
/// none on the server, and nothing that brings a newer body is on its way
/// (the body itself, the remote images, Download Pictures). Once until the
/// next download of the message (`picturesRechecked`): a body that still
/// counts none while the daemon will not serve the picture is not asked
/// for in a loop.
@MainActor
public func recheckPictures(_ lm: LoadedMessage?, _ partID: String) -> Bool {
    guard let lm, !lm.picturesRechecked, !lm.fetching, !lm.loadingImages, !lm.loadingPictures else {
        return false
    }
    guard let b = lm.body, showsHTML(b), b.remotePictureCount == 0 else {
        return false
    }
    return (b.inlineParts ?? [:]).values.contains(partID)
}

/// Whether a download of the message `lm` holds asks for its body again
/// (remote.go `reloadAfterDownload`): the body on display counts pictures
/// on the mail server, which the daemon now holds, and neither Download
/// Pictures (which asks for the body itself) nor a body is on its way.
@MainActor
public func reloadAfterDownload(_ lm: LoadedMessage?) -> Bool {
    guard let lm, !lm.loadingPictures, !lm.fetching else {
        return false
    }
    return remotePictures(lm.body) > 0
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
