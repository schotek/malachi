// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The Disk Space Used row of the settings (ui/internal/window/
/// preferences.go `bindStorage`) without the widgets: system.storage when
/// the window opens, again after every change the daemon confirmed
/// (`refresh`), and every `interval` while the window is open, one call at
/// a time: a refresh asked for while one runs follows it. The pane renders
/// `onUsage` through `storageTexts`; a failure
/// replaces only the details (`onError`, the value stays as it was), the
/// next answer puts them back; a daemon without the method (methodNotFound,
/// notImplemented) hides the row and is not asked again (`onUnsupported`).
@MainActor
public final class StorageUsageController {
    /// How often the row is refreshed while the window is open.
    public nonisolated static let interval: Duration = .seconds(5)

    /// Called with every answer.
    public var onUsage: (@MainActor (SystemStorageResult) -> Void)?
    /// Called once when the daemon does not offer system.storage.
    public var onUnsupported: (@MainActor () -> Void)?
    /// Called with the sentence of a failed call (anything but what
    /// `methodUnsupported` says).
    public var onError: (@MainActor (String) -> Void)?

    /// The last answer.
    public private(set) var usage: SystemStorageResult?
    /// The daemon has no system.storage; nothing is asked any more.
    public private(set) var unsupported = false
    /// The window closed: the timer stopped, late replies dropped.
    public private(set) var closed = false

    private let client: RPCClient
    private let period: Duration
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "preferences")
    /// A call is running (`inFlight`); `again`: a refresh was asked for
    /// meanwhile and follows it.
    private var inFlight = false
    private var again = false
    private var timer: Task<Void, Never>?

    public init(client: RPCClient, interval: Duration = StorageUsageController.interval) {
        self.client = client
        period = interval
    }

    /// Asks now and then every `interval` until `close`.
    public func start() {
        guard !closed, timer == nil else { return }
        refresh()
        let period = period
        timer = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: period)
                guard let self, !self.closed, !self.unsupported else { return }
                self.refresh()
            }
        }
    }

    /// Asks system.storage now, or right after the call that is running.
    public func refresh() {
        guard !closed, !unsupported else { return }
        if inFlight {
            again = true
            return
        }
        inFlight = true
        let client = client
        Task { [weak self] in
            let outcome: Result<SystemStorageResult, any Error>
            do {
                outcome = .success(try await client.call(API.SystemStorage.self, EmptyParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            self.inFlight = false
            guard !self.closed else { return }
            switch outcome {
            case .success(let res):
                self.usage = res
                self.onUsage?(res)
            case .failure(let err) where methodUnsupported(err):
                self.unsupported = true
                self.timer?.cancel()
                self.timer = nil
                self.onUnsupported?()
            case .failure(let err):
                // Asked every few seconds: a failure is not worth a warning each time.
                self.log.debug("system.storage: \(String(describing: err), privacy: .public)")
                self.onError?(rpcErrorText(L10n.T("Measuring the disk space"), err))
            }
            if self.again, !self.unsupported {
                self.again = false
                self.refresh()
            }
        }
    }

    /// Stops the timer; nothing is emitted afterwards.
    public func close() {
        closed = true
        timer?.cancel()
        timer = nil
    }
}
