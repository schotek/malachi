// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The status line and the sign-in banner over the controller
// (ui/internal/window/sync.go: applySyncState, refreshSyncLabel,
// loadSyncStatus, triggerSync's fallback, showAuthRequired; status.go:
// statusLineFor, and window.go's refresh timer).

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: () -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// Collects what the controller emits.
@MainActor
private final class Log {
    var footers: [SyncController.FooterState] = []
    var lines: [StatusLine] = []
    var banners: [(account: AccountID?, title: String?, button: String?)] = []
    var certBanners: [(account: AccountID?, title: String?, button: String?)] = []

    func attach(_ sc: SyncController) {
        sc.onFooter = { [weak self] f in self?.footers.append(f) }
        sc.onStatusLine = { [weak self] l in self?.lines.append(l) }
        sc.onAuthBanner = { [weak self] acc, title, button in self?.banners.append((acc, title, button)) }
        sc.onCertBanner = { [weak self] acc, title, button in self?.certBanners.append((acc, title, button)) }
    }
}

private func state(
    _ acc: AccountID, _ status: SyncStatus, folder: FolderID? = nil, progress: Int = -1, pending: Int = 0, failed: Int = 0,
    last: Date? = nil
) -> SyncState {
    SyncState(
        accountId: acc, status: status, folderId: folder, progress: progress, lastSync: last, pendingOutbox: pending,
        failedOutbox: failed)
}

private let info = SystemInfo(version: "1.2.3", protocolVersion: API.protocolVersion, pid: 4242, storePath: "/tmp/s.db")

/// Counts a handler's calls.
private actor CallCount {
    var n = 0

    func next() -> Int {
        n += 1
        return n
    }
}

/// The params of the last call a handler saw.
private actor ParamsBox {
    var value: Data?

    func set(_ data: Data) {
        value = data
    }
}

private let twoAccounts = [
    testAccount("a1", name: "Work", email: "w@example.invalid"),
    testAccount("a2", email: "home@example.invalid"),
]

@MainActor
private func makeController(accounts: [Account] = twoAccounts) -> (SyncController, Log) {
    let sc = SyncController()
    sc.accounts = { accounts }
    sc.folderName = { acc, id in acc == "a1" && id == "f_inbox" ? "Inbox" : "" }
    let log = Log()
    log.attach(sc)
    return (sc, log)
}

@MainActor
@Suite(.serialized) struct SyncControllerTests {
    @Test func footerLadder() {
        let (sc, log) = makeController()
        // Before any state the enabled accounts' account.list state counts.
        sc.refreshFooter()
        #expect(sc.footer == .init(text: "Up to date", spinning: false))

        sc.apply(state("a1", .syncing, folder: "f_inbox", progress: 42))
        #expect(sc.footer == .init(text: "Syncing Inbox… 42 %", spinning: true))
        sc.apply(state("a1", .syncing, folder: "f_gone"))
        #expect(sc.footer.text == "Syncing Work…")
        sc.apply(state("a2", .authRequired))
        #expect(sc.footer.text == "Syncing Work…", "syncing beats authRequired")
        sc.apply(state("a1", .idle))
        // One of two accounts: named.
        #expect(sc.footer == .init(text: "Sign-in required: home@example.invalid", spinning: false))
        sc.apply(state("a2", .idle, pending: 2))
        #expect(sc.footer == .init(text: "Sending 2 messages…", spinning: true))
        sc.apply(state("a2", .idle, failed: 1))
        #expect(sc.footer == .init(text: "1 message not sent", spinning: false))
        sc.apply(state("a2", .error))
        #expect(sc.footer == .init(text: "Sync error: home@example.invalid", spinning: false))
        sc.apply(state("a2", .offline))
        #expect(sc.footer == .init(text: "Offline: home@example.invalid", spinning: false))
        sc.apply(state("a2", .idle))
        #expect(sc.footer == .init(text: "Up to date", spinning: false))
        #expect(log.footers.count == 10)
        #expect(log.footers.last == sc.footer)
        // The line follows every footer; without a connection it says so.
        #expect(log.lines.count == 10)
        #expect(sc.line == StatusLine(text: "Connecting to backend…", icon: "network-idle-symbolic"))

        // A pure computation over other accounts leaves the emitted footer alone.
        let other = sc.footerState(accounts: [testAccount("a9", state: state("a9", .offline))], folderName: nil)
        #expect(other == .init(text: "Offline, retrying", spinning: false))
        #expect(sc.footer.text == "Up to date")
    }

