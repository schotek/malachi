// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/signature signature.go: the view logic of signed mail (phase
// 1 of docs/plans/signatures.md, the daemon verifies S/MIME and OpenPGP
// signatures of incoming mail): the small icon of a message row, the
// badge in a message's header (reading pane, message window, conversation
// card), the details popover with its buttons (Check Revocation, Import
// Key, Confirm Key…), which attachment tiles a verdict hides, and
// Settings → Security with the stored OpenPGP keys.
//
// The daemon verifies and decides the verdict (message.signature, pgp.*,
// docs/api.md §4.21); this only turns its API values into texts and small
// view models. Every string of a certificate or key is hostile input: the
// daemon cleans it, `clean` cleans it again, and the views show it as
// plain text only (`stringValue`). No Security.framework: nothing is
// verified here.

/// ui/internal/signature (`signature.KindName`, `ListIconFor`, `BadgeFor`,
/// `DetailsFor`, `KeyRows`, …). Named like the Windows `SignatureView` so
/// that it never shadows a framework's type.
public enum SignatureView {
    // MARK: Names

    /// Names that stay as they are in every language.
    public static let smimeName = "S/MIME"
    public static let openPGPName = "OpenPGP"
    /// The placeholder of the import sheet's text area (the armour's first line; not translated).
    public static let armorHeader = "-----BEGIN PGP PUBLIC KEY BLOCK-----"

    /// signature.KindName: the name of a signature format ("" unknown).
    public static func kindName(_ k: SignatureKind) -> String {
        switch k {
        case .smime: return smimeName
        case .pgp: return openPGPName
        default: return ""
        }
    }

    // MARK: Severity, icon and colour

    /// The colour family of a verdict.
    public enum Severity: Int, Sendable, Equatable {
        /// Nothing is shown (no verdict).
        case none
        /// valid, green with the check mark.
        case good
        /// validSeen, green without the check mark.
        case goodSeen
        /// mismatch, untrusted, weak; yellow.
        case warning
        /// invalid, revoked; red.
        case bad
        /// unknownKey, unsupported, error (and an unknown status); grey.
        case neutral
    }

    /// signature.SeverityOf: an unknown status reads as `.error` (neutral).
    public static func severity(of s: SignatureStatus) -> Severity {
        switch s {
        case .valid: return .good
        case .validSeen: return .goodSeen
        case .mismatch, .untrusted, .weak: return .warning
        case .invalid, .revoked: return .bad
        default: return .neutral
        }
    }

    /// The icon names of the GTK client (Adwaita's symbolic icons), the keys
    /// the reference hands out; `symbolName` maps them to SF Symbols.
    public static let iconGood = "security-high-symbolic"
    public static let iconGoodSeen = "security-medium-symbolic"
    public static let iconWarning = "dialog-warning-symbolic"
    public static let iconBad = "dialog-error-symbolic"
    public static let iconNeutral = "security-low-symbolic"
    /// The icon of Settings → Security.
    public static let iconPage = "security-high-symbolic"

    /// signature.IconOf ("" for `.none`).
    public static func icon(of s: Severity) -> String {
        switch s {
        case .good: return iconGood
        case .goodSeen: return iconGoodSeen
        case .warning: return iconWarning
        case .bad: return iconBad
        case .neutral: return iconNeutral
        case .none: return ""
        }
    }

    /// The style classes of libadwaita; the views colour with `NSColor`
    /// (systemGreen, systemOrange, systemRed, secondaryLabelColor).
    public static let styleGood = "success"
    public static let styleWarning = "warning"
    public static let styleBad = "error"
    public static let styleNeutral = "dim-label"

    /// signature.StyleOf ("" for `.none`).
    public static func style(of s: Severity) -> String {
        switch s {
        case .good, .goodSeen: return styleGood
        case .warning: return styleWarning
        case .bad: return styleBad
        case .neutral: return styleNeutral
        case .none: return ""
        }
    }

