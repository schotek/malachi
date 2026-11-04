// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// MalachiCore/Calendar/CalDAVWizard.swift, the cases of Go caldav_test.go.

private let testSHA = String(repeating: "ab", count: 32)

private func tlsErr(_ reason: TLSErrorReason, _ sha: String) throws -> RPCError {
    var d = TLSErrorData(reason: reason)
    if !sha.isEmpty {
        d.certificate = CertificateInfo(
            sha256: sha, issuer: "Home CA", notBefore: Date(timeIntervalSince1970: 0), notAfter: Date(timeIntervalSince1970: 0),
            selfSigned: true)
    }
    return try tlsError(d)
}

private func listed() -> AccountListCalDAVCalendarsResult {
    AccountListCalDAVCalendarsResult(
        principalUrl: "https://dav.example.net/p/", homeSetUrl: "https://dav.example.net/c/", displayName: "Jana",
        addresses: ["jana@example.org"], scheduling: .client,
        calendars: [
            CalDAVCalendar(href: "/c/personal/", name: "Personal", access: .owner, isDefault: true),
            CalDAVCalendar(href: "/c/work/", name: "Work", access: .owner),
            CalDAVCalendar(href: "/c/holidays/", name: "Holidays", access: .read),
        ])
}

private func found(_ url: String, _ via: CalDAVFoundVia, needs: Bool = false, cert: CertificateInfo? = nil) -> AccountDiscoverCalDAVResult {
    AccountDiscoverCalDAVResult(serverUrl: url, foundVia: via, needsCredentials: needs, certificate: cert)
}

private func err(_ code: ErrorCode) -> RPCError { RPCError(code: code, message: "x") }

@Suite struct CalDAVWizardTests {
    typealias W = Cal.CalDAVWizard

    @Test func flow() {
        var w = W(mailAccount: "acc_mail")
        #expect(!w.canDiscover && !w.beginDiscover() && !w.canAdd)
        w.edit(name: "", server: "cloud.example.org", username: "jana", password: "")
        #expect(!w.canDiscover)
        w.edit(name: "", server: "cloud.example.org", username: "jana", password: "secret")
        #expect(w.canDiscover && w.beginDiscover() && w.phase == .discovering && w.step == .discover && !w.fieldsEnabled)
        #expect(w.discoverParams.url == "cloud.example.org")
        w.edit(name: "x", server: "other.example.org", username: "x", password: "x")
        #expect(w.server == "cloud.example.org")
        #expect(w.progress == "Looking for calendars…" && !w.canAdd)
        w.listDone(listed(), error: nil)  // out of order: ignored
        #expect(w.listed == nil)
        w.discoverDone(found("https://dav.example.net", .wellKnown, needs: true, cert: CertificateInfo(
            sha256: testSHA, issuer: "R11", notBefore: Date(timeIntervalSince1970: 0), notAfter: Date(timeIntervalSince1970: 0))), error: nil)
        #expect(w.phase == .discovering && w.step == .list)
        #expect(w.serverChanged == "The calendars are at dav.example.net." && w.certificateText == "Verified (R11)")
        let lp = w.listParams
        #expect(lp.config.kind == .caldav && lp.config.caldav?.serverUrl == "https://dav.example.net")
        #expect(lp.config.caldav?.username == "jana" && lp.credentials.password == "secret" && lp.config.name == "cloud.example.org")
        w.listDone(listed(), error: nil)
        #expect(w.phase == .listed && w.selected.count == 3 && w.name == "Jana" && w.showsMailAccount && w.canAdd)
        #expect(w.foundLine == "Found through /.well-known/caldav · 3 calendars")
        w.toggle("/c/holidays/", false)
        w.toggle("/nowhere/", true)
        #expect(!w.isSelected("/nowhere/"))
        w.toggle("/c/personal/", false)
        w.toggle("/c/work/", false)
        #expect(!w.canAdd && !w.beginAdd())
        w.toggle("/c/personal/", true)
        w.toggle("/c/work/", true)
        #expect(w.beginAdd() && w.phase == .adding && w.progress == "Adding the account…")
        let ap = w.addParams
        #expect(ap.config.name == "Jana" && ap.config.email == "jana@example.org" && ap.config.caldav?.mailAccountId == "acc_mail")
        #expect(ap.config.caldav?.principalUrl == "https://dav.example.net/p/" && ap.config.calendar?.sync == true)
        #expect(ap.config.calendar?.disabledCalendars == ["/c/holidays/"] && ap.config.calendar?.defaultCalendar == "/c/personal/")
        #expect(ap.credentials.password == "secret")
        w.addDone(nil, error: err(.authFailed))
        #expect(w.phase == .listed && w.errorText == "The server did not accept the username or password.")
        w.beginAdd()
        w.addDone("acc_new", error: nil)
        #expect(w.phase == .added && w.accountId == "acc_new" && w.password == "")
    }

