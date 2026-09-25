// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The typed API layer against the JSON examples of docs/api.md: every
/// method's result decodes, the Go habits (null slices, omitempty, RFC 3339)
/// come out as plain Swift, and the method table matches methods.go.
@Suite struct APICodingTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    // MARK: docs/api.md examples

    @Test func systemInfoExample() throws {
        let r = try decode(SystemInfoResult.self, #"{"version":"0.1.0","protocolVersion":1,"pid":4242,"storePath":"/home/u/.local/share/malachi/store.db"}"#)
        #expect(r == SystemInfoResult(version: "0.1.0", protocolVersion: 1, pid: 4242, storePath: "/home/u/.local/share/malachi/store.db"))
        // The skeleton's name still resolves.
        let legacy: SystemInfo = r
        #expect(legacy.protocolVersion == API.protocolVersion)
    }

    @Test func accountListExample() throws {
        let r = try decode(AccountListResult.self, #"""
        {"accounts":[
          {"id":"acc_1","config":{"name":"Work","email":"me@example.org","displayName":"Me",
             "imap":{"host":"imap.example.org","port":993,"security":"tls","username":"me@example.org","authMethod":"password"},
             "smtp":{"host":"smtp.example.org","port":587,"security":"starttls","username":"me@example.org","authMethod":"password"},
             "syncIntervalSeconds":300},
           "enabled":true,"state":{"accountId":"acc_1","status":"idle","progress":-1,"pendingOutbox":0}},
          {"id":"acc_2","config":{"name":"Gmail","email":"me@gmail.com","kind":"imap",
             "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
             "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
             "oauth2":{"source":"goa","goaAccountId":"account_1788683507_0","provider":"google"}},
           "enabled":true,"state":{"accountId":"acc_2","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","pendingOutbox":1}},
          {"id":"acc_3","config":{"name":"M365","email":"me@contoso.com","kind":"graph","graph":{"source":"goa","goaAccountId":"account_1788512854_0"}},
           "enabled":false,"state":{"accountId":"acc_3","status":"disabled","progress":-1,"error":{"code":1200,"message":"sign in again"},"pendingOutbox":0}}
        ]}
        """#)
        #expect(r.accounts.count == 3)
        let work = r.accounts[0]
        #expect(work.id == "acc_1")
        #expect(work.config.kind == nil && work.config.protocolKind == .imap)
        #expect(work.config.imap?.security == .tls && work.config.smtp?.port == 587)
        #expect(work.config.syncIntervalSeconds == 300)
        #expect(work.state.status == .idle && work.state.progress == -1 && work.state.lastSync == nil)
        let gmail = r.accounts[1]
        #expect(gmail.config.oauth2 == OAuth2Config(source: .goa, goaAccountId: "account_1788683507_0", provider: .google))
        #expect(gmail.config.imap?.authMethod == .oauth2)
        #expect(gmail.state.folderId == "f_inbox" && gmail.state.pendingOutbox == 1)
        #expect(gmail.state.lastSync == RFC3339.parse("2026-09-02T10:00:00Z"))
        let m365 = r.accounts[2]
        #expect(m365.config.protocolKind == .graph && m365.config.imap == nil && m365.config.smtp == nil)
        #expect(m365.config.graph == GraphConfig(source: .goa, goaAccountId: "account_1788512854_0"))
        #expect(!m365.enabled && m365.state.status == .disabled)
        #expect(m365.state.error == RPCError(code: .authRequired, message: "sign in again"))
    }

    @Test func folderListExample() throws {
        let r = try decode(FolderListResult.self, #"""
        {"folders":[
          {"id":"f_1","accountId":"acc_1","name":"Inbox","path":"Inbox","role":"inbox","subscribed":true,"selectable":true,"synced":true,"unread":3,"total":120},
          {"id":"f_2","accountId":"acc_1","parentId":"f_1","name":"Sub","path":"Inbox/Sub","role":"none","subscribed":false,"selectable":true,"synced":true,"unread":0,"total":1},
          {"id":"f_all","accountId":"acc_1","name":"All Mail","path":"[Gmail]/All Mail","role":"archive","subscribed":true,"selectable":true,"synced":false,"unread":0,"total":0},
          {"id":"f_out","accountId":"acc_1","name":"Outbox","path":"","role":"outbox","subscribed":true,"selectable":true,"synced":false,"unread":0,"total":2}
        ]}
        """#)
        #expect(r.folders.map(\.role) == [.inbox, .none, .archive, .outbox])
        #expect(r.folders[0].parentId == nil && r.folders[1].parentId == "f_1")
        #expect(r.folders[2].synced == false)
        #expect(r.folders[3].path == "" && r.folders[3].total == 2)
    }

    static let summaryJSON = #"""
    {"id":"m_123","accountId":"acc_1","folderId":"f_inbox","threadId":"t_9",
     "from":[{"name":"Alice","address":"alice@example.org"}],"to":[{"address":"me@example.org"}],
     "subject":"Lunch","date":"2026-09-02T10:00:00Z",
     "snippet":"plain text, derived by the backend",
     "flags":["seen"],"hasAttachments":false,"size":4321}
    """#

    @Test func messageListExample() throws {
        let r = try decode(MessageListResult.self, #"""
        {"messages":[\#(Self.summaryJSON),
          {"id":"m_7","accountId":"acc_1","folderId":"f_outbox","from":[{"address":"me@example.org"}],
           "subject":"Re: Lunch","date":"2026-09-02T11:30:00.5+02:00","snippet":"","flags":["seen"],"hasAttachments":true,"size":100,
           "outbox":{"state":"failed","attempts":3,"nextAttemptAt":"2026-09-02T12:00:00Z","error":{"code":1302,"message":"550 no"}}}],
         "page":{"nextCursor":"opaque","total":1234}}
        """#)
        #expect(r.page == PageInfo(nextCursor: "opaque", total: 1234))
        #expect(r.messages.count == 2)
        let m = r.messages[0]
        #expect(m.id == "m_123" && m.accountId == "acc_1" && m.folderId == "f_inbox" && m.threadId == "t_9")
        #expect(m.from == [Address(name: "Alice", address: "alice@example.org")])
        #expect(m.to == [Address(address: "me@example.org")])
        #expect(m.flags == [.seen] && m.hasAttachments == false && m.size == 4321)
        #expect(m.date == Date(timeIntervalSince1970: 1_788_343_200))
        #expect(m.outbox == nil)
        let q = r.messages[1]
        #expect(q.threadId == nil, "an unlinked message has no thread")
        #expect(q.to == nil, "omitempty slice absent")
        #expect(q.date == Date(timeIntervalSince1970: 1_788_343_200 - 1800 + 0.5), "11:30+02:00 is 09:30Z")
        let outbox = try #require(q.outbox)
        #expect(outbox.state == .failed && outbox.attempts == 3)
        #expect(outbox.nextAttemptAt == RFC3339.parse("2026-09-02T12:00:00Z"))
        #expect(outbox.error?.code == .serverError)
    }

    static let messageJSON = #"""
    {"id":"m_123","accountId":"acc_1","folderId":"f_inbox","threadId":"t_9",
     "from":[{"name":"Alice","address":"alice@example.org"}],"to":[{"address":"me@example.org"}],
     "subject":"Lunch","date":"2026-09-02T10:00:00Z","snippet":"plain","flags":["seen","answered"],"hasAttachments":true,"size":4321,
     "cc":[{"name":"Bob","address":"bob@example.org"}],"replyTo":[{"address":"alice-reply@example.org"}],
     "rfcMessageId":"<x@example.org>","inReplyTo":"<w@example.org>","references":["<v@example.org>","<w@example.org>"],
     "attachments":[{"partId":"2.1","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false},
                    {"partId":"2.2","filename":"image001.png","contentType":"image/png","size":100,"inline":true,"contentId":"image001@example.org"}],
     "headers":{"List-Unsubscribe":"<mailto:u@example.org>","Auto-Submitted":"no"}}
    """#

    @Test func messageGetExampleFlattensTheSummary() throws {
        let r = try decode(MessageGetResult.self, #"{"message":\#(Self.messageJSON)}"#)
        let m = r.message
        #expect(m.summary.id == "m_123" && m.summary.subject == "Lunch" && m.summary.flags == [.seen, .answered])
        #expect(m.cc == [Address(name: "Bob", address: "bob@example.org")])
        #expect(m.bcc == nil)
        #expect(m.replyTo == [Address(address: "alice-reply@example.org")])
        #expect(m.rfcMessageId == "<x@example.org>" && m.inReplyTo == "<w@example.org>")
        #expect(m.references == ["<v@example.org>", "<w@example.org>"])
        #expect(m.attachments.count == 2 && m.attachments[1].inline && m.attachments[1].contentId == "image001@example.org")
        #expect(m.attachments[0].contentId == nil)
        #expect(m.headers == ["List-Unsubscribe": "<mailto:u@example.org>", "Auto-Submitted": "no"])
    }

    @Test func messageRoundTripsFlat() throws {
        let original = try decode(Message.self, Self.messageJSON)
        let data = try JSONCoding.encoder().encode(original)
        let obj = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
        #expect(obj["summary"] == nil, "the summary is flattened, never nested")
        #expect(obj["id"] as? String == "m_123")
        #expect(obj["cc"] != nil && obj["bcc"] == nil)
        #expect((obj["attachments"] as? [Any])?.count == 2)
        let again = try JSONCoding.decoder().decode(Message.self, from: data)
        #expect(again == original)

        // A message built in code, with nothing optional, survives as well.
        let bare = Message(summary: MessageSummary(
            id: "m_1", accountId: "a", folderId: "f", from: [], subject: "", date: .goZero, snippet: "", flags: [],
            hasAttachments: false, size: 0))
        let bareData = try JSONCoding.encoder().encode(bare)
        #expect(try JSONCoding.decoder().decode(Message.self, from: bareData) == bare)
        let bareObj = try #require(JSONSerialization.jsonObject(with: bareData) as? [String: Any])
        #expect((bareObj["attachments"] as? [Any])?.isEmpty == true)
        #expect(bareObj["threadId"] == nil && bareObj["to"] == nil && bareObj["headers"] == nil)
    }

    @Test func messageBodyExample() throws {
        let r = try decode(MessageBodyResult.self, #"""
        {
          "messageId": "m_123",
          "bodyState": "fetched",
          "hasHtml": true,
          "html": "<p>sanitised</p>",
          "htmlWithheld": false,
          "text": "plain text alternative, or text derived from html",
          "blocked": { "remoteImages": 3, "remoteStyles": 1, "remoteFonts": 0, "scripts": 1,
                       "forms": 0, "eventHandlers": 2, "dangerousUrls": 0, "embeddedFrames": 0,
                       "trackingPixels": 1 },
          "links": [ { "text": "Click here", "href": "https://real.destination/x" } ],
          "inlineParts": { "image001@example.org": "2.1" },
          "remoteContent": "block",
          "sanitizerVersion": "1"
        }
        """#)
        #expect(r.messageId == "m_123" && r.bodyState == .fetched && r.hasHtml)
        #expect(r.html == "<p>sanitised</p>" && r.htmlWithheld == false)
        #expect(r.blocked == BlockedContent(remoteImages: 3, remoteStyles: 1, scripts: 1, eventHandlers: 2, trackingPixels: 1))
        #expect(!r.blocked.isEmpty && BlockedContent().isEmpty)
        #expect(r.links == [Link(text: "Click here", href: "https://real.destination/x")])
        #expect(r.inlineParts == ["image001@example.org": "2.1"])
        #expect(r.remoteContent == .block && r.sanitizerVersion == "1")

        let text = try decode(MessageBodyResult.self, #"""
        {"messageId":"m_1","bodyState":"pending","hasHtml":false,"text":"",
         "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
         "links":null,"remoteContent":"allow","sanitizerVersion":"1"}
        """#)
        #expect(text.html == nil && text.htmlWithheld == nil && text.inlineParts == nil)
        #expect(text.links.isEmpty && text.bodyState == .pending)
    }

    static let threadJSON = #"""
    {"id":"t_9","accountId":"acc_1","subject":"Lunch",
     "participants":[{"name":"Alice","address":"alice@example.org"},{"address":"me@example.org"}],
     "messageCount":3,"unreadCount":1,"latestDate":"2026-09-02T10:00:00Z",
     "latest":\#(summaryJSON),
     "snippet":"plain text, derived by the backend","flags":["flagged","seen"],"hasAttachments":true,
     "folderIds":["f_inbox","f_sent"]}
    """#

    @Test func threadListExample() throws {
        let r = try decode(ThreadListResult.self, #"{"threads":[\#(Self.threadJSON)],"page":{"total":1}}"#)
        #expect(r.page == PageInfo(total: 1))
        let t = try #require(r.threads.first)
        #expect(t.id == "t_9" && t.subject == "Lunch")
        #expect(t.participants.count == 2 && t.messageCount == 3 && t.unreadCount == 1)
        #expect(t.latest.id == "m_123" && t.latestDate == t.latest.date)
        #expect(t.flags == [.flagged, .seen] && t.hasAttachments)
        #expect(t.folderIds == ["f_inbox", "f_sent"])
    }

    @Test func threadGetExample() throws {
        let r = try decode(ThreadGetResult.self, #"{"thread":\#(Self.threadJSON),"messages":[\#(Self.summaryJSON),\#(Self.summaryJSON)]}"#)
        #expect(r.thread.id == "t_9")
        #expect(r.messages.count == 2 && r.messages[1].threadId == "t_9")
    }

    @Test func draftSaveExample() throws {
        let r = try decode(DraftSaveResult.self, #"""
        {"draftId":"d_1","version":2,"textBody":"hi","htmlBody":"<p>hi</p>",
         "blocked":{"remoteImages":1,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
         "attachments":[{"id":"att_1","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false}]}
        """#)
        #expect(r.draftId == "d_1" && r.version == 2 && r.textBody == "hi" && r.htmlBody == "<p>hi</p>")
        #expect(r.blocked.remoteImages == 1)
        #expect(r.attachments == [DraftAttachment(id: "att_1", filename: "safe-name.pdf", contentType: "application/pdf", size: 12345, inline: false)])

        let plain = try decode(DraftSaveResult.self, #"""
        {"draftId":"d_2","version":1,"textBody":"hi",
         "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0}}
        """#)
        #expect(plain.htmlBody == nil && plain.attachments == nil)
    }

    @Test func draftCreateExample() throws {
        let r = try decode(DraftCreateResult.self, #"""
        {"draft":{"accountId":"acc_1","version":0,
                  "to":[{"name":"Alice","address":"alice@example.org"}],"subject":"Re: Lunch",
                  "textBody":"> hi",
                  "htmlBody":"<p><br/></p><div>On Tue, Alice wrote:</div><blockquote type=\"cite\"><p>hi</p></blockquote>",
                  "inReplyTo":"m_123",
                  "attachments":[{"id":"att_2","filename":"image001.png","contentType":"image/png","size":100,"inline":true,"contentId":"abc@malachi.local"}],
                  "updatedAt":"0001-01-01T00:00:00Z"},
         "quoted":"html",
         "blocked":{"remoteImages":2,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
         "skipped":[{"partId":"3","filename":"big.iso","contentType":"application/octet-stream","size":99999999,"inline":false}]}
        """#)
        let d = r.draft
        #expect(d.id == nil && d.version == 0, "unsaved: no id, version 0")
        #expect(d.to == [Address(name: "Alice", address: "alice@example.org")] && d.cc == nil)
        #expect(d.subject == "Re: Lunch" && d.inReplyTo == "m_123" && d.forwarding == nil)
        #expect(d.htmlBody?.contains("<blockquote type=\"cite\">") == true)
        #expect(d.attachments?.first?.contentId == "abc@malachi.local")
        #expect(d.updatedAt.isGoZero)
        #expect(r.quoted == .html && r.blocked.remoteImages == 2)
        #expect(r.skipped?.first?.partId == "3")

        // The draft goes back to draft.save as it came.
        let obj = try encodeObject(DraftSaveParams(draft: d))
        let draft = try #require(obj["draft"] as? [String: Any])
        #expect(draft["id"] == nil && draft["version"] as? Int == 0)
        #expect((draft["to"] as? [Any])?.count == 1 && draft["cc"] == nil)
        #expect(draft["updatedAt"] as? String == "0001-01-01T00:00:00Z")
    }

    @Test func draftOpenExample() throws {
        let r = try decode(DraftOpenResult.self, #"""
        {"draft":{"accountId":"acc_1","version":0,"to":[{"address":"alice@example.org"}],
                  "bcc":[{"name":"Hidden","address":"hidden@example.org"}],"subject":"Re: Plans",
                  "textBody":"x","htmlBody":"<p>x</p>","replaces":"m_9","updatedAt":"0001-01-01T00:00:00Z"},
         "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0}}
        """#)
        #expect(r.draft.id == nil && r.draft.replaces == "m_9" && r.draft.bcc?.first?.address == "hidden@example.org")
        #expect(r.skipped == nil)

        // replaces goes back to draft.save as it came, and is omitted when nil.
        let obj = try encodeObject(DraftSaveParams(draft: r.draft))
        let draft = try #require(obj["draft"] as? [String: Any])
        #expect(draft["replaces"] as? String == "m_9")
        let plain = try encodeObject(DraftSaveParams(draft: Draft(accountId: "a")))
        #expect((plain["draft"] as? [String: Any])?["replaces"] == nil)

        let params = try encodeObject(DraftOpenParams(accountId: "a", messageId: "m_1"))
        #expect(params["accountId"] as? String == "a" && params["messageId"] as? String == "m_1")
    }

    @Test func attachmentImportExample() throws {
        let r = try decode(AttachmentImportResult.self, #"{"attachment":{"id":"att_1","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false,"contentId":"x@malachi.local"}}"#)
        #expect(r.attachment.id == "att_1" && r.attachment.size == 12345 && r.attachment.contentId == "x@malachi.local")

        let params = try encodeObject(AttachmentImportParams(accountId: "acc_1", data: Data([1, 2, 3]), filename: "a.bin", inline: true))
        #expect(params["data"] as? String == "AQID", "binary travels as standard base64")
        #expect(params["path"] == nil && params["inline"] as? Bool == true)
    }

    @Test func accountTestExample() throws {
        let r = try decode(AccountTestResult.self, #"""
        {"imap":{"ok":true,"capabilities":["IDLE","CONDSTORE"],"latencyMs":120},
         "smtp":{"ok":false,"error":{"code":1201,"message":"535 authentication failed"},"latencyMs":80}}
        """#)
        #expect(r.imap == EndpointTestResult(ok: true, capabilities: ["IDLE", "CONDSTORE"], latencyMs: 120))
        #expect(r.smtp?.ok == false && r.smtp?.error?.code == .authFailed && r.smtp?.capabilities == nil)
        #expect(r.graph == nil)

        let graph = try decode(AccountTestResult.self, #"{"graph":{"ok":true,"capabilities":["graph"],"latencyMs":300}}"#)
        #expect(graph.imap == nil && graph.smtp == nil && graph.graph?.capabilities == ["graph"])
    }

    @Test func accountDiscoverExample() throws {
        let ispdb = try decode(AccountDiscoverResult.self, #"""
        {"config":{"name":"example.org","email":"me@example.org",
                   "imap":{"host":"imap.example.org","port":993,"security":"tls","username":"me@example.org","authMethod":"password"},
                   "smtp":{"host":"smtp.example.org","port":587,"security":"starttls","username":"me@example.org","authMethod":"password"}},
         "source":"ispdb"}
        """#)
        #expect(ispdb.source == .ispdb && ispdb.providerName == nil)
        #expect(ispdb.config?.imap?.host == "imap.example.org" && ispdb.config?.smtp?.security == .starttls)

        let none = try decode(AccountDiscoverResult.self, #"{"source":"none"}"#)
        #expect(none == AccountDiscoverResult(source: .none))

        let provider = try decode(AccountDiscoverResult.self, #"""
        {"source":"provider","providerName":"Google",
         "config":{"name":"Google","email":"me@gmail.com",
                   "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                   "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                   "oauth2":{"provider":"google"}}}
        """#)
        #expect(provider.source == .provider && provider.providerName == "Google")
        #expect(provider.config?.oauth2 == OAuth2Config(provider: .google))
        #expect(provider.alternatives.isEmpty, "absent alternatives read as empty")

        // Without GNOME Online Accounts: the daemon's own sign-in first, the
        // app password as the alternative.
        let own = try decode(AccountDiscoverResult.self, #"""
        {"source":"provider","providerName":"Google",
         "config":{"name":"me@gmail.com","email":"me@gmail.com",
                   "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                   "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                   "oauth2":{"source":"daemon","provider":"google"}},
         "alternatives":[{"name":"me@gmail.com","email":"me@gmail.com",
                   "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"password"},
                   "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"password"}}]}
        """#)
        #expect(own.config?.oauth2 == OAuth2Config(source: .daemon, provider: .google))
        #expect(own.alternatives.count == 1 && own.alternatives[0].imap?.authMethod == .password)
        #expect(try decode(AccountDiscoverResult.self, #"{"source":"none","alternatives":null}"#).alternatives.isEmpty)

        let graph = try decode(AccountDiscoverResult.self, #"""
        {"source":"provider","providerName":"Microsoft 365",
         "config":{"name":"me@contoso.com","email":"me@contoso.com","kind":"graph","graph":{"source":"daemon"},
                   "oauth2":{"source":"daemon","provider":"office365","tenantId":"common"}}}
        """#)
        #expect(graph.config?.graph == GraphConfig(source: .daemon))
        #expect(graph.config?.oauth2 == OAuth2Config(source: .daemon, provider: .office365, tenantId: "common"))
    }

    @Test func accountOAuthExamples() throws {
        let start = try decode(AccountOAuthStartResult.self, #"""
        {"sessionId":"s_1","authUrl":"https://accounts.google.com/o/oauth2/v2/auth?x=1","expiresAt":"2026-09-25T10:10:00Z"}
        """#)
        #expect(start.sessionId == "s_1" && start.authUrl.hasPrefix("https://accounts.google.com/"))
        #expect(start.expiresAt == RFC3339.parse("2026-09-25T10:10:00Z"))

        let pending = try decode(AccountOAuthWaitResult.self, #"{"status":"pending"}"#)
        #expect(pending == AccountOAuthWaitResult(status: .pending))
        let complete = try decode(AccountOAuthWaitResult.self, #"""
        {"status":"complete","config":{"name":"Gmail","email":"me@gmail.com",
          "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
          "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
          "oauth2":{"source":"daemon","provider":"google"}}}
        """#)
        #expect(complete.status == .complete && complete.config?.oauth2?.source == .daemon)
        #expect(try decode(AccountOAuthWaitResult.self, #"{"status":"expired"}"#).status == "expired", "an unknown status decodes")

        let byConfig = try encodeObject(AccountOAuthStartParams(
            config: AccountConfig(name: "Gmail", email: "me@gmail.com", oauth2: OAuth2Config(source: .daemon, provider: .google)),
            browserPage: OAuthBrowserPage(successTitle: "Signed in", failureTitle: "Sign-in failed")))
        #expect(byConfig["accountId"] == nil)
        #expect(((byConfig["config"] as? [String: Any])?["oauth2"] as? [String: Any])?["source"] as? String == "daemon")
        let page = try #require(byConfig["browserPage"] as? [String: Any])
        #expect(page.keys.sorted() == ["failureTitle", "successTitle"], "omitted texts are left out")
        let byID = try encodeObject(AccountOAuthStartParams(accountId: "acc_1"))
        #expect(byID.keys.sorted() == ["accountId"])
        #expect(try encodeObject(AccountOAuthWaitParams(sessionId: "s_1"))["sessionId"] as? String == "s_1")
        #expect(try encodeObject(AccountOAuthCancelParams(sessionId: "s_1"))["sessionId"] as? String == "s_1")
    }

    @Test func syncStatusExample() throws {
        let r = try decode(SyncStatusResult.self, #"""
        {"accounts":[
          {"accountId":"acc_1","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","pendingOutbox":0},
          {"accountId":"acc_2","status":"offline","progress":-1,"error":{"code":1301,"message":"dial tcp: connection refused"},"pendingOutbox":2}]}
        """#)
        #expect(r.accounts.count == 2)
        #expect(r.accounts[0] == SyncState(accountId: "acc_1", status: .syncing, folderId: "f_inbox", progress: 42,
                                           lastSync: RFC3339.parse("2026-09-02T10:00:00Z"), pendingOutbox: 0))
        #expect(r.accounts[1].status == .offline && r.accounts[1].error?.code == .networkError && r.accounts[1].pendingOutbox == 2)
    }

    @Test func configGetExample() throws {
        let r = try decode(ConfigGetResult.self, #"{"preferences":{"syncIntervalSeconds":300,"remoteContent":"knownSenders","offlineDays":30}}"#)
        #expect(r.preferences == Preferences(syncIntervalSeconds: 300, remoteContent: .knownSenders, offlineDays: 30))
        // config.set echoes the whole set.
        let obj = try encodeObject(ConfigSetParams(preferences: r.preferences))
        let prefs = try #require(obj["preferences"] as? [String: Any])
        #expect(prefs["syncIntervalSeconds"] as? Int == 300 && prefs["remoteContent"] as? String == "knownSenders" && prefs["offlineDays"] as? Int == 30)
    }

    @Test func contactSearchExample() throws {
        let r = try decode(ContactSearchResult.self, #"""
        {"contacts":[{"name":"Alice Example","address":"alice@example.org","source":"addressBook","book":"Contacts"},
                     {"address":"bob@example.org","source":"sent"}]}
        """#)
        #expect(r.contacts == [
            Contact(name: "Alice Example", address: "alice@example.org", source: .addressBook, book: "Contacts"),
            Contact(address: "bob@example.org", source: .sent),
        ])
        #expect(try decode(ContactSearchResult.self, #"{"contacts":[]}"#).contacts.isEmpty)
    }

    @Test func remainingResultsDecode() throws {
        #expect(try decode(AccountAddResult.self, #"{"accountId":"acc_2"}"#).accountId == "acc_2")
        #expect(try decode(EmptyResult.self, "{}") == EmptyResult())
        #expect(try decode(MessageSendResult.self, #"{"outboxId":"m_7"}"#).outboxId == "m_7")
        let linked = try decode(AccountLinkedResult.self, #"""
        {"accounts":[{"provider":"microsoft365","email":"me@contoso.com","name":"Me","goaAccountId":"account_1788512854_0","configured":false,"attentionNeeded":false,
                      "config":{"name":"Me","email":"me@contoso.com","kind":"graph","graph":{"source":"goa","goaAccountId":"account_1788512854_0"}}}]}
        """#)
        #expect(linked.accounts.first?.provider == .microsoft365 && linked.accounts.first?.config?.protocolKind == .graph)
        let part = try decode(MessagePartResult.self, #"{"partId":"2.1","contentType":"image/png","filename":"a.png","size":3,"data":"AQID"}"#)
        #expect(part.data == Data([1, 2, 3]) && part.size == 3)
        let got = try decode(AttachmentGetResult.self, #"{"attachmentId":"att_1","filename":"a.png","contentType":"image/png","size":3,"data":"AQID"}"#)
        #expect(got.data == Data([1, 2, 3]) && got.attachmentId == "att_1")
        let embedded = try decode(MessageEmbeddedResult.self, #"""
        {"partId":"3","message":\#(Self.messageJSON),
         "body":{"messageId":"m_123","bodyState":"fetched","hasHtml":false,"text":"inner",
                 "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
                 "links":[],"remoteContent":"block","sanitizerVersion":"1"}}
        """#)
        #expect(embedded.partId == "3" && embedded.message.summary.id == "m_123" && embedded.body.text == "inner")
        let drafts = try decode(DraftListResult.self, #"""
        {"drafts":[{"id":"d_1","accountId":"acc_1","version":3,"to":[],"subject":"s","textBody":"t","updatedAt":"2026-09-02T10:00:00.123456789Z"}],"page":{"total":1}}
        """#)
        #expect(drafts.drafts.first?.id == "d_1" && drafts.drafts.first?.to == [])
        let senders = try decode(SenderListResult.self, #"{"senders":[{"address":"alice@example.org","source":"sent","addedAt":"2026-09-02T10:00:00Z"}]}"#)
        #expect(senders.senders.first?.source == .sent)
        let search = try decode(SearchQueryResult.self, #"""
        {"results":[{"message":\#(Self.summaryJSON),"snippet":"plain text excerpt","ranges":[{"start":12,"end":18}],"score":0.83}],"page":{"total":1}}
        """#)
        #expect(search.results.first?.ranges == [MatchRange(start: 12, end: 18)] && search.results.first?.score == 0.83)
    }

    // MARK: Go habits

    @Test func nullOrMissingSliceReadsAsEmpty() throws {
        #expect(try decode(FolderListResult.self, #"{"folders":null}"#).folders.isEmpty)
        #expect(try decode(FolderListResult.self, "{}").folders.isEmpty)
        #expect(try decode(MessageListResult.self, #"{"messages":null,"page":{"total":0}}"#).messages.isEmpty)
        #expect(try decode(AccountListResult.self, #"{"accounts":null}"#).accounts.isEmpty)
        let m = try decode(MessageSummary.self, #"{"id":"m","accountId":"a","folderId":"f","from":null,"subject":"","date":"2026-09-02T10:00:00Z","snippet":"","flags":null,"hasAttachments":false,"size":0}"#)
        #expect(m.from.isEmpty && m.flags.isEmpty)
        // Encodes as an array, never null.
        let obj = try encodeObject(m)
        #expect((obj["from"] as? [Any])?.isEmpty == true && (obj["flags"] as? [Any])?.isEmpty == true)
    }

    @Test func missingOptionalsAreNil() throws {
        let cfg = try decode(AccountConfig.self, #"{"name":"n","email":"e@x"}"#)
        #expect(cfg.displayName == nil && cfg.kind == nil && cfg.imap == nil && cfg.oauth2 == nil && cfg.syncIntervalSeconds == nil)
        let obj = try encodeObject(cfg)
        #expect(obj.keys.sorted() == ["email", "name"], "nil never encodes as null")
        let state = try decode(SyncState.self, #"{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0}"#)
        #expect(state.folderId == nil && state.lastSync == nil && state.error == nil)
    }

    @Test func unknownEnumValuesDecode() throws {
        let f = try decode(Folder.self, #"{"id":"f","accountId":"a","name":"n","path":"n","role":"spam","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0}"#)
        #expect(f.role == FolderRole(rawValue: "spam") && f.role != .junk)
        let s = try decode(SyncState.self, #"{"accountId":"a","status":"hibernating","progress":-1,"pendingOutbox":0}"#)
        #expect(s.status == "hibernating")
        let m = try decode(MessageSummary.self, #"{"id":"m","accountId":"a","folderId":"f","from":[],"subject":"","date":"2026-09-02T10:00:00Z","snippet":"","flags":["seen","pinned"],"hasAttachments":false,"size":0}"#)
        #expect(m.flags == [.seen, "pinned"])
        #expect(FolderRole.inbox.description == "inbox" && FolderRole.inbox.rawValue == "inbox")
    }

    @Test func datesParseWithAndWithoutFractions() throws {
        let plain = try #require(RFC3339.parse("2026-09-02T10:00:00Z"))
        #expect(plain == Date(timeIntervalSince1970: 1_788_343_200))
        #expect(plain == ISO8601DateFormatter().date(from: "2026-09-02T10:00:00Z"))
        #expect(RFC3339.parse("2026-09-02T10:00:00.5Z") == Date(timeIntervalSince1970: 1_788_343_200.5))
        #expect(RFC3339.parse("2026-09-02T10:00:00.123456789Z")?.timeIntervalSince1970 ?? 0 == 1_788_343_200.123456789)
        #expect(RFC3339.parse("2026-09-02T12:00:00+02:00") == plain)
        #expect(RFC3339.parse("2026-09-02T09:30:00.25-00:30") == Date(timeIntervalSince1970: 1_788_343_200.25))
        #expect(RFC3339.parse("2026-09-02t10:00:00z") == plain, "lower-case letters are allowed by RFC 3339")
        #expect(RFC3339.parse("0001-01-01T00:00:00Z") == .goZero)
        #expect(Date.goZero.isGoZero && !plain.isGoZero)
        #expect(Date(timeIntervalSince1970: Date.goZero.timeIntervalSince1970 + 86400 * 200).isGoZero)
        for bad in ["", "2026-09-02", "2026-09-02T10:00:00", "2026-09-02T10:00:00.Z", "2026-13-02T10:00:00Z",
                    "2026-09-02T10:00:00+02", "2026-09-02T10:00:00Zjunk", "1788343200", "2026-09-02 10:00:00Z"] {
            #expect(RFC3339.parse(bad) == nil, "\(bad) must not parse")
        }
        // Through the decoder, inside a struct.
        struct Stamp: Decodable { let at: Date }
        #expect(try decode(Stamp.self, #"{"at":"2026-09-02T10:00:00.5+02:00"}"#).at == Date(timeIntervalSince1970: 1_788_343_200.5 - 7200))
        #expect(throws: DecodingError.self) { try decode(Stamp.self, #"{"at":"yesterday"}"#) }
    }

    @Test func datesEncodeAsRFC3339UTC() throws {
        struct Stamp: Codable, Equatable { let at: Date }
        let obj = try encodeObject(Stamp(at: Date(timeIntervalSince1970: 1_788_343_200)))
        #expect(obj["at"] as? String == "2026-09-02T10:00:00Z")
        let zero = try encodeObject(Stamp(at: .goZero))
        #expect(zero["at"] as? String == "0001-01-01T00:00:00Z", "proleptic Gregorian, as Go; not Foundation's Julian cutover")
        let data = try JSONCoding.encoder().encode(Stamp(at: .goZero))
        #expect(try JSONCoding.decoder().decode(Stamp.self, from: data).at == .goZero)
        #expect(RFC3339.format(Date(timeIntervalSince1970: 1_788_343_200.5)) == "2026-09-02T10:00:00.5Z")
        #expect(RFC3339.format(Date(timeIntervalSince1970: 1_788_343_200.25)) == "2026-09-02T10:00:00.25Z")
        #expect(RFC3339.format(Date(timeIntervalSince1970: -1)) == "1969-12-31T23:59:59Z")
        #expect(RFC3339.format(Date(timeIntervalSince1970: -0.5)) == "1969-12-31T23:59:59.5Z")
        #expect(RFC3339.format(Date(timeIntervalSince1970: 951_782_400)) == "2000-02-29T00:00:00Z", "leap day")
        for s in ["2026-09-02T10:00:00Z", "1999-12-31T23:59:59.999Z", "0001-01-01T00:00:00Z", "2024-02-29T12:34:56.789Z"] {
            #expect(RFC3339.format(try #require(RFC3339.parse(s))) == s, "\(s) must round-trip")
        }
    }

    @Test func attachmentTooBigCarriesSizeLimit() throws {
        let both = try decode(RPCError.self, #"{"code":1502,"message":"too big","data":{"limit":26214400,"size":30000000}}"#)
        #expect(both.attachmentTooBig == SizeLimit(limit: 26_214_400, size: 30_000_000))
        #expect(both.data == .object(["limit": .number(26_214_400), "size": .number(30_000_000)]))
        let limitOnly = try decode(RPCError.self, #"{"code":1502,"message":"too big","data":{"limit":16777216}}"#)
        #expect(limitOnly.attachmentTooBig == SizeLimit(limit: 16_777_216, size: nil))
        let other = try decode(RPCError.self, #"{"code":1001,"message":"bad","data":{"limit":1,"size":2}}"#)
        #expect(other.attachmentTooBig == nil)
        let noData = try decode(RPCError.self, #"{"code":1502,"message":"too big"}"#)
        #expect(noData.attachmentTooBig == nil && noData.data == nil)
        #expect(RPCError(code: 1502, message: "x") == RPCError(code: .attachmentTooBig, message: "x", data: nil))
    }

    @Test func errorCodesAreNamed() {
        #expect(ErrorCode.all.count == 31 && Set(ErrorCode.all).count == 31)
        for code in ErrorCode.all {
            #expect(!code.name.hasPrefix("unknown"), "\(code.rawValue) has no name")
        }
        #expect(ErrorCode.attachmentTooBig.name == "attachmentTooBig" && ErrorCode.attachmentTooBig.rawValue == 1502)
        #expect(ErrorCode.parseError.rawValue == -32700 && ErrorCode.parseError.name == "parseError")
        #expect(ErrorCode(rawValue: 1234).name == "unknown(1234)")
        #expect("\(ErrorCode.keyringError)" == "keyringError")
        #expect(ErrorCode.oauthClientMissing.rawValue == 1203 && ErrorCode.oauthClientMissing.name == "oauthClientMissing")
        #expect(ErrorCode(rawValue: 1203) == .oauthClientMissing)
        let code: ErrorCode = 1102
        #expect(code == .messageNotFound)
    }

    // MARK: Params encoding

    @Test func paramsEncodeVerbatimWithoutNils() throws {
        let list = try encodeObject(MessageListParams(accountId: "acc_1", folderId: "f_inbox", page: Page(limit: 50)))
        #expect(list["accountId"] as? String == "acc_1" && list["folderId"] as? String == "f_inbox")
        #expect((list["page"] as? [String: Any])?["limit"] as? Int == 50)
        #expect((list["page"] as? [String: Any])?["cursor"] == nil)
        #expect(list["sort"] == nil && list["filter"] == nil && list["unreadOnly"] == nil)

        let filtered = try encodeObject(MessageListParams(accountId: "a", folderId: "f", page: Page(cursor: "c"), sort: .dateAsc, filter: .unread))
        #expect(filtered["sort"] as? String == "dateAsc" && filtered["filter"] as? String == "unread")
        #expect((filtered["page"] as? [String: Any])?["cursor"] as? String == "c")

        let flag = try encodeObject(MessageFlagParams(accountId: "a", messageIds: ["m1", "m2"], set: [.seen]))
        #expect(flag["messageIds"] as? [String] == ["m1", "m2"] && flag["set"] as? [String] == ["seen"] && flag["clear"] == nil)

        let empty = try encodeObject(EmptyParams())
        #expect(empty.isEmpty)
        let trigger = try encodeObject(SyncTriggerParams())
        #expect(trigger.isEmpty, "all-optional params encode as {}")

        let add = try encodeObject(AccountAddParams(
            config: AccountConfig(name: "Work", email: "me@example.org",
                                  imap: ServerConfig(host: "imap.example.org", port: 993, security: .tls, username: "me", authMethod: .password),
                                  smtp: ServerConfig(host: "smtp.example.org", port: 587, security: .starttls, username: "me", authMethod: .password)),
            credentials: Credentials(password: "secret")))
        #expect((add["credentials"] as? [String: Any])?["password"] as? String == "secret")
        #expect((add["credentials"] as? [String: Any])?["oauthSession"] == nil)
        let session = try encodeObject(AccountAddParams(
            config: AccountConfig(name: "Gmail", email: "me@gmail.com"), credentials: Credentials(oauthSession: "s_1")))
        #expect((session["credentials"] as? [String: Any])?.keys.sorted() == ["oauthSession"])
        #expect((session["credentials"] as? [String: Any])?["oauthSession"] as? String == "s_1")
        let update = try encodeObject(AccountUpdateParams(
            accountId: "acc_1", config: AccountConfig(name: "Gmail", email: "me@gmail.com"), credentials: Credentials(oauthSession: "s_2")))
        #expect((update["credentials"] as? [String: Any])?["oauthSession"] as? String == "s_2")
        let cfg = try #require(add["config"] as? [String: Any])
        #expect(cfg["kind"] == nil && (cfg["imap"] as? [String: Any])?["security"] as? String == "tls")

        let remove = try encodeObject(AccountRemoveParams(accountId: "a", deleteLocalData: false))
        #expect(remove["deleteLocalData"] as? Bool == false, "a non-omitempty bool is always sent")

        let create = try encodeObject(DraftCreateParams(accountId: "a", mode: .replyAll, messageId: "m", attribution: "On x, y wrote:"))
        #expect(create["mode"] as? String == "replyAll" && create["mailto"] == nil && create["attribution"] as? String == "On x, y wrote:")
    }

    // MARK: Method table

    /// api.AllMethods, copied from backend/pkg/api/methods.go.
    static let goMethods = [
        "system.info",
        "account.list", "account.add", "account.remove", "account.setEnabled",
        "account.update", "account.discover", "account.test", "account.linked",
        "account.reorder", "account.oauthStart", "account.oauthWait", "account.oauthCancel",
        "folder.list", "folder.subscribe",
        "message.list", "message.get", "message.body", "message.part",
        "message.embedded", "message.flag", "message.move", "message.delete",
        "message.send",
        "outbox.retry",
        "thread.list", "thread.get",
        "draft.save", "draft.list", "draft.delete", "draft.create", "draft.open",
        "attachment.import", "attachment.remove", "attachment.get",
        "search.query",
        "sync.status", "sync.trigger",
        "config.get", "config.set",
        "sender.list", "sender.add", "sender.remove",
        "contact.search",
    ]

    @Test func methodTableMatchesGo() {
        #expect(API.allMethods.count == 44)
        #expect(Set(API.allMethods).count == 44, "no duplicates")
        #expect(API.allMethods == Self.goMethods)
        #expect(API.methods.count == API.allMethods.count)
        #expect(API.systemInfo == API.SystemInfo.name)
        #expect(API.allNotifications == ["notify.newMessage", "notify.syncState", "notify.authRequired", "notify.accountsChanged"])
    }

    @Test func timeoutsFollowThePlan() {
        #expect(API.SystemInfo.timeout == .seconds(3))
        #expect(API.MessagePart.timeout == .seconds(60) && API.AttachmentGet.timeout == .seconds(60))
        #expect(API.MessageEmbedded.timeout == .seconds(30) && API.DraftCreate.timeout == .seconds(30))
        #expect(API.DraftOpen.timeout == .seconds(30))
        #expect(API.AccountAdd.timeout == .seconds(30) && API.AccountUpdate.timeout == .seconds(30))
        #expect(API.AccountDiscover.timeout == .seconds(15) && API.AccountTest.timeout == .seconds(45))
        #expect(API.MessageBody.timeout == .seconds(5) && API.MessageList.timeout == .seconds(5) && API.SenderAdd.timeout == .seconds(5))
        #expect(API.AccountOAuthStart.timeout == .seconds(10) && API.AccountOAuthWait.timeout == .seconds(75))
        #expect(RPCTimeouts.oauthStart == .seconds(10) && RPCTimeouts.oauthWaitCall == .seconds(75))
        #expect(API.AccountOAuthCancel.timeout == .seconds(5))
        #expect(RPCTimeouts.default == .seconds(5) && RPCTimeouts.remote == .seconds(30))
        let special: Set<String> = ["system.info", "message.part", "attachment.get", "message.embedded", "draft.create", "draft.open",
                                    "account.add", "account.update", "account.discover", "account.test",
                                    "account.oauthStart", "account.oauthWait"]
        for m in API.methods where !special.contains(m.name) {
            #expect(m.timeout == RPCTimeouts.default, "\(m.name) should use the default timeout")
        }
    }

    @Test func identifiersAreDistinctTypesOverBareStrings() throws {
        let id = AccountID("acc_1")
        #expect(id.rawValue == "acc_1" && id.description == "acc_1" && id == "acc_1")
        let data = try JSONCoding.encoder().encode([id])
        #expect(String(decoding: data, as: UTF8.self) == #"["acc_1"]"#)
        #expect(try JSONCoding.decoder().decode([FolderID].self, from: json(#"["f_1"]"#)) == [FolderID("f_1")])
        #expect(Set([MessageID("m"), MessageID("m"), MessageID("n")]).count == 2)
    }
}
