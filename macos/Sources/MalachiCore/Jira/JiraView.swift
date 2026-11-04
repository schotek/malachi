// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/jira/jira.go: the view logic of issue-tracker accounts (kind
// jira, docs/api.md): what the sidebar, the message list and the reading
// pane show for Jira spaces, issues, comments and status or assignee
// changes, and which link opens an issue. The daemon does the work
// (synchronisation, sanitising, comments); this only turns its API values
// into texts and small view models, one to one with the Go package
// (JiraWizard.swift ports wizard.go, JiraCompose.swift compose.go). Go's
// Translator is the gettext shim here: every text goes through L10n with
// the GTK msgid as the key.
//
// Every string of an issue except its key and URL is display text from the
// site and hostile input: `clean` makes it safe to lay out on one line and
// the views show it as plain text only. Brand names (Jira, Jira Cloud, Jira
// Data Center, id.atlassian.com) are not translated.
//
// Where Swift cannot follow Go literally: a String never holds invalid
// UTF-8, so the replacement character (what invalid bytes decode to)
// stands for it and is dropped or refused, as certtrust.cleanText does;
// url.Parse is `parseURL` (JiraURL.swift) over URLSyntax.

import Foundation

/// The jira package: a namespace, so the Go names map 1:1
/// (`jira.IssueCard` → `Jira.issueCard`).
public enum Jira {
    /// jira.KindBadge: the capsule next to a Jira account's name in the
    /// sidebar. A brand name, never translated.
    public static let kindBadge = "JIRA"

    /// jira.CloudName: the brand name of the cloud deployment, never
    /// translated.
    public static let cloudName = "Jira Cloud"

    /// jira.DataCenterName: the brand name of the self-hosted deployment,
    /// never translated.
    public static let dataCenterName = "Jira Data Center"

    /// jira.emptyValue: a missing side of a change (an em dash).
    static let emptyValue = "—"

    /// jira.maxText: the cap of a cleaned display string, in UTF-8 bytes.
    static let maxText = 512

    /// jira.StatusStyle: how a status pill is coloured, by the status's
    /// category. The raw value is the GTK style class; the AppKit views map
    /// it to their colours (grey, blue, green; `plain` has no colour).
    public enum StatusStyle: String, Sendable, Equatable {
        case plain = ""
        case todo = "status-todo"
        case inProgress = "status-in-progress"
        case done = "status-done"
    }

    /// jira.StyleOf: the style of a status category; an unknown or empty
    /// category is `plain` (the category is an open enum).
    public static func styleOf(_ c: IssueStatusCategory?) -> StatusStyle {
        guard let c else { return .plain }
        switch c {
        case .todo:
            return .todo
        case .inProgress:
            return .inProgress
        case .done:
            return .done
        default:
            return .plain
        }
    }

    /// jira.Clean: display text from the site made safe to lay out on one
    /// line: drops invalid UTF-8 (the replacement character here), format
    /// characters (Cf: bidirectional overrides such as U+202E, zero-width
    /// characters, the soft hyphen) and control characters, turns line
    /// breaks, tabs and the line and paragraph separators into spaces,
    /// collapses runs of spaces, trims, and caps the result at `maxText`
    /// bytes on a character boundary.
    public static func clean(_ s: String) -> String {
        var out = String.UnicodeScalarView()
        var space = false
        for r in s.unicodeScalars {
            if r.value == 0xFFFD {
                continue
            }
            // unicode.IsSpace is White_Space, which holds Zl and Zp.
            if r.properties.isWhitespace {
                space = !out.isEmpty
                continue
            }
            switch r.properties.generalCategory {
            case .control, .format:
                continue
            default:
                break
            }
            if space {
                out.append(" ")
                space = false
            }
            out.append(r)
        }
        return truncate(String(out), maxText)
    }

