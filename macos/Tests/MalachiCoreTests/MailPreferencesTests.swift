// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Mail group of the settings against a fake daemon
// (ui/internal/window/preferences.go `bindMail`).

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// What the daemon was asked to store, in order, and the keys of each
/// preference object as it went over the wire.
private actor SetLog {
    var sent: [Preferences] = []
    var keys: [[String]] = []

    func record(_ p: Preferences) {
        sent.append(p)
    }

    func record(keys: [String]) {
        self.keys.append(keys)
    }
}

/// Collects what the controller emits.
@MainActor
private final class Recorder {
    var preferences: [Preferences?] = []
    var enabled: [Bool] = []
    var descriptions: [String] = []
    var toasts: [String] = []
    var saved = 0

    func attach(_ c: MailPreferencesController) {
        c.onPreferences = { [weak self] in self?.preferences.append($0) }
        c.onEnabled = { [weak self] in self?.enabled.append($0) }
        c.onDescription = { [weak self] in self?.descriptions.append($0) }
        c.onToast = { [weak self] in self?.toasts.append($0) }
        c.onSaved = { [weak self] in self?.saved += 1 }
    }
}

private let stored = Preferences(syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30)
/// What a daemon that knows the storage preferences reports.
private let storedFull = Preferences(
    syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, compressStore: true, attachmentOfflineDays: 30)

/// A daemon that serves `initial` from config.get and echoes config.set
/// (after `normalise`, the daemon's validation) into `log`.
private func makeFake(
    log: SetLog, initial: Preferences = stored, delay: (@Sendable (Preferences) -> Duration?)? = nil,
    normalise: @escaping @Sendable (Preferences) -> Preferences = { $0 }
) async throws -> FakeDaemon {
    let fake = try FakeDaemon()
    await fake.on(API.ConfigGet.name) { _ in
        try JSONCoding.encoder().encode(ConfigGetResult(preferences: initial))
    }
    await fake.on(API.ConfigSet.name) { params in
        let p = try JSONCoding.decoder().decode(ConfigSetParams.self, from: params).preferences
        await log.record(p)
        let object = try JSONSerialization.jsonObject(with: params) as? [String: Any]
        await log.record(keys: ((object?["preferences"] as? [String: Any])?.keys.sorted()) ?? [])
        if let d = delay?(p) {
            try await Task.sleep(for: d)
        }
        return try JSONCoding.encoder().encode(ConfigSetResult(preferences: normalise(p)))
    }
    try await fake.start()
    return fake
}

@MainActor
private func makeController(_ fake: FakeDaemon) async throws -> (MailPreferencesController, Recorder) {
    let client = RPCClient(socketPath: fake.path)
    try await client.connect()
    let c = MailPreferencesController(client: client)
    let rec = Recorder()
    rec.attach(c)
    return (c, rec)
}

/// A controller whose load has answered.
@MainActor
private func loadedController(_ fake: FakeDaemon) async throws -> (MailPreferencesController, Recorder) {
    let (c, rec) = try await makeController(fake)
    c.load()
    try await waitUntil { c.isEnabled }
    return (c, rec)
}

@MainActor
@Suite(.serialized) struct MailPreferencesTests {
    @Test func selectionMapsValuesOntoPopupPositions() {
        #expect(MailPreferencesController.MailSelection(stored) == .init(interval: 1, remoteContent: 0, retention: 1))
        let odd = Preferences(syncIntervalSeconds: 1000, remoteContent: .allow, offlineDays: 400)
        #expect(MailPreferencesController.MailSelection(odd) == .init(interval: 2, remoteContent: 2, retention: 3))
        let manual = Preferences(syncIntervalSeconds: 0, remoteContent: .knownSenders, offlineDays: 0)
        #expect(MailPreferencesController.MailSelection(manual) == .init(interval: 0, remoteContent: 1, retention: 4))
    }

