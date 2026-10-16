// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// An account's Calendar and Invitations groups (CalendarAccountController)
// over the in-memory samples: the Graph switch with the consent question
// and the re-sign-in hand-off, the calendars' checkboxes and default by
// remote id (Graph from calendar.list, CalDAV also from the server), Keep Events, the invitation switches, an account without a
// calendar, a refusal undone.

@MainActor
private func account(_ src: InMemoryCalendarSource, _ id: AccountID) -> CalendarAccountController {
    let c = CalendarAccountController(source: src, accountId: id)
    c.start()
    return c
}

@MainActor
private func lastConfig(_ src: InMemoryCalendarSource) -> CalendarConfig? {
    guard case .updateAccount(let p) = src.writes.last else { return nil }
    return p.config.calendar
}

@MainActor
@Suite(.serialized) struct CalendarAccountControllerTests {
    @Test func graphSyncAsksForConsent() async throws {
        let src = calSamples()
        // The Work account with sync off.
        src.update { s in
            s.accounts[0].config.calendar = CalendarConfig(sync: false)
        }
        let c = account(src, Cal.sampleWork)
        #expect(c.isGraph && c.showsSyncSwitch && !c.syncOn && c.hasCalendarCapability && !c.showsNoCalendar)
        var questions: [Cal.Question] = []
        var signIn: [AccountID] = []
        c.onQuestion = { q in questions.append(q); return .confirm }
        c.onSignIn = { signIn.append($0) }
        c.setSync(true)
        #expect(c.syncOn && c.saving)
        try await calWaitUntil { !c.saving }
        #expect(lastConfig(src)?.sync == true)
        if case .updateAccount(let p) = src.writes.last {
            #expect(p.credentials == Credentials())
        }
        // The daemon reports the missing scopes.
        src.handle(.state(CalendarSyncState(accountId: Cal.sampleWork, status: .consentRequired, progress: -1)))
        try await calWaitUntil { !signIn.isEmpty }
        #expect(questions.first?.body == Cal.Prefs.syncConsentNote)
        #expect(signIn == [Cal.sampleWork] && c.consentRequired)
    }

    @Test func graphCalendarsToggleAndDefault() async throws {
        let src = calSamples()
        src.update { s in
            s.calendarAccounts[0].calendars.append(CalendarRecord(
                id: "cal_sample_old", accountId: Cal.sampleWork, remoteId: "AAMkSampleOld", name: "Old projects",
                color: "#888888", visible: false, access: .owner, kind: .events, disabled: true))
            // As the daemon keeps it: the account's configuration names it.
            s.accounts[0].config.calendar = CalendarConfig(sync: true, disabledCalendars: ["AAMkSampleOld"])
        }
        let c = account(src, Cal.sampleWork)
        #expect(c.listed == nil)
        #expect(c.rows.map(\.name) == ["Calendar", "Birthdays", "Czech holidays", "Old projects"])
        #expect(c.rows.allSatisfy { $0.canToggle })
        #expect(c.rows.map(\.enabled) == [true, true, true, false])
        #expect(c.rows[0].note == "default")
        c.setEnabled(false, remoteId: "AAMkSampleHolidays")
        try await calWaitUntil { !c.saving }
        #expect(lastConfig(src)?.disabledCalendars == ["AAMkSampleOld", "AAMkSampleHolidays"])
        #expect(c.rows[2].enabled == false)
        c.setEnabled(true, remoteId: "AAMkSampleOld")
        try await calWaitUntil { !c.saving }
        #expect(c.rows[3].enabled && c.defaultChoices.map(\.remoteId) == ["AAMkSampleCalendar", "AAMkSampleOld"])
        c.setDefaultCalendar("AAMkSampleOld")
        try await calWaitUntil { !c.saving }
        #expect(lastConfig(src)?.defaultCalendar == "AAMkSampleOld")
        #expect(c.rows[3].note == "default" && c.rows[0].note.isEmpty)
        // Disabling the default drops it.
        c.setEnabled(false, remoteId: "AAMkSampleOld")
        try await calWaitUntil { !c.saving }
        #expect(lastConfig(src)?.defaultCalendar == nil)
        let listedCalDAV = src.writes.contains { w in
            if case .listCalDAV = w { return true }
            return false
        }
        #expect(!listedCalDAV)
    }

