// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The application's copy of the update check's status (docs/api.md
/// §4.20, the flow of ui/internal/updates): one per application
/// (`AppState`), shared by the main window's banner, Settings → General and
/// the About panel. It asks update.status when the connection comes
/// (`refresh`) and again on every notify.updateChanged, and writes through
/// `checkNow`, `setEnabled`, `dismiss` and `dismissNotice`, whose answers
/// are the new status.
///
/// A security release asks its question once (`Updates.promptFor`): the
/// first status that carries it calls `onPrompt`, at most once per version
/// for the process, and the answer (`promptAnswered`) tells the daemon with
/// update.dismiss "prompted", so that no other client asks again. A daemon
/// without the update check (an older one) leaves `status` nil, which shows
/// nothing.
@MainActor
public final class UpdateController {
    /// The status as the daemon last answered; nil before the first answer
    /// and for an older daemon.
    public private(set) var status: UpdateStatus?

    /// Asks the question of a security release; set by the application
    /// before the connection comes. The handler calls `promptAnswered`.
    public var onPrompt: (@MainActor (Updates.Prompt) -> Void)? {
        didSet { offerPrompt() }
    }

    private let client: RPCClient
    /// The language the client's own texts are in (`L10n`), for the notes
    /// and the notice.
    private let locale: @MainActor () -> String
    private let observers = BoardObservers()
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "updates")
    /// Orders the answers: one that ends after a newer one was applied is
    /// dropped.
    private var generation = 0
    private var applied = 0
    /// The versions whose question this process asked already.
    private var prompted: Set<String> = []

    public init(client: RPCClient, locale: @escaping @MainActor () -> String = { L10n.catalogue.language }) {
        self.client = client
        self.locale = locale
    }

    /// Calls `f` after every change of `status`.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// Reads the status again (update.status).
    public func refresh() {
        let p = UpdateStatusParams(locale: locale())
        run(API.UpdateGetStatus.self, p)
    }

    /// Check Now (update.check); the answer is the status after the check.
    public func checkNow() {
        guard Updates.canCheckNow(status) else { return }
        run(API.UpdateCheck.self, UpdateCheckParams(locale: locale()))
    }

    /// The switch of Settings → General (update.setPrefs).
    public func setEnabled(_ on: Bool) {
        run(API.UpdateSetPrefs.self, UpdateSetPrefsParams(enabled: on, locale: locale()))
    }

    /// Later, Skip, or Prompted of `version` (update.dismiss).
    public func dismiss(_ version: String, _ action: UpdateDismissAction) {
        Task { await dismissing(version, action) }
    }

    /// `dismiss`, waited for (the security question quits after it).
    public func dismissing(_ version: String, _ action: UpdateDismissAction) async {
        await call(API.UpdateDismiss.self, UpdateDismissParams(version: version, action: action, locale: locale()))
    }

    /// A notice's Close (update.dismissNotice).
    public func dismissNotice(_ id: String) {
        run(API.UpdateDismissNotice.self, UpdateDismissNoticeParams(id: id, locale: locale()))
    }

    /// The question of `prompt` was answered (whatever the answer): the
    /// daemon records it, so that no other window or client asks again.
    public func promptAnswered(_ prompt: Updates.Prompt) async {
        await dismissing(prompt.version, .prompted)
    }

    // MARK: Internals

    private func run<M: RPCMethod>(_ method: M.Type, _ params: M.Params) where M.Result == UpdateStatus {
        Task { await call(method, params) }
    }

    private func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params) async where M.Result == UpdateStatus {
        generation += 1
        let my = generation
        let client = client
        do {
            let s = try await client.call(method, params)
            guard my > applied else { return }
            applied = my
            if s != status {
                status = s
                observers.notify()
            }
            offerPrompt()
        } catch {
            // The status carries the check's failures; an error here is the
            // connection's or an older daemon's, and changes nothing.
            log.info("\(M.name, privacy: .public): \(String(describing: error), privacy: .public)")
        }
    }

    /// Asks the question of the status once per version, when there is a
    /// handler to ask it.
    private func offerPrompt() {
        guard let onPrompt, let p = Updates.promptFor(status), !prompted.contains(p.version) else { return }
        prompted.insert(p.version)
        onPrompt(p)
    }
}
