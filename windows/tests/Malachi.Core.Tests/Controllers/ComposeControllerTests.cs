// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeControllerTests.swift
// (ComposeControllerTests, every test): the compose manager
// (ui/internal/compose/manager.go) over a fake daemon and fake windows. The
// same file's ComposeLinkURLTests suite is ComposeLinkUrlTests. The last
// test is the Windows addition of saving on Quit (docs/windows-port.md §0),
// at the manager: only the windows whose save failed are asked.
//
// Swift polls until the account list arrived and sleeps before the negative
// checks; here the test waits until the controller, the daemon and the UI
// queue are idle.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ComposeControllerTests
{
    [Fact]
    public async Task AccountsAreFetchedOnTheFirstWindowOnly()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(h.Compose.Placeholder);
        Assert.Equal(ComposeController.PlaceholderAccounts, h.Compose.Accounts);
        Assert.Equal(0, h.ListCalls);

        await h.Run(() =>
        {
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.New, Subject = "x" });
            Assert.Single(h.Handles);
            Assert.Equal("x", h.Handles[0].Params.Subject);
            Assert.Single(h.Compose.OpenWindows);
        });
        await h.IdleAsync();
        Assert.False(h.Compose.Placeholder);
        Assert.Equal(1, h.ListCalls);
        Assert.Equal(["acc1"], h.Compose.Accounts.Select(a => a.Id.Value));
        Assert.Equal([["acc1"]], h.Handles[0].Accounts.Select(l => l.Select(a => a.Id.Value).ToArray()));
        Assert.Equal([false], h.Handles[0].Placeholders);
        Assert.Equal(new Address { Name = "One", Email = "one@example.invalid" }, h.Compose.SelfAddress);

        // The second window uses what is cached.
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        Assert.Equal(1, h.ListCalls);
        Assert.Equal(2, h.Handles.Count);
        Assert.True(h.Handles[1].Accounts.Count == 0, "nothing pushed: the window read the cache when it was made");

        await h.Run(() => h.Compose.Remove(h.Handles[0]));
        Assert.Same(h.Handles[1], Assert.Single(h.Compose.OpenWindows));
    }

    [Fact]
    public async Task InvalidateRefetchesWhileAWindowIsOpen()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        Assert.Equal(1, h.ListCalls);
        Assert.False(h.Compose.Placeholder);

        h.Script.Accounts =
        [
            MailModelTests.TestAccount("acc1", email: "one@example.invalid"),
            MailModelTests.TestAccount("acc2", email: "two@example.invalid"),
        ];
        await h.Run(() => h.Compose.Invalidate());
        await h.IdleAsync();
        Assert.Equal(2, h.Handles[0].Accounts.Count);
        Assert.Equal(2, h.ListCalls);
        Assert.Equal(["acc1", "acc2"], h.Handles[0].Accounts[1].Select(a => a.Id.Value));
        Assert.Equal(2, h.Compose.Accounts.Count);

        // Without a window nothing is asked until the next one opens.
        await h.Run(() =>
        {
            h.Compose.Remove(h.Handles[0]);
            h.Compose.Invalidate();
        });
        await h.IdleAsync();
        Assert.Equal(2, h.ListCalls);
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        Assert.Equal(3, h.ListCalls);
    }

    [Fact]
    public async Task NoAccountsMeansThePlaceholderIdentity()
    {
        await using var h = await Harness.StartAsync(accounts: []);
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        Assert.True(h.Compose.Placeholder);
        Assert.Equal(ComposeController.PlaceholderAccounts, h.Compose.Accounts);
        Assert.Equal(new Address { Name = "Malachi User", Email = "me@example.invalid" }, h.Compose.SelfAddress);
        Assert.Equal([true], h.Handles[0].Placeholders);
        Assert.Same(ComposeController.PlaceholderAccounts, Assert.Single(h.Handles[0].Accounts));
    }

    [Fact]
    public async Task AFailedListIsAskedAgainForTheNextWindow()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Fails = true;
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        Assert.Equal(1, h.ListCalls);
        Assert.True(h.Compose.Placeholder);
        Assert.Empty(h.Handles[0].Accounts);
        h.Script.Fails = false;
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        Assert.False(h.Compose.Placeholder);
        Assert.Equal(2, h.ListCalls);
        // Both windows hear about it.
        Assert.Single(h.Handles[0].Accounts);
        Assert.Single(h.Handles[1].Accounts);
    }

    [Fact]
    public async Task BlockedContentOfTheTemplateIsSaidOnOpen()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.Reply, Blocked = new BlockedContent { Forms = 1 } });
            Assert.Equal(["1 unsafe element was removed from the message"], h.Handles[0].Toasts);
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.Reply });
            Assert.Empty(h.Handles[1].Toasts);
        });
        await h.IdleAsync();
    }

    /// <summary>
    /// Manager.FindDraft: the window editing the draft draft.open answered
    /// with, by its id or by the Drafts message it takes over.
    /// </summary>
    [Fact]
    public async Task FindDraftByIdOrReplacedMessage()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.New });
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.Edit, DraftId = "d_1", Version = 2 });
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.Edit, Replaces = "m_9" });
        });
        await h.IdleAsync();
        Assert.Same(h.Handles[1], h.Compose.FindDraft(new Draft { Id = "d_1", AccountId = "a" }));
        Assert.Same(h.Handles[2], h.Compose.FindDraft(new Draft { AccountId = "a", Replaces = "m_9" }));
        Assert.Null(h.Compose.FindDraft(new Draft { Id = "d_2", AccountId = "a" }));
        Assert.True(h.Compose.FindDraft(new Draft { AccountId = "a" }) is null, "a new window edits nothing");
    }

    [Fact]
    public async Task OpenWithoutAFactoryDoesNothing()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Compose.MakeWindow = null;
            h.Compose.Open(new ComposeParams { Kind = ComposeKind.New });
        });
        await h.IdleAsync();
        Assert.Empty(h.Handles);
        Assert.Equal(0, h.ListCalls);
    }

    /// <summary>
    /// Windows addition (docs/windows-port.md §0): Quit saves every open
    /// window's draft without asking, and hands back only the windows whose
    /// save failed, in the order they were opened, for their question. A
    /// window that knows no draft (the protocol's default) has nothing to
    /// save.
    /// </summary>
    [Fact]
    public async Task SaveForQuitAsksOnlyWhereTheSaveFailed()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            for (var i = 0; i < 4; i++)
            {
                h.Compose.Open(new ComposeParams { Kind = ComposeKind.New });
            }
        });
        await h.IdleAsync();
        h.Handles[1].SavesForQuit = false;
        h.Handles[3].SavesForQuit = false;
        var failed = await h.Ui.InvokeAsync(() => h.Compose.SaveForQuitAsync());
        Assert.Equal([h.Handles[1], h.Handles[3]], failed);
        Assert.All(h.Handles, w => Assert.Equal(1, w.QuitSaves));

        IComposeWindowHandle plain = new PlainHandle();
        Assert.True(await plain.SaveForQuitAsync());
        Assert.False(await plain.CloseForQuitAsync());
        Assert.False(plain.Edits(new Draft { Id = "d_1", AccountId = "a" }));
        plain.Present();

        await h.Run(() =>
        {
            foreach (var w in h.Handles.ToArray())
            {
                h.Compose.Remove(w);
            }
        });
        Assert.Empty(await h.Ui.InvokeAsync(() => h.Compose.SaveForQuitAsync()));
    }

    /// <summary>
    /// The Quit of the app (QuitSequence over SaveForQuitAsync, as the
    /// shell wires it): a window whose save fails and which asks no close
    /// question of its own keeps its draft, and the app does not exit.
    /// Once the save goes through, the next Quit does.
    /// </summary>
    [Fact]
    public async Task AQuitNeverLosesADraftItCouldNotSave()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Compose.Open(new ComposeParams { Kind = ComposeKind.New }));
        await h.IdleAsync();
        h.Handles[0].SavesForQuit = false;
        var exits = 0;
        var quit = new Malachi.Core.Presentation.QuitSequence(new Malachi.Core.Presentation.QuitSteps
        {
            SaveDrafts = h.Compose.SaveForQuitAsync,
            Exit = () => exits++,
        });
        Assert.False(await h.Ui.InvokeAsync(() => quit.QuitAsync()));
        Assert.Equal(0, exits);
        Assert.False(quit.IsQuitting);

        h.Handles[0].SavesForQuit = true;
        Assert.True(await h.Ui.InvokeAsync(() => quit.QuitAsync()));
        Assert.Equal(1, exits);
    }

    /// <summary>What <c>account.list</c> answers.</summary>
    private sealed class Script
    {
        private readonly Lock gate = new();
        private IReadOnlyList<Account> accounts = [];
        private bool fails;

        public IReadOnlyList<Account> Accounts
        {
            get
            {
                lock (gate)
                {
                    return accounts;
                }
            }

            set
            {
                lock (gate)
                {
                    accounts = value;
                }
            }
        }

        public bool Fails
        {
            get
            {
                lock (gate)
                {
                    return fails;
                }
            }

            set
            {
                lock (gate)
                {
                    fails = value;
                }
            }
        }

        public string List(string p)
        {
            if (Fails)
            {
                throw new RpcException(new RpcError { Code = ErrorCode.StorageError, Message = "disk" });
            }
            return JsonCoding.EncodeToString(new AccountListResult { Accounts = Accounts });
        }
    }

    /// <summary>A compose window as the manager sees it.</summary>
    private sealed class FakeHandle(ComposeParams p) : IComposeWindowHandle
    {
        public ComposeParams Params { get; } = p;

        public List<IReadOnlyList<Account>> Accounts { get; } = [];

        public List<bool> Placeholders { get; } = [];

        public List<string> Toasts { get; } = [];

        /// <summary>What <see cref="SaveForQuitAsync"/> answers.</summary>
        public bool SavesForQuit { get; set; } = true;

        public int QuitSaves { get; private set; }

        public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder)
        {
            Accounts.Add(accounts);
            Placeholders.Add(placeholder);
        }

        public void Toast(string text) => Toasts.Add(text);

        /// <summary>As the window does: the saved draft it was opened with, or the Drafts message it takes over.</summary>
        public bool Edits(Draft draft) =>
            (draft.Id is not null && Params.DraftId == draft.Id) || (draft.Replaces is not null && Params.Replaces == draft.Replaces);

        public async Task<bool> SaveForQuitAsync()
        {
            QuitSaves++;
            await Task.Yield();
            return SavesForQuit;
        }
    }

    /// <summary>A window that implements only what the protocol requires.</summary>
    private sealed class PlainHandle : IComposeWindowHandle
    {
        public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder)
        {
        }

        public void Toast(string text)
        {
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public ComposeController Compose { get; private set; } = null!;

        public List<FakeHandle> Handles { get; } = [];

        public int ListCalls => Fake.Calls.Count(m => m == API.AccountList.Name);

        public static async Task<Harness> StartAsync(IReadOnlyList<Account>? accounts = null)
        {
            var h = new Harness();
            h.Script.Accounts = accounts ?? [MailModelTests.TestAccount("acc1", email: "one@example.invalid", displayName: "One")];
            h.Fake.On(API.AccountList.Name, h.Script.List);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Compose = await h.Ui.RunAsync(() => new ComposeController(h.Client, h.Settings, h.Pending)
            {
                MakeWindow = p =>
                {
                    var w = new FakeHandle(p);
                    h.Handles.Add(w);
                    return w;
                },
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() => Compose.Dispose());
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