    @Test func applyReturnsThePreviousStateAndHidesTheBanner() {
        let (sc, log) = makeController()
        let first = sc.apply(state("a1", .syncing))
        #expect(first.prev == nil)
        #expect(first.cur.status == .syncing)
        let second = sc.apply(state("a1", .idle, pending: 1))
        #expect(second.prev?.status == .syncing)
        #expect(second.cur.pendingOutbox == 1)
        #expect(sc.state(of: "a1")?.pendingOutbox == 1)
        #expect(sc.state(of: "a2") == nil)

        let n = AuthRequiredNotification(accountId: "a1", reason: .authFailed, message: "bad password")
        sc.showAuthRequired(n, account: twoAccounts[0])
        #expect(sc.authBannerAccount == "a1")
        #expect(log.banners.count == 1)
        #expect(log.banners.last?.title == "Sign in to Work again")
        #expect(log.banners.last?.button == "Open Preferences")
        // Another account's state, or the same account still failing, keeps it.
        sc.apply(state("a2", .idle))
        sc.apply(state("a1", .authRequired))
        #expect(sc.authBannerAccount == "a1")
        #expect(log.banners.count == 1)
        // The account left the sign-in state: hidden, once.
        sc.apply(state("a1", .idle))
        #expect(sc.authBannerAccount == nil)
        #expect(log.banners.count == 2)
        #expect(log.banners.last?.account == nil)
        #expect(log.banners.last?.title == nil)
        sc.hideAuthBanner()
        #expect(log.banners.count == 2, "hiding a hidden banner emits nothing")
    }

    /// sync.go `refreshCertBanner`: the first enabled account, in account
    /// order, whose server's certificate was refused; hidden when none.
    @Test func certificateBanner() throws {
        let accounts = twoAccounts + [testAccount("a3", enabled: false, name: "Paused")]
        let (sc, log) = makeController(accounts: accounts)
        func tls(_ acc: AccountID, _ reason: TLSErrorReason, status: SyncStatus = .offline) throws -> SyncState {
            SyncState(accountId: acc, status: status, error: try tlsError(TLSErrorData(reason: reason)))
        }
        // A paused account and a handshake failure raise nothing.
        sc.apply(try tls("a3", .untrusted, status: .disabled))
        sc.apply(try tls("a1", .handshake))
        #expect(sc.certBannerAccount == nil && log.certBanners.isEmpty)
        #expect(sc.footer.text == "Offline: Work")

        sc.apply(try tls("a2", .untrusted))
        #expect(sc.certBannerAccount == "a2")
        #expect(sc.footer.text == "Certificate problem")
        #expect(log.certBanners.count == 1)
        #expect(log.certBanners.last?.title == "The certificate of home@example.invalid is not trusted")
        #expect(log.certBanners.last?.button == "Edit Account…")
        // The first account in account order wins.
        sc.apply(try tls("a1", .pinMismatch))
        #expect(sc.certBannerAccount == "a1")
        #expect(log.certBanners.last?.title == "The certificate of Work has changed")
        #expect(sc.footer.text == "Certificate changed")
        // Unchanged: nothing emitted.
        let emitted = log.certBanners.count
        sc.apply(try tls("a1", .pinMismatch))
        sc.refreshFooter()
        #expect(log.certBanners.count == emitted)
        // Fixed: the next account with a problem.
        sc.apply(state("a1", .syncing))
        #expect(sc.certBannerAccount == "a2")
        #expect(log.certBanners.last?.title == "The certificate of home@example.invalid is not trusted")
        // A pass that runs while the error still holds the last failure no
        // longer counts; neither does idle.
        sc.apply(SyncState(accountId: "a2", status: .syncing, error: try tlsError(TLSErrorData(reason: .untrusted))))
        #expect(sc.certBannerAccount == nil)
        #expect(log.certBanners.last?.account == nil && log.certBanners.last?.title == nil)
        let hidden = log.certBanners.count
        sc.apply(state("a2", .idle))
        #expect(log.certBanners.count == hidden, "hiding a hidden banner emits nothing")
    }

