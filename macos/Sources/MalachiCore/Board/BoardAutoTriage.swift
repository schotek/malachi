// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// When the board's triage runs on its own (docs/api.md §4.13, the
// `autoTriage*` preferences, which the daemon only stores): the pure rule.
// `BoardAutoTriageScheduler` gathers the inputs and acts on the decision;
// `BoardTriageController` runs it.
//
// An automatic run starts when automatic triage is on, the assistant can
// run (shown, Claude Code found, the bridge beside the application) and is
// not known to be signed out, both consents are given and the board's
// `assistant` preference is on, no run is under way, the triage queue has
// cases, the day's cap of cases for automatic runs is not used up, and at
// least `autoTriageMinutes` passed since the last automatic attempt (after
// `failures` failed automatic runs in a row that interval doubles each time,
// at most a day). A success or a manual run resets the failures. A manual
// run ignores the switch, the interval, the cap, the back-off and the queue
// (its consent is asked when it starts).
//
// Swift-first: the Go reference (ui/internal/board) follows with the GTK
// board.

import Foundation

extension Board {
    /// Who starts a triage run.
    public enum TriageTrigger: Sendable, Equatable {
        /// The user's Triage button.
        case manual
        /// The application's schedule.
        case automatic

        /// `board.runStart`'s trigger.
        public var wire: BoardTrigger { self == .manual ? .manual : .auto }
    }

    public enum AutoTriage {
        /// The longest wait between two automatic attempts, back-off
        /// included.
        public static let maxInterval: TimeInterval = 24 * 3600

        /// What the rule reads.
        public struct Inputs: Sendable, Equatable {
            public var trigger: TriageTrigger
            /// The `autoTriage` preference.
            public var enabled: Bool
            /// The assistant is shown, Claude Code found and the bridge is
            /// beside the application.
            public var available: Bool
            /// Whether Claude Code is signed in; nil when not known (which
            /// does not hold a run back: the run asks again).
            public var signedIn: Bool?
            /// Both consents and the board's `assistant` preference.
            public var consent: Bool
            /// A run is under way (this application's, or one the daemon
            /// reports open).
            public var running: Bool
            /// Cases `board.queue` would offer (`BoardTriage.queue`).
            public var queue: Int
            /// Cases automatic runs annotated on the day of `countedAt`.
            public var annotatedToday: Int
            /// When `annotatedToday` was reported; on an earlier day it
            /// counts as 0. nil: as reported today.
            public var countedAt: Date?
            /// `autoTriageDailyCases`; 0 = none.
            public var dailyCap: Int
            /// `autoTriageMinutes`.
            public var minutes: Int
            /// When the last automatic run started (or was tried); nil
            /// before the first.
            public var lastAttempt: Date?
            /// Automatic runs that failed in a row since the last success
            /// or manual run.
            public var failures: Int
            public var now: Date

            public init(
                trigger: TriageTrigger = .automatic, enabled: Bool = true, available: Bool = true,
                signedIn: Bool? = true, consent: Bool = true, running: Bool = false, queue: Int = 1,
                annotatedToday: Int = 0, countedAt: Date? = nil,
                dailyCap: Int = API.Limits.defaultBoardAutoTriageDailyCases,
                minutes: Int = API.Limits.defaultBoardAutoTriageMinutes, lastAttempt: Date? = nil, failures: Int = 0,
                now: Date
            ) {
                self.trigger = trigger
                self.enabled = enabled
                self.available = available
                self.signedIn = signedIn
                self.consent = consent
                self.running = running
                self.queue = queue
                self.annotatedToday = annotatedToday
                self.countedAt = countedAt
                self.dailyCap = dailyCap
                self.minutes = minutes
                self.lastAttempt = lastAttempt
                self.failures = failures
                self.now = now
            }
        }

        /// Why no run starts and nothing is waited for: the next change
        /// of the inputs decides again.
        public enum OffReason: Sendable, Equatable {
            /// Automatic triage is off.
            case switchedOff
            /// The assistant is off, Claude Code was not found, or the
            /// bridge is missing.
            case unavailable
            /// Claude Code is signed out.
            case signedOut
            /// A consent is missing, or the board's assistant preference is
            /// off.
            case noConsent
            /// A run is under way.
            case running
            /// Nothing waits for the assistant.
            case emptyQueue
            /// The daily cap is 0: automatic runs annotate nothing.
            case noDailyCases
        }

        public enum Decision: Sendable, Equatable {
            /// Start a run of at most `limit` cases.
            case run(limit: Int)
            /// Decide again at `until` (the interval, the back-off, the
            /// next day for a cap used up).
            case wait(until: Date)
            case off(OffReason)
        }

        /// The interval after `failures` failed automatic runs in a row:
        /// `minutes`, doubled per failure, at most `maxInterval`.
        public static func interval(minutes: Int, failures: Int) -> TimeInterval {
            let base = TimeInterval(min(max(minutes, API.Limits.minBoardAutoTriageMinutes),
                                        API.Limits.maxBoardAutoTriageMinutes)) * 60
            var d = base
            for _ in 0 ..< max(0, failures) {
                d *= 2
                if d >= maxInterval {
                    return maxInterval
                }
            }
            return min(d, maxInterval)
        }

        /// The rule (see the top of this file).
        public static func decide(_ i: Inputs, calendar: Calendar = .current) -> Decision {
            if i.trigger == .manual {
                if !i.available { return .off(.unavailable) }
                if i.signedIn == false { return .off(.signedOut) }
                if i.running { return .off(.running) }
                return .run(limit: Assistant.triageBatch)
            }
            if !i.enabled { return .off(.switchedOff) }
            if !i.available { return .off(.unavailable) }
            if i.signedIn == false { return .off(.signedOut) }
            if !i.consent { return .off(.noConsent) }
            if i.running { return .off(.running) }
            if i.queue <= 0 { return .off(.emptyQueue) }
            if i.dailyCap <= 0 { return .off(.noDailyCases) }
            let counted = i.countedAt.map { calendar.isDate($0, inSameDayAs: i.now) } ?? true
            let remaining = i.dailyCap - (counted ? max(0, i.annotatedToday) : 0)
            if remaining <= 0 {
                let tomorrow = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: i.now))
                return .wait(until: tomorrow ?? i.now.addingTimeInterval(maxInterval))
            }
            if let last = i.lastAttempt {
                let earliest = last.addingTimeInterval(interval(minutes: i.minutes, failures: i.failures))
                if i.now < earliest {
                    return .wait(until: earliest)
                }
            }
            return .run(limit: min(Assistant.triageBatch, remaining))
        }
    }
}
