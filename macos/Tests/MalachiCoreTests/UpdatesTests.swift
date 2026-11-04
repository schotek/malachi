// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/updates updates_test.go one to one, the update check's wire
// types (backend/pkg/api update_test.go), and the application's
// UpdateController against a fake daemon.

private let dl = "https://github.com/schotek/malachi/releases/download/v1.0.0/Malachi-Mail-1.0.0-macos-universal.dmg"

private func utc(_ y: Int, _ mo: Int, _ d: Int, _ h: Int) -> Date {
    var c = DateComponents()
    (c.year, c.month, c.day, c.hour) = (y, mo, d, h)
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC")!
    return cal.date(from: c)!
}

private func info(_ security: Bool) -> UpdateInfo {
    UpdateInfo(
        version: "1.0.0", publishedAt: utc(2026, 11, 1, 19), security: security,
        notes: "  ## New\n- Things.\n", infoUrl: "https://malachimail.app/download.html", url: dl, size: 1,
        prompt: security)
}

/// One of every state and offer.
private func statuses(_ now: Date) -> [UpdateStatus?] {
    let at = now.addingTimeInterval(-3600)
    return [
        nil,
        UpdateStatus(enabled: false, state: .disabled),
        UpdateStatus(enabled: true, state: .unsupported),
        UpdateStatus(enabled: true, state: .idle),
        UpdateStatus(enabled: true, state: .checking),
        UpdateStatus(enabled: true, state: .upToDate, checkedAt: at),
        UpdateStatus(enabled: true, state: .upToDate),
        UpdateStatus(enabled: true, state: .failed, failure: .network),
        UpdateStatus(enabled: true, state: .available, available: info(false)),
        UpdateStatus(enabled: true, state: .available, available: info(true)),
        UpdateStatus(enabled: true, state: .upToDate, notice: UpdateNotice(id: "n-1", text: "Hello.", url: "https://malachimail.app/news.html")),
        UpdateStatus(enabled: true, state: .upToDate, notice: UpdateNotice(id: "n-2", text: "Hello.")),
    ]
}

private func labels(_ b: Updates.Banner) -> [Updates.Action] {
    b.buttons.map(\.action)
}

private let utcDay: @Sendable (Date) -> String = { d in
    let f = DateFormatter()
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = TimeZone(identifier: "UTC")
    f.dateFormat = "yyyy-MM-dd"
    return f.string(from: d)
}

@Suite struct UpdatesTests {
    @Test func bannerFor() {
        let now = utc(2026, 11, 2, 8)
        let st = statuses(now)
        let want: [Updates.BannerKind] = [.none, .none, .none, .none, .none, .none, .none, .none, .update, .security, .notice, .notice]
        for (i, k) in want.enumerated() {
            #expect(Updates.bannerFor(st[i], now: now).kind == k, "status \(i)")
        }
        var b = Updates.bannerFor(st[8], now: now)
        #expect(b.title == "Malachi Mail 1.0.0 is available." && b.url == dl && b.version == "1.0.0")
        #expect(labels(b) == [.download, .whatsNew, .later, .skip])
        b = Updates.bannerFor(st[9], now: now)
        #expect(labels(b) == [.download, .whatsNew])
        b = Updates.bannerFor(st[10], now: now)
        #expect(b.title == "Hello." && b.noticeID == "n-1" && b.url == "https://malachimail.app/news.html")
        #expect(labels(b) == [.moreInfo, .close])
        #expect(labels(Updates.bannerFor(st[11], now: now)) == [.close])

        // Skipped or put off: the notice, if any, shows instead.
        let later = now.addingTimeInterval(3600)
        var s = UpdateStatus(enabled: true, state: .available, available: info(false), notice: UpdateNotice(id: "n", text: "x"))
        s.available?.remindAfter = later
        #expect(Updates.bannerFor(s, now: now).kind == .notice, "put off")
        #expect(Updates.bannerFor(s, now: later).kind == .update, "after the reminder")
        s.available?.remindAfter = nil
        s.available?.skipped = true
        s.notice = nil
        #expect(!Updates.bannerFor(s, now: now).shown, "skipped")
        // A security release is never put off, whatever the status says.
        s.available?.security = true
        #expect(Updates.bannerFor(s, now: now).kind == .security, "skipped security")
        // A URL off the allowed hosts gives no Download.
        s.available?.url = "https://example.com/x.dmg"
        b = Updates.bannerFor(s, now: now)
        #expect(labels(b) == [.whatsNew] && b.url == "", "foreign URL")
        // Disabled: nothing, even with an offer.
        s.enabled = false
        s.state = .disabled
        #expect(!Updates.bannerFor(s, now: now).shown, "disabled shows a banner")
    }

