// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// What the application keeps of signature trust once (docs/api.md §4.21,
/// §5): a number that goes up on every notify.signaturesChanged, and the
/// observers that ask again when it does: an open details popover
/// (`SignatureController`) and the key list of Settings → Security
/// (`PGPKeysController`). The messages on display ask message.get again
/// through `MessageCache.refreshSignatures`; the rows whose verdict changed
/// arrive as notify.messagesChanged. One per application (`AppState`).
@MainActor
public final class SignatureState {
    /// Counts the notifications seen.
    public private(set) var generation = 0
    private let observers = BoardObservers()

    public init() {}

    /// Calls `f` after every notify.signaturesChanged.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// notify.signaturesChanged arrived.
    public func changed() {
        generation += 1
        observers.notify()
    }
}

/// The details popover of one message's verdict (plan §6, `ui/internal/
/// signature` `DetailsFor`): message.signature fills it, Check Revocation
/// (message.signatureRevocation, 20 s), Import Key (pgp.keyImport) and
/// Confirm Key… (the question, then pgp.keyVerify) change it. Until the
/// daemon answers, or when it cannot (an older one answers `notImplemented`),
/// the popover shows what `Message.signature` carries (`fallback`).
///
/// The view draws `model` and calls `perform` for a button; `onChange` says
/// the model changed. Every text is plain.
@MainActor
public final class SignatureController {
    public let messageID: MessageID
    /// The message's From address (the explanation of a mismatch names it).
    public let from: String

    /// What message.signature (or a button's call) answered; nil until it
    /// did, and for a message without a verdict.
    public private(set) var details: SignatureDetails?
    /// The button whose call runs.
    public private(set) var busy: SignatureView.Action = .none
    /// The popover asked and nothing answered yet.
    public private(set) var loading = false

    /// The details changed: the view draws `model` again.
    public var onChange: (@MainActor () -> Void)?
    /// A transient message (a failed button, the toast of a done one).
    public var toast: (@MainActor (String) -> Void)?
    /// Asks Confirm This Key? with the key's formatted fingerprint under
    /// the text; true goes ahead.
    public var confirm: (@MainActor (SignatureView.Question, _ fingerprint: String) async -> Bool)?

    private let client: RPCClient
    private let fallback: SignatureSummary?
    private let date: @MainActor (Date) -> String
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "signature")
    /// Orders the answers of message.signature: one that ends after a newer
    /// one began is dropped. (A button's call is never dropped: at most one
    /// runs, and its end must free the button.)
    private var loadGeneration = 0

    public init(
        client: RPCClient, messageID: MessageID, from: String, fallback: SignatureSummary?,
        date: @escaping @MainActor (Date) -> String = { formatDateTime($0) }
    ) {
        self.client = client
        self.messageID = messageID
        self.from = from
        self.fallback = fallback
        self.date = date
    }

    /// The details to draw: the daemon's, else the summary's alone.
    public var current: SignatureDetails? {
        if let details {
            return details
        }
        guard let fallback else { return nil }
        return SignatureDetails(summary: fallback, messageId: messageID, arrivedAt: .goZero)
    }

    /// Fills the details without the daemon (a development aid:
    /// `MALACHI_START` `signature-popover`).
    public func seed(_ d: SignatureDetails) {
        details = d
    }

    /// The view model of `current`.
    public var model: SignatureView.Details {
        let d = date
        return SignatureView.details(current, from: from, busy: busy) { d($0) }
    }

    /// message.signature; also what notify.signaturesChanged asks for.
    public func load() {
        loadGeneration += 1
        let my = loadGeneration
        loading = details == nil
        onChange?()
        let client = client, id = messageID
        Task { [weak self] in
            let outcome: Result<MessageSignatureResult, any Error>
            do {
                outcome = .success(try await client.call(API.MessageSignatureGet.self, MessageSignatureParams(messageId: id)))
            } catch {
                outcome = .failure(error)
            }
            guard let self, my == self.loadGeneration else { return }
            self.loading = false
            switch outcome {
            case .success(let r):
                if let d = r.signature {
                    self.details = d
                }
            case .failure(let error):
                // The summary the message carries stays; nothing to say.
                self.log.info("message.signature: \(String(describing: error), privacy: .public)")
            }
            self.onChange?()
        }
    }

    /// A button of the popover.
    public func perform(_ action: SignatureView.Action) {
        guard busy == .none else { return }
        switch action {
        case .checkRevocation:
            checkRevocation()
        case .importKey:
            importKey()
        case .confirmKey:
            confirmKey()
        case .none, .unconfirmKey, .removeKey:
            break
        }
    }

    // MARK: Buttons

    private func checkRevocation() {
        guard SignatureView.canCheckRevocation(current) else { return }
        run(.checkRevocation) { [client = self.client, messageID = self.messageID] in
            let r = try await client.call(
                API.MessageSignatureRevocation.self, MessageSignatureParams(messageId: messageID))
            return { controller in
                if let d = r.signature {
                    controller.details = d
                }
            }
        } failure: { error in
            SignatureView.revocationFailedText(SignatureView.revocationFailure(of: error))
        }
    }

    private func importKey() {
        guard let params = SignatureView.importParams(current) else { return }
        run(.importKey) { [client = self.client] in
            let r = try await client.call(API.PGPKeyImport.self, params)
            return { controller in
                controller.toast?(SignatureView.importedText(r))
                // The notification is on its way too; ask now so that the
                // popover turns at once.
                controller.load()
            }
        } failure: { error in
            SignatureView.importFailedText(SignatureView.importFailure(of: error))
        }
    }

    private func confirmKey() {
        guard let params = SignatureView.confirmParams(current), let confirm else { return }
        let q = SignatureView.confirmKeyQuestion()
        let fingerprint = SignatureView.formatFingerprint(params.fingerprint)
        busy = .confirmKey
        onChange?()
        Task { [weak self] in
            let ok = await confirm(q, fingerprint)
            guard let self else { return }
            self.busy = .none
            guard ok else {
                self.onChange?()
                return
            }
            self.run(.confirmKey) { [client = self.client] in
                _ = try await client.call(API.PGPKeyVerify.self, params)
                return { controller in
                    controller.toast?(SignatureView.keyConfirmedText)
                    controller.load()
                }
            } failure: { _ in
                SignatureView.keyVerifyFailedText(verified: true)
            }
        }
    }

    /// Runs one call with the button busy: `body` makes the call and
    /// returns what to do with its answer on the main actor.
    private func run(
        _ action: SignatureView.Action,
        _ body: @escaping @Sendable () async throws -> @MainActor (SignatureController) -> Void,
        failure: @escaping @MainActor (any Error) -> String
    ) {
        busy = action
        onChange?()
        Task { [weak self] in
            let outcome: Result<@MainActor (SignatureController) -> Void, any Error>
            do {
                outcome = .success(try await body())
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            self.busy = .none
            switch outcome {
            case .success(let apply):
                apply(self)
            case .failure(let error):
                self.toast?(failure(error))
            }
            self.onChange?()
        }
    }
}
