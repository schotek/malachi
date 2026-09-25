// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/accountwizard/results_test.go (English catalogue).
struct WizardResultsTests {
    private static let ok = EndpointTestResult(ok: true, latencyMs: 0)
    private static let auth = EndpointTestResult(ok: false, error: RPCError(code: .authFailed, message: "x"), latencyMs: 0)
    private static let tls = EndpointTestResult(ok: false, error: RPCError(code: .tlsError, message: "x"), latencyMs: 0)

    @Test func classifyOutcomes() {
        let cases: [(AccountTestResult, Outcome)] = [
            (AccountTestResult(imap: Self.ok, smtp: Self.ok), .ok),
            (AccountTestResult(imap: Self.ok, smtp: Self.auth), .authFailed),
            (AccountTestResult(imap: Self.tls, smtp: Self.auth), .authFailed),
            (AccountTestResult(imap: Self.tls, smtp: Self.ok), .failed),
            (AccountTestResult(), .failed),
        ]
        for (i, c) in cases.enumerated() {
            #expect(classify(c.0) == c.1, "case \(i)")
        }
        // An endpoint that failed without an error is a failure, not ok.
        #expect(classify(AccountTestResult(imap: EndpointTestResult(ok: false, latencyMs: 0), smtp: Self.ok)) == .failed)
    }

    @Test func endpointSummaries() {
        let (icon, text) = endpointSummary(EndpointTestResult(ok: true, capabilities: ["<b>x</b>"], latencyMs: 42))
        #expect(icon == "emblem-ok-symbolic")
        #expect(text == "Connected in 42 ms")

        let timeout = endpointSummary(EndpointTestResult(ok: false, error: RPCError(code: .serverTimeout, message: "x"), latencyMs: 0))
        #expect(timeout.icon == "dialog-error-symbolic")
        #expect(timeout.text.contains("did not respond"))

        #expect(endpointSummary(EndpointTestResult(ok: false, latencyMs: 0)).text == "Failed")

        let unknown = endpointSummary(EndpointTestResult(ok: false, error: RPCError(code: .storageError, message: "disk full"), latencyMs: 0))
        #expect(unknown.text == "Failed: disk full")
        let rejected = endpointSummary(EndpointTestResult(ok: false, error: RPCError(code: .invalidArgument, message: "bad port"), latencyMs: 0))
        #expect(rejected.text == "Rejected: bad port")
        #expect(endpointSummary(Self.auth).text == "The server rejected the user name or password")
    }

    @Test func classifyGraph() {
        #expect(classify(AccountTestResult(graph: Self.ok)) == .ok)
        #expect(classify(AccountTestResult(graph: Self.auth)) == .authFailed)
        let (icon, text) = endpointSummary(nil)
        #expect(icon == "dialog-question-symbolic")
        #expect(text == "Not tested")
    }
}
