// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SyncControllerTests.swift: the status
// line and the sign-in banner over the controller (ui/internal/window/
// sync.go: applySyncState, refreshSyncLabel, loadSyncStatus, triggerSync's
// fallback, showAuthRequired; status.go: statusLineFor, and window.go's
// refresh timer). StatusLineForTest is status_test.go TestStatusLineFor,
// which SyncStatusTests leaves to this suite; it carries the Go cases of a
// handshake mismatch that the Swift table folds away. Swift pins `now` and
// shortens the timers; here the controller runs on a fake clock, and the
// timers fire when the tests advance it. Added: what a Swift callback
// cannot do, a handler that throws (AThrowingHandlerDoesNotStopTheRefresh,
// AThrowingBindingDoesNotStopTheLine), what Swift's close() makes
// harmless (NothingStartsOnceClosed), and the cancellation this port's
// RequestSignInUrlAsync takes (RequestSignInUrlCancelledByTheCallerOpensNothing).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Model.SyncStatusFixtures;

namespace Malachi.Core.Tests.Controllers;

public sealed class SyncControllerTests
{
    private static readonly SystemInfoResult Info = new() { Version = "1.2.3", ProtocolVersion = API.ProtocolVersion, Pid = 4242, StorePath = "/tmp/s.db" };

    private static readonly Account[] TwoAccounts =
    [
        TestAccount("a1", name: "Work", email: "w@example.invalid"),
        TestAccount("a2", email: "home@example.invalid"),
    ];

    private static readonly StatusLine ConnectingLine = new("Connecting to backend…", Icon: "network-idle-symbolic");

    private const string Daemon = "Connected to malachid 1.2.3 (pid 4242)";

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task FooterLadder()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            // Before any state the enabled accounts' account.list state counts.
            sc.RefreshFooter();
            Assert.Equal(new FooterState("Up to date", false), sc.Footer);

            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_inbox", progress: 42));
            Assert.Equal(new FooterState("Syncing Inbox… 42 %", true), sc.Footer);
            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_gone"));
            Assert.Equal("Syncing Work…", sc.Footer.Text);
            sc.Apply(State("a2", SyncStatus.AuthRequired));
            Assert.True(sc.Footer.Text == "Syncing Work…", "syncing beats authRequired");
            sc.Apply(State("a1", SyncStatus.Idle));
            // One of two accounts: named.
            Assert.Equal(new FooterState("Sign-in required: home@example.invalid", false), sc.Footer);
            sc.Apply(State("a2", SyncStatus.Idle, pending: 2));
            Assert.Equal(new FooterState("Sending 2 messages…", true), sc.Footer);
            sc.Apply(State("a2", SyncStatus.Idle, failed: 1));
            Assert.Equal(new FooterState("1 message not sent", false), sc.Footer);
            sc.Apply(State("a2", SyncStatus.Error));
            Assert.Equal(new FooterState("Sync error: home@example.invalid", false), sc.Footer);
            sc.Apply(State("a2", SyncStatus.Offline));
            Assert.Equal(new FooterState("Offline: home@example.invalid", false), sc.Footer);
            sc.Apply(State("a2", SyncStatus.Idle));
            Assert.Equal(new FooterState("Up to date", false), sc.Footer);
            Assert.Equal(10, log.Footers.Count);
            Assert.Equal(sc.Footer, log.Footers[^1]);
            // The line follows every footer; without a connection it says so.
            Assert.Equal(10, log.Lines.Count);
            Assert.Equal(ConnectingLine, sc.Line);

