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
        let r = try decode(SystemInfoResult.self, #"{"version":"0.1.0","protocolVersion":2,"pid":4242,"storePath":"/home/u/.local/share/malachi/store.db"}"#)
        #expect(r == SystemInfoResult(version: "0.1.0", protocolVersion: 2, pid: 4242, storePath: "/home/u/.local/share/malachi/store.db"))
        // The skeleton's name still resolves.
        let legacy: SystemInfo = r
        #expect(legacy.protocolVersion == API.protocolVersion)
    }

    /// docs/api.md §4.0 and §1.4: system.hello and system.authenticate.
    @Test func handshakeExamples() throws {
        let nonce = String(repeating: "0f", count: 32)
        let proof = String(repeating: "ab", count: 32)
        let hello = try encodeObject(SystemHelloParams(clientNonce: nonce))
        #expect(hello.keys.sorted() == ["clientNonce"] && hello["clientNonce"] as? String == nonce)
        let r = try decode(SystemHelloResult.self, #"{"protocolVersion":2,"daemonNonce":"\#(nonce)","daemonProof":"\#(proof)"}"#)
        #expect(r == SystemHelloResult(protocolVersion: 2, daemonNonce: nonce, daemonProof: proof))
        let auth = try encodeObject(SystemAuthenticateParams(clientProof: proof))
        #expect(auth.keys.sorted() == ["clientProof"] && auth["clientProof"] as? String == proof)
        #expect(try decode(API.SystemAuthenticate.Result.self, "{}") == EmptyResult())
        #expect(API.SystemHello.name == "system.hello" && API.SystemAuthenticate.name == "system.authenticate")
        #expect(API.protocolVersion == 2)
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
        #expect(m.attachments[0].remote == nil && !m.attachments[0].isRemote, "absent is false")
        #expect(m.headers == ["List-Unsubscribe": "<mailto:u@example.org>", "Auto-Submitted": "no"])
    }

    /// docs/api.md §3 Attachment: `remote` (opt), omitted when not set.
    @Test func attachmentRemoteFlag() throws {
        let a = try decode(MalachiCore.Attachment.self, #"""
        {"partId":"2","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false,"contentId":"x","remote":true}
        """#)
        #expect(a.isRemote && a.remote == true && a.size == 12345)
        let local = try decode(MalachiCore.Attachment.self, #"{"partId":"3","filename":"a.txt","contentType":"text/plain","size":1,"inline":false,"remote":false}"#)
        #expect(!local.isRemote)
        #expect(try encodeObject(a)["remote"] as? Bool == true)
        let plain = try encodeObject(MalachiCore.Attachment(partId: "1", filename: "a", contentType: "text/plain", size: 1, inline: false))
        #expect(plain["remote"] == nil, "omitempty")
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
          "remotePictures": 1,
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
        #expect(r.remotePictures == 1 && r.remotePictureCount == 1)
        #expect(r.remoteContent == .block && r.sanitizerVersion == "1")

        let text = try decode(MessageBodyResult.self, #"""
        {"messageId":"m_1","bodyState":"pending","hasHtml":false,"text":"",
         "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
         "links":null,"remoteContent":"allow","sanitizerVersion":"1"}
        """#)
        #expect(text.html == nil && text.htmlWithheld == nil && text.inlineParts == nil)
        #expect(text.remotePictures == nil && text.remotePictureCount == 0, "absent is 0")
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

    /// docs/api.md §4.4 (2026-10-01): `sentCount` on the summary (0 when an
    /// older daemon leaves it out), `withSent` on the request (left out
    /// when not asked for) and `sent` on the answer (empty when absent).
    @Test func threadSentReplies() throws {
        #expect(try decode(ThreadSummary.self, Self.threadJSON).sentCount == 0, "absent is 0")
        let withCount = Self.threadJSON.replacingOccurrences(of: #""hasAttachments":true"#, with: #""hasAttachments":true,"sentCount":2"#)
        let t = try decode(ThreadSummary.self, withCount)
        #expect(t.sentCount == 2 && t.messageCount == 3 && t.folderIds == ["f_inbox", "f_sent"])
        let roundTrip = try JSONCoding.decoder().decode(ThreadSummary.self, from: JSONCoding.encoder().encode(t))
        #expect(roundTrip == t)

        let r = try decode(ThreadGetResult.self, #"{"thread":\#(withCount),"messages":[\#(Self.summaryJSON)],"sent":[\#(Self.summaryJSON)]}"#)
        #expect(r.messages.count == 1 && r.sent.count == 1)
        #expect(try decode(ThreadGetResult.self, #"{"thread":\#(withCount),"messages":[]}"#).sent.isEmpty)

        let plain = String(decoding: try JSONCoding.encoder().encode(ThreadGetParams(accountId: "a", threadId: "t")), as: UTF8.self)
        #expect(!plain.contains("withSent"))
        let asked = String(
            decoding: try JSONCoding.encoder().encode(ThreadGetParams(accountId: "a", threadId: "t", folderId: "f", withSent: true)),
            as: UTF8.self)
        #expect(asked.contains(#""withSent":true"#))
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

    @Test func draftMarkdownExample() throws {
        let md = try decode(DraftMarkdownResult.self, #"{"markdown":true,"html":"<h1>Plan</h1>"}"#)
        #expect(md == DraftMarkdownResult(markdown: true, html: "<h1>Plan</h1>") && md.insertion == "<h1>Plan</h1>")
        let plain = try decode(DraftMarkdownResult.self, #"{"markdown":false}"#)
        #expect(plain.html == nil && plain.insertion == nil)
        #expect(DraftMarkdownResult(markdown: true, html: "").insertion == nil)
        #expect(DraftMarkdownResult(markdown: false, html: "<p>x</p>").insertion == nil)
        #expect(try encodeObject(DraftMarkdownParams(text: "# x")).keys.sorted() == ["text"])
        #expect(API.DraftMarkdown.name == "draft.markdown")
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

    /// SyncState.failedOutbox (docs/api.md §3, protocol 1 changelog): read
    /// when present, 0 from a daemon that predates it; always written.
    @Test func syncStateFailedOutbox() throws {
        let s = try decode(SyncState.self, #"{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":1,"failedOutbox":2}"#)
        #expect(s.pendingOutbox == 1 && s.failedOutbox == 2)
        let old = try decode(SyncState.self, #"{"accountId":"a","status":"offline","folderId":"f","progress":3,"lastSync":"2026-09-02T10:00:00Z","error":{"code":1301,"message":"x"},"pendingOutbox":0}"#)
        #expect(old == SyncState(accountId: "a", status: .offline, folderId: "f", progress: 3,
                                 lastSync: RFC3339.parse("2026-09-02T10:00:00Z"),
                                 error: RPCError(code: .networkError, message: "x"), pendingOutbox: 0, failedOutbox: 0))
        #expect(throws: (any Error).self) {
            try decode(SyncState.self, #"{"accountId":"a","status":"idle","progress":-1}"#)
        }
        let obj = try encodeObject(SyncState(accountId: "a", status: .idle))
        #expect(obj["failedOutbox"] as? Int == 0 && obj["pendingOutbox"] as? Int == 0)
        #expect(obj.keys.sorted() == ["accountId", "failedOutbox", "pendingOutbox", "progress", "status"])
        // A round trip keeps it.
        let back = try JSONCoding.decoder().decode(
            SyncState.self, from: JSONCoding.encoder().encode(SyncState(accountId: "a", status: .idle, failedOutbox: 4)))
        #expect(back.failedOutbox == 4)
    }

    @Test func configGetExample() throws {
        let r = try decode(ConfigGetResult.self, #"{"preferences":{"syncIntervalSeconds":300,"remoteContent":"knownSenders","offlineDays":30}}"#)
        #expect(r.preferences == Preferences(syncIntervalSeconds: 300, remoteContent: .knownSenders, offlineDays: 30))
        #expect(r.preferences.compressStore == nil && r.preferences.attachmentOfflineDays == nil, "an older daemon")
        // config.set echoes the whole set; what the daemon did not report
        // is left out, which it reads as unchanged.
        let obj = try encodeObject(ConfigSetParams(preferences: r.preferences))
        let prefs = try #require(obj["preferences"] as? [String: Any])
        #expect(prefs["syncIntervalSeconds"] as? Int == 300 && prefs["remoteContent"] as? String == "knownSenders" && prefs["offlineDays"] as? Int == 30)
        #expect(prefs.keys.sorted() == ["offlineDays", "remoteContent", "syncIntervalSeconds"], "nil is never sent")

        // docs/api.md §4.8: a daemon that knows the storage preferences.
        let full = try decode(ConfigGetResult.self, #"""
        {"preferences":{"syncIntervalSeconds":300,"remoteContent":"block","offlineDays":30,
                        "compressStore":true,"attachmentOfflineDays":30}}
        """#)
        #expect(full.preferences == Preferences(
            syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, compressStore: true, attachmentOfflineDays: 30))
        let small = try decode(ConfigSetResult.self, #"""
        {"preferences":{"syncIntervalSeconds":0,"remoteContent":"allow","offlineDays":0,"compressStore":false,"attachmentOfflineDays":-1}}
        """#)
        #expect(small.preferences.compressStore == false && small.preferences.attachmentOfflineDays == API.Limits.attachmentOfflineNone)
        // Set values are sent, false and 0 included.
        let off = try encodeObject(ConfigSetParams(preferences: Preferences(
            syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, compressStore: false, attachmentOfflineDays: 0)))
        let sent = try #require(off["preferences"] as? [String: Any])
        #expect(sent["compressStore"] as? Bool == false && sent["attachmentOfflineDays"] as? Int == 0)
        let one = try encodeObject(ConfigSetParams(preferences: Preferences(
            syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, compressStore: true)))
        let partial = try #require(one["preferences"] as? [String: Any])
        #expect(partial["compressStore"] as? Bool == true && partial["attachmentOfflineDays"] == nil)
        #expect(partial["neverStoreAttachments"] == nil)
        #expect(API.Limits.attachmentOfflineDaysMax == 3650 && API.Limits.largeAttachmentMinBytes == 100 << 10)

        // docs/api.md §4.8: neverStoreAttachments, added after the two; a
        // daemon without it (the two above) decodes it as nil.
        #expect(full.preferences.neverStoreAttachments == nil && small.preferences.neverStoreAttachments == nil)
        let never = try decode(ConfigGetResult.self, #"""
        {"preferences":{"syncIntervalSeconds":300,"remoteContent":"block","offlineDays":30,
                        "compressStore":true,"attachmentOfflineDays":30,"neverStoreAttachments":true}}
        """#)
        #expect(never.preferences == Preferences(
            syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, compressStore: true, attachmentOfflineDays: 30,
            neverStoreAttachments: true))
        let keep = try decode(ConfigSetResult.self, #"""
        {"preferences":{"syncIntervalSeconds":300,"remoteContent":"block","offlineDays":30,"compressStore":false,"attachmentOfflineDays":0,"neverStoreAttachments":false}}
        """#)
        #expect(keep.preferences.neverStoreAttachments == false)
        // false goes over the wire; nil never does.
        let offNever = try encodeObject(ConfigSetParams(preferences: keep.preferences))
        let sentNever = try #require(offNever["preferences"] as? [String: Any])
        #expect(sentNever["neverStoreAttachments"] as? Bool == false)
        #expect(sentNever.keys.sorted() == [
            "attachmentOfflineDays", "compressStore", "neverStoreAttachments", "offlineDays", "remoteContent", "syncIntervalSeconds",
        ])
        let onlyNever = try encodeObject(ConfigSetParams(preferences: Preferences(
            syncIntervalSeconds: 300, remoteContent: .block, offlineDays: 30, neverStoreAttachments: true)))
        let sentOnly = try #require(onlyNever["preferences"] as? [String: Any])
        #expect(sentOnly["neverStoreAttachments"] as? Bool == true)
        #expect(sentOnly["compressStore"] == nil && sentOnly["attachmentOfflineDays"] == nil)
    }

    /// docs/api.md §4.0 `system.storage`.
    @Test func systemStorageExample() throws {
        let r = try decode(SystemStorageResult.self, #"""
        {
          "totalBytes": 734003200,
          "databaseBytes": 44470272,
          "messageBytes": 546700000,
          "messageUncompressedBytes": 909800000,
          "savedBytes": 363100000,
          "attachmentBytes": 250000,
          "remoteAttachmentBytes": 312000000,
          "messages": 3725,
          "compressedMessages": 3725,
          "partialMessages": 410,
          "conversion": "idle"
        }
        """#)
        #expect(r == SystemStorageResult(
            totalBytes: 734_003_200, databaseBytes: 44_470_272, messageBytes: 546_700_000,
            messageUncompressedBytes: 909_800_000, savedBytes: 363_100_000, attachmentBytes: 250_000,
            remoteAttachmentBytes: 312_000_000, messages: 3725, compressedMessages: 3725, partialMessages: 410,
            conversion: .idle))
        let full = try decode(SystemStorageResult.self, #"{"totalBytes":0,"databaseBytes":0,"messageBytes":0,"messageUncompressedBytes":0,"savedBytes":0,"attachmentBytes":0,"remoteAttachmentBytes":0,"messages":0,"compressedMessages":0,"partialMessages":0,"conversion":"noSpace"}"#)
        #expect(full.conversion == .noSpace && full.conversion != .running)
        // A state a newer daemon adds still decodes.
        let odd = try decode(SystemStorageResult.self, #"{"totalBytes":1,"databaseBytes":1,"messageBytes":0,"messageUncompressedBytes":0,"savedBytes":0,"attachmentBytes":0,"remoteAttachmentBytes":0,"messages":0,"compressedMessages":0,"partialMessages":0,"conversion":"paused"}"#)
        #expect(odd.conversion == StorageConversion(rawValue: "paused"))
    }

    /// docs/api.md §4.3 `message.download`.
    @Test func messageDownloadExample() throws {
        let params = try encodeObject(MessageDownloadParams(accountId: "acc_1", messageId: "m_123"))
        #expect(params.keys.sorted() == ["accountId", "messageId"])
        #expect(params["accountId"] as? String == "acc_1" && params["messageId"] as? String == "m_123")
        let r = try decode(MessageDownloadResult.self, #"{"message":\#(Self.messageJSON)}"#)
        #expect(r.message.summary.id == "m_123" && r.message.attachments.count == 2)
        #expect(r.message.attachments.allSatisfy { !$0.isRemote }, "nothing is remote after a download")
    }

    /// docs/api.md `message.unsubscribe`, `MessageSummary.bulk`, `Message.unsubscribe`.
    @Test func messageUnsubscribeExample() throws {
        let params = try encodeObject(MessageUnsubscribeParams(accountId: "acc_1", messageId: "m_123"))
        #expect(params.keys.sorted() == ["accountId", "messageId"], "the client sends no URL or address")

        let done = try decode(MessageUnsubscribeResult.self, #"{"outcome":"unsubscribed","unsubscribedAt":"2026-09-30T12:00:00Z"}"#)
        #expect(done.outcome == .unsubscribed && done.unsubscribedAt == Date(timeIntervalSince1970: 1_790_769_600))
        #expect(done.url == nil && done.unverified == nil)
        let open = try decode(MessageUnsubscribeResult.self, #"{"outcome":"openUrl","url":"https://shop.example/u","unverified":true}"#)
        #expect(open.outcome == .openUrl && open.url == "https://shop.example/u" && open.unverified == true)
        #expect(try decode(MessageUnsubscribeResult.self, #"{"outcome":"queued"}"#).outcome == .queued)
        // An outcome a newer daemon adds still decodes.
        #expect(try decode(MessageUnsubscribeResult.self, #"{"outcome":"later"}"#).outcome == UnsubscribeOutcome(rawValue: "later"))

        // The summary's bulk info and the message's offer, flat in the message.
        var json = Self.messageJSON
        json.removeLast()
        json += #","bulk":{"kind":"newsletter","listId":"news.shop.example","domain":"shop.example"},"unsubscribe":{"method":"oneClick","target":"shop.example","unsubscribedAt":"2026-09-30T12:00:00Z"}}"#
        let m = try decode(Message.self, json)
        #expect(m.summary.bulk == BulkInfo(kind: .newsletter, listId: "news.shop.example", domain: "shop.example"))
        #expect(m.unsubscribe?.method == .oneClick && m.unsubscribe?.target == "shop.example")
        #expect(m.unsubscribe?.url == nil && m.unsubscribe?.unsubscribedAt != nil)
        let again = try JSONCoding.decoder().decode(Message.self, from: JSONCoding.encoder().encode(m))
        #expect(again == m)
        // Absent means personal mail, no offer.
        let plain = try decode(Message.self, Self.messageJSON)
        #expect(plain.summary.bulk == nil && plain.unsubscribe == nil)
        let obj = try #require(JSONSerialization.jsonObject(with: JSONCoding.encoder().encode(plain)) as? [String: Any])
        #expect(obj["bulk"] == nil && obj["unsubscribe"] == nil)
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

    // MARK: Jira accounts (docs/api.md §3, §4.1–§4.5, §5; protocol 2, compatible addition)

    static let jiraConfigJSON = #"""
    {"siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
     "login":"jana.dvorakova@acme.example","spaces":[{"id":"10001","key":"ITSD","name":"IT Service Desk"},{"id":"10002","key":"WEB"}],
     "offlineDays":90,"onlyMine":true,"hideEvents":true,"disabledFolders":["watching"],
     "closedStatuses":[{"id":"6","name":"Closed"},{"id":"10005"}],"notificationMail":"hide",
     "notificationSenders":["jira@acme.atlassian.net","@acme.example"],"botNames":["Relay Bot"],
     "metadataFilters":["^Sent from .*$"],"authorPrefixes":["[EXT]"]}
    """#

    /// Every JiraConfig field survives a decode and an encode (an
    /// account.update built from account.list must not drop one), and
    /// nothing unset is written.
    @Test func jiraAccountConfigRoundTrips() throws {
        let r = try decode(AccountListResult.self, #"""
        {"accounts":[{"id":"acc_j","config":{"name":"Acme Jira","email":"jana.dvorakova@acme.example","kind":"jira",
           "jira":\#(Self.jiraConfigJSON),"syncIntervalSeconds":300},
          "enabled":true,"state":{"accountId":"acc_j","status":"idle","progress":-1,"pendingOutbox":0},
          "capabilities":[]}]}
        """#)
        let acc = try #require(r.accounts.first)
        #expect(acc.config.protocolKind == .jira && acc.config.kind == .jira)
        #expect(acc.config.imap == nil && acc.config.smtp == nil && acc.config.graph == nil && acc.config.oauth2 == nil)
        let j = try #require(acc.config.jira)
        #expect(j == JiraConfig(
            siteUrl: "https://acme.atlassian.net", deployment: .cloud, cloudId: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
            login: "jana.dvorakova@acme.example",
            spaces: [SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk"), SpaceRef(id: "10002", key: "WEB")],
            offlineDays: 90, onlyMine: true, hideEvents: true, disabledFolders: [.watching],
            closedStatuses: [StatusRef(id: "6", name: "Closed"), StatusRef(id: "10005")], notificationMail: .hide,
            notificationSenders: ["jira@acme.atlassian.net", "@acme.example"], botNames: ["Relay Bot"],
            metadataFilters: ["^Sent from .*$"], authorPrefixes: ["[EXT]"]))

        // account.update echoes it verbatim, key for key.
        let update = try encodeObject(AccountUpdateParams(accountId: acc.id, config: acc.config))
        let cfg = try #require(update["config"] as? [String: Any])
        #expect(cfg.keys.sorted() == ["email", "jira", "kind", "name", "syncIntervalSeconds"])
        let jira = try #require(cfg["jira"] as? [String: Any])
        let original = try #require(JSONSerialization.jsonObject(with: json(Self.jiraConfigJSON)) as? [String: Any])
        #expect(jira.keys.sorted() == original.keys.sorted(), "every field is written back")
        #expect(NSDictionary(dictionary: jira).isEqual(to: original), "and with the same values")
        let back = try JSONCoding.decoder().decode(AccountConfig.self, from: JSONCoding.encoder().encode(acc.config))
        #expect(back == acc.config)

        // A minimal block: the three fields Go writes without omitempty, nothing else.
        let minimal = JiraConfig(siteUrl: "https://jira.acme.example/jira", deployment: .datacenter, spaces: [SpaceRef(id: "1", key: "MOB")])
        let obj = try encodeObject(minimal)
        #expect(obj.keys.sorted() == ["deployment", "siteUrl", "spaces"], "nil and empty lists are left out")
        #expect(obj["deployment"] as? String == "datacenter")
        let space = try #require((obj["spaces"] as? [[String: Any]])?.first)
        #expect(space.keys.sorted() == ["id", "key"])
        #expect(try encodeObject(JiraConfig(siteUrl: "https://a.example", deployment: .cloud))["spaces"] as? [Any] != nil,
                "spaces is always written")
        // Set values are written, false and 0 included.
        let off = try encodeObject(JiraConfig(siteUrl: "https://a.example", deployment: .cloud, offlineDays: 0, onlyMine: false))
        #expect(off["offlineDays"] as? Int == 0 && off["onlyMine"] as? Bool == false && off["hideEvents"] == nil)

        // What an older or terser daemon leaves out decodes as the default.
        let bare = try decode(JiraConfig.self, #"{"siteUrl":"https://a.example","deployment":"cloud","spaces":null}"#)
        #expect(bare == JiraConfig(siteUrl: "https://a.example", deployment: .cloud))
        #expect(bare.cloudId == nil && bare.offlineDays == nil && bare.notificationMail == nil && bare.botNames.isEmpty)
        #expect(try decode(JiraConfig.self, #"{"siteUrl":"https://a.example","deployment":"server"}"#).deployment == "server",
                "an unknown deployment decodes as itself")

        #expect(API.Limits.maxJiraSpaces == 200 && API.Limits.maxJiraStatuses == 64)
        #expect(API.Limits.maxJiraListEntries == 32 && API.Limits.maxJiraPatternBytes == 512)
        #expect(API.Limits.maxJiraOfflineDays == 365 && API.Limits.defaultJiraOfflineDays == 30)
    }

    /// Account.capabilities: absent (an older daemon) is nil and reads as
    /// the mail set; an empty list (a Jira account) can do none of them.
    @Test func accountCapabilities() throws {
        let state = #"{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0}"#
        let old = try decode(Account.self, #"{"id":"a","config":{"name":"n","email":"e@x"},"enabled":true,"state":\#(state)}"#)
        #expect(old.capabilities == nil, "missing is nil, not []")
        let null = try decode(Account.self, #"{"id":"a","config":{"name":"n","email":"e@x"},"enabled":true,"state":\#(state),"capabilities":null}"#)
        #expect(null.capabilities == nil)
        for c: Capability in [.compose, .reply, .replyAll, .forward, .move, .delete] {
            #expect(old.can(c), "\(c) is a mail capability")
        }
        #expect(!old.can(.comment) && !old.can("archive"))
        #expect(API.mailCapabilities == [.compose, .reply, .replyAll, .forward, .move, .delete])

        let mail = try decode(Account.self, #"""
        {"id":"a","config":{"name":"n","email":"e@x"},"enabled":true,"state":\#(state),
         "capabilities":["compose","reply","replyAll","forward","move","delete"]}
        """#)
        #expect(mail.capabilities == API.mailCapabilities && mail.can(.delete) && !mail.can(.comment))

        let m1 = try decode(Account.self, #"{"id":"j","config":{"name":"n","email":"e@x","kind":"jira"},"enabled":true,"state":\#(state),"capabilities":[]}"#)
        #expect(m1.capabilities == [])
        for c: Capability in [.compose, .reply, .replyAll, .forward, .comment, .move, .delete] {
            #expect(!m1.can(c), "\(c) is not a capability of an empty list")
        }
        let m2 = try decode(Account.self, #"{"id":"j","config":{"name":"n","email":"e@x","kind":"jira"},"enabled":true,"state":\#(state),"capabilities":["comment","forward","teleport"]}"#)
        #expect(m2.can(.comment) && m2.can(.forward) && !m2.can(.reply) && !m2.can(.compose))
        #expect(m2.capabilities?.last == "teleport", "an unknown capability decodes as itself")

        // Written only when known; an empty list stays an empty list.
        #expect(try encodeObject(old)["capabilities"] == nil)
        #expect((try encodeObject(m1)["capabilities"] as? [Any])?.isEmpty == true)
        #expect(try JSONCoding.decoder().decode(Account.self, from: JSONCoding.encoder().encode(m1)).capabilities == [])
        #expect(Account(id: "a", config: AccountConfig(name: "n", email: "e@x"), enabled: true,
                        state: SyncState(accountId: "a", status: .idle)).can(.forward))
    }

    static let issueJSON = #"""
    "key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer on 3rd floor jams",
    "status":"In Progress","statusCategory":"inProgress","type":"Service Request","priority":"High",
    "assignee":"Jana Dvořáková","reporter":"Petr Novák","assignedToMe":true,"watching":true,
    "commentVisibilities":["public","internal"]
    """#

    static let issueSummaryJSON = #"""
    {"id":"m_j1","accountId":"acc_j","folderId":"f_itsd","threadId":"t_j",
     "from":[{"name":"Petr Novák","address":"petr.novak@acme.example"}],
     "subject":"ITSD-42: Printer on 3rd floor jams","date":"2026-09-02T10:00:00Z","snippet":"Still jams",
     "flags":["seen"],"hasAttachments":false,"size":321,
     "issue":{\#(issueJSON),"item":"comment","visibility":"internal","via":"Relay Bot","edited":true}}
    """#

    static let eventSummaryJSON = #"""
    {"id":"m_j2","accountId":"acc_j","folderId":"f_itsd","threadId":"t_j",
     "from":[{"name":"Jana Dvořáková","address":"jana.dvorakova@acme.example"}],
     "subject":"ITSD-42: Printer on 3rd floor jams","date":"2026-09-02T11:00:00Z","snippet":"To Do → In Progress",
     "flags":["seen"],"hasAttachments":false,"size":0,
     "issue":{"key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer on 3rd floor jams",
              "status":"In Progress","item":"event",
              "changes":[{"field":"status","from":"To Do","to":"In Progress"},{"field":"assignee","to":"Jana Dvořáková"}]}}
    """#

    /// MessageSummary.issue flattens IssueInfo beside the item fields, on
    /// message.list, message.get (itself flattened) and thread members;
    /// ThreadSummary.issue is the plain IssueInfo.
    @Test func issueDecodesOnMessagesAndThreads() throws {
        let list = try decode(MessageListResult.self, #"{"messages":[\#(Self.issueSummaryJSON),\#(Self.summaryJSON)],"page":{"total":2}}"#)
        let issue = try #require(list.messages[0].issue)
        #expect(issue.info == IssueInfo(
            key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "Printer on 3rd floor jams",
            status: "In Progress", statusCategory: .inProgress, type: "Service Request", priority: "High",
            assignee: "Jana Dvořáková", reporter: "Petr Novák", assignedToMe: true, watching: true,
            commentVisibilities: [.public, .internal]))
        #expect(issue.item == .comment && issue.visibility == .internal && issue.via == "Relay Bot" && issue.edited == true)
        #expect(issue.changes.isEmpty)
        #expect(issue.mine == nil, "a relayed comment is never the user's own")
        #expect(list.messages[1].issue == nil, "a mail message has none")

        let get = try decode(MessageGetResult.self, #"""
        {"message":{"id":"m_j1","accountId":"acc_j","folderId":"f_itsd","threadId":"t_j",
          "from":[{"name":"Petr Novák","address":"petr.novak@acme.example"}],
          "subject":"ITSD-42: Printer on 3rd floor jams","date":"2026-09-02T10:00:00Z","snippet":"Still jams",
          "flags":[],"hasAttachments":false,"size":321,
          "issue":{\#(Self.issueJSON),"item":"description","mine":true},
          "attachments":[]}}
        """#)
        let m = get.message
        #expect(m.summary.issue?.item == .description && m.summary.issue?.info.key == "ITSD-42")
        #expect(m.summary.issue?.visibility == nil && m.summary.issue?.via == nil && m.summary.issue?.edited == nil)
        #expect(m.summary.issue?.mine == true, "the account's own user wrote it")

        // The encoding flattens too: no "info" and no "summary" key.
        let obj = try encodeObject(m)
        let wire = try #require(obj["issue"] as? [String: Any])
        #expect(obj["summary"] == nil && wire["info"] == nil)
        #expect(wire["key"] as? String == "ITSD-42" && wire["item"] as? String == "description")
        #expect(wire["visibility"] == nil && wire["changes"] == nil, "omitempty")
        #expect(wire["mine"] as? Bool == true)
        #expect(try JSONCoding.decoder().decode(Message.self, from: JSONCoding.encoder().encode(m)) == m)
        // mine is written only when known (omitempty).
        let relayed = try #require(try encodeObject(list.messages[0])["issue"] as? [String: Any])
        #expect(relayed["mine"] == nil && relayed["via"] as? String == "Relay Bot")
        #expect(try decode(MessageIssue.self, #"{"key":"WEB-1","url":"u","summary":"s","status":"Open","item":"comment","mine":false}"#).mine == false)

        // An event row: its changes, no visibility.
        let event = try decode(MessageSummary.self, Self.eventSummaryJSON)
        let e = try #require(event.issue)
        #expect(e.item == .event && e.visibility == nil)
        #expect(e.changes == [IssueChange(field: .status, from: "To Do", to: "In Progress"), IssueChange(field: .assignee, to: "Jana Dvořáková")])
        #expect(e.info.statusCategory == nil && e.info.assignee == nil && e.info.commentVisibilities.isEmpty)
        #expect(try JSONCoding.decoder().decode(MessageSummary.self, from: JSONCoding.encoder().encode(event)) == event)

        // thread.list: the thread carries the issue, its latest member (an event here) its own.
        let threads = try decode(ThreadListResult.self, #"""
        {"threads":[{"id":"t_j","accountId":"acc_j","subject":"ITSD-42: Printer on 3rd floor jams",
          "participants":[{"name":"Jana Dvořáková","address":"jana.dvorakova@acme.example"}],
          "messageCount":3,"unreadCount":0,"latestDate":"2026-09-02T11:00:00Z",
          "latest":\#(Self.eventSummaryJSON),"snippet":"To Do → In Progress","flags":["seen"],"hasAttachments":false,
          "folderIds":["f_itsd","f_assigned"],"issue":{\#(Self.issueJSON)}},
          \#(Self.threadJSON)],"page":{"total":2}}
        """#)
        let t = threads.threads[0]
        #expect(t.issue?.key == "ITSD-42" && t.issue?.commentVisibilities == [.public, .internal])
        #expect(t.latest.issue?.item == .event && t.latest.issue?.changes.count == 2)
        #expect(threads.threads[1].issue == nil)
        let get2 = try decode(ThreadGetResult.self, #"{"thread":\#(Self.threadJSON),"messages":[\#(Self.issueSummaryJSON),\#(Self.eventSummaryJSON)]}"#)
        #expect(get2.messages.map { $0.issue?.item } == [.comment, .event])
        #expect(try encodeObject(t)["issue"] as? [String: Any] != nil)
        #expect(try encodeObject(threads.threads[1])["issue"] == nil)

        // search results and notify.newMessage carry MessageSummary as it is.
        let search = try decode(SearchQueryResult.self, #"{"results":[{"message":\#(Self.issueSummaryJSON),"snippet":"x","ranges":[],"score":1}],"page":{"total":1}}"#)
        #expect(search.results.first?.message.issue?.info.key == "ITSD-42")
    }

    /// Open enums of the issue projection: a newer daemon's value decodes
    /// as itself; the empty category is not a known one.
    @Test func unknownIssueValuesDecode() throws {
        let i = try decode(IssueInfo.self, #"{"key":"WEB-1","url":"https://acme.atlassian.net/browse/WEB-1","summary":"s","status":"Blocked","statusCategory":"blocked"}"#)
        #expect(i.statusCategory == IssueStatusCategory("blocked"))
        #expect(i.statusCategory != .todo && i.statusCategory != .inProgress && i.statusCategory != .done)
        let empty = try decode(IssueInfo.self, #"{"key":"WEB-1","url":"u","summary":"s","status":"","statusCategory":""}"#)
        #expect(empty.statusCategory == "" && empty.status == "")
        let odd = try decode(MessageIssue.self, #"""
        {"key":"MOB-7","url":"u","summary":"s","status":"Open","item":"worklog","visibility":"partners",
         "changes":[{"field":"priority","from":"Low","to":"High"}]}
        """#)
        #expect(odd.item == "worklog" && odd.visibility == "partners" && odd.changes.first?.field == "priority")
        #expect(throws: (any Error).self, "item is required") {
            try decode(MessageIssue.self, #"{"key":"MOB-7","url":"u","summary":"s","status":"Open"}"#)
        }
        #expect(CommentVisibility.public.rawValue == "public" && CommentVisibility.internal.rawValue == "internal")
        #expect(IssueItemKind.description.rawValue == "description" && IssueItemKind.event.description == "event")
        #expect(VirtualFolder.open.rawValue == "open" && NotificationMailMode.sync.rawValue == "sync")
    }

    /// Folder.virtual (docs/api.md §4.2): the virtual folders of a jira
    /// account first, role none; absent on every other folder.
    @Test func virtualFoldersDecode() throws {
        let r = try decode(FolderListResult.self, #"""
        {"folders":[
          {"id":"f_a","accountId":"acc_j","name":"Assigned to Me","path":"Assigned to Me","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":2,"total":7,"virtual":"assignedToMe"},
          {"id":"f_w","accountId":"acc_j","name":"Watching","path":"Watching","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":3,"virtual":"watching"},
          {"id":"f_o","accountId":"acc_j","name":"Open","path":"Open","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":2,"total":9,"virtual":"open"},
          {"id":"f_x","accountId":"acc_j","name":"Later","path":"Later","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0,"virtual":"starred"},
          {"id":"f_itsd","accountId":"acc_j","name":"IT Service Desk","path":"IT Service Desk","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":1,"total":40}
        ]}
        """#)
        #expect(r.folders.map(\.virtual) == [.assignedToMe, .watching, .open, VirtualFolder("starred"), nil])
        #expect(r.folders.allSatisfy { $0.role == .none })
        #expect(try encodeObject(r.folders[0])["virtual"] as? String == "assignedToMe")
        #expect(try encodeObject(r.folders[4])["virtual"] == nil)
        let plain = Folder(id: "f", accountId: "a", name: "n", path: "n", role: .inbox, subscribed: true, selectable: true,
                           synced: true, unread: 0, total: 0)
        #expect(plain.virtual == nil)
    }

    /// Comment drafts (docs/api.md §4.5): Draft.comment comes with a
    /// draft.create reply on a jira account and goes back in draft.save;
    /// DraftCreateParams.messageAccountId only when set.
    @Test func commentDraftsAndCrossAccountForward() throws {
        let r = try decode(DraftCreateResult.self, #"""
        {"draft":{"accountId":"acc_j","version":0,"to":[],"subject":"ITSD-42: Printer on 3rd floor jams","textBody":"",
                  "inReplyTo":"m_j1","updatedAt":"0001-01-01T00:00:00Z",
                  "comment":{"issue":{\#(Self.issueJSON)},"visibility":""}},
         "quoted":"none",
         "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0}}
        """#)
        let comment = try #require(r.draft.comment)
        #expect(comment.issue.key == "ITSD-42" && comment.issue.commentVisibilities == [.public, .internal])
        #expect(comment.visibility == "", "empty is public")
        #expect(r.quoted == .none && r.draft.to.isEmpty)

        var d = r.draft
        d.comment?.visibility = .internal
        let obj = try encodeObject(DraftSaveParams(draft: d))
        let draft = try #require(obj["draft"] as? [String: Any])
        let sent = try #require(draft["comment"] as? [String: Any])
        #expect(sent["visibility"] as? String == "internal")
        #expect((sent["issue"] as? [String: Any])?["key"] as? String == "ITSD-42")
        #expect(try JSONCoding.decoder().decode(Draft.self, from: JSONCoding.encoder().encode(d)) == d)
        // A mail draft has none, and does not write one.
        let mail = try encodeObject(DraftSaveParams(draft: Draft(accountId: "a")))
        #expect((mail["draft"] as? [String: Any])?["comment"] == nil)
        #expect(try decode(Draft.self, #"{"accountId":"a","version":1,"to":[],"subject":"s","textBody":"t","updatedAt":"2026-09-02T10:00:00Z"}"#).comment == nil)
        #expect(DraftComment(issue: comment.issue).visibility == "")

        // Forward of a jira message from a mail account.
        let fwd = try encodeObject(DraftCreateParams(accountId: "acc_mail", mode: .forward, messageId: "m_j1",
                                                     attribution: "---", messageAccountId: "acc_j"))
        #expect(fwd["messageAccountId"] as? String == "acc_j" && fwd["accountId"] as? String == "acc_mail")
        let reply = try encodeObject(DraftCreateParams(accountId: "acc_j", mode: .reply, messageId: "m_j1"))
        #expect(reply.keys.sorted() == ["accountId", "messageId", "mode"], "messageAccountId is left out when nil")
        let back = try decode(DraftCreateParams.self, #"{"accountId":"a","mode":"forward","messageId":"m","messageAccountId":"j"}"#)
        #expect(back.messageAccountId == "j")
    }

    /// account.detectSite, account.listSpaces and the jira probe of
    /// account.test (docs/api.md §4.1).
    @Test func jiraAccountMethods() throws {
        #expect(try encodeObject(AccountDetectSiteParams(url: "acme.atlassian.net")) as NSDictionary == ["url": "acme.atlassian.net"])
        let cloud = try decode(AccountDetectSiteResult.self, #"""
        {"kind":"jira","siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","title":"Acme","version":"1001.0.0-SNAPSHOT"}
        """#)
        #expect(cloud == AccountDetectSiteResult(
            kind: .jira, siteUrl: "https://acme.atlassian.net", deployment: .cloud,
            cloudId: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", title: "Acme", version: "1001.0.0-SNAPSHOT"))
        let dc = try decode(AccountDetectSiteResult.self, #"{"kind":"jira","siteUrl":"https://jira.acme.example/jira","deployment":"datacenter"}"#)
        #expect(dc.deployment == .datacenter && dc.cloudId == nil && dc.title == nil && dc.version == nil)

        let config = AccountConfig(name: "", email: "jana.dvorakova@acme.example", kind: .jira,
                                   jira: JiraConfig(siteUrl: cloud.siteUrl, deployment: .cloud, cloudId: cloud.cloudId,
                                                    login: "jana.dvorakova@acme.example"))
        let params = try encodeObject(AccountListSpacesParams(config: config, credentials: Credentials(password: "token"), counts: true))
        #expect(params.keys.sorted() == ["config", "counts", "credentials"], "accountId is left out when nil")
        #expect(params["counts"] as? Bool == true)
        #expect((params["credentials"] as? [String: Any])?["password"] as? String == "token")
        let jira = try #require((params["config"] as? [String: Any])?["jira"] as? [String: Any])
        #expect(jira.keys.sorted() == ["cloudId", "deployment", "login", "siteUrl", "spaces"])
        #expect((jira["spaces"] as? [Any])?.isEmpty == true)
        let editing = try encodeObject(AccountListSpacesParams(accountId: "acc_j", config: config))
        #expect(editing["accountId"] as? String == "acc_j" && editing["counts"] == nil)
        #expect((editing["credentials"] as? [String: Any])?.isEmpty == true, "no password: the stored token")

        let spaces = try decode(AccountListSpacesResult.self, #"""
        {"user":{"name":"Jana Dvořáková","email":"jana.dvorakova@acme.example"},
         "spaces":[{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":120},
                   {"id":"10002","key":"WEB","name":"Website","issues":-1}],
         "statuses":[{"id":"1","name":"To Do","category":"todo"},{"id":"6","name":"Closed","category":"done"},{"id":"9","name":"Odd","category":""}]}
        """#)
        #expect(spaces.user == SiteUser(name: "Jana Dvořáková", email: "jana.dvorakova@acme.example"))
        #expect(spaces.spaces == [
            Space(id: "10001", key: "ITSD", name: "IT Service Desk", serviceDesk: true, issues: 120),
            Space(id: "10002", key: "WEB", name: "Website", issues: -1),
        ])
        #expect(spaces.statuses.map(\.category) == [.todo, .done, ""])
        let none = try decode(AccountListSpacesResult.self, #"{"user":{"name":"jdvorakova"},"spaces":null,"statuses":null}"#)
        #expect(none.user.email == nil && none.spaces.isEmpty && none.statuses.isEmpty)

        let test = try decode(AccountTestResult.self, #"{"jira":{"ok":true,"capabilities":["cloud","gateway"],"latencyMs":210}}"#)
        #expect(test.jira == EndpointTestResult(ok: true, capabilities: ["cloud", "gateway"], latencyMs: 210))
        #expect(test.imap == nil && test.smtp == nil && test.graph == nil)
        #expect(try encodeObject(AccountTestResult(imap: EndpointTestResult(ok: true, latencyMs: 1)))["jira"] == nil)
    }

    /// issue.transitions and issue.transition (docs/api.md §4.12): the
    /// params encode with the JSON names of pkg/api, the
    /// results decode, `needsInput` and `toCategory` are optional on the
    /// wire, and the transition capability is one more `Capability`.
    @Test func issueTransitionMethods() throws {
        let list = try encodeObject(IssueTransitionsParams(accountId: "acc_j", messageId: "m_j1"))
        #expect(list as NSDictionary == ["accountId": "acc_j", "messageId": "m_j1"])
        let perform = try encodeObject(IssueTransitionParams(accountId: "acc_j", messageId: "m_j1", transitionId: "31"))
        #expect(perform as NSDictionary == ["accountId": "acc_j", "messageId": "m_j1", "transitionId": "31"])

        let r = try decode(IssueTransitionsResult.self, #"""
        {"issue":{\#(Self.issueJSON)},
         "transitions":[{"id":"11","name":"Start Progress","to":"In Progress","toCategory":"inProgress"},
                        {"id":"21","name":"Done","to":"Done","toCategory":"done","needsInput":true},
                        {"id":"41","name":"Escalate","to":"Escalated","toCategory":"blocked"}]}
        """#)
        #expect(r.issue.key == "ITSD-42" && r.issue.status == "In Progress" && r.issue.commentVisibilities == [.public, .internal])
        #expect(r.transitions == [
            IssueTransition(id: "11", name: "Start Progress", to: "In Progress", toCategory: .inProgress),
            IssueTransition(id: "21", name: "Done", to: "Done", toCategory: .done, needsInput: true),
            IssueTransition(id: "41", name: "Escalate", to: "Escalated", toCategory: "blocked"),
        ])
        #expect(r.transitions[0].needsInput == nil && r.transitions[2].toCategory == "blocked", "an unknown category decodes as itself")
        let none = try decode(IssueTransitionsResult.self, #"{"issue":{\#(Self.issueJSON)},"transitions":null}"#)
        #expect(none.transitions.isEmpty, "never null on the wire, but an old fixture may say so")
        let done = try decode(IssueTransitionResult.self, #"{"issue":{"key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer","status":"Done","statusCategory":"done"}}"#)
        #expect(done.issue.status == "Done" && done.issue.statusCategory == .done && done.issue.assignee == nil)

        let state = #"{"accountId":"j","status":"idle","progress":-1,"pendingOutbox":0}"#
        let acc = try decode(Account.self, #"{"id":"j","config":{"name":"n","email":"e@x","kind":"jira"},"enabled":true,"state":\#(state),"capabilities":["comment","forward","transition"]}"#)
        #expect(acc.can(.transition) && Capability.transition == "transition")
        #expect(!API.mailCapabilities.contains(.transition), "mail accounts never change statuses")
        #expect(API.Limits.maxIssueTransitions == 100)
    }

    /// notify.messagesChanged (docs/api.md §5): the payload decodes, also
    /// as a notification.
    @Test func messagesChangedNotification() throws {
        let n = try decode(MessagesChangedNotification.self, #"{"accountId":"acc_mail","folderIds":["f_inbox"]}"#)
        #expect(n == MessagesChangedNotification(accountId: "acc_mail", folderIds: ["f_inbox"]))
        #expect(try decode(MessagesChangedNotification.self, #"{"accountId":"acc_mail"}"#).folderIds.isEmpty)
        let raw = RPCNotification(
            method: API.Notify.messagesChanged,
            line: json(#"{"jsonrpc":"2.0","method":"notify.messagesChanged","params":{"accountId":"acc_mail","folderIds":["f_inbox"]}}"#))
        #expect(try raw.params(MessagesChangedNotification.self) == n)
        #expect(try DaemonNotification(raw) == .messagesChanged(n))
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

    /// ServerConfig.certificateSha256 survives a decode and an encode (an
    /// edit must never drop the pin) and is left out when unset.
    @Test func serverConfigCarriesTheCertificatePin() throws {
        let pin = String(repeating: "ab", count: 32)
        let sc = try decode(ServerConfig.self, #"{"host":"100.64.0.1","port":1143,"security":"starttls","username":"me","authMethod":"password","certificateSha256":"\#(pin)"}"#)
        #expect(sc.certificateSha256 == pin)
        #expect(try encodeObject(sc)["certificateSha256"] as? String == pin)
        let update = try encodeObject(AccountUpdateParams(accountId: "a", config: AccountConfig(name: "B", email: "me@x.org", imap: sc)))
        let imap = (update["config"] as? [String: Any])?["imap"] as? [String: Any]
        #expect(imap?["certificateSha256"] as? String == pin)
        let plain = try decode(ServerConfig.self, #"{"host":"h","port":993,"security":"tls","username":"u","authMethod":"password"}"#)
        let encoded = try encodeObject(plain)
        #expect(plain.certificateSha256 == nil && encoded["certificateSha256"] == nil)
    }

    /// docs/api.md §2: the data of a tlsError from an IMAP/SMTP endpoint,
    /// in an account.test result and in a SyncState.
    @Test func tlsErrorDataExample() throws {
        let sum = String(repeating: "0f", count: 32)
        let r = try decode(AccountTestResult.self, #"""
        {"imap":{"ok":false,"latencyMs":40,"error":{"code":1303,"message":"x509: certificate is not standards compliant",
          "data":{"reason":"other","certificate":{"sha256":"\#(sum)","subject":"127.0.0.1","issuer":"127.0.0.1",
            "ipAddresses":["127.0.0.1"],"notBefore":"2024-01-02T03:04:05Z","notAfter":"2044-01-02T03:04:05Z","selfSigned":true}}}},
         "smtp":{"ok":false,"latencyMs":1,"error":{"code":1303,"message":"handshake","data":{"reason":"handshake"}}}}
        """#)
        let d = try #require(tlsErrorData(r.imap?.error))
        #expect(d.reason == .other && d.expectedSha256 == nil)
        let c = try #require(d.certificate)
        #expect(c.sha256 == sum && c.subject == "127.0.0.1" && c.ipAddresses == ["127.0.0.1"] && c.dnsNames.isEmpty && c.selfSigned)
        #expect(c.notBefore == RFC3339.parse("2024-01-02T03:04:05Z") && c.notAfter == RFC3339.parse("2044-01-02T03:04:05Z"))
        #expect(tlsErrorData(r.smtp?.error) == TLSErrorData(reason: .handshake))

        let state = try decode(SyncState.self, #"""
        {"accountId":"acc_1","status":"offline","progress":-1,"pendingOutbox":0,
         "error":{"code":1303,"message":"pinned certificate mismatch","data":{"reason":"pinMismatch","expectedSha256":"\#(sum)",
           "certificate":{"sha256":"\#(String(repeating: "ab", count: 32))","notBefore":"2026-01-01T00:00:00Z","notAfter":"2027-01-01T00:00:00Z","selfSigned":false}}}}
        """#)
        let p = try #require(tlsErrorData(state.error))
        #expect(p.reason == .pinMismatch && p.expectedSha256 == sum && p.certificate?.subject == nil)

        // Not a tlsError, no data, or data of another shape: nothing.
        #expect(tlsErrorData(RPCError(code: .networkError, message: "x", data: state.error?.data)) == nil)
        #expect(tlsErrorData(RPCError(code: .tlsError, message: "x")) == nil)
        #expect(tlsErrorData(RPCError(code: .tlsError, message: "x", data: .array([]))) == nil)
        #expect(tlsErrorData(nil) == nil)
        // A reason of a newer daemon decodes as itself.
        let newer = try decode(RPCError.self, #"{"code":1303,"message":"x","data":{"reason":"quantum"}}"#)
        #expect(tlsErrorData(newer)?.reason == TLSErrorReason("quantum"))
    }

    @Test func certificateFingerprintsNormalize() {
        let sum = String(repeating: "ab", count: 32)
        #expect(normalizeCertificateSHA256(sum) == sum)
        #expect(normalizeCertificateSHA256(sum.uppercased()) == sum)
        #expect(normalizeCertificateSHA256(stride(from: 0, to: 64, by: 2).map { _ in "AB" }.joined(separator: ":")) == sum)
        #expect(normalizeCertificateSHA256(stride(from: 0, to: 64, by: 2).map { _ in "ab" }.joined(separator: " ")) == sum)
        for bad in ["", "abc", sum + "0", String(repeating: "g", count: 64), String(sum.dropLast()) + "\u{0660}", "-" + sum] {
            #expect(normalizeCertificateSHA256(bad) == nil, "\(bad)")
        }
    }

    /// api.ErrorCode, copied from backend/pkg/api/errors.go in its order.
    static let goCodes: [(Int, String)] = [
        (-32700, "parseError"), (-32600, "invalidRequest"), (-32601, "methodNotFound"), (-32602, "invalidParams"),
        (-32603, "internalError"),
        (1000, "notImplemented"), (1001, "invalidArgument"), (1002, "conflict"), (1003, "cancelled"), (1004, "unavailable"),
        (1005, "unauthenticated"),
        (1100, "accountNotFound"), (1101, "folderNotFound"), (1102, "messageNotFound"), (1103, "threadNotFound"),
        (1104, "draftNotFound"), (1105, "attachmentNotFound"),
        (1200, "authRequired"), (1201, "authFailed"), (1202, "keyringError"), (1203, "oauthClientMissing"),
        (1300, "offline"), (1301, "networkError"), (1302, "serverError"), (1303, "tlsError"), (1304, "serverTimeout"),
        (1305, "messageGone"),
        (1400, "storageError"), (1401, "migrationFailed"),
        (1500, "malformedMessage"), (1501, "sanitizeFailed"), (1502, "attachmentTooBig"), (1503, "partNotFound"),
        (1504, "partNotDownloaded"), (1505, "unsubscribeFailed"),
    ]

    @Test func errorCodesAreNamed() {
        #expect(ErrorCode.all.count == 35 && Set(ErrorCode.all).count == 35)
        #expect(ErrorCode.all.map(\.rawValue) == Self.goCodes.map { $0.0 })
        #expect(ErrorCode.all.map(\.name) == Self.goCodes.map { $0.1 })
        for code in ErrorCode.all {
            #expect(!code.name.hasPrefix("unknown"), "\(code.rawValue) has no name")
        }
        #expect(ErrorCode.unauthenticated.rawValue == 1005 && ErrorCode.unauthenticated.name == "unauthenticated")
        #expect(ErrorCode(rawValue: 1005) == .unauthenticated)
        #expect(ErrorCode.attachmentTooBig.name == "attachmentTooBig" && ErrorCode.attachmentTooBig.rawValue == 1502)
        #expect(ErrorCode.parseError.rawValue == -32700 && ErrorCode.parseError.name == "parseError")
        #expect(ErrorCode(rawValue: 1234).name == "unknown(1234)")
        #expect("\(ErrorCode.keyringError)" == "keyringError")
        #expect(ErrorCode.oauthClientMissing.rawValue == 1203 && ErrorCode.oauthClientMissing.name == "oauthClientMissing")
        #expect(ErrorCode(rawValue: 1203) == .oauthClientMissing)
        let code: ErrorCode = 1102
        #expect(code == .messageNotFound)
        #expect(ErrorCode.messageGone.rawValue == 1305 && ErrorCode(rawValue: 1305).name == "messageGone")
        #expect(ErrorCode.partNotDownloaded.rawValue == 1504 && ErrorCode(rawValue: 1504).name == "partNotDownloaded")
        #expect(ErrorCode.unsubscribeFailed.rawValue == 1505 && ErrorCode(rawValue: 1505).name == "unsubscribeFailed")
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
        "system.info", "system.hello", "system.authenticate", "system.storage",
        "account.list", "account.add", "account.remove", "account.setEnabled",
        "account.update", "account.discover", "account.test", "account.linked",
        "account.reorder", "account.oauthStart", "account.oauthWait", "account.oauthCancel",
        "account.detectSite", "account.listSpaces",
        "folder.list", "folder.subscribe",
        "message.list", "message.get", "message.body", "message.part",
        "message.embedded", "message.download", "message.flag", "message.move", "message.delete",
        "message.send", "message.unsubscribe",
        "outbox.retry",
        "thread.list", "thread.get",
        "draft.save", "draft.list", "draft.delete", "draft.create", "draft.open",
        "draft.markdown",
        "attachment.import", "attachment.remove", "attachment.get",
        "search.query",
        "sync.status", "sync.trigger",
        "config.get", "config.set",
        "sender.list", "sender.add", "sender.remove",
        "contact.search",
        "issue.transitions", "issue.transition",
    ]

    @Test func methodTableMatchesGo() {
        #expect(API.allMethods.count == 54)
        #expect(Set(API.allMethods).count == 54, "no duplicates")
        #expect(API.allMethods == Self.goMethods)
        #expect(API.methods.count == API.allMethods.count)
        #expect(API.systemInfo == API.SystemInfo.name)
        #expect(API.allNotifications == [
            "notify.newMessage", "notify.syncState", "notify.authRequired", "notify.accountsChanged", "notify.messagesChanged",
        ])
        #expect(API.AccountDetectSite.name == "account.detectSite" && API.AccountListSpaces.name == "account.listSpaces")
        #expect(API.IssueTransitions.name == "issue.transitions" && API.IssueTransition.name == "issue.transition")
    }

    @Test func timeoutsFollowThePlan() {
        #expect(API.SystemInfo.timeout == .seconds(3))
        #expect(RPCTimeouts.handshake == .seconds(5), "api.HandshakeTimeout")
        #expect(API.SystemHello.timeout == RPCTimeouts.handshake && API.SystemAuthenticate.timeout == RPCTimeouts.handshake)
        #expect(API.MessagePart.timeout == .seconds(60) && API.AttachmentGet.timeout == .seconds(60))
        #expect(API.MessageEmbedded.timeout == .seconds(30) && API.DraftCreate.timeout == .seconds(30))
        #expect(API.DraftOpen.timeout == .seconds(30))
        #expect(API.AccountAdd.timeout == .seconds(30) && API.AccountUpdate.timeout == .seconds(30))
        #expect(API.AccountDiscover.timeout == .seconds(15) && API.AccountTest.timeout == .seconds(45))
        #expect(API.MessageBody.timeout == .seconds(30))
        #expect(API.MessageList.timeout == .seconds(5) && API.SenderAdd.timeout == .seconds(5))
        #expect(API.AccountOAuthStart.timeout == .seconds(10) && API.AccountOAuthWait.timeout == .seconds(75))
        #expect(RPCTimeouts.oauthStart == .seconds(10) && RPCTimeouts.oauthWaitCall == .seconds(75))
        #expect(API.AccountOAuthCancel.timeout == .seconds(5))
        #expect(RPCTimeouts.default == .seconds(5) && RPCTimeouts.remote == .seconds(30))
        // The daemon's download budget is 4 minutes; the client waits 5.
        #expect(RPCTimeouts.download == .seconds(300) && API.MessageDownload.timeout == RPCTimeouts.download)
        #expect(API.SystemStorage.timeout == .seconds(5))
        // The daemon may verify the message and call the sender's server (15 s).
        #expect(RPCTimeouts.unsubscribe == .seconds(30) && API.MessageUnsubscribe.timeout == RPCTimeouts.unsubscribe)
        // Like account.discover and account.test: a site lookup, a sign-in with listing.
        #expect(API.AccountDetectSite.timeout == .seconds(15) && RPCTimeouts.detectSite == .seconds(15))
        #expect(API.AccountListSpaces.timeout == .seconds(45) && RPCTimeouts.listSpaces == .seconds(45))
        // One value for both, as in the GTK UI (issue_actions.go
        // `issueTimeout`): the listing may be slow, and the change waits
        // for the daemon's refresh (up to 30 s).
        #expect(API.IssueTransitions.timeout == .seconds(45) && RPCTimeouts.transitions == .seconds(45))
        #expect(API.IssueTransition.timeout == .seconds(45) && RPCTimeouts.transition == .seconds(45))
        let special: Set<String> = ["system.info", "system.hello", "system.authenticate", "message.body",
                                    "message.part", "attachment.get", "message.embedded", "draft.create", "draft.open",
                                    "account.add", "account.update", "account.discover", "account.test",
                                    "account.oauthStart", "account.oauthWait", "message.download",
                                    "account.detectSite", "account.listSpaces", "issue.transitions", "issue.transition",
                                    "message.unsubscribe"]
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
