// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AccountsPageController, the Accounts page of the preferences
// (ui/internal/window/accounts_page.go and accounts_reorder.go, which the
// Go UI does not test beyond the pure functions of AccountsPageTests;
// macOS keeps the page in AppKit, untested). Against a fake daemon that
// serves account.list from a list the test changes and records every
// setEnabled, remove and reorder; a slow answer is held until the test
// releases it (HeldAnswer), and the test waits until nothing is left to
// happen (Quiescence.IdleAsync).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Controllers;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Model.SyncStatusFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class AccountsPageControllerTests
{
    private static readonly AccountId A = new("a");
    private static readonly AccountId B = new("b");
    private static readonly AccountId C = new("c");

    [Fact]
    public async Task InsensitiveAndEmptyUntilLoaded()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.IsEnabled);
            Assert.True(c.IsEmpty);
            Assert.Empty(c.Rows);
            Assert.Equal("", c.Description);
            // Nothing to act on before the load.
            Assert.False(c.MoveBy(A, 1));
            c.SetEnabled(A, false);
        });
        await h.IdleAsync();
        Assert.Empty(h.Calls(API.AccountSetEnabled.Name));

        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.False(c.IsEmpty);
        Assert.Equal([A, B, C], c.Rows.Select(r => r.Id));
        Assert.Equal(c.Rows, rec.Rows[^1]);
        Assert.Empty(rec.Toasts);
    }

    [Fact]
    public async Task RowsShowTheAccountsAsGtkDoes()
    {
        await using var h = await Harness.StartAsync();
        h.Accounts =
        [
            TestAccount("a", name: "Work", email: "me@work.test"),
            TestAccount("b", enabled: false, email: "home@example.test", state: new SyncState { AccountId = B, Status = SyncStatus.Disabled }),
            Browser("c", "g@example.test", SyncStatus.AuthRequired),
            Browser("d", "h@example.test", SyncStatus.Idle),
        ];
        var (c, _) = await h.LoadedAsync();
        var rows = c.Rows;
        Assert.Equal(["Work", "home@example.test", "g@example.test", "h@example.test"], rows.Select(r => r.Title));
        Assert.Equal(["me@work.test", "home@example.test", "g@example.test", "h@example.test"], rows.Select(r => r.Email));
        Assert.Equal(["", "Paused", "Sign-in required", ""], rows.Select(r => r.Status));
        Assert.Equal([true, false, true, true], rows.Select(r => r.Enabled));
        // "Sign In…" only for an account of the browser sign-in that needs it.
        Assert.Equal([false, false, true, false], rows.Select(r => r.OffersSignIn));
        Assert.All(rows, r => Assert.False(r.Busy));
        // The provider's GTK name, which the Windows table draws as the envelope (U6).
        Assert.Equal(["mail-unread-symbolic", "mail-unread-symbolic", "goa-account-google-symbolic", "goa-account-google-symbolic"], rows.Select(r => r.Icon));
    }

    [Fact]
    public async Task ALoadFailureGoesIntoTheDescription()
    {
        await using var h = await Harness.StartAsync();
        h.Fake.On(API.AccountList.Name, Fails(ErrorCode.InternalError, "no store"));
        var (c, rec) = await h.MakeAsync();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal("Loading accounts failed", c.Description);
        Assert.False(c.IsEnabled);
        Assert.True(c.IsEmpty);
        Assert.Empty(rec.Toasts);

        // A later load that answers clears it.
        h.ServeAccountList();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal("", c.Description);
        Assert.True(c.IsEnabled);
        Assert.Equal(3, c.Rows.Count);
    }

    [Fact]
    public async Task ALoadWithoutADaemonSaysSo()
    {
        await using var h = await Harness.StartAsync();
        var (c, _) = await h.MakeAsync();
        h.Client.Close();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal("Loading accounts needs a running mail backend", c.Description);
        Assert.False(c.IsEnabled);
    }

    [Fact]
    public async Task PausingShowsTheSwitchAsAskedUntilTheDaemonConfirms()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountSetEnabled.Name ? held : null);
        var (c, _) = await h.LoadedAsync();

        await h.Ui.RunAsync(() => c.SetEnabled(B, false));
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            var row = c.Rows[1];
            Assert.True(row.Busy);
            Assert.False(row.Enabled);
            Assert.Equal("", row.Status);
            // A second flip while the call runs is put back, not sent.
            c.SetEnabled(B, true);
            Assert.False(c.Rows[1].Enabled);
        });
        held.Release();
        await h.IdleAsync();
        var done = c.Rows[1];
        Assert.False(done.Busy);
        Assert.False(done.Enabled);
        Assert.Equal("Paused", done.Status);
        Assert.Equal(["{\"accountId\":\"b\",\"enabled\":false}"], h.Calls(API.AccountSetEnabled.Name));

        // Resuming leaves the account idle.
        await h.Ui.RunAsync(() => c.SetEnabled(B, true));
        await h.IdleAsync();
        Assert.True(c.Rows[1].Enabled);
        Assert.Equal("", c.Rows[1].Status);
        Assert.Equal(SyncStatus.Idle, c.Rows[1].Account.State.Status);
    }

    [Fact]
    public async Task ARefusedPauseFlipsTheSwitchBackAndSaysWhy()
    {
        await using var h = await Harness.StartAsync();
        h.Fake.On(API.AccountSetEnabled.Name, Fails(ErrorCode.AccountNotFound, "gone"));
        var (c, rec) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => c.SetEnabled(A, false));
        await h.IdleAsync();
        Assert.True(c.Rows[0].Enabled);
        Assert.False(c.Rows[0].Busy);
        Assert.Equal(["Pausing the account failed: unknown account"], rec.Toasts);

        await h.Ui.RunAsync(() => c.SetEnabled(A, true));
        await h.IdleAsync();
        Assert.Single(rec.Toasts); // already enabled: nothing sent

        h.Accounts = [TestAccount("a", enabled: false, email: "a@example.test")];
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetEnabled(A, true));
        await h.IdleAsync();
        Assert.Equal("Resuming the account failed: unknown account", rec.Toasts[^1]);
        Assert.False(c.Rows[0].Enabled);
    }

    [Fact]
    public async Task RemovingAsksFirstAndSendsTheCheckBox()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedAsync();
        var prompts = new List<AccountRemovalPrompt>();
        ConfirmAccountRemoval confirm = p =>
        {
            prompts.Add(p);
            return Task.FromResult((true, false));
        };

        h.Accounts = [.. h.Accounts.Where(a => a.Id != B)];
        await h.Ui.RunAsync(() => c.Remove(B, confirm));
        await h.IdleAsync();
        var prompt = Assert.Single(prompts);
        Assert.Equal("Remove this account?", prompt.Heading);
        Assert.Equal("b@example.test will be removed from Malachi Mail. Mail on the server is not affected.", prompt.Body);
        Assert.Equal("_Remove", prompt.ConfirmLabel);
        Assert.Equal("Also delete _drafts and downloaded data", prompt.ExtraLabel);
        Assert.True(prompt.ExtraDefault);
        Assert.Equal(["{\"accountId\":\"b\",\"deleteLocalData\":false}"], h.Calls(API.AccountRemove.Name));
        // The page reloads after its own action.
        Assert.Equal(2, h.Calls(API.AccountList.Name).Count);
        Assert.Equal([A, C], c.Rows.Select(r => r.Id));
        Assert.Empty(rec.Toasts);
    }

    [Fact]
    public async Task ACancelledRemovalSendsNothing()
    {
        await using var h = await Harness.StartAsync();
        var (c, _) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => c.Remove(A, _ => Task.FromResult((false, true))));
        await h.IdleAsync();
        Assert.Empty(h.Calls(API.AccountRemove.Name));
        Assert.Single(h.Calls(API.AccountList.Name));
        Assert.False(c.Rows[0].Busy);
    }

    [Fact]
    public async Task AFailedRemovalMakesTheRowSensitiveAgainAndSaysWhy()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountRemove.Name ? held : null);
        h.Fake.On(API.AccountRemove.Name, (FakeDaemon.MethodHandler)(async _ =>
        {
            await held.WaitAsync();
            throw new RpcException(new RpcError { Code = ErrorCode.KeyringError, Message = "locked" });
        }));
        var (c, rec) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => c.Remove(C, _ => Task.FromResult((true, true))));
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() => Assert.True(c.Rows[2].Busy));
        held.Release();
        await h.IdleAsync();
        Assert.False(c.Rows[2].Busy);
        Assert.Equal(["Removing the account failed: the system keyring is unavailable"], rec.Toasts);
        Assert.Single(h.Calls(API.AccountList.Name));
    }

    [Fact]
    public async Task TheKeyboardMovesTheSelectedRowAtOnceAndSavesTheOrder()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountReorder.Name ? held : null);
        var (c, rec) = await h.LoadedAsync();
        await h.Ui.RunAsync(() =>
        {
            // No selection: the keys do nothing.
            Assert.False(c.MoveSelected(1));
            c.Select(B);
            Assert.Equal(B, c.SelectedId);
            Assert.True(c.MoveSelected(-1));
            // The rows move before the daemon answers; the group waits for it.
            Assert.Equal([B, A, C], c.Rows.Select(r => r.Id));
            Assert.Equal(B, c.SelectedId);
            Assert.False(c.IsEnabled);
            // A move while the order is being saved is refused.
            Assert.False(c.MoveSelected(1));
        });
        Assert.Equal([B], rec.Focus);
        await held.ArrivedAsync();
        held.Release();
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal(["{\"accountIds\":[\"b\",\"a\",\"c\"]}"], h.Calls(API.AccountReorder.Name));
        Assert.Empty(rec.Toasts);

        // At the ends of the list nothing moves, so the key is not swallowed.
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.MoveSelected(-1));
            Assert.True(c.MoveBy(C, -1));
        });
        await h.IdleAsync();
        Assert.Equal([B, C, A], c.Rows.Select(r => r.Id));
        Assert.Equal(C, c.SelectedId);
        Assert.Equal([B, C], rec.Focus);
    }

    [Fact]
    public async Task ADraggedRowSavesTheNewOrder()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedAsync();
        var published = rec.Rows.Count;
        await h.Ui.RunAsync(() =>
        {
            Assert.True(c.MoveTo(A, 2));
            Assert.Equal([B, C, A], c.Rows.Select(r => r.Id));
            // A drop on its own place, or out of range, moves nothing and
            // publishes the rows again for a list that moved it itself.
            Assert.False(c.MoveTo(B, 0));
            Assert.False(c.MoveTo(B, 3));
        });
        await h.IdleAsync();
        Assert.Equal(["{\"accountIds\":[\"b\",\"c\",\"a\"]}"], h.Calls(API.AccountReorder.Name));
        Assert.Equal(published + 3, rec.Rows.Count);
        Assert.Empty(rec.Focus);
        Assert.Null(c.SelectedId);
    }

    [Fact]
    public async Task AFailedReorderSaysWhyAndReloadsTheDaemonsOrder()
    {
        await using var h = await Harness.StartAsync();
        h.Fake.On(API.AccountReorder.Name, Fails(ErrorCode.InvalidArgument, "stale order"));
        var (c, rec) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => Assert.True(c.MoveBy(A, 2)));
        await h.IdleAsync();
        Assert.Equal(["Saving the account order was rejected: stale order"], rec.Toasts);
        Assert.Equal([A, B, C], c.Rows.Select(r => r.Id));
        Assert.Equal(2, h.Calls(API.AccountList.Name).Count);
        Assert.True(c.IsEnabled);
    }

    [Fact]
    public async Task ALoadOlderThanAReorderDoesNotPutTheOldOrderBack()
    {
        var held = new HeldAnswer();
        var hold = false;
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountList.Name && hold ? held : null);
        var (c, rec) = await h.LoadedAsync();
        hold = true;
        await h.Ui.RunAsync(c.Load);
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() => Assert.True(c.MoveBy(C, -2)));
        // The reorder answers while the load still waits.
        await rec.Conditions.WhenAsync(h.Ui, () => c.IsEnabled);
        Assert.Equal([C, A, B], c.Rows.Select(r => r.Id));
        held.Release();
        await h.IdleAsync();
        Assert.Equal([C, A, B], c.Rows.Select(r => r.Id));
    }

    [Fact]
    public async Task ABusyRowCannotBeMoved()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountSetEnabled.Name ? held : null);
        var (c, _) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => c.SetEnabled(A, false));
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.MoveBy(A, 1));
            Assert.True(c.MoveBy(B, 1));
            // The call in flight stays with its account.
            Assert.True(c.Rows.Single(r => r.Id == A).Busy);
        });
        held.Release();
        await h.IdleAsync();
        Assert.False(c.Rows.Single(r => r.Id == A).Busy);
        Assert.False(c.Rows.Single(r => r.Id == A).Enabled);
    }

    [Fact]
    public async Task TheWizardsAccountsReloadThePageWithAToast()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedAsync();
        h.Accounts = [.. h.Accounts, TestAccount("d", email: "d@example.test")];
        await h.Ui.RunAsync(() => c.AccountAdded(new AccountConfig { Name = "", Email = "d@example.test" }));
        await h.IdleAsync();
        Assert.Equal([A, B, C, new AccountId("d")], c.Rows.Select(r => r.Id));
        await h.Ui.RunAsync(() => c.AccountSaved(new AccountConfig { Name = "", Email = "a@example.test" }));
        await h.IdleAsync();
        Assert.Equal(["Added d@example.test", "Saved a@example.test"], rec.Toasts);
        Assert.Equal(3, h.Calls(API.AccountList.Name).Count);
    }

    [Fact]
    public async Task TheSelectionStaysWithItsAccount()
    {
        await using var h = await Harness.StartAsync();
        var (c, _) = await h.LoadedAsync();
        await h.Ui.RunAsync(() =>
        {
            c.Select(new AccountId("nobody"));
            Assert.Null(c.SelectedId);
            c.Select(C);
            Assert.NotNull(c.Account(C));
            Assert.Null(c.Account(new AccountId("nobody")));
        });
        h.Accounts = [h.Accounts[2], h.Accounts[0]];
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(C, c.SelectedId);
        h.Accounts = [h.Accounts[1]];
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Null(c.SelectedId);
        Assert.Equal([A], c.Rows.Select(r => r.Id));
    }

    [Fact]
    public async Task AChangeOfTheAccountsElsewhereReloadsThePage()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => c.Select(B));
        // The main window's Add Account… added one; notify.accountsChanged.
        h.Accounts = [.. h.Accounts, TestAccount("d", email: "d@example.test")];
        await h.Ui.RunAsync(c.HandleAccountsChanged);
        await h.IdleAsync();
        Assert.Equal([A, B, C, new AccountId("d")], c.Rows.Select(r => r.Id));
        Assert.Equal(B, c.SelectedId);
        // Unlike the wizard's own completions, no toast.
        Assert.Empty(rec.Toasts);
        Assert.Equal(2, h.Calls(API.AccountList.Name).Count);
    }

    [Fact]
    public async Task AChangeWhileAnOrderIsSavedReloadsOnceItIsSaved()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountReorder.Name ? held : null);
        var (c, _) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => Assert.True(c.MoveBy(A, 1)));
        await held.ArrivedAsync();
        // An account came from elsewhere; a load now could answer with the
        // order before the move.
        await h.Ui.RunAsync(() =>
        {
            c.HandleAccountsChanged();
            // Nothing is asked but the order being saved.
            Assert.Equal(1, h.Pending.Count);
            Assert.Equal([B, A, C], c.Rows.Select(r => r.Id));
        });
        Assert.Single(h.Calls(API.AccountList.Name));

        h.Accounts = [h.Accounts[1], h.Accounts[0], h.Accounts[2], TestAccount("d", email: "d@example.test")];
        held.Release();
        await h.IdleAsync();
        Assert.Equal(2, h.Calls(API.AccountList.Name).Count);
        Assert.Equal([B, A, C, new AccountId("d")], c.Rows.Select(r => r.Id));
        Assert.True(c.IsEnabled);

        // Saved without a change meanwhile: no load of its own.
        await h.Ui.RunAsync(() => Assert.True(c.MoveBy(A, -1)));
        held.Release();
        await h.IdleAsync();
        Assert.Equal(2, h.Calls(API.AccountList.Name).Count);
    }

    [Fact]
    public async Task ASyncStateShowsInItsRow()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedAsync();
        var published = rec.Rows.Count;
        await h.Ui.RunAsync(() =>
        {
            c.HandleSyncState(new SyncState { AccountId = B, Status = SyncStatus.Syncing, Progress = 10 });
            Assert.Equal(["", "Syncing…", ""], c.Rows.Select(r => r.Status));
            Assert.Equal(published + 1, rec.Rows.Count);
            // A step of the same pass changes nothing the row shows.
            c.HandleSyncState(new SyncState { AccountId = B, Status = SyncStatus.Syncing, Progress = 60 });
            Assert.Equal(published + 1, rec.Rows.Count);
            // The new state stays with the account: Edit Account… opens on it.
            Assert.Equal(60, c.Account(B)!.State.Progress);
            c.HandleSyncState(new SyncState { AccountId = B, Status = SyncStatus.AuthRequired });
            Assert.Equal("Sign-in required", c.Rows[1].Status);
            // An account the page does not show is left alone.
            c.HandleSyncState(new SyncState { AccountId = new AccountId("nobody"), Status = SyncStatus.Error });
            Assert.Equal(published + 2, rec.Rows.Count);
        });

        // A pause in flight keeps the switch as asked under a new state.
        var held = new HeldAnswer();
        await using var h2 = await Harness.StartAsync(hold: m => m == API.AccountSetEnabled.Name ? held : null);
        var (c2, _) = await h2.LoadedAsync();
        await h2.Ui.RunAsync(() => c2.SetEnabled(A, false));
        await held.ArrivedAsync();
        await h2.Ui.RunAsync(() =>
        {
            c2.HandleSyncState(new SyncState { AccountId = A, Status = SyncStatus.Syncing });
            Assert.True(c2.Rows[0].Busy);
            Assert.False(c2.Rows[0].Enabled);
        });
        held.Release();
        await h2.IdleAsync();
    }

    [Fact]
    public async Task CloseDropsLateReplies()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: m => m == API.AccountReorder.Name ? held : null);
        h.Fake.On(API.AccountReorder.Name, (FakeDaemon.MethodHandler)(async _ =>
        {
            await held.WaitAsync();
            throw new RpcException(new RpcError { Code = ErrorCode.InternalError, Message = "boom" });
        }));
        var (c, rec) = await h.LoadedAsync();
        await h.Ui.RunAsync(() => Assert.True(c.MoveBy(A, 1)));
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            c.Close();
            Assert.True(c.IsClosed);
        });
        var published = rec.Rows.Count;
        held.Release();
        await h.IdleAsync();
        // No toast, no reload after the close.
        Assert.Empty(rec.Toasts);
        Assert.Single(h.Calls(API.AccountList.Name));
        Assert.Equal(published, rec.Rows.Count);
        await h.Ui.RunAsync(() =>
        {
            c.Load();
            c.SetEnabled(A, false);
            c.AccountAdded(new AccountConfig { Name = "", Email = "x@example.test" });
        });
        await h.IdleAsync();
        Assert.Single(h.Calls(API.AccountList.Name));
        Assert.Empty(h.Calls(API.AccountSetEnabled.Name));
        Assert.Empty(rec.Toasts);
    }

    // An account of the daemon's own browser sign-in (Google over IMAP).
    private static Account Browser(string id, string email, string status) => TestAccount(id, email: email) with
    {
        Config = new AccountConfig
        {
            Name = "",
            Email = email,
            OAuth2 = new OAuth2Config { Provider = OAuth2Provider.Google, Source = OAuth2Source.Daemon },
        },
        State = new SyncState { AccountId = new AccountId(id), Status = status },
    };

    /// <summary>A handler that fails as the daemon does.</summary>
    private static Func<string, string> Fails(int code, string message) =>
        _ => throw new RpcException(new RpcError { Code = code, Message = message });

    /// <summary>Collects what the page emits.</summary>
    private sealed class Recorder
    {
        public List<IReadOnlyList<AccountRow>> Rows { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<AccountId> Focus { get; } = [];

        public UiConditions Conditions { get; } = new();

        public void Attach(AccountsPageController c)
        {
            c.RowsChanged += (_, rows) => Note(() => Rows.Add(rows));
            c.ToastRequested += (_, text) => Note(() => Toasts.Add(text));
            c.FocusRequested += (_, id) => Note(() => Focus.Add(id));
            c.PropertyChanged += (_, _) => Conditions.Changed();
        }

        private void Note(Action record)
        {
            record();
            Conditions.Changed();
        }
    }

    /// <summary>
    /// A daemon with three accounts (a, b, c) that answers account.list from
    /// <see cref="Accounts"/> and records the params of every call, the UI
    /// thread and a connected client.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly object gate = new();
        private readonly List<(string Method, string Params)> calls = [];
        private readonly Func<string, HeldAnswer?>? hold;

        private Harness(Func<string, HeldAnswer?>? hold)
        {
            this.hold = hold;
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        /// <summary>What account.list answers.</summary>
        public IReadOnlyList<Account> Accounts { get; set; } =
        [
            TestAccount("a", email: "a@example.test"),
            TestAccount("b", email: "b@example.test"),
            TestAccount("c", email: "c@example.test"),
        ];

        public static async Task<Harness> StartAsync(Func<string, HeldAnswer?>? hold = null)
        {
            var h = new Harness(hold);
            h.ServeAccountList();
            foreach (var method in (string[])[API.AccountSetEnabled.Name, API.AccountRemove.Name, API.AccountReorder.Name])
            {
                h.Serve(method);
            }
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            return h;
        }

        /// <summary>The params of every call of <paramref name="method"/>, in order.</summary>
        public IReadOnlyList<string> Calls(string method)
        {
            lock (gate)
            {
                return [.. calls.Where(c => c.Method == method).Select(c => c.Params)];
            }
        }

        public void ServeAccountList() => Fake.On(API.AccountList.Name, async json =>
        {
            Record(API.AccountList.Name, json);
            var list = Accounts;
            if (hold?.Invoke(API.AccountList.Name) is { } held)
            {
                await held.WaitAsync();
            }
            return JsonCoding.EncodeToString(new AccountListResult { Accounts = list });
        });

        public Task<(AccountsPageController, Recorder)> MakeAsync() => Ui.RunAsync(() =>
        {
            var c = new AccountsPageController(Client, pending: Pending);
            var rec = new Recorder();
            rec.Attach(c);
            return (c, rec);
        });

        /// <summary>A page whose first load has answered.</summary>
        public async Task<(AccountsPageController, Recorder)> LoadedAsync()
        {
            var (c, rec) = await MakeAsync();
            await Ui.RunAsync(c.Load);
            await IdleAsync();
            Assert.True(c.IsEnabled);
            return (c, rec);
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async ValueTask DisposeAsync()
        {
            Client?.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }

        private void Serve(string method) => Fake.On(method, async json =>
        {
            Record(method, json);
            if (hold?.Invoke(method) is { } held)
            {
                await held.WaitAsync();
            }
            return "{}";
        });

        private void Record(string method, string json)
        {
            lock (gate)
            {
                calls.Add((method, json));
            }
        }
    }
}
