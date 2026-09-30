// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/APICodingTests.swift, the Jira
// accounts (docs/api.md §3, §4.1–§4.5, §4.12, §5; protocol 2, compatible
// addition): jiraAccountConfigRoundTrips, accountCapabilities,
// issueDecodesOnMessagesAndThreads, unknownIssueValuesDecode,
// virtualFoldersDecode, commentDraftsAndCrossAccountForward,
// jiraAccountMethods, issueTransitionMethods, messagesChangedNotification.
// Where Swift compares structs holding arrays, these compare their JSON
// (AssertSameValue): C# records compare lists by reference.

using System;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Xunit;
using static Malachi.Core.Tests.Api.ApiJson;

namespace Malachi.Core.Tests.Api;

public sealed partial class ApiCodingTests
{
    internal const string JiraConfigJson = """
        {"siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
         "login":"jana.dvorakova@acme.example","spaces":[{"id":"10001","key":"ITSD","name":"IT Service Desk"},{"id":"10002","key":"WEB"}],
         "offlineDays":90,"onlyMine":true,"hideEvents":true,"disabledFolders":["watching"],
         "closedStatuses":[{"id":"6","name":"Closed"},{"id":"10005"}],"notificationMail":"hide",
         "notificationSenders":["jira@acme.atlassian.net","@acme.example"],"botNames":["Relay Bot"],
         "metadataFilters":["^Sent from .*$"],"authorPrefixes":["[EXT]"]}
        """;

    // The issue's members without the braces (Swift issueJSON), for an
    // IssueInfo and, with the item's members after them, a MessageIssue.
    internal const string IssueJson = """
        "key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer on 3rd floor jams",
        "status":"In Progress","statusCategory":"inProgress","type":"Service Request","priority":"High",
        "assignee":"Jana Dvořáková","reporter":"Petr Novák","assignedToMe":true,"watching":true,
        "commentVisibilities":["public","internal"]
        """;

    internal const string IssueSummaryJson = """
        {"id":"m_j1","accountId":"acc_j","folderId":"f_itsd","threadId":"t_j",
         "from":[{"name":"Petr Novák","address":"petr.novak@acme.example"}],
         "subject":"ITSD-42: Printer on 3rd floor jams","date":"2026-09-02T10:00:00Z","snippet":"Still jams",
         "flags":["seen"],"hasAttachments":false,"size":321,
         "issue":{
        """ + IssueJson + """
        ,"item":"comment","visibility":"internal","via":"Relay Bot","edited":true}}
        """;

    internal const string EventSummaryJson = """
        {"id":"m_j2","accountId":"acc_j","folderId":"f_itsd","threadId":"t_j",
         "from":[{"name":"Jana Dvořáková","address":"jana.dvorakova@acme.example"}],
         "subject":"ITSD-42: Printer on 3rd floor jams","date":"2026-09-02T11:00:00Z","snippet":"To Do → In Progress",
         "flags":["seen"],"hasAttachments":false,"size":0,
         "issue":{"key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer on 3rd floor jams",
                  "status":"In Progress","item":"event",
                  "changes":[{"field":"status","from":"To Do","to":"In Progress"},{"field":"assignee","to":"Jana Dvořáková"}]}}
        """;

    private const string IdleStateJson = """{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0}""";