    /// jira.truncate: `s` cut to at most `n` UTF-8 bytes on a character
    /// boundary, spaces after the cut trimmed.
    static func truncate(_ s: String, _ n: Int) -> String {
        if s.utf8.count <= n {
            return s
        }
        var out = String.UnicodeScalarView()
        var bytes = 0
        for r in s.unicodeScalars {
            let w = UTF8.width(r)
            if bytes + w > n {
                break
            }
            bytes += w
            out.append(r)
        }
        while out.last == " " {
            out.removeLast()
        }
        return String(out)
    }

    /// strings.TrimSpace: only White_Space goes.
    static func trimSpace(_ s: String) -> String {
        let scalars = s.unicodeScalars
        guard let first = scalars.firstIndex(where: { !$0.properties.isWhitespace }),
              let last = scalars.lastIndex(where: { !$0.properties.isWhitespace }) else { return "" }
        return String(scalars[first...last])
    }

    /// jira.DeploymentName: the brand name of a deployment; "Jira" for an
    /// unknown one.
    public static func deploymentName(_ d: JiraDeployment) -> String {
        switch d {
        case .cloud:
            return cloudName
        case .datacenter:
            return dataCenterName
        default:
            return "Jira"
        }
    }

    /// jira.IsJira: an account of kind jira.
    public static func isJira(_ cfg: AccountConfig) -> Bool {
        cfg.protocolKind == .jira
    }

    /// jira.AlwaysThreaded: an account whose folders are always listed as
    /// conversations (thread.list), whatever the "group by conversation"
    /// setting: a Jira issue is one thread.
    public static func alwaysThreaded(_ cfg: AccountConfig) -> Bool {
        isJira(cfg)
    }

    /// jira.SiteHost: the host of a Jira account's site, lower-case and
    /// without port ("acme.atlassian.net"); "" for another kind of account
    /// or a site that is not a URL.
    public static func siteHost(_ cfg: AccountConfig) -> String {
        guard isJira(cfg), let jc = cfg.jira, let u = parseURL(trimSpace(jc.siteUrl)) else { return "" }
        return clean(u.hostname.lowercased())
    }

    /// jira.AccountLabel: the name the sidebar and the settings show for an
    /// account: its name; for a Jira account without one the site's host;
    /// else its e-mail address (model.go accountLabel for mail accounts).
    public static func accountLabel(_ cfg: AccountConfig) -> String {
        let name = trimSpace(cfg.name)
        if !name.isEmpty {
            return name
        }
        let host = siteHost(cfg)
        if !host.isEmpty {
            return host
        }
        return trimSpace(cfg.email)
    }

    /// jira.VirtualFolderTitle: the localised name of a fixed view of a
    /// Jira account; "" for no view or one this client does not know (show
    /// the folder's name then).
    public static func virtualFolderTitle(_ v: VirtualFolder?) -> String {
        guard let v else { return "" }
        switch v {
        case .assignedToMe:
            // TRANSLATORS: a folder of a Jira account: the issues assigned to the user.
            return L10n.C("folder", "Assigned to Me")
        case .watching:
            // TRANSLATORS: a folder of a Jira account: the issues the user watches.
            return L10n.C("folder", "Watching")
        case .open:
            // TRANSLATORS: a folder of a Jira account: the issues that are not closed yet.
            return L10n.C("folder", "Open")
        default:
            return ""
        }
    }

    /// jira.VirtualRank: the order of the fixed views in the sidebar: below
    /// the account's mail role folders, above its spaces. 100 for no view
    /// or an unknown one, like an ordinary folder.
    public static func virtualRank(_ v: VirtualFolder?) -> Int {
        guard let v else { return 100 }
        switch v {
        case .assignedToMe:
            return 0
        case .watching:
            return 1
        case .open:
            return 2
        default:
            return 100
        }
    }

    /// jira.VirtualIcon: the icon of a fixed view (a GTK icon name, which
    /// the icon tables map to an SF Symbol); "" for no view.
    public static func virtualIcon(_ v: VirtualFolder?) -> String {
        guard let v, !v.rawValue.isEmpty else { return "" }
        return "folder-saved-search-symbolic"
    }