    @Test func authBannerTexts() {
        let (sc, _) = makeController()
        let password = testAccount("a1", name: "Work", email: "w@example.invalid")
        var goa = testAccount("a2", name: "Cloud", email: "c@example.invalid")
        goa.config.oauth2 = OAuth2Config(source: .goa, goaAccountId: "goa_1", provider: .google)
        var graph = testAccount("a3", name: "Office", email: "o@example.invalid")
        graph.config.kind = .graph
        graph.config.graph = GraphConfig(source: .goa, goaAccountId: "goa_2")
        var browser = testAccount("a4", name: "Mail", email: "m@example.invalid")
        browser.config.oauth2 = OAuth2Config(source: .daemon, provider: .google)
        var browserGraph = testAccount("a5", name: "Contoso", email: "c@example.invalid")
        browserGraph.config.kind = .graph
        browserGraph.config.graph = GraphConfig(source: .daemon)
        browserGraph.config.oauth2 = OAuth2Config(source: .daemon, provider: .office365)

        func n(_ acc: AccountID, _ reason: ErrorCode) -> AuthRequiredNotification {
            AuthRequiredNotification(accountId: acc, reason: reason, message: "detail")
        }
        #expect(sc.authBanner(for: n("a1", .authRequired), account: password) == ("Sign in to Work again", "Open Preferences"))
        #expect(sc.authBanner(for: n("a1", .keyringError), account: password) == ("The system keyring is unavailable; Work cannot sign in", "Open Preferences"))
        #expect(sc.authBanner(for: n("a1", .networkError), account: password) == ("Work needs attention", "Open Preferences"))
        #expect(sc.authBanner(for: n("a2", .authRequired), account: goa) == ("Sign in to Cloud again in Settings → Online Accounts", "Open Online Accounts"))
        #expect(sc.authBanner(for: n("a2", .unavailable), account: goa) == ("GNOME Online Accounts is not available; Cloud cannot sign in", "Open Online Accounts"))
        #expect(sc.authBanner(for: n("a3", .authRequired), account: graph) == ("Sign in to Office again in Settings → Online Accounts", "Open Online Accounts"))
        // The browser sign-in: signing in again is the repair, unless the
        // keyring failed.
        #expect(sc.authBanner(for: n("a4", .authRequired), account: browser) == ("Sign in to Mail again in your browser", "Sign In"))
        #expect(sc.authBanner(for: n("a4", .networkError), account: browser) == ("Sign in to Mail again in your browser", "Sign In"))
        #expect(sc.authBanner(for: n("a4", .keyringError), account: browser) == ("The system keyring is unavailable; Mail cannot sign in", "Sign In"))
        #expect(sc.authBanner(for: n("a5", .authFailed), account: browserGraph) == ("Sign in to Contoso again in your browser", "Sign In"))
        // An unknown account is named by its id.
        #expect(sc.authBanner(for: n("acc_zz", .authFailed), account: nil) == ("Sign in to acc_zz again", "Open Preferences"))
    }

