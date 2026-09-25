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

    @Test func tlsErrorTexts() throws {
        let cert = CertificateInfo(sha256: String(repeating: "ab", count: 32), subject: "127.0.0.1", notBefore: .goZero, notAfter: .goZero)
        func tlsErr(_ reason: TLSErrorReason) throws -> RPCError {
            try tlsError(TLSErrorData(reason: reason, certificate: cert))
        }
        let cases: [TLSErrorReason: String] = [
            .untrusted: "The server's certificate is not from a trusted authority",
            .hostnameMismatch: "The server's certificate is for another name",
            .expired: "The server's certificate has expired",
            .notYetValid: "The server's certificate is not valid yet",
            .invalid: "The server's certificate is not valid",
            .other: "The system does not accept the server's certificate",
            "brandNew": "The system does not accept the server's certificate",
            .pinMismatch: "The server presented a different certificate than the one you trust",
            .starttlsUnavailable: "The server does not offer STARTTLS",
            .tlsRequired: "The server requires TLS before signing in",
        ]
        for (reason, want) in cases {
            #expect(endpointErrorText(try tlsErr(reason)) == want, "endpoint \(reason)")
            #expect(rpcErrorText("Sending", try tlsErr(reason)) == want, "rpc \(reason)")
        }
        // A handshake failure and a tlsError without details keep the
        // general sentence.
        for e in [try tlsErr(.handshake), RPCError(code: .tlsError, message: "x")] {
            #expect(endpointErrorText(e) == "The secure connection could not be established")
            #expect(rpcErrorText("Sending", e) == "Sending failed: the secure connection could not be established")
        }
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