    @Test func promptFor() throws {
        let now = utc(2026, 11, 2, 8)
        let st = statuses(now)
        for (i, s) in st.enumerated() {
            #expect((Updates.promptFor(s) != nil) == (i == 9), "status \(i)")
        }
        let p = try #require(Updates.promptFor(st[9]))
        #expect(p.url == dl && p.version == "1.0.0" && !p.quit.isEmpty && p.download == "Download")
        var s = try #require(st[9])
        s.available?.prompt = false
        #expect(Updates.promptFor(s) == nil, "asked twice")
    }

    @Test func notesAndAbout() throws {
        let now = utc(2026, 11, 2, 8)
        let st = statuses(now)
        let n = try #require(Updates.notesFor(st[8], date: utcDay))
        #expect(n.title == "What's New in Malachi Mail 1.0.0" && n.subtitle == "Released 2026-11-01" && n.body == "## New\n- Things.")
        #expect(n.url == dl && n.infoURL == Updates.newsURL)
        #expect(n.blocks.map(\.kind) == [.heading, .bullet])
        let b = Updates.notesBlocks("See [the site](https://malachimail.app/) and https://github.com/x.")
        #expect(b[0].spans.allSatisfy { $0.link.isEmpty }, "a link stayed")
        #expect(Updates.notesFor(st[5], date: utcDay) == nil, "notes without an offer")
        let lines = [
            "", "Automatic update checks are off.", "This build does not check for updates.", "Not checked for updates yet.",
            "Checking for updates…", "You are using the latest version (checked 2026-11-02).", "You are using the latest version.",
            "Could not check for updates.", "Malachi Mail 1.0.0 is available.", "Malachi Mail 1.0.0 is available.",
        ]
        for (i, want) in lines.enumerated() {
            #expect(Updates.aboutLine(st[i], date: utcDay) == want, "status \(i)")
        }
        for (i, want) in [false, false, false, true, false, true, true, true, true, true].enumerated() {
            #expect(Updates.canCheckNow(st[i]) == want, "CanCheckNow(status \(i))")
        }
    }

    @Test func openableURL() {
        let cases: [String: Bool] = [
            dl: true,
            "https://malachimail.app/": true,
            "http://malachimail.app/": false,
            "https://malachimail.app:443/": false,
            "https://user@github.com/": false,
            "https://github.com/#x": false,
            "https://GITHUB.com/": false,
            "https://github.com.evil/": false,
            "javascript:alert(1)": false,
            "": false,
            "https://github.com/a b": false,
            "https://github.com\\@example.com": false,
            // Beyond the Go table: what url.Parse does past the byte checks.
            "https://github.com": true,
            "https://github.com?x=1": true,
            "https://github.com/a%2Fb": true,
            "https://github.com/a%zz": false,
            "https://github.com/a%2": false,
            "https:///github.com/": false,
            "https://github.com/" + String(repeating: "a", count: 2048): false,
        ]
        for (s, ok) in cases {
            #expect((Updates.openableURL(s) != nil) == ok, "openableURL(\(s.prefix(60)))")
        }
        #expect(Updates.openableURL(dl) == dl)
    }

    /// Every text is a msgid of po/malachi.pot (po_test.go's point, by
    /// value here: the English texts are the msgids).
    @Test func textsAreTheMsgids() {
        #expect(Updates.checkNow == "Check Now")
        #expect(Updates.prefsGroup == "Updates")
        #expect(Updates.prefsTitle == "Check for Updates Automatically")
        #expect(Updates.prefsSubtitle.hasPrefix("Once a day, Malachi Mail asks its website which versions exist."))
        #expect(Updates.wizardNote.hasSuffix("You can turn this off in Preferences."))
    }
}