    /// The SF Symbol of an icon name of the reference (plan §11.3):
    /// a full shield with the check mark, a half shield, the warning and
    /// error signs, an empty shield.
    public static func symbolName(_ icon: String) -> String {
        switch icon {
        case iconGood: return "checkmark.shield.fill"
        case iconGoodSeen: return "shield.lefthalf.filled"
        case iconWarning: return "exclamationmark.triangle.fill"
        case iconBad: return "xmark.octagon.fill"
        case iconNeutral: return "shield"
        default: return "shield"
        }
    }

    // MARK: The message list

    /// signature.ListIcon: the small icon of a message row, next to the
    /// paperclip.
    public struct ListIcon: Sendable, Equatable {
        public var icon: String
        public var style: String
        public var severity: Severity
        /// `statusTitle`.
        public var tooltip: String

        public init(icon: String, style: String, severity: Severity, tooltip: String) {
            self.icon = icon
            self.style = style
            self.severity = severity
            self.tooltip = tooltip
        }
    }

    /// signature.ListIconFor: only valid, validSeen, invalid, revoked and
    /// mismatch get one (plan §6); the grey verdicts, untrusted and weak
    /// show nothing in the list, only in the header. A collapsed thread
    /// row passes its latest member's signature.
    public static func listIcon(for sig: MessageSignature?) -> ListIcon? {
        guard let sig else { return nil }
        switch sig.status {
        case .valid, .validSeen, .invalid, .revoked, .mismatch:
            break
        default:
            return nil
        }
        let sev = severity(of: sig.status)
        return ListIcon(
            icon: icon(of: sev), style: style(of: sev), severity: sev,
            tooltip: statusTitle(sig.kind, sig.status))
    }

    // MARK: The header's badge

    /// signature.Badge: the button in a message's header that opens the
    /// details.
    public struct Badge: Sendable, Equatable {
        /// The message has a verdict; nothing else is set otherwise.
        public var visible = false
        public var severity: Severity = .none
        public var icon = ""
        public var style = ""
        /// The badge's sentence, plain text.
        public var text = ""
        /// Opens the details.
        public var tooltip = ""

        public init(
            visible: Bool = false, severity: Severity = .none, icon: String = "", style: String = "",
            text: String = "", tooltip: String = ""
        ) {
            self.visible = visible
            self.severity = severity
            self.icon = icon
            self.style = style
            self.text = text
            self.tooltip = tooltip
        }
    }

    /// signature.BadgeFor: the badge of message.get's signature (nil: no
    /// badge). The reading pane, the message window and a conversation
    /// card show it; a chat bubble and an attached message
    /// (message.embedded) do not.
    public static func badge(for s: SignatureSummary?) -> Badge {
        guard let s else { return Badge() }
        let sev = severity(of: s.status)
        return Badge(
            visible: true, severity: sev, icon: icon(of: sev), style: style(of: sev),
            text: badgeText(s, signerLabel(s.signer)), tooltip: badgeTooltip)
    }

    /// signature.SignerLabel: "Name <address>", the address alone or the
    /// name alone ("" for nil or empty), cleaned.
    public static func signerLabel(_ s: SignatureSigner?) -> String {
        guard let s else { return "" }
        let name = clean(s.name ?? ""), email = clean(s.email ?? "")
        if !name.isEmpty, !email.isEmpty, name.lowercased() != email.lowercased() {
            return name + " <" + email + ">"
        }
        if !email.isEmpty {
            return email
        }
        return name
    }

    // MARK: Attachment tiles (plan S15)

    /// signature.HidesPart: `a`'s tile is not shown, as it is the detached
    /// signature part of a message with a verdict. A message without a
    /// verdict (`sig` nil) shows its signature parts like any file.
    public static func hidesPart(_ a: Attachment, _ sig: SignatureSummary?) -> Bool {
        guard let sig, let part = sig.partId, !part.isEmpty else { return false }
        return a.partId == part
    }

