// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Add CalDAV Calendar sheet's controller (CalDAVWizardController) over
// the in-memory source: discover → list → add with their params, the
// mail-account popup, a refused discovery, and the certificate trust that
// runs discovery again with the pin only after the confirmation.

private let sha = String(repeating: "cd", count: 32)

@MainActor
private func wizard(_ src: InMemoryCalendarSource) -> CalDAVWizardController {
    src.discoverResult = AccountDiscoverCalDAVResult(
        serverUrl: "https://dav.example.net/remote.php/dav", foundVia: .wellKnown, needsCredentials: true)
    src.listResult = AccountListCalDAVCalendarsResult(
        principalUrl: "https://dav.example.net/p/", homeSetUrl: "https://dav.example.net/c/", displayName: "Jana",
        addresses: ["jana@example.org"], scheduling: .client,
        calendars: [
            CalDAVCalendar(href: "/c/personal/", name: "Personal", access: .owner, isDefault: true),
            CalDAVCalendar(href: "/c/work/", name: "Work", access: .owner),
        ])
    src.addedAccount = "acc_dav"
    let w = CalDAVWizardController(source: src)
    w.edit(name: "", server: "dav.example.net", username: "jana", password: "secret")
    return w
}

@MainActor
@Suite(.serialized) struct CalendarCalDAVWizardControllerTests {
    @Test func discoverListAndAdd() async throws {
        let src = calSamples()
        let w = wizard(src)
        // The mail accounts that can send: Work and Gmail, not Nextcloud.
        #expect(w.mailAccounts.map(\.id) == [Cal.sampleWork, Cal.sampleGmail])
        #expect(w.wizard.mailAccount == Cal.sampleWork)
        #expect(w.wizard.canDiscover)
        w.discover()
        try await calWaitUntil { w.wizard.phase == .listed }
        #expect(w.wizard.foundLine == "Found through /.well-known/caldav · 2 calendars")
        #expect(w.wizard.showsMailAccount)
        w.setMailAccount(Cal.sampleGmail)
        w.toggle("/c/work/", false)
        var done: [AccountID] = []
        w.onDone = { done.append($0) }
        w.add()
        try await calWaitUntil { !done.isEmpty }
        #expect(done == ["acc_dav"] && w.wizard.phase == .added)
        #expect(src.writes.count == 3)
        guard case .discover(let d) = src.writes[0], case .listCalDAV(let l) = src.writes[1],
              case .addAccount(let a) = src.writes[2] else {
            Issue.record("calls")
            return
        }
        #expect(d.url == "dav.example.net" && d.certificateSha256 == nil)
        #expect(l.config.caldav?.serverUrl == "https://dav.example.net/remote.php/dav" && l.credentials.password == "secret")
        #expect(a.config.kind == .caldav && a.config.name == "Jana" && a.config.email == "jana@example.org")
        #expect(a.config.caldav?.mailAccountId == Cal.sampleGmail)
        #expect(a.config.calendar?.disabledCalendars == ["/c/work/"] && a.config.calendar?.defaultCalendar == "/c/personal/")
    }

    @Test func refusedDiscoveryGoesBackToEntering() async throws {
        let src = calSamples()
        src.failures[API.AccountDiscoverCalDAV.name] = RPCError(code: .serverError, message: "not caldav")
        let w = wizard(src)
        w.discover()
        try await calWaitUntil { w.wizard.phase == .entering && w.wizard.error != nil }
        #expect(w.errorText == "The server answered, but it is not a CalDAV server.")
        let preview = wizard(calSamples(writes: false))
        preview.discover()
        try await calWaitUntil { preview.wizard.phase == .entering && preview.previewRefused }
        #expect(preview.errorText == Cal.Text.previewOnly)
    }

    @Test func trustRunsDiscoveryAgainWithThePin() async throws {
        let src = calSamples()
        var d = TLSErrorData(reason: .untrusted)
        d.certificate = CertificateInfo(
            sha256: sha, issuer: "Home CA", notBefore: Date(timeIntervalSince1970: 0), notAfter: Date(timeIntervalSince1970: 0),
            selfSigned: true)
        src.failures[API.AccountDiscoverCalDAV.name] = try tlsError(d)
        let w = wizard(src)
        w.discover()
        try await calWaitUntil { w.wizard.trustOffer != nil }
        // Refused: nothing pinned.
        var prompts: [TrustPrompt] = []
        var answer = false
        w.onConfirmTrust = { p in prompts.append(p); return answer }
        w.trust()
        try await calWaitUntil { prompts.count == 1 }
        try await Task.sleep(for: .milliseconds(10))
        #expect(src.writes.count == 1 && w.wizard.pin.isEmpty)
        // Confirmed: discovery again with the pin, then the list.
        src.failures[API.AccountDiscoverCalDAV.name] = nil
        answer = true
        w.trust()
        try await calWaitUntil { w.wizard.phase == .listed }
        #expect(prompts.count == 2)
        guard case .discover(let again) = src.writes[1] else {
            Issue.record("no second discovery")
            return
        }
        #expect(again.certificateSha256 == sha)
        #expect(w.wizard.certificateText == "Trusted by you")
    }

    @Test func listedCalendarsAndTheMailAccountDefault() async throws {
        let src = calSamples()
        let w = wizard(src)
        w.start()
        w.discover()
        try await calWaitUntil { w.wizard.phase == .listed }
        #expect(w.listedCalendars.map(\.name) == ["Personal", "Work"])
        #expect(w.listedCalendars.map(\.note) == ["default", ""] && w.listedCalendars.allSatisfy(\.selected))
        w.toggle("/c/work/", false)
        #expect(w.listedCalendars.map(\.selected) == [true, false])
        // None chosen stays None when the snapshot changes.
        w.setMailAccount(nil)
        src.update { _ in }
        #expect(w.wizard.mailAccount == nil)
        w.cancel()
    }
}
