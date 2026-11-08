// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/updates/updates.go: the view logic of the update check
// (docs/plans/update-check.md): the banner in the main window, the question
// a security release asks once, the What's New dialog, the status line and
// Check Now of Settings → General, and the texts of the preference and the
// first-run note.
//
// The daemon checks and decides what is offered (update.*, docs/api.md
// §4.20); this only turns an `UpdateStatus` into texts and small view
// models, one to one with the Go package. Go's Translator is the gettext
// shim here: every text goes through L10n with the GTK msgid as the key.
//
// The flow (the application's `UpdateController` and its views):
//
//   - at start and on every notify.updateChanged, call update.status with
//     the locale and keep the result;
//   - show `bannerFor(status, now)` in the main window (nothing in a
//     window that is not the main one);
//   - when `promptFor(status)` is not nil, ask it once (download, then
//     quit) and call update.dismiss {version, "prompted"} whatever the
//     answer, so that no other window or client asks again;
//   - Download opens `available.url` in the browser (`openableURL`), What's
//     New shows `notesFor`, Later and Skip call update.dismiss, a notice's
//     Close calls update.dismissNotice;
//   - Settings → General shows `aboutLine` and Check Now (update.check).
//
// The notes and the notice text come from the signed manifest and were
// checked by the daemon; they are still shown as plain text only.

import Foundation

/// The updates package: a namespace, so the Go names map 1:1
/// (`updates.BannerFor` → `Updates.bannerFor`).
public enum Updates {
    /// updates.BannerKind: what the banner is, which picks its look.
    public enum BannerKind: Sendable, Equatable {
        /// No banner.
        case none
        /// A newer version (information).
        case update
        /// A security release (warning); it stays until the update is
        /// installed.
        case security
        /// A message of the project (information).
        case notice
    }

    /// updates.Action: what a banner button does.
    public enum Action: Sendable, Equatable {
        /// Opens `Banner.url` (the package) in the browser.
        case download
        /// Shows `notesFor`.
        case whatsNew
        /// update.dismiss {Banner.version, "later"}.
        case later
        /// update.dismiss {Banner.version, "skip"}.
        case skip
        /// Opens `Banner.url` (the notice's page).
        case moreInfo
        /// update.dismissNotice {Banner.noticeID}.
        case close
    }

    /// updates.Button: one button of the banner, in display order; the
    /// first is the default one.
    public struct Button: Sendable, Equatable {
        public var action: Action
        public var label: String

        public init(_ action: Action, _ label: String) {
            self.action = action
            self.label = label
        }
    }

    /// updates.Banner: the bar at the top of the main window.
    public struct Banner: Sendable, Equatable {
        public var kind: BannerKind = .none
        /// Plain text; a notice's may be long.
        public var title = ""
        public var buttons: [Button] = []
        /// `.update`, `.security`.
        public var version = ""
        /// The package, or the notice's page; "" when none.
        public var url = ""
        /// `.notice`.
        public var noticeID = ""

        public init() {}

        /// Banner.Shown: a banner to show.
        public var shown: Bool { kind != .none }
    }

    /// active: the check is on and the build has a channel.
    static func active(_ s: UpdateStatus?) -> Bool {
        guard let s else { return false }
        return s.enabled && s.state != .disabled && s.state != .unsupported
    }