    private static readonly IssueInfo AcmeIssue = new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "Printer on 3rd floor jams",
        Status = "In Progress",
        StatusCategory = IssueStatusCategory.InProgress,
        Type = "Service Request",
        Priority = "High",
        Assignee = "Jana Dvořáková",
        Reporter = "Petr Novák",
        AssignedToMe = true,
        Watching = true,
        CommentVisibilities = [CommentVisibility.Public, CommentVisibility.Internal],
    };

    /// <summary>
    /// Every JiraConfig member survives a decode and an encode (an
    /// account.update built from account.list must not drop one), and
    /// nothing unset is written.
    /// </summary>
    [Fact]
    public void JiraAccountConfigRoundTrips()
    {
        var r = Decode<AccountListResult>($$$"""
            {"accounts":[{"id":"acc_j","config":{"name":"Acme Jira","email":"jana.dvorakova@acme.example","kind":"jira",
               "jira":{{{JiraConfigJson}}},"syncIntervalSeconds":300},
              "enabled":true,"state":{"accountId":"acc_j","status":"idle","progress":-1,"pendingOutbox":0},
              "capabilities":[]}]}
            """);
        var acc = Assert.Single(r.Accounts);
        Assert.Equal(AccountKind.Jira, acc.Config.ProtocolKind);
        Assert.Equal<AccountKind?>(AccountKind.Jira, acc.Config.Kind);
        Assert.Null(acc.Config.Imap);
        Assert.Null(acc.Config.Smtp);
        Assert.Null(acc.Config.Graph);
        Assert.Null(acc.Config.OAuth2);
        var j = Assert.IsType<JiraConfig>(acc.Config.Jira);
        AssertSameValue(
            new JiraConfig
            {
                SiteUrl = "https://acme.atlassian.net",
                Deployment = JiraDeployment.Cloud,
                CloudId = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
                Login = "jana.dvorakova@acme.example",
                Spaces = [new SpaceRef { Id = "10001", Key = "ITSD", Name = "IT Service Desk" }, new SpaceRef { Id = "10002", Key = "WEB" }],
                OfflineDays = 90,
                OnlyMine = true,
                HideEvents = true,
                DisabledFolders = [VirtualFolder.Watching],
                ClosedStatuses = [new StatusRef { Id = "6", Name = "Closed" }, new StatusRef { Id = "10005" }],
                NotificationMail = NotificationMailMode.Hide,
                NotificationSenders = ["jira@acme.atlassian.net", "@acme.example"],
                BotNames = ["Relay Bot"],
                MetadataFilters = ["^Sent from .*$"],
                AuthorPrefixes = ["[EXT]"],
            },
            j);

        // account.update echoes it verbatim, key for key.
        var update = EncodeObject(new AccountUpdateParams { AccountId = acc.Id, Config = acc.Config });
        var cfg = update.GetProperty("config");
        Assert.Equal(["email", "jira", "kind", "name", "syncIntervalSeconds"], Keys(cfg));
        AssertSameJson(JiraConfigJson, cfg.GetProperty("jira").GetRawText()); // every member, with the same values
        AssertSameValue(acc.Config, Decode<AccountConfig>(JsonCoding.EncodeToString(acc.Config)));

        // A minimal block: the three members Go writes without omitempty, nothing else.
        var obj = EncodeObject(new JiraConfig
        {
            SiteUrl = "https://jira.acme.example/jira",
            Deployment = JiraDeployment.Datacenter,
            Spaces = [new SpaceRef { Id = "1", Key = "MOB" }],
        });
        Assert.Equal(["deployment", "siteUrl", "spaces"], Keys(obj)); // null lists are left out
        Assert.Equal("datacenter", obj.GetProperty("deployment").GetString());
        Assert.Equal(["id", "key"], Keys(obj.GetProperty("spaces")[0]));
        var noSpaces = EncodeObject(new JiraConfig { SiteUrl = "https://a.example", Deployment = JiraDeployment.Cloud });
        Assert.Equal(JsonValueKind.Array, noSpaces.GetProperty("spaces").ValueKind); // spaces is always written
        // Set values are written, false and 0 included.
        var off = EncodeObject(new JiraConfig { SiteUrl = "https://a.example", Deployment = JiraDeployment.Cloud, OfflineDays = 0, OnlyMine = false });
        Assert.Equal(0, off.GetProperty("offlineDays").GetInt32());
        Assert.False(off.GetProperty("onlyMine").GetBoolean());
        Assert.Null(Member(off, "hideEvents"));

        // What an older or terser daemon leaves out decodes as the default.
        var bare = Decode<JiraConfig>("""{"siteUrl":"https://a.example","deployment":"cloud","spaces":null}""");
        AssertSameValue(new JiraConfig { SiteUrl = "https://a.example", Deployment = JiraDeployment.Cloud }, bare);
        Assert.Null(bare.CloudId);
        Assert.Null(bare.OfflineDays);
        Assert.Null(bare.NotificationMail);
        Assert.Null(bare.BotNames);
        Assert.Empty(bare.Spaces);
        // An unknown deployment decodes as itself.
        Assert.Equal(new JiraDeployment("server"), Decode<JiraConfig>("""{"siteUrl":"https://a.example","deployment":"server"}""").Deployment);

        Assert.Equal(200, API.Limits.MaxJiraSpaces);
        Assert.Equal(64, API.Limits.MaxJiraStatuses);
        Assert.Equal(32, API.Limits.MaxJiraListEntries);
        Assert.Equal(512, API.Limits.MaxJiraPatternBytes);
        Assert.Equal(365, API.Limits.MaxJiraOfflineDays);
        Assert.Equal(30, API.Limits.DefaultJiraOfflineDays);
    }

    /// <summary>
    /// Account.Capabilities: absent (an older daemon) is null and reads as
    /// the mail set; an empty list (a Jira account) can do none of them.
    /// </summary>
    [Fact]
    public void AccountCapabilities()
    {
        var old = Decode<Account>($$$"""{"id":"a","config":{"name":"n","email":"e@x"},"enabled":true,"state":{{{IdleStateJson}}}}""");
        Assert.Null(old.Capabilities); // missing is null, not []
        var nul = Decode<Account>($$$"""{"id":"a","config":{"name":"n","email":"e@x"},"enabled":true,"state":{{{IdleStateJson}}},"capabilities":null}""");
        Assert.Null(nul.Capabilities);
        foreach (var c in new Capability[] { Capability.Compose, Capability.Reply, Capability.ReplyAll, Capability.Forward, Capability.Move, Capability.Delete })
        {
            Assert.True(old.Can(c), $"{c} is a mail capability");
        }
        Assert.False(old.Can(Capability.Comment));
        Assert.False(old.Can("archive"));
        Assert.Equal<Capability>(
            [Capability.Compose, Capability.Reply, Capability.ReplyAll, Capability.Forward, Capability.Move, Capability.Delete],
            API.MailCapabilities);

        var mail = Decode<Account>($$$"""
            {"id":"a","config":{"name":"n","email":"e@x"},"enabled":true,"state":{{{IdleStateJson}}},
             "capabilities":["compose","reply","replyAll","forward","move","delete"]}
            """);
        Assert.Equal(API.MailCapabilities, mail.Capabilities);
        Assert.True(mail.Can(Capability.Delete));
        Assert.False(mail.Can(Capability.Comment));

        var m1 = Decode<Account>($$$"""{"id":"j","config":{"name":"n","email":"e@x","kind":"jira"},"enabled":true,"state":{{{IdleStateJson}}},"capabilities":[]}""");
        Assert.NotNull(m1.Capabilities);
        Assert.Empty(m1.Capabilities);
        foreach (var c in new Capability[] { Capability.Compose, Capability.Reply, Capability.ReplyAll, Capability.Forward, Capability.Comment, Capability.Move, Capability.Delete })
        {
            Assert.False(m1.Can(c), $"{c} is not a capability of an empty list");
        }
        var m2 = Decode<Account>($$$"""{"id":"j","config":{"name":"n","email":"e@x","kind":"jira"},"enabled":true,"state":{{{IdleStateJson}}},"capabilities":["comment","forward","teleport"]}""");
        Assert.True(m2.Can(Capability.Comment) && m2.Can(Capability.Forward));
        Assert.False(m2.Can(Capability.Reply) || m2.Can(Capability.Compose));
        Assert.Equal(new Capability("teleport"), m2.Capabilities![^1]); // an unknown capability decodes as itself

        // Written only when known; an empty list stays an empty list.
        Assert.Null(Member(EncodeObject(old), "capabilities"));
        Assert.Equal(0, EncodeObject(m1).GetProperty("capabilities").GetArrayLength());
        var back = Decode<Account>(JsonCoding.EncodeToString(m1));
        Assert.NotNull(back.Capabilities);
        Assert.Empty(back.Capabilities);
        var built = new Account
        {
            Id = "a",
            Config = new AccountConfig { Name = "n", Email = "e@x" },
            Enabled = true,
            State = new SyncState { AccountId = "a", Status = SyncStatus.Idle },
        };
        Assert.True(built.Can(Capability.Forward));
    }

    /// <summary>
    /// MessageSummary.Issue flattens IssueInfo beside the item's members, on
    /// message.list, message.get (itself flattened) and thread members;
    /// ThreadSummary.Issue is the plain IssueInfo.
    /// </summary>
    [Fact]
    public void IssueDecodesOnMessagesAndThreads()
    {
        var list = Decode<MessageListResult>($$$"""{"messages":[{{{IssueSummaryJson}}},{{{SummaryJson}}}],"page":{"total":2}}""");
        var issue = Assert.IsType<MessageIssue>(list.Messages[0].Issue);
        AssertSameValue(AcmeIssue, issue.Info);
        Assert.Equal(IssueItemKind.Comment, issue.Item);
        Assert.Equal<CommentVisibility?>(CommentVisibility.Internal, issue.Visibility);
        Assert.Equal("Relay Bot", issue.Via);
        Assert.True(issue.Edited);
        Assert.Null(issue.Changes);
        Assert.Null(issue.Mine); // a relayed comment is never the user's own
        Assert.Null(list.Messages[1].Issue); // a mail message has none

        var get = Decode<MessageGetResult>($$$"""
            {"message":{"id":"m_j1","accountId":"acc_j","folderId":"f_itsd","threadId":"t_j",
              "from":[{"name":"Petr Novák","address":"petr.novak@acme.example"}],
              "subject":"ITSD-42: Printer on 3rd floor jams","date":"2026-09-02T10:00:00Z","snippet":"Still jams",
              "flags":[],"hasAttachments":false,"size":321,
              "issue":{{{{IssueJson}}},"item":"description","mine":true},
              "attachments":[]}}
            """);
        var m = get.Message;
        Assert.Equal(IssueItemKind.Description, m.Summary.Issue!.Item);
        Assert.Equal("ITSD-42", m.Summary.Issue.Info.Key);
        Assert.Null(m.Summary.Issue.Visibility);
        Assert.Null(m.Summary.Issue.Via);
        Assert.Null(m.Summary.Issue.Edited);
        Assert.True(m.Summary.Issue.Mine); // the account's own user wrote it

        // The encoding flattens too: no "info" and no "summary" key.
        var obj = EncodeObject(m);
        var wire = obj.GetProperty("issue");
        Assert.Null(Member(obj, "summary"));
        Assert.Null(Member(wire, "info"));
        Assert.Equal("ITSD-42", wire.GetProperty("key").GetString());
        Assert.Equal("description", wire.GetProperty("item").GetString());
        Assert.Null(Member(wire, "visibility")); // omitempty
        Assert.Null(Member(wire, "changes"));
        Assert.True(wire.GetProperty("mine").GetBoolean());
        AssertSameValue(m, Decode<Message>(JsonCoding.EncodeToString(m)));
        // mine is written only when known (omitempty).
        var relayed = EncodeObject(list.Messages[0]).GetProperty("issue");
        Assert.Null(Member(relayed, "mine"));
        Assert.Equal("Relay Bot", relayed.GetProperty("via").GetString());
        Assert.False(Decode<MessageIssue>("""{"key":"WEB-1","url":"u","summary":"s","status":"Open","item":"comment","mine":false}""").Mine);

        // An event row: its changes, no visibility.
        var eventRow = Decode<MessageSummary>(EventSummaryJson);
        var e = Assert.IsType<MessageIssue>(eventRow.Issue);
        Assert.Equal(IssueItemKind.Event, e.Item);
        Assert.Null(e.Visibility);
        Assert.Equal<IssueChange>(
            [
                new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" },
                new IssueChange { Field = IssueField.Assignee, To = "Jana Dvořáková" },
            ],
            e.Changes!);
        Assert.Null(e.Info.StatusCategory);
        Assert.Null(e.Info.Assignee);
        Assert.Null(e.Info.CommentVisibilities);
        AssertSameValue(eventRow, Decode<MessageSummary>(JsonCoding.EncodeToString(eventRow)));

        // thread.list: the thread carries the issue, its latest member (an event here) its own.
        var threads = Decode<ThreadListResult>($$$$"""
            {"threads":[{"id":"t_j","accountId":"acc_j","subject":"ITSD-42: Printer on 3rd floor jams",
              "participants":[{"name":"Jana Dvořáková","address":"jana.dvorakova@acme.example"}],
              "messageCount":3,"unreadCount":0,"latestDate":"2026-09-02T11:00:00Z",
              "latest":{{{{EventSummaryJson}}}},"snippet":"To Do → In Progress","flags":["seen"],"hasAttachments":false,
              "folderIds":["f_itsd","f_assigned"],"issue":{{{{{IssueJson}}}}}},
              {{{{ThreadJson}}}}],"page":{"total":2}}
            """);
        var t = threads.Threads[0];
        Assert.Equal("ITSD-42", t.Issue!.Key);
        Assert.Equal<CommentVisibility>([CommentVisibility.Public, CommentVisibility.Internal], t.Issue.CommentVisibilities!);
        Assert.Equal(IssueItemKind.Event, t.Latest.Issue!.Item);
        Assert.Equal(2, t.Latest.Issue.Changes!.Count);
        Assert.Null(threads.Threads[1].Issue);
        var get2 = Decode<ThreadGetResult>($$$"""{"thread":{{{ThreadJson}}},"messages":[{{{IssueSummaryJson}}},{{{EventSummaryJson}}}]}""");
        Assert.Equal<IssueItemKind?>([IssueItemKind.Comment, IssueItemKind.Event], get2.Messages.Select(x => x.Issue?.Item));
        Assert.Equal(JsonValueKind.Object, EncodeObject(t).GetProperty("issue").ValueKind);
        Assert.Null(Member(EncodeObject(threads.Threads[1]), "issue"));

        // Search results and notify.newMessage carry MessageSummary as it is.
        var search = Decode<SearchQueryResult>($$$"""{"results":[{"message":{{{IssueSummaryJson}}},"snippet":"x","ranges":[],"score":1}],"page":{"total":1}}""");
        Assert.Equal("ITSD-42", search.Results[0].Message.Issue!.Info.Key);
    }

    /// <summary>
    /// Open enums of the issue projection: a newer daemon's value decodes as
    /// itself; the empty category is not a known one.
    /// </summary>
    [Fact]
    public void UnknownIssueValuesDecode()
    {
        var i = Decode<IssueInfo>("""{"key":"WEB-1","url":"https://acme.atlassian.net/browse/WEB-1","summary":"s","status":"Blocked","statusCategory":"blocked"}""");
        Assert.Equal<IssueStatusCategory?>(new IssueStatusCategory("blocked"), i.StatusCategory);
        Assert.NotEqual<IssueStatusCategory?>(IssueStatusCategory.Todo, i.StatusCategory);
        Assert.NotEqual<IssueStatusCategory?>(IssueStatusCategory.InProgress, i.StatusCategory);
        Assert.NotEqual<IssueStatusCategory?>(IssueStatusCategory.Done, i.StatusCategory);
        var empty = Decode<IssueInfo>("""{"key":"WEB-1","url":"u","summary":"s","status":"","statusCategory":""}""");
        Assert.Equal<IssueStatusCategory?>(new IssueStatusCategory(""), empty.StatusCategory);
        Assert.Equal("", empty.Status);
        var odd = Decode<MessageIssue>("""
            {"key":"MOB-7","url":"u","summary":"s","status":"Open","item":"worklog","visibility":"partners",
             "changes":[{"field":"priority","from":"Low","to":"High"}]}
            """);
        Assert.Equal(new IssueItemKind("worklog"), odd.Item);
        Assert.Equal<CommentVisibility?>(new CommentVisibility("partners"), odd.Visibility);
        Assert.Equal(new IssueField("priority"), odd.Changes![0].Field);
        // item is required.
        Assert.Throws<JsonException>(() => Decode<MessageIssue>("""{"key":"MOB-7","url":"u","summary":"s","status":"Open"}"""));
        Assert.Equal("public", new CommentVisibility(CommentVisibility.Public).Value);
        Assert.Equal("internal", new CommentVisibility(CommentVisibility.Internal).Value);
        Assert.Equal("description", new IssueItemKind(IssueItemKind.Description).Value);
        Assert.Equal("event", new IssueItemKind(IssueItemKind.Event).ToString());
        Assert.Equal("open", new VirtualFolder(VirtualFolder.Open).Value);
        Assert.Equal("sync", new NotificationMailMode(NotificationMailMode.Sync).Value);
    }

    /// <summary>
    /// Folder.Virtual (docs/api.md §4.2): the virtual folders of a jira
    /// account first, role none; absent on every other folder.
    /// </summary>
    [Fact]
    public void VirtualFoldersDecode()
    {
        var r = Decode<FolderListResult>("""
            {"folders":[
              {"id":"f_a","accountId":"acc_j","name":"Assigned to Me","path":"Assigned to Me","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":2,"total":7,"virtual":"assignedToMe"},
              {"id":"f_w","accountId":"acc_j","name":"Watching","path":"Watching","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":3,"virtual":"watching"},
              {"id":"f_o","accountId":"acc_j","name":"Open","path":"Open","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":2,"total":9,"virtual":"open"},
              {"id":"f_x","accountId":"acc_j","name":"Later","path":"Later","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0,"virtual":"starred"},
              {"id":"f_itsd","accountId":"acc_j","name":"IT Service Desk","path":"IT Service Desk","role":"none","subscribed":true,"selectable":true,"synced":true,"unread":1,"total":40}
            ]}
            """);
        Assert.Equal<VirtualFolder?>(
            [VirtualFolder.AssignedToMe, VirtualFolder.Watching, VirtualFolder.Open, new VirtualFolder("starred"), null],
            r.Folders.Select(f => f.Virtual));
        Assert.All(r.Folders, f => Assert.Equal(FolderRole.None, f.Role));
        Assert.Equal("assignedToMe", EncodeObject(r.Folders[0]).GetProperty("virtual").GetString());
        Assert.Null(Member(EncodeObject(r.Folders[4]), "virtual"));
        var plain = new Folder
        {
            Id = "f",
            AccountId = "a",
            Name = "n",
            Path = "n",
            Role = FolderRole.Inbox,
            Subscribed = true,
            Selectable = true,
            Synced = true,
            Unread = 0,
            Total = 0,
        };
        Assert.Null(plain.Virtual);
    }

    /// <summary>
    /// Comment drafts (docs/api.md §4.5): Draft.Comment comes with a
    /// draft.create reply on a jira account and goes back in draft.save;
    /// DraftCreateParams.MessageAccountId only when set.
    /// </summary>
    [Fact]
    public void CommentDraftsAndCrossAccountForward()
    {
        var r = Decode<DraftCreateResult>($$$$"""
            {"draft":{"accountId":"acc_j","version":0,"to":[],"subject":"ITSD-42: Printer on 3rd floor jams","textBody":"",
                      "inReplyTo":"m_j1","updatedAt":"0001-01-01T00:00:00Z",
                      "comment":{"issue":{{{{{IssueJson}}}}},"visibility":""}},
             "quoted":"none",
             "blocked":{"remoteImages":0,"remoteStyles":0,"remoteFonts":0,"scripts":0,"forms":0,"eventHandlers":0,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":0}}
            """);
        var comment = Assert.IsType<DraftComment>(r.Draft.Comment);
        Assert.Equal("ITSD-42", comment.Issue.Key);
        Assert.Equal<CommentVisibility>([CommentVisibility.Public, CommentVisibility.Internal], comment.Issue.CommentVisibilities!);
        Assert.Equal(new CommentVisibility(""), comment.Visibility); // empty is public
        Assert.Equal(QuoteForm.None, r.Quoted);
        Assert.Empty(r.Draft.To);

        var d = r.Draft with { Comment = comment with { Visibility = CommentVisibility.Internal } };
        var obj = EncodeObject(new DraftSaveParams { Draft = d });
        var sent = obj.GetProperty("draft").GetProperty("comment");
        Assert.Equal("internal", sent.GetProperty("visibility").GetString());
        Assert.Equal("ITSD-42", sent.GetProperty("issue").GetProperty("key").GetString());
        AssertSameValue(d, Decode<Draft>(JsonCoding.EncodeToString(d)));
        // A mail draft has none, and does not write one.
        var mail = EncodeObject(new DraftSaveParams { Draft = new Draft { AccountId = "a" } });
        Assert.Null(Member(mail.GetProperty("draft"), "comment"));
        Assert.Null(Decode<Draft>("""{"accountId":"a","version":1,"to":[],"subject":"s","textBody":"t","updatedAt":"2026-09-02T10:00:00Z"}""").Comment);
        Assert.Equal(new CommentVisibility(""), new DraftComment { Issue = comment.Issue }.Visibility);

        // Forward of a jira message from a mail account.
        var fwd = EncodeObject(new DraftCreateParams
        {
            AccountId = "acc_mail",
            Mode = ComposeMode.Forward,
            MessageId = "m_j1",
            Attribution = "---",
            MessageAccountId = "acc_j",
        });
        Assert.Equal("acc_j", fwd.GetProperty("messageAccountId").GetString());
        Assert.Equal("acc_mail", fwd.GetProperty("accountId").GetString());
        var reply = EncodeObject(new DraftCreateParams { AccountId = "acc_j", Mode = ComposeMode.Reply, MessageId = "m_j1" });
        Assert.Equal(["accountId", "messageId", "mode"], Keys(reply)); // messageAccountId is left out when null
        Assert.Equal<AccountId?>(new AccountId("j"), Decode<DraftCreateParams>("""{"accountId":"a","mode":"forward","messageId":"m","messageAccountId":"j"}""").MessageAccountId);
    }

    /// <summary>account.detectSite, account.listSpaces and the jira probe of account.test (docs/api.md §4.1).</summary>
    [Fact]
    public void JiraAccountMethods()
    {
        AssertSameJson("""{"url":"acme.atlassian.net"}""", EncodeObject(new AccountDetectSiteParams { Url = "acme.atlassian.net" }).GetRawText());
        var cloud = Decode<AccountDetectSiteResult>("""
            {"kind":"jira","siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","title":"Acme","version":"1001.0.0-SNAPSHOT"}
            """);
        Assert.Equal(
            new AccountDetectSiteResult
            {
                Kind = AccountKind.Jira,
                SiteUrl = "https://acme.atlassian.net",
                Deployment = JiraDeployment.Cloud,
                CloudId = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
                Title = "Acme",
                Version = "1001.0.0-SNAPSHOT",
            },
            cloud);
        var dc = Decode<AccountDetectSiteResult>("""{"kind":"jira","siteUrl":"https://jira.acme.example/jira","deployment":"datacenter"}""");
        Assert.Equal(JiraDeployment.Datacenter, dc.Deployment);
        Assert.Null(dc.CloudId);
        Assert.Null(dc.Title);
        Assert.Null(dc.Version);

        var config = new AccountConfig
        {
            Name = "",
            Email = "jana.dvorakova@acme.example",
            Kind = AccountKind.Jira,
            Jira = new JiraConfig { SiteUrl = cloud.SiteUrl, Deployment = JiraDeployment.Cloud, CloudId = cloud.CloudId, Login = "jana.dvorakova@acme.example" },
        };
        var parameters = EncodeObject(new AccountListSpacesParams { Config = config, Credentials = new Credentials { Password = "token" }, Counts = true });
        Assert.Equal(["config", "counts", "credentials"], Keys(parameters)); // accountId is left out when null
        Assert.True(parameters.GetProperty("counts").GetBoolean());
        Assert.Equal("token", parameters.GetProperty("credentials").GetProperty("password").GetString());
        var jira = parameters.GetProperty("config").GetProperty("jira");
        Assert.Equal(["cloudId", "deployment", "login", "siteUrl", "spaces"], Keys(jira));
        Assert.Equal(0, jira.GetProperty("spaces").GetArrayLength());
        var editing = EncodeObject(new AccountListSpacesParams { AccountId = "acc_j", Config = config });
        Assert.Equal("acc_j", editing.GetProperty("accountId").GetString());
        Assert.Null(Member(editing, "counts"));
        Assert.Empty(Keys(editing.GetProperty("credentials"))); // no password: the stored token

        var spaces = Decode<AccountListSpacesResult>("""
            {"user":{"name":"Jana Dvořáková","email":"jana.dvorakova@acme.example"},
             "spaces":[{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":120},
                       {"id":"10002","key":"WEB","name":"Website","issues":-1}],
             "statuses":[{"id":"1","name":"To Do","category":"todo"},{"id":"6","name":"Closed","category":"done"},{"id":"9","name":"Odd","category":""}]}
            """);
        Assert.Equal(new SiteUser { Name = "Jana Dvořáková", Email = "jana.dvorakova@acme.example" }, spaces.User);
        Assert.Equal<Space>(
            [
                new Space { Id = "10001", Key = "ITSD", Name = "IT Service Desk", ServiceDesk = true, Issues = 120 },
                new Space { Id = "10002", Key = "WEB", Name = "Website", Issues = -1 },
            ],
            spaces.Spaces);
        Assert.Equal<IssueStatusCategory>([IssueStatusCategory.Todo, IssueStatusCategory.Done, ""], spaces.Statuses.Select(s => s.Category));
        var none = Decode<AccountListSpacesResult>("""{"user":{"name":"jdvorakova"},"spaces":null,"statuses":null}""");
        Assert.Null(none.User.Email);
        Assert.Empty(none.Spaces);
        Assert.Empty(none.Statuses);

        var test = Decode<AccountTestResult>("""{"jira":{"ok":true,"capabilities":["cloud","gateway"],"latencyMs":210}}""");
        AssertSameValue(new EndpointTestResult { Ok = true, Capabilities = ["cloud", "gateway"], LatencyMs = 210 }, test.Jira);
        Assert.Null(test.Imap);
        Assert.Null(test.Smtp);
        Assert.Null(test.Graph);
        Assert.Null(Member(EncodeObject(new AccountTestResult { Imap = new EndpointTestResult { Ok = true, LatencyMs = 1 } }), "jira"));
    }

    /// <summary>
    /// issue.transitions and issue.transition (docs/api.md §4.12): the params
    /// encode with the JSON names of pkg/api, the results decode, needsInput
    /// and toCategory are optional on the wire, and the transition capability
    /// is one more Capability.
    /// </summary>
    [Fact]
    public void IssueTransitionMethods()
    {
        AssertSameJson("""{"accountId":"acc_j","messageId":"m_j1"}""", EncodeObject(new IssueTransitionsParams { AccountId = "acc_j", MessageId = "m_j1" }).GetRawText());
        AssertSameJson(
            """{"accountId":"acc_j","messageId":"m_j1","transitionId":"31"}""",
            EncodeObject(new IssueTransitionParams { AccountId = "acc_j", MessageId = "m_j1", TransitionId = "31" }).GetRawText());

        var r = Decode<IssueTransitionsResult>($$$$"""
            {"issue":{{{{{IssueJson}}}}},
             "transitions":[{"id":"11","name":"Start Progress","to":"In Progress","toCategory":"inProgress"},
                            {"id":"21","name":"Done","to":"Done","toCategory":"done","needsInput":true},
                            {"id":"41","name":"Escalate","to":"Escalated","toCategory":"blocked"}]}
            """);
        Assert.Equal("ITSD-42", r.Issue.Key);
        Assert.Equal("In Progress", r.Issue.Status);
        Assert.Equal<CommentVisibility>([CommentVisibility.Public, CommentVisibility.Internal], r.Issue.CommentVisibilities!);
        Assert.Equal<IssueTransition>(
            [
                new IssueTransition { Id = "11", Name = "Start Progress", To = "In Progress", ToCategory = IssueStatusCategory.InProgress },
                new IssueTransition { Id = "21", Name = "Done", To = "Done", ToCategory = IssueStatusCategory.Done, NeedsInput = true },
                new IssueTransition { Id = "41", Name = "Escalate", To = "Escalated", ToCategory = new IssueStatusCategory("blocked") },
            ],
            r.Transitions);
        Assert.Null(r.Transitions[0].NeedsInput);
        var none = Decode<IssueTransitionsResult>($$$$"""{"issue":{{{{{IssueJson}}}}},"transitions":null}""");
        Assert.Empty(none.Transitions); // never null on the wire, but an old fixture may say so
        var done = Decode<IssueTransitionResult>("""{"issue":{"key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer","status":"Done","statusCategory":"done"}}""");
        Assert.Equal("Done", done.Issue.Status);
        Assert.Equal<IssueStatusCategory?>(IssueStatusCategory.Done, done.Issue.StatusCategory);
        Assert.Null(done.Issue.Assignee);

        var acc = Decode<Account>($$$"""{"id":"j","config":{"name":"n","email":"e@x","kind":"jira"},"enabled":true,"state":{{{IdleStateJson}}},"capabilities":["comment","forward","transition"]}""");
        Assert.True(acc.Can(Capability.Transition));
        Assert.Equal("transition", new Capability(Capability.Transition).Value);
        Assert.DoesNotContain(new Capability(Capability.Transition), API.MailCapabilities); // mail accounts never change statuses
        Assert.Equal(100, API.Limits.MaxIssueTransitions);
    }

    /// <summary>notify.messagesChanged (docs/api.md §5): the payload decodes, also as a notification.</summary>
    [Fact]
    public void MessagesChangedNotification()
    {
        var n = Decode<MessagesChangedNotification>("""{"accountId":"acc_mail","folderIds":["f_inbox"]}""");
        AssertSameValue(new MessagesChangedNotification { AccountId = "acc_mail", FolderIds = ["f_inbox"] }, n);
        Assert.Empty(Decode<MessagesChangedNotification>("""{"accountId":"acc_mail"}""").FolderIds);
        var line = Encoding.UTF8.GetBytes("""{"jsonrpc":"2.0","method":"notify.messagesChanged","params":{"accountId":"acc_mail","folderIds":["f_inbox"]}}""");
        var decoded = Assert.IsType<DaemonNotification.MessagesChanged>(DaemonNotification.Decode(API.Notify.MessagesChanged, line));
        AssertSameValue(n, decoded.Payload);
    }
}
