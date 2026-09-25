// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/widget/rpc_test.go.
@Suite struct RPCErrorTextTests {
    @Test func rpcErrorTextTest() {
        let cases: [ErrorCode: String] = [
            .authFailed: "rejected the user name or password",
            .networkError: "could not be reached",
            .serverError: "returned an error",
            .tlsError: "secure connection",
            .serverTimeout: "did not respond",
            .keyringError: "keyring",
            .conflict: "conflicted",
        ]
        for (code, want) in cases {
            let got = rpcErrorText("Testing", RPCError(code: code, message: "detail"))
            #expect(got.hasPrefix("Testing ") && got.contains(want), "\(code): \(got)")
        }
        #expect(rpcErrorText("Testing", RPCClient.ClientError.disconnected).contains("running mail backend"))
        #expect(rpcErrorText("Testing", RPCClient.ClientError.notConnected).contains("running mail backend"))
        #expect(rpcErrorText("Testing", RPCClient.ClientError.timeout(method: "x")).contains("timed out"))
        #expect(rpcErrorText("Testing", CancellationError()).contains("timed out"))
        #expect(rpcErrorText("Testing", RPCError(code: 9999, message: "x")) == "Testing failed")
        #expect(rpcErrorText("Testing", RPCClient.ClientError.transport("boom")) == "Testing failed")
        #expect(rpcErrorText("Testing", nil) == "Testing failed")
        #expect(rpcErrorText("Testing", RPCError(code: .invalidArgument, message: "port 0")) == "Testing was rejected: port 0")
        #expect(rpcErrorText("Testing", RPCError(code: .notImplemented, message: "")) == "Testing is not available yet")
        #expect(rpcErrorText("Testing", RPCError(code: .draftNotFound, message: "")) == "The draft no longer exists")
    }

    @Test func endpointErrorTextTest() {
        #expect(endpointErrorText(nil) == "Failed")
        #expect(endpointErrorText(RPCError(code: .invalidArgument, message: "port 0")) == "Rejected: port 0")
        #expect(endpointErrorText(RPCError(code: 9999, message: "odd")) == "Failed: odd")
        #expect(endpointErrorText(RPCError(code: .tlsError, message: "x")).contains("secure connection"))
        #expect(endpointErrorText(RPCError(code: .authRequired, message: "x")) == "Sign in to this account again")
        #expect(endpointErrorText(RPCError(code: .unavailable, message: "x")) == "The sign-in service is not available")
    }
}
