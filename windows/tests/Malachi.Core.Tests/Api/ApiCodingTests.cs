// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/APICodingTests.swift.
//
// The typed API layer against the JSON examples of docs/api.md: every
// method's result decodes, the Go habits (null slices, omitempty, RFC 3339)
// come out as plain C#, and the method table matches methods.go. Where Swift
// compares two structs holding arrays, the test compares their JSON or their
// members: C# records compare lists by reference.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Xunit;
using static Malachi.Core.Tests.Api.ApiJson;

namespace Malachi.Core.Tests.Api;

public sealed class ApiCodingTests
{
    // MARK: docs/api.md examples

    [Fact]
    public void SystemInfoExample()
    {
        var r = Decode<SystemInfoResult>("""{"version":"0.1.0","protocolVersion":2,"pid":4242,"storePath":"/home/u/.local/share/malachi/store.db"}""");
        Assert.Equal(
            new SystemInfoResult { Version = "0.1.0", ProtocolVersion = 2, Pid = 4242, StorePath = "/home/u/.local/share/malachi/store.db" },
            r);
        // Swift's typealias SystemInfo (the skeleton's name) has no C#
        // counterpart; the version is the handshake's.
        Assert.Equal(API.ProtocolVersion, r.ProtocolVersion);
    }

    /// <summary>docs/api.md §4.0 and §1.4: system.hello and system.authenticate.</summary>
    [Fact]
    public void HandshakeExamples()
    {
        var nonce = string.Concat(Enumerable.Repeat("0f", 32));
        var proof = string.Concat(Enumerable.Repeat("ab", 32));
        var hello = EncodeObject(new SystemHelloParams { ClientNonce = nonce });
        Assert.Equal(["clientNonce"], Keys(hello));
        Assert.Equal(nonce, hello.GetProperty("clientNonce").GetString());
        var r = Decode<SystemHelloResult>($$$"""{"protocolVersion":2,"daemonNonce":"{{{nonce}}}","daemonProof":"{{{proof}}}"}""");
        Assert.Equal(new SystemHelloResult { ProtocolVersion = 2, DaemonNonce = nonce, DaemonProof = proof }, r);
        var auth = EncodeObject(new SystemAuthenticateParams { ClientProof = proof });
        Assert.Equal(["clientProof"], Keys(auth));
        Assert.Equal(proof, auth.GetProperty("clientProof").GetString());
        Assert.Equal(new EmptyResult(), JsonSerializer.Deserialize("{}", API.SystemAuthenticate.ResultInfo));
        Assert.Equal("system.hello", API.SystemHello.Name);
        Assert.Equal("system.authenticate", API.SystemAuthenticate.Name);
        Assert.Equal(2, API.ProtocolVersion);
    }