    @Test func disabledUntilLoaded() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log)
        defer { Task { await fake.stop() } }
        let (c, rec) = try await makeController(fake)
        #expect(!c.isEnabled)
        #expect(c.preferences == nil)
        // A change before the load has nothing to base itself on.
        c.set(checkInterval: 900)
        c.selectRetention(at: 0)
        #expect(await log.sent.isEmpty)
        #expect(rec.enabled.isEmpty)

        c.load()
        try await waitUntil { c.isEnabled }
        #expect(c.preferences == stored)
        #expect(rec.preferences == [stored])
        #expect(rec.enabled == [true])
        #expect(rec.descriptions.isEmpty)
        #expect(rec.toasts.isEmpty)
        #expect(await log.sent.isEmpty)
    }

    @Test func loadFailureGoesIntoTheDescriptionAndKeepsTheGroupInsensitive() async throws {
        let fake = try FakeDaemon()
        await fake.on(API.ConfigGet.name) { _ in
            throw RPCError(code: .internalError, message: "no store")
        }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let (c, rec) = try await makeController(fake)
        c.load()
        try await waitUntil { !rec.descriptions.isEmpty }
        #expect(rec.descriptions == ["Loading mail settings failed"])
        #expect(!c.isEnabled)
        #expect(c.preferences == nil)
        #expect(rec.preferences.isEmpty)
        #expect(rec.toasts.isEmpty)
    }

    @Test func loadWithoutADaemonReportsTheBackend() async throws {
        let fake = try FakeDaemon()
        try await fake.start()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()
        let c = MailPreferencesController(client: client)
        let rec = Recorder()
        rec.attach(c)
        await client.close()
        c.load()
        try await waitUntil { !rec.descriptions.isEmpty }
        #expect(rec.descriptions == ["Loading mail settings needs a running mail backend"])
        #expect(!c.isEnabled)
        #expect(c.preferences == nil)
    }

    @Test func setSendsTheWholeSetWithOneFieldChanged() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log)
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)

        c.set(checkInterval: 900)
        #expect(!c.isEnabled, "insensitive while the save is in flight")
        try await waitUntil { c.isEnabled }
        let want1 = Preferences(syncIntervalSeconds: 900, remoteContent: .block, offlineDays: 30)
        #expect(await log.sent == [want1])
        #expect(c.preferences == want1)

        c.set(remoteContent: .allow)
        try await waitUntil { c.isEnabled }
        let want2 = Preferences(syncIntervalSeconds: 900, remoteContent: .allow, offlineDays: 30)
        #expect(await log.sent.last == want2)

        c.set(offlineDays: 0)
        try await waitUntil { c.isEnabled }
        let want3 = Preferences(syncIntervalSeconds: 900, remoteContent: .allow, offlineDays: 0)
        #expect(await log.sent.last == want3)
        #expect(c.preferences == want3)

        #expect(rec.preferences == [stored, want1, want2, want3])
        #expect(rec.enabled == [true, false, true, false, true, false, true])
        #expect(rec.toasts.isEmpty)
        #expect(rec.descriptions.isEmpty)
    }

    @Test func popupPositionsMapThroughTheChoiceTables() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log)
        defer { Task { await fake.stop() } }
        let (c, _) = try await loadedController(fake)

        c.selectInterval(at: 3)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.last?.syncIntervalSeconds == 1800)
        c.selectRemoteContent(at: 1)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.last?.remoteContent == .knownSenders)
        c.selectRetention(at: 4)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.last?.offlineDays == 0)
        c.selectRetention(at: 0)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.last?.offlineDays == 7)

        // Positions outside the tables are ignored.
        c.selectInterval(at: 4)
        c.selectRemoteContent(at: -1)
        c.selectRetention(at: 5)
        #expect(c.isEnabled)
        #expect(await log.sent.count == 4)
    }

    @Test func theDaemonsEchoIsWhatIsShown() async throws {
        let log = SetLog()
        // The daemon clamps the interval to its minimum.
        let fake = try await makeFake(log: log, normalise: { p in
            var p = p
            p.syncIntervalSeconds = max(p.syncIntervalSeconds, 600)
            return p
        })
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)
        c.set(checkInterval: 300)
        try await waitUntil { c.isEnabled }
        let echoed = Preferences(syncIntervalSeconds: 600, remoteContent: .block, offlineDays: 30)
        #expect(await log.sent.last?.syncIntervalSeconds == 300)
        #expect(c.preferences == echoed)
        #expect(rec.preferences.last == echoed)
    }

    @Test func failedSaveToastsAndReverts() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log)
        await fake.on(API.ConfigSet.name) { _ in
            throw RPCError(code: .invalidArgument, message: "offlineDays out of range")
        }
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)

        c.set(offlineDays: -3)
        #expect(!c.isEnabled)
        try await waitUntil { c.isEnabled }
        #expect(rec.toasts == ["Saving mail settings was rejected: offlineDays out of range"])
        #expect(c.preferences == stored, "the last confirmed values stay")
        #expect(rec.preferences == [stored, stored], "rendered again so the pop-ups revert")
        #expect(rec.enabled == [true, false, true])
        #expect(rec.descriptions.isEmpty)

        // The next change bases itself on the confirmed values, not the
        // rejected ones.
        await fake.on(API.ConfigSet.name) { params in
            let p = try JSONCoding.decoder().decode(ConfigSetParams.self, from: params).preferences
            await log.record(p)
            return try JSONCoding.encoder().encode(ConfigSetResult(preferences: p))
        }
        c.set(remoteContent: .allow)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.last == Preferences(syncIntervalSeconds: 300, remoteContent: .allow, offlineDays: 30))
    }

    @Test func aStaleReplyDoesNotOverwriteANewerOne() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log, delay: { p in p.syncIntervalSeconds == 900 ? .milliseconds(300) : nil })
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)

        c.set(checkInterval: 900) // slow
        c.set(checkInterval: 1800) // fast, answers first
        try await waitUntil { c.isEnabled }
        let newer = Preferences(syncIntervalSeconds: 1800, remoteContent: .block, offlineDays: 30)
        #expect(c.preferences == newer)
        #expect(rec.preferences.last == newer)
        let renders = rec.preferences.count
        let toggles = rec.enabled.count

        // The slow reply arrives and is dropped.
        try await Task.sleep(for: .milliseconds(500))
        #expect(c.preferences == newer)
        #expect(rec.preferences.count == renders)
        #expect(rec.enabled.count == toggles)
        #expect(c.isEnabled)
    }

    @Test func closeDropsLateReplies() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log, delay: { _ in .milliseconds(200) })
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)

        c.set(checkInterval: 0)
        #expect(!c.isEnabled)
        c.close()
        #expect(c.closed)
        let renders = rec.preferences.count
        let toggles = rec.enabled.count
        try await Task.sleep(for: .milliseconds(400))
        #expect(await log.sent.count == 1, "the request itself went out")
        #expect(rec.preferences.count == renders)
        #expect(rec.enabled.count == toggles)
        #expect(rec.toasts.isEmpty)
        // Nothing starts after close either.
        c.load()
        c.set(offlineDays: 7)
        try await Task.sleep(for: .milliseconds(100))
        #expect(await log.sent.count == 1)
        #expect(rec.preferences.count == renders)
    }

    // MARK: Storage preferences (compressStore, attachmentOfflineDays)

    @Test func selectionMapsTheStoragePreferences() {
        let full = MailPreferencesController.MailSelection(storedFull)
        #expect(full == .init(interval: 1, remoteContent: 0, retention: 1, attachments: 2, compress: true))
        let small = Preferences(syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, compressStore: false, attachmentOfflineDays: -1)
        #expect(MailPreferencesController.MailSelection(small).attachments == 0)
        #expect(MailPreferencesController.MailSelection(small).compress == false)
        let everything = Preferences(syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, attachmentOfflineDays: 0)
        #expect(MailPreferencesController.MailSelection(everything).attachments == 4)
        // An older daemon: the rows are hidden.
        let old = MailPreferencesController.MailSelection(stored)
        #expect(old.attachments == nil && old.compress == nil)
    }

    @Test func storagePreferencesAreSentWithTheWholeSet() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log, initial: storedFull)
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)
        #expect(c.preferences == storedFull)

        c.set(compressStore: false)
        #expect(!c.isEnabled)
        try await waitUntil { c.isEnabled }
        var want = storedFull
        want.compressStore = false
        #expect(await log.sent == [want])
        #expect(rec.saved == 1)

        c.selectAttachmentDays(at: 0)
        try await waitUntil { c.isEnabled }
        want.attachmentOfflineDays = -1
        #expect(await log.sent.last == want)
        c.selectAttachmentDays(at: 4)
        try await waitUntil { c.isEnabled }
        want.attachmentOfflineDays = 0
        #expect(await log.sent.last == want)
        #expect(await log.keys.last == ["attachmentOfflineDays", "compressStore", "offlineDays", "remoteContent", "syncIntervalSeconds"],
                "false and 0 go over the wire")
        // A position outside the table is ignored.
        c.selectAttachmentDays(at: 5)
        c.selectAttachmentDays(at: -1)
        #expect(c.isEnabled)
        #expect(await log.sent.count == 3)
        #expect(rec.saved == 3)
        #expect(c.preferences == want)

        // Another row keeps the storage values as confirmed.
        c.set(checkInterval: 900)
        try await waitUntil { c.isEnabled }
        let last = await log.sent.last
        #expect(last?.compressStore == false && last?.attachmentOfflineDays == 0)
        #expect(rec.toasts.isEmpty)
    }

    /// A value between the pop-up's positions (14 days from config.toml)
    /// is shown at its nearest one and goes back unchanged when another
    /// row is saved; only a choice in its own pop-up replaces it
    /// (preferences.go `attachmentDaysToSave`).
    @Test func anUntouchedAttachmentValueIsKept() async throws {
        let log = SetLog()
        var initial = storedFull
        initial.attachmentOfflineDays = 14
        let fake = try await makeFake(log: log, initial: initial)
        defer { Task { await fake.stop() } }
        let (c, _) = try await loadedController(fake)
        #expect(MailPreferencesController.MailSelection(initial).attachments == 1, "shown as 1 week")

        c.set(checkInterval: 900)
        try await waitUntil { c.isEnabled }
        c.set(compressStore: false)
        try await waitUntil { c.isEnabled }
        c.selectRetention(at: 2)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.map(\.attachmentOfflineDays) == [14, 14, 14])

        c.selectAttachmentDays(at: 2)
        try await waitUntil { c.isEnabled }
        #expect(await log.sent.last?.attachmentOfflineDays == 30)
    }

    /// An older daemon does not report them: their setters do nothing and
    /// the fields never go back (absent is "unchanged" to a newer one).
    @Test func anOlderDaemonsSetIsSentWithoutThem() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log)
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)
        c.set(compressStore: true)
        c.set(attachmentOfflineDays: 7)
        c.selectAttachmentDays(at: 1)
        #expect(c.isEnabled)
        try await Task.sleep(for: .milliseconds(30))
        #expect(await log.sent.isEmpty)

        c.set(offlineDays: 90)
        try await waitUntil { c.isEnabled }
        #expect(await log.keys == [["offlineDays", "remoteContent", "syncIntervalSeconds"]])
        #expect(rec.saved == 1)
    }

    @Test func aFailedSaveIsNotReportedAsSaved() async throws {
        let log = SetLog()
        let fake = try await makeFake(log: log, initial: storedFull)
        await fake.on(API.ConfigSet.name) { _ in
            throw RPCError(code: .invalidArgument, message: "attachmentOfflineDays out of range")
        }
        defer { Task { await fake.stop() } }
        let (c, rec) = try await loadedController(fake)
        c.set(attachmentOfflineDays: 99999)
        try await waitUntil { c.isEnabled }
        #expect(rec.saved == 0)
        #expect(rec.toasts == ["Saving mail settings was rejected: attachmentOfflineDays out of range"])
        #expect(c.preferences == storedFull)
    }
}
