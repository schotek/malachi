// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The calendar's wire types (docs/api.md §4.14–§4.16, §5,
/// backend/pkg/api/calendar.go) with invented data: documents with optional
/// fields absent and present, params encoding, the open enums, DateOrTime,
/// the error data and the notifications.
@Suite struct APICodingCalendarTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    /// Encodes and decodes again: the value must survive.
    private func roundTrip<T: Codable & Equatable>(_ v: T) throws -> T {
        try JSONCoding.decoder().decode(T.self, from: JSONCoding.encoder().encode(v))
    }

    private func at(_ s: String) -> Date { RFC3339.parse(s)! }

    // MARK: Scalars and enums

    @Test func dateOrTimeDecodesEitherForm() throws {
        #expect(try decode([DateOrTime].self, #"["2026-10-14T08:00:00Z","2026-10-14"]"#)
            == [.time(at("2026-10-14T08:00:00Z")), .date("2026-10-14")])
        #expect(try decode([DateOrTime].self, #"["2026-10-14T10:00:00+02:00"]"#) == [.time(at("2026-10-14T08:00:00Z"))])
        for bad in [#""tomorrow""#, #""2026-13-01""#, #""2026-02-30""#, #""2026-10-14T08:00:00""#, "5", "null"] {
            #expect(throws: DecodingError.self) { try decode(DateOrTime.self, bad) }
        }
        let wire = try JSONCoding.encoder().encode(["a": DateOrTime.time(at("2026-10-14T08:00:00Z")), "b": .date("2026-10-14")])
        let back = try #require(JSONSerialization.jsonObject(with: wire) as? [String: String])
        #expect(back == ["a": "2026-10-14T08:00:00Z", "b": "2026-10-14"])
        #expect(DateOrTime.date("2026-10-14").wire == "2026-10-14")
    }

    @Test func calendarDayReadsItsParts() {
        let d = CalendarDay("2028-02-29").parts
        #expect(d?.year == 2028 && d?.month == 2 && d?.day == 29)
        for bad in ["", "2026-10-1", "2026-02-29", "2026-00-10", "2026-10-00", "20261014", "2026-10-14T00:00:00", "٢٠٢٦-10-14"] {
            #expect(CalendarDay(bad).parts == nil, "\(bad)")
        }
    }

    @Test func scalarsAndIDsAreBareStrings() throws {
        #expect(try decode(CalendarID.self, #""cal_1""#) == "cal_1")
        #expect(try decode(EventID.self, #""ev_1""#).rawValue == "ev_1")
        #expect(try decode(AlertID.self, #""al_1""#).description == "al_1")
        #expect(try decode(LocalDateTime.self, #""2026-10-14T10:00:00""#).rawValue == "2026-10-14T10:00:00")
        #expect(try decode(TimeZoneID.self, #""Europe/Prague""#) == "Europe/Prague")
        let wire = try JSONCoding.encoder().encode(["x": CalendarDay("2026-10-14")])
        #expect(String(decoding: wire, as: UTF8.self) == #"{"x":"2026-10-14"}"#)
    }

    @Test func enumsKeepUnknownValues() throws {
        let values = try decode([CalendarSource].self, #"["graph","caldav","google","jmap"]"#)
        #expect(values == [.graph, .caldav, .google, "jmap"])
        #expect(try decode(CalendarKind.self, #""resources""#).rawValue == "resources")
        #expect(try decode(ShowAs.self, #""tomorrow""#) != .unknown)
        #expect(try decode(CalendarSyncStatus.self, #""consentRequired""#) == .consentRequired)
        let known: [(String, String)] = [
            (CalendarAccess.owner.rawValue, "owner"), (CalendarAccess.write.rawValue, "write"), (CalendarAccess.read.rawValue, "read"),
            (CalendarKind.events.rawValue, "events"), (CalendarKind.birthdays.rawValue, "birthdays"),
            (CalendarKind.holidays.rawValue, "holidays"), (CalendarKind.other.rawValue, "other"),
            (EventResponse.needsAction.rawValue, "needsAction"), (EventResponse.accepted.rawValue, "accepted"),
            (EventResponse.tentative.rawValue, "tentative"), (EventResponse.declined.rawValue, "declined"),
            (EventResponse.delegated.rawValue, "delegated"),
            (ShowAs.free.rawValue, "free"), (ShowAs.tentative.rawValue, "tentative"), (ShowAs.busy.rawValue, "busy"),
            (ShowAs.oof.rawValue, "oof"), (ShowAs.workingElsewhere.rawValue, "workingElsewhere"),
            (ShowAs.unknown.rawValue, "unknown"),
            (EventStatus.confirmed.rawValue, "confirmed"), (EventStatus.tentative.rawValue, "tentative"),
            (EventStatus.cancelled.rawValue, "cancelled"),
            (EditScope.this.rawValue, "this"), (EditScope.thisAndFuture.rawValue, "thisAndFuture"), (EditScope.all.rawValue, "all"),
            (InvitationMethod.request.rawValue, "request"), (InvitationMethod.reply.rawValue, "reply"),
            (InvitationMethod.cancel.rawValue, "cancel"), (InvitationMethod.counter.rawValue, "counter"),
            (InvitationMethod.declineCounter.rawValue, "declineCounter"), (InvitationMethod.refresh.rawValue, "refresh"),
            (InvitationMethod.add.rawValue, "add"), (InvitationMethod.publish.rawValue, "publish"),
            (InvitationMethod.other.rawValue, "other"),
            (InvitationKind.invitation.rawValue, "invitation"), (InvitationKind.update.rawValue, "update"),
            (InvitationKind.cancelled.rawValue, "cancelled"), (InvitationKind.reply.rawValue, "reply"),
            (InvitationKind.other.rawValue, "other"),
            (InvitationState.unanswered.rawValue, "unanswered"), (InvitationState.answered.rawValue, "answered"),
            (InvitationState.outdated.rawValue, "outdated"), (InvitationState.cancelled.rawValue, "cancelled"),
            (InvitationState.processed.rawValue, "processed"), (InvitationState.invalid.rawValue, "invalid"),
            (CalendarSyncStatus.idle.rawValue, "idle"), (CalendarSyncStatus.syncing.rawValue, "syncing"),
            (CalendarSyncStatus.offline.rawValue, "offline"), (CalendarSyncStatus.authRequired.rawValue, "authRequired"),
            (CalendarSyncStatus.consentRequired.rawValue, "consentRequired"), (CalendarSyncStatus.error.rawValue, "error"),
            (CalendarSyncStatus.disabled.rawValue, "disabled"),
            (InvitationsVia.server.rawValue, "server"), (InvitationsVia.mail.rawValue, "mail"), (InvitationsVia.none.rawValue, "none"),
            (VideoProvider.teams.rawValue, "teams"), (VideoProvider.zoom.rawValue, "zoom"), (VideoProvider.meet.rawValue, "meet"),
            (VideoProvider.webex.rawValue, "webex"), (VideoProvider.jitsi.rawValue, "jitsi"),
            (VideoProvider.whereby.rawValue, "whereby"), (VideoProvider.other.rawValue, "other"),
            (CalDAVScheduling.auto.rawValue, "auto"), (CalDAVScheduling.server.rawValue, "server"),
            (CalDAVScheduling.client.rawValue, "client"),
            (AfterAnswer.keep.rawValue, "keep"), (AfterAnswer.archive.rawValue, "archive"),
            (WeekStart.locale.rawValue, "locale"), (WeekStart.monday.rawValue, "monday"),
            (WeekStart.sunday.rawValue, "sunday"), (WeekStart.saturday.rawValue, "saturday"),
            (RecurrenceFrequency.daily.rawValue, "daily"), (RecurrenceFrequency.weekly.rawValue, "weekly"),
            (RecurrenceFrequency.monthly.rawValue, "monthly"), (RecurrenceFrequency.yearly.rawValue, "yearly"),
            (AttendeeRole.required.rawValue, "required"), (AttendeeRole.optional.rawValue, "optional"),
            (AttendeeRole.chair.rawValue, "chair"), (AttendeeRole.nonParticipant.rawValue, "nonParticipant"),
            (AttendeeType.person.rawValue, "person"), (AttendeeType.room.rawValue, "room"),
            (AttendeeType.resource.rawValue, "resource"), (AttendeeType.group.rawValue, "group"),
            (AttendeeType.unknown.rawValue, "unknown"),
            (EventAlertState.due.rawValue, "due"), (EventAlertState.dismissed.rawValue, "dismissed"),
            (VideoCallMode.none.rawValue, "none"), (VideoCallMode.create.rawValue, "create"), (VideoCallMode.link.rawValue, "link"),
            (CalDAVFoundVia.wellKnown.rawValue, "wellKnown"), (CalDAVFoundVia.srv.rawValue, "srv"),
            (CalDAVFoundVia.url.rawValue, "url"),
            (AccountKind.caldav.rawValue, "caldav"), (Capability.calendar.rawValue, "calendar"),
        ]
        for (got, want) in known { #expect(got == want) }
    }

    // MARK: Accounts

    @Test func caldavAccountConfig() throws {
        let c = try decode(AccountConfig.self, #"""
        {"name":"Work","email":"me@example.org","kind":"caldav",
         "caldav":{"serverUrl":"https://cal.example.org/dav/","principalUrl":"/dav/p/me/","homeSetUrl":"/dav/c/me/",
                   "username":"me","certificateSha256":"ab","mailAccountId":"acc_mail","scheduling":"client"},
         "calendar":{"sync":true,"disabledCalendars":["/dav/c/me/old/"],"defaultCalendar":"/dav/c/me/main/","keepDays":90,
                     "showUnanswered":false,"invitationCard":true,"afterAnswer":"archive"}}
        """#)
        #expect(c.protocolKind == .caldav)
        #expect(c.caldav == CalDAVConfig(serverUrl: "https://cal.example.org/dav/", principalUrl: "/dav/p/me/",
                                         homeSetUrl: "/dav/c/me/", username: "me", certificateSha256: "ab",
                                         mailAccountId: "acc_mail", scheduling: .client))
        #expect(c.calendar?.sync == true && c.calendar?.disabledCalendars == ["/dav/c/me/old/"])
        #expect(c.calendar?.defaultCalendar == "/dav/c/me/main/" && c.calendar?.keepDays == 90)
        #expect(c.calendar?.showsUnanswered == false && c.calendar?.showsInvitationCard == true)
        #expect(c.calendar?.afterAnswer == .archive)
        #expect(try roundTrip(c) == c)

        let minimal = try decode(AccountConfig.self, #"""
        {"name":"Cal","email":"me@example.org","kind":"caldav","caldav":{"serverUrl":"https://x","username":"me"},"calendar":{"sync":true}}
        """#)
        #expect(minimal.caldav?.principalUrl == nil && minimal.caldav?.scheduling == nil && minimal.caldav?.mailAccountId == nil)
        #expect(minimal.calendar?.showsUnanswered == true && minimal.calendar?.showsInvitationCard == true)
        #expect(minimal.calendar?.keepDays == nil && minimal.calendar?.afterAnswer == nil)
        let wire = try encodeObject(minimal)
        #expect((wire["caldav"] as? [String: Any])?.keys.sorted() == ["serverUrl", "username"])
        #expect((wire["calendar"] as? [String: Any])?.keys.sorted() == ["sync"])
        let mail = try encodeObject(AccountConfig(name: "a", email: "b"))
        #expect(mail["caldav"] == nil && mail["calendar"] == nil)
    }

    @Test func accountCalendarExtras() throws {
        let t = try decode(AccountTestResult.self, #"{"caldav":{"ok":true,"capabilities":["calendar-access","sync-collection"],"latencyMs":80}}"#)
        #expect(t.caldav?.capabilities == ["calendar-access", "sync-collection"] && t.imap == nil && t.graph == nil)
        let account = try decode(Account.self, #"""
        {"id":"a1","config":{"name":"C","email":"c@x","kind":"caldav"},"enabled":true,
         "state":{"accountId":"a1","status":"idle","progress":-1,"pendingOutbox":0},"capabilities":["calendar"]}
        """#)
        #expect(account.can(.calendar) && !account.can(.reply))
        let start = try encodeObject(AccountOAuthStartParams(accountId: "a1", calendar: true))
        #expect(start["calendar"] as? Bool == true)
        #expect(try encodeObject(AccountOAuthStartParams(accountId: "a1"))["calendar"] == nil)
    }

    @Test func discoverAndListCalDAV() throws {
        let found = try decode(AccountDiscoverCalDAVResult.self, #"""
        {"serverUrl":"https://cal.example.org/dav/","foundVia":"wellKnown","needsCredentials":true,
         "certificate":{"sha256":"aa","notBefore":"2026-01-01T00:00:00Z","notAfter":"2027-01-01T00:00:00Z","selfSigned":false}}
        """#)
        #expect(found.foundVia == .wellKnown && found.needsCredentials && found.certificate?.sha256 == "aa")
        let bare = try decode(AccountDiscoverCalDAVResult.self, #"{"serverUrl":"https://x","foundVia":"srv","needsCredentials":false}"#)
        #expect(bare.certificate == nil && bare.foundVia == .srv)
        #expect(try encodeObject(AccountDiscoverCalDAVParams(url: "example.org")) as NSDictionary == ["url": "example.org"])
        let pinned = AccountDiscoverCalDAVParams(url: "example.org", certificateSha256: "ab")
        #expect(try encodeObject(pinned) as NSDictionary == ["url": "example.org", "certificateSha256": "ab"])
        #expect(try roundTrip(pinned) == pinned)
        #expect(try decode(AccountDiscoverCalDAVParams.self, #"{"url":"x"}"#).certificateSha256 == nil)

        let list = try decode(AccountListCalDAVCalendarsResult.self, #"""
        {"principalUrl":"/p/","homeSetUrl":"/h/","displayName":"Me","addresses":["mailto:me@example.org"],"scheduling":"server",
         "calendars":[{"href":"/h/a/","name":"Main","color":"#1f6feb","access":"owner","isDefault":true},
                      {"href":"/h/b/","name":"Shared","access":"read"}]}
        """#)
        #expect(list.displayName == "Me" && list.scheduling == .server && list.calendars.count == 2)
        #expect(list.calendars[0].isDefault && list.calendars[0].color == "#1f6feb")
        #expect(!list.calendars[1].isDefault && list.calendars[1].color == nil && list.calendars[1].access == .read)
        let nulls = try decode(AccountListCalDAVCalendarsResult.self,
                               #"{"principalUrl":"/p/","homeSetUrl":"/h/","addresses":null,"scheduling":"client","calendars":null}"#)
        #expect(nulls.addresses.isEmpty && nulls.calendars.isEmpty && nulls.displayName == nil)
        let params = try encodeObject(AccountListCalDAVCalendarsParams(
            config: AccountConfig(name: "C", email: "c@x", kind: .caldav), credentials: Credentials(password: "pw")))
        #expect(params["accountId"] == nil && (params["credentials"] as? [String: Any])?["password"] as? String == "pw")
    }

    // MARK: Calendars

    private let calendarList = #"""
    {"accounts":[{"accountId":"acc_1","source":"graph","sync":true,
      "state":{"accountId":"acc_1","status":"syncing","progress":40,"lastSync":"2026-10-14T07:00:00Z",
               "error":{"code":1204,"message":"consent","data":{"accountId":"acc_1","signIn":true}}},
      "features":{"freeBusy":true,"proposeNewTime":true,"invitations":"server","onlineMeetings":true},
      "window":{"from":"2025-10-14","to":"2027-11-18"},
      "calendars":[{"id":"cal_1","accountId":"acc_1","remoteId":"AAMk1","name":"Calendar","color":"#1f6feb","serverColor":"#0000ff","userColor":"#1f6feb",
                    "visible":true,"access":"owner","kind":"events","isDefault":true,"timeZone":"Europe/Prague",
                    "onlineMeetingProviders":["teamsForBusiness"],"disabled":true,"owner":{"name":"Ann","address":"ann@example.org"}},
                   {"id":"cal_2","accountId":"acc_1","remoteId":"AAMk2","name":"Holidays","color":"#aaaaaa","visible":false,"access":"read","kind":"holidays"}]},
     {"accountId":"acc_2","source":"caldav","sync":true,
      "state":{"accountId":"acc_2","status":"idle","progress":-1},
      "features":{"freeBusy":false,"proposeNewTime":false,"invitations":"mail","mailAccountId":"acc_m","onlineMeetings":false},
      "window":{"from":"2025-10-14","to":"2027-11-18"},"calendars":null}],
     "unanswered":3}
    """#

    @Test func calendarListDecodes() throws {
        let r = try decode(CalendarListResult.self, calendarList)
        #expect(r.unanswered == 3 && r.accounts.count == 2)
        let g = r.accounts[0]
        #expect(g.source == .graph && g.sync && g.state.status == .syncing && g.state.progress == 40)
        #expect(g.state.lastSync == at("2026-10-14T07:00:00Z"))
        #expect(g.state.error?.code == .calendarConsentRequired)
        #expect(g.state.error?.calendarConsentRequired == CalendarConsentRequiredData(accountId: "acc_1", signIn: true))
        #expect(g.features.freeBusy && g.features.invitations == .server && g.features.mailAccountId == nil)
        #expect(g.window.from == "2025-10-14" && g.window.to.parts?.year == 2027)
        let full = g.calendars[0]
        #expect(full.remoteId == "AAMk1" && full.disabled)
        #expect(full.id == "cal_1" && full.serverColor == "#0000ff" && full.userColor == "#1f6feb" && full.isDefault)
        #expect(full.timeZone == "Europe/Prague" && full.onlineMeetingProviders == ["teamsForBusiness"])
        #expect(full.owner == Address(name: "Ann", address: "ann@example.org"))
        let bare = g.calendars[1]
        #expect(!bare.visible && bare.kind == .holidays && bare.access == .read)
        #expect(!bare.isDefault && bare.serverColor == nil && bare.userColor == nil && bare.owner == nil && bare.timeZone == nil)
        #expect(bare.onlineMeetingProviders == nil && bare.remoteId == "AAMk2" && !bare.disabled)
        let c = r.accounts[1]
        #expect(c.source == .caldav && c.calendars.isEmpty && c.state.lastSync == nil && c.state.error == nil)
        #expect(c.features.invitations == .mail && c.features.mailAccountId == "acc_m")
        #expect(try roundTrip(r) == r)
    }

    @Test func calendarParamsEncode() throws {
        #expect(try encodeObject(CalendarListParams()).isEmpty)
        #expect(try encodeObject(CalendarListParams(accountId: "a")) as NSDictionary == ["accountId": "a"])
        let upd = try encodeObject(CalendarUpdateParams(calendarId: "cal_1", visible: false, userColor: ""))
        #expect(upd["visible"] as? Bool == false && upd["userColor"] as? String == "" && upd["calendarId"] as? String == "cal_1")
        let onlyID = try encodeObject(CalendarUpdateParams(calendarId: "cal_1"))
        #expect(onlyID.keys.sorted() == ["calendarId"])
        #expect(try encodeObject(CalendarSyncParams()).isEmpty)
        #expect(try encodeObject(CalendarSyncParams(accountId: "a", calendarId: "c")).keys.sorted() == ["accountId", "calendarId"])
        #expect(try encodeObject(CalendarPreferencesParams()).isEmpty)
        let fb = try encodeObject(CalendarFreeBusyParams(accountId: "a", attendees: ["x@y"], from: at("2026-10-14T00:00:00Z"),
                                                         to: at("2026-10-15T00:00:00Z")))
        #expect(fb["from"] as? String == "2026-10-14T00:00:00Z" && fb["intervalMinutes"] == nil)
        let ff = try encodeObject(CalendarFindFreeTimeParams(durationMinutes: 30, from: at("2026-10-14T00:00:00Z"),
                                                             to: at("2026-10-20T00:00:00Z")))
        #expect(ff.keys.sorted() == ["durationMinutes", "from", "to"])
        let ffAll = try encodeObject(CalendarFindFreeTimeParams(accountId: "a", attendees: ["x@y"], durationMinutes: 30,
                                                                from: at("2026-10-14T00:00:00Z"), to: at("2026-10-20T00:00:00Z"),
                                                                workingHoursOnly: false, limit: 5))
        #expect(ffAll["workingHoursOnly"] as? Bool == false && ffAll["limit"] as? Int == 5)
    }

    @Test func preferences() throws {
        let p = try decode(CalendarPreferencesResult.self, #"""
        {"preferences":{"weekStart":"locale","defaultDurationMinutes":60,"defaultAlertMinutes":-1,"showDeclined":false,
                        "workingHours":{"days":["MO","TU","WE","TH","FR"],"start":"08:00","end":"17:00"}}}
        """#)
        #expect(p.preferences.weekStart == .locale && p.preferences.defaultAlertMinutes == -1 && !p.preferences.showDeclined)
        #expect(p.preferences.workingHours == WorkingHours(days: ["MO", "TU", "WE", "TH", "FR"], start: "08:00", end: "17:00"))
        #expect(try roundTrip(p) == p)
        #expect(try decode(CalendarSetPreferencesResult.self,
                           #"{"preferences":{"weekStart":"monday","defaultDurationMinutes":30,"defaultAlertMinutes":0,"showDeclined":true,"workingHours":{"days":["SA"],"start":"09:00","end":"12:00"}}}"#)
            .preferences.weekStart == .monday)
        // A partial update: only what is set is sent.
        let none = try encodeObject(CalendarSetPreferencesParams(preferences: CalendarPreferencesUpdate()))
        #expect((none["preferences"] as? [String: Any])?.isEmpty == true)
        let some = try encodeObject(CalendarSetPreferencesParams(preferences: CalendarPreferencesUpdate(
            weekStart: .sunday, defaultAlertMinutes: -1, showDeclined: false)))
        let inner = try #require(some["preferences"] as? [String: Any])
        #expect(inner.keys.sorted() == ["defaultAlertMinutes", "showDeclined", "weekStart"])
        #expect(inner["defaultAlertMinutes"] as? Int == -1 && inner["showDeclined"] as? Bool == false)
        let hours = try encodeObject(CalendarPreferencesUpdate(workingHours: WorkingHours(days: ["MO"], start: "09:00", end: "10:00")))
        #expect((hours["workingHours"] as? [String: Any])?["days"] as? [String] == ["MO"])
    }

    @Test func freeBusyAndFreeTime() throws {
        let r = try decode(CalendarFreeBusyResult.self, #"""
        {"me":{"address":"me@x","known":true,"busy":[{"start":"2026-10-14T08:00:00Z","end":"2026-10-14T09:00:00Z","showAs":"busy"}]},
         "attendees":[{"address":"a@x","known":false,"busy":null},{"address":"b@x","known":true,"busy":[]}]}
        """#)
        #expect(r.me.busy == [BusyInterval(start: at("2026-10-14T08:00:00Z"), end: at("2026-10-14T09:00:00Z"), showAs: .busy)])
        #expect(!r.attendees[0].known && r.attendees[0].busy.isEmpty && r.attendees[1].known)
        let f = try decode(CalendarFindFreeTimeResult.self,
                           #"{"slots":[{"start":"2026-10-14T08:00:00Z","end":"2026-10-14T09:00:00Z"}],"unknown":["c@x"]}"#)
        #expect(f.slots == [TimeSlot(start: at("2026-10-14T08:00:00Z"), end: at("2026-10-14T09:00:00Z"))] && f.unknown == ["c@x"])
        let empty = try decode(CalendarFindFreeTimeResult.self, #"{"slots":null,"unknown":null}"#)
        #expect(empty.slots.isEmpty && empty.unknown.isEmpty)
    }

    // MARK: Events

    private let minimalOccurrence = #"""
    {"accountId":"acc_1","title":"Standup","start":"2026-10-14T07:00:00Z","end":"2026-10-14T07:15:00Z","allDay":false,
     "status":"confirmed","showAs":"busy"}
    """#
    private let fullOccurrence = #"""
    {"eventId":"ev_1","recurrenceId":"2026-10-14T07:00:00Z","seriesId":"ev_1","calendarId":"cal_1","accountId":"acc_1",
     "title":"Standup","start":"2026-10-14T07:00:00Z","end":"2026-10-14T07:15:00Z","allDay":false,"floating":true,
     "timeZone":"Europe/Prague","location":"Room 1","status":"tentative","showAs":"tentative","myResponse":"accepted",
     "organizer":true,"attendeeCount":4,"recurring":true,"videoCall":true,"private":true,"alert":true,"readOnly":true,
     "invitation":{"accountId":"acc_1","messageId":"m_1"}}
    """#

    @Test func occurrenceMinimalAndFull() throws {
        let m = try decode(Occurrence.self, minimalOccurrence)
        #expect(m.eventId == nil && m.recurrenceId == nil && m.seriesId == nil && m.calendarId == nil)
        #expect(m.start == at("2026-10-14T07:00:00Z") && m.end == at("2026-10-14T07:15:00Z") && m.startDate == nil && m.endDate == nil)
        #expect(!m.allDay && !m.floating && m.timeZone == nil && m.location == nil && m.myResponse == nil)
        #expect(!m.organizer && m.attendeeCount == 0 && !m.recurring && !m.videoCall && !m.private && !m.alert && !m.readOnly)
        #expect(m.invitation == nil && m.status == .confirmed && m.showAs == .busy)
        #expect(try roundTrip(m) == m)
        // Defaults are not written for the omitted keys' types, and read back the same.
        let f = try decode(Occurrence.self, fullOccurrence)
        #expect(f.eventId == "ev_1" && f.recurrenceId == .time(at("2026-10-14T07:00:00Z")) && f.seriesId == "ev_1")
        #expect(f.calendarId == "cal_1" && f.floating && f.timeZone == "Europe/Prague" && f.location == "Room 1")
        #expect(f.status == .tentative && f.myResponse == .accepted && f.organizer && f.attendeeCount == 4)
        #expect(f.recurring && f.videoCall && f.private && f.alert && f.readOnly)
        #expect(f.invitation == MessageRef(accountId: "acc_1", messageId: "m_1"))
        #expect(try roundTrip(f) == f)
        let allDay = try decode(Occurrence.self, #"""
        {"eventId":"ev_2","recurrenceId":"2026-10-15","accountId":"a","title":"Holiday","startDate":"2026-10-15","endDate":"2026-10-16",
         "allDay":true,"status":"confirmed","showAs":"free"}
        """#)
        #expect(allDay.allDay && allDay.start == nil && allDay.startDate == "2026-10-15" && allDay.recurrenceId == .date("2026-10-15"))
    }

    @Test func eventMinimalFlattensTheOccurrence() throws {
        let minimal = String(minimalOccurrence.dropLast())
            + #","etag":"e1","canEdit":false,"canDelete":false,"canRespond":false,"canProposeNewTime":false,"removeIsDelete":false}"#
        let e = try decode(Event.self, minimal)
        #expect(e.etag == "e1" && e.title == "Standup" && e.occurrence.accountId == "acc_1")
        #expect(e.notes == nil && e.links == nil && e.videoCallLink == nil && e.organizerAddress == nil && e.attendees == nil)
        #expect(!e.attendeesTruncated && e.recurrence == nil && e.alerts == nil && !e.responseRequested)
        #expect(e.invitationMessages == nil && e.created == nil && e.updated == nil)
        #expect(!e.canEdit && !e.removeIsDelete)
        #expect(try roundTrip(e) == e)
        let wire = try encodeObject(e)
        #expect(wire["title"] as? String == "Standup" && wire["etag"] as? String == "e1" && wire["occurrence"] == nil)
        #expect(wire["notes"] == nil && wire["attendeesTruncated"] == nil && wire["responseRequested"] == nil)
        var changed = e
        changed.title = "Other"
        #expect(changed.occurrence.title == "Other")
    }

    @Test func eventFull() throws {
        let full = String(fullOccurrence.dropLast()) + #"""
        ,"etag":"W/\"7\"","notes":"Agenda","links":["https://example.org/a"],
         "videoCallLink":{"url":"https://teams.microsoft.com/l/x","provider":"teams","host":"teams.microsoft.com"},
         "organizerAddress":{"name":"Ann","address":"ann@example.org"},
         "attendees":[{"name":"Ann","address":"ann@example.org","role":"chair","response":"accepted","type":"person","organizer":true},
                      {"address":"room@example.org","role":"optional","response":"needsAction","type":"room","me":true}],
         "attendeesTruncated":true,
         "recurrence":{"frequency":"weekly","interval":2,"byDay":["MO","WE"],"until":"2027-01-01","weekStart":"MO","complex":true,"exceptions":3},
         "alerts":[15,0],"responseRequested":true,"canEdit":true,"canDelete":true,"canRespond":true,"canProposeNewTime":true,
         "removeIsDelete":false,"invitationMessages":[{"accountId":"acc_1","messageId":"m_9"}],
         "created":"2026-09-01T10:00:00Z","updated":"2026-10-01T10:00:00.25Z"}
        """#
        let e = try decode(Event.self, full)
        #expect(e.etag == #"W/"7""# && e.notes == "Agenda" && e.links == ["https://example.org/a"])
        #expect(e.videoCallLink == VideoCall(url: "https://teams.microsoft.com/l/x", provider: .teams, host: "teams.microsoft.com"))
        #expect(e.organizerAddress == Address(name: "Ann", address: "ann@example.org"))
        let a = try #require(e.attendees)
        #expect(a.count == 2 && a[0].role == .chair && a[0].organizer && !a[0].me && a[0].type == .person)
        #expect(a[1].name == nil && a[1].me && !a[1].organizer && a[1].type == .room && a[1].response == .needsAction)
        #expect(e.attendeesTruncated && e.responseRequested && e.alerts == [15, 0] && e.canProposeNewTime && !e.removeIsDelete)
        let r = try #require(e.recurrence)
        #expect(r.frequency == .weekly && r.interval == 2 && r.byDay == ["MO", "WE"] && r.until == "2027-01-01")
        #expect(r.count == 0 && r.complex && r.exceptions == 3 && r.weekStart == "MO" && r.byMonthDay == nil && r.byMonth == nil)
        #expect(e.invitationMessages == [MessageRef(accountId: "acc_1", messageId: "m_9")])
        #expect(e.created == at("2026-09-01T10:00:00Z") && e.updated == at("2026-10-01T10:00:00.25Z"))
        #expect(try roundTrip(e) == e)
        #expect(e.private && e.alert && e.recurring, "occurrence fields read through")
    }

    @Test func recurrenceShapes() throws {
        let monthly = try decode(Recurrence.self, #"{"frequency":"monthly","interval":1,"byDay":["-1FR"],"byMonthDay":[-1,15],"byMonth":[3],"count":10}"#)
        #expect(monthly.byDay == ["-1FR"] && monthly.byMonthDay == [-1, 15] && monthly.byMonth == [3])
        #expect(monthly.count == 10 && monthly.until == nil && !monthly.complex && monthly.exceptions == 0)
        let wire = try encodeObject(Recurrence(frequency: .daily, interval: 1))
        #expect(wire["frequency"] as? String == "daily" && wire["interval"] as? Int == 1)
        #expect(try decode(Recurrence.self, #"{"frequency":"hourly","interval":1}"#).frequency.rawValue == "hourly")
    }

    @Test func eventInputEncoding() throws {
        let timed = try encodeObject(EventInput(title: "T", allDay: false, start: "2026-10-14T10:00:00", end: "2026-10-14T11:00:00",
                                                timeZone: "Europe/Prague"))
        #expect(timed.keys.sorted() == ["allDay", "end", "start", "timeZone", "title"])
        #expect(timed["start"] as? String == "2026-10-14T10:00:00")
        let allDay = try encodeObject(EventInput(title: "", allDay: true, startDate: "2026-10-14", endDate: "2026-10-15"))
        #expect(allDay["startDate"] as? String == "2026-10-14" && allDay["start"] == nil && allDay["title"] as? String == "")
        // nil alerts = the default alert (key left out); empty = none (kept).
        #expect(allDay["alerts"] == nil)
        let none = try encodeObject(EventInput(title: "T", allDay: true, startDate: "2026-10-14", endDate: "2026-10-15", alerts: []))
        #expect((none["alerts"] as? [Any])?.isEmpty == true)
        let full = try encodeObject(EventInput(
            title: "T", allDay: false, start: "2026-10-14T10:00:00", end: "2026-10-14T11:00:00", timeZone: "Europe/Prague",
            location: "L", notes: "N",
            recurrence: Recurrence(frequency: .weekly, interval: 1, byDay: ["MO"], count: 5), keepRecurrence: true,
            alerts: [10, 60], showAs: .oof, private: true,
            attendees: [EventInputAttendee(name: "A", address: "a@x", role: .optional), EventInputAttendee(address: "b@x")],
            videoCall: VideoCallInput(mode: .create, provider: "teamsForBusiness"), attachments: ["att_1"]))
        #expect(full["keepRecurrence"] as? Bool == true && full["private"] as? Bool == true && full["showAs"] as? String == "oof")
        #expect(full["alerts"] as? [Int] == [10, 60] && full["attachments"] as? [String] == ["att_1"])
        let attendees = try #require(full["attendees"] as? [[String: Any]])
        #expect(attendees[0]["role"] as? String == "optional" && attendees[1].keys.sorted() == ["address"])
        #expect((full["videoCall"] as? [String: Any])?["mode"] as? String == "create")
        let rec = try #require(full["recurrence"] as? [String: Any])
        #expect(rec["count"] as? Int == 5 && rec["complex"] == nil && rec["exceptions"] == nil, "omitempty values are left out")
        #expect(try roundTrip(EventInput(title: "T", allDay: true, startDate: "2026-10-14", endDate: "2026-10-15", alerts: [])).alerts == [])
        let link = try encodeObject(VideoCallInput(mode: .link, url: "https://meet.example.org/x"))
        #expect(link["url"] as? String == "https://meet.example.org/x" && link["provider"] == nil)
    }

    @Test func eventListAndSearch() throws {
        let list = try encodeObject(EventListParams(from: at("2026-10-12T00:00:00Z"), to: at("2026-10-19T00:00:00Z"), timeZone: "Europe/Prague"))
        #expect(list.keys.sorted() == ["from", "timeZone", "to"])
        let all = try encodeObject(EventListParams(
            from: at("2026-10-12T00:00:00Z"), to: at("2026-10-19T00:00:00Z"), timeZone: "Europe/Prague",
            calendarIds: ["cal_1"], accountIds: ["acc_1"], includeHidden: true, includeDeclined: false, includeInvitations: false, limit: 10))
        #expect(all["includeHidden"] as? Bool == true && all["includeDeclined"] as? Bool == false && all["limit"] as? Int == 10)
        #expect(all["calendarIds"] as? [String] == ["cal_1"] && all["accountIds"] as? [String] == ["acc_1"])

        let r = try decode(EventListResult.self, #"{"occurrences":[\#(minimalOccurrence)],"truncated":false,"complete":true}"#)
        #expect(r.occurrences.count == 1 && !r.truncated && r.complete)
        let none = try decode(EventListResult.self, #"{"occurrences":null,"truncated":true,"complete":false}"#)
        #expect(none.occurrences.isEmpty && none.truncated && !none.complete)
        let found = try decode(EventSearchResult.self, #"{"occurrences":[\#(fullOccurrence)],"total":1000}"#)
        #expect(found.total == 1000 && found.occurrences[0].private)
        let q = try encodeObject(EventSearchParams(query: "dentist"))
        #expect(q as NSDictionary == ["query": "dentist"])
        let qa = try encodeObject(EventSearchParams(query: "x", from: at("2026-10-12T00:00:00Z"), to: at("2026-10-19T00:00:00Z"),
                                                    calendarIds: ["c"], limit: 3))
        #expect(qa["from"] as? String == "2026-10-12T00:00:00Z" && qa["limit"] as? Int == 3)

        let get = try encodeObject(EventGetParams(eventId: "ev_1", recurrenceId: .date("2026-10-15"), timeZone: "Europe/Prague"))
        #expect(get["recurrenceId"] as? String == "2026-10-15" && get["timeZone"] as? String == "Europe/Prague")
        #expect(try encodeObject(EventGetParams(eventId: "ev_1")) as NSDictionary == ["eventId": "ev_1"])
    }

    @Test func eventWritesAndResults() throws {
        let input = EventInput(title: "T", allDay: true, startDate: "2026-10-14", endDate: "2026-10-15")
        let create = try encodeObject(EventCreateParams(calendarId: "cal_1", event: input))
        #expect(create.keys.sorted() == ["calendarId", "event"])
        let created = try encodeObject(EventCreateParams(calendarId: "cal_1", event: input, sendInvitations: false,
                                                         mail: MailText(subject: "S", text: "B")))
        #expect(created["sendInvitations"] as? Bool == false && (created["mail"] as? [String: Any])?["subject"] as? String == "S")
        let update = try encodeObject(EventUpdateParams(eventId: "ev_1", recurrenceId: .time(at("2026-10-14T07:00:00Z")),
                                                        scope: .thisAndFuture, etag: "e", event: input, sendUpdates: true))
        #expect(update["scope"] as? String == "thisAndFuture" && update["recurrenceId"] as? String == "2026-10-14T07:00:00Z")
        #expect(update["sendUpdates"] as? Bool == true && update["mail"] == nil)
        let del = try encodeObject(EventDeleteParams(eventId: "ev_1", scope: .all, etag: "e"))
        #expect(del.keys.sorted() == ["etag", "eventId", "scope"])
        let delAll = try encodeObject(EventDeleteParams(eventId: "ev_1", scope: .this, etag: "e", notify: false, comment: "sorry"))
        #expect(delAll["notify"] as? Bool == false && delAll["comment"] as? String == "sorry")
        let respond = try encodeObject(EventRespondParams(eventId: "ev_1", scope: .this, etag: "e", response: .tentative,
                                                          sendResponse: false, proposedStart: at("2026-10-14T09:00:00Z"),
                                                          proposedEnd: at("2026-10-14T10:00:00Z")))
        #expect(respond["response"] as? String == "tentative" && respond["proposedStart"] as? String == "2026-10-14T09:00:00Z")
        #expect(try encodeObject(EventSnoozeAlertParams(alertId: "al_1", minutes: 10)) as NSDictionary == ["alertId": "al_1", "minutes": 10])
        #expect(try encodeObject(EventDismissAlertParams(alertId: "al_1")) as NSDictionary == ["alertId": "al_1"])

        let minimal = String(minimalOccurrence.dropLast())
            + #","etag":"e1","canEdit":true,"canDelete":true,"canRespond":false,"canProposeNewTime":false,"removeIsDelete":false}"#
        let c = try decode(EventCreateResult.self, #"{"event":\#(minimal),"invitations":{"via":"mail","count":3}}"#)
        #expect(c.invitations == InvitationsSent(via: .mail, count: 3) && c.event.etag == "e1")
        let u = try decode(EventUpdateResult.self, #"{"event":\#(minimal),"droppedExceptions":2,"invitations":{"via":"none","count":0}}"#)
        #expect(u.droppedExceptions == 2 && u.invitations.via == .none)
        #expect(try decode(EventDeleteResult.self, #"{"invitations":{"via":"server","count":1}}"#).invitations.via == .server)
        let rr = try decode(EventRespondResult.self, #"{"event":\#(minimal),"via":"server"}"#)
        #expect(rr.via == .server && rr.event.canEdit)
        #expect(try decode(EventGetResult.self, #"{"event":\#(minimal)}"#).event.title == "Standup")
        #expect(try decode(EmptyResult.self, "{}") == EmptyResult())
    }

    // MARK: Invitations

    private let summaryJSON = #"""
    {"method":"request","kind":"update","state":"unanswered","title":"Review","start":"2026-10-14T07:00:00Z","end":"2026-10-14T08:00:00Z",
     "organizer":{"name":"Ann","address":"ann@example.org"}}
    """#
    private let cardJSON = #"""
    {"method":"request","kind":"update","state":"unanswered","title":"Review","start":"2026-10-14T07:00:00Z","end":"2026-10-14T08:00:00Z",
     "organizer":{"name":"Ann","address":"ann@example.org"},
     "partId":"p_2","uid":"uid-1@example.org","sequence":2,"attendeeCount":3,
     "day":{"from":"2026-10-14T00:00:00Z","to":"2026-10-15T00:00:00Z","busy":[]},
     "conflicts":null,"targets":null,"canRespond":true,"canProposeNewTime":false,"replyVia":"mail"}
    """#

    @Test func invitationSummaryAndCardMinimal() throws {
        let s = try decode(InvitationSummary.self, summaryJSON)
        #expect(s.method == .request && s.kind == .update && s.state == .unanswered && s.title == "Review")
        #expect(s.start == at("2026-10-14T07:00:00Z") && s.startDate == nil && !s.allDay && s.replyFrom == nil)
        #expect(s.organizer == Address(name: "Ann", address: "ann@example.org"))
        #expect(try roundTrip(s) == s)

        let c = try decode(InvitationCard.self, cardJSON)
        #expect(c.kind == .update && c.title == "Review" && c.summary.method == .request, "summary fields read through")
        #expect(c.partId == "p_2" && c.uid == "uid-1@example.org" && c.sequence == 2 && c.attendeeCount == 3)
        #expect(c.recurrenceId == nil && c.endDate == nil && c.timeZone == nil && c.recurrence == nil && c.location == nil)
        #expect(c.videoCall == nil && c.notes == nil && !c.organizerMismatch && c.attendees == nil && c.me == nil)
        #expect(c.response == nil && c.respondedAt == nil && c.eventId == nil && c.calendarId == nil && c.previous == nil)
        #expect(c.conflicts.isEmpty && c.targets.isEmpty && c.day.busy.isEmpty && !c.fixedTarget)
        #expect(c.canRespond && !c.canProposeNewTime && c.replyVia == .mail && c.replyAccountId == nil && c.replyTo == nil)
        #expect(!c.replyApplied)
        #expect(try roundTrip(c) == c)
        let wire = try encodeObject(c)
        #expect(wire["summary"] == nil && wire["method"] as? String == "request" && wire["conflicts"] as? [Any] != nil)
        #expect(wire["fixedTarget"] == nil && wire["replyApplied"] == nil && wire["organizerMismatch"] == nil)
    }

    @Test func invitationCardFull() throws {
        let full = String(cardJSON.replacingOccurrences(of: #""conflicts":null,"targets":null,"#, with: "").dropLast()) + #"""
        ,"recurrenceId":"2026-10-14T07:00:00Z","endDate":"2026-10-15","timeZone":"Europe/Prague",
         "recurrence":{"frequency":"daily","interval":1,"count":3},"location":"Room","notes":"Bring slides",
         "videoCall":{"url":"https://zoom.us/j/1","provider":"zoom","host":"zoom.us"},"organizerMismatch":true,
         "attendees":[{"address":"me@example.org","role":"required","response":"needsAction","type":"person","me":true}],
         "me":{"address":"me@example.org","response":"needsAction"},"response":"accepted","respondedAt":"2026-10-13T12:00:00Z",
         "eventId":"ev_5","calendarId":"cal_1",
         "previous":{"start":"2026-10-14T06:00:00Z","end":"2026-10-14"},
         "conflicts":[{"title":"Lunch","start":"2026-10-14T07:30:00Z","end":"2026-10-14T08:00:00Z","calendarId":"cal_1","eventId":"ev_9"},
                      {"start":"2026-10-14T07:00:00Z","end":"2026-10-14T07:10:00Z","calendarId":"cal_2","eventId":"ev_10"}],
         "targets":[{"calendarId":"cal_1","isDefault":true},{"calendarId":"cal_2"}],"fixedTarget":true,
         "replyAccountId":"acc_1","replyTo":{"address":"ann@example.org"},"replyApplied":true}
        """#
        let c = try decode(InvitationCard.self, full)
        #expect(c.recurrenceId == .time(at("2026-10-14T07:00:00Z")) && c.endDate == "2026-10-15" && c.timeZone == "Europe/Prague")
        #expect(c.recurrence?.count == 3 && c.location == "Room" && c.notes == "Bring slides" && c.videoCall?.provider == .zoom)
        #expect(c.organizerMismatch && c.attendees?.count == 1 && c.me == InvitationMe(address: "me@example.org", response: .needsAction))
        #expect(c.response == .accepted && c.respondedAt == at("2026-10-13T12:00:00Z") && c.eventId == "ev_5" && c.calendarId == "cal_1")
        #expect(c.previous == InvitationPrevious(start: .time(at("2026-10-14T06:00:00Z")), end: .date("2026-10-14")))
        #expect(c.conflicts.count == 2 && c.conflicts[0].title == "Lunch" && c.conflicts[1].title == nil)
        #expect(c.targets == [InvitationTarget(calendarId: "cal_1", isDefault: true), InvitationTarget(calendarId: "cal_2")])
        #expect(c.fixedTarget && c.replyAccountId == "acc_1" && c.replyTo == Address(address: "ann@example.org") && c.replyApplied)
        #expect(try roundTrip(c) == c)
    }

    @Test func invitationOwnEvent() throws {
        #expect(try decode(InvitationSummary.self, summaryJSON).ownEvent == false)
        let own = try decode(InvitationSummary.self, String(summaryJSON.dropLast()) + #","ownEvent":true}"#)
        #expect(own.ownEvent)
        #expect(try roundTrip(own) == own)
        #expect(try encodeObject(own)["ownEvent"] as? Bool == true)
        #expect(try encodeObject(InvitationSummary(method: .request, kind: .invitation, state: .unanswered, title: "T"))["ownEvent"] == nil)
    }

    @Test func invitationPreviousFields() throws {
        let all = try decode(InvitationPrevious.self, #"""
        {"start":"2026-10-14T06:00:00Z","end":"2026-10-14T07:00:00Z","location":"Room 1","recurrence":{"frequency":"daily","interval":1}}
        """#)
        #expect(all.start == .time(at("2026-10-14T06:00:00Z")) && all.end == .time(at("2026-10-14T07:00:00Z")))
        #expect(all.location == "Room 1" && all.recurrence?.frequency == .daily)
        #expect(try roundTrip(all) == all)
        let place = try decode(InvitationPrevious.self, #"{"location":"Room 1"}"#)
        #expect(place == InvitationPrevious(location: "Room 1") && place.start == nil && place.recurrence == nil)
        #expect(try encodeObject(place) as NSDictionary == ["location": "Room 1"])
        let old = try decode(InvitationPrevious.self, #"{"start":"2026-10-14","end":"2026-10-15"}"#)
        #expect(old.start == .date("2026-10-14") && old.location == nil)
    }

    @Test func invitationRidesOnTheMessageUnderOneKey() throws {
        let base = #"""
        "id":"m_1","accountId":"acc_1","folderId":"f_1","from":[{"address":"ann@example.org"}],"subject":"Invitation: Review",
        "date":"2026-10-13T10:00:00Z","snippet":"","flags":[],"hasAttachments":true,"size":900
        """#
        let summary = try decode(MessageSummary.self, "{" + base + #","invitation":\#(summaryJSON)}"#)
        #expect(summary.invitation?.kind == .update && summary.invitation?.title == "Review")
        #expect(try decode(MessageSummary.self, "{" + base + "}").invitation == nil)
        #expect(try roundTrip(summary) == summary)

        let message = try decode(Message.self, "{" + base + #","attachments":[],"invitation":\#(cardJSON)}"#)
        #expect(message.invitation?.partId == "p_2" && message.invitation?.uid == "uid-1@example.org")
        #expect(message.summary.invitation == nil, "the card is Message.invitation")
        #expect(message.summary.id == "m_1")
        #expect(try roundTrip(message) == message)
        let wire = try encodeObject(message)
        #expect((wire["invitation"] as? [String: Any])?["partId"] as? String == "p_2")
        #expect(try decode(Message.self, "{" + base + #","attachments":[]}"#).invitation == nil)
    }

    @Test func invitationMethods() throws {
        let list = try decode(InvitationListResult.self, #"{"invitations":[{"accountId":"a","messageId":"m","summary":\#(summaryJSON)}]}"#)
        #expect(list.invitations.count == 1 && list.invitations[0].summary.state == .unanswered)
        #expect(try decode(InvitationListResult.self, #"{"invitations":null}"#).invitations.isEmpty)
        #expect(try encodeObject(InvitationListParams()).isEmpty)
        let params = try encodeObject(InvitationListParams(accountIds: ["a"], state: .unanswered, limit: 50))
        #expect(params["state"] as? String == "unanswered" && params["limit"] as? Int == 50)

        let respond = try encodeObject(InvitationRespondParams(accountId: "a", messageId: "m", response: .accepted))
        #expect(respond.keys.sorted() == ["accountId", "messageId", "response"])
        let respondAll = try encodeObject(InvitationRespondParams(
            accountId: "a", messageId: "m", response: .declined, calendarId: "cal_1", sendResponse: false, comment: "c",
            proposedStart: at("2026-10-14T09:00:00Z"), proposedEnd: at("2026-10-14T10:00:00Z"), mail: MailText(subject: "s", text: "t")))
        #expect(respondAll["calendarId"] as? String == "cal_1" && respondAll["sendResponse"] as? Bool == false)
        let result = try decode(InvitationRespondResult.self, #"{"invitation":\#(cardJSON),"eventId":"ev_1","movedTo":"f_archive"}"#)
        #expect(result.eventId == "ev_1" && result.movedTo == "f_archive" && result.invitation.canRespond)
        let bare = try decode(InvitationRespondResult.self, #"{"invitation":\#(cardJSON)}"#)
        #expect(bare.eventId == nil && bare.movedTo == nil)
        #expect(try encodeObject(InvitationApplyCancelParams(accountId: "a", messageId: "m")) as NSDictionary
            == ["accountId": "a", "messageId": "m"])
        #expect(try decode(InvitationApplyCancelResult.self, #"{"removed":false}"#).removed == false)
    }

    // MARK: Errors and notifications

    @Test func errorCodesAndData() throws {
        #expect(ErrorCode.readOnly.rawValue == 1006 && ErrorCode.invitationsUnavailable.rawValue == 1007)
        #expect(ErrorCode.calendarNotFound.rawValue == 1107 && ErrorCode.eventNotFound.rawValue == 1108)
        #expect(ErrorCode.calendarConsentRequired.rawValue == 1204 && ErrorCode.eventGone.rawValue == 1306)
        #expect(ErrorCode.malformedCalendar.rawValue == 1507 && ErrorCode.recurrenceUnsupported.rawValue == 1508)

        let consent = try decode(RPCError.self, #"{"code":1204,"message":"calendar consent required","data":{"accountId":"acc_1","signIn":false}}"#)
        #expect(consent.calendarConsentRequired == CalendarConsentRequiredData(accountId: "acc_1", signIn: false))
        #expect(consent.conflictEvent == nil)
        #expect(try decode(RPCError.self, #"{"code":1204,"message":"x"}"#).calendarConsentRequired == nil)
        #expect(try decode(RPCError.self, #"{"code":1204,"message":"x","data":"text"}"#).calendarConsentRequired == nil)

        let minimal = String(minimalOccurrence.dropLast())
            + #","etag":"new","canEdit":true,"canDelete":true,"canRespond":false,"canProposeNewTime":false,"removeIsDelete":false}"#
        let conflict = try decode(RPCError.self, #"{"code":1002,"message":"etag mismatch","data":{"event":\#(minimal)}}"#)
        #expect(conflict.conflictEvent?.event.etag == "new" && conflict.calendarConsentRequired == nil)
        #expect(try decode(RPCError.self, #"{"code":1002,"message":"draft"}"#).conflictEvent == nil)
        #expect(try decode(RPCError.self, #"{"code":1002,"message":"draft","data":{"other":1}}"#).conflictEvent == nil)
    }

    @Test func notifications() throws {
        func note(_ method: String, _ params: String?) -> RPCNotification {
            let body = params.map { #","params":\#($0)"# } ?? ""
            return RPCNotification(method: method, line: json(#"{"jsonrpc":"2.0","method":"\#(method)"\#(body)}"#))
        }
        let changed = try #require(try CalendarNotification(note(API.Notify.calendarChanged,
            #"{"accountIds":["a"],"calendarIds":["cal_1"],"from":"2026-10-01T00:00:00Z","to":"2026-11-01T00:00:00Z"}"#)))
        guard case .changed(let c) = changed else { Issue.record("not changed"); return }
        #expect(c.accountIds == ["a"] && c.calendarIds == ["cal_1"] && c.from == at("2026-10-01T00:00:00Z") && c.to == at("2026-11-01T00:00:00Z"))
        for params in [nil, "null", "{}"] {
            guard case .changed(let any)? = try CalendarNotification(note(API.Notify.calendarChanged, params)) else {
                Issue.record("not changed"); return
            }
            #expect(any.accountIds == nil && any.calendarIds == nil && any.from == nil && any.to == nil)
        }
        guard case .state(let s)? = try CalendarNotification(note(API.Notify.calendarState,
            #"{"state":{"accountId":"a","status":"offline","progress":-1}}"#)) else { Issue.record("not state"); return }
        #expect(s.status == .offline && s.accountId == "a")
        guard case .alert(let alert)? = try CalendarNotification(note(API.Notify.eventAlert, #"""
            {"alert":{"alertId":"al_1","state":"due","eventId":"ev_1","accountId":"a","calendarId":"cal_1","title":"Standup",
                      "start":"2026-10-14T07:00:00Z","allDay":false,"minutesBefore":10}}
            """#)) else { Issue.record("not alert"); return }
        #expect(alert.state == .due && alert.minutesBefore == 10 && alert.recurrenceId == nil && alert.end == nil)
        #expect(alert.location == nil && !alert.videoCall && alert.startDate == nil)
        guard case .alert(let full)? = try CalendarNotification(note(API.Notify.eventAlert, #"""
            {"alert":{"alertId":"al_1","state":"dismissed","eventId":"ev_1","recurrenceId":"2026-10-15","accountId":"a","calendarId":"cal_1",
                      "title":"Holiday","startDate":"2026-10-15","allDay":true,"location":"X","videoCall":true,"minutesBefore":0}}
            """#)) else { Issue.record("not alert"); return }
        #expect(full.state == .dismissed && full.recurrenceId == .date("2026-10-15") && full.allDay && full.videoCall)
        guard case .invitationsChanged(let inv)? = try CalendarNotification(note(API.Notify.invitationsChanged, #"{"accountIds":["a"]}"#))
        else { Issue.record("not invitations"); return }
        #expect(inv.concerns("a") && !inv.concerns("b"))
        guard case .invitationsChanged(let any)? = try CalendarNotification(note(API.Notify.invitationsChanged, nil))
        else { Issue.record("not invitations"); return }
        #expect(any.concerns("b"))
        #expect(try CalendarNotification(note(API.Notify.boardChanged, nil)) == nil)
        #expect(throws: (any Error).self) { try CalendarNotification(note(API.Notify.calendarState, #"{"state":5}"#)) }
        // The mail-side enum still reads them as unknown.
        let raw = note(API.Notify.calendarChanged, nil)
        #expect(try DaemonNotification(raw) == .unknown(method: API.Notify.calendarChanged))
    }

    // MARK: Method table and limits

    @Test func methodTable() {
        let names = [
            "account.discoverCalDAV", "account.listCalDAVCalendars", "calendar.list", "calendar.update", "calendar.sync",
            "calendar.preferences", "calendar.setPreferences", "calendar.freeBusy", "calendar.findFreeTime", "event.list",
            "event.get", "event.search", "event.create", "event.update", "event.delete", "event.respond", "event.snoozeAlert",
            "event.dismissAlert", "invitation.list", "invitation.respond", "invitation.applyCancel",
        ]
        #expect(names.count == 21)
        #expect(Array(API.allMethods.dropLast(API.crmMethods.count + API.assistantProfileMethods.count).suffix(21)) == names)
        #expect(API.calendarMethods.map { $0.name } == names)
        #expect(API.CalendarPreferencesGet.name == "calendar.preferences" && API.EventSnoozeAlert.name == "event.snoozeAlert")
        #expect(API.AccountDiscoverCalDAV.timeout == RPCTimeouts.discover && API.AccountListCalDAVCalendars.timeout == RPCTimeouts.test)
        #expect(API.EventCreate.timeout == .seconds(45) && API.EventList.timeout == RPCTimeouts.default)
    }

    @Test func limitsMatchGo() {
        let L = API.Limits.self
        #expect(L.maxICSBytes == 1 << 20 && L.maxEventTitleBytes == 1024 && L.maxEventLocationBytes == 1024)
        #expect(L.maxEventNotesBytes == 32 << 10 && L.maxMailTextBytes == 4 << 10 && L.maxCommentBytes == 2 << 10)
        #expect(L.maxEventAttendeesIn == 100 && L.maxEventAttendeesStored == 500 && L.maxInvitationAttendees == 50)
        #expect(L.maxEventAlerts == 4 && L.maxAlertMinutes == 40320 && L.maxEventAttachments == 10)
        #expect(L.maxEventAttachmentBytes == 3 << 20 && L.maxEventListDays == 400 && L.maxEventListResults == 5000)
        #expect(L.defaultEventListResults == 2000 && L.maxEventSearchResults == 200 && L.maxSeriesOccurrences == 1000)
        #expect(L.maxCalendarsPerAccount == 200 && L.maxEventsPerCalendar == 50000 && L.maxFreeBusyAttendees == 20)
        #expect(L.maxFreeBusyDays == 62 && L.maxFreeSlots == 50 && L.maxInvitationList == 500)
        #expect(L.calendarFutureDays == 400 && L.defaultCalendarKeepDays == 365)
        #expect(L.maxRecurrenceInterval == 99 && L.maxRecurrenceCount == 999)
    }
}