    /// jira.CardRow: one line of the issue card's grid, a field and its
    /// value. `missing` marks a placeholder value ("Unassigned", "None"),
    /// shown dimmed.
    public struct CardRow: Sendable, Equatable {
        public var label = ""
        public var value = ""
        public var missing = false
    }

    /// jira.Card: the issue card above a Jira message in the reading pane:
    /// the key as a link to the issue, the summary, the status pill, the
    /// grid of the other fields, and what the message is of the issue.
    public struct Card: Sendable, Equatable {
        /// `key` opens `url` in the browser (after `isIssueURL`);
        /// `openTooltip` is the link's tooltip.
        public var key = ""
        public var url = ""
        public var openTooltip = ""
        public var summary = ""
        /// The pill's text ("" hides the pill), its accessible name and
        /// its colour.
        public var status = ""
        public var statusLabel = ""
        public var statusStyle = StatusStyle.plain
        /// Assignee, Priority, Type and Reporter, in this order.
        public var rows: [CardRow] = []
        /// An internal comment of a service-desk issue: the card shows the
        /// badge `internalLabel` ("" when not internal).
        public var `internal` = false
        public var internalLabel = ""
        /// `via` names the integration that posted the comment for its
        /// author ("via Issue Sync"); `edited` says the comment was changed
        /// after it was posted. "" when not.
        public var via = ""
        public var edited = ""
    }

    /// jira.IssueCard: the card of `issue`; `item`, when not nil, is the
    /// message's part of it (`MessageSummary.issue`) and adds the internal
    /// badge, `via` and `edited`.
    public static func issueCard(_ issue: IssueInfo, item: MessageIssue? = nil) -> Card {
        let key = clean(issue.key)
        var c = Card(
            key: key,
            url: trimSpace(issue.url),
            // TRANSLATORS: tooltip of an issue key such as "ITSD-42".
            openTooltip: L10n.T("Open %s in the Browser", key),
            summary: clean(issue.summary),
            status: clean(issue.status),
            // TRANSLATORS: a field of a Jira issue.
            statusLabel: L10n.T("Status"),
            statusStyle: styleOf(issue.statusCategory)
        )
        func none(_ v: String?) -> CardRow {
            let v = clean(v ?? "")
            if !v.isEmpty {
                return CardRow(value: v)
            }
            // TRANSLATORS: the value of an empty field of a Jira issue (priority, type, reporter).
            return CardRow(value: L10n.C("jira value", "None"), missing: true)
        }
        var assignee = CardRow(value: clean(issue.assignee ?? ""))
        if assignee.value.isEmpty {
            assignee = CardRow(value: L10n.T("Unassigned"), missing: true)
        }
        // TRANSLATORS: a field of a Jira issue: the person who works on it.
        assignee.label = L10n.T("Assignee")
        var priority = none(issue.priority)
        // TRANSLATORS: a field of a Jira issue.
        priority.label = L10n.T("Priority")
        var kind = none(issue.type)
        // TRANSLATORS: a field of a Jira issue: its type, such as Bug or Task.
        kind.label = L10n.T("Type")
        var reporter = none(issue.reporter)
        // TRANSLATORS: a field of a Jira issue: the person who created it.
        reporter.label = L10n.T("Reporter")
        c.rows = [assignee, priority, kind, reporter]
        guard let item else { return c }
        if isInternal(item) {
            c.internal = true
            c.internalLabel = internalLabel()
        }
        let via = clean(item.via ?? "")
        if !via.isEmpty {
            // TRANSLATORS: %s is an integration (a bot) that posted a comment on its author's behalf.
            c.via = L10n.T("via %s", via)
        }
        if item.edited == true {
            // TRANSLATORS: a comment of a Jira issue was changed after it was posted.
            c.edited = L10n.T("Edited")
        }
        return c
    }