    @Test func authBannerActions() {
        let (sc, log) = makeController()
        let password = testAccount("a1", name: "Work", email: "w@example.invalid")
        var goa = testAccount("a2", name: "Cloud", email: "c@example.invalid")
        goa.config.oauth2 = OAuth2Config(source: .goa, goaAccountId: "goa_1", provider: .google)
        var browser = testAccount("a4", name: "Mail", email: "m@example.invalid")
        browser.config.oauth2 = OAuth2Config(source: .daemon, provider: .google)

        let plain = AuthRequiredNotification(accountId: "a4", reason: .authRequired, message: "x")
        let withURL = AuthRequiredNotification(accountId: "a4", reason: .authRequired, message: "x", authUrl: "https://accounts.google.com/o/oauth2/v2/auth?s=1")
        let emptyURL = AuthRequiredNotification(accountId: "a4", reason: .authRequired, message: "x", authUrl: "")
        #expect(sc.authBannerAction(for: plain, account: password) == .openPreferences)
        #expect(sc.authBannerAction(for: plain, account: nil) == .openPreferences)
        #expect(sc.authBannerAction(for: plain, account: goa) == .openOnlineAccounts)
        #expect(sc.authBannerAction(for: plain, account: browser) == .signInAgain("a4", fallbackURL: nil))
        #expect(sc.authBannerAction(for: emptyURL, account: browser) == .signInAgain("a4", fallbackURL: nil))
        #expect(sc.authBannerAction(for: withURL, account: browser) == .signInAgain("a4", fallbackURL: "https://accounts.google.com/o/oauth2/v2/auth?s=1"))
        // Not listed yet, but only the daemon's own sign-in has a URL.
        #expect(sc.authBannerAction(for: withURL, account: nil) == .signInAgain("a4", fallbackURL: "https://accounts.google.com/o/oauth2/v2/auth?s=1"))
        #expect(sc.authBanner(for: withURL, account: nil) == ("Sign in to a4 again in your browser", "Sign In"))
        #expect(sc.authBannerAction(for: emptyURL, account: nil) == .openPreferences)

        // The shown banner keeps its action until it hides.
        #expect(sc.authBannerAction == nil)
        sc.showAuthRequired(withURL, account: browser)
        #expect(sc.authBannerAction == .signInAgain("a4", fallbackURL: "https://accounts.google.com/o/oauth2/v2/auth?s=1"))
        #expect(log.banners.last?.title == "Sign in to Mail again in your browser")
        #expect(log.banners.last?.button == "Sign In")
        sc.apply(state("a4", .idle))
        #expect(sc.authBannerAction == nil && sc.authBannerAccount == nil)
        sc.showAuthRequired(plain, account: password)
        #expect(sc.authBannerAction == .openPreferences)
        sc.hideAuthBanner()
        #expect(sc.authBannerAction == nil)
    }

    @Test func requestSignInURLStartsASessionForTheAccount() async throws {
        let fake = try FakeDaemon()
        let seen = ParamsBox()
        await fake.on(API.AccountOAuthStart.name) { p in
            await seen.set(p)
            return json(#"{"sessionId":"s_9","authUrl":"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1","expiresAt":"2026-09-25T10:10:00Z"}"#)
        }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()

        let (sc, _) = makeController()
        let outcome = await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: "https://stale.example/x")
        #expect(outcome == .open("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1"))
        let params = try #require(await seen.value)
        let sent = try JSONCoding.decoder().decode(AccountOAuthStartParams.self, from: params)
        #expect(sent.accountId == "a5" && sent.config == nil)
        #expect(sent.browserPage?.successTitle == "Signed in")
        await client.close()
    }

