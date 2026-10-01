// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/bulkmail/bulkmail.go: the view logic of bulk mail: the tag a
// newsletter, mailing-list or automated message carries in the message list,
// the strip above an opened message (with the Unsubscribe button), the
// confirmation before the daemon acts, the dialog that offers the sender's
// web page when the daemon could not verify a one-click request, and the
// texts of the toasts.
//
// The daemon classifies the mail and does the unsubscribing
// (`message.unsubscribe`, docs/api.md); this only turns its API values into
// texts and small view models, one to one with the Go package. Go's
// Translator is the gettext shim here: every text goes through L10n with the
// GTK msgid as the key.
//
// Every string of a message here (the list id, the domain, the target, the
// URL) is hostile input; the views show it as plain text only. A format
// argument is never parsed as a format (String(format:) takes it as a value),
// so a "%s" in a list id comes out as it is.

import Foundation

/// The bulkmail package: a namespace, so the Go names map 1:1
/// (`bulkmail.StripFor` → `Bulk.stripFor`).
public enum Bulk {
    /// bulkmail.Tag: the neutral pill next to a message's subject in the
    /// list: "" for personal mail (nil) and for a kind this client does not
    /// know.
    public static func tag(_ b: BulkInfo?) -> String {
        guard let b else { return "" }
        switch b.kind {
        case .newsletter:
            return L10n.C("message tag", "Bulk")
        case .list:
            // TRANSLATORS: tag of a message from a discussion mailing list
            return L10n.C("message tag", "Mailing List")
        case .automated:
            // TRANSLATORS: tag of a machine-sent message (receipt, ticket)
            return L10n.C("message tag", "Automated")
        default:
            return ""
        }
    }

    /// window/bulk.go `bulkMessage`: the message the strip is decided from:
    /// the cached full message when message.get answered, else the summary
    /// alone (the strip shows at once, the button follows with the offer).
    @MainActor
    public static func message(_ s: MessageSummary, _ lm: LoadedMessage?) -> Message {
        guard let lm, var m = lm.msg else { return Message(summary: s) }
        if m.summary.bulk == nil, s.bulk != nil {
            m.summary.bulk = s.bulk
        }
        return m
    }

    /// bulkmail.StripKind: what the strip above a message is, which picks
    /// its icon.
    public enum StripKind: Sendable, Equatable {
        /// No strip is shown.
        case none
        /// Bulk mail (megaphone).
        case newsletter
        /// A mailing list (people).
        case list
        /// An automated message (gear).
        case automated
        /// Bulk mail in the junk folder (warning).
        case junk
        /// The user already unsubscribed (check).
        case unsubscribed
    }

    /// bulkmail.Strip: the bar above the message body.
    public struct Strip: Sendable, Equatable {
        public var kind: StripKind
        /// Plain text, never markup.
        public var text: String
        /// The label of the button, with a mnemonic; "" = no button.
        public var action: String
        /// Asks for the warning style.
        public var warning: Bool

        public init(kind: StripKind = .none, text: String = "", action: String = "", warning: Bool = false) {
            self.kind = kind
            self.text = text
            self.action = action
            self.warning = warning
        }

        /// Strip.Visible: reports a strip to show.
        public var visible: Bool { kind != .none }
    }

    /// bulkmail.StripFor: the strip of message `m` shown from a folder of
    /// the given role. `date` formats the full date of the client's message
    /// headers.
    public static func stripFor(_ m: Message?, role: FolderRole, date: ((Date) -> String)?) -> Strip {
        guard let m, let b = m.summary.bulk else { return Strip() }
        let offer = m.unsubscribe
        switch b.kind {
        case .newsletter, .list:
            if role == .junk {
                return Strip(
                    kind: .junk,
                    text: L10n.T("Unsubscribing would confirm to the sender that your address exists."),
                    warning: true)
            }
        case .automated:
            return Strip(kind: .automated, text: L10n.T("Automated message"))
        default:
            return Strip()
        }
        if let at = offer?.unsubscribedAt, let date {
            return Strip(kind: .unsubscribed, text: L10n.T("Unsubscribed on %s", date(at)))
        }
        let isURL = offer?.method == .url
        if b.kind == .list {
            var s = Strip(kind: .list, text: L10n.T("Message from mailing list %s", listName(b, offer)))
            if offer != nil {
                s.action = isURL ? L10n.T("_Leave List…") : L10n.T("_Leave List")
            }
            return s
        }
        var s = Strip(kind: .newsletter, text: L10n.T("Bulk message from %s", senderName(b, offer)))
        if offer != nil {
            s.action = isURL ? L10n.T("_Unsubscribe…") : L10n.T("_Unsubscribe")
        }
        return s
    }

    /// bulkmail.senderName: the domain the sender writes from; the list id
    /// or the offer's target stand in for a missing one.
    static func senderName(_ b: BulkInfo?, _ offer: UnsubscribeOffer?) -> String {
        if let d = b?.domain, !d.isEmpty { return d }
        if let l = b?.listId, !l.isEmpty { return l }
        if let offer { return offer.target }
        return ""
    }

    /// bulkmail.listName: the list id of a mailing list; the domain falls in
    /// for a list without one.
    static func listName(_ b: BulkInfo?, _ offer: UnsubscribeOffer?) -> String {
        if let l = b?.listId, !l.isEmpty { return l }
        return senderName(b, offer)
    }

