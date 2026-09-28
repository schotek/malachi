// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Disk Space Used row of the settings (ui/internal/window/
// preferences.go `storageTexts`, `bindStorage`; preferences_test.go
// TestStorageTexts), against a fake daemon.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// The daemon's side of system.storage: what it answers, how long it
/// takes, how often it was asked.
private actor StorageScript {
    var answer = SystemStorageResult(totalBytes: 3 << 30, savedBytes: 1 << 30)
    var failure: RPCError?
    var delay: Duration = .zero
    var calls = 0

    func set(answer: SystemStorageResult) { self.answer = answer }
    func set(failure: RPCError?) { self.failure = failure }
    func set(delay: Duration) { self.delay = delay }

    func serve() async throws -> Data {
        calls += 1
        if delay > .zero {
            try await Task.sleep(for: delay)
        }
        if let failure {
            throw failure
        }
        return try JSONCoding.encoder().encode(answer)
    }
}

/// What the controller emitted, in order.
@MainActor
private final class UsageLog {
    var usages: [SystemStorageResult] = []
    var errors: [String] = []
    var unsupported = 0

    func attach(_ c: StorageUsageController) {
        c.onUsage = { [weak self] in self?.usages.append($0) }
        c.onError = { [weak self] in self?.errors.append($0) }
        c.onUnsupported = { [weak self] in self?.unsupported += 1 }
    }
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = StorageScript()
    let client: RPCClient
    let controller: StorageUsageController
    let log = UsageLog()

    init(interval: Duration) async throws {
        fake = try FakeDaemon()
        let script = script
        await fake.on(API.SystemStorage.name) { _ in try await script.serve() }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        controller = StorageUsageController(client: client, interval: interval)
        log.attach(controller)
    }