    @Test func caldavCalendarsDefaultAndKeep() async throws {
        let src = calSamples()
        src.listResult = AccountListCalDAVCalendarsResult(
            principalUrl: "p", homeSetUrl: "h", scheduling: .client,
            calendars: [
                CalDAVCalendar(href: "/remote.php/dav/calendars/me/sport/", name: "Sport", color: "#0082c9", access: .owner, isDefault: true),
                CalDAVCalendar(href: "/c/club/", name: "Club", access: .read),
            ])
        let c = account(src, Cal.sampleNextcloud)
        try await calWaitUntil { c.listed != nil }
        guard case .listCalDAV(let l) = src.writes.first else {
            Issue.record("no list")
            return
        }
        #expect(l.accountId == Cal.sampleNextcloud && l.credentials == Credentials())
        #expect(c.isCalDAV && !c.showsSyncSwitch && c.syncOn)
        // The stored calendar once, then the server's calendar not stored.
        #expect(c.rows.map(\.name) == ["Sport", "Club"] && c.rows.allSatisfy { $0.canToggle })
        #expect(c.rows[0].note == "default" && c.rows[1].note == "read only")
        c.setEnabled(false, remoteId: "/c/club/")
        try await calWaitUntil { !c.saving }
        #expect(lastConfig(src)?.disabledCalendars == ["/c/club/"])
        #expect(c.rows[1].enabled == false)
        #expect(c.defaultChoices.map(\.remoteId) == ["/remote.php/dav/calendars/me/sport/"])
        c.setKeepDays(90)
        try await calWaitUntil { !c.saving }
        #expect(lastConfig(src)?.keepDays == 90 && c.keepDays == 90)
        c.setKeepDays(91)
        #expect(c.keepDays == 90)
    }

    @Test func invitationSwitches() async throws {
        let src = calSamples()
        let c = account(src, Cal.sampleWork)
        #expect(c.invitationCard && c.showUnanswered && c.afterAnswer == .keep)
        c.setInvitationCard(false)
        try await calWaitUntil { !c.saving }
        c.setAfterAnswer(.archive)
        try await calWaitUntil { !c.saving }
        c.setShowUnanswered(false)
        try await calWaitUntil { !c.saving }
        let cfg = try #require(lastConfig(src))
        #expect(cfg.invitationCard == false && cfg.afterAnswer == .archive && cfg.showUnanswered == false)
    }

    @Test func accountWithoutACalendar() {
        let src = calSamples()
        src.update { s in
            s.accounts.append(Account(
                id: "acc_imap", config: AccountConfig(name: "Club", email: "c@example.org", kind: .imap), enabled: true,
                state: SyncState(accountId: "acc_imap", status: .idle), capabilities: API.mailCapabilities))
        }
        let c = account(src, "acc_imap")
        #expect(c.showsNoCalendar)
        var asked = 0
        c.onAddCalDAV = { asked += 1 }
        c.addCalDAV()
        #expect(asked == 1)
    }

    @Test func refusalIsUndone() async throws {
        let src = calSamples()
        src.failures[API.AccountUpdate.name] = RPCError(code: .invalidArgument, message: "no")
        let c = account(src, Cal.sampleWork)
        c.setAfterAnswer(.archive)
        #expect(c.afterAnswer == .archive)
        try await calWaitUntil { !c.saving }
        #expect(c.afterAnswer == .keep && !c.error.isEmpty)
    }

    @Test func caldavAccountGroup() async throws {
        let src = calSamples()
        src.listResult = AccountListCalDAVCalendarsResult(principalUrl: "p", homeSetUrl: "h", scheduling: .client, calendars: [])
        let c = account(src, Cal.sampleNextcloud)
        try await calWaitUntil { c.listed != nil }
        #expect(c.scheduling == .client && c.serverHost == "cloud.example.com" && c.username == "me")
        #expect(!c.certificatePinned && c.certificateText == "Verified" && c.mailAccountId == nil)
        #expect(c.mailAccounts.map(\.id) == [Cal.sampleWork, Cal.sampleGmail])
        c.setName("  Cloud  ")
        try await calWaitUntil { !c.saving }
        guard case .updateAccount(let p) = src.writes.last else {
            Issue.record("no update")
            return
        }
        #expect(p.config.name == "Cloud" && p.credentials == Credentials())
        c.setName(" ")
        c.setMailAccount(Cal.sampleWork)
        try await calWaitUntil { !c.saving }
        guard case .updateAccount(let m) = src.writes.last else { return }
        #expect(m.config.caldav?.mailAccountId == Cal.sampleWork && m.config.name == "Cloud")
        c.replacePassword("new secret")
        try await calWaitUntil { !c.saving }
        guard case .updateAccount(let pw) = src.writes.last else { return }
        #expect(pw.credentials.password == "new secret")
        // A pin, then Forget.
        src.update { s in
            if let i = s.accounts.firstIndex(where: { $0.id == Cal.sampleNextcloud }) {
                s.accounts[i].config = pw.config
                s.accounts[i].config.caldav?.certificateSha256 = String(repeating: "ab", count: 32)
            }
        }
        try await calWaitUntil { c.certificatePinned }
        #expect(c.certificateText == "Trusted by you")
        c.forgetCertificate()
        try await calWaitUntil { !c.saving }
        guard case .updateAccount(let f) = src.writes.last else { return }
        #expect(f.config.caldav?.certificateSha256 == nil && !c.certificatePinned)
    }
}
