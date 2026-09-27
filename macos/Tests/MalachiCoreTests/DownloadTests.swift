// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/download_test.go: `withDownload`,
// the fetch that downloads a part kept on the mail server first, or once
// after the daemon said partNotDownloaded.

private struct Boom: Error {}

/// What `withDownload` did, in order: "fetch:<partId>" and "download".
@MainActor
private final class Steps {
    var log: [String] = []
    /// The answers of the fetches, in order; past the end the fetch
    /// succeeds.
    var fetchErrors: [(any Error)?] = []
    var downloadError: (any Error)?
    /// The message the download answers with.
    var downloaded: Message?

    func fetch(_ a: MalachiCore.Attachment) throws -> String {
        log.append("fetch:" + a.partId)
        let i = log.filter { $0.hasPrefix("fetch:") }.count - 1
        if i < fetchErrors.count, let err = fetchErrors[i] {
            throw err
        }
        return "data of " + a.partId
    }

    func download() throws -> Message? {
        log.append("download")
        if let downloadError {
            throw downloadError
        }
        return downloaded
    }
}

private let notDownloaded = RPCError(code: .partNotDownloaded, message: "on the server")

private func part(_ id: String, remote: Bool? = nil) -> MalachiCore.Attachment {
    MalachiCore.Attachment(partId: id, filename: "report.pdf", contentType: "application/pdf", size: 200_000, inline: false, remote: remote)
}

@MainActor
private func run(_ steps: Steps, remote: Bool, _ a: MalachiCore.Attachment = part("2")) async throws -> String {
    try await withDownload(a, remote: remote, fetch: { try steps.fetch($0) }, download: { try steps.download() })
}

@MainActor
@Suite struct DownloadTests {
    @Test func aLocalPartIsFetchedOnce() async throws {
        let steps = Steps()
        #expect(try await run(steps, remote: false) == "data of 2")
        #expect(steps.log == ["fetch:2"])
    }

    @Test func aRemotePartIsDownloadedFirst() async throws {
        let steps = Steps()
        #expect(try await run(steps, remote: true) == "data of 2")
        #expect(steps.log == ["download", "fetch:2"])
    }

    @Test func aFailedDownloadFetchesNothing() async throws {
        let steps = Steps()
        steps.downloadError = RPCError(code: .offline, message: "no network")
        await #expect(throws: RPCError.self) { try await run(steps, remote: true) }
        #expect(steps.log == ["download"])
    }

    @Test func partNotDownloadedGetsOneDownloadAndOneRetry() async throws {
        let steps = Steps()
        steps.fetchErrors = [notDownloaded]
        #expect(try await run(steps, remote: false) == "data of 2")
        #expect(steps.log == ["fetch:2", "download", "fetch:2"])
    }

    @Test func aFailedDownloadAfterPartNotDownloadedIsThrown() async throws {
        let steps = Steps()
        steps.fetchErrors = [notDownloaded]
        steps.downloadError = RPCError(code: .messageGone, message: "gone")
        do {
            _ = try await run(steps, remote: false)
            Issue.record("no error")
        } catch let e as RPCError {
            #expect(e.code == .messageGone)
        }
        #expect(steps.log == ["fetch:2", "download"])
    }

    @Test func anotherErrorIsNotRetried() async throws {
        let steps = Steps()
        steps.fetchErrors = [RPCError(code: .partNotFound, message: "no such part")]
        do {
            _ = try await run(steps, remote: false)
            Issue.record("no error")
        } catch let e as RPCError {
            #expect(e.code == .partNotFound)
        }
        steps.log.removeAll()
        steps.fetchErrors = [Boom()]
        await #expect(throws: Boom.self) { try await run(steps, remote: false) }
        #expect(steps.log == ["fetch:2"])
    }

    @Test func neverALoop() async throws {
        // A second partNotDownloaded after the download is thrown as it is.
        let steps = Steps()
        steps.fetchErrors = [notDownloaded, notDownloaded, nil]
        do {
            _ = try await run(steps, remote: false)
            Issue.record("no error")
        } catch let e as RPCError {
            #expect(e.code == .partNotDownloaded)
        }
        #expect(steps.log == ["fetch:2", "download", "fetch:2"])
        // A remote part that is still not there after its download, too.
        steps.log.removeAll()
        steps.fetchErrors = [notDownloaded]
        await #expect(throws: RPCError.self) { try await run(steps, remote: true) }
        #expect(steps.log == ["download", "fetch:2"])
    }

    /// The fetch after a download asks for the part the downloaded message
    /// lists under the same name and type (`partAfterDownload`).
    @Test func theRetryFollowsAMovedPart() async throws {
        let steps = Steps()
        steps.downloaded = Message(summary: summary("m"), attachments: [part("3")])
        #expect(try await run(steps, remote: true, part("2", remote: true)) == "data of 3")
        #expect(steps.log == ["download", "fetch:3"])
        steps.log.removeAll()
        steps.fetchErrors = [notDownloaded]
        #expect(try await run(steps, remote: false, part("2")) == "data of 3")
        #expect(steps.log == ["fetch:2", "download", "fetch:3"])
    }

    /// A part the downloaded message no longer lists is not fetched at
    /// all: its old id names another file there (download.go
    /// `errPartNotFound`), and the toast says the attachment no longer
    /// exists.
    @Test func aPartGoneAfterTheDownloadIsNotFetched() async throws {
        let logo = MalachiCore.Attachment(partId: "2", filename: "logo.png", contentType: "image/png", size: 9_000, inline: false)
        let steps = Steps()
        steps.downloaded = Message(summary: summary("m"), attachments: [logo])
        do {
            _ = try await run(steps, remote: true, part("2", remote: true))
            Issue.record("no error")
        } catch let e as RPCError {
            #expect(e == partNotFoundAfterDownload)
            #expect(e.code == .partNotFound)
        }
        #expect(steps.log == ["download"])
        // After partNotDownloaded too: no second fetch.
        steps.log.removeAll()
        steps.fetchErrors = [notDownloaded]
        await #expect(throws: partNotFoundAfterDownload) { try await run(steps, remote: false, part("2")) }
        #expect(steps.log == ["fetch:2", "download"])
        // Two candidates under the name and type, none at the old id.
        steps.log.removeAll()
        steps.fetchErrors = []
        steps.downloaded = Message(summary: summary("m"), attachments: [part("3"), part("4")])
        await #expect(throws: partNotFoundAfterDownload) { try await run(steps, remote: true, part("2", remote: true)) }
        #expect(steps.log == ["download"])
        #expect(rpcErrorText("Opening the attachment", partNotFoundAfterDownload) == "The attachment no longer exists")
        #expect(!isPartNotDownloaded(partNotFoundAfterDownload))
    }

    @Test func methodUnsupportedTest() {
        #expect(methodUnsupported(RPCError(code: .methodNotFound, message: "x")))
        #expect(methodUnsupported(RPCError(code: .notImplemented, message: "x")))
        let supported: [any Error] = [
            Boom(), RPCError(code: .unavailable, message: "x"), RPCError(code: .storageError, message: "x"),
            RPCClient.ClientError.disconnected,
        ]
        for err in supported {
            #expect(!methodUnsupported(err), "\(err)")
        }
    }

    @Test func isPartNotDownloadedTest() {
        #expect(isPartNotDownloaded(notDownloaded))
        #expect(!isPartNotDownloaded(RPCError(code: .partNotFound, message: "")))
        #expect(!isPartNotDownloaded(RPCClient.ClientError.disconnected))
        #expect(!isPartNotDownloaded(Boom()))
    }
}
