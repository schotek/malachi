// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CRMControllerTests.swift
// (savesThroughTheDialogAndDownloadsFirst, removesAfterTheQuestion,
// searchMergesAndLookupFailureShows, buttonOfSeveral): CrmController and
// CrmDialogSession against a fake daemon: crm.lookup into the dialog,
// crm.search merged in after the pause in typing, crm.save with the
// partNotDownloaded round (message.download, then crm.save again),
// crm.remove after the question, and the shared busy state. Fictional
// people only.
//
// Swift waits with waitUntil and sleeps; here a test waits until nothing is
// left to happen (IdleAsync), the search's pause runs on a FakeTimeProvider
// and an answer Swift delays is held (HeldAnswer). Swift's openURL is the
// ILauncher the controller is given (RecordingLauncher). Added: a save
// whose window closed still ends its busy mark.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Controllers.DaemonHarness;
using static Malachi.Core.Tests.Crm.CrmFixture;

namespace Malachi.Core.Tests.Controllers;

public sealed class CrmControllerTests
{
    private const string LookupJson =
        """{"accountId":"e1","saved":[{"messageId":"m_1","record":null}],"suggestions":["""
        + """{"kind":"contact","guid":"00000000-0000-4000-8000-000000000001","name":"Novák, Jan","reason":"address","url":"https://open.eway-crm.com/?l=1"},"""
        + """{"kind":"deal","guid":"00000000-0000-4000-8000-000000000005","name":"Fleet renewal","reason":"contact","superior":true,"url":"https://open.eway-crm.com/?l=5"}]}""";

    private const string SavedRecordJson =
        """{"accountId":"e1","itemGuid":"00000000-0000-4000-8000-000000000100","savedAt":"2026-10-17T10:00:00Z","checkedAt":"2026-10-17T10:00:00Z","private":false,"items":[],"url":"https://open.eway-crm.com/?l=e"}""";

    private const string DownloadJson =
        """{"message":{"id":"m_1","accountId":"m1","folderId":"f_inbox","from":[],"subject":"Offer for 2027","date":"2026-10-17T10:00:00Z","snippet":"","flags":[],"hasAttachments":false,"size":0}}""";

    private static MessageSummary Summary(string id, string subject = "Offer for 2027", bool saved = false) => new()
    {
        Id = id,
        AccountId = "m1",
        FolderId = "f_inbox",
        From = [new Address { Name = "Jan Novák", Email = "jan@acme.example" }],
        Subject = subject,
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
        CrmSaved = saved,
    };