    /// jira.IsInternal: an internal comment of a service-desk issue.
    public static func isInternal(_ item: MessageIssue?) -> Bool {
        guard let item else { return false }
        return item.item == .comment && item.visibility == .internal
    }

    /// jira.InternalLabel: the badge of an internal comment.
    public static func internalLabel() -> String {
        // TRANSLATORS: badge of a comment only the service-desk team can read.
        L10n.C("jira", "Internal")
    }

    /// jira.IsEvent: a message that stands for status or assignee changes.
    public static func isEvent(_ item: MessageIssue?) -> Bool {
        item?.item == .event
    }

    /// jira.EventLines: the sentences of an event message, one per change
    /// it knows ("Status: To Do → In Progress"); a change of a field this
    /// client does not know is skipped (the field is an open enum). An
    /// empty side is "Unassigned" for the assignee and "—" otherwise.
    public static func eventLines(_ changes: [IssueChange]) -> [String] {
        var out: [String] = []
        for ch in changes {
            var from = clean(ch.from ?? "")
            var to = clean(ch.to ?? "")
            switch ch.field {
            case .status:
                // TRANSLATORS: an issue's status changed; the first %s is the old status, the second the new one.
                out.append(L10n.T("Status: %s → %s", orEmpty(from), orEmpty(to)))
            case .assignee:
                if from.isEmpty {
                    from = L10n.T("Unassigned")
                }
                if to.isEmpty {
                    to = L10n.T("Unassigned")
                }
                // TRANSLATORS: an issue was assigned to someone else; the first %s is the old assignee, the second the new one.
                out.append(L10n.T("Assignee: %s → %s", from, to))
            default:
                continue
            }
        }
        return out
    }

    /// jira.EventText: `eventLines` on one line, for the message list.
    public static func eventText(_ changes: [IssueChange]) -> String {
        let lines = eventLines(changes)
        if lines.isEmpty {
            return ""
        }
        // TRANSLATORS: put between two changes of an issue on one line ("Status: A → B; Assignee: C → D").
        return lines.joined(separator: L10n.C("change list separator", "; "))
    }

    private static func orEmpty(_ s: String) -> String {
        s.isEmpty ? emptyValue : s
    }

    /// jira.IssueRow: what a message list row shows of an issue: the key,
    /// the summary and the status pill on the subject line, the internal
    /// badge, and for an event the changes instead of the preview.
    public struct IssueRow: Sendable, Equatable {
        public var key = ""
        public var summary = ""
        public var status = ""
        public var statusStyle = StatusStyle.plain
        /// An internal comment; `internalLabel` is its badge ("" when not
        /// internal).
        public var `internal` = false
        public var internalLabel = ""
        /// An event row (for a conversation: its latest member is one):
        /// `eventText` replaces the preview, and the row has the secondary
        /// style.
        public var event = false
        public var eventText = ""
        /// Whether the row shows as unread. An event never does, whatever
        /// its flags.
        public var unread = false
    }

    /// jira.RowIssue: the issue part of a message row; nil for a message of
    /// a mail account.
    public static func rowIssue(_ s: MessageSummary) -> IssueRow? {
        guard let issue = s.issue else { return nil }
        var r = newRow(issue.info, issue)
        r.unread = !r.event && !s.flags.contains(.seen)
        return r
    }

    /// jira.ThreadRowIssue: the issue part of a conversation row: the
    /// thread's issue, and the badge or the event text of its latest
    /// member. nil for a conversation of a mail account.
    public static func threadRowIssue(_ t: ThreadSummary) -> IssueRow? {
        guard let info = t.issue ?? t.latest.issue?.info else { return nil }
        var r = newRow(info, t.latest.issue)
        r.unread = t.unreadCount > 0
        return r
    }

    private static func newRow(_ info: IssueInfo, _ item: MessageIssue?) -> IssueRow {
        var r = IssueRow(
            key: clean(info.key),
            summary: clean(info.summary),
            status: clean(info.status),
            statusStyle: styleOf(info.statusCategory)
        )
        if isInternal(item) {
            r.internal = true
            r.internalLabel = internalLabel()
        }
        if let item, isEvent(item) {
            r.event = true
            r.eventText = eventText(item.changes)
        }
        return r
    }