    /// caldav_test.go TestWizardAddsAServerWithOnlyAddressBooks: a server
    /// with only address books is added for its contacts: Add is on without
    /// a calendar and the calendar is not synchronised; without a book and
    /// a calendar Add stays off.
    @Test func addsAServerWithOnlyAddressBooks() {
        var w = W(mailAccount: nil)
        w.edit(name: "Cloud", server: "dav.example.net", username: "jana", password: "secret")
        let began = w.beginDiscover()
        #expect(began)
        w.discoverDone(found("https://dav.example.net/", .url), error: nil)
        w.listDone(AccountListCalDAVCalendarsResult(
            principalUrl: "https://dav.example.net/p/", homeSetUrl: "", scheduling: .server,
            addressBookHomeSetUrl: "https://dav.example.net/ab/",
            addressBooks: [CardDAVAddressBook(href: "/ab/family/", name: "Family")]), error: nil)
        #expect(w.phase == .listed && w.canAdd)
        #expect(w.foundLine.hasSuffix("· 1 address book"))
        let ap = w.addParams
        #expect(ap.config.calendar?.sync == false && ap.config.caldav?.contacts == true)
        #expect(ap.config.caldav?.addressBookHomeSetUrl == "https://dav.example.net/ab/")
    }

    /// caldav_test.go TestWizardFlow's address books: no contacts without
    /// books, on with their home set when the listing found some.
    @Test func addressBooksComeWithTheAccount() {
        var w = W(mailAccount: nil)
        w.edit(name: "", server: "cloud.example.org", username: "jana", password: "pw")
        w.beginDiscover()
        w.discoverDone(found("https://dav.example.net", .wellKnown), error: nil)
        w.listDone(listed(), error: nil)
        var c = w.addParams.config.caldav
        #expect(c?.contacts == false && c?.addressBookHomeSetUrl == nil && w.addParams.config.calendar?.sync == true)
        var l = listed()
        l.addressBookHomeSetUrl = "https://dav.example.net/ab/"
        l.addressBooks = [CardDAVAddressBook(href: "/ab/family/", name: "Family")]
        var v = W(mailAccount: nil)
        v.edit(name: "", server: "cloud.example.org", username: "jana", password: "pw")
        v.beginDiscover()
        v.discoverDone(found("https://dav.example.net", .wellKnown), error: nil)
        v.listDone(l, error: nil)
        c = v.addParams.config.caldav
        #expect(c?.contacts == true && c?.addressBookHomeSetUrl == "https://dav.example.net/ab/")
        #expect(v.addressBooks.count == 1)
        // Without a calendar and a book Add stays off.
        var e = W(mailAccount: nil)
        e.edit(name: "", server: "cloud.example.org", username: "jana", password: "pw")
        e.beginDiscover()
        e.discoverDone(found("https://dav.example.net", .wellKnown), error: nil)
        e.listDone(AccountListCalDAVCalendarsResult(principalUrl: "p", homeSetUrl: "h", scheduling: .server), error: nil)
        #expect(!e.canAdd)
    }