    /// signature.TileAttachments: `atts` without the part `hidesPart`
    /// hides; the other rules of the tiles (inline pictures, the
    /// invitation's calendar part) apply as before.
    public static func tileAttachments(_ atts: [Attachment], _ sig: SignatureSummary?) -> [Attachment] {
        atts.filter { !hidesPart($0, sig) }
    }

    // MARK: The details

    /// signature.RowKey: names a row of the details.
    public enum RowKey: Int, Sendable, Equatable {
        case signer, kind, reason, signedAt, arrivedAt, algorithm, revocation, keyTrust, keyFingerprint, keyID
        case keySource, firstSeen, userIDs, previousKey, expired, revoked
        // Rows of a certificate section.
        case issuedTo, emails, issuedBy, validity, certFingerprint
        // A row of the signed headers' section (its label is the header's).
        case signedHeader
    }

    /// signature.Row: one line of the details, a label and a plain-text
    /// value.
    public struct Row: Sendable, Equatable {
        public var key: RowKey
        public var label: String
        public var value: String
        /// A fingerprint or key ID. Every value is selectable.
        public var monospace: Bool

        public init(key: RowKey, label: String, value: String, monospace: Bool = false) {
            self.key = key
            self.label = label
            self.value = value
            self.monospace = monospace
        }
    }

    public enum SectionKey: Int, Sendable, Equatable {
        case signerCertificate, intermediate, root, signedHeaders
    }

    /// signature.Section: a titled group of rows under the main rows.
    public struct Section: Sendable, Equatable {
        public var key: SectionKey
        public var title: String
        public var rows: [Row]

        public init(key: SectionKey, title: String, rows: [Row] = []) {
            self.key = key
            self.title = title
            self.rows = rows
        }
    }

    /// signature.Action: a button of the details or a key row.
    public enum Action: Int, Sendable, Equatable {
        case none
        /// message.signatureRevocation.
        case checkRevocation
        /// pgp.keyImport with `importParams`.
        case importKey
        /// Asks `confirmKeyQuestion`, then pgp.keyVerify `verified: true`.
        case confirmKey
        /// pgp.keyVerify `verified: false` (key rows).
        case unconfirmKey
        /// Asks `removeKeyQuestion`, then pgp.keyRemove (key rows).
        case removeKey
    }

    /// signature.Button: a button of the details.
    public struct Button: Sendable, Equatable {
        public var action: Action
        public var label: String
        public var enabled: Bool

        public init(action: Action, label: String, enabled: Bool) {
            self.action = action
            self.label = label
            self.enabled = enabled
        }
    }

    /// signature.Question: a confirmation dialog.
    public struct Question: Sendable, Equatable {
        public var heading: String
        public var body: String
        /// Button labels with mnemonics (the alert strips them); the
        /// platform's order applies.
        public var cancel: String
        public var accept: String
        /// Styles `accept`.
        public var destructive: Bool

        public init(heading: String, body: String, cancel: String, accept: String, destructive: Bool = false) {
            self.heading = heading
            self.body = body
            self.cancel = cancel
            self.accept = accept
            self.destructive = destructive
        }
    }

    /// signature.Details: the view model of the details popover: heading
    /// with icon, explanation, rows, notes, sections, the caveat and
    /// buttons.
    public struct Details: Sendable, Equatable {
        public var severity: Severity = .none
        public var icon = ""
        public var style = ""
        /// `statusTitle`; `explanation` the paragraph under it.
        public var title = ""
        public var explanation = ""
        public var rows: [Row] = []
        /// The sentences of the summary's notes, without those the
        /// Revocation row already says (revocationUnchecked,
        /// revocationGood).
        public var notes: [String] = []
        /// The S/MIME chain (signer first), then the signed headers.
        public var sections: [Section] = []
        /// The line under a valid verdict ("" otherwise).
        public var caveat = ""
        public var buttons: [Button] = []
        /// The line under Check Revocation ("" without that button).
        public var revocationCaption = ""
        /// The formatted fingerprint `confirmKeyQuestion` shows ("" without
        /// Confirm Key…).
        public var keyFingerprint = ""