    /// jira.AuthBannerText: the sign-in banner of a Jira account for a
    /// notify.authRequired reason; `account` is the account's display name.
    /// "" for another kind of account, and for a reason whose sentence is
    /// the mail accounts' (a keyring failure).
    public static func authBannerText(kind: AccountKind, reason: ErrorCode, account: String) -> String {
        guard kind == .jira else { return "" }
        switch reason {
        case .authRequired:
            // TRANSLATORS: banner; %s is an account name.
            return L10n.T("No API token is stored for %s", account)
        case .authFailed:
            // TRANSLATORS: banner; %s is an account name.
            return L10n.T("The Jira site rejected the token of %s", account)
        default:
            return ""
        }
    }

    /// jira.IsIssueURL: a link that may be opened as an issue of the site
    /// `siteURL` (`JiraConfig.siteUrl`): an absolute https URL, or http
    /// when the site itself is http, without user info, whose host is the
    /// site's (ignoring case and one trailing dot; an internationalised
    /// name matches its punycode form) on the same port. The raw text must
    /// be valid UTF-8 (no replacement character here) without spaces,
    /// control or format characters and backslashes, and its authority
    /// without '%' (parsers disagree about those). Anything else is
    /// refused.
    public static func isIssueURL(_ raw: String, siteURL: String) -> Bool {
        guard let site = parseURL(trimSpace(siteURL)), site.scheme == "https" || site.scheme == "http",
              !site.host.isEmpty else { return false }
        for r in raw.unicodeScalars {
            if r.value <= 0x20 || r.value == 0x7F || r == "\\" || r.value == 0xFFFD || r.properties.isWhitespace {
                return false
            }
            switch r.properties.generalCategory {
            case .control, .format:
                return false
            default:
                continue
            }
        }
        guard let u = parseURL(raw), !u.opaque, !u.hasUser, !u.host.isEmpty else { return false }
        switch u.scheme {
        case "https":
            break
        case "http":
            if site.scheme != "http" {
                return false
            }
        default:
            return false
        }
        let bytes = Array(raw.utf8)
        let prefix = Array((u.scheme + "://").utf8)
        guard bytes.count >= prefix.count, zip(bytes, prefix).allSatisfy({ lowerASCII($0) == lowerASCII($1) }) else {
            return false
        }
        var authority = bytes[prefix.count...]
        if let end = authority.firstIndex(where: { $0 == UInt8(ascii: "/") || $0 == UInt8(ascii: "?") || $0 == UInt8(ascii: "#") }) {
            authority = authority[..<end]
        }
        if authority.contains(UInt8(ascii: "%")) || authority.contains(UInt8(ascii: "@")) {
            return false
        }
        guard let host = hostKey(u.hostname), let want = hostKey(site.hostname),
              host.utf8.elementsEqual(want.utf8) else { return false }
        guard let port = portOf(u), let sitePort = portOf(site) else { return false }
        return port == sitePort
    }

    /// An ASCII letter in lower case, any other byte as it is (the
    /// strings.EqualFold of the scheme prefix, which url.Parse only takes
    /// from ASCII letters).
    private static func lowerASCII(_ b: UInt8) -> UInt8 {
        b >= UInt8(ascii: "A") && b <= UInt8(ascii: "Z") ? b + 32 : b
    }

    /// jira.portOf: the URL's port, or its scheme's default.
    static func portOf(_ u: ParsedURL) -> Int? {
        let p = u.port
        if p.isEmpty {
            return u.scheme == "http" ? 80 : 443
        }
        guard let n = Int(p), n > 0, n <= 65535 else { return nil }
        return n
    }

