// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of PrintComposer, the Windows home of macOS's PrintController (Mail
// and Calendar parts): the layout of a selection, whose messages are the
// user's own, a message's printed header and body, an event's lines, and
// over a FakeDaemon the print of messages, which asks message.body for the
// whole body (never trimQuoted) without switching the variant the cache
// holds for the reader, puts a conversation oldest first and takes at most
// Print.MaxMessages.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Printing;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Printing;

public sealed class PrintComposerTests
{
    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    private static MessageSummary Summary(string id, long seconds = 1_700_000_000, string folder = "fld_in") => new()
    {
        Id = id,
        AccountId = "acc_1",
        FolderId = folder,
        From = [new Address { Name = "Alice", Email = "alice@example.com" }],
        To = [new Address { Name = "Bob", Email = "bob@example.com" }],
        Subject = $"Hello {id}",
        Date = DateTimeOffset.FromUnixTimeSeconds(seconds),
        Snippet = $"snippet of {id}",
        Flags = [],
        HasAttachments = false,
        Size = 10,
    };

    [Fact]
    public void LayoutOfASelection()
    {
        Assert.Equal(Print.Layout.One, PrintComposer.LayoutFor(1, 1));
        Assert.Equal(Print.Layout.Conversation, PrintComposer.LayoutFor(1, 3));
        Assert.Equal(Print.Layout.Pages, PrintComposer.LayoutFor(2, 2));
        Assert.Equal(Print.Layout.Pages, PrintComposer.LayoutFor(2, 5));
    }

    [Fact]
    public void OwnMessagesAreOfSentDraftsAndOutbox()
    {
        Assert.True(PrintComposer.IsOwn(FolderRole.Sent));
        Assert.True(PrintComposer.IsOwn(FolderRole.Drafts));
        Assert.True(PrintComposer.IsOwn(FolderRole.Outbox));
        Assert.False(PrintComposer.IsOwn(FolderRole.Inbox));
        Assert.False(PrintComposer.IsOwn(FolderRole.None));
    }

    [Fact]
    public void HeaderHasBccOnlyOfOwnMessagesAndNamesTheAttachments()
    {
        var s = Summary("a");
        var m = new Message
        {
            Summary = s,
            Cc = [new Address { Name = "Carol", Email = "carol@example.com" }],
            Bcc = [new Address { Name = "Me", Email = "me@example.com" }],
            Attachments =
            [
                new Attachment { PartId = "2", Filename = "report.pdf", ContentType = "application/pdf", Size = 41_100, Inline = false },
                new Attachment { PartId = "3", Filename = "logo.png", ContentType = "image/png", Size = 900, Inline = true, ContentId = "logo" },
            ],
        };
        var body = new MessageBodyResult
        {
            MessageId = "a",
            BodyState = BodyState.Fetched,
            HasHtml = true,
            Html = "<p>x</p>",
            Text = "x",
            InlineParts = new Dictionary<string, string> { ["logo"] = "3" },
            RemoteContent = RemoteContentPolicy.Block,
            SanitizerVersion = "1",
        };
        var own = PrintComposer.HeaderOf(s, m, body, own: true);
        Assert.Equal("Hello a", own.Subject);
        Assert.Equal("Alice", own.From);
        Assert.Equal("Bob", own.To);
        Assert.Equal("Carol", own.Cc);
        Assert.Equal("Me", own.Bcc);
        Assert.NotEqual("", own.Date);
        // The picture the HTML shows through cid: is no tile, so no attachment.
        Assert.Equal(["report.pdf (" + Malachi.Core.Text.Format.FormatSize(41_100) + ")"], own.Attachments);
        Assert.Equal("", PrintComposer.HeaderOf(s, m, body, own: false).Bcc);
        // Without message.get: the list's summary, no copies, no date of Go's zero time.
        var bare = PrintComposer.HeaderOf(s with { Date = DateTimeOffset.GoZero }, null, null, own: true);
        Assert.Equal(("", "", ""), (bare.Cc, bare.Bcc, bare.Date));
        Assert.Empty(bare.Attachments);
    }

    [Fact]
    public void BodyIsTheSanitisedHtmlElseTheText()
    {
        MessageBodyResult Body(string? html, bool? withheld = null, BodyState? state = null) => new()
        {
            MessageId = "a",
            BodyState = state ?? BodyState.Fetched,
            HasHtml = html is not null,
            Html = html,
            HtmlWithheld = withheld,
            Text = "plain",
            RemoteContent = RemoteContentPolicy.Block,
            SanitizerVersion = "1",
        };
        Assert.Equal(new Print.Body(Html: "<p>x</p>"), PrintComposer.BodyOf(Body("<p>x</p>"), "snip"));
        Assert.Equal(new Print.Body(Text: "plain"), PrintComposer.BodyOf(Body(null), "snip"));
        Assert.Equal(new Print.Body(Text: "plain"), PrintComposer.BodyOf(Body("<p>x</p>", withheld: true), "snip"));
        Assert.Equal(new Print.Body(Text: "plain"), PrintComposer.BodyOf(Body("<p>x</p>", state: BodyState.Pending), "snip"));
        Assert.Equal(new Print.Body(Text: "snip"), PrintComposer.BodyOf(null, "snip"));
    }

