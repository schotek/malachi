// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/apiassistant registry.go: the open sessions of the daemon's
// assistant by the daemon's session id.

import Foundation

/// apiassistant.Sessions: the open sessions of the daemon's assistant, by
/// the daemon's session id: the application gives every notification to
/// `handle` from its hub, and a notify.assistantEvent reaches its session.
/// One per application (per connection: a session belongs to the
/// connection that started it).
@MainActor
public final class AssistantSessions {
    private var open: [String: DaemonAssistantSession] = [:]
    /// The events of a session before it was added here.
    struct Early {
        var at: ContinuousClock.Instant
        var events: [AssistantEvent]
    }

    /// The events of a session not open here yet: a turn sent with
    /// assistant.sessionStart may notify before the start answers with the
    /// id; `add` replays them. At most `earlyMax` events per id, and an id
    /// nobody claimed within `earlyFor` is forgotten.
    var early: [String: Early] = [:]
    static let earlyMax = 256
    static let earlyFor: Duration = .seconds(30)
    /// The assistant.sessionStop calls under way (`terminate` sends them
    /// in the background); `wait` lets the application send them before it
    /// closes the connection.
    private(set) var stops = 0

    public init() {}

    func add(_ s: DaemonAssistantSession) {
        open[s.id] = s
        guard let e = early.removeValue(forKey: s.id) else { return }
        for ev in e.events {
            s.handle(ev)
        }
    }

    /// Remembers an event of a session not open yet, and forgets the ids
    /// nobody claimed in time.
    private func keepEarly(_ e: AssistantEvent) {
        let now = ContinuousClock.now
        for (id, kept) in early where now - kept.at > Self.earlyFor {
            early.removeValue(forKey: id)
        }
        var kept = early[e.sessionId] ?? Early(at: now, events: [])
        if kept.events.count < Self.earlyMax {
            kept.events.append(e)
        }
        early[e.sessionId] = kept
    }

    func remove(_ s: DaemonAssistantSession) {
        if open[s.id] === s {
            open.removeValue(forKey: s.id)
        }
    }

    func stopBegan() {
        stops += 1
    }

    func stopEnded() {
        stops -= 1
    }

    /// apiassistant.Sessions.Wait: waits for the sessionStop calls under
    /// way (`terminate` sends them in the background), at most `d`: true
    /// when all are through. The application calls it as it quits, after
    /// it ended the panel's and the requests' sessions and before it
    /// closes the connection, so that the daemon gets each stop (it would
    /// end the sessions with the connection anyway; the call then just
    /// fails on the closed connection).
    public func wait(_ d: Duration) async -> Bool {
        let deadline = ContinuousClock.now + d
        while stops > 0 {
            if ContinuousClock.now >= deadline {
                return false
            }
            try? await Task.sleep(for: .milliseconds(5))
        }
        return true
    }

    /// apiassistant.Sessions.Len: the number of open sessions.
    public var count: Int { open.count }

    /// apiassistant.Sessions.Handle for a decoded notification: true when
    /// it was a notify.assistantEvent (handled, or kept for a session not
    /// open here yet); false for any other, which the caller dispatches on.
    @discardableResult
    public func handle(_ n: DaemonNotification) -> Bool {
        guard case .assistantEvent(let e) = n else { return false }
        return handle(e)
    }

    /// Takes one notify.assistantEvent: its session's, else kept for a
    /// session not open here yet. Always true (it was an assistant event).
    @discardableResult
    public func handle(_ e: AssistantEvent) -> Bool {
        if let s = open[e.sessionId] {
            s.handle(e)
        } else if !e.sessionId.isEmpty {
            keepEarly(e)
        }
        return true
    }

    /// apiassistant.Sessions.Disconnected: the connection to the daemon
    /// went, and every session with it (the daemon ends them itself); each
    /// ends with "api_stopped".
    public func disconnected() {
        for s in Array(open.values) {
            s.disconnected()
        }
    }
}