    /// jira.hostKey: the form two host names are compared in: lower case,
    /// one trailing dot dropped, every non-ASCII label as its punycode
    /// "xn--" form. An IPv6 literal (`hostname` drops its brackets) is
    /// compared lower case. nil for an empty name or an empty label.
    /// Lower case is Swift's full mapping where Go maps rune by rune; the
    /// two differ only on a few non-ASCII letters, and both hosts go
    /// through the same mapping.
    static func hostKey(_ host: String) -> String? {
        if host.unicodeScalars.contains(":") {
            return host.isEmpty ? nil : host.lowercased()
        }
        var scalars = Array(host.unicodeScalars)
        if scalars.last == "." {
            scalars.removeLast()
        }
        if scalars.isEmpty {
            return nil
        }
        let lower = String(String.UnicodeScalarView(scalars)).lowercased()
        var labels: [String] = []
        for label in lower.unicodeScalars.split(separator: ".", omittingEmptySubsequences: false) {
            if label.isEmpty {
                return nil
            }
            let text = String(String.UnicodeScalarView(label))
            if label.allSatisfy(\.isASCII) {
                labels.append(text)
                continue
            }
            guard let enc = punycode(text) else { return nil }
            labels.append("xn--" + enc)
        }
        return labels.joined(separator: ".")
    }

    // Punycode parameters (RFC 3492 §5).
    private static let pcBase = 36
    private static let pcTMin = 1
    private static let pcTMax = 26
    private static let pcSkew = 38
    private static let pcDamp = 700
    private static let pcInitialBias = 72
    private static let pcInitialN = 128
    /// Far above any label; guards the arithmetic.
    private static let pcMaxDelta = 1 << 30

    /// jira.punycode: a label encoded by RFC 3492 §6.3 without the "xn--"
    /// prefix; the ASCII characters are kept as they are. nil on overflow
    /// or an invalid character (the replacement character).
    static func punycode(_ label: String) -> String? {
        let runes = label.unicodeScalars.map { Int($0.value) }
        var out: [UInt8] = []
        for r in runes {
            if r == 0xFFFD {
                return nil
            }
            if r < pcInitialN {
                out.append(UInt8(r))
            }
        }
        let basic = out.count
        var handled = basic
        if basic > 0 {
            out.append(UInt8(ascii: "-"))
        }
        var n = pcInitialN
        var delta = 0
        var bias = pcInitialBias
        while handled < runes.count {
            var m = 0x10FFFF + 1
            for r in runes where r >= n && r < m {
                m = r
            }
            if m - n > (pcMaxDelta - delta) / (handled + 1) {
                return nil
            }
            delta += (m - n) * (handled + 1)
            n = m
            for r in runes {
                if r < n {
                    delta += 1
                    if delta > pcMaxDelta {
                        return nil
                    }
                }
                if r != n {
                    continue
                }
                var q = delta
                var k = pcBase
                while true {
                    let t = min(max(k - bias, pcTMin), pcTMax)
                    if q < t {
                        break
                    }
                    out.append(pcDigit(t + (q - t) % (pcBase - t)))
                    q = (q - t) / (pcBase - t)
                    k += pcBase
                }
                out.append(pcDigit(q))
                bias = pcAdapt(delta, handled + 1, handled == basic)
                delta = 0
                handled += 1
            }
            delta += 1
            n += 1
        }
        return String(decoding: out, as: UTF8.self)
    }

    private static func pcDigit(_ d: Int) -> UInt8 {
        d < 26 ? UInt8(ascii: "a") + UInt8(d) : UInt8(ascii: "0") + UInt8(d - 26)
    }

    private static func pcAdapt(_ delta0: Int, _ points: Int, _ first: Bool) -> Int {
        var delta = first ? delta0 / pcDamp : delta0 / 2
        delta += delta / points
        var k = 0
        while delta > ((pcBase - pcTMin) * pcTMax) / 2 {
            delta /= pcBase - pcTMin
            k += pcBase
        }
        return k + (pcBase - pcTMin + 1) * delta / (delta + pcSkew)
    }
}