        public init() {}
    }

    /// signature.DetailsFor (nil: an empty model, nothing to show). `from`
    /// is the message's From address, `date` formats a date and time like
    /// the client's message headers, and `busy` is the action that runs
    /// (its button says so and is disabled).
    public static func details(
        _ d: SignatureDetails?, from: String, busy: Action, date: (Date) -> String
    ) -> Details {
        guard let d else { return Details() }
        let s = d.summary
        let sev = severity(of: s.status)
        var out = Details()
        out.severity = sev
        out.icon = icon(of: sev)
        out.style = style(of: sev)
        out.title = statusTitle(s.kind, s.status)
        out.explanation = explanation(s, from: from)
        func add(_ k: RowKey, _ value: String, mono: Bool = false) {
            if !value.isEmpty {
                out.rows.append(Row(key: k, label: rowLabel(k), value: value, monospace: mono))
            }
        }
        add(.signer, signerLabel(s.signer))
        add(.kind, kindName(s.kind))
        add(.reason, reasonText(s.kind, d.reason))
        if let at = d.signedAt {
            add(.signedAt, date(at))
        }
        if !d.arrivedAt.isGoZero {
            add(.arrivedAt, date(d.arrivedAt))
        }
        add(.algorithm, clean(d.algorithm ?? ""))
        if s.kind == .smime, let sm = d.smime {
            add(.revocation, revocationText(sm.revocation, date: date))
        }
        if s.kind == .pgp, let p = d.pgp {
            add(.keyTrust, keyTrustText(p.keyTrust))
            var fp = p.primaryFingerprint ?? ""
            if fp.isEmpty, let signer = s.signer {
                fp = signer.fingerprint ?? ""
            }
            add(.keyFingerprint, formatFingerprint(fp), mono: true)
            if formatFingerprint(fp).isEmpty {
                add(.keyID, formatFingerprint(p.keyId ?? ""), mono: true)
            }
            add(.keySource, keySourceText(p.keySource))
            if let seen = p.firstSeen {
                add(.firstSeen, date(seen))
            }
            add(.userIDs, cleanList(p.userIds, limit: API.Limits.maxSignatureUserIDs).joined(separator: "\n"))
            add(.previousKey, formatFingerprint(p.previousFingerprint ?? ""), mono: true)
        }
        if let at = d.expiredAt {
            add(.expired, date(at))
        }
        if let at = d.revokedAt, s.kind == .pgp {
            add(.revoked, date(at))
        }

        for n in s.notes {
            if n == .revocationUnchecked || n == .revocationGood {
                continue
            }
            let t = noteText(s.kind, n)
            if !t.isEmpty {
                out.notes.append(t)
            }
        }

        if s.kind == .smime, let sm = d.smime {
            let chain = Array(sm.chain.prefix(API.Limits.maxSignatureChain))
            for (i, c) in chain.enumerated() {
                out.sections.append(certificateSection(c, sectionOf(i, chain.count, c), date: date))
            }
        }
        let hs = signedHeaderRows(d.protectedHeaders)
        if !hs.isEmpty {
            out.sections.append(Section(key: .signedHeaders, title: sectionTitle(.signedHeaders), rows: hs))
        }

        if s.status == .valid || s.status == .validSeen {
            out.caveat = caveat
        }

        func button(_ a: Action, again: Bool) {
            out.buttons.append(Button(action: a, label: buttonLabel(a, again: again, busy: busy == a), enabled: busy != a))
        }
        if canCheckRevocation(d), let sm = d.smime {
            button(.checkRevocation, again: sm.revocation.state != .unchecked)
            out.revocationCaption = revocationCaption
        }
        if importParams(d) != nil {
            button(.importKey, again: false)
        }
        if let p = confirmParams(d) {
            button(.confirmKey, again: false)
            out.keyFingerprint = formatFingerprint(p.fingerprint)
        }
        return out
    }

