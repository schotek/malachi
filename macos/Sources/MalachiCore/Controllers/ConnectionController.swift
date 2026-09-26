// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// Keeps the app connected to malachid, the counterpart of the reconnect
/// loop and connection status of ui/internal/window/window.go.
///
/// Owns the `RPCClient` and the `DaemonSupervisor`: on `start()` it brings
/// the daemon up (or adopts a running one), dials (the client authenticates
/// the connection and compares the protocol version in the handshake),
/// checks `system.info`, and while there is no connection retries every
/// `reconnectInterval`. Notifications and state changes are consumed from
/// the client's streams and handed to `onNotification`/`onState` on the
/// main actor; decoding a notification is the consumer's job.
///
/// An attempt ends connected, as a protocol mismatch or as unavailable. A
/// mismatch stays up across the retries (no "Connecting…" every few
/// seconds) until an attempt ends otherwise; a connection ends it at once.
/// A refused handshake is logged at error level once per distinct reason
/// until the next connection; the routine failures while the daemon is
/// down stay at debug level.
@MainActor
public final class ConnectionController {
    public enum ConnectionState: Sendable, Equatable {
        case connecting
        case connected(SystemInfo)
        /// The daemon speaks another protocol version (1 for one without a
        /// handshake): the handshake refused it, so there is no connection
        /// and nothing is loaded; `system.info` disagreeing on an
        /// authenticated connection ends here too, as a defence.
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
    /// The handshake refusals logged at error level since the last
    /// connection, in order: the retry loop meets the same refusal every few
    /// seconds, and the log names each one once (the repeats go to debug).
    private(set) var loggedRefusals: [String] = []

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
        // even when the previous state was already `.connecting`; a
        // protocol mismatch stays up instead until an attempt ends
        // otherwise, so the line does not flip every few seconds.
        if !showsMismatch {
            state = .connecting
            onState?(.connecting)
        }
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
    /// daemon is down or backing off, so they are logged at debug level; a
    /// daemon that answers but refuses the handshake, or is refused by it,
    /// is not, and is logged once (`logRefusal`). A daemon of another
    /// protocol is the mismatch, anything else the client refused is
    /// unavailable.
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
        } catch let refusal as RPCClient.HandshakeError {
            logRefusal(refusal.description)
            if case .protocolMismatch(let daemon) = refusal {
                report(.protocolMismatch(daemon: daemon))
            } else {
                report(.unavailable(refusal.description))
            }
        } catch {
            let reason = describe(error)
            log.debug("backend unavailable: \(reason, privacy: .public)")
            report(.unavailable(reason))
        }
    }

    /// How many refusals `loggedRefusals` remembers before it starts afresh
    /// (window/connection.go `maxConnWarned`): the peer on the socket
    /// chooses the error codes of its refusals, and so their texts.
    private static let maxLoggedRefusals = 16

    /// Logs a refused handshake at error level the first time since the
    /// last connection, at debug level when the retry loop meets it again.
    /// The descriptions carry no key, nonce or proof.
    private func logRefusal(_ description: String) {
        if loggedRefusals.contains(description) {
            log.debug("backend refused: \(description, privacy: .public)")
            return
        }
        if loggedRefusals.count >= Self.maxLoggedRefusals {
            loggedRefusals.removeAll()
        }
        loggedRefusals.append(description)
        log.error("backend refused: \(description, privacy: .public)")
    }

    /// Whether the state is a protocol mismatch, which `reconnectNow`
    /// keeps up and a connection ends.
    private var showsMismatch: Bool {
        if case .protocolMismatch = state {
            return true
        }
        return false
    }

    private func clientStateChanged(_ s: RPCClient.State) {
        switch s {
        case .connecting:
            break // reported by reconnectNow already
        case .connected:
            clientConnected = true
            generation += 1
            loggedRefusals.removeAll()
            // A connection ends a mismatch kept up through the attempts at
            // once, as GTK drops it at Connected: "Connecting…" until
            // system.info answers.
            if showsMismatch {
                report(.connecting)
            }
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
                // The handshake compared the version already; an answer
                // that disagrees with it is still a mismatch, as a defence.
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
    case let e as RPCClient.HandshakeError:
        return e.description
    case let e as RPCError:
        return e.description
    case is CancellationError:
        return "cancelled"
    default:
        return String(describing: error)
    }
}