    @Test func editsAndErrors() {
        var w = W(mailAccount: nil)
        w.edit(name: "Home", server: "cloud.example.org", username: "jana", password: "pw")
        w.beginDiscover()
        w.discoverDone(nil, error: err(.networkError))
        #expect(w.phase == .entering && w.errorText == "The server could not be reached." && w.canDiscover)
        w.beginDiscover()
        w.discoverDone(found("https://cloud.example.org/dav", .srv), error: nil)
        w.listDone(nil, error: err(.serverError))
        #expect(w.phase == .entering && w.listed == nil && w.errorText == "The server answered, but it is not a CalDAV server.")
        w.beginDiscover()
        w.discoverDone(found("https://cloud.example.org/dav", .srv), error: nil)
        var l = listed()
        l.scheduling = .server
        w.listDone(l, error: nil)
        #expect(w.name == "Home" && !w.showsMailAccount && w.foundLine == "Found through a DNS SRV record · 3 calendars")
        w.edit(name: "Home 2", server: "cloud.example.org", username: "jana", password: "pw")
        #expect(w.phase == .listed && w.listed != nil)
        w.edit(name: "Home 2", server: "cloud.example.org", username: "jana2", password: "pw")
        #expect(w.phase == .entering && w.listed == nil && w.discovered == nil && w.selected.isEmpty)
        #expect(w.addParams.config.caldav?.mailAccountId == nil)
    }

    @Test func trust() throws {
        var w = W(mailAccount: nil)
        w.edit(name: "", server: "cloud.example.org", username: "jana", password: "pw")
        w.beginDiscover()
        w.discoverDone(nil, error: try tlsErr(.untrusted, testSHA))
        let p = try #require(w.trustOffer)
        #expect(p.cert?.sha256 == testSHA && w.errorText == "The server’s certificate is not trusted.")
        #expect(w.trust() && w.pin == testSHA && w.error == nil && w.phase == .discovering && w.step == .discover)
        #expect(w.certificateText == "Trusted by you")
        #expect(w.trustOffer == nil)
        // Discovery runs again with the pin, which goes on into the listing
        // and the account.
        #expect(w.discoverParams.certificateSha256 == testSHA && w.discoverParams.url == "cloud.example.org")
        w.discoverDone(found("https://cloud.example.org/remote.php/dav", .wellKnown), error: nil)
        #expect(w.listParams.config.caldav?.certificateSha256 == testSHA)
        #expect(w.listParams.config.caldav?.serverUrl == "https://cloud.example.org/remote.php/dav")
        w.listDone(listed(), error: nil)
        w.beginAdd()
        #expect(w.addParams.config.caldav?.certificateSha256 == testSHA)
        w.addDone(nil, error: err(.serverError))
        // A redirect to another host does not take the pin along.
        w.edit(name: w.name, server: w.server, username: w.username, password: "pw2")
        w.beginDiscover()
        w.discoverDone(found("https://elsewhere.example.net/dav", .wellKnown), error: nil)
        #expect(w.listParams.config.caldav?.certificateSha256 == nil)
        w.listDone(nil, error: try tlsErr(.untrusted, testSHA))
        // The same certificate refused again offers nothing.
        w.listDone(nil, error: try tlsErr(.untrusted, testSHA))
        #expect(w.trustOffer == nil)
        // Another host forgets the pin; the same host keeps it.
        w.edit(name: "", server: "https://cloud.example.org/remote.php/dav", username: "jana", password: "pw")
        #expect(w.pin == testSHA)
        w.edit(name: "", server: "evil.example.net", username: "jana", password: "pw")
        #expect(w.pin == "")
        let offers: [(String, RPCError, String, Bool)] = [
            ("handshake", try tlsErr(.handshake, testSHA), "cloud.example.org", false),
            ("no certificate", try tlsErr(.untrusted, ""), "cloud.example.org", false),
            ("bad fingerprint", try tlsErr(.untrusted, "xyz"), "cloud.example.org", false),
            ("pin mismatch", try tlsErr(.pinMismatch, testSHA), "cloud.example.org", true),
            ("plain http", try tlsErr(.untrusted, testSHA), "http://127.0.0.1:5232", false),
            ("another error", err(.networkError), "cloud.example.org", false),
        ]
        for (name, e, url, ok) in offers {
            var w = W(mailAccount: nil)
            w.edit(name: "", server: url, username: "u", password: "p")
            w.beginDiscover()
            w.discoverDone(nil, error: e)
            #expect((w.trustOffer != nil) == ok, "\(name)")
        }
    }

    @Test func hosts() {
        #expect(W.host("cloud.example.org") == "cloud.example.org")
        #expect(W.host(" https://Cloud.example.org:8443/dav ") == "Cloud.example.org")
        #expect(W.host("http://[::1]:5232/") == "::1")
        #expect(W.host("") == "" && W.host("a b") == "")
        #expect(W.typedURL("cloud.example.org/dav//") == "https://cloud.example.org/dav")
    }
}
