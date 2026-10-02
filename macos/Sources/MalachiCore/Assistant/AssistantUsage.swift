// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistant usage.go and the Usage of events.go: what a run of
// Claude Code used, from the usage its events carry. This file holds no
// translatable text.

extension Assistant {
    /// assistant.Usage: what Claude Code reports an API message, or a run,
    /// used, in tokens. A counter missing or null in the line reads as 0.
    public struct Usage: Sendable, Equatable {
        public var inputTokens: Int64
        public var outputTokens: Int64
        public var cacheCreationInputTokens: Int64
        public var cacheReadInputTokens: Int64

        public init(
            inputTokens: Int64 = 0, outputTokens: Int64 = 0, cacheCreationInputTokens: Int64 = 0,
            cacheReadInputTokens: Int64 = 0
        ) {
            self.inputTokens = inputTokens
            self.outputTokens = outputTokens
            self.cacheCreationInputTokens = cacheCreationInputTokens
            self.cacheReadInputTokens = cacheReadInputTokens
        }

        /// All four counters are 0.
        var isZero: Bool { self == Usage() }
    }

    /// assistant.MaxUsageTokens: the largest value of each counter of a
    /// tally's total, `API.Limits.maxBoardUsageTokens`: what the daemon
    /// stores at most.
    public static let maxUsageTokens: Int64 = 1_000_000_000_000

    /// assistant.UsageTally: adds up the usage of one run's events (`add`
    /// each event, in order). Its `total` is the result's usage when the
    /// run reported one; a run that ended without one (cancelled, timed
    /// out, killed, stopped at a limit) has the sum over the distinct API
    /// messages seen, each counted once with the last usage seen for its
    /// id: a lower bound, whose output tokens are the placeholders of the
    /// messages' starts. A result whose counters are all 0 while the
    /// messages counted some (Claude Code's crash result may be zeroed)
    /// gives way to that sum. A new tally is empty.
    public struct UsageTally: Sendable, Equatable {
        private var result: Usage?
        private var messages: [String: Usage] = [:]

        public init() {}

        /// Counts the usage `e` carries, if any.
        public mutating func add(_ e: Event) {
            guard let u = e.usage else { return }
            if e.kind == .result {
                result = u
                return
            }
            guard !e.messageID.isEmpty else { return }
            messages[e.messageID] = u
        }

        /// The run's usage, each counter at most `maxUsageTokens`; nil when
        /// nothing reported any.
        public var total: Usage? {
            var sum = Usage()
            for u in messages.values {
                sum = Usage(
                    inputTokens: addTokens(sum.inputTokens, u.inputTokens),
                    outputTokens: addTokens(sum.outputTokens, u.outputTokens),
                    cacheCreationInputTokens: addTokens(sum.cacheCreationInputTokens, u.cacheCreationInputTokens),
                    cacheReadInputTokens: addTokens(sum.cacheReadInputTokens, u.cacheReadInputTokens))
            }
            if let r = result, !r.isZero || sum.isZero {
                return Usage(
                    inputTokens: addTokens(0, r.inputTokens), outputTokens: addTokens(0, r.outputTokens),
                    cacheCreationInputTokens: addTokens(0, r.cacheCreationInputTokens),
                    cacheReadInputTokens: addTokens(0, r.cacheReadInputTokens))
            }
            return messages.isEmpty ? nil : sum
        }
    }
}

/// a + b of two counters from 0 up, at most `Assistant.maxUsageTokens`.
private func addTokens(_ a: Int64, _ b: Int64) -> Int64 {
    let m = Assistant.maxUsageTokens
    return min(min(max(a, 0), m) + min(max(b, 0), m), m)
}
