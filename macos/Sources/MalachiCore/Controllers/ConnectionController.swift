// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// Keeps the app connected to malachid, the counterpart of the reconnect
/// loop and connection status of ui/internal/window/window.go.
///
/// Owns the `RPCClient` and the `DaemonSupervisor`: on `start()` it brings
/// the daemon up (or adopts a running one), dials, checks `system.info` and
/// the protocol version, and while the socket is dead retries every
/// `reconnectInterval`. Notifications and state changes are consumed from
/// the client's streams and handed to `onNotification`/`onState` on the
/// main actor; decoding a notification is the consumer's job.
@MainActor
public final class ConnectionController {
    public enum ConnectionState: Sendable, Equatable {
        case connecting
        case connected(SystemInfo)
        case protocolMismatch(daemon: Int)
        case infoFailed(String)
        case unavailable(String)
        case stopping
    }

    /// How long system.info may take, as in the GTK UI.
    public static let infoTimeout: Duration = RPCTimeouts.systemInfo
    /// How often a dead socket is retried, as in the GTK UI.
    public static let defaultReconnectInterval: Duration = .seconds(5)

    public let client: RPCClient
    public let supervisor: DaemonSupervisor?
    public let reconnectInterval: Duration

    public private(set) var state: ConnectionState = .connecting
    /// Called on every state change, after `state` was updated.
    public var onState: (@MainActor (ConnectionState) -> Void)?
    /// Called for every daemon notification, in order.
    public var onNotification: (@MainActor (RPCNotification) -> Void)?

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "connection")
    private var loop: Task<Void, Never>?
    private var attempt: Task<Void, Never>?
    private var stateReader: Task<Void, Never>?
    private var notificationReader: Task<Void, Never>?
    /// What the client last reported; the retry loop keys off it.
    private var clientConnected = false
    /// Bumped on every connect and disconnect so a late system.info reply
    /// of an earlier connection is dropped.
    private var generation = 0
    private var started = false
    private var stopping = false

    /// - Parameters:
    ///   - client: the transport; its streams are consumed here, so nobody
    ///     else may iterate them.
    ///   - supervisor: brings the daemon up before each dial; nil to only
    ///     ever dial.
    ///   - reconnectInterval: the retry period while disconnected.
    public init(client: RPCClient, supervisor: DaemonSupervisor?, reconnectInterval: Duration = ConnectionController.defaultReconnectInterval) {
        self.client = client
        self.supervisor = supervisor
        self.reconnectInterval = reconnectInterval
    }

    /// Connects now and keeps retrying while disconnected. Idempotent.
    public func start() {
        guard !started, !stopping else { return }
        started = true
        let client = client
        stateReader = Task { [weak self] in
            for await s in client.states {
                guard let self else { return }
                self.clientStateChanged(s)
            }
        }
        notificationReader = Task { [weak self] in
            for await n in client.notifications {
                guard let self else { return }
                self.onNotification?(n)
            }
        }
        reconnectNow()
        let interval = reconnectInterval
        loop = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: interval)
                guard let self, !Task.isCancelled else { return }
                if !self.clientConnected {
                    self.reconnectNow()
                }
            }
        }
    }

    /// Starts a connection attempt at once unless one is in flight or the
    /// client is connected.
    public func reconnectNow() {
        guard !stopping, attempt == nil, !clientConnected else { return }
        // Every attempt announces itself, as the GTK client's Connect does,
        // even when the previous state was already `.connecting`.
        state = .connecting
        onState?(.connecting)
        attempt = Task { [weak self] in
            await self?.connectOnce()
            self?.attempt = nil
        }
    }

    /// Stops retrying, closes the connection and stops the daemon this app
    /// started. The state stays `.stopping`.
    public func stop() async {
        stopping = true
        setState(.stopping)
        loop?.cancel()
        loop = nil
        attempt?.cancel()
        attempt = nil
        await client.close()
        if let supervisor {
            await supervisor.stop()
        }
        stateReader?.cancel()
        stateReader = nil
        notificationReader?.cancel()
        notificationReader = nil
    }

    // MARK: Internals

    /// One attempt: daemon up, then dial. Failures are routine while the
    /// daemon is down or backing off, so they are logged at debug level.
    private func connectOnce() async {
        if let supervisor {
            do {
                try await supervisor.ensure()
            } catch {
                let reason = describe(error)
                log.debug("backend not started: \(reason, privacy: .public)")
                report(.unavailable(reason))
                return
            }
        }
        do {
            try await client.connect()
        } catch {
            let reason = describe(error)
            log.debug("backend unavailable: \(reason, privacy: .public)")
            report(.unavailable(reason))
        }
    }

    private func clientStateChanged(_ s: RPCClient.State) {
        switch s {
        case .connecting:
            break // reported by reconnectNow already
        case .connected:
            clientConnected = true
            generation += 1
            checkSystemInfo()
        case .disconnected(let reason):
            // A dial that never got through is reported by the attempt
            // itself; here only a connection that was up and dropped.
            guard clientConnected else { return }
            clientConnected = false
            generation += 1
            report(.unavailable(reason ?? RPCClient.ClientError.disconnected.description))
        }
    }

    private func checkSystemInfo() {
        let gen = generation
        let client = client
        Task { [weak self] in
            let outcome: ConnectionState
            do {
                let info = try await client.call(API.SystemInfo.self, EmptyParams(), timeout: ConnectionController.infoTimeout)
                outcome = info.protocolVersion == API.protocolVersion
                    ? .connected(info)
                    : .protocolMismatch(daemon: info.protocolVersion)
            } catch {
                outcome = .infoFailed(describe(error))
            }
            guard let self, self.generation == gen else { return }
            if case .infoFailed(let reason) = outcome {
                self.log.error("system.info: \(reason, privacy: .public)")
            }
            self.report(outcome)
        }
    }

    private func report(_ s: ConnectionState) {
        guard !stopping else { return }
        setState(s)
    }

    private func setState(_ s: ConnectionState) {
        guard s != state else { return }
        state = s
        onState?(s)
    }
}

/// A short description of a connect-time error for the status line.
private func describe(_ error: any Error) -> String {
    switch error {
    case let e as DaemonSupervisor.SupervisorError:
        return e.description
    case let e as RPCClient.ClientError:
        return e.description
    case let e as RPCError:
        return e.description
    case is CancellationError:
        return "cancelled"
    default:
        return String(describing: error)
    }
}