    /// signature.sectionOf: the section of the i-th of n certificates: the
    /// signer's first, a root last (a system root, or self-signed after the
    /// signer's), intermediates between.
    static func sectionOf(_ i: Int, _ n: Int, _ c: SignatureCertificate) -> SectionKey {
        if i == 0 {
            return .signerCertificate
        }
        if i == n - 1, c.systemRoot || c.selfSigned {
            return .root
        }
        return .intermediate
    }

    static func certificateSection(_ c: SignatureCertificate, _ k: SectionKey, date: (Date) -> String) -> Section {
        var sec = Section(key: k, title: sectionTitle(k))
        func add(_ rk: RowKey, _ value: String, mono: Bool = false) {
            if !value.isEmpty {
                sec.rows.append(Row(key: rk, label: rowLabel(rk), value: value, monospace: mono))
            }
        }
        add(.issuedTo, clean(c.subject ?? ""))
        add(.emails, cleanList(c.emails, limit: API.Limits.maxSignatureEmails).joined(separator: "\n"))
        add(.issuedBy, clean(c.issuer ?? ""))
        if !c.notBefore.isGoZero, !c.notAfter.isGoZero {
            add(.validity, validity(date(c.notBefore), date(c.notAfter)))
        }
        add(.certFingerprint, formatFingerprint(c.sha256), mono: true)
        return sec
    }

    static func signedHeaderRows(_ hs: [ProtectedHeader]) -> [Row] {
        var out: [Row] = []
        for h in hs {
            if out.count == API.Limits.maxProtectedHeaders {
                break
            }
            let label = headerName(h.name)
            if label.isEmpty {
                continue
            }
            out.append(Row(key: .signedHeader, label: label, value: clean(h.value)))
        }
        return out
    }

    /// signature.CanCheckRevocation: an S/MIME verdict whose signer's
    /// certificate names an OCSP responder or CRL the daemon may ask.
    public static func canCheckRevocation(_ d: SignatureDetails?) -> Bool {
        guard let d, d.kind == .smime, let sm = d.smime else { return false }
        return !sm.chain.isEmpty && sm.revocation.checkable
    }

    /// signature.ImportParams: the params of Import Key, the key the
    /// message offers (`PGPDetails.importable`), from its part or its
    /// Autocrypt header.
    public static func importParams(_ d: SignatureDetails?) -> PGPKeyImportParams? {
        guard let d, d.kind == .pgp, let o = d.pgp?.importable, !d.messageId.rawValue.isEmpty else { return nil }
        let part = o.partId ?? ""
        if !part.isEmpty, !o.autocrypt {
            return PGPKeyImportParams(messageId: d.messageId, partId: part)
        }
        if part.isEmpty, o.autocrypt {
            return PGPKeyImportParams(messageId: d.messageId, autocrypt: true)
        }
        return nil
    }

    /// signature.ConfirmParams: the params of Confirm Key…, a stored key
    /// that is only seen, of a verdict that is not invalid or revoked.
    public static func confirmParams(_ d: SignatureDetails?) -> PGPKeyVerifyParams? {
        guard let d, d.kind == .pgp, let p = d.pgp, p.keyTrust == .seen,
              !formatFingerprint(p.primaryFingerprint ?? "").isEmpty else { return nil }
        if d.status == .invalid || d.status == .revoked {
            return nil
        }
        return PGPKeyVerifyParams(fingerprint: p.primaryFingerprint ?? "", verified: true)
    }

    // MARK: Errors of the methods

    /// signature.RevocationFailureOf: the class of a
    /// message.signatureRevocation error ("" for another error or data
    /// without a class); `revocationFailedText` turns it into the sentence.
    public static func revocationFailure(of error: (any Error)?) -> RevocationFailure {
        (error as? RPCError)?.revocationFailedData?.reason ?? ""
    }