            // A pure computation over other accounts leaves the emitted footer alone.
            var other = sc.FooterStateFor([TestAccount("a9", state: State("a9", SyncStatus.Offline))], null);
            Assert.Equal(new FooterState("Offline, retrying", false), other);
            Assert.Equal("Up to date", sc.Footer.Text);
        });
    }

    [Fact]
    public async Task ApplyReturnsThePreviousStateAndHidesTheBanner()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            var first = sc.Apply(State("a1", SyncStatus.Syncing));
            Assert.Null(first.Prev);
            Assert.Equal(SyncStatus.Syncing, first.Cur.Status.Value);
            var second = sc.Apply(State("a1", SyncStatus.Idle, pending: 1));
            Assert.Equal(SyncStatus.Syncing, second.Prev?.Status.Value);
            Assert.Equal(1, second.Cur.PendingOutbox);
            Assert.Equal(1, sc.StateOf("a1")?.PendingOutbox);
            Assert.Null(sc.StateOf("a2"));

            var n = new AuthRequiredNotification { AccountId = "a1", Reason = ErrorCode.AuthFailed, Message = "bad password" };
            sc.ShowAuthRequired(n, TwoAccounts[0]);
            Assert.Equal(new AccountId("a1"), sc.AuthBannerAccount);
            Assert.Single(log.Banners);
            Assert.Equal("The server rejected the password of Work", log.Banners[^1].Title);
            Assert.Equal("Edit Account…", log.Banners[^1].Button);
            // Another account's state, or the same account still failing, keeps it.
            sc.Apply(State("a2", SyncStatus.Idle));
            sc.Apply(State("a1", SyncStatus.AuthRequired));
            Assert.Equal(new AccountId("a1"), sc.AuthBannerAccount);
            Assert.Single(log.Banners);
            // The account left the sign-in state: hidden, once.
            sc.Apply(State("a1", SyncStatus.Idle));
            Assert.Null(sc.AuthBannerAccount);
            Assert.Equal(2, log.Banners.Count);
            Assert.Null(log.Banners[^1].Account);
            Assert.Null(log.Banners[^1].Title);
            sc.HideAuthBanner();
            Assert.True(log.Banners.Count == 2, "hiding a hidden banner emits nothing");
        });
    }

    /// <summary>
    /// sync.go <c>refreshCertBanner</c>: the first enabled account, in account
    /// order, whose server's certificate was refused; hidden when none.
    /// </summary>
    [Fact]
    public async Task CertificateBanner()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync([.. TwoAccounts, TestAccount("a3", enabled: false, name: "Paused")]);
        static SyncState Tls(string acc, string reason, string status = SyncStatus.Offline) => TlsState(acc, status, reason);
        await h.Ui.RunAsync(() =>
        {
            // A paused account and a handshake failure raise nothing.
            sc.Apply(Tls("a3", TlsErrorReason.Untrusted, status: SyncStatus.Disabled));
            sc.Apply(Tls("a1", TlsErrorReason.Handshake));
            Assert.True(sc.CertBannerAccount is null && log.CertBanners.Count == 0);
            Assert.Equal("Offline: Work", sc.Footer.Text);

            sc.Apply(Tls("a2", TlsErrorReason.Untrusted));
            Assert.Equal(new AccountId("a2"), sc.CertBannerAccount);
            Assert.Equal("Certificate problem", sc.Footer.Text);
            Assert.Single(log.CertBanners);
            Assert.Equal("The certificate of home@example.invalid is not trusted", log.CertBanners[^1].Title);
            Assert.Equal("Edit Account…", log.CertBanners[^1].Button);
            // The first account in account order wins.
            sc.Apply(Tls("a1", TlsErrorReason.PinMismatch));
            Assert.Equal(new AccountId("a1"), sc.CertBannerAccount);
            Assert.Equal("The certificate of Work has changed", log.CertBanners[^1].Title);
            Assert.Equal("Certificate changed", sc.Footer.Text);
            // Unchanged: nothing emitted.
            var emitted = log.CertBanners.Count;
            sc.Apply(Tls("a1", TlsErrorReason.PinMismatch));
            sc.RefreshFooter();
            Assert.Equal(emitted, log.CertBanners.Count);
            // Fixed: the next account with a problem.
            sc.Apply(State("a1", SyncStatus.Syncing));
            Assert.Equal(new AccountId("a2"), sc.CertBannerAccount);
            Assert.Equal("The certificate of home@example.invalid is not trusted", log.CertBanners[^1].Title);
            // A pass that runs while the error still holds the last failure no
            // longer counts; neither does idle.
            sc.Apply(new SyncState { AccountId = "a2", Status = SyncStatus.Syncing, Error = TlsError(TlsErrorReason.Untrusted) });
            Assert.Null(sc.CertBannerAccount);
            Assert.True(log.CertBanners[^1].IsHidden);
            var hidden = log.CertBanners.Count;
            sc.Apply(State("a2", SyncStatus.Idle));
            Assert.True(log.CertBanners.Count == hidden, "hiding a hidden banner emits nothing");
        });
    }

    [Fact]
    public void AuthBannerTexts()
    {
        var password = TestAccount("a1", name: "Work", email: "w@example.invalid");
        var goa = WithConfig(TestAccount("a2", name: "Cloud", email: "c@example.invalid"), c => c with
        {
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "goa_1", Provider = OAuth2Provider.Google },
        });
        var graph = WithConfig(TestAccount("a3", name: "Office", email: "o@example.invalid"), c => c with
        {
            Kind = AccountKind.Graph,
            Graph = new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "goa_2" },
        });
        var browser = WithConfig(TestAccount("a4", name: "Mail", email: "m@example.invalid"), c => c with
        {
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
        });
        var browserGraph = WithConfig(TestAccount("a5", name: "Contoso", email: "c@example.invalid"), c => c with
        {
            Kind = AccountKind.Graph,
            Graph = new GraphConfig { Source = GraphSource.Daemon },
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 },
        });
        static AuthRequiredNotification N(string acc, int reason) => new() { AccountId = acc, Reason = reason, Message = "detail" };

        // A password account: a missing or refused password is named and the
        // button edits the account.
        Assert.Equal(("No password is stored for Work", "Edit Account…"), SyncController.AuthBannerFor(N("a1", ErrorCode.AuthRequired), password));
        Assert.Equal(("The server rejected the password of Work", "Edit Account…"), SyncController.AuthBannerFor(N("a1", ErrorCode.AuthFailed), password));
        Assert.Equal(("The system keyring is unavailable; Work cannot sign in", "Open Preferences"), SyncController.AuthBannerFor(N("a1", ErrorCode.KeyringError), password));
        Assert.Equal(("Work needs attention", "Open Preferences"), SyncController.AuthBannerFor(N("a1", ErrorCode.NetworkError), password));
        Assert.Equal(("Sign in to Cloud again in Settings → Online Accounts", "Open Online Accounts"), SyncController.AuthBannerFor(N("a2", ErrorCode.AuthRequired), goa));
        Assert.Equal(("GNOME Online Accounts is not available; Cloud cannot sign in", "Open Online Accounts"), SyncController.AuthBannerFor(N("a2", ErrorCode.Unavailable), goa));
        Assert.Equal(("Sign in to Office again in Settings → Online Accounts", "Open Online Accounts"), SyncController.AuthBannerFor(N("a3", ErrorCode.AuthRequired), graph));
        // The browser sign-in: signing in again is the repair, unless the
        // keyring failed.
        Assert.Equal(("Sign in to Mail again in your browser", "Sign In"), SyncController.AuthBannerFor(N("a4", ErrorCode.AuthRequired), browser));
        Assert.Equal(("Sign in to Mail again in your browser", "Sign In"), SyncController.AuthBannerFor(N("a4", ErrorCode.NetworkError), browser));
        Assert.Equal(("The system keyring is unavailable; Mail cannot sign in", "Sign In"), SyncController.AuthBannerFor(N("a4", ErrorCode.KeyringError), browser));
        Assert.Equal(("Sign in to Contoso again in your browser", "Sign In"), SyncController.AuthBannerFor(N("a5", ErrorCode.AuthFailed), browserGraph));
        // An unknown account is named by its id.
        Assert.Equal(("The server rejected the password of acc_zz", "Edit Account…"), SyncController.AuthBannerFor(N("acc_zz", ErrorCode.AuthFailed), null));
        Assert.Equal(("The system keyring is unavailable; acc_zz cannot sign in", "Open Preferences"), SyncController.AuthBannerFor(N("acc_zz", ErrorCode.KeyringError), null));
    }

    [Fact]
    public async Task AuthBannerActions()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        var password = TestAccount("a1", name: "Work", email: "w@example.invalid");
        var goa = WithConfig(TestAccount("a2", name: "Cloud", email: "c@example.invalid"), c => c with
        {
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "goa_1", Provider = OAuth2Provider.Google },
        });
        var browser = WithConfig(TestAccount("a4", name: "Mail", email: "m@example.invalid"), c => c with
        {
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
        });
        const string SignInPage = "https://accounts.google.com/o/oauth2/v2/auth?s=1";

        var plain = new AuthRequiredNotification { AccountId = "a4", Reason = ErrorCode.AuthRequired, Message = "x" };
        var withUrl = plain with { AuthUrl = SignInPage };
        var emptyUrl = plain with { AuthUrl = "" };
        var keyring = new AuthRequiredNotification { AccountId = "a1", Reason = ErrorCode.KeyringError, Message = "x" };
        var refused = new AuthRequiredNotification { AccountId = "a1", Reason = ErrorCode.AuthFailed, Message = "x" };
        Assert.Equal(new AuthBannerAction.EditAccount("a4", ErrorCode.AuthRequired), SyncController.AuthBannerActionFor(plain, password));
        Assert.Equal(new AuthBannerAction.EditAccount("a1", ErrorCode.AuthFailed), SyncController.AuthBannerActionFor(refused, password));
        Assert.Equal(new AuthBannerAction.OpenPreferences(), SyncController.AuthBannerActionFor(keyring, password));
        Assert.Equal(new AuthBannerAction.EditAccount("a4", ErrorCode.AuthRequired), SyncController.AuthBannerActionFor(plain, null));
        Assert.Equal(new AuthBannerAction.OpenOnlineAccounts(), SyncController.AuthBannerActionFor(keyring, goa));
        Assert.Equal(new AuthBannerAction.OpenOnlineAccounts(), SyncController.AuthBannerActionFor(plain, goa));
        Assert.Equal(new AuthBannerAction.SignInAgain("a4", null), SyncController.AuthBannerActionFor(plain, browser));
        Assert.Equal(new AuthBannerAction.SignInAgain("a4", null), SyncController.AuthBannerActionFor(emptyUrl, browser));
        Assert.Equal(new AuthBannerAction.SignInAgain("a4", SignInPage), SyncController.AuthBannerActionFor(withUrl, browser));
        // Not listed yet, but only the daemon's own sign-in has a URL.
        Assert.Equal(new AuthBannerAction.SignInAgain("a4", SignInPage), SyncController.AuthBannerActionFor(withUrl, null));
        Assert.Equal(("Sign in to a4 again in your browser", "Sign In"), SyncController.AuthBannerFor(withUrl, null));
        Assert.Equal(new AuthBannerAction.EditAccount("a4", ErrorCode.AuthRequired), SyncController.AuthBannerActionFor(emptyUrl, null));

        await h.Ui.RunAsync(() =>
        {
            // The shown banner keeps its action until it hides.
            Assert.Null(sc.AuthBannerAction);
            sc.ShowAuthRequired(withUrl, browser);
            Assert.Equal(new AuthBannerAction.SignInAgain("a4", SignInPage), sc.AuthBannerAction);
            Assert.Equal("Sign in to Mail again in your browser", log.Banners[^1].Title);
            Assert.Equal("Sign In", log.Banners[^1].Button);
            sc.Apply(State("a4", SyncStatus.Idle));
            Assert.True(sc.AuthBannerAction is null && sc.AuthBannerAccount is null);
            sc.ShowAuthRequired(refused, password);
            Assert.Equal(new AuthBannerAction.EditAccount("a1", ErrorCode.AuthFailed), sc.AuthBannerAction);
            Assert.Equal("The server rejected the password of Work", log.Banners[^1].Title);
            Assert.Equal("Edit Account…", log.Banners[^1].Button);
            // Hidden by the next state of that account that is not authRequired.
            sc.Apply(State("a1", SyncStatus.AuthRequired));
            Assert.Equal(new AccountId("a1"), sc.AuthBannerAccount);
            sc.Apply(State("a1", SyncStatus.Idle));
            Assert.True(sc.AuthBannerAction is null && sc.AuthBannerAccount is null);
            sc.ShowAuthRequired(keyring, password);
            Assert.Equal(new AuthBannerAction.OpenPreferences(), sc.AuthBannerAction);
            sc.HideAuthBanner();
            Assert.Null(sc.AuthBannerAction);
        });
    }

    [Fact]
    public async Task RequestSignInUrlStartsASessionForTheAccount()
    {
        string? seen = null;
        await using var fake = new FakeDaemon();
        fake.On(API.AccountOAuthStart.Name, p =>
        {
            Volatile.Write(ref seen, p);
            return """{"sessionId":"s_9","authUrl":"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1","expiresAt":"2026-09-25T10:10:00Z"}""";
        });
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);

        using var h = new Harness();
        var (sc, _) = await h.MakeAsync();
        var outcome = await h.Ui.InvokeAsync(() => sc.RequestSignInUrlAsync(client, "a5", "https://stale.example/x"));
        Assert.Equal(new SignInUrl.Open("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1"), outcome);
        var sent = JsonCoding.Decode<AccountOAuthStartParams>(Assert.IsType<string>(Volatile.Read(ref seen)));
        Assert.True(sent.AccountId == "a5" && sent.Config is null);
        Assert.Equal("Signed in", sent.BrowserPage?.SuccessTitle);
    }

    [Fact]
    public async Task RequestSignInUrlFallsBackToTheNotificationsPage()
    {
        await using var fake = new FakeDaemon();
        fake.On(
            API.AccountOAuthStart.Name,
            new FakeDaemon.MethodHandler(_ => Task.FromException<string>(new RpcException(new RpcError { Code = ErrorCode.Unavailable, Message = "too many" }))));
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);

        using var h = new Harness();
        var (sc, _) = await h.MakeAsync();
        Task<SignInUrl> Request(string? fallback) => h.Ui.InvokeAsync(() => sc.RequestSignInUrlAsync(client, "a5", fallback));
        Assert.Equal(new SignInUrl.Open("https://login.example/x"), await Request("https://login.example/x"));
        Assert.Equal(new SignInUrl.Failed("Starting the sign-in failed"), await Request(null));
        Assert.Equal(new SignInUrl.Failed("Starting the sign-in failed"), await Request(""));
        Assert.Equal(new SignInUrl.Failed("The link could not be opened: not an https address"), await Request("http://login.example/x"));
    }

    /// <summary>
    /// A caller that gives up (its window closed) gets the cancellation, not
    /// the notification's page: nothing is opened for a request it
    /// abandoned, and a request cancelled before it started sends nothing.
    /// </summary>
    [Fact]
    public async Task RequestSignInUrlCancelledByTheCallerOpensNothing()
    {
        var asked = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var gate = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        await using var fake = new FakeDaemon();
        fake.On(API.AccountOAuthStart.Name, async _ =>
        {
            asked.TrySetResult();
            await gate.Task;
            return """{"sessionId":"s_1","authUrl":"https://login.example/late","expiresAt":"2026-09-25T10:10:00Z"}""";
        });
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);

        using var h = new Harness();
        var (sc, _) = await h.MakeAsync();
        using var cts = new CancellationTokenSource();
        Task<SignInUrl> Request() => h.Ui.InvokeAsync(() => sc.RequestSignInUrlAsync(client, "a5", "https://login.example/x", cts.Token));
        var request = Request();
        await asked.Task.WaitAsync(TimeSpan.FromSeconds(10), Ct);
        await cts.CancelAsync();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => request);
        gate.SetResult();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(Request);
        await fake.IdleAsync();
        Assert.Equal([API.AccountOAuthStart.Name], fake.Calls);
    }

    [Fact]
    public async Task RequestSignInUrlRefusesWhatIsNotABrowserAddress()
    {
        string[] answers = ["", "file:///tmp/x"];
        var calls = 0;
        await using var fake = new FakeDaemon();
        fake.On(API.AccountOAuthStart.Name, _ =>
        {
            var url = answers[Math.Min(Interlocked.Increment(ref calls), answers.Length) - 1];
            return $$"""{"sessionId":"s_1","authUrl":"{{url}}","expiresAt":"2026-09-25T10:10:00Z"}""";
        });
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);

        using var h = new Harness();
        var (sc, _) = await h.MakeAsync();
        // An empty answer is a failed start; the fallback is for failed calls only.
        Assert.Equal(
            new SignInUrl.Failed("Starting the sign-in failed"),
            await h.Ui.InvokeAsync(() => sc.RequestSignInUrlAsync(client, "a5", "https://login.example/x")));
        Assert.Equal(
            new SignInUrl.Failed("The link could not be opened: not an https address"),
            await h.Ui.InvokeAsync(() => sc.RequestSignInUrlAsync(client, "a5", null)));
    }

    /// <summary>
    /// sync.go <c>loadSyncStatus</c>: a failed sync.status says "Not syncing"
    /// on the line (status.go <c>statusLineFor</c>) until a state arrives
    /// after all, or the connection changes; the sync half keeps its own text.
    /// </summary>
    [Fact]
    public async Task LoadSyncStatusFailureSaysNotSyncing()
    {
        await using var fixture = new MailFixture();
        fixture.Fail(API.SyncStatus.Name, new RpcError { Code = ErrorCode.NotImplemented, Message = "no syncer" });
        await fixture.StartAsync();
        using var client = new RpcClient(fixture.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);

        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            sc.SetConnection(new ConnectionState.Connected(Info));
            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_inbox"));
            Assert.True(sc.Line.Spinning);
            sc.LoadSyncStatus(client);
        });
        await h.IdleAsync(fixture.Daemon);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("Not syncing", log.Lines[^1].Text);
            Assert.Equal(new StatusLine("Not syncing", Active: true, Daemon: Daemon), sc.Line);
            Assert.Equal(new FooterState("Syncing Inbox…", true), sc.Footer);
            Assert.True(sc.Connection.SyncFailed);
        });
        Assert.Equal(1, fixture.CallCount(API.SyncStatus.Name));

        await h.Ui.RunAsync(() =>
        {
            // The minute's redraw keeps it.
            sc.RefreshFooter();
            Assert.Equal("Not syncing", sc.Line.Text);
            // A state from the daemon takes the line back.
            sc.Apply(State("a1", SyncStatus.Idle));
            Assert.Equal(new StatusLine("Up to date", Active: true, Daemon: Daemon), sc.Line);
            // So does a new connection.
            sc.LoadSyncStatus(client);
        });
        await h.IdleAsync(fixture.Daemon);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("Not syncing", log.Lines[^1].Text);
            sc.SetConnection(new ConnectionState.InfoFailed("x"));
            Assert.Equal(new StatusLine("Up to date", Active: true, Daemon: "Connected, but system.info failed"), sc.Line);
        });
    }

    [Fact]
    public async Task LoadSyncStatusAppliesEveryState()
    {
        await using var fixture = new MailFixture();
        fixture.SetSyncStates([State("a1", SyncStatus.Idle, pending: 1), State("a2", SyncStatus.Offline)]);
        await fixture.StartAsync();
        using var client = new RpcClient(fixture.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);

        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        var seen = new List<AccountId>();
        await h.Ui.RunAsync(() => sc.LoadSyncStatus(client, s =>
        {
            seen.Add(s.AccountId);
            sc.Apply(s);
        }));
        await h.IdleAsync(fixture.Daemon);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([new AccountId("a1"), new AccountId("a2")], seen);
            Assert.Equal(new FooterState("Sending 1 message…", true), sc.Footer);
            Assert.Equal(2, log.Footers.Count);
        });

        // Without `each`, the states are applied directly.
        var (plain, plainLog) = await h.MakeAsync();
        await h.Ui.RunAsync(() => plain.LoadSyncStatus(client));
        await h.IdleAsync(fixture.Daemon);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, plain.States.Count);
            Assert.Equal("Sending 1 message…", plain.Footer.Text);
            Assert.Equal(2, plainLog.Footers.Count);
        });
    }

    [Fact]
    public async Task BeginCheckingFallsBackToTheComputedLine()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            sc.SetConnection(new ConnectionState.Connected(Info));
            sc.BeginChecking();
            Assert.Equal(new FooterState("Checking for new mail…", true), sc.Footer);
            Assert.Equal(new StatusLine("Checking for new mail…", Spinning: true, Active: true, Daemon: Daemon), sc.Line);
        });
        await h.AdvanceAsync(SyncController.DefaultFallbackDelay);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("Up to date", log.Footers[^1].Text);
            Assert.False(sc.Footer.Spinning);
            Assert.True(sc.Line.Text == "Up to date" && !sc.Line.Spinning);

            // A syncState in the meantime takes over; the fallback then only
            // recomputes what is already shown.
            sc.BeginChecking();
            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_inbox"));
            Assert.Equal("Syncing Inbox…", sc.Footer.Text);
        });
        await h.AdvanceAsync(SyncController.DefaultFallbackDelay);
        var count = await h.Ui.RunAsync(() =>
        {
            Assert.Equal(new FooterState("Syncing Inbox…", true), sc.Footer);

            // Closed: the timer emits nothing any more.
            sc.BeginChecking();
            var n = log.Footers.Count;
            sc.Close();
            return n;
        });
        await h.AdvanceAsync(SyncController.DefaultFallbackDelay);
        Assert.Equal(count, await h.Ui.RunAsync(() => log.Footers.Count));
    }

    /// <summary>
    /// Calling BeginChecking again restarts the fallback: the first timer
    /// fires no more.
    /// </summary>
    [Fact]
    public async Task BeginCheckingAgainRestartsTheFallback()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            sc.SetConnection(new ConnectionState.Connected(Info));
            sc.BeginChecking();
        });
        await h.AdvanceAsync(TimeSpan.FromSeconds(20));
        await h.Ui.RunAsync(sc.BeginChecking);
        await h.AdvanceAsync(TimeSpan.FromSeconds(20));
        await h.Ui.RunAsync(() => Assert.Equal(new FooterState("Checking for new mail…", true), sc.Footer));
        await h.AdvanceAsync(TimeSpan.FromSeconds(10));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(new FooterState("Up to date", false), sc.Footer);
            Assert.Equal(["Checking for new mail…", "Checking for new mail…", "Up to date"], log.Footers.Skip(1).Select(f => f.Text));
        });
    }

    [Fact]
    public void ConnectionStatusLineTexts()
    {
        Assert.Equal(("network-idle-symbolic", "Connecting to backend…"), SyncController.ConnectionStatusLine(new ConnectionState.Connecting()));
        Assert.Equal(("network-transmit-receive-symbolic", Daemon), SyncController.ConnectionStatusLine(new ConnectionState.Connected(Info)));
        Assert.Equal(
            ("network-transmit-receive-symbolic", $"Protocol mismatch: UI {API.ProtocolVersion}, backend 9"),
            SyncController.ConnectionStatusLine(new ConnectionState.ProtocolMismatch(9)));
        Assert.Equal(("network-transmit-receive-symbolic", "Connected, but system.info failed"), SyncController.ConnectionStatusLine(new ConnectionState.InfoFailed("x")));
        Assert.Equal(("network-offline-symbolic", "Backend unavailable"), SyncController.ConnectionStatusLine(new ConnectionState.Unavailable("x")));
        Assert.Equal(("network-offline-symbolic", "Backend unavailable"), SyncController.ConnectionStatusLine(new ConnectionState.Stopping()));
    }

    /// <summary>
    /// status_test.go <c>TestStatusLineFor</c>. GTK has one moment more,
    /// connected with system.info on its way ("connected, system.info
    /// pending"), and keeps a handshake mismatch beside the state of the next
    /// attempt ("connecting after a mismatch"); the connection controller
    /// reports a connection only once system.info answered and keeps the
    /// mismatch as its state, so there are no such states to test here.
    /// </summary>
    [Fact]
    public void StatusLineForTest()
    {
        var other = new ConnectionState.ProtocolMismatch(API.ProtocolVersion + 1);
        var mismatch = $"Protocol mismatch: UI {API.ProtocolVersion}, backend {API.ProtocolVersion + 1}";
        var protocol1 = $"Protocol mismatch: UI {API.ProtocolVersion}, backend 1";
        (string Name, ConnView Conn, string Text, bool Spinning, StatusLine Want)[] cases =
        [
            ("connecting", new(new ConnectionState.Connecting()), "Up to date", false,
                new("Connecting to backend…", Icon: "network-idle-symbolic")),
            ("unavailable", new(new ConnectionState.Unavailable("gone")), "Syncing Inbox…", true,
                new("Backend unavailable", Icon: "network-offline-symbolic")),
            // What the daemon said before it went away does not count.
            ("unavailable forgets", new(new ConnectionState.Unavailable("gone"), SyncFailed: true), "Up to date", false,
                new("Backend unavailable", Icon: "network-offline-symbolic")),
            ("stopping", new(new ConnectionState.Stopping()), "Up to date", false,
                new("Backend unavailable", Icon: "network-offline-symbolic")),
            ("connected", new(new ConnectionState.Connected(Info)), "Up to date · 15:04", false,
                new("Up to date · 15:04", Active: true, Daemon: Daemon)),
            ("connected and syncing", new(new ConnectionState.Connected(Info)), "Syncing Inbox…", true,
                new("Syncing Inbox…", Spinning: true, Active: true, Daemon: Daemon)),
            ("system.info failed", new(new ConnectionState.InfoFailed("x")), "Up to date", false,
                new("Up to date", Active: true, Daemon: "Connected, but system.info failed")),
            // No connection: the handshake refused that daemon, so there is
            // nothing to click through to.
            ("protocol mismatch", new(other), "Syncing Inbox…", true,
                new(mismatch)),
            ("protocol mismatch beats sync.status", new(other, SyncFailed: true), "Up to date", false,
                new(mismatch)),
            // Go's handshake cases: a daemon older than the handshake, and one
            // of a later protocol with no account line under it.
            ("handshake mismatch", new(new ConnectionState.ProtocolMismatch(1)), "Syncing Inbox…", true,
                new(protocol1)),
            ("handshake mismatch, later protocol", new(other), "", false,
                new(mismatch)),
            ("sync.status failed", new(new ConnectionState.Connected(Info), SyncFailed: true), "Syncing Inbox…", true,
                new("Not syncing", Active: true, Daemon: Daemon)),
            // An empty line (no account at all) is no button to tab to.
            ("no account", new(new ConnectionState.Connected(Info)), "", false,
                new("", Daemon: Daemon)),
            ("no account, sync.status failed", new(new ConnectionState.InfoFailed("x"), SyncFailed: true), "", false,
                new("Not syncing", Active: true, Daemon: "Connected, but system.info failed")),
        ];
        foreach (var (name, conn, text, spinning, want) in cases)
        {
            var got = SyncController.StatusLineFor(conn, text, spinning);
            Assert.True(got == want, $"{name}: got {got}");
        }
    }

    /// <summary>
    /// A daemon of another protocol version, refused by the handshake: the
    /// line says so and is no button, whatever the accounts said before; the
    /// popover has no daemon to name.
    /// </summary>
    [Fact]
    public async Task ProtocolMismatchIsNotAButton()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            sc.SetConnection(new ConnectionState.Connected(Info));
            sc.Apply(State("a1", SyncStatus.Idle));
            Assert.True(sc.Line.Active);
            sc.SetConnection(new ConnectionState.ProtocolMismatch(1));
            var want = new StatusLine($"Protocol mismatch: UI {API.ProtocolVersion}, backend 1");
            Assert.Equal(want, sc.Line);
            Assert.True(!sc.Line.Active && sc.Line.Icon.Length == 0 && sc.Line.Daemon.Length == 0);
            Assert.Equal(want, log.Lines[^1]);
            sc.Close();
        });
    }

    /// <summary>
    /// window.go <c>showConnectionState</c>: the line names the connection
    /// until there is one, starting with the first attempt, and follows each
    /// change; the sync half stays what the accounts say.
    /// </summary>
    [Fact]
    public async Task LineFollowsTheConnection()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(sc.Line == ConnectingLine, "before anything");
            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_inbox", progress: 5));
            Assert.Equal(ConnectingLine, sc.Line);
            Assert.Equal(new FooterState("Syncing Inbox… 5 %", true), sc.Footer);

            sc.SetConnection(new ConnectionState.Connected(Info));
            Assert.Equal(new StatusLine("Syncing Inbox… 5 %", Spinning: true, Active: true, Daemon: Daemon), sc.Line);
            Assert.Equal(sc.Line, log.Lines[^1]);
            // What XAML binds to changes with the values, not with every
            // emission: the line stayed "Connecting…" while the footer moved.
            Assert.Equal([nameof(SyncController.Footer), nameof(SyncController.Connection), nameof(SyncController.Line)], log.Properties);

            sc.SetConnection(new ConnectionState.Unavailable("gone"));
            Assert.Equal(new StatusLine("Backend unavailable", Icon: "network-offline-symbolic"), sc.Line);
            // A check started meanwhile changes only the text and the spinner.
            sc.BeginChecking();
            Assert.Equal(new StatusLine("Checking for new mail…", Spinning: true, Icon: "network-offline-symbolic"), sc.Line);
            sc.Close();
        });
    }

    /// <summary>
    /// The line names the time of the last check; the refresh timer redraws
    /// it, so a day later it shows the date (window.go's
    /// <c>statusRefreshSeconds</c> timeout; the clock is the controller's).
    /// </summary>
    [Fact]
    public async Task RefreshRedrawsTheTimeOfTheLastCheck()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        var today = StatusNow();
        var checkedAt = today.AddHours(-2);
        await h.Ui.RunAsync(() =>
        {
            sc.Apply(State("a1", SyncStatus.Idle, last: checkedAt));
            Assert.Equal("Up to date · 13:30", sc.Footer.Text);
        });

        var tomorrow = today.AddDays(1);
        h.Time.SetUtcNow(tomorrow);
        var dated = "Up to date · " + Format.FormatDate(checkedAt, tomorrow);
        await h.Ui.RunAsync(() => sc.StartRefreshing());
        await h.AdvanceAsync(SyncController.RefreshInterval);
        var count = await h.Ui.RunAsync(() =>
        {
            Assert.Equal(dated, log.Footers[^1].Text);
            Assert.Equal(dated, sc.Footer.Text);
            // Closed: the timer emits nothing any more.
            sc.Close();
            return log.Footers.Count;
        });
        await h.AdvanceAsync(SyncController.RefreshInterval);
        await h.AdvanceAsync(SyncController.RefreshInterval);
        Assert.Equal(count, await h.Ui.RunAsync(() => log.Footers.Count));
    }

    /// <summary>
    /// A handler that throws (a view's bug) is reported and stops nothing:
    /// the line follows the footer in the same tick, and the minute's
    /// redraw goes on; so it does after a tick whose account lookup (the
    /// mailbox's) failed.
    /// </summary>
    [Fact]
    public async Task AThrowingHandlerDoesNotStopTheRefresh()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        var boom = new InvalidOperationException("a broken view");
        var trip = false; // touched on the UI thread only
        Task<(int Footers, int Lines)> Emitted() => h.Ui.RunAsync(() => (log.Footers.Count, log.Lines.Count));
        await h.Ui.RunAsync(() =>
        {
            sc.FooterChanged += (_, _) =>
            {
                if (trip)
                {
                    trip = false;
                    throw boom;
                }
            };
            sc.StartRefreshing();
        });
        await h.AdvanceAsync(SyncController.RefreshInterval);
        Assert.Equal((1, 1), await Emitted());

        await h.Ui.RunAsync(() => trip = true);
        h.Time.Advance(SyncController.RefreshInterval);
        var failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync());
        Assert.Same(boom, Assert.Single(failed.InnerExceptions));
        Assert.Equal((2, 2), await Emitted());

        await h.Ui.RunAsync(() =>
        {
            var accounts = sc.Accounts;
            var once = true;
            sc.Accounts = () =>
            {
                if (once)
                {
                    once = false;
                    throw boom;
                }
                return accounts();
            };
        });
        h.Time.Advance(SyncController.RefreshInterval);
        failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync());
        Assert.Same(boom, Assert.Single(failed.InnerExceptions));
        Assert.Equal((2, 2), await Emitted());
        await h.AdvanceAsync(SyncController.RefreshInterval);
        Assert.Equal((3, 3), await Emitted());
        await h.Ui.RunAsync(sc.Close);
    }

    /// <summary>
    /// A PropertyChanged handler that throws (a binding) is reported for
    /// every change and does not stop the change's other outputs.
    /// </summary>
    [Fact]
    public async Task AThrowingBindingDoesNotStopTheLine()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        var boom = new InvalidOperationException("a broken binding");
        var changes = await h.Ui.RunAsync(() =>
        {
            sc.PropertyChanged += (_, _) => throw boom;
            sc.SetConnection(new ConnectionState.Connected(Info));
            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_inbox"));
            Assert.Equal(new StatusLine("Syncing Inbox…", Spinning: true, Active: true, Daemon: Daemon), sc.Line);
            Assert.Equal(sc.Line, log.Lines[^1]);
            Assert.Equal(new FooterState("Syncing Inbox…", true), log.Footers[^1]);
            return log.Properties.Count;
        });
        var failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync());
        Assert.Equal(changes, failed.InnerExceptions.Count);
        Assert.All(failed.InnerExceptions, e => Assert.Same(boom, e));
    }

    /// <summary>
    /// Once closed, a check or a refresh starts no timer and emits nothing,
    /// and closing again (a window's teardown disposing what it closed) is
    /// harmless.
    /// </summary>
    [Fact]
    public async Task NothingStartsOnceClosed()
    {
        using var h = new Harness();
        var (sc, log) = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            sc.SetConnection(new ConnectionState.Connected(Info));
            sc.Close();
            sc.BeginChecking();
            sc.StartRefreshing();
        });
        await h.AdvanceAsync(SyncController.DefaultFallbackDelay);
        await h.AdvanceAsync(SyncController.RefreshInterval);
        await h.Ui.RunAsync(() =>
        {
            sc.Close();
            sc.Dispose();
            Assert.Single(log.Footers);
            Assert.Single(log.Lines);
        });
    }

    /// <summary>
    /// The popover's rows over the controller's states, accounts, folder
    /// names and clock (status.go <c>refreshStatusPopover</c>).
    /// </summary>
    [Fact]
    public async Task AccountStatusesOverTheController()
    {
        using var h = new Harness(DateTimeOffset.FromUnixTimeSeconds(1_788_343_200));
        var (sc, _) = await h.MakeAsync([.. TwoAccounts, TestAccount("a3", enabled: false, name: "Paused")]);
        var got = await h.Ui.RunAsync(() =>
        {
            sc.Apply(State("a1", SyncStatus.Syncing, folder: "f_inbox", progress: 42));
            sc.Apply(State("a2", SyncStatus.Offline, failed: 2));
            return sc.AccountStatuses();
        });
        Assert.Equal(
            [
                new AccountStatus { Account = "a1", Title = "Work", Detail = "Syncing Inbox… 42 %", Action = StatusAction.Check },
                new AccountStatus { Account = "a2", Title = "home@example.invalid", Detail = "Offline, retrying", Action = StatusAction.Retry, Failed = 2 },
                new AccountStatus { Account = "a3", Title = "Paused", Detail = "Paused" },
            ],
            got);
    }

    private static SyncState State(
        string acc, string status, string? folder = null, int progress = -1, int pending = 0, int failed = 0, DateTimeOffset? last = null) =>
        new()
        {
            AccountId = acc,
            Status = status,
            FolderId = folder is null ? (FolderId?)null : new FolderId(folder),
            Progress = progress,
            LastSync = last,
            PendingOutbox = pending,
            FailedOutbox = failed,
        };

    private static Account WithConfig(Account a, Func<AccountConfig, AccountConfig> change) => a with { Config = change(a.Config) };

    /// <summary>The test's UI thread and a fake clock, at <see cref="StatusNow"/> unless told otherwise.</summary>
    private sealed class Harness(DateTimeOffset? start = null) : IDisposable
    {
        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(start ?? StatusNow());

        /// <summary>A controller made on the UI thread over <paramref name="accounts"/> (two by default), and what it emits.</summary>
        public Task<(SyncController Controller, Log Log)> MakeAsync(IReadOnlyList<Account>? accounts = null)
        {
            var list = accounts ?? TwoAccounts;
            return Ui.RunAsync(() =>
            {
                var sc = new SyncController(Time, pending: Pending)
                {
                    Accounts = () => list,
                    FolderName = (acc, id) => acc.Value == "a1" && id.Value == "f_inbox" ? "Inbox" : "",
                };
                var log = new Log();
                log.Attach(sc);
                return (sc, log);
            });
        }

        public Task IdleAsync(FakeDaemon? daemon = null) => Quiescence.IdleAsync(Ui, Pending, daemon);

        /// <summary>Lets what is scheduled set its timers, moves the clock on, and waits for what fired.</summary>
        public async Task AdvanceAsync(TimeSpan by)
        {
            await IdleAsync();
            Time.Advance(by);
            await IdleAsync();
        }

        public void Dispose() => Ui.Dispose();
    }

    /// <summary>Collects what the controller emits; read on the UI thread.</summary>
    private sealed class Log
    {
        public List<FooterState> Footers { get; } = [];

        public List<StatusLine> Lines { get; } = [];

        public List<SyncBanner> Banners { get; } = [];

        public List<SyncBanner> CertBanners { get; } = [];

        /// <summary>The properties the controller said changed (INotifyPropertyChanged, what XAML binds to).</summary>
        public List<string?> Properties { get; } = [];

        public void Attach(SyncController sc)
        {
            sc.FooterChanged += (_, f) => Footers.Add(f);
            sc.StatusLineChanged += (_, l) => Lines.Add(l);
            sc.AuthBannerChanged += (_, b) => Banners.Add(b);
            sc.CertBannerChanged += (_, b) => CertBanners.Add(b);
            sc.PropertyChanged += (_, e) => Properties.Add(e.PropertyName);
        }
    }
}