    [Fact]
    public void AccountListExample()
    {
        var r = Decode<AccountListResult>("""
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
            """);
        Assert.Equal(3, r.Accounts.Count);
        var work = r.Accounts[0];
        Assert.Equal("acc_1", work.Id);
        Assert.Null(work.Config.Kind);
        Assert.Equal(AccountKind.Imap, work.Config.ProtocolKind);
        Assert.Equal(Security.Tls, work.Config.Imap!.Security);
        Assert.Equal(587, work.Config.Smtp!.Port);
        Assert.Equal(300, work.Config.SyncIntervalSeconds);
        Assert.Equal(SyncStatus.Idle, work.State.Status);
        Assert.Equal(-1, work.State.Progress);
        Assert.Null(work.State.LastSync);
        var gmail = r.Accounts[1];
        Assert.Equal(
            new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "account_1788683507_0", Provider = OAuth2Provider.Google },
            gmail.Config.OAuth2);
        Assert.Equal(AuthMethod.OAuth2, gmail.Config.Imap!.AuthMethod);
        Assert.Equal("f_inbox", gmail.State.FolderId);
        Assert.Equal(1, gmail.State.PendingOutbox);
        Assert.Equal(Rfc3339.Parse("2026-09-02T10:00:00Z"), gmail.State.LastSync);
        var m365 = r.Accounts[2];
        Assert.Equal(AccountKind.Graph, m365.Config.ProtocolKind);
        Assert.Null(m365.Config.Imap);
        Assert.Null(m365.Config.Smtp);
        Assert.Equal(new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "account_1788512854_0" }, m365.Config.Graph);
        Assert.False(m365.Enabled);
        Assert.Equal(SyncStatus.Disabled, m365.State.Status);
        Assert.Equal(new RpcError { Code = ErrorCode.AuthRequired, Message = "sign in again" }, m365.State.Error);
    }

    [Fact]
    public void FolderListExample()
    {
        var r = Decode<FolderListResult>("""
            {"folders":[
              {"id":"f_1","accountId":"acc_1","name":"Inbox","path":"Inbox","role":"inbox","subscribed":true,"selectable":true,"synced":true,"unread":3,"total":120},
              {"id":"f_2","accountId":"acc_1","parentId":"f_1","name":"Sub","path":"Inbox/Sub","role":"none","subscribed":false,"selectable":true,"synced":true,"unread":0,"total":1},
              {"id":"f_all","accountId":"acc_1","name":"All Mail","path":"[Gmail]/All Mail","role":"archive","subscribed":true,"selectable":true,"synced":false,"unread":0,"total":0},
              {"id":"f_out","accountId":"acc_1","name":"Outbox","path":"","role":"outbox","subscribed":true,"selectable":true,"synced":false,"unread":0,"total":2}
            ]}
            """);
        Assert.Equal<FolderRole>([FolderRole.Inbox, FolderRole.None, FolderRole.Archive, FolderRole.Outbox], r.Folders.Select(f => f.Role));
        Assert.Null(r.Folders[0].ParentId);
        Assert.Equal("f_1", r.Folders[1].ParentId);
        Assert.False(r.Folders[2].Synced);
        Assert.Equal("", r.Folders[3].Path);
        Assert.Equal(2, r.Folders[3].Total);
    }

    internal const string SummaryJson = """
        {"id":"m_123","accountId":"acc_1","folderId":"f_inbox","threadId":"t_9",
         "from":[{"name":"Alice","address":"alice@example.org"}],"to":[{"address":"me@example.org"}],
         "subject":"Lunch","date":"2026-09-02T10:00:00Z",
         "snippet":"plain text, derived by the backend",
         "flags":["seen"],"hasAttachments":false,"size":4321}
        """;

    [Fact]
    public void MessageListExample()
    {
        var r = Decode<MessageListResult>("""
            {"messages":[@summary@,
              {"id":"m_7","accountId":"acc_1","folderId":"f_outbox","from":[{"address":"me@example.org"}],
               "subject":"Re: Lunch","date":"2026-09-02T11:30:00.5+02:00","snippet":"","flags":["seen"],"hasAttachments":true,"size":100,
               "outbox":{"state":"failed","attempts":3,"nextAttemptAt":"2026-09-02T12:00:00Z","error":{"code":1302,"message":"550 no"}}}],
             "page":{"nextCursor":"opaque","total":1234}}
            """.Replace("@summary@", SummaryJson, StringComparison.Ordinal));
        Assert.Equal(new PageInfo { NextCursor = "opaque", Total = 1234 }, r.Page);
        Assert.Equal(2, r.Messages.Count);
        var m = r.Messages[0];
        Assert.Equal("m_123", m.Id);
        Assert.Equal("acc_1", m.AccountId);
        Assert.Equal("f_inbox", m.FolderId);
        Assert.Equal("t_9", m.ThreadId);
        Assert.Equal([new Address { Name = "Alice", Email = "alice@example.org" }], m.From);
        Assert.Equal([new Address { Email = "me@example.org" }], m.To!);
        Assert.Equal<Flag>([Flag.Seen], m.Flags);
        Assert.False(m.HasAttachments);
        Assert.Equal(4321, m.Size);
        Assert.Equal(DateTimeOffset.FromUnixTimeSeconds(1_788_343_200), m.Date);
        Assert.Null(m.Outbox);
        var q = r.Messages[1];
        Assert.Null(q.ThreadId); // an unlinked message has no thread
        Assert.Null(q.To); // omitempty slice absent
        Assert.Equal(DateTimeOffset.FromUnixTimeMilliseconds(((1_788_343_200 - 1800) * 1000L) + 500), q.Date); // 11:30+02:00 is 09:30Z
        var outbox = Assert.IsType<OutboxInfo>(q.Outbox);
        Assert.Equal(OutboxState.Failed, outbox.State);
        Assert.Equal(3, outbox.Attempts);
        Assert.Equal(Rfc3339.Parse("2026-09-02T12:00:00Z"), outbox.NextAttemptAt);
        Assert.Equal(ErrorCode.ServerError, outbox.Error!.Code);
    }

    internal const string MessageJson = """
        {"id":"m_123","accountId":"acc_1","folderId":"f_inbox","threadId":"t_9",
         "from":[{"name":"Alice","address":"alice@example.org"}],"to":[{"address":"me@example.org"}],
         "subject":"Lunch","date":"2026-09-02T10:00:00Z","snippet":"plain","flags":["seen","answered"],"hasAttachments":true,"size":4321,
         "cc":[{"name":"Bob","address":"bob@example.org"}],"replyTo":[{"address":"alice-reply@example.org"}],
         "rfcMessageId":"<x@example.org>","inReplyTo":"<w@example.org>","references":["<v@example.org>","<w@example.org>"],
         "attachments":[{"partId":"2.1","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false},
                        {"partId":"2.2","filename":"image001.png","contentType":"image/png","size":100,"inline":true,"contentId":"image001@example.org"}],
         "headers":{"List-Unsubscribe":"<mailto:u@example.org>","Auto-Submitted":"no"}}
        """;

    [Fact]
    public void MessageGetExampleFlattensTheSummary()
    {
        var r = Decode<MessageGetResult>($$$"""{"message":{{{MessageJson}}}}""");
        var m = r.Message;
        Assert.Equal("m_123", m.Summary.Id);
        Assert.Equal("Lunch", m.Summary.Subject);
        Assert.Equal<Flag>([Flag.Seen, Flag.Answered], m.Summary.Flags);
        Assert.Equal([new Address { Name = "Bob", Email = "bob@example.org" }], m.Cc!);
        Assert.Null(m.Bcc);
        Assert.Equal([new Address { Email = "alice-reply@example.org" }], m.ReplyTo!);
        Assert.Equal("<x@example.org>", m.RfcMessageId);
        Assert.Equal("<w@example.org>", m.InReplyTo);
        Assert.Equal(["<v@example.org>", "<w@example.org>"], m.References!);
        Assert.Equal(2, m.Attachments.Count);
        Assert.True(m.Attachments[1].Inline);
        Assert.Equal("image001@example.org", m.Attachments[1].ContentId);
        Assert.Null(m.Attachments[0].ContentId);
        Assert.Equal(
            new Dictionary<string, string> { ["List-Unsubscribe"] = "<mailto:u@example.org>", ["Auto-Submitted"] = "no" },
            m.Headers!);
    }

    [Fact]
    public void MessageRoundTripsFlat()
    {
        var original = Decode<Message>(MessageJson);
        var data = JsonCoding.EncodeToString(original);
        var obj = Parse(data);
        Assert.Null(Member(obj, "summary")); // the summary is flattened, never nested
        Assert.Equal("m_123", obj.GetProperty("id").GetString());
        Assert.NotNull(Member(obj, "cc"));
        Assert.Null(Member(obj, "bcc"));
        Assert.Equal(2, obj.GetProperty("attachments").GetArrayLength());
        var again = Decode<Message>(data);
        AssertSameValue(original, again);
        AssertSameJson(MessageJson, data);

        // A message built in code, with nothing optional, survives as well.
        var bare = new Message
        {
            Summary = new MessageSummary
            {
                Id = "m_1",
                AccountId = "a",
                FolderId = "f",
                From = [],
                Subject = "",
                Date = DateTimeOffset.GoZero,
                Snippet = "",
                Flags = [],
                HasAttachments = false,
                Size = 0,
            },
        };
        var bareData = JsonCoding.EncodeToString(bare);
        AssertSameValue(bare, Decode<Message>(bareData));
        var bareObj = Parse(bareData);
        Assert.Equal(0, bareObj.GetProperty("attachments").GetArrayLength());
        Assert.Null(Member(bareObj, "threadId"));
        Assert.Null(Member(bareObj, "to"));
        Assert.Null(Member(bareObj, "headers"));
    }

    [Fact]
    public void MessageBodyExample()
    {
        var r = Decode<MessageBodyResult>("""
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
            """);
        Assert.Equal("m_123", r.MessageId);
        Assert.Equal(BodyState.Fetched, r.BodyState);
        Assert.True(r.HasHtml);
        Assert.Equal("<p>sanitised</p>", r.Html);
        Assert.False(r.HtmlWithheld);
        Assert.Equal(new BlockedContent { RemoteImages = 3, RemoteStyles = 1, Scripts = 1, EventHandlers = 2, TrackingPixels = 1 }, r.Blocked);
        Assert.False(r.Blocked.IsEmpty);
        Assert.True(new BlockedContent().IsEmpty);
        Assert.Equal([new Link { Text = "Click here", Href = "https://real.destination/x" }], r.Links);
        Assert.Equal(new Dictionary<string, string> { ["image001@example.org"] = "2.1" }, r.InlineParts!);
        Assert.Equal(RemoteContentPolicy.Block, r.RemoteContent);
        Assert.Equal("1", r.SanitizerVersion);

        var text = Decode<MessageBodyResult>("""
            {"messageId":"m_1","bodyState":"pending","hasHtml":false,"text":"",
             "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
             "links":null,"remoteContent":"allow","sanitizerVersion":"1"}
            """);
        Assert.Null(text.Html);
        Assert.Null(text.HtmlWithheld);
        Assert.Null(text.InlineParts);
        Assert.Empty(text.Links);
        Assert.Equal(BodyState.Pending, text.BodyState);
    }

    internal const string ThreadJson = $$$"""
        {"id":"t_9","accountId":"acc_1","subject":"Lunch",
         "participants":[{"name":"Alice","address":"alice@example.org"},{"address":"me@example.org"}],
         "messageCount":3,"unreadCount":1,"latestDate":"2026-09-02T10:00:00Z",
         "latest":{{{SummaryJson}}},
         "snippet":"plain text, derived by the backend","flags":["flagged","seen"],"hasAttachments":true,
         "folderIds":["f_inbox","f_sent"]}
        """;

    [Fact]
    public void ThreadListExample()
    {
        var r = Decode<ThreadListResult>($$$"""{"threads":[{{{ThreadJson}}}],"page":{"total":1}}""");
        Assert.Equal(new PageInfo { Total = 1 }, r.Page);
        var t = Assert.Single(r.Threads);
        Assert.Equal("t_9", t.Id);
        Assert.Equal("Lunch", t.Subject);
        Assert.Equal(2, t.Participants.Count);
        Assert.Equal(3, t.MessageCount);
        Assert.Equal(1, t.UnreadCount);
        Assert.Equal("m_123", t.Latest.Id);
        Assert.Equal(t.Latest.Date, t.LatestDate);
        Assert.Equal<Flag>([Flag.Flagged, Flag.Seen], t.Flags);
        Assert.True(t.HasAttachments);
        Assert.Equal<FolderId>(["f_inbox", "f_sent"], t.FolderIds);
    }

    [Fact]
    public void ThreadGetExample()
    {
        var r = Decode<ThreadGetResult>($$$"""{"thread":{{{ThreadJson}}},"messages":[{{{SummaryJson}}},{{{SummaryJson}}}]}""");
        Assert.Equal("t_9", r.Thread.Id);
        Assert.Equal(2, r.Messages.Count);
        Assert.Equal("t_9", r.Messages[1].ThreadId);
    }

    [Fact]
    public void DraftSaveExample()
    {
        var r = Decode<DraftSaveResult>("""
            {"draftId":"d_1","version":2,"textBody":"hi","htmlBody":"<p>hi</p>",
             "blocked":{"remoteImages":1,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
             "attachments":[{"id":"att_1","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false}]}
            """);
        Assert.Equal("d_1", r.DraftId);
        Assert.Equal(2, r.Version);
        Assert.Equal("hi", r.TextBody);
        Assert.Equal("<p>hi</p>", r.HtmlBody);
        Assert.Equal(1, r.Blocked.RemoteImages);
        Assert.Equal(
            [new DraftAttachment { Id = "att_1", Filename = "safe-name.pdf", ContentType = "application/pdf", Size = 12345, Inline = false }],
            r.Attachments!);

        var plain = Decode<DraftSaveResult>("""
            {"draftId":"d_2","version":1,"textBody":"hi",
             "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0}}
            """);
        Assert.Null(plain.HtmlBody);
        Assert.Null(plain.Attachments);
    }

    [Fact]
    public void DraftCreateExample()
    {
        var r = Decode<DraftCreateResult>("""
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
            """);
        var d = r.Draft;
        Assert.Null(d.Id); // unsaved: no id, version 0
        Assert.Equal(0, d.Version);
        Assert.Equal([new Address { Name = "Alice", Email = "alice@example.org" }], d.To);
        Assert.Null(d.Cc);
        Assert.Equal("Re: Lunch", d.Subject);
        Assert.Equal("m_123", d.InReplyTo);
        Assert.Null(d.Forwarding);
        Assert.Contains("<blockquote type=\"cite\">", d.HtmlBody, StringComparison.Ordinal);
        Assert.Equal("abc@malachi.local", d.Attachments![0].ContentId);
        Assert.True(d.UpdatedAt.IsGoZero);
        Assert.Equal(QuoteForm.Html, r.Quoted);
        Assert.Equal(2, r.Blocked.RemoteImages);
        Assert.Equal("3", r.Skipped![0].PartId);

        // The draft goes back to draft.save as it came.
        var obj = EncodeObject(new DraftSaveParams { Draft = d });
        var draft = obj.GetProperty("draft");
        Assert.Null(Member(draft, "id"));
        Assert.Equal(0, draft.GetProperty("version").GetInt32());
        Assert.Equal(1, draft.GetProperty("to").GetArrayLength());
        Assert.Null(Member(draft, "cc"));
        Assert.Equal("0001-01-01T00:00:00Z", draft.GetProperty("updatedAt").GetString());
    }

    [Fact]
    public void DraftOpenExample()
    {
        var r = Decode<DraftOpenResult>("""
            {"draft":{"accountId":"acc_1","version":0,"to":[{"address":"alice@example.org"}],
                      "bcc":[{"name":"Hidden","address":"hidden@example.org"}],"subject":"Re: Plans",
                      "textBody":"x","htmlBody":"<p>x</p>","replaces":"m_9","updatedAt":"0001-01-01T00:00:00Z"},
             "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0}}
            """);
        Assert.Null(r.Draft.Id);
        Assert.Equal("m_9", r.Draft.Replaces);
        Assert.Equal("hidden@example.org", r.Draft.Bcc![0].Email);
        Assert.Null(r.Skipped);

        // replaces goes back to draft.save as it came, and is omitted when null.
        var obj = EncodeObject(new DraftSaveParams { Draft = r.Draft });
        Assert.Equal("m_9", obj.GetProperty("draft").GetProperty("replaces").GetString());
        var plain = EncodeObject(new DraftSaveParams { Draft = new Draft { AccountId = "a" } });
        Assert.Null(Member(plain.GetProperty("draft"), "replaces"));

        var parameters = EncodeObject(new DraftOpenParams { AccountId = "a", MessageId = "m_1" });
        Assert.Equal("a", parameters.GetProperty("accountId").GetString());
        Assert.Equal("m_1", parameters.GetProperty("messageId").GetString());
    }

    [Fact]
    public void AttachmentImportExample()
    {
        var r = Decode<AttachmentImportResult>("""{"attachment":{"id":"att_1","filename":"safe-name.pdf","contentType":"application/pdf","size":12345,"inline":false,"contentId":"x@malachi.local"}}""");
        Assert.Equal("att_1", r.Attachment.Id);
        Assert.Equal(12345, r.Attachment.Size);
        Assert.Equal("x@malachi.local", r.Attachment.ContentId);

        var parameters = EncodeObject(new AttachmentImportParams { AccountId = "acc_1", Data = [1, 2, 3], Filename = "a.bin", Inline = true });
        Assert.Equal("AQID", parameters.GetProperty("data").GetString()); // binary travels as standard base64
        Assert.Null(Member(parameters, "path"));
        Assert.True(parameters.GetProperty("inline").GetBoolean());
    }

    [Fact]
    public void AccountTestExample()
    {
        var r = Decode<AccountTestResult>("""
            {"imap":{"ok":true,"capabilities":["IDLE","CONDSTORE"],"latencyMs":120},
             "smtp":{"ok":false,"error":{"code":1201,"message":"535 authentication failed"},"latencyMs":80}}
            """);
        AssertSameValue(new EndpointTestResult { Ok = true, Capabilities = ["IDLE", "CONDSTORE"], LatencyMs = 120 }, r.Imap);
        Assert.False(r.Smtp!.Ok);
        Assert.Equal(ErrorCode.AuthFailed, r.Smtp.Error!.Code);
        Assert.Null(r.Smtp.Capabilities);
        Assert.Null(r.Graph);

        var graph = Decode<AccountTestResult>("""{"graph":{"ok":true,"capabilities":["graph"],"latencyMs":300}}""");
        Assert.Null(graph.Imap);
        Assert.Null(graph.Smtp);
        Assert.Equal(["graph"], graph.Graph!.Capabilities!);
    }

    [Fact]
    public void AccountDiscoverExample()
    {
        var ispdb = Decode<AccountDiscoverResult>("""
            {"config":{"name":"example.org","email":"me@example.org",
                       "imap":{"host":"imap.example.org","port":993,"security":"tls","username":"me@example.org","authMethod":"password"},
                       "smtp":{"host":"smtp.example.org","port":587,"security":"starttls","username":"me@example.org","authMethod":"password"}},
             "source":"ispdb"}
            """);
        Assert.Equal(DiscoverSource.Ispdb, ispdb.Source);
        Assert.Null(ispdb.ProviderName);
        Assert.Equal("imap.example.org", ispdb.Config!.Imap!.Host);
        Assert.Equal(Security.Starttls, ispdb.Config.Smtp!.Security);

        var none = Decode<AccountDiscoverResult>("""{"source":"none"}""");
        Assert.Equal(new AccountDiscoverResult { Source = DiscoverSource.None }, none);

        var provider = Decode<AccountDiscoverResult>("""
            {"source":"provider","providerName":"Google",
             "config":{"name":"Google","email":"me@gmail.com",
                       "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                       "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                       "oauth2":{"provider":"google"}}}
            """);
        Assert.Equal(DiscoverSource.Provider, provider.Source);
        Assert.Equal("Google", provider.ProviderName);
        Assert.Equal(new OAuth2Config { Provider = OAuth2Provider.Google }, provider.Config!.OAuth2);
        Assert.Empty(provider.Alternatives); // absent alternatives read as empty

        // Without GNOME Online Accounts: the daemon's own sign-in first, the
        // app password as the alternative.
        var own = Decode<AccountDiscoverResult>("""
            {"source":"provider","providerName":"Google",
             "config":{"name":"me@gmail.com","email":"me@gmail.com",
                       "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                       "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
                       "oauth2":{"source":"daemon","provider":"google"}},
             "alternatives":[{"name":"me@gmail.com","email":"me@gmail.com",
                       "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"password"},
                       "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"password"}}]}
            """);
        Assert.Equal(new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google }, own.Config!.OAuth2);
        var alternative = Assert.Single(own.Alternatives);
        Assert.Equal(AuthMethod.Password, alternative.Imap!.AuthMethod);
        Assert.Empty(Decode<AccountDiscoverResult>("""{"source":"none","alternatives":null}""").Alternatives);

        var graph = Decode<AccountDiscoverResult>("""
            {"source":"provider","providerName":"Microsoft 365",
             "config":{"name":"me@contoso.com","email":"me@contoso.com","kind":"graph","graph":{"source":"daemon"},
                       "oauth2":{"source":"daemon","provider":"office365","tenantId":"common"}}}
            """);
        Assert.Equal(new GraphConfig { Source = GraphSource.Daemon }, graph.Config!.Graph);
        Assert.Equal(
            new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365, TenantId = "common" },
            graph.Config.OAuth2);
    }

    [Fact]
    public void AccountOAuthExamples()
    {
        var start = Decode<AccountOAuthStartResult>("""
            {"sessionId":"s_1","authUrl":"https://accounts.google.com/o/oauth2/v2/auth?x=1","expiresAt":"2026-09-25T10:10:00Z"}
            """);
        Assert.Equal("s_1", start.SessionId);
        Assert.StartsWith("https://accounts.google.com/", start.AuthUrl, StringComparison.Ordinal);
        Assert.Equal(Rfc3339.Parse("2026-09-25T10:10:00Z"), start.ExpiresAt);

        var pending = Decode<AccountOAuthWaitResult>("""{"status":"pending"}""");
        Assert.Equal(new AccountOAuthWaitResult { Status = OAuthSessionStatus.Pending }, pending);
        var complete = Decode<AccountOAuthWaitResult>("""
            {"status":"complete","config":{"name":"Gmail","email":"me@gmail.com",
              "imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
              "smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},
              "oauth2":{"source":"daemon","provider":"google"}}}
            """);
        Assert.Equal(OAuthSessionStatus.Complete, complete.Status);
        Assert.Equal(OAuth2Source.Daemon, complete.Config!.OAuth2!.Source);
        Assert.Equal("expired", Decode<AccountOAuthWaitResult>("""{"status":"expired"}""").Status); // an unknown status decodes

        var byConfig = EncodeObject(new AccountOAuthStartParams
        {
            Config = new AccountConfig
            {
                Name = "Gmail",
                Email = "me@gmail.com",
                OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
            },
            BrowserPage = new OAuthBrowserPage { SuccessTitle = "Signed in", FailureTitle = "Sign-in failed" },
        });
        Assert.Null(Member(byConfig, "accountId"));
        Assert.Equal("daemon", byConfig.GetProperty("config").GetProperty("oauth2").GetProperty("source").GetString());
        Assert.Equal(["failureTitle", "successTitle"], Keys(byConfig.GetProperty("browserPage"))); // omitted texts are left out
        var byId = EncodeObject(new AccountOAuthStartParams { AccountId = "acc_1" });
        Assert.Equal(["accountId"], Keys(byId));
        Assert.Equal("s_1", EncodeObject(new AccountOAuthWaitParams { SessionId = "s_1" }).GetProperty("sessionId").GetString());
        Assert.Equal("s_1", EncodeObject(new AccountOAuthCancelParams { SessionId = "s_1" }).GetProperty("sessionId").GetString());
    }

    [Fact]
    public void SyncStatusExample()
    {
        var r = Decode<SyncStatusResult>("""
            {"accounts":[
              {"accountId":"acc_1","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","pendingOutbox":0},
              {"accountId":"acc_2","status":"offline","progress":-1,"error":{"code":1301,"message":"dial tcp: connection refused"},"pendingOutbox":2}]}
            """);
        Assert.Equal(2, r.Accounts.Count);
        Assert.Equal(
            new SyncState
            {
                AccountId = "acc_1",
                Status = SyncStatus.Syncing,
                FolderId = "f_inbox",
                Progress = 42,
                LastSync = Rfc3339.Parse("2026-09-02T10:00:00Z"),
                PendingOutbox = 0,
            },
            r.Accounts[0]);
        Assert.Equal(SyncStatus.Offline, r.Accounts[1].Status);
        Assert.Equal(ErrorCode.NetworkError, r.Accounts[1].Error!.Code);
        Assert.Equal(2, r.Accounts[1].PendingOutbox);
    }

    /// <summary>
    /// SyncState.failedOutbox (docs/api.md §3, protocol 1 changelog): read
    /// when present, 0 from a daemon that predates it; always written.
    /// </summary>
    [Fact]
    public void SyncStateFailedOutbox()
    {
        var s = Decode<SyncState>("""{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":1,"failedOutbox":2}""");
        Assert.Equal(1, s.PendingOutbox);
        Assert.Equal(2, s.FailedOutbox);
        var old = Decode<SyncState>("""{"accountId":"a","status":"offline","folderId":"f","progress":3,"lastSync":"2026-09-02T10:00:00Z","error":{"code":1301,"message":"x"},"pendingOutbox":0}""");
        Assert.Equal(
            new SyncState
            {
                AccountId = "a",
                Status = SyncStatus.Offline,
                FolderId = "f",
                Progress = 3,
                LastSync = Rfc3339.Parse("2026-09-02T10:00:00Z"),
                Error = new RpcError { Code = ErrorCode.NetworkError, Message = "x" },
                PendingOutbox = 0,
                FailedOutbox = 0,
            },
            old);
        Assert.Throws<JsonException>(() => Decode<SyncState>("""{"accountId":"a","status":"idle","progress":-1}"""));
        var obj = EncodeObject(new SyncState { AccountId = "a", Status = SyncStatus.Idle });
        Assert.Equal(0, obj.GetProperty("failedOutbox").GetInt32());
        Assert.Equal(0, obj.GetProperty("pendingOutbox").GetInt32());
        Assert.Equal(["accountId", "failedOutbox", "pendingOutbox", "progress", "status"], Keys(obj));
        // A round trip keeps it.
        var back = Decode<SyncState>(JsonCoding.EncodeToString(new SyncState { AccountId = "a", Status = SyncStatus.Idle, FailedOutbox = 4 }));
        Assert.Equal(4, back.FailedOutbox);
    }

    [Fact]
    public void ConfigGetExample()
    {
        var r = Decode<ConfigGetResult>("""{"preferences":{"syncIntervalSeconds":300,"remoteContent":"knownSenders","offlineDays":30}}""");
        Assert.Equal(
            new Preferences { SyncIntervalSeconds = 300, RemoteContent = RemoteContentPolicy.KnownSenders, OfflineDays = 30 },
            r.Preferences);
        // config.set echoes the whole set.
        var prefs = EncodeObject(new ConfigSetParams { Preferences = r.Preferences }).GetProperty("preferences");
        Assert.Equal(300, prefs.GetProperty("syncIntervalSeconds").GetInt32());
        Assert.Equal("knownSenders", prefs.GetProperty("remoteContent").GetString());
        Assert.Equal(30, prefs.GetProperty("offlineDays").GetInt32());
    }

    [Fact]
    public void ContactSearchExample()
    {
        var r = Decode<ContactSearchResult>("""
            {"contacts":[{"name":"Alice Example","address":"alice@example.org","source":"addressBook","book":"Contacts"},
                         {"address":"bob@example.org","source":"sent"}]}
            """);
        Assert.Equal(
            [
                new Contact { Name = "Alice Example", Address = "alice@example.org", Source = ContactSource.AddressBook, Book = "Contacts" },
                new Contact { Address = "bob@example.org", Source = ContactSource.Sent },
            ],
            r.Contacts);
        Assert.Empty(Decode<ContactSearchResult>("""{"contacts":[]}""").Contacts);
    }

    [Fact]
    public void RemainingResultsDecode()
    {
        Assert.Equal("acc_2", Decode<AccountAddResult>("""{"accountId":"acc_2"}""").AccountId);
        Assert.Equal(new EmptyResult(), Decode<EmptyResult>("{}"));
        Assert.Equal("m_7", Decode<MessageSendResult>("""{"outboxId":"m_7"}""").OutboxId);
        var linked = Decode<AccountLinkedResult>("""
            {"accounts":[{"provider":"microsoft365","email":"me@contoso.com","name":"Me","goaAccountId":"account_1788512854_0","configured":false,"attentionNeeded":false,
                          "config":{"name":"Me","email":"me@contoso.com","kind":"graph","graph":{"source":"goa","goaAccountId":"account_1788512854_0"}}}]}
            """);
        Assert.Equal(LinkedProvider.Microsoft365, linked.Accounts[0].Provider);
        Assert.Equal(AccountKind.Graph, linked.Accounts[0].Config!.ProtocolKind);
        var part = Decode<MessagePartResult>("""{"partId":"2.1","contentType":"image/png","filename":"a.png","size":3,"data":"AQID"}""");
        Assert.Equal([1, 2, 3], part.Data);
        Assert.Equal(3, part.Size);
        var got = Decode<AttachmentGetResult>("""{"attachmentId":"att_1","filename":"a.png","contentType":"image/png","size":3,"data":"AQID"}""");
        Assert.Equal([1, 2, 3], got.Data);
        Assert.Equal("att_1", got.AttachmentId);
        var embedded = Decode<MessageEmbeddedResult>($$$"""
            {"partId":"3","message":{{{MessageJson}}},
             "body":{"messageId":"m_123","bodyState":"fetched","hasHtml":false,"text":"inner",
                     "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0},
                     "links":[],"remoteContent":"block","sanitizerVersion":"1"}}
            """);
        Assert.Equal("3", embedded.PartId);
        Assert.Equal("m_123", embedded.Message.Summary.Id);
        Assert.Equal("inner", embedded.Body.Text);
        var drafts = Decode<DraftListResult>("""
            {"drafts":[{"id":"d_1","accountId":"acc_1","version":3,"to":[],"subject":"s","textBody":"t","updatedAt":"2026-09-02T10:00:00.123456789Z"}],"page":{"total":1}}
            """);
        Assert.Equal("d_1", drafts.Drafts[0].Id);
        Assert.Empty(drafts.Drafts[0].To);
        var senders = Decode<SenderListResult>("""{"senders":[{"address":"alice@example.org","source":"sent","addedAt":"2026-09-02T10:00:00Z"}]}""");
        Assert.Equal(KnownSenderSource.Sent, senders.Senders[0].Source);
        var search = Decode<SearchQueryResult>($$$"""
            {"results":[{"message":{{{SummaryJson}}},"snippet":"plain text excerpt","ranges":[{"start":12,"end":18}],"score":0.83}],"page":{"total":1}}
            """);
        Assert.Equal([new MatchRange { Start = 12, End = 18 }], search.Results[0].Ranges!);
        Assert.Equal(0.83, search.Results[0].Score);
    }

    // MARK: Go habits

    [Fact]
    public void NullOrMissingSliceReadsAsEmpty()
    {
        Assert.Empty(Decode<FolderListResult>("""{"folders":null}""").Folders);
        Assert.Empty(Decode<FolderListResult>("{}").Folders);
        Assert.Empty(Decode<MessageListResult>("""{"messages":null,"page":{"total":0}}""").Messages);
        Assert.Empty(Decode<AccountListResult>("""{"accounts":null}""").Accounts);
        var m = Decode<MessageSummary>("""{"id":"m","accountId":"a","folderId":"f","from":null,"subject":"","date":"2026-09-02T10:00:00Z","snippet":"","flags":null,"hasAttachments":false,"size":0}""");
        Assert.Empty(m.From);
        Assert.Empty(m.Flags);
        // Encodes as an array, never null.
        var obj = EncodeObject(m);
        Assert.Equal(0, obj.GetProperty("from").GetArrayLength());
        Assert.Equal(0, obj.GetProperty("flags").GetArrayLength());
    }

    [Fact]
    public void MissingOptionalsAreNil()
    {
        var cfg = Decode<AccountConfig>("""{"name":"n","email":"e@x"}""");
        Assert.Null(cfg.DisplayName);
        Assert.Null(cfg.Kind);
        Assert.Null(cfg.Imap);
        Assert.Null(cfg.OAuth2);
        Assert.Null(cfg.SyncIntervalSeconds);
        var obj = EncodeObject(cfg);
        Assert.Equal(["email", "name"], Keys(obj)); // null never encodes as null
        var state = Decode<SyncState>("""{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0}""");
        Assert.Null(state.FolderId);
        Assert.Null(state.LastSync);
        Assert.Null(state.Error);
    }

    [Fact]
    public void UnknownEnumValuesDecode()
    {
        var f = Decode<Folder>("""{"id":"f","accountId":"a","name":"n","path":"n","role":"spam","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0}""");
        Assert.Equal(new FolderRole("spam"), f.Role);
        Assert.NotEqual(FolderRole.Junk, f.Role);
        var s = Decode<SyncState>("""{"accountId":"a","status":"hibernating","progress":-1,"pendingOutbox":0}""");
        Assert.Equal("hibernating", s.Status);
        var m = Decode<MessageSummary>("""{"id":"m","accountId":"a","folderId":"f","from":[],"subject":"","date":"2026-09-02T10:00:00Z","snippet":"","flags":["seen","pinned"],"hasAttachments":false,"size":0}""");
        Assert.Equal<Flag>([Flag.Seen, "pinned"], m.Flags);
        FolderRole inbox = FolderRole.Inbox;
        Assert.Equal("inbox", inbox.ToString());
        Assert.Equal("inbox", inbox.Value);
    }

    [Fact]
    public void DatesParseWithAndWithoutFractions()
    {
        var plain = Rfc3339.Parse("2026-09-02T10:00:00Z")!.Value;
        Assert.Equal(DateTimeOffset.FromUnixTimeSeconds(1_788_343_200), plain);
        Assert.Equal(DateTimeOffset.Parse("2026-09-02T10:00:00Z", CultureInfo.InvariantCulture), plain);
        Assert.Equal(DateTimeOffset.FromUnixTimeMilliseconds(1_788_343_200_500), Rfc3339.Parse("2026-09-02T10:00:00.5Z"));
        // Swift keeps what its double holds; a DateTimeOffset holds 100 ns
        // ticks, and the digits beyond the seventh are cut.
        Assert.Equal(plain.AddTicks(1_234_567), Rfc3339.Parse("2026-09-02T10:00:00.123456789Z"));
        Assert.Equal(plain, Rfc3339.Parse("2026-09-02T12:00:00+02:00"));
        Assert.Equal(DateTimeOffset.FromUnixTimeMilliseconds(1_788_343_200_250), Rfc3339.Parse("2026-09-02T09:30:00.25-00:30"));
        Assert.Equal(plain, Rfc3339.Parse("2026-09-02t10:00:00z")); // lower-case letters are allowed by RFC 3339
        Assert.Equal(DateTimeOffset.GoZero, Rfc3339.Parse("0001-01-01T00:00:00Z"));
        Assert.True(DateTimeOffset.GoZero.IsGoZero);
        Assert.False(plain.IsGoZero);
        Assert.True(DateTimeOffset.GoZero.AddDays(200).IsGoZero);
        foreach (var bad in new[]
        {
            "", "2026-09-02", "2026-09-02T10:00:00", "2026-09-02T10:00:00.Z", "2026-13-02T10:00:00Z",
            "2026-09-02T10:00:00+02", "2026-09-02T10:00:00Zjunk", "1788343200", "2026-09-02 10:00:00Z",
        })
        {
            Assert.True(Rfc3339.Parse(bad) is null, $"{bad} must not parse");
        }
        // Through the decoder, inside a record (Swift's Stamp: a KnownSender here).
        Assert.Equal(
            DateTimeOffset.FromUnixTimeMilliseconds(1_788_343_200_500 - 7_200_000),
            Decode<KnownSender>("""{"address":"a","source":"user","addedAt":"2026-09-02T10:00:00.5+02:00"}""").AddedAt);
        Assert.Throws<JsonException>(() => Decode<KnownSender>("""{"address":"a","source":"user","addedAt":"yesterday"}"""));
    }

    [Fact]
    public void DatesEncodeAsRfc3339Utc()
    {
        static KnownSender Stamp(DateTimeOffset at) => new() { Address = "a", Source = KnownSenderSource.User, AddedAt = at };
        var obj = EncodeObject(Stamp(DateTimeOffset.FromUnixTimeSeconds(1_788_343_200)));
        Assert.Equal("2026-09-02T10:00:00Z", obj.GetProperty("addedAt").GetString());
        var zero = EncodeObject(Stamp(DateTimeOffset.GoZero));
        Assert.Equal("0001-01-01T00:00:00Z", zero.GetProperty("addedAt").GetString()); // proleptic Gregorian, as Go
        Assert.Equal(DateTimeOffset.GoZero, Decode<KnownSender>(JsonCoding.EncodeToString(Stamp(DateTimeOffset.GoZero))).AddedAt);
        Assert.Equal("2026-09-02T10:00:00.5Z", Rfc3339.Format(DateTimeOffset.FromUnixTimeMilliseconds(1_788_343_200_500)));
        Assert.Equal("2026-09-02T10:00:00.25Z", Rfc3339.Format(DateTimeOffset.FromUnixTimeMilliseconds(1_788_343_200_250)));
        Assert.Equal("1969-12-31T23:59:59Z", Rfc3339.Format(DateTimeOffset.FromUnixTimeSeconds(-1)));
        Assert.Equal("1969-12-31T23:59:59.5Z", Rfc3339.Format(DateTimeOffset.FromUnixTimeMilliseconds(-500)));
        Assert.Equal("2000-02-29T00:00:00Z", Rfc3339.Format(DateTimeOffset.FromUnixTimeSeconds(951_782_400))); // leap day
        foreach (var s in new[] { "2026-09-02T10:00:00Z", "1999-12-31T23:59:59.999Z", "0001-01-01T00:00:00Z", "2024-02-29T12:34:56.789Z" })
        {
            Assert.Equal(s, Rfc3339.Format(Rfc3339.Parse(s)!.Value)); // must round-trip
        }
    }

    [Fact]
    public void AttachmentTooBigCarriesSizeLimit()
    {
        var both = Decode<RpcError>("""{"code":1502,"message":"too big","data":{"limit":26214400,"size":30000000}}""");
        Assert.Equal(new SizeLimit(26_214_400, 30_000_000), both.AttachmentTooBig);
        Assert.True(JsonElement.DeepEquals(Parse("""{"limit":26214400,"size":30000000}"""), both.Data!.Value));
        var limitOnly = Decode<RpcError>("""{"code":1502,"message":"too big","data":{"limit":16777216}}""");
        Assert.Equal(new SizeLimit(16_777_216, null), limitOnly.AttachmentTooBig);
        var other = Decode<RpcError>("""{"code":1001,"message":"bad","data":{"limit":1,"size":2}}""");
        Assert.Null(other.AttachmentTooBig);
        var noData = Decode<RpcError>("""{"code":1502,"message":"too big"}""");
        Assert.Null(noData.AttachmentTooBig);
        Assert.Null(noData.Data);
        Assert.Equal(new RpcError { Code = ErrorCode.AttachmentTooBig, Message = "x", Data = null }, new RpcError { Code = 1502, Message = "x" });
    }

    /// <summary>
    /// ServerConfig.certificateSha256 survives a decode and an encode (an
    /// edit must never drop the pin) and is left out when unset.
    /// </summary>
    [Fact]
    public void ServerConfigCarriesTheCertificatePin()
    {
        var pin = string.Concat(Enumerable.Repeat("ab", 32));
        var sc = Decode<ServerConfig>($$$"""{"host":"100.64.0.1","port":1143,"security":"starttls","username":"me","authMethod":"password","certificateSha256":"{{{pin}}}"}""");
        Assert.Equal(pin, sc.CertificateSha256);
        Assert.Equal(pin, EncodeObject(sc).GetProperty("certificateSha256").GetString());
        var update = EncodeObject(new AccountUpdateParams { AccountId = "a", Config = new AccountConfig { Name = "B", Email = "me@x.org", Imap = sc } });
        Assert.Equal(pin, update.GetProperty("config").GetProperty("imap").GetProperty("certificateSha256").GetString());
        var plain = Decode<ServerConfig>("""{"host":"h","port":993,"security":"tls","username":"u","authMethod":"password"}""");
        Assert.Null(plain.CertificateSha256);
        Assert.Null(Member(EncodeObject(plain), "certificateSha256"));
    }

    /// <summary>
    /// docs/api.md §2: the data of a tlsError from an IMAP/SMTP endpoint, in
    /// an account.test result and in a SyncState.
    /// </summary>
    [Fact]
    public void TlsErrorDataExample()
    {
        var sum = string.Concat(Enumerable.Repeat("0f", 32));
        var r = Decode<AccountTestResult>("""
            {"imap":{"ok":false,"latencyMs":40,"error":{"code":1303,"message":"x509: certificate is not standards compliant",
              "data":{"reason":"other","certificate":{"sha256":"@sum@","subject":"127.0.0.1","issuer":"127.0.0.1",
                "ipAddresses":["127.0.0.1"],"notBefore":"2024-01-02T03:04:05Z","notAfter":"2044-01-02T03:04:05Z","selfSigned":true}}}},
             "smtp":{"ok":false,"latencyMs":1,"error":{"code":1303,"message":"handshake","data":{"reason":"handshake"}}}}
            """.Replace("@sum@", sum, StringComparison.Ordinal));
        var d = Tls.TlsErrorData(r.Imap!.Error);
        Assert.NotNull(d);
        Assert.Equal(TlsErrorReason.Other, d.Reason);
        Assert.Null(d.ExpectedSha256);
        var c = Assert.IsType<CertificateInfo>(d.Certificate);
        Assert.Equal(sum, c.Sha256);
        Assert.Equal("127.0.0.1", c.Subject);
        Assert.Equal(["127.0.0.1"], c.IpAddresses);
        Assert.Empty(c.DnsNames);
        Assert.True(c.SelfSigned);
        Assert.Equal(Rfc3339.Parse("2024-01-02T03:04:05Z"), c.NotBefore);
        Assert.Equal(Rfc3339.Parse("2044-01-02T03:04:05Z"), c.NotAfter);
        Assert.Equal(new TlsErrorData { Reason = TlsErrorReason.Handshake }, Tls.TlsErrorData(r.Smtp!.Error));

        var state = Decode<SyncState>("""
            {"accountId":"acc_1","status":"offline","progress":-1,"pendingOutbox":0,
             "error":{"code":1303,"message":"pinned certificate mismatch","data":{"reason":"pinMismatch","expectedSha256":"@sum@",
               "certificate":{"sha256":"@other@","notBefore":"2026-01-01T00:00:00Z","notAfter":"2027-01-01T00:00:00Z","selfSigned":false}}}}
            """.Replace("@sum@", sum, StringComparison.Ordinal).Replace("@other@", string.Concat(Enumerable.Repeat("ab", 32)), StringComparison.Ordinal));
        var p = Tls.TlsErrorData(state.Error);
        Assert.NotNull(p);
        Assert.Equal(TlsErrorReason.PinMismatch, p.Reason);
        Assert.Equal(sum, p.ExpectedSha256);
        Assert.Null(p.Certificate!.Subject);

        // Not a tlsError, no data, or data of another shape: nothing.
        Assert.Null(Tls.TlsErrorData(new RpcError { Code = ErrorCode.NetworkError, Message = "x", Data = state.Error!.Data }));
        Assert.Null(Tls.TlsErrorData(new RpcError { Code = ErrorCode.TlsError, Message = "x" }));
        Assert.Null(Tls.TlsErrorData(new RpcError { Code = ErrorCode.TlsError, Message = "x", Data = Parse("[]") }));
        Assert.Null(Tls.TlsErrorData(null));
        // A reason of a newer daemon decodes as itself.
        var newer = Decode<RpcError>("""{"code":1303,"message":"x","data":{"reason":"quantum"}}""");
        Assert.Equal(new TlsErrorReason("quantum"), Tls.TlsErrorData(newer)!.Reason);
    }

    [Fact]
    public void CertificateFingerprintsNormalize()
    {
        var sum = string.Concat(Enumerable.Repeat("ab", 32));
        Assert.Equal(sum, Tls.NormalizeCertificateSha256(sum));
        Assert.Equal(sum, Tls.NormalizeCertificateSha256(sum.ToUpperInvariant()));
        Assert.Equal(sum, Tls.NormalizeCertificateSha256(string.Join(':', Enumerable.Repeat("AB", 32))));
        Assert.Equal(sum, Tls.NormalizeCertificateSha256(string.Join(' ', Enumerable.Repeat("ab", 32))));
        foreach (var bad in new[] { "", "abc", sum + "0", new string('g', 64), sum[..^1] + "\u0660", "-" + sum })
        {
            Assert.True(Tls.NormalizeCertificateSha256(bad) is null, bad);
        }
    }

    /// <summary>api.ErrorCode, copied from backend/pkg/api/errors.go in its order.</summary>
    internal static readonly (int Code, string Name)[] GoCodes =
    [
        (-32700, "parseError"), (-32600, "invalidRequest"), (-32601, "methodNotFound"), (-32602, "invalidParams"),
        (-32603, "internalError"),
        (1000, "notImplemented"), (1001, "invalidArgument"), (1002, "conflict"), (1003, "cancelled"), (1004, "unavailable"),
        (1005, "unauthenticated"),
        (1100, "accountNotFound"), (1101, "folderNotFound"), (1102, "messageNotFound"), (1103, "threadNotFound"),
        (1104, "draftNotFound"), (1105, "attachmentNotFound"),
        (1200, "authRequired"), (1201, "authFailed"), (1202, "keyringError"), (1203, "oauthClientMissing"),
        (1300, "offline"), (1301, "networkError"), (1302, "serverError"), (1303, "tlsError"), (1304, "serverTimeout"),
        (1400, "storageError"), (1401, "migrationFailed"),
        (1500, "malformedMessage"), (1501, "sanitizeFailed"), (1502, "attachmentTooBig"), (1503, "partNotFound"),
    ];

    [Fact]
    public void ErrorCodesAreNamed()
    {
        Assert.Equal(32, ErrorCode.All.Count);
        Assert.Equal(32, ErrorCode.All.Distinct().Count());
        Assert.Equal(GoCodes.Select(c => c.Code), ErrorCode.All.Select(c => c.Value));
        Assert.Equal(GoCodes.Select(c => c.Name), ErrorCode.All.Select(c => c.Name));
        foreach (var code in ErrorCode.All)
        {
            Assert.False(code.Name.StartsWith("unknown", StringComparison.Ordinal), $"{code.Value} has no name");
        }
        ErrorCode unauthenticated = ErrorCode.Unauthenticated;
        Assert.Equal(1005, unauthenticated.Value);
        Assert.Equal("unauthenticated", unauthenticated.Name);
        Assert.Equal(unauthenticated, new ErrorCode(1005));
        ErrorCode attachmentTooBig = ErrorCode.AttachmentTooBig;
        Assert.Equal("attachmentTooBig", attachmentTooBig.Name);
        Assert.Equal(1502, attachmentTooBig.Value);
        ErrorCode parseError = ErrorCode.ParseError;
        Assert.Equal(-32700, parseError.Value);
        Assert.Equal("parseError", parseError.Name);
        Assert.Equal("unknown(1234)", new ErrorCode(1234).Name);
        Assert.Equal("keyringError", $"{(ErrorCode)ErrorCode.KeyringError}");
        ErrorCode oauthClientMissing = ErrorCode.OAuthClientMissing;
        Assert.Equal(1203, oauthClientMissing.Value);
        Assert.Equal("oauthClientMissing", oauthClientMissing.Name);
        Assert.Equal(oauthClientMissing, new ErrorCode(1203));
        ErrorCode code1102 = 1102;
        Assert.Equal(ErrorCode.MessageNotFound, code1102);
    }

    // MARK: Params encoding

    [Fact]
    public void ParamsEncodeVerbatimWithoutNils()
    {
        var list = EncodeObject(new MessageListParams { AccountId = "acc_1", FolderId = "f_inbox", Page = new Page { Limit = 50 } });
        Assert.Equal("acc_1", list.GetProperty("accountId").GetString());
        Assert.Equal("f_inbox", list.GetProperty("folderId").GetString());
        Assert.Equal(50, list.GetProperty("page").GetProperty("limit").GetInt32());
        Assert.Null(Member(list.GetProperty("page"), "cursor"));
        Assert.Null(Member(list, "sort"));
        Assert.Null(Member(list, "filter"));
        Assert.Null(Member(list, "unreadOnly"));

        var filtered = EncodeObject(new MessageListParams
        {
            AccountId = "a",
            FolderId = "f",
            Page = new Page { Cursor = "c" },
            Sort = SortOrder.DateAsc,
            Filter = MessageFilter.Unread,
        });
        Assert.Equal("dateAsc", filtered.GetProperty("sort").GetString());
        Assert.Equal("unread", filtered.GetProperty("filter").GetString());
        Assert.Equal("c", filtered.GetProperty("page").GetProperty("cursor").GetString());

        var flag = EncodeObject(new MessageFlagParams { AccountId = "a", MessageIds = ["m1", "m2"], Set = [Flag.Seen] });
        Assert.Equal(["m1", "m2"], flag.GetProperty("messageIds").EnumerateArray().Select(e => e.GetString()));
        Assert.Equal(["seen"], flag.GetProperty("set").EnumerateArray().Select(e => e.GetString()));
        Assert.Null(Member(flag, "clear"));

        Assert.Empty(Keys(EncodeObject(new EmptyParams())));
        Assert.Empty(Keys(EncodeObject(new SyncTriggerParams()))); // all-optional params encode as {}

        var add = EncodeObject(new AccountAddParams
        {
            Config = new AccountConfig
            {
                Name = "Work",
                Email = "me@example.org",
                Imap = new ServerConfig { Host = "imap.example.org", Port = 993, Security = Security.Tls, Username = "me", AuthMethod = AuthMethod.Password },
                Smtp = new ServerConfig { Host = "smtp.example.org", Port = 587, Security = Security.Starttls, Username = "me", AuthMethod = AuthMethod.Password },
            },
            Credentials = new Credentials { Password = "secret" },
        });
        Assert.Equal("secret", add.GetProperty("credentials").GetProperty("password").GetString());
        Assert.Null(Member(add.GetProperty("credentials"), "oauthSession"));
        var session = EncodeObject(new AccountAddParams
        {
            Config = new AccountConfig { Name = "Gmail", Email = "me@gmail.com" },
            Credentials = new Credentials { OAuthSession = "s_1" },
        });
        Assert.Equal(["oauthSession"], Keys(session.GetProperty("credentials")));
        Assert.Equal("s_1", session.GetProperty("credentials").GetProperty("oauthSession").GetString());
        var update = EncodeObject(new AccountUpdateParams
        {
            AccountId = "acc_1",
            Config = new AccountConfig { Name = "Gmail", Email = "me@gmail.com" },
            Credentials = new Credentials { OAuthSession = "s_2" },
        });
        Assert.Equal("s_2", update.GetProperty("credentials").GetProperty("oauthSession").GetString());
        var cfg = add.GetProperty("config");
        Assert.Null(Member(cfg, "kind"));
        Assert.Equal("tls", cfg.GetProperty("imap").GetProperty("security").GetString());

        var remove = EncodeObject(new AccountRemoveParams { AccountId = "a", DeleteLocalData = false });
        Assert.False(remove.GetProperty("deleteLocalData").GetBoolean()); // a non-omitempty bool is always sent

        var create = EncodeObject(new DraftCreateParams { AccountId = "a", Mode = ComposeMode.ReplyAll, MessageId = "m", Attribution = "On x, y wrote:" });
        Assert.Equal("replyAll", create.GetProperty("mode").GetString());
        Assert.Null(Member(create, "mailto"));
        Assert.Equal("On x, y wrote:", create.GetProperty("attribution").GetString());
    }

    // MARK: Method table

    /// <summary>api.AllMethods, copied from backend/pkg/api/methods.go.</summary>
    internal static readonly string[] GoMethods =
    [
        "system.info", "system.hello", "system.authenticate",
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
    ];

    [Fact]
    public void MethodTableMatchesGo()
    {
        Assert.Equal(46, API.AllMethods.Count);
        Assert.Equal(46, API.AllMethods.Distinct().Count()); // no duplicates
        Assert.Equal(GoMethods, API.AllMethods);
        Assert.Equal(API.AllMethods.Count, API.Methods.Count);
        Assert.Equal(API.SystemInfoName, API.SystemInfo.Name);
        Assert.Equal(["notify.newMessage", "notify.syncState", "notify.authRequired", "notify.accountsChanged"], API.AllNotifications);
    }

    [Fact]
    public void TimeoutsFollowThePlan()
    {
        Assert.Equal(TimeSpan.FromSeconds(3), API.SystemInfo.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(5), RpcTimeouts.Handshake); // api.HandshakeTimeout
        Assert.Equal(RpcTimeouts.Handshake, API.SystemHello.Timeout);
        Assert.Equal(RpcTimeouts.Handshake, API.SystemAuthenticate.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(60), API.MessagePart.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(60), API.AttachmentGet.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(30), API.MessageEmbedded.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(30), API.DraftCreate.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(30), API.DraftOpen.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(30), API.AccountAdd.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(30), API.AccountUpdate.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(15), API.AccountDiscover.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(45), API.AccountTest.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(30), API.MessageBody.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(5), API.MessageList.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(5), API.SenderAdd.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(10), API.AccountOAuthStart.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(75), API.AccountOAuthWait.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(10), RpcTimeouts.OAuthStart);
        Assert.Equal(TimeSpan.FromSeconds(75), RpcTimeouts.OAuthWaitCall);
        Assert.Equal(TimeSpan.FromSeconds(5), API.AccountOAuthCancel.Timeout);
        Assert.Equal(TimeSpan.FromSeconds(5), RpcTimeouts.Default);
        Assert.Equal(TimeSpan.FromSeconds(30), RpcTimeouts.Remote);
        var special = new HashSet<string>(StringComparer.Ordinal)
        {
            "system.info", "system.hello", "system.authenticate", "message.body",
            "message.part", "attachment.get", "message.embedded", "draft.create", "draft.open",
            "account.add", "account.update", "account.discover", "account.test",
            "account.oauthStart", "account.oauthWait",
        };
        foreach (var m in API.Methods.Where(m => !special.Contains(m.Name)))
        {
            Assert.True(m.Timeout == RpcTimeouts.Default, $"{m.Name} should use the default timeout");
        }
    }

    [Fact]
    public void IdentifiersAreDistinctTypesOverBareStrings()
    {
        var id = new AccountId("acc_1");
        Assert.Equal("acc_1", id.Value);
        Assert.Equal("acc_1", id.ToString());
        Assert.True(id == "acc_1");
        Assert.Equal("""["acc_1"]""", Encoding.UTF8.GetString(JsonCoding.Encode<IReadOnlyList<AccountId>>([id])));
        // Swift decodes [FolderID]; the context declares the id lists the
        // contract has, [AccountId] among them, and every id alone.
        Assert.Equal<AccountId>(["acc_2"], Decode<IReadOnlyList<AccountId>>("""["acc_2"]"""));
        Assert.Equal(new FolderId("f_1"), Decode<FolderId>("\"f_1\""));
        Assert.Equal(2, new HashSet<MessageId> { "m", "m", "n" }.Count);
    }
}