    /// signature.ImportFailureOf: the class of a pgp.keyImport error ("" for
    /// another error); `importFailedText` turns it into the sentence.
    public static func importFailure(of error: (any Error)?) -> PGPImportFailure {
        (error as? RPCError)?.pgpImportErrorData?.reason ?? ""
    }

    // MARK: Import

    /// signature.PastedParams: a key pasted in the import sheet; nil when
    /// there is nothing to import or it is over `maxPGPKeyBytes` (the
    /// Import button is disabled then).
    public static func pastedParams(_ text: String) -> PGPKeyImportParams? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty || t.utf8.count > API.Limits.maxPGPKeyBytes {
            return nil
        }
        return PGPKeyImportParams(armored: t)
    }

    /// signature.FileParams: a key file, ASCII armour as `armored`, anything
    /// else as binary `data`; nil for an empty file or one over
    /// `maxPGPKeyBytes` (clients read at most that + 1 bytes and show
    /// `importFailedText(.tooLarge)`).
    public static func fileParams(_ data: Data) -> PGPKeyImportParams? {
        if data.isEmpty || data.count > API.Limits.maxPGPKeyBytes {
            return nil
        }
        if let s = String(data: data, encoding: .utf8) {
            let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
            if t.hasPrefix("-----BEGIN PGP") {
                return PGPKeyImportParams(armored: t)
            }
        }
        return PGPKeyImportParams(data: data)
    }

    // MARK: Settings → Security: the stored keys

    /// signature.KeyRow: a row of the OpenPGP key list.
    public struct KeyRow: Sendable, Equatable {
        /// The key's, raw: what pgp.keyVerify and pgp.keyRemove take.
        public var fingerprint: String
        /// The first user ID (or address, or the fingerprint); `subtitle`
        /// the formatted fingerprint (monospace); `origin` where and when
        /// it came from.
        public var title: String
        public var subtitle: String
        public var origin: String
        /// "Confirmed" or "Not confirmed"; `verified` styles it as success.
        public var trustPill: String
        public var verified: Bool
        /// "Revoked", "Expired" or "".
        public var statePill: String
        /// The row's menu: Confirm Key… or Remove Confirmation, then
        /// Remove Key….
        public var actions: [Button]

        public init(
            fingerprint: String, title: String, subtitle: String, origin: String, trustPill: String,
            verified: Bool, statePill: String, actions: [Button]
        ) {
            self.fingerprint = fingerprint
            self.title = title
            self.subtitle = subtitle
            self.origin = origin
            self.trustPill = trustPill
            self.verified = verified
            self.statePill = statePill
            self.actions = actions
        }
    }

    /// signature.KeyRows: the rows of pgp.keyList's keys, sorted by title
    /// (ignoring case), then fingerprint. `now` decides what has expired;
    /// `date` formats the day a key was first seen.
    public static func keyRows(_ keys: [PGPKey], now: Date, date: (Date) -> String) -> [KeyRow] {
        keys.map { keyRow($0, now: now, date: date) }.enumerated().sorted { a, b in
            let x = a.element.title.lowercased(), y = b.element.title.lowercased()
            if x != y {
                return x < y
            }
            if a.element.fingerprint != b.element.fingerprint {
                return a.element.fingerprint < b.element.fingerprint
            }
            return a.offset < b.offset
        }.map(\.element)
    }

    /// signature.KeyRowFor: the row of one key.
    public static func keyRow(_ k: PGPKey, now: Date, date: (Date) -> String) -> KeyRow {
        let fp = formatFingerprint(k.fingerprint)
        var title = fp
        if let id = cleanList(k.userIds, limit: 1).first {
            title = id
        } else if let e = cleanList(k.emails, limit: 1).first {
            title = e
        }
        let revoked = k.revokedAt.map { $0 <= now } ?? false
        let expired = k.expiresAt.map { $0 <= now } ?? false
        let verified = k.trust == .verified
        var actions: [Button] = []
        if verified {
            actions.append(Button(action: .unconfirmKey, label: buttonLabel(.unconfirmKey, again: false, busy: false), enabled: true))
        } else if !revoked {
            actions.append(Button(action: .confirmKey, label: buttonLabel(.confirmKey, again: false, busy: false), enabled: true))
        }
        actions.append(Button(action: .removeKey, label: buttonLabel(.removeKey, again: false, busy: false), enabled: true))
        return KeyRow(
            fingerprint: k.fingerprint, title: title, subtitle: fp, origin: keyOrigin(k.source, date(k.firstSeen)),
            trustPill: keyTrustPill(k.trust), verified: verified, statePill: keyStatePill(revoked: revoked, expired: expired),
            actions: actions)
    }

    // MARK: Text helpers

    /// signature.FormatFingerprint: a fingerprint or key ID in upper-case
    /// groups of four ("AB12 CD34 …"); "" when `hex` is not 16, 40 or 64 hex
    /// digits.
    public static func formatFingerprint(_ hex: String) -> String {
        let h = Array(hex.trimmingCharacters(in: .whitespacesAndNewlines).uppercased().utf8)
        guard [16, 40, 64].contains(h.count) else { return "" }
        for c in h where !((c >= 0x30 && c <= 0x39) || (c >= 0x41 && c <= 0x46)) {
            return ""
        }
        var groups: [String] = []
        var i = 0
        while i < h.count {
            groups.append(String(decoding: h[i..<(i + 4)], as: UTF8.self))
            i += 4
        }
        return groups.joined(separator: " ")
    }

    /// signature.clean: `s` on one line, without control, format (bidi,
    /// zero-width) and private-use characters, trimmed and capped at
    /// `maxSignatureTextBytes` on a scalar boundary: the daemon cleaned it
    /// already, this is the client's own guard.
    static func clean(_ s: String) -> String {
        var kept = String.UnicodeScalarView()
        for u in s.unicodeScalars {
            let cat = u.properties.generalCategory
            if u.value == 0xFFFD || cat == .privateUse || cat == .format {
                continue
            }
            if cat == .control || u.value == 0x2028 || u.value == 0x2029 {
                kept.append(" ")
                continue
            }
            kept.append(u)
        }
        var fields: [String.UnicodeScalarView] = []
        var field = String.UnicodeScalarView()
        for u in kept {
            if u.properties.isWhitespace {
                if !field.isEmpty {
                    fields.append(field)
                    field = String.UnicodeScalarView()
                }
            } else {
                field.append(u)
            }
        }
        if !field.isEmpty {
            fields.append(field)
        }
        var out = String.UnicodeScalarView()
        var bytes = 0
        for (i, f) in fields.enumerated() {
            if i > 0 {
                out.append(" ")
            }
            out.append(contentsOf: f)
        }
        var capped = String.UnicodeScalarView()
        for u in out {
            bytes += String(u).utf8.count
            if bytes > API.Limits.maxSignatureTextBytes {
                break
            }
            capped.append(u)
        }
        return String(capped).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// signature.cleanList: every non-empty cleaned entry, at most `limit`.
    static func cleanList(_ list: [String], limit: Int) -> [String] {
        var out: [String] = []
        for s in list {
            if out.count == limit {
                break
            }
            let c = clean(s)
            if !c.isEmpty {
                out.append(c)
            }
        }
        return out
    }

    /// signature.hasNote.
    static func hasNote(_ notes: [SignatureNote], _ n: SignatureNote) -> Bool {
        notes.contains(n)
    }
}

extension SignatureView {
    /// The day a key was first seen (Settings → Security's origin line).
    public static func formatDay(_ t: Date) -> String {
        t.formatted(date: .abbreviated, time: .omitted)
    }
}