    [Fact]
    public void EventLinesAndAttendees()
    {
        var d = new Cal.Detail
        {
            Title = "Review",
            TimeLine = "Wed 4 Nov, 9:00–10:00",
            Recurrence = "",
            Location = "Room 1",
            CalendarLine = "Work",
            PeopleLine = "3 people",
            Attendees =
            [
                new Cal.AttendeeRow("Ann", "ann@example.com", EventResponse.Accepted, "Accepted", false, false, false),
                new Cal.AttendeeRow("", "bob@example.com", EventResponse.NeedsAction, "", false, false, false),
            ],
            Notes = "Bring the draft",
        };
        var e = PrintComposer.EventOf(d);
        Assert.Equal("Review", e.Title);
        Assert.Equal(["Wed 4 Nov, 9:00–10:00", "", "Room 1", "Work", "3 people"], e.Lines);
        Assert.Equal(["Ann — Accepted", "bob@example.com"], e.Attendees);
        Assert.Equal("Bring the draft", e.Notes);
    }

    [Fact]
    public async Task AConversationPrintsWholeBodiesOldestFirst()
    {
        await using var h = await Harness.CreateAsync();
        var newer = Summary("b", 1_700_000_500);
        var older = Summary("a", 1_700_000_000, folder: "fld_sent");
        var composer = await h.Ui.RunAsync(() => new PrintComposer(
            h.Cache, s => s.FolderId.Value == "fld_sent" ? FolderRole.Sent : FolderRole.Inbox));
        var doc = await h.Ui.InvokeAsync(() => composer.MessagesAsync([newer, older], Print.Layout.Conversation, Ct));

        Assert.NotNull(doc);
        Assert.Equal("Hello a", doc.Title); // the conversation's first, its oldest, names the job
        var a = doc.Fragment.IndexOf("<p>whole a</p>", StringComparison.Ordinal);
        var b = doc.Fragment.IndexOf("<p>whole b</p>", StringComparison.Ordinal);
        Assert.True(a > 0 && b > a, doc.Fragment);
        Assert.Contains("<hr class=\"malachi-print-sep\">", doc.Fragment, StringComparison.Ordinal);
        // The user's own message (Sent) prints its Bcc, the other none.
        Assert.Equal(1, Count(doc.Fragment, "<th>Bcc</th>"));
        // The whole body: no message.body of the print asked for trimQuoted.
        Assert.Contains(false, h.Requests);
        // The cache's own variant for the reader stays what it was.
        await h.Ui.RunAsync(() => Assert.Equal("<p>trimmed b</p>", h.Cache.Loaded("b")?.Body?.Html));
    }

    [Fact]
    public async Task SeveralRowsPrintOnPagesAndAtMostFifty()
    {
        await using var h = await Harness.CreateAsync();
        var composer = await h.Ui.RunAsync(() => new PrintComposer(h.Cache, _ => FolderRole.Inbox));
        var many = Enumerable.Range(0, Print.MaxMessages + 5).Select(i => Summary("m" + i, 1_700_000_000 + i)).ToList();
        var doc = await h.Ui.InvokeAsync(() => composer.MessagesAsync(many, Print.Layout.Pages, Ct));

        Assert.NotNull(doc);
        Assert.Equal(Print.MaxMessages, Count(doc.Fragment, "<table class=\"malachi-print-header\">"));
        Assert.Equal(Print.MaxMessages - 1, Count(doc.Fragment, "<div class=\"malachi-print-page\"></div>"));
        Assert.Null(await h.Ui.InvokeAsync(() => composer.MessagesAsync([], Print.Layout.One, Ct)));
    }

    private static int Count(string s, string part)
    {
        var n = 0;
        for (var i = s.IndexOf(part, StringComparison.Ordinal); i >= 0; i = s.IndexOf(part, i + part.Length, StringComparison.Ordinal))
        {
            n++;
        }
        return n;
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly Lock gate = new();
        private readonly List<bool> requests = [];

        private Harness()
        {
            Client = new RpcClient(Daemon.Path, PortableKeyFilePolicy.Instance);
        }

        public FakeDaemon Daemon { get; } = new();

        public RpcClient Client { get; }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public MessageCache Cache { get; private set; } = null!;

        /// <summary>The trimQuoted of every message.body answered.</summary>
        public IReadOnlyList<bool> Requests
        {
            get
            {
                lock (gate)
                {
                    return [.. requests];
                }
            }
        }

        public static async Task<Harness> CreateAsync()
        {
            var h = new Harness();
            h.Cache = await h.Ui.RunAsync(() => new MessageCache(h.Client, _ => { }, pending: h.Pending));
            h.Daemon.On(API.MessageGet.Name, p =>
            {
                var q = JsonCoding.Decode<MessageGetParams>(p);
                var id = q.MessageId.Value;
                return JsonCoding.EncodeToString(new MessageGetResult
                {
                    Message = new Message
                    {
                        Summary = Summary(id, folder: id == "a" ? "fld_sent" : "fld_in"),
                        Bcc = [new Address { Name = "Me", Email = "me@example.com" }],
                    },
                });
            });
            h.Daemon.On(API.MessageBody.Name, p =>
            {
                var q = JsonCoding.Decode<MessageBodyParams>(p);
                var trim = q.TrimQuoted == true;
                lock (h.gate)
                {
                    h.requests.Add(trim);
                }
                var id = q.MessageId.Value;
                var html = (trim ? "<p>trimmed " : "<p>whole ") + id + "</p>";
                return JsonCoding.EncodeToString(new MessageBodyResult
                {
                    MessageId = id,
                    BodyState = BodyState.Fetched,
                    HasHtml = true,
                    Html = html,
                    Text = id,
                    QuotedTrimmed = trim,
                    RemoteContent = RemoteContentPolicy.Block,
                    SanitizerVersion = "1",
                });
            });
            await h.Daemon.StartAsync();
            await h.Client.ConnectAsync(Ct);
            return h;
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(Cache.Dispose);
            Client.Dispose();
            await Daemon.StopAsync();
            Ui.Dispose();
        }
    }
}