    /// bulkmail.subject: what the heading of an unsubscribe confirmation
    /// names: the list id of a list, else the sender's domain.
    static func subject(_ b: BulkInfo?, _ offer: UnsubscribeOffer?) -> String {
        if b?.kind == .list { return listName(b, offer) }
        return senderName(b, offer)
    }

    /// bulkmail.Confirmation: the dialog that asks before the daemon acts or
    /// a page opens. The texts are plain text; `confirm` has a mnemonic.
    public struct Confirmation: Sendable, Equatable {
        public var heading: String
        public var body: String
        public var confirm: String

        public init(heading: String = "", body: String = "", confirm: String = "") {
            self.heading = heading
            self.body = body
            self.confirm = confirm
        }
    }

    /// bulkmail.Confirm: the confirmation of the unsubscribe offer of `m`;
    /// nil when the message has none.
    public static func confirm(_ m: Message?) -> Confirmation? {
        guard let m, let offer = m.unsubscribe else { return nil }
        let b = m.summary.bulk
        switch offer.method {
        case .oneClick:
            return Confirmation(
                heading: L10n.T("Unsubscribe from %s?", subject(b, offer)),
                body: L10n.T(
                    "Malachi Mail will ask %s to stop sending these messages. The sender may still send a few more over the next days.",
                    offer.target),
                confirm: L10n.T("_Unsubscribe"))
        case .mailto:
            var heading = L10n.T("Unsubscribe from %s?", subject(b, offer))
            if b?.kind == .list {
                heading = L10n.T("Leave the mailing list %s?", listName(b, offer))
            }
            return Confirmation(
                heading: heading,
                body: L10n.T(
                    "Malachi Mail will send an unsubscribe request to %s from your account. It will appear in Sent.",
                    offer.target),
                confirm: L10n.T("_Send Request"))
        case .url:
            return Confirmation(
                heading: L10n.T("Open the unsubscribe page?"),
                body: L10n.T(
                    "The sender does not offer unsubscribing in one step. This page opens in your browser:\n%s",
                    offer.url ?? ""),
                confirm: L10n.T("_Open in Browser"))
        default:
            return nil
        }
    }

    /// bulkmail.Unverified: the dialog after the daemon answered
    /// unverified: it sent nothing because the one-click request is not
    /// signed by the sender's domain. With `res.mailto` the user may let it
    /// send an unsubscribe request to that address instead (`confirm` is
    /// the button); without, `confirm` is "" and the dialog only informs,
    /// with a single Close button (`close`).
    public static func unverified(_ m: Message?, _ res: MessageUnsubscribeResult) -> Confirmation {
        let domain = senderName(m?.summary.bulk, m?.unsubscribe)
        var c = Confirmation(heading: L10n.T("The sender could not be verified"))
        guard let mailto = res.mailto, !mailto.isEmpty else {
            c.body = L10n.T(
                "Malachi Mail sent nothing because the one-click request is not signed by %s. Use the unsubscribe link in the message instead.",
                domain)
            return c
        }
        c.body = L10n.T(
            "Malachi Mail sent nothing because the one-click request is not signed by %s. It can send an unsubscribe request to %s from your account instead. It will appear in Sent.",
            domain, mailto)
        c.confirm = L10n.T("_Send Request")
        return c
    }

    /// bulkmail.Close: the label of the single button of an information
    /// dialog.
    public static func close() -> String { L10n.T("_Close") }

    /// bulkmail.Applied: the offer of a message after `message.unsubscribe`
    /// answered `res`: a copy with the time the daemon remembered for
    /// unsubscribed and queued, the offer unchanged for any other outcome
    /// (and nil for nil). `now` stands in for a result without a time.
    public static func applied(
        _ offer: UnsubscribeOffer?, _ res: MessageUnsubscribeResult, now: Date = Date()
    ) -> UnsubscribeOffer? {
        guard var c = offer else { return nil }
        if res.outcome == .unsubscribed || res.outcome == .queued {
            c.unsubscribedAt = res.unsubscribedAt ?? now
        }
        return c
    }

    /// bulkmail.Queued: the toast after the unsubscribe request went to the
    /// outbox.
    public static func queued() -> String { L10n.T("Unsubscribe request queued") }

    /// bulkmail.ErrorWhat: the action of the error toast, in the progressive
    /// form `rpcErrorText` takes.
    public static func errorWhat() -> String { L10n.T("Unsubscribing") }

    /// bulkmail.Refused: the sentence for error 1505 (unsubscribeFailed):
    /// the sender's server refused or did not answer the one-click request.
    public static func refused() -> String { L10n.T("The sender's server refused the request.") }

    /// window/bulk.go `wantsOffer`: reports a summary whose card needs
    /// `message.get` for the unsubscribe offer.
    public static func wantsOffer(_ s: MessageSummary) -> Bool {
        guard let b = s.bulk else { return false }
        return b.kind == .newsletter || b.kind == .list
    }

    /// bulkmail.OpenableURL: `raw` when a page may be opened in the
    /// browser: an https URL with a host and no control, space or bidi
    /// characters; nil for anything else (http, javascript:, data:, a
    /// relative reference).
    public static func openableURL(_ raw: String) -> String? {
        if raw.isEmpty || raw.utf8.count > 2048 { return nil }
        for scalar in raw.unicodeScalars {
            let p = scalar.properties
            if p.generalCategory == .control || p.isWhitespace || p.generalCategory == .format {
                return nil
            }
        }
        guard let u = Jira.parseURL(raw), u.scheme.lowercased() == "https", !u.hostname.isEmpty else { return nil }
        return raw
    }
}