/// The wire types as the daemon writes them (update_test.go
/// TestUpdateStatusWire) and the method names.
@Suite struct APICodingUpdateTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: Data(s.utf8))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    @Test func statusAsGoWritesIt() throws {
        let minimal = try decode(UpdateStatus.self, #"{"enabled":true,"current":"0.1.2","state":"idle"}"#)
        #expect(minimal == UpdateStatus(enabled: true, current: "0.1.2", state: .idle))
        let s = try decode(UpdateStatus.self, #"""
        {"enabled":true,"channel":"macos-dmg","current":"0.1.2","state":"available","checkedAt":"2026-11-02T08:00:00Z",
         "failure":"network",
         "available":{"version":"0.1.3","publishedAt":"2026-11-02T08:00:00Z","security":true,"notes":"n","infoUrl":"i",
           "url":"u","size":81234567,"skipped":false,"remindAfter":"2026-11-05T08:00:00Z","prompt":true},
         "notice":{"id":"n-1","text":"Hello.","url":"https://malachimail.app/"}}
        """#)
        #expect(s.channel == "macos-dmg" && s.state == .available && s.failure == .network && s.checkedAt != nil)
        let a = try #require(s.available)
        #expect(a.version == "0.1.3" && a.security && a.prompt && a.size == 81_234_567 && a.remindAfter != nil)
        #expect(s.notice == UpdateNotice(id: "n-1", text: "Hello.", url: "https://malachimail.app/"))
        #expect(try decode(UpdateState.self, #""later""#).rawValue == "later", "a newer value decodes as itself")
    }

    @Test func paramsSendOnlyWhatTheyCarry() throws {
        #expect(try encodeObject(UpdateStatusParams()).isEmpty)
        #expect(try encodeObject(UpdateSetPrefsParams(enabled: false, locale: "cs")).keys.sorted() == ["enabled", "locale"])
        let d = try encodeObject(UpdateDismissParams(version: "1.0.0", action: .prompted))
        #expect(d.keys.sorted() == ["action", "version"] && d["action"] as? String == "prompted")
        #expect(try encodeObject(UpdateDismissNoticeParams(id: "n-1")).keys.sorted() == ["id"])
    }

    @Test func methodNames() {
        #expect(API.UpdateGetStatus.name == "update.status")
        #expect(API.UpdateCheck.name == "update.check")
        #expect(API.UpdateSetPrefs.name == "update.setPrefs")
        #expect(API.UpdateDismiss.name == "update.dismiss")
        #expect(API.UpdateDismissNotice.name == "update.dismissNotice")
        #expect(API.Notify.updateChanged == "notify.updateChanged")
        #expect(API.UpdateCheck.timeout == .seconds(75) && API.UpdateGetStatus.timeout == RPCTimeouts.default)
        #expect(API.Limits.updateRemindDelay == 72 * 3600)
    }
}

// MARK: UpdateController

/// The daemon's side of the update check.
private actor UpdateScript {
    var status = UpdateStatus(enabled: true, current: "0.1.2", state: .upToDate)
    private(set) var calls: [(method: String, params: Data)] = []

    func set(_ s: UpdateStatus) { status = s }

    func answer(_ method: String, _ params: Data) throws -> Data {
        calls.append((method, params))
        switch method {
        case API.UpdateSetPrefs.name:
            let p = try JSONCoding.decoder().decode(UpdateSetPrefsParams.self, from: params)
            if let on = p.enabled {
                status.enabled = on
                status.state = on ? .idle : .disabled
            }
        case API.UpdateDismiss.name:
            let p = try JSONCoding.decoder().decode(UpdateDismissParams.self, from: params)
            if p.action == .prompted {
                status.available?.prompt = false
            }
        default:
            break
        }
        return try JSONCoding.encoder().encode(status)
    }

    func methods() -> [String] { calls.map(\.method) }

    func locales() throws -> [String?] {
        try calls.map { try JSONCoding.decoder().decode(UpdateStatusParams.self, from: $0.params).locale }
    }
}

private struct CallsTimeout: Error {
    let want: Int
}

/// Waits until the daemon got `n` calls.
private func callsReach(_ n: Int, _ script: UpdateScript) async throws {
    let deadline = ContinuousClock.now + .seconds(10)
    while await script.methods().count < n {
        if ContinuousClock.now > deadline { throw CallsTimeout(want: n) }
        try await Task.sleep(for: .milliseconds(10))
    }
}

@MainActor
private final class UpdateDaemon {
    let fake: FakeDaemon
    let script = UpdateScript()
    let client: RPCClient

    init() async throws {
        fake = try FakeDaemon()
        let s = script
        for m in [API.UpdateGetStatus.name, API.UpdateCheck.name, API.UpdateSetPrefs.name, API.UpdateDismiss.name, API.UpdateDismissNotice.name] {
            await fake.on(m) { p in try await s.answer(m, p) }
        }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

@MainActor
@Suite(.serialized) struct UpdateControllerTests {
    @Test func refreshSetAndCheck() async throws {
        let d = try await UpdateDaemon()
        let c = UpdateController(client: d.client, locale: { "cs" })
        var changes = 0
        let token = c.observe { changes += 1 }
        defer { token.cancel() }
        #expect(c.status == nil)
        c.refresh()
        try await waitFor { c.status != nil }
        #expect(c.status?.state == .upToDate && changes == 1)

        c.setEnabled(false)
        try await waitFor { c.status?.state == .disabled }
        // Check Now is not asked while the check is off.
        c.checkNow()
        c.setEnabled(true)
        try await waitFor { c.status?.state == .idle }
        c.checkNow()
        try await callsReach(4, d.script)
        #expect(await d.script.methods() == ["update.status", "update.setPrefs", "update.setPrefs", "update.check"])
        #expect(try await d.script.locales() == ["cs", "cs", "cs", "cs"], "every call carries the locale")
        await d.stop()
    }

    @Test func securityQuestionOncePerVersion() async throws {
        let d = try await UpdateDaemon()
        await d.script.set(UpdateStatus(enabled: true, current: "0.1.2", state: .available, available: info(true)))
        let c = UpdateController(client: d.client, locale: { "en" })
        var asked: [Updates.Prompt] = []
        // No handler yet: nothing is asked, and nothing is lost.
        c.refresh()
        try await waitFor { c.status != nil }
        c.onPrompt = { asked.append($0) }
        #expect(asked.count == 1 && asked.first?.version == "1.0.0")
        // Another status with the same question does not ask again.
        c.refresh()
        c.refresh()
        try await callsReach(3, d.script)
        #expect(asked.count == 1)
        // The answer tells the daemon; its status asks nothing any more.
        await c.promptAnswered(asked[0])
        #expect(await d.script.methods().last == "update.dismiss")
        #expect(c.status?.available?.prompt == false && Updates.promptFor(c.status) == nil)
        // The banner stays: a security release has no Later or Skip.
        #expect(Updates.bannerFor(c.status, now: Date()).kind == .security)
        await d.stop()
    }

    @Test func dismissAndCloseNotice() async throws {
        let d = try await UpdateDaemon()
        await d.script.set(UpdateStatus(enabled: true, current: "0.1.2", state: .available, available: info(false)))
        let c = UpdateController(client: d.client, locale: { "en" })
        c.dismiss("1.0.0", .later)
        c.dismissNotice("n-1")
        try await callsReach(2, d.script)
        // Two tasks: either may reach the daemon first.
        let calls = await d.script.calls
        let dismiss = try #require(calls.first { $0.method == API.UpdateDismiss.name })
        let notice = try #require(calls.first { $0.method == API.UpdateDismissNotice.name })
        let p = try JSONCoding.decoder().decode(UpdateDismissParams.self, from: dismiss.params)
        #expect(p == UpdateDismissParams(version: "1.0.0", action: .later, locale: "en"))
        let n = try JSONCoding.decoder().decode(UpdateDismissNoticeParams.self, from: notice.params)
        #expect(n == UpdateDismissNoticeParams(id: "n-1", locale: "en"))
        await d.stop()
    }
}