    /// updates.BannerFor: the banner of `s` at `now`: an offered version
    /// that the user has not skipped or put off, else a notice not yet
    /// dismissed, else none. A security release is never skipped or put
    /// off.
    public static func bannerFor(_ s: UpdateStatus?, now: Date) -> Banner {
        guard active(s), let s else { return Banner() }
        if let a = s.available {
            let url = openableURL(a.url) ?? ""
            if a.security {
                var b = Banner()
                b.kind = .security
                // TRANSLATORS: %s is a version number such as 0.2.1
                b.title = L10n.T("Malachi Mail %s fixes a security problem. Please update.", a.version)
                b.version = a.version
                b.url = url
                if !url.isEmpty {
                    b.buttons.append(Button(.download, L10n.T("Download")))
                }
                b.buttons.append(Button(.whatsNew, L10n.T("What's New")))
                return b
            }
            let putOff = a.remindAfter.map { now < $0 } ?? false
            if !a.skipped && !putOff {
                var b = Banner()
                b.kind = .update
                // TRANSLATORS: %s is a version number such as 0.2.1
                b.title = L10n.T("Malachi Mail %s is available.", a.version)
                b.version = a.version
                b.url = url
                if !url.isEmpty {
                    b.buttons.append(Button(.download, L10n.T("Download")))
                }
                b.buttons += [
                    Button(.whatsNew, L10n.T("What's New")),
                    Button(.later, L10n.T("Remind Me Later")),
                    Button(.skip, L10n.T("Skip This Version")),
                ]
                return b
            }
        }
        if let n = s.notice, !n.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            var b = Banner()
            b.kind = .notice
            b.title = n.text
            b.noticeID = n.id
            if let u = openableURL(n.url ?? "") {
                b.url = u
                b.buttons.append(Button(.moreInfo, L10n.T("More Information")))
            }
            b.buttons.append(Button(.close, L10n.T("Close")))
            return b
        }
        return Banner()
    }

    /// updates.Prompt: the question a security release asks once.
    public struct Prompt: Sendable, Equatable {
        public var version: String
        /// The package.
        public var url: String
        public var title: String
        public var body: String
        /// The default button.
        public var download: String
        public var cancel: String
        /// Labels the check box, on by default: after the browser opened
        /// the download, the app quits so that the installer can replace
        /// it.
        public var quit: String
    }

    /// updates.PromptFor: the question to ask: a security release no
    /// client has asked about yet (`UpdateInfo.prompt`) with a package to
    /// open. nil: none.
    public static func promptFor(_ s: UpdateStatus?) -> Prompt? {
        guard active(s), let a = s?.available, a.security, a.prompt, let u = openableURL(a.url) else { return nil }
        return Prompt(
            version: a.version,
            url: u,
            title: L10n.T("Security Update Available"),
            // TRANSLATORS: %s is a version number such as 0.2.1
            body: L10n.T(
                "Malachi Mail %s fixes a security problem. Download the installer and install it once Malachi Mail has quit.",
                a.version),
            download: L10n.T("Download"),
            cancel: L10n.T("Cancel"),
            quit: L10n.T("Quit Malachi Mail after the download starts"))
    }

    /// updates.Notes: the What's New dialog.
    public struct Notes: Sendable, Equatable {
        public var title: String
        /// The release date.
        public var subtitle: String
        /// The notes as written (Markdown).
        public var body: String
        /// `body` read as Markdown (`Assistant.markdown`: headings, lists,
        /// bold, italic, code, never HTML), every link as plain text: the
        /// way out of the dialog is More Information.
        public var blocks: [Assistant.Block]
        public var download: String
        /// The package; "" hides Download.
        public var url: String
        public var moreInfo: String
        /// `newsURL`; "" hides More Information.
        public var infoURL: String
        public var close: String
    }

    /// updates.NotesFor: the What's New dialog of the offered version;
    /// `date` formats the release date in the user's locale. nil: nothing
    /// is offered.
    public static func notesFor(_ s: UpdateStatus?, date: (Date) -> String) -> Notes? {
        guard active(s), let a = s?.available else { return nil }
        return Notes(
            // TRANSLATORS: %s is a version number such as 0.2.1
            title: L10n.T("What's New in Malachi Mail %s", a.version),
            // TRANSLATORS: %s is a date
            subtitle: L10n.T("Released %s", date(a.publishedAt)),
            body: trimSpace(a.notes),
            blocks: notesBlocks(trimSpace(a.notes)),
            download: L10n.T("Download"),
            url: openableURL(a.url) ?? "",
            moreInfo: L10n.T("More Information"),
            infoURL: newsURL,
            close: L10n.T("Close"))
    }

    /// updates.NewsURL: the page More Information opens, the project's
    /// news (rather than the release's infoUrl).
    public static let newsURL = "https://malachimail.app/news.html"

    /// updates.NotesBlocks: notes read as Markdown, links as plain text.
    public static func notesBlocks(_ notes: String) -> [Assistant.Block] {
        var blocks = Assistant.markdown(notes)
        for i in blocks.indices {
            for j in blocks[i].spans.indices {
                blocks[i].spans[j].link = ""
            }
        }
        return blocks
    }

    /// updates.AboutLine: the status line of Settings → General (and the
    /// About panel); `date` formats the time of the last check.
    public static func aboutLine(_ s: UpdateStatus?, date: (Date) -> String) -> String {
        guard let s else { return "" }
        switch s.state {
        case .disabled:
            return L10n.T("Automatic update checks are off.")
        case .unsupported:
            return L10n.T("This build does not check for updates.")
        case .checking:
            return L10n.T("Checking for updates…")
        case .available:
            if let a = s.available {
                // TRANSLATORS: %s is a version number such as 0.2.1
                return L10n.T("Malachi Mail %s is available.", a.version)
            }
        case .failed:
            return L10n.T("Could not check for updates.")
        case .upToDate:
            if let at = s.checkedAt {
                // TRANSLATORS: %s is the date and time of the last check
                return L10n.T("You are using the latest version (checked %s).", date(at))
            }
            return L10n.T("You are using the latest version.")
        default:
            break
        }
        return L10n.T("Not checked for updates yet.")
    }

    /// updates.CheckNow: labels the Check Now button.
    public static var checkNow: String { L10n.T("Check Now") }

    /// updates.CanCheckNow: Check Now is enabled (the check is on, the
    /// build has a channel, no check is running).
    public static func canCheckNow(_ s: UpdateStatus?) -> Bool {
        active(s) && s?.state != .checking
    }

    /// updates.PrefsGroup, PrefsTitle and PrefsSubtitle: the group, the
    /// switch and its explanation in Settings → General.
    public static var prefsGroup: String { L10n.T("Updates") }

    public static var prefsTitle: String { L10n.T("Check for Updates Automatically") }

    public static var prefsSubtitle: String {
        L10n.T("Once a day, Malachi Mail asks its website which versions exist. It sends only its version number: nothing about you, your mail or your computer.")
    }

    /// updates.WizardNote: the sentence of the first run (the No Accounts
    /// page).
    public static var wizardNote: String {
        L10n.T("Malachi Mail checks once a day whether a new version exists and sends only its version number. You can turn this off in Preferences.")
    }

    /// downloadHosts: the hosts a package or page may be on
    /// (docs/services.md §2.3); the daemon checked them already.
    static let downloadHosts = ["github.com", "malachimail.app"]

    /// updates.OpenableURL: `raw` when it is an https URL on github.com or
    /// malachimail.app, exactly so (the daemon's rule, checked again before
    /// a browser opens it); nil otherwise. Go's url.Parse is replayed for
    /// what can pass the byte checks: the query is cut first, the
    /// authority ends at the first "/", an "@" in it is a user, the host
    /// must equal an allowed one (no port), and a "%" in the path must
    /// start a valid escape.
    public static func openableURL(_ raw: String) -> String? {
        let bytes = Array(raw.utf8)
        let prefix = Array("https://".utf8)
        guard !bytes.isEmpty, bytes.count <= 2048, bytes.starts(with: prefix) else { return nil }
        for b in bytes {
            if b <= 0x20 || b >= 0x7f || b == UInt8(ascii: "#") || b == UInt8(ascii: "\\") {
                return nil
            }
        }
        var rest = bytes[prefix.count...]
        if let q = rest.firstIndex(of: UInt8(ascii: "?")) {
            rest = rest[rest.startIndex..<q]
        }
        var authority = rest
        var path = rest[rest.endIndex...]
        if let slash = rest.firstIndex(of: UInt8(ascii: "/")) {
            authority = rest[rest.startIndex..<slash]
            path = rest[slash...]
        }
        guard !authority.contains(UInt8(ascii: "@")), validEscapes(path) else { return nil }
        let host = String(decoding: authority, as: UTF8.self)
        return downloadHosts.contains(host) ? raw : nil
    }

    /// Every "%" of `s` starts "%XX" with two hex digits (Go's unescape in
    /// encodePath mode).
    private static func validEscapes(_ s: ArraySlice<UInt8>) -> Bool {
        var i = s.startIndex
        while i < s.endIndex {
            if s[i] == UInt8(ascii: "%") {
                guard i + 2 < s.endIndex, isHex(s[i + 1]), isHex(s[i + 2]) else { return false }
                i += 3
                continue
            }
            i += 1
        }
        return true
    }

    private static func isHex(_ b: UInt8) -> Bool {
        (b >= 0x30 && b <= 0x39) || (b >= 0x41 && b <= 0x46) || (b >= 0x61 && b <= 0x66)
    }

    /// strings.TrimSpace: Unicode white space at both ends.
    static func trimSpace(_ s: String) -> String {
        s.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
