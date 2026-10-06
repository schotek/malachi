// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardReplyPanesTests.swift (suite
// BoardReplyPanesDraftTests: the real draft controller behind the rules).
// Go does not port it (ui/internal/boardreply/panes.go names it the
// acceptance list of a pane).
//
// The daemon takes every draft.save of a linked suggested reply as the
// user's edit, so a pane that is loaded, shown, refreshed, moved and left
// without an edit must send none. These run the rules over a real
// ComposeDraftController (DraftOwner.Board) and an editor that writes the
// draft differently from how it was given, against a fake daemon that
// records every call. The draft controller's autosave runs on a fake clock
// that is never advanced; the tests wait until everything is idle.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class BoardReplyPanesDraftTests
{
    [Fact]
    public async Task APaneLeftWithoutAnEditSavesNothing()
    {
        await using var r = await Rig.StartAsync();
        var c1 = BoardReplyPanesTests.BoardCase("1");
        var p = await r.OpenAsync(c1);
        // Board refreshes (every autosave elsewhere, a triage note), and the
        // detail asking from the List's pane and from the panel.
        await r.Ui.RunAsync(() =>
        {
            for (var i = 0; i < 3; i++)
            {
                r.Panes.Show(c1);
                _ = r.Panes.SlotFor(c1.Id);
            }
        });
        // Another case, then back and away again, then the quit.
        await r.Ui.RunAsync(() => r.Panes.Show(BoardReplyPanesTests.BoardCase("2")));
        await r.IdleAsync();
        Assert.True(p.Draft.Draft.Closed);
        var q = await r.OpenAsync(c1);
        await r.Ui.RunAsync(() => r.Panes.Show(null));
        await r.IdleAsync();
        Assert.True(q.Draft.Draft.Closed);
        var finishing = await r.Ui.RunAsync(() => r.Panes.FinishAllAsync(TimeSpan.FromSeconds(10)));
        Assert.True(await finishing);
        Assert.Empty(r.Store.Saves);
    }

    [Fact]
    public async Task AnEditIsSavedOnceWhenThePaneIsLeft()
    {
        await using var r = await Rig.StartAsync();
        var c1 = BoardReplyPanesTests.BoardCase("1");
        var p = await r.OpenAsync(c1);
        // Typed and left within the bridge's debounce.
        await r.Ui.RunAsync(() => p.Form.Unreported = "Tuesday works");
        await r.Ui.RunAsync(() => r.Panes.Show(BoardReplyPanesTests.BoardCase("2")));
        await r.IdleAsync();
        Assert.True(p.Draft.Draft.Closed);
        var save = Assert.Single(r.Store.Saves);
        Assert.Equal("Tuesday works", save.Draft.TextBody);
        Assert.True(save.Draft.Id == new DraftId("d_1") && save.Draft.Version == 5);
    }

    /// <summary>
    /// The inline editor as the draft controller sees it: until ready a flush
    /// reports nothing; the first flush after it reports the editor's own
    /// writing of the loaded draft; typed text is reported by the next flush.
    /// </summary>
    private sealed class EditorForm : IComposeForm
    {
        private string html = "<p>Text</p>";
        private string text = "Text";
        private string? rendering = "<p>Text</p>\n";

        public Account Account { get; } = MailModelTests.TestAccount("acc_1", email: "me@example.invalid");

        public string Subject => "Re: Offer";

        public IReadOnlyList<DraftAttachment> Attachments => [];

        public bool Ready { get; set; }

        public string? Unreported { get; set; }

        public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients() =>
            ([new Address { Name = "Ann", Email = "ann@example.org" }], [], [], true);

        public string EditorHtml() => html;

        public string EditorText() => text;

        public void FlushEditor(Action done)
        {
            if (Ready)
            {
                if (rendering is { } r)
                {
                    rendering = null;
                    html = r;
                }
                if (Unreported is { } u)
                {
                    Unreported = null;
                    html = "<p>" + u + "</p>";
                    text = u;
                }
            }
            done();
        }

        public void SetAttachments(IReadOnlyList<DraftAttachment> attachments)
        {
        }

        public void SetStatus(string text)
        {
        }

        public void Toast(string text)
        {
        }

        public void SetSendEnabled(bool enabled)
        {
        }

        public void CloseWindow()
        {
        }
    }

    /// <summary>A pane over the real draft controller, as the inline compose pane is one.</summary>
    private sealed class DraftPane : IBoardReplyPane
    {
        public DraftPane(RpcClient client, SettingsStore settings, ComposeParams parameters, TimeProvider time, PendingWork pending)
        {
            Draft = new ComposeDraftController(client, settings, () => false, new CidRegistry(), time, pending, owner: DraftOwner.Board)
            {
                Form = Form,
            };
            Draft.SetOriginal(parameters.InReplyTo, null);
            Draft.SetOpened(parameters.DraftId, parameters.Version, null, fromDrafts: true);
        }

        public EditorForm Form { get; } = new();

        public ComposeDraftController Draft { get; }

        public bool HasUnsavedText => Draft.Draft.Dirty || Draft.Draft.Saving;

        public bool IsSending => Draft.Draft.Sending;

        public bool IsLost => Draft.Lost;

        public string ReplyTitle => Form.Subject;

        /// <summary>The page came up (the host's ready).</summary>
        public void BecomeReady()
        {
            Form.Ready = true;
            Draft.EditorReady();
        }

        public Task<bool> SettleAsync() => Draft.SettleAsync();

        public void Close() => Draft.Cleanup();

        public void Abandon() => Draft.Abandon();
    }

    /// <summary>draft.get and draft.save, recorded.</summary>
    private sealed class DraftStore
    {
        private readonly Lock gate = new();
        private readonly List<DraftSaveParams> saves = [];
        private int version = 5;

        public IReadOnlyList<DraftSaveParams> Saves
        {
            get
            {
                lock (gate)
                {
                    return [.. saves];
                }
            }
        }

        public string Get(string json)
        {
            var p = JsonCoding.Decode<DraftGetParams>(json);
            int v;
            lock (gate)
            {
                v = version;
            }
            return JsonCoding.EncodeToString(new DraftGetResult
            {
                Draft = new Draft
                {
                    Id = p.DraftId,
                    AccountId = p.AccountId,
                    Version = v,
                    To = [new Address { Name = "Ann", Email = "ann@example.org" }],
                    Subject = "Re: Offer",
                    TextBody = "Text",
                    HtmlBody = "<p>Text</p>",
                    InReplyTo = "m_2",
                    Local = true,
                },
            });
        }

        public string Save(string json)
        {
            var p = JsonCoding.Decode<DraftSaveParams>(json);
            int v;
            lock (gate)
            {
                saves.Add(p);
                v = ++version;
            }
            return JsonCoding.EncodeToString(new DraftSaveResult { DraftId = p.Draft.Id ?? new DraftId("d_new"), Version = v, TextBody = p.Draft.TextBody });
        }
    }

    private sealed class Rig : IAsyncDisposable
    {
        private readonly UiConditions conditions = new();

        private Rig()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 10, 2, 10, 0, 0, TimeSpan.Zero));

        public FakeDaemon Fake { get; } = new();

        public DraftStore Store { get; } = new();

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public RpcClient Client { get; private set; } = null!;

        public BoardReplyEditorController Loader { get; private set; } = null!;

        public BoardReplyPanes Panes { get; private set; } = null!;

        public List<DraftPane> Made { get; } = [];

        public static async Task<Rig> StartAsync()
        {
            var r = new Rig();
            r.Fake.On(API.DraftGet.Name, r.Store.Get);
            r.Fake.On(API.DraftSave.Name, r.Store.Save);
            await r.Fake.StartAsync();
            r.Client = new RpcClient(r.Fake.Path, PortableKeyFilePolicy.Instance);
            await r.Client.ConnectAsync(TestContext.Current.CancellationToken);
            await r.Ui.RunAsync(() =>
            {
                r.Loader = new BoardReplyEditorController(r.Client, pending: r.Pending);
                r.Panes = new BoardReplyPanes(r.Loader, time: r.Time, pending: r.Pending)
                {
                    Make = (_, parameters) =>
                    {
                        var p = new DraftPane(r.Client, r.Settings, parameters, r.Time, r.Pending);
                        r.Made.Add(p);
                        return p;
                    },
                    OnChange = r.conditions.Changed,
                };
            });
            return r;
        }

        public async Task<DraftPane> OpenAsync(Board.Case c)
        {
            await Ui.RunAsync(() => Panes.Show(c));
            await conditions.WhenAsync(Ui, () => (Panes.Live as DraftPane)?.Draft.Draft.DraftId == c.Draft?.Id, "the pane");
            var p = (DraftPane)(await Ui.RunAsync(() => Panes.Live))!;
            await Ui.RunAsync(p.BecomeReady);
            await IdleAsync();
            return p;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                foreach (var p in Made)
                {
                    p.Draft.Dispose();
                }
                Panes.Dispose();
                Loader.Dispose();
            });
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
