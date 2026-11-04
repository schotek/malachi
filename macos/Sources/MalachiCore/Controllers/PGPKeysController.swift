// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// Settings → Security → OpenPGP Keys (plan §6, `ui/internal/signature`
/// `KeyRows`): pgp.keyList into rows, and the row actions Confirm Key…,
/// Remove Confirmation and Remove Key… (pgp.keyVerify, pgp.keyRemove) and
/// Import Key… (pgp.keyImport, from a pasted text or a file). The daemon
/// never stores a secret key and never looks keys up online; this only
/// shows what it has. Every name in a row is a certificate's or a key's
/// own text: plain, through `SignatureView.keyRows`.
///
/// `observe` follows `SignatureState` (notify.signaturesChanged asks
/// pgp.keyList again) and the connection (`load` when it comes).
@MainActor
public final class PGPKeysController {
    /// The keys as pgp.keyList last answered.
    public private(set) var keys: [PGPKey] = []
    /// The rows of `keys`.
    public private(set) var rows: [SignatureView.KeyRow] = []
    /// pgp.keyList has answered at least once.
    public private(set) var loaded = false
    /// The last pgp.keyList failed (the page says `SecurityPage.loadFailed`).
    public private(set) var loadFailed = false
    /// A call of Import, Confirm or Remove runs.
    public private(set) var busy = false

    /// `keys`, `rows` or a flag changed: the pane draws again.
    public var onChange: (@MainActor () -> Void)?
    /// A transient message (a done action, or what failed).
    public var toast: (@MainActor (String) -> Void)?
    /// Asks a question before Confirm Key… and Remove Key…; true goes ahead.
    /// The fingerprint is `nil` for Remove, the key's formatted one for
    /// Confirm.
    public var ask: (@MainActor (SignatureView.Question, _ fingerprint: String?) async -> Bool)?

    private let client: RPCClient
    private let now: @MainActor () -> Date
    private let date: @MainActor (Date) -> String
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "signature")
    private var loadGeneration = 0

    public init(
        client: RPCClient, now: @escaping @MainActor () -> Date = { Date() },
        date: @escaping @MainActor (Date) -> String = { SignatureView.formatDay($0) }
    ) {
        self.client = client
        self.now = now
        self.date = date
    }

    /// The pane's texts.
    public var texts: SignatureView.SecurityPage { SignatureView.securityPageTexts() }

    /// pgp.keyList.
    public func load() {
        loadGeneration += 1
        let my = loadGeneration
        let client = client
        Task { [weak self] in
            let outcome: Result<PGPKeyListResult, any Error>
            do {
                outcome = .success(try await client.call(API.PGPKeyList.self, EmptyParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self, my == self.loadGeneration else { return }
            self.loaded = true
            switch outcome {
            case .success(let r):
                self.loadFailed = false
                self.setKeys(r.keys)
            case .failure(let error):
                self.log.info("pgp.keyList: \(String(describing: error), privacy: .public)")
                self.loadFailed = true
            }
            self.onChange?()
        }
    }

    private func setKeys(_ list: [PGPKey]) {
        keys = list
        let d = date
        rows = SignatureView.keyRows(list, now: now()) { d($0) }
    }

    // MARK: Import

    /// Import Key… with the text of the sheet's area.
    public func importPasted(_ text: String) {
        guard let p = SignatureView.pastedParams(text) else { return }
        importKey(p)
    }

    /// Import Key… with the file the user chose (read up to
    /// `maxPGPKeyBytes + 1` bytes: more is `tooLarge`).
    public func importFile(_ data: Data) {
        guard let p = SignatureView.fileParams(data) else {
            toast?(SignatureView.importFailedText(data.isEmpty ? .noKey : .tooLarge))
            return
        }
        importKey(p)
    }

    /// pgp.keyImport; the answer's keys join the list at once.
    public func importKey(_ params: PGPKeyImportParams) {
        guard !busy else { return }
        run { [client = self.client] in
            let r = try await client.call(API.PGPKeyImport.self, params)
            return { controller in
                controller.toast?(SignatureView.importedText(r))
                controller.load()
            }
        } failure: { SignatureView.importFailedText(SignatureView.importFailure(of: $0)) }
    }

    // MARK: Row actions

    /// A row's menu item.
    public func perform(_ action: SignatureView.Action, fingerprint: String) {
        guard !busy else { return }
        switch action {
        case .confirmKey:
            confirmKey(fingerprint)
        case .unconfirmKey:
            run { [client = self.client] in
                _ = try await client.call(API.PGPKeyVerify.self, PGPKeyVerifyParams(fingerprint: fingerprint, verified: false))
                return { controller in
                    controller.toast?(SignatureView.confirmationRemovedText)
                    controller.load()
                }
            } failure: { _ in
                SignatureView.keyVerifyFailedText(verified: false)
            }
        case .removeKey:
            removeKey(fingerprint)
        case .none, .checkRevocation, .importKey:
            break
        }
    }

    private func confirmKey(_ fingerprint: String) {
        guard let ask else { return }
        busy = true
        onChange?()
        let q = SignatureView.confirmKeyQuestion()
        let shown = SignatureView.formatFingerprint(fingerprint)
        Task { [weak self] in
            let ok = await ask(q, shown)
            guard let self else { return }
            self.busy = false
            guard ok else {
                self.onChange?()
                return
            }
            self.run { [client = self.client] in
                _ = try await client.call(API.PGPKeyVerify.self, PGPKeyVerifyParams(fingerprint: fingerprint, verified: true))
                return { controller in
                    controller.toast?(SignatureView.keyConfirmedText)
                    controller.load()
                }
            } failure: { _ in
                SignatureView.keyVerifyFailedText(verified: true)
            }
        }
    }

    private func removeKey(_ fingerprint: String) {
        guard let ask else { return }
        busy = true
        onChange?()
        let q = SignatureView.removeKeyQuestion()
        Task { [weak self] in
            let ok = await ask(q, nil)
            guard let self else { return }
            self.busy = false
            guard ok else {
                self.onChange?()
                return
            }
            self.run { [client = self.client] in
                _ = try await client.call(API.PGPKeyRemove.self, PGPKeyRemoveParams(fingerprint: fingerprint))
                return { controller in
                    controller.toast?(SignatureView.keyRemovedText)
                    controller.load()
                }
            } failure: { _ in
                SignatureView.keyRemoveFailedText
            }
        }
    }

    /// Runs one call with `busy` set; `body` returns what to do with the
    /// answer on the main actor.
    private func run(
        _ body: @escaping @Sendable () async throws -> @MainActor (PGPKeysController) -> Void,
        failure: @escaping @MainActor (any Error) -> String
    ) {
        busy = true
        onChange?()
        Task { [weak self] in
            let outcome: Result<@MainActor (PGPKeysController) -> Void, any Error>
            do {
                outcome = .success(try await body())
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            self.busy = false
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
