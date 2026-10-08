// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The board's wire types (docs/api.md §4.13, §5, backend/pkg/api/board.go)
/// with invented data: full and minimal documents, params encoding, the open
/// enums, the notification and the error data.
@Suite struct APICodingBoardTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private let caseID = "c_0123456789abcdef0123456789abcdef"
    private let minimalCase = #"""
    {"id":"c_0123456789abcdef0123456789abcdef","accountId":"acc_1","threadId":"t_1","ruleState":"you",
     "ruleReason":"you.addressed","visibility":"live","subject":"Offer","person":{"address":"ann@example.org"},
     "date":"2026-09-30T08:00:00Z","snippet":"Hello","unread":true,"hasAttachments":false,"messageCount":2,
     "replyMessageId":"m_2","replyFolderId":"f_inbox","latestMessageId":"m_2","canArchive":true,"version":7}
    """#
    private let fullCase = #"""
    {"id":"c_0123456789abcdef0123456789abcdef","accountId":"acc_j","threadId":"jira:10001","ruleState":"info",
     "ruleReason":"jira.watching","userState":"hot",
     "annotation":{"state":"you","title":"Answer Ann","summary":"Line 1\nLine 2","why":"She asked","tasks":["Send quote","Call"],
        "due":{"at":"2026-10-15T00:00:00Z","quote":"by the fifteenth of October","messageId":"m_1"},
        "source":"claude-x","at":"2026-09-30T09:00:00.5Z","stale":true},
     "visibility":"snoozed","doneAt":"2026-09-29T10:00:00Z","remindAt":"2026-10-05T07:00:00Z",
     "subject":"ITSD-42: Printer","person":{"name":"Ann","address":"ann@example.org"},
     "date":"2026-09-30T08:00:00Z","snippet":"x","unread":false,"hasAttachments":true,"messageCount":5,
     "replyMessageId":"c_9","replyFolderId":"space:1","latestMessageId":"m_9",
     "issue":{"key":"ITSD-42","status":"In Progress","statusCategory":"inProgress"},"canArchive":false,
     "draft":{"draftId":"d_1","text":"Thanks","updated":"2026-09-30T09:30:00Z"},"version":9223372036854775807}
    """#

    // MARK: Cases

    @Test func minimalCaseDecodes() throws {
        let c = try decode(BoardCase.self, minimalCase)
        #expect(c.id == BoardCaseID(caseID) && c.accountId == "acc_1" && c.threadId == "t_1")
        #expect(c.ruleState == .you && c.ruleReason == .youAddressed && c.visibility == .live)
        #expect(c.userState == nil && c.annotation == nil && c.doneAt == nil && c.remindAt == nil)
        #expect(c.issue == nil && c.draft == nil && c.canArchive && c.unread && c.version == 7)
        #expect(c.person == Address(address: "ann@example.org") && c.messageCount == 2)
        #expect(c.date == RFC3339.parse("2026-09-30T08:00:00Z"))
        #expect(c.replyMessageId == "m_2" && c.replyFolderId == "f_inbox" && c.latestMessageId == "m_2")
    }

    @Test func fullCaseDecodes() throws {
        let c = try decode(BoardCase.self, fullCase)
        #expect(c.userState == .hot && c.ruleState == .info && c.ruleReason == .jiraWatching)
        #expect(c.visibility == .snoozed && c.doneAt == RFC3339.parse("2026-09-29T10:00:00Z"))
        #expect(c.remindAt == RFC3339.parse("2026-10-05T07:00:00Z"))
        #expect(c.issue == BoardIssue(key: "ITSD-42", status: "In Progress", statusCategory: .inProgress))
        #expect(c.draft == BoardDraft(draftId: "d_1", text: "Thanks", updated: RFC3339.parse("2026-09-30T09:30:00Z")!))
        #expect(c.version == Int64.max)
        let a = try #require(c.annotation)
        #expect(a.state == .you && a.title == "Answer Ann" && a.summary == "Line 1\nLine 2" && a.why == "She asked")
        #expect(a.tasks == ["Send quote", "Call"] && a.source == "claude-x" && a.stale == true)
        #expect(a.at == RFC3339.parse("2026-09-30T09:00:00.5Z"))
        #expect(a.due == BoardDue(at: RFC3339.parse("2026-10-15T00:00:00Z")!, quote: "by the fifteenth of October", messageId: "m_1"))
    }

    @Test func minimalAnnotationAndNullTasks() throws {
        let a = try decode(BoardAnnotation.self, #"{"title":"","summary":"","why":"","tasks":null,"source":"s","at":"2026-09-30T09:00:00Z"}"#)
        #expect(a.state == nil && a.due == nil && a.tasks.isEmpty && a.stale == nil)
        let missing = try decode(BoardAnnotation.self, #"{"title":"t","summary":"","why":"","source":"s","at":"2026-09-30T09:00:00Z"}"#)
        #expect(missing.tasks.isEmpty && missing.title == "t")
    }

    @Test func caseRoundTrips() throws {
        let c = try decode(BoardCase.self, fullCase)
        let back = try JSONCoding.decoder().decode(BoardCase.self, from: JSONCoding.encoder().encode(c))
        #expect(back == c)
        let min = try decode(BoardCase.self, minimalCase)
        let o = try encodeObject(min)
        #expect(o["userState"] == nil && o["annotation"] == nil && o["doneAt"] == nil && o["issue"] == nil && o["draft"] == nil)
        #expect(o["canArchive"] as? Bool == true && o["ruleReason"] as? String == "you.addressed")
    }

    // MARK: Enums

    /// `remindedAt` and the reason `you.newContact` (contract of
    /// 2026-10-08): both decode, round-trip, and are absent by default.
    @MainActor @Test func remindedAtAndNewContact() throws {
        #expect(try decode(BoardCase.self, minimalCase).remindedAt == nil)
        let doc = minimalCase
            .replacingOccurrences(of: "you.addressed", with: "you.newContact")
            .replacingOccurrences(of: #""visibility":"live""#, with: #""visibility":"live","remindedAt":"2026-10-08T07:00:00Z""#)
        let c = try decode(BoardCase.self, doc)
        #expect(c.ruleReason == .youNewContact && BoardReason.known.contains(c.ruleReason))
        #expect(c.remindedAt == RFC3339.parse("2026-10-08T07:00:00Z"))
        let back = try JSONCoding.decoder().decode(BoardCase.self, from: JSONCoding.encoder().encode(c))
        #expect(back == c)
        #expect(try encodeObject(try decode(BoardCase.self, minimalCase))["remindedAt"] == nil)
        // The board's model: reminded only while live.
        #expect(DaemonBoardSource.convert(c).reminded)
        var snoozed = c
        snoozed.visibility = .snoozed
        snoozed.remindAt = RFC3339.parse("2026-10-09T07:00:00Z")
        #expect(DaemonBoardSource.convert(snoozed).remindedAt == nil)
        #expect(DaemonBoardSource.convert(c).newContact)
    }

    @Test func unknownEnumValuesDecode() throws {
        let doc = minimalCase
            .replacingOccurrences(of: #""ruleState":"you""#, with: #""ruleState":"later""#)
            .replacingOccurrences(of: "you.addressed", with: "you.brandNew")
            .replacingOccurrences(of: #""visibility":"live""#, with: #""visibility":"parked""#)
        let c = try decode(BoardCase.self, doc)
        #expect(c.ruleState == BoardState("later") && !c.ruleState.isValid)
        #expect(c.ruleReason == BoardReason("you.brandNew") && !BoardReason.known.contains(c.ruleReason))
        #expect(c.visibility == BoardVisibility("parked"))
        // The unknown value survives a round trip.
        let again = try JSONCoding.decoder().decode(BoardCase.self, from: JSONCoding.encoder().encode(c))
        #expect(again.ruleReason.rawValue == "you.brandNew")

        let cm = try decode(BoardCommitment.self, #"""
        {"id":"k_1","caseId":"c_1","accountId":"a","messageId":"m","text":"t","quote":"q","state":"paused","closedReason":"expired","at":"2026-09-30T09:00:00Z"}
        """#)
        #expect(cm.state == BoardCommitmentState("paused") && cm.closedReason == BoardCommitmentClosedReason("expired"))
        let run = try decode(BoardRun.self, #"{"at":"2026-09-30T09:00:00Z","trigger":"cron","source":"s","annotated":0,"error":"overheated"}"#)
        #expect(run.trigger == BoardTrigger("cron") && run.error == BoardRunError("overheated"))
    }

    @Test func enumConstantsAreTheContractValues() {
        #expect(BoardState.all == ["hot", "you", "them", "info"] && BoardState.all.allSatisfy(\.isValid))
        #expect(BoardReason.known.count == 17 && Set(BoardReason.known).count == 17)
        #expect(BoardReason.known.map(\.rawValue) == [
            "hot.important", "hot.flagged", "you.addressed", "you.repliedToYou", "them.replied", "them.asked",
            "info.ccOnly", "info.notAddressed", "you.newContact", "info.unknownSender", "info.yourNote",
            "jira.yourComment", "jira.assigned", "jira.reporter", "jira.commented", "jira.watching", "kept",
        ])
        #expect([BoardVisibility.live, .done, .snoozed].map(\.rawValue) == ["live", "done", "snoozed"])
        #expect([BoardCommitmentState.open, .done, .closed].map(\.rawValue) == ["open", "done", "closed"])
        #expect([BoardCommitmentClosedReason.replied, .done].map(\.rawValue) == ["replied", "done"])
        #expect([BoardTrigger.manual, .auto, .external].map(\.rawValue) == ["manual", "auto", "external"])
        #expect([BoardRunError.cancelled, .timeout, .signedOut, .failed].map(\.rawValue) == ["cancelled", "timeout", "signedOut", "failed"])
        #expect(QuoteField.due.rawValue == "due" && QuoteField.commitment.rawValue == "commitment")
    }

    // MARK: board.list, board.get

    @Test func listResultFullAndMinimal() throws {
        let full = try decode(BoardListResult.self, #"""
        {"cases":[\#(fullCase),\#(minimalCase)],
         "commitments":[{"id":"k_1","caseId":"c_1","accountId":"acc_1","messageId":"m_3","text":"Send the report","quote":"I will send the report",
            "due":"2026-10-02T00:00:00Z","state":"open","at":"2026-09-30T09:00:00Z"}],
         "enabled":true,"assistant":true,
         "triage":{"lastRun":{"at":"2026-09-30T07:00:00Z","endedAt":"2026-09-30T07:02:00Z","trigger":"auto","source":"claude-x","annotated":4,"error":"signedOut"},
                   "annotatedTodayAuto":4,"queue":11},
         "ready":true,"truncated":true}
        """#)
        #expect(full.cases.count == 2 && full.commitments.count == 1 && full.enabled && full.assistant)
        #expect(full.ready && full.truncated == true)
        #expect(full.commitments[0].due == RFC3339.parse("2026-10-02T00:00:00Z") && full.commitments[0].closedReason == nil)
        #expect(full.commitments[0].state == .open && full.commitments[0].id == BoardCommitmentID("k_1"))
        let run = try #require(full.triage.lastRun)
        #expect(run.trigger == .auto && run.error == .signedOut && run.annotated == 4 && run.endedAt != nil)
        #expect(full.triage.annotatedTodayAuto == 4 && full.triage.queue == 11)

        let min = try decode(BoardListResult.self, #"{"cases":null,"commitments":null,"enabled":false,"assistant":false,"triage":{"annotatedTodayAuto":0,"queue":0},"ready":false}"#)
        #expect(min.cases.isEmpty && min.commitments.isEmpty && !min.enabled && !min.ready)
        #expect(min.truncated == nil && min.triage.lastRun == nil && min.triage.usage24h == nil)
        let running = try decode(BoardRun.self, #"{"at":"2026-09-30T07:00:00Z","trigger":"manual","source":"s","annotated":0}"#)
        #expect(running.endedAt == nil && running.error == nil)
    }

    @Test func listParamsEncode() throws {
        #expect(try encodeObject(BoardListParams()).isEmpty, "accountIds is left out when empty")
        #expect(try encodeObject(BoardListParams(accountIds: ["a", "b"]))["accountIds"] as? [String] == ["a", "b"])
        #expect(try decode(BoardListParams.self, "{}") == BoardListParams())
    }

    @Test func getResult() throws {
        let r = try decode(BoardGetResult.self, #"""
        {"case":\#(minimalCase),"messages":[
          {"id":"m_1","folderId":"f_inbox","from":{"name":"Ann","address":"ann@example.org"},"date":"2026-09-30T07:00:00Z","mine":false,"text":"Hi","trimmed":true},
          {"id":"m_2","folderId":"f_sent","from":{"address":"me@example.org"},"date":"2026-09-30T08:00:00Z","mine":true,"text":"Re"}]}
        """#)
        #expect(r.case.version == 7 && r.messages.count == 2)
        #expect(r.messages[0].trimmed == true && r.messages[0].from.name == "Ann")
        #expect(r.messages[1].trimmed == nil && r.messages[1].mine)
        #expect(try decode(BoardGetResult.self, #"{"case":\#(minimalCase),"messages":null}"#).messages.isEmpty)
        #expect(try encodeObject(BoardGetParams(caseId: "c_1"))["caseId"] as? String == "c_1")
    }

    // MARK: The user's decisions

    @Test func setStateEncodesNullToClear() throws {
        let set = try encodeObject(BoardSetStateParams(caseId: "c_1", state: .them))
        #expect(set["state"] as? String == "them" && set["caseId"] as? String == "c_1")
        let clear = try encodeObject(BoardSetStateParams(caseId: "c_1", state: nil))
        #expect(clear.keys.contains("state") && clear["state"] is NSNull, "null = back to automatic")
        let data = try JSONCoding.encoder().encode(BoardSetStateParams(caseId: "c_1", state: nil))
        #expect(try JSONCoding.decoder().decode(BoardSetStateParams.self, from: data).state == nil)
        #expect(try decode(BoardSetStateParams.self, #"{"caseId":"c_1"}"#).state == nil)
        #expect(try decode(BoardSetStateResult.self, #"{"case":\#(minimalCase)}"#).case.version == 7)
    }

    @Test func remindEncodesNullToClear() throws {
        let until = RFC3339.parse("2026-10-05T07:00:00Z")!
        let set = try encodeObject(BoardRemindParams(caseId: "c_1", until: until))
        #expect(set["until"] as? String == "2026-10-05T07:00:00Z")
        let clear = try encodeObject(BoardRemindParams(caseId: "c_1", until: nil))
        #expect(clear.keys.contains("until") && clear["until"] is NSNull)
        let data = try JSONCoding.encoder().encode(BoardRemindParams(caseId: "c_1", until: until))
        #expect(try JSONCoding.decoder().decode(BoardRemindParams.self, from: data).until == until)
        #expect(try decode(BoardRemindResult.self, #"{"case":\#(minimalCase)}"#).case.id == BoardCaseID(caseID))
    }

    @Test func doneArchiveDiscard() throws {
        let done = try encodeObject(BoardSetDoneParams(caseId: "c_1", done: false))
        #expect(done["done"] as? Bool == false, "a non-omitempty bool is always sent")
        #expect(try decode(BoardSetDoneResult.self, #"{"case":\#(minimalCase)}"#).case.canArchive)
        #expect(try encodeObject(BoardArchiveParams(caseId: "c_1")).keys.sorted() == ["caseId"])
        let a = try decode(BoardArchiveResult.self, #"{"archived":2,"case":\#(minimalCase)}"#)
        #expect(a.archived == 2 && a.noArchive == nil)
        let n = try decode(BoardArchiveResult.self, #"{"archived":0,"noArchive":true,"case":\#(minimalCase)}"#)
        #expect(n.archived == 0 && n.noArchive == true)
        #expect(a.moved == nil && n.moved == nil)  // absent: nothing moved, or an older daemon
        let m = try decode(BoardArchiveResult.self, #"{"archived":2,"case":\#(minimalCase),"moved":[{"messageId":"m_1","fromFolderId":"f_in"},{"messageId":"m_2","fromFolderId":"f_work"}]}"#)
        #expect(m.moved == [
            BoardMoved(messageId: "m_1", fromFolderId: "f_in"), BoardMoved(messageId: "m_2", fromFolderId: "f_work"),
        ])
        let back = try JSONCoding.decoder().decode(BoardArchiveResult.self, from: JSONCoding.encoder().encode(m))
        #expect(back == m)
        #expect(try encodeObject(BoardDiscardDraftParams(caseId: "c_1")).keys.sorted() == ["caseId"])
        #expect(try decode(BoardDiscardDraftResult.self, #"{"case":\#(minimalCase)}"#).case.draft == nil)
    }

    @Test func unflag() throws {
        #expect(try encodeObject(BoardUnflagParams(caseId: "c_1")).keys.sorted() == ["caseId"])
        let r = try decode(BoardUnflagResult.self, #"{"case":\#(minimalCase),"unflagged":3}"#)
        #expect(r.unflagged == 3 && r.case.id == BoardCaseID(caseID))
        let data = try JSONCoding.encoder().encode(r)
        #expect(try JSONCoding.decoder().decode(BoardUnflagResult.self, from: data) == r)
        #expect(API.BoardUnflag.name == "board.unflag")
    }

    @Test func setDraft() throws {
        let p = try encodeObject(BoardSetDraftParams(caseId: "c_1", draftId: "d_7"))
        #expect(p.keys.sorted() == ["caseId", "draftId"])
        #expect(p["caseId"] as? String == "c_1" && p["draftId"] as? String == "d_7")
        #expect(try decode(BoardSetDraftResult.self, #"{"case":\#(minimalCase)}"#).case.id == BoardCaseID(caseID))
        #expect(API.BoardSetDraft.name == "board.setDraft")
    }

    // MARK: Triage

    @Test func queueResultAndParams() throws {
        let r = try decode(BoardQueueResult.self, #"""
        {"items":[{"caseId":"c_1","accountId":"acc_1","inputKey":"k1","ruleState":"them","ruleReason":"them.asked",
           "subject":"Hello","replyMessageId":"m_2","own":["me@example.org"],
           "messages":[{"messageId":"m_1","from":{"address":"a@x"},"to":[{"address":"b@x"}],"cc":[{"address":"c@x"}],
              "date":"2026-09-30T07:00:00Z","mine":true,"text":"Q?","truncated":true},
              {"messageId":"m_2","from":{"address":"b@x"},"date":"2026-09-30T08:00:00Z","mine":false,"text":"A"}]},
          {"caseId":"c_2","accountId":"acc_j","inputKey":"k2","ruleState":"you","ruleReason":"jira.assigned","userState":"hot",
           "subject":"K-1: s","replyMessageId":"m_9","issue":{"key":"K-1","status":"Open"},"own":null,"messages":null}],
         "remaining":7}
        """#)
        #expect(r.remaining == 7 && r.items.count == 2)
        let m = r.items[0].messages
        #expect(m[0].to == [Address(address: "b@x")] && m[0].cc?.count == 1 && m[0].truncated == true && m[0].mine)
        #expect(m[1].to == nil && m[1].cc == nil && m[1].truncated == nil)
        #expect(r.items[0].own == ["me@example.org"] && r.items[0].inputKey == "k1" && r.items[0].userState == nil)
        #expect(r.items[1].userState == .hot && r.items[1].issue?.statusCategory == nil)
        #expect(r.items[1].own.isEmpty && r.items[1].messages.isEmpty)
        #expect(try decode(BoardQueueResult.self, #"{"items":null,"remaining":0}"#).items.isEmpty)

        #expect(try encodeObject(BoardQueueParams()).isEmpty)
        let p = try encodeObject(BoardQueueParams(accountIds: ["a"], caseIds: ["c_1"], limit: 5))
        #expect(p["accountIds"] as? [String] == ["a"] && p["caseIds"] as? [String] == ["c_1"] && p["limit"] as? Int == 5)
    }

    @Test func annotateParamsLeaveOutWhatIsEmpty() throws {
        let minimal = try encodeObject(BoardAnnotateParams(caseId: "c_1", inputKey: "k", source: "claude-x"))
        #expect(minimal.keys.sorted() == ["caseId", "inputKey", "source"])
        let due = BoardDue(at: RFC3339.parse("2026-10-15T00:00:00Z")!, quote: "by the fifteenth", messageId: "m_1")
        let full = BoardAnnotateParams(
            caseId: "c_1", inputKey: "k", runId: "r_1", state: .you, title: "T", summary: "S\nS", why: "W",
            tasks: ["a", "b"], due: due, draftId: "d_1", source: "claude-x")
        let o = try encodeObject(full)
        #expect(o["runId"] as? String == "r_1" && o["state"] as? String == "you" && o["title"] as? String == "T")
        #expect(o["summary"] as? String == "S\nS" && o["why"] as? String == "W" && o["tasks"] as? [String] == ["a", "b"])
        #expect(o["draftId"] as? String == "d_1" && (o["due"] as? [String: Any])?["messageId"] as? String == "m_1")
        #expect((o["due"] as? [String: Any])?["at"] as? String == "2026-10-15T00:00:00Z")
        let back = try JSONCoding.decoder().decode(BoardAnnotateParams.self, from: JSONCoding.encoder().encode(full))
        #expect(back == full)
        #expect(try JSONCoding.decoder().decode(BoardAnnotateParams.self, from: JSONCoding.encoder().encode(
            BoardAnnotateParams(caseId: "c_1", inputKey: "k", source: "s"))).tasks.isEmpty)
        #expect(try decode(BoardAnnotateResult.self, #"{"case":\#(minimalCase)}"#).case.version == 7)
    }

    @Test func commitAndSetCommitment() throws {
        let p = try encodeObject(BoardCommitParams(
            caseId: "c_1", inputKey: "k", messageId: "m_3", text: "Send it", quote: "I will send it",
            due: RFC3339.parse("2026-10-02T00:00:00Z"), source: "s"))
        #expect(p["runId"] == nil && p["due"] as? String == "2026-10-02T00:00:00Z" && p["quote"] as? String == "I will send it")
        let bare = try encodeObject(BoardCommitParams(caseId: "c_1", inputKey: "k", messageId: "m", text: "t", quote: "q", source: "s"))
        #expect(bare["due"] == nil && bare["runId"] == nil)
        let r = try decode(BoardCommitResult.self, #"""
        {"commitment":{"id":"k_1","caseId":"c_1","accountId":"a","messageId":"m_3","text":"Send it","quote":"I will send it",
          "state":"closed","closedReason":"replied","at":"2026-09-30T09:00:00Z"}}
        """#)
        #expect(r.commitment.state == .closed && r.commitment.closedReason == .replied && r.commitment.due == nil)
        #expect(r.existing == nil)
        let again = try decode(BoardCommitResult.self, #"""
        {"commitment":{"id":"k_1","caseId":"c_1","accountId":"a","messageId":"m_3","text":"Send it","quote":"I will send it",
          "state":"open","at":"2026-09-30T09:00:00Z"},"existing":true}
        """#)
        #expect(again.existing == true && again.commitment.id == "k_1")
        let s = try encodeObject(BoardSetCommitmentParams(commitmentId: "k_1", done: false))
        #expect(s["commitmentId"] as? String == "k_1" && s["done"] as? Bool == false)
        let sr = try decode(BoardSetCommitmentResult.self, #"""
        {"commitment":{"id":"k_1","caseId":"c_1","accountId":"a","messageId":"m","text":"t","quote":"q","state":"done","at":"2026-09-30T09:00:00Z"}}
        """#)
        #expect(sr.commitment.state == .done)
    }

    // MARK: Preferences and runs

    @Test func preferences() throws {
        #expect(try encodeObject(BoardPreferencesParams()).isEmpty)
        let r = try decode(BoardPreferencesResult.self, #"""
        {"preferences":{"enabled":true,"assistant":false,"windows":{"hot":90,"you":30,"them":30,"info":14},
         "triageAccounts":null,"autoTriage":false,"autoTriageMinutes":30,"autoTriageDailyCases":60}}
        """#)
        #expect(r.preferences == BoardPreferences.defaults && r.preferences.triageAccounts.isEmpty)
        #expect(BoardPreferences.defaults.windows == BoardWindows(hot: 90, you: 30, them: 30, info: 14))

        var changed = r.preferences
        changed.assistant = true
        changed.autoTriage = true
        changed.triageAccounts = ["acc_j"]
        changed.windows.info = 7
        changed.autoTriageDailyCases = 0
        let o = try encodeObject(BoardSetPreferencesParams(preferences: changed))
        let p = try #require(o["preferences"] as? [String: Any])
        #expect(p["assistant"] as? Bool == true && p["autoTriage"] as? Bool == true && p["triageAccounts"] as? [String] == ["acc_j"])
        #expect((p["windows"] as? [String: Any])?["info"] as? Int == 7 && p["autoTriageDailyCases"] as? Int == 0)
        #expect(p["enabled"] as? Bool == true && p["autoTriageMinutes"] as? Int == 30)
        // Every preference is sent, an empty list as [] (the daemon replaces all).
        let empty = try encodeObject(BoardSetPreferencesParams(preferences: .defaults))
        #expect((empty["preferences"] as? [String: Any])?["triageAccounts"] as? [String] == [])
        let stored = try decode(BoardSetPreferencesResult.self, #"{"preferences":{"enabled":false,"assistant":true,"windows":{"hot":1,"you":2,"them":3,"info":365},"triageAccounts":["a"],"autoTriage":true,"autoTriageMinutes":1440,"autoTriageDailyCases":1000}}"#)
        #expect(stored.preferences.windows.info == 365 && stored.preferences.triageAccounts == ["a"] && !stored.preferences.enabled)
    }

    @Test func runs() throws {
        let s = try encodeObject(BoardRunStartParams(trigger: .manual, source: "malachi"))
        #expect(s["trigger"] as? String == "manual" && s["source"] as? String == "malachi")
        #expect(try decode(BoardRunStartResult.self, #"{"runId":"r_1"}"#).runId == BoardRunID("r_1"))
        let ok = try encodeObject(BoardRunEndParams(runId: "r_1"))
        #expect(ok.keys.sorted() == ["runId"], "no error = success")
        let failed = try encodeObject(BoardRunEndParams(runId: "r_1", error: .timeout))
        #expect(failed["error"] as? String == "timeout")
        #expect(try decode(API.BoardRunEnd.Result.self, "{}") == EmptyResult())
        // The run's usage, the four counters under the contract's keys.
        let used = try encodeObject(BoardRunEndParams(
            runId: "r_1", error: .cancelled,
            usage: BoardUsage(inputTokens: 1200, outputTokens: 340, cacheCreationInputTokens: 5, cacheReadInputTokens: 9000)))
        let u = try #require(used["usage"] as? [String: Any])
        #expect(u.keys.sorted() == ["cacheCreationInputTokens", "cacheReadInputTokens", "inputTokens", "outputTokens"])
        #expect(u["inputTokens"] as? Int == 1200 && u["outputTokens"] as? Int == 340)
        #expect(u["cacheCreationInputTokens"] as? Int == 5 && u["cacheReadInputTokens"] as? Int == 9000)
        // The assistant's tally, clamped to the contract's maximum.
        let clamped = BoardUsage(Assistant.Usage(inputTokens: -3, outputTokens: Int64.max, cacheReadInputTokens: 7))
        #expect(clamped == BoardUsage(outputTokens: API.Limits.maxBoardUsageTokens, cacheReadInputTokens: 7))
        // A lower bound says so; a whole count leaves the key out.
        let partial = try encodeObject(BoardRunEndParams(runId: "r_1", usage: BoardUsage(inputTokens: 1, lowerBound: true)))
        #expect((partial["usage"] as? [String: Any])?["lowerBound"] as? Bool == true)
        let whole = try encodeObject(BoardRunEndParams(runId: "r_1", usage: BoardUsage(inputTokens: 1)))
        #expect((whole["usage"] as? [String: Any])?["lowerBound"] == nil)
        #expect(BoardUsage(Assistant.Usage(inputTokens: 1), lowerBound: true).lowerBound)
        let decoded = try decode(
            BoardUsage.self,
            #"{"inputTokens":1,"outputTokens":2,"cacheCreationInputTokens":3,"cacheReadInputTokens":4,"lowerBound":true}"#)
        #expect(decoded == BoardUsage(inputTokens: 1, outputTokens: 2, cacheCreationInputTokens: 3, cacheReadInputTokens: 4, lowerBound: true))
    }

    /// `triage.usage24h`: one flat object; absent = none; a counter left
    /// out reads as 0.
    @Test func usage24h() throws {
        let t = try decode(BoardTriage.self, #"{"annotatedTodayAuto":0,"queue":1,"usage24h":{"inputTokens":1200,"outputTokens":340,"cacheCreationInputTokens":5,"cacheReadInputTokens":9000,"runs":2}}"#)
        #expect(t.usage24h == BoardUsageTotal(
            inputTokens: 1200, outputTokens: 340, cacheCreationInputTokens: 5, cacheReadInputTokens: 9000, runs: 2))
        let partial = try decode(BoardTriage.self, #"{"annotatedTodayAuto":0,"queue":0,"usage24h":{"cacheReadInputTokens":1000000000000,"runs":1}}"#)
        #expect(partial.usage24h == BoardUsageTotal(cacheReadInputTokens: 1_000_000_000_000, runs: 1))
        #expect(try decode(BoardTriage.self, #"{"annotatedTodayAuto":0,"queue":0,"usage24h":null}"#).usage24h == nil)
        #expect(t.usage24h?.lowerBound == false)
        let atLeast = try decode(BoardTriage.self, #"{"annotatedTodayAuto":0,"queue":0,"usage24h":{"inputTokens":5,"runs":2,"lowerBound":true}}"#)
        #expect(atLeast.usage24h == BoardUsageTotal(inputTokens: 5, runs: 2, lowerBound: true))
        #expect(Board.triageUsageTexts(atLeast.usage24h, locale: Locale(identifier: "en_US_POSIX")).value == "at least 5")
        #expect(try encodeObject(BoardTriage()).keys.sorted() == ["annotatedTodayAuto", "queue"])
    }

    // MARK: Notification and error data

    @Test func boardChangedNotification() throws {
        func note(_ line: String) -> RPCNotification { RPCNotification(method: API.Notify.boardChanged, line: json(line)) }
        #expect(API.Notify.boardChanged == "notify.boardChanged")
        let some = try DaemonNotification(note(#"{"jsonrpc":"2.0","method":"notify.boardChanged","params":{"accountIds":["acc_1"]}}"#))
        #expect(some == .boardChanged(BoardChangedNotification(accountIds: ["acc_1"])))
        let empty = BoardChangedNotification()
        #expect(try DaemonNotification(note(#"{"jsonrpc":"2.0","method":"notify.boardChanged","params":{}}"#)) == .boardChanged(empty))
        #expect(try DaemonNotification(note(#"{"jsonrpc":"2.0","method":"notify.boardChanged","params":null}"#)) == .boardChanged(empty))
        #expect(try DaemonNotification(note(#"{"jsonrpc":"2.0","method":"notify.boardChanged"}"#)) == .boardChanged(empty))
        #expect(try DaemonNotification(note(#"{"jsonrpc":"2.0","method":"notify.boardChanged","params":{"accountIds":null}}"#)) == .boardChanged(empty))
        #expect(throws: (any Error).self) {
            try DaemonNotification(note(#"{"jsonrpc":"2.0","method":"notify.boardChanged","params":[1]}"#))
        }
        #expect(empty.concerns("any") && !BoardChangedNotification(accountIds: ["a"]).concerns("b"))
        #expect(BoardChangedNotification(accountIds: ["a"]).concerns("a"))
    }

    @Test func quoteNotFoundData() throws {
        let e = try decode(RPCError.self, #"{"code":1506,"message":"quote not found","data":{"field":"due"}}"#)
        #expect(e.code == .quoteNotFound && e.code.rawValue == 1506 && e.code.name == "quoteNotFound")
        #expect(e.quoteNotFound == QuoteNotFoundData(field: .due))
        let c = try decode(RPCError.self, #"{"code":1506,"message":"x","data":{"field":"commitment"}}"#)
        #expect(c.quoteNotFound?.field == .commitment)
        let newer = try decode(RPCError.self, #"{"code":1506,"message":"x","data":{"field":"deadline"}}"#)
        #expect(newer.quoteNotFound?.field == QuoteField("deadline"))
        #expect(RPCError(code: .quoteNotFound, message: "x").quoteNotFound == nil)
        #expect(RPCError(code: .quoteNotFound, message: "x", data: .array([])).quoteNotFound == nil)
        #expect(RPCError(code: .quoteNotFound, message: "x", data: .object([:])).quoteNotFound == nil)
        #expect(RPCError(code: .invalidArgument, message: "x", data: e.data).quoteNotFound == nil)
        #expect(ErrorCode.caseNotFound.rawValue == 1106 && ErrorCode(rawValue: 1106).name == "caseNotFound")
    }

    @Test func methodEntries() {
        let names = API.methods.map { $0.name }
        for m in ["board.list", "board.get", "board.setState", "board.setDone", "board.remind", "board.archive",
                  "board.unflag", "board.discardDraft", "board.setDraft", "board.queue", "board.annotate", "board.commit",
                  "board.setCommitment", "board.preferences", "board.setPreferences", "board.runStart", "board.runEnd"] {
            #expect(names.contains(m), "\(m)")
        }
        #expect(API.BoardList.name == "board.list" && API.BoardPreferencesGet.name == "board.preferences")
        // The board answers from the local store: the default timeout.
        var board = 0
        for m in API.methods where m.name.hasPrefix("board.") {
            board += 1
            let timeout: Duration = m.timeout
            #expect(timeout == RPCTimeouts.default)
        }
        #expect(board == 17)
    }
}
