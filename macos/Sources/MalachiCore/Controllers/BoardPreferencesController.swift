// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The board's preferences in the daemon (docs/api.md §4.13
/// `board.preferences`, `board.setPreferences`): whether the board is on,
/// whether the assistant's notes count, the windows, the triage accounts and
/// the automatic triage's schedule, once for the whole application (the
/// triage controller, its schedule and Settings read the same object).
///
/// `preferences` is nil until the daemon answered. `load()` asks again (at
/// the start, on a reconnect, when a window shows the board); a reply that
/// a newer load or a lost connection overtook is dropped. `update` is
/// optimistic like the board's other writes: the change is laid over the
/// daemon's preferences at once and reported, then `board.setPreferences`
/// sends the whole object with it (writes go one after another, each with
/// what the earlier ones left), and its answer, the preferences as stored,
/// replaces it; a refused write is taken back, reported, and `onError` says
/// why. The contract has no partial write (docs/api.md §4.13: "every
/// field"), so each write reads the preferences afresh first and lays only
/// the user's changes (the writes' closures) over them: a field another
/// client changed since the last load is not written back. A write whose
/// fresh read fails goes on with the preferences last read; with none, it
/// fails. `observe` reports every change of `preferences`,
/// `observeLoaded` every answer of `board.preferences`.
///
/// Swift-first: the GTK and Windows ports follow with the board.
@MainActor
public final class BoardPreferencesController {
    /// The preferences with the writes under way; nil until the daemon
    /// answered.
    public private(set) var preferences: BoardPreferences?
    /// Called with a short sentence for a toast when a write failed (it is
    /// taken back by then), unless the write was `quiet`.
    public var onError: (@MainActor (String) -> Void)?

    /// The last `board.preferences` failed (the daemon is unreachable, or
    /// does not know the board); false once one answered.
    public private(set) var lastLoadFailed = false

    /// The daemon's preferences as last read, without the writes under way;
    /// nil until the daemon answered.
    public var stored: BoardPreferences? { base }

    /// A write is under way or waiting.
    public var writing: Bool { writesInFlight > 0 }

    private let client: RPCClient
    private let observers = BoardObservers()
    private let loaded = BoardObservers()
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")
    /// The daemon's preferences as last answered.
    private var base: BoardPreferences?
    /// The writes under way, laid over `base` in order.
    private var overlays: [(token: Int, apply: (inout BoardPreferences) -> Void)] = []
    private var nextToken = 0
    /// The latest load; older replies are dropped.
    private var loadGeneration = 0
    private var loadTask: Task<Bool, Never>?
    /// Why the last load failed, for the toast of a write that needed it.
    private var loadError: (any Error)?
    /// The last write, which the next one waits for.
    private var tail: Task<Void, Never>?
    private var writesInFlight = 0

    public init(client: RPCClient) {
        self.client = client
    }

    /// Calls `f` after every change of `preferences`.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// Calls `f` after every answer of `board.preferences` (`stored` holds
    /// it), also the reads before a write.
    public func observeLoaded(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        loaded.add(f)
    }

    /// Nothing is loading or being written (the tests wait for it).
    var isIdle: Bool { loadTask == nil && writesInFlight == 0 }

    // MARK: Loading

    /// Asks the daemon for the preferences (the answer is reported).
    public func load() {
        _ = startLoad()
    }

    /// Asks the daemon now and waits for the answer: true when it came.
    @discardableResult
    public func loadNow() async -> Bool {
        await startLoad().value
    }

    /// The connection came (the preferences are asked for again) or went
    /// (replies on the way are dropped; the last preferences stay).
    public func connectionChanged(connected: Bool) {
        if connected {
            load()
        } else {
            loadGeneration += 1
            loadTask = nil
        }
    }

    private func startLoad() -> Task<Bool, Never> {
        loadGeneration += 1
        let generation = loadGeneration
        let client = client
        let task = Task { [weak self] () -> Bool in
            let outcome: Result<BoardPreferencesResult, any Error>
            do {
                outcome = .success(try await client.call(API.BoardPreferencesGet.self, BoardPreferencesParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self, generation == self.loadGeneration else { return false }
            self.loadTask = nil
            switch outcome {
            case .success(let r):
                self.loadError = nil
                self.base = r.preferences
                self.publish()
                if self.lastLoadFailed {
                    self.lastLoadFailed = false
                    self.observers.notify()
                }
                self.loaded.notify()
                return true
            case .failure(let err):
                self.log.info("board.preferences: \(String(describing: err), privacy: .public)")
                self.loadError = err
                if !self.lastLoadFailed {
                    self.lastLoadFailed = true
                    self.observers.notify()
                }
                return false
            }
        }
        loadTask = task
        return task
    }

    // MARK: Writing

    /// Changes the preferences: at once here, then in the daemon (see the
    /// type's comment). True when the daemon stored it; false when it was
    /// refused (taken back, `onError` called unless `quiet`), the
    /// preferences could not be loaded first, or the controller went away
    /// before the write ran.
    @discardableResult
    public func update(quiet: Bool = false, _ change: @escaping (inout BoardPreferences) -> Void) async -> Bool {
        await withCheckedContinuation { cont in
            update(quiet: quiet, change) { cont.resume(returning: $0) }
        }
    }

    /// `update` with a completion instead of `await`; the completion is
    /// always called once.
    public func update(
        quiet: Bool = false, _ change: @escaping (inout BoardPreferences) -> Void,
        completion: (@MainActor (Bool) -> Void)?
    ) {
        nextToken += 1
        let token = nextToken
        overlays.append((token, change))
        publish()
        writesInFlight += 1
        let previous = tail
        let client = client
        let task = Task { [weak self] in
            await previous?.value
            guard let self else {
                completion?(false)
                return
            }
            let stored = await self.write(token, client, quiet: quiet)
            self.writesInFlight -= 1
            completion?(stored)
        }
        tail = task
    }

    /// Sends the preferences as they are with write `token` and the ones
    /// before it, and takes the answer.
    private func write(_ token: Int, _ client: RPCClient, quiet: Bool) async -> Bool {
        // Afresh (see the type's comment); the last read stands in when
        // this one fails.
        let fresh = await loadNow()
        if !fresh, base == nil {
            lift(token)
            if !quiet {
                onError?(Board.Text.failed(.preferences, loadError ?? RPCClient.ClientError.notConnected))
            }
            return false
        }
        guard var wanted = base else {
            lift(token)
            return false
        }
        for o in overlays {
            o.apply(&wanted)
            if o.token == token {
                break
            }
        }
        do {
            let r = try await client.call(API.BoardSetPreferences.self, BoardSetPreferencesParams(preferences: wanted))
            base = r.preferences
            lift(token)
            return true
        } catch {
            log.info("board.setPreferences: \(String(describing: error), privacy: .public)")
            lift(token)
            if !quiet {
                onError?(Board.Text.failed(.preferences, error))
            }
            return false
        }
    }

    private func lift(_ token: Int) {
        overlays.removeAll { $0.token == token }
        publish()
    }

    /// Builds `preferences` from the daemon's and the writes under way and
    /// reports it when it changed.
    private func publish() {
        var p = base
        if p != nil {
            for o in overlays {
                o.apply(&p!)
            }
        }
        guard p != preferences else { return }
        preferences = p
        observers.notify()
    }
}