    [Fact]
    public async Task SavesThroughTheDialogAndDownloadsFirst()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.CrmLookup.Name, _ => LookupJson);
        h.On(API.CrmSave.Name, _ =>
        {
            if (h.Params.Count(API.CrmSave.Name) == 1)
            {
                throw new RpcException(new RpcError { Code = ErrorCode.PartNotDownloaded, Message = "not whole", Data = ApiJson.Parse("""{"messageIds":["m_1"]}""") });
            }
            return $$"""{"results":[{"messageId":"m_1","created":true,"record":{{SavedRecordJson}}}]}""";
        });
        h.On(API.MessageDownload.Name, _ => DownloadJson);
        var (c, rec) = await h.StartAsync([Summary("m_1")]);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(CrmText.ButtonFor(CrmButtonState.NotSaved), c.Button);
            Assert.Equal(new CrmOffer(true, true, 1, false), c.Offer);
            c.Save();
        });
        var session = Assert.Single(rec.Sessions);
        Assert.Equal("Offer for 2027 · Jan Novák", session.Summary);
        await h.IdleAsync();
        Assert.Equal(CrmDialogSession.SessionPhase.Ready, session.Phase);
        ApiJson.AssertSameValue(
            new CrmLookupParams { AccountId = "e1", MessageAccountId = "m1", MessageIds = ["m_1"] },
            h.Params.Last<CrmLookupParams>(API.CrmLookup.Name));
        var dialog = session.Dialog!;
        Assert.False(dialog.Editing);
        Assert.Equal(Ref(CrmItemKind.Deal, 5), dialog.Superior);
        Assert.Equal("Save to eWay-CRM", session.Texts.Title);

        var closed = false;
        session.Closed += (_, _) => closed = true;
        await h.Ui.RunAsync(() =>
        {
            session.Save();
            Assert.True(closed && session.Finished);
            Assert.Equal(CrmButtonState.Saving, c.Button.State);
            Assert.True(h.State.InFlight.Has("m1", "m_1"));
        });
        await h.IdleAsync();
        Assert.Equal(["Downloading the message first…", "Saved to eWay-CRM"], rec.Toasts);
        Assert.Equal([API.CrmLookup.Name, API.CrmSave.Name, API.MessageDownload.Name, API.CrmSave.Name], h.Fake.Calls);
        ApiJson.AssertSameValue(new MessageDownloadParams { AccountId = "m1", MessageId = "m_1" }, h.Params.Last<MessageDownloadParams>(API.MessageDownload.Name));
        var saved = h.Params.Last<CrmSaveParams>(API.CrmSave.Name)!;
        Assert.Equal([Ref(CrmItemKind.Contact, 1)], saved.Link!);
        Assert.Equal(Ref(CrmItemKind.Deal, 5), saved.Superior);
        Assert.Equal<MessageId>(["m_1"], saved.MessageIds);
        // The call's own answer turns the button before the daemon's
        // notification refreshes the message.
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.State.InFlight.Has("m1", "m_1"));
            Assert.Equal(CrmButtonState.Saved, c.Button.State);
            Assert.Equal("https://open.eway-crm.com/?l=e", c.Record?.Url);
            c.Open();
        });
        await h.IdleAsync();
        Assert.Equal(["https://open.eway-crm.com/?l=e"], h.Launcher.Opened);
        Assert.True(rec.Changes > 0, "the windows heard of the call");
    }

    [Fact]
    public async Task RemovesAfterTheQuestion()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.CrmRemove.Name, _ => "{}");
        var (c, rec) = await h.StartAsync([Summary("m_1", saved: true)]);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(CrmButtonState.Saved, c.Button.State);
            Assert.True(c.Offer.Saved);
            rec.Answer = false;
            c.Remove();
        });
        await h.IdleAsync();
        Assert.Single(rec.Questions);
        Assert.Equal(0, h.Params.Count(API.CrmRemove.Name));
        Assert.Equal("Remove from eWay-CRM?", rec.Questions[0].Heading);
        Assert.StartsWith("“Offer for 2027”", rec.Questions[0].Body, StringComparison.Ordinal);
        await h.Ui.RunAsync(() =>
        {
            rec.Answer = true;
            c.Remove();
        });
        await h.IdleAsync();
        Assert.Equal(["Removed from eWay-CRM"], rec.Toasts);
        ApiJson.AssertSameValue(
            new CrmRemoveParams { AccountId = "e1", MessageAccountId = "m1", MessageId = "m_1" },
            h.Params.Last<CrmRemoveParams>(API.CrmRemove.Name));
        // The call's answer wins over the stale summary.
        await h.Ui.RunAsync(() => Assert.Equal(CrmButtonState.NotSaved, c.Button.State));
    }

    [Fact]
    public async Task SearchMergesAndLookupFailureShows()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.CrmLookup.Name, _ => LookupJson);
        h.On(API.CrmSearch.Name, _ =>
            """{"items":[{"kind":"project","guid":"00000000-0000-4000-8000-000000000007","name":"Pilot","reason":"search","url":""}]}""");
        var (c, rec) = await h.StartAsync([Summary("m_1")]);
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        var session = Assert.Single(rec.Sessions);
        Assert.Equal(CrmDialogSession.SessionPhase.Ready, session.Phase);
        await h.Ui.RunAsync(() =>
        {
            session.Search("p");
            session.Search("pi");
            session.Search("pil");
        });
        await h.IdleAsync();
        Assert.Equal(0, h.Params.Count(API.CrmSearch.Name)); // typing has not paused yet
        h.Time.Advance(CrmDialogSession.SearchDelay);
        await h.IdleAsync();
        Assert.Equal(1, h.Params.Count(API.CrmSearch.Name)); // typing pauses before the search
        var p = h.Params.Last<CrmSearchParams>(API.CrmSearch.Name)!;
        Assert.Equal(("pil", "e1"), (p.Query, p.AccountId?.Value));
        Assert.Contains(session.Dialog!.Groups(), g => g.Kind == CrmItemKind.Project);
        Assert.False(session.Searching);
        await h.Ui.RunAsync(session.Cancel);
        Assert.True(session.Finished);

        h.On(API.CrmLookup.Name, Fails(ErrorCode.NetworkError, "dial"));
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        var failing = rec.Sessions[^1];
        Assert.Equal(CrmDialogSession.SessionPhase.Failed, failing.Phase);
        Assert.Equal(RpcErrorText.Text("Contacting eWay-CRM", DaemonHarness.Daemon(ErrorCode.NetworkError, "dial")), failing.Failure);
        await h.Ui.RunAsync(failing.Save);
        Assert.False(failing.Finished, "nothing to save");
    }

    // Several rows: the Save button with their count; nothing for an account
    // the rules refuse.
    [Fact]
    public async Task ButtonOfSeveral()
    {
        await using var h = await Harness.StartAsync();
        var (c, _) = await h.StartAsync([Summary("m_1"), Summary("m_2"), Summary("m_3")]);
        await h.Ui.RunAsync(() =>
        {
            var b = c.Button;
            Assert.True(b.State == CrmButtonState.NotSaved && b.Label == "Save 3 Messages to eWay-CRM…" && b.Enabled && !b.Menu, b.ToString());
            h.State.Begin("m1", ["m_2"]);
            Assert.Equal(CrmButtonState.Saving, c.Button.State);
            Assert.False(c.Button.Enabled);
            h.State.End("m1", ["m_2"]);
            h.State.Accounts = () => [Imap];
            Assert.Equal(CrmButtonState.Hidden, c.Button.State);
            Assert.False(c.Offer.Shown);
        });
        h.Subjects = [];
        await h.Ui.RunAsync(() => Assert.Equal(CrmButtonState.Hidden, c.Button.State));
    }

    // A conversation's row offers nothing (owner, Gate B 2026-10-17).
    [Fact]
    public async Task ConversationRowsOfferNothing()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.StartAsync([Summary("m_1")]);
        h.ConversationIds = new HashSet<MessageId> { "m_1" };
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(CrmButtonState.Hidden, c.Button.State);
            c.Save();
        });
        Assert.Empty(rec.Sessions);
    }

    // Windows addition: a save outlives its window: the busy mark ends and
    // the override stays, though the window's toast is dropped.
    [Fact]
    public async Task ASaveWhoseWindowClosedStillEnds()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.CrmLookup.Name, _ => LookupJson);
        var held = h.Hold();
        h.On(API.CrmSave.Name, async _ =>
        {
            await held.WaitAsync();
            return $$"""{"results":[{"messageId":"m_1","record":{{SavedRecordJson}}}]}""";
        });
        var (c, rec) = await h.StartAsync([Summary("m_1")]);
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        var session = rec.Sessions[0];
        await h.Ui.RunAsync(session.Save);
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.State.InFlight.Has("m1", "m_1"));
            c.Close();
        });
        held.Release();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.State.InFlight.IsEmpty);
            Assert.True(h.State.IsSaved(Summary("m_1")));
        });
        Assert.Empty(rec.Toasts);
    }

    // What the controller hands the window.
    private sealed class Recorder
    {
        public List<CrmDialogSession> Sessions { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<CrmQuestion> Questions { get; } = [];

        public bool Answer { get; set; } = true;

        public int Changes { get; set; }
    }

    private sealed class Harness(DaemonHarness daemon) : IAsyncDisposable
    {
        public TestUIContext Ui => daemon.Ui;

        public FakeDaemon Fake => daemon.Fake;

        public ParamsLog Params => daemon.Params;

        public RecordingLauncher Launcher { get; } = new();

        public FakeTimeProvider Time { get; } = new();

        public CrmState State { get; } = new();

        public IReadOnlyList<MessageSummary> Subjects { get; set; } = [];

        public IReadOnlySet<MessageId> ConversationIds { get; set; } = new HashSet<MessageId>();

        public static async Task<Harness> StartAsync() => new(await DaemonHarness.StartAsync());

        public HeldAnswer Hold() => daemon.Hold();

        public void On(string method, Func<string, string> answer) => daemon.On(method, answer);

        public void On(string method, Func<string, Task<string>> answer) => daemon.On(method, answer);

        public async Task<(CrmController, Recorder)> StartAsync(IReadOnlyList<MessageSummary> subjects)
        {
            Subjects = subjects;
            var client = await daemon.ConnectAsync();
            return await Ui.RunAsync(() =>
            {
                State.Accounts = () => [Imap, EwayA];
                State.RoleOf = _ => FolderRole.Inbox;
                var rec = new Recorder();
                State.Changed += (_, _) => rec.Changes++;
                var c = new CrmController(client, State, Launcher, pending: daemon.Pending, time: Time)
                {
                    Subjects = () => Subjects,
                    Conversations = () => ConversationIds,
                    Confirm = q =>
                    {
                        rec.Questions.Add(q);
                        return Task.FromResult(rec.Answer);
                    },
                };
                c.PresentRequested += (_, s) => rec.Sessions.Add(s);
                c.ToastRequested += (_, t) => rec.Toasts.Add(t);
                daemon.CloseAtEnd(c.Close);
                return (c, rec);
            });
        }

        public Task IdleAsync() => daemon.IdleAsync();

        public ValueTask DisposeAsync() => daemon.DisposeAsync();
    }
}