    func stop() async {
        controller.close()
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct StorageUsageTests {
    @Test func storageTextsTest() {
        #expect(storageTexts(SystemStorageResult()) == (value: "0 B", details: ""))
        let plain = SystemStorageResult(totalBytes: 44_470_272 + 909_800_000, messageBytes: 909_800_000, messageUncompressedBytes: 909_800_000)
        #expect(storageTexts(plain) == (value: "910.1 MiB", details: ""), "no compression, nothing on the server")
        let both = SystemStorageResult(totalBytes: 734_003_200, savedBytes: 363_100_000, remoteAttachmentBytes: 312_000_000)
        #expect(storageTexts(both) == (
            value: "700.0 MiB",
            details: "Compression saves 346.3 MiB\n297.5 MiB of attachments are on the server only"))
        let remoteOnly = SystemStorageResult(totalBytes: 5 << 30, remoteAttachmentBytes: 2 << 30)
        #expect(storageTexts(remoteOnly) == (value: "5.0 GiB", details: "2.0 GiB of attachments are on the server only"))
        // preferences_test.go TestStorageTexts.
        #expect(storageTexts(SystemStorageResult(totalBytes: 700 << 20, messageBytes: 650 << 20, messageUncompressedBytes: 650 << 20))
            == (value: "700.0 MiB", details: ""))
        #expect(storageTexts(SystemStorageResult(totalBytes: 3 << 30, savedBytes: 512 << 20))
            == (value: "3.0 GiB", details: "Compression saves 512.0 MiB"))
        #expect(storageTexts(SystemStorageResult(totalBytes: 1 << 30 + 512 << 20, savedBytes: 300 << 10, remoteAttachmentBytes: 40 << 20))
            == (value: "1.5 GiB", details: "Compression saves 300 KiB\n40.0 MiB of attachments are on the server only"))
        let savedOnly = SystemStorageResult(totalBytes: 2048, savedBytes: 1024)
        #expect(storageTexts(savedOnly) == (value: "2 KiB", details: "Compression saves 1 KiB"))
        // A negative saving (a store converting back) says nothing.
        #expect(storageTexts(SystemStorageResult(totalBytes: 10, savedBytes: -5)).details == "")
        // The background conversion comes first while it runs or stopped.
        #expect(storageTexts(SystemStorageResult(totalBytes: 3 << 30, savedBytes: 512 << 20, conversion: .running))
            == (value: "3.0 GiB", details: "Converting the stored mail in the background\nCompression saves 512.0 MiB"))
        #expect(storageTexts(SystemStorageResult(totalBytes: 3 << 30, conversion: .noSpace))
            == (value: "3.0 GiB", details: "Converting stopped: the disk is full"))
        #expect(storageTexts(SystemStorageResult(totalBytes: 5, conversion: .idle)).details == "")
    }

    /// Asked when the window opens and then every interval.
    @Test func asksAtOnceAndThenPeriodically() async throws {
        let h = try await Harness(interval: .milliseconds(100))
        defer { Task { await h.stop() } }
        h.controller.start()
        try await waitUntil { h.log.usages.count == 1 }
        #expect(h.controller.usage == SystemStorageResult(totalBytes: 3 << 30, savedBytes: 1 << 30))
        try await waitUntil { h.log.usages.count >= 3 }
        #expect(h.log.errors.isEmpty && h.log.unsupported == 0)
        // A second start does not add a second timer.
        h.controller.start()
        let calls = await h.script.calls
        try await Task.sleep(for: .milliseconds(250))
        let later = await h.script.calls
        #expect(later - calls <= 3, "one timer: \(later - calls) calls in 250 ms")
    }

    /// One call at a time: refreshes asked for while one runs make one
    /// more call after it (a saved change must not go unmeasured).
    @Test func oneCallAtATime() async throws {
        let h = try await Harness(interval: .seconds(60))
        defer { Task { await h.stop() } }
        await h.script.set(delay: .milliseconds(150))
        h.controller.start()
        h.controller.refresh()
        h.controller.refresh()
        try await waitUntil { h.log.usages.count == 1 }
        await h.script.set(answer: SystemStorageResult(totalBytes: 1 << 30))
        try await waitUntil { h.log.usages.count == 2 }
        #expect(h.log.usages.last?.totalBytes == 1 << 30)
        try await Task.sleep(for: .milliseconds(250))
        #expect(await h.script.calls == 2, "the two refreshes made one call after the first")
        // Once it answered, a refresh asks at once.
        await h.script.set(delay: .zero)
        h.controller.refresh()
        try await waitUntil { h.log.usages.count == 3 }
        #expect(await h.script.calls == 3)
    }

    /// A failure replaces the details only and the polling goes on; the
    /// next answer brings the numbers back.
    @Test func aFailureIsSaidAndThePollingGoesOn() async throws {
        let h = try await Harness(interval: .milliseconds(80))
        defer { Task { await h.stop() } }
        await h.script.set(failure: RPCError(code: .storageError, message: "disk"))
        h.controller.start()
        try await waitUntil { h.log.errors.count >= 2 }
        #expect(h.log.errors.first == "Measuring the disk space failed")
        #expect(h.log.usages.isEmpty && h.log.unsupported == 0)
        await h.script.set(failure: nil)
        try await waitUntil { h.log.usages.count == 1 }
    }

    /// A daemon without system.storage hides the row and is not asked again.
    @Test func anOlderDaemonHidesTheRow() async throws {
        for code in [ErrorCode.methodNotFound, .notImplemented] {
            let h = try await Harness(interval: .milliseconds(50))
            await h.script.set(failure: RPCError(code: code, message: "no"))
            h.controller.start()
            try await waitUntil { h.log.unsupported == 1 }
            #expect(h.controller.unsupported)
            h.controller.refresh()
            try await Task.sleep(for: .milliseconds(200))
            #expect(await h.script.calls == 1, "\(code): asked again")
            #expect(h.log.unsupported == 1 && h.log.errors.isEmpty && h.log.usages.isEmpty)
            await h.stop()
        }
    }

    /// Nothing is asked or emitted once the window closed.
    @Test func closeStopsEverything() async throws {
        let h = try await Harness(interval: .milliseconds(50))
        defer { Task { await h.stop() } }
        await h.script.set(delay: .milliseconds(100))
        h.controller.start()
        h.controller.close()
        try await Task.sleep(for: .milliseconds(300))
        #expect(h.log.usages.isEmpty, "the answer in flight is dropped")
        #expect(await h.script.calls == 1)
        h.controller.start()
        h.controller.refresh()
        try await Task.sleep(for: .milliseconds(100))
        #expect(await h.script.calls == 1)
        #expect(h.controller.closed)
    }
}