    @Test func requestSignInURLFallsBackToTheNotificationsPage() async throws {
        let fake = try FakeDaemon()
        await fake.on(API.AccountOAuthStart.name) { _ in throw RPCError(code: .unavailable, message: "too many") }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()

        let (sc, _) = makeController()
        #expect(await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: "https://login.example/x") == .open("https://login.example/x"))
        #expect(await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: nil) == .failed("Starting the sign-in failed"))
        #expect(await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: "") == .failed("Starting the sign-in failed"))
        #expect(await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: "http://login.example/x")
                == .failed("The link could not be opened: not an https address"))
        await client.close()
    }

    @Test func requestSignInURLRefusesWhatIsNotABrowserAddress() async throws {
        let fake = try FakeDaemon()
        let answers = ["", "file:///tmp/x"]
        let calls = CallCount()
        await fake.on(API.AccountOAuthStart.name) { _ in
            let url = answers[min(await calls.next(), answers.count) - 1]
            return json(#"{"sessionId":"s_1","authUrl":"\#(url)","expiresAt":"2026-09-25T10:10:00Z"}"#)
        }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let client = RPCClient(socketPath: fake.path)
        try await client.connect()

        let (sc, _) = makeController()
        // An empty answer is a failed start; the fallback is for failed calls only.
        #expect(await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: "https://login.example/x") == .failed("Starting the sign-in failed"))
        #expect(await sc.requestSignInURL(client: client, accountId: "a5", fallbackURL: nil) == .failed("The link could not be opened: not an https address"))
        await client.close()
    }

    /// sync.go `loadSyncStatus`: a failed sync.status says "Not syncing"
    /// on the line (status.go `statusLineFor`) until a state arrives after
    /// all, or the connection changes; the sync half keeps its own text.
    @Test func loadSyncStatusFailureSaysNotSyncing() async throws {
        let fixture = try MailFixture()
        await fixture.fail(API.SyncStatus.name, with: RPCError(code: .notImplemented, message: "no syncer"))
        try await fixture.start()
        defer { Task { await fixture.stop() } }
        let client = RPCClient(socketPath: fixture.path)
        try await client.connect()

        let (sc, log) = makeController()
        sc.setConnection(.connected(info))
        sc.apply(state("a1", .syncing, folder: "f_inbox"))
        #expect(sc.line.spinning)
        sc.loadSyncStatus(client: client)
        try await waitUntil { log.lines.last?.text == "Not syncing" }
        #expect(sc.line == StatusLine(text: "Not syncing", active: true, daemon: "Connected to malachid 1.2.3 (pid 4242)"))
        #expect(sc.footer == .init(text: "Syncing Inbox…", spinning: true))
        #expect(sc.connection.syncFailed)
        #expect(await fixture.callCount(API.SyncStatus.name) == 1)

        // The minute's redraw keeps it.
        sc.refreshFooter()
        #expect(sc.line.text == "Not syncing")
        // A state from the daemon takes the line back.
        sc.apply(state("a1", .idle))
        #expect(sc.line == StatusLine(text: "Up to date", active: true, daemon: "Connected to malachid 1.2.3 (pid 4242)"))
        // So does a new connection.
        sc.loadSyncStatus(client: client)
        try await waitUntil { log.lines.last?.text == "Not syncing" }
        sc.setConnection(.infoFailed("x"))
        #expect(sc.line == StatusLine(text: "Up to date", active: true, daemon: "Connected, but system.info failed"))
        await client.close()
    }

    @Test func loadSyncStatusAppliesEveryState() async throws {
        let fixture = try MailFixture()
        await fixture.setSyncStates([state("a1", .idle, pending: 1), state("a2", .offline)])
        try await fixture.start()
        defer { Task { await fixture.stop() } }
        let client = RPCClient(socketPath: fixture.path)
        try await client.connect()

        let (sc, log) = makeController()
        var seen: [AccountID] = []
        sc.loadSyncStatus(client: client) { s in
            seen.append(s.accountId)
            sc.apply(s)
        }
        try await waitUntil { seen.count == 2 }
        #expect(seen == ["a1", "a2"])
        #expect(sc.footer == .init(text: "Sending 1 message…", spinning: true))
        #expect(log.footers.count == 2)

        // Without `each`, the states are applied directly.
        let (plain, plainLog) = makeController()
        plain.loadSyncStatus(client: client)
        try await waitUntil { plain.states.count == 2 }
        #expect(plain.footer.text == "Sending 1 message…")
        #expect(plainLog.footers.count == 2)
        await client.close()
    }

    @Test func beginCheckingFallsBackToTheComputedLine() async throws {
        let (sc, log) = makeController()
        sc.setConnection(.connected(info))
        sc.fallbackDelay = .milliseconds(100)
        sc.beginChecking()
        #expect(sc.footer == .init(text: "Checking for new mail…", spinning: true))
        #expect(sc.line == StatusLine(text: "Checking for new mail…", spinning: true, active: true, daemon: "Connected to malachid 1.2.3 (pid 4242)"))
        try await waitUntil { log.footers.last?.text == "Up to date" }
        #expect(!sc.footer.spinning)
        #expect(sc.line.text == "Up to date" && !sc.line.spinning)

        // A syncState in the meantime takes over; the fallback then only
        // recomputes what is already shown.
        sc.beginChecking()
        sc.apply(state("a1", .syncing, folder: "f_inbox"))
        #expect(sc.footer.text == "Syncing Inbox…")
        try await Task.sleep(for: .milliseconds(200))
        #expect(sc.footer == .init(text: "Syncing Inbox…", spinning: true))

        // Closed: the timer emits nothing any more.
        sc.beginChecking()
        let count = log.footers.count
        sc.close()
        try await Task.sleep(for: .milliseconds(200))
        #expect(log.footers.count == count)
    }

    @Test func connectionStatusLineTexts() {
        #expect(connectionStatusLine(.connecting) == ("network-idle-symbolic", "Connecting to backend…"))
        #expect(connectionStatusLine(.connected(info)) == ("network-transmit-receive-symbolic", "Connected to malachid 1.2.3 (pid 4242)"))
        #expect(connectionStatusLine(.protocolMismatch(daemon: 9)) == ("network-transmit-receive-symbolic", "Protocol mismatch: UI \(API.protocolVersion), backend 9"))
        #expect(connectionStatusLine(.infoFailed("x")) == ("network-transmit-receive-symbolic", "Connected, but system.info failed"))
        #expect(connectionStatusLine(.unavailable("x")) == ("network-offline-symbolic", "Backend unavailable"))
        #expect(connectionStatusLine(.stopping) == ("network-offline-symbolic", "Backend unavailable"))
    }

    /// status_test.go `TestStatusLineFor`. GTK has one moment more,
    /// connected with system.info on its way ("connected, system.info
    /// pending"); the connection controller reports a connection only once
    /// system.info answered, so there is no such state to test here.
    @Test func statusLineForTest() {
        let other = ConnectionController.ConnectionState.protocolMismatch(daemon: API.protocolVersion + 1)
        let mismatch = "Protocol mismatch: UI \(API.protocolVersion), backend \(API.protocolVersion + 1)"
        let daemon = "Connected to malachid 1.2.3 (pid 4242)"
        let cases: [(String, ConnView, String, Bool, StatusLine)] = [
            ("connecting", ConnView(state: .connecting), "Up to date", false,
             StatusLine(text: "Connecting to backend…", icon: "network-idle-symbolic")),
            ("unavailable", ConnView(state: .unavailable("gone")), "Syncing Inbox…", true,
             StatusLine(text: "Backend unavailable", icon: "network-offline-symbolic")),
            // What the daemon said before it went away does not count.
            ("unavailable forgets", ConnView(state: .unavailable("gone"), syncFailed: true), "Up to date", false,
             StatusLine(text: "Backend unavailable", icon: "network-offline-symbolic")),
            ("stopping", ConnView(state: .stopping), "Up to date", false,
             StatusLine(text: "Backend unavailable", icon: "network-offline-symbolic")),
            ("connected", ConnView(state: .connected(info)), "Up to date · 15:04", false,
             StatusLine(text: "Up to date · 15:04", active: true, daemon: daemon)),
            ("connected and syncing", ConnView(state: .connected(info)), "Syncing Inbox…", true,
             StatusLine(text: "Syncing Inbox…", spinning: true, active: true, daemon: daemon)),
            ("system.info failed", ConnView(state: .infoFailed("x")), "Up to date", false,
             StatusLine(text: "Up to date", active: true, daemon: "Connected, but system.info failed")),
            ("protocol mismatch", ConnView(state: other), "Syncing Inbox…", true,
             StatusLine(text: mismatch, active: true)),
            ("protocol mismatch beats sync.status", ConnView(state: other, syncFailed: true), "Up to date", false,
             StatusLine(text: mismatch, active: true)),
            ("sync.status failed", ConnView(state: .connected(info), syncFailed: true), "Syncing Inbox…", true,
             StatusLine(text: "Not syncing", active: true, daemon: daemon)),
            // An empty line (no account at all) is no button to tab to.
            ("no account", ConnView(state: .connected(info)), "", false,
             StatusLine(text: "", daemon: daemon)),
            ("no account, sync.status failed", ConnView(state: .infoFailed("x"), syncFailed: true), "", false,
             StatusLine(text: "Not syncing", active: true, daemon: "Connected, but system.info failed")),
        ]
        for (name, conn, text, spinning, want) in cases {
            let got = statusLineFor(conn, text: text, spinning: spinning)
            #expect(got == want, "\(name): got \(got)")
        }
    }

    /// window.go `showConnectionState`: the line names the connection until
    /// there is one, starting with the first attempt, and follows each
    /// change; the sync half stays what the accounts say.
    @Test func lineFollowsTheConnection() {
        let (sc, log) = makeController()
        #expect(sc.line == StatusLine(text: "Connecting to backend…", icon: "network-idle-symbolic"), "before anything")
        sc.apply(state("a1", .syncing, folder: "f_inbox", progress: 5))
        #expect(sc.line == StatusLine(text: "Connecting to backend…", icon: "network-idle-symbolic"))
        #expect(sc.footer == .init(text: "Syncing Inbox… 5 %", spinning: true))

        sc.setConnection(.connected(info))
        #expect(sc.line == StatusLine(text: "Syncing Inbox… 5 %", spinning: true, active: true, daemon: "Connected to malachid 1.2.3 (pid 4242)"))
        #expect(log.lines.last == sc.line)

        sc.setConnection(.unavailable("gone"))
        #expect(sc.line == StatusLine(text: "Backend unavailable", icon: "network-offline-symbolic"))
        // A check started meanwhile changes only the text and the spinner.
        sc.beginChecking()
        #expect(sc.line == StatusLine(text: "Checking for new mail…", spinning: true, icon: "network-offline-symbolic"))
        sc.close()
    }

    /// The line names the time of the last check; the refresh timer redraws
    /// it, so a day later it shows the date (window.go's
    /// `statusRefreshSeconds` timeout; `now` is the controller's clock).
    @Test func refreshRedrawsTheTimeOfTheLastCheck() async throws {
        let (sc, log) = makeController()
        let cal = Calendar.current
        let today = try #require(cal.date(from: DateComponents(year: 2026, month: 9, day: 2, hour: 15, minute: 30)))
        let checked = today.addingTimeInterval(-2 * 3600)
        var clock = today
        sc.now = { clock }
        sc.apply(state("a1", .idle, last: checked))
        #expect(sc.footer.text == "Up to date · 13:30")

        let tomorrow = try #require(cal.date(byAdding: .day, value: 1, to: today))
        clock = tomorrow
        let dated = "Up to date · " + formatDate(checked, now: tomorrow)
        sc.startRefreshing(every: .milliseconds(50))
        try await waitUntil { log.footers.last?.text == dated }
        #expect(sc.footer.text == dated)

        // Closed: the timer emits nothing any more.
        sc.close()
        let count = log.footers.count
        try await Task.sleep(for: .milliseconds(200))
        #expect(log.footers.count == count)
    }

    /// The popover's rows over the controller's states, accounts, folder
    /// names and clock (status.go `refreshStatusPopover`).
    @Test func accountStatusesOverTheController() throws {
        let (sc, _) = makeController(accounts: twoAccounts + [testAccount("a3", enabled: false, name: "Paused")])
        sc.now = { Date(timeIntervalSince1970: 1_788_343_200) }
        sc.apply(state("a1", .syncing, folder: "f_inbox", progress: 42))
        sc.apply(state("a2", .offline, failed: 2))
        let got = sc.accountStatuses()
        #expect(got == [
            AccountStatus(account: "a1", title: "Work", detail: "Syncing Inbox… 42 %", action: .check),
            AccountStatus(account: "a2", title: "home@example.invalid", detail: "Offline, retrying", action: .retry, failed: 2),
            AccountStatus(account: "a3", title: "Paused", detail: "Paused"),
        ])
    }
}
