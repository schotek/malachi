// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SyncController.swift
// (SyncController, connectionStatusLine, statusLineFor; withoutMnemonic of
// Controllers/WizardController.swift, which it uses); GTK:
// ui/internal/window/sync.go (loadSyncStatus, applySyncState,
// refreshSyncLabel, refreshCertBanner, startSync, showAuthRequired,
// hideAuthBanner, signInInBrowser), status.go (statusLineFor) and window.go
// (showConnectionState, the statusRefreshSeconds timer).
//
// Swift's free functions connectionStatusLine and statusLineFor are static
// members here; its nested FooterState, AuthBannerAction and SignInURL and
// the file's ConnView and StatusLine have files of their own, and so has
// SyncBanner, the three arguments of onAuthBanner and onCertBanner. The
// members Swift overloads on a property's name get a suffix:
// authBanner(for:account:) is AuthBannerFor, authBannerAction(for:account:)
// AuthBannerActionFor (both static, as they read no state),
// footerState(accounts:folderName:) FooterStateFor, state(of:) StateOf.
// Swift's `now` is the TimeProvider's (tests use a FakeTimeProvider), and
// its two timers wait on the TimeProvider too: the fallback of beginChecking
// and the refresh of startRefreshing, each cancelled when restarted.
// Swift's callbacks cannot throw; the handlers of the events here and of
// PropertyChanged can, and are isolated (ControllerEvents): a handler's
// failure is reported and neither stops the other outputs of a change nor
// ends the minute's redraw, which also guards every tick. After close()
// Swift's timers start and return at once; here none is started.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The sync status line, the sign-in banner and the certificate banner of
/// the main window (ui/internal/window/sync.go and status.go), minus the
/// widgets: the daemon owns the sync state, this only mirrors the last
/// <c>sync.status</c> / <c>notify.syncState</c> per account into a status
/// line, the rows of its popover (<see cref="AccountStatuses"/>) and banner
/// texts.
/// </summary>
/// <remarks>
/// <para>
/// The line comes in two halves, as in GTK's <c>refreshSyncLabel</c>:
/// <see cref="Footer"/> is the sync state of every account
/// (<c>syncStatusText</c>), and <see cref="Line"/> is what the status bar
/// shows, the footer with the connection to the daemon put over it
/// (<see cref="StatusLineFor"/>).
/// </para>
/// <para>
/// <see cref="Apply"/> records one state and refreshes the line; who called
/// it decides what else follows (the mailbox controller's
/// <c>handleSyncState</c> reloads folders and the list, as the GTK
/// <c>applySyncState</c> does). The 30 s fallback of <c>triggerSync</c> lives
/// here (<see cref="BeginChecking"/>): the line says "Checking for new mail…"
/// at once and the daemon's <c>notify.syncState</c> takes over, with the
/// timer as the guarantee that the spinner never sticks. A second timer
/// (<see cref="StartRefreshing"/>) redraws the line every minute, so the
/// time of the last check it names becomes a date once the day is over.
/// </para>
/// <para>
/// A handler of the events or of
/// <see cref="ObservableObject.PropertyChanged"/> that throws is reported by
/// <see cref="Pending"/> (logged at error level) and stops nothing: the
/// other handlers are called, the other outputs of the change follow, and
/// the timers go on.
/// </para>
/// <para>UI-thread-affine (docs/windows-port.md §7.1).</para>
/// </remarks>
public sealed partial class SyncController : ObservableObject, IDisposable
{
    /// <summary>
    /// How long the spinner started by <see cref="BeginChecking"/> stays on
    /// when no <c>notify.syncState</c> follows (sync.go
    /// <c>syncFallbackSeconds</c>; Swift's static <c>fallbackDelay</c>).
    /// </summary>
    public static readonly TimeSpan DefaultFallbackDelay = TimeSpan.FromSeconds(30);

    /// <summary>
    /// How often <see cref="StartRefreshing"/> redraws the line without a
    /// state change (sync.go <c>statusRefreshSeconds</c>).
    /// </summary>
    public static readonly TimeSpan RefreshInterval = TimeSpan.FromSeconds(60);

    private readonly ControllerScope scope;
    private readonly TimeProvider time;
    private readonly ILogger logger;

    // The last state per account (sync.go syncStates).
    private readonly Dictionary<AccountId, SyncState> states = [];

    // The certificate banner's title last emitted.
    private string? certBannerTitle;
    private CancellationTokenSource? fallback;
    private CancellationTokenSource? refresher;
    private bool closed;

    /// <summary>A controller whose line says "Connecting to backend…" until the connection reports anything.</summary>
    /// <param name="timeProvider">The clock of the timers and of the time of the last check (Swift <c>now</c>).</param>
    /// <param name="logger">Receives methods, ids and codes, never mail data.</param>
    /// <param name="pending">Counts the controller's background work (a tracker of its own when null).</param>
    public SyncController(TimeProvider? timeProvider = null, ILogger<SyncController>? logger = null, PendingWork? pending = null)
    {
        time = timeProvider ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        Footer = new FooterState("", false);
        Connection = new ConnView(new ConnectionState.Connecting());
        Line = StatusLineFor(Connection, "", false);
    }

    /// <summary>Called after every change of the footer line (Swift <c>onFooter</c>).</summary>
    public event EventHandler<FooterState>? FooterChanged;

    /// <summary>
    /// Called after every change of the status line, and whenever the rows
    /// of the popover may have changed with it (Swift <c>onStatusLine</c>;
    /// sync.go <c>refreshSyncLabel</c> refreshes an open popover from the same
    /// place).
    /// </summary>
    public event EventHandler<StatusLine>? StatusLineChanged;

    /// <summary>
    /// Called to show the sign-in banner (account, title, button label) or
    /// to hide it (<see cref="SyncBanner.Hidden"/>) (Swift <c>onAuthBanner</c>).
    /// </summary>
    public event EventHandler<SyncBanner>? AuthBannerChanged;

    /// <summary>
    /// Called to show the certificate banner (account, title, button label
    /// without its mnemonic) or to hide it (<see cref="SyncBanner.Hidden"/>)
    /// (Swift <c>onCertBanner</c>). Its button edits the account (the wizard
    /// in edit mode).
    /// </summary>
    public event EventHandler<SyncBanner>? CertBannerChanged;

    /// <summary>The fallback in use (<see cref="DefaultFallbackDelay"/>).</summary>
    public TimeSpan FallbackDelay { get; set; } = DefaultFallbackDelay;

    /// <summary>
    /// The accounts the line counts and the popover lists (<c>account.list</c>
    /// order); the mailbox controller supplies its model's.
    /// </summary>
    public Func<IReadOnlyList<Account>> Accounts { get; set; } = () => [];

    /// <summary>
    /// The display name of a folder, "" when unknown (sync.go
    /// <c>refreshSyncLabel</c>'s lookup).
    /// </summary>
    public Func<AccountId, FolderId, string> FolderName { get; set; } = (_, _) => "";

    /// <summary>Counts the controller's background work; tests wait on it.</summary>
    public PendingWork Pending => scope.Pending;

    /// <summary>The last state per account (sync.go <c>syncStates</c>).</summary>
    public IReadOnlyDictionary<AccountId, SyncState> States => states;

    /// <summary>The sync half of the line last emitted.</summary>
    [ObservableProperty]
    public partial FooterState Footer { get; private set; }

    /// <summary>
    /// What the status bar shows, last emitted: <see cref="Footer"/> under
    /// the connection (<see cref="StatusLineFor"/>). Until the connection
    /// reports anything the first attempt is underway: "Connecting to
    /// backend…".
    /// </summary>
    [ObservableProperty]
    public partial StatusLine Line { get; private set; }

    /// <summary>The connection as the line knows it (status.go <c>connView</c>).</summary>
    [ObservableProperty]
    public partial ConnView Connection { get; private set; }

    /// <summary>The account the sign-in banner is up for, null while hidden.</summary>
    [ObservableProperty]
    public partial AccountId? AuthBannerAccount { get; private set; }

    /// <summary>What the banner's button does; null while hidden.</summary>
    [ObservableProperty]
    public partial AuthBannerAction? AuthBannerAction { get; private set; }

    /// <summary>The account the certificate banner is up for, null while hidden.</summary>
    [ObservableProperty]
    public partial AccountId? CertBannerAccount { get; private set; }

    /// <summary>
    /// The connection's sentence for a connection state (window.go
    /// <c>showConnectionState</c> and <c>fetchSystemInfo</c>, the texts of
    /// status.go <c>statusLineFor</c>): the GTK icon name, which the view
    /// maps to a glyph, and the text. Without a connection the status line
    /// says it; once connected the sentence is the foot of the status
    /// popover. Stopping shows as unavailable: the window is on its way out.
    /// </summary>
    public static (string Icon, string Text) ConnectionStatusLine(ConnectionState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        return state switch
        {
            ConnectionState.Connecting => ("network-idle-symbolic", L10n.T("Connecting to backend…")),
            ConnectionState.Connected c => ("network-transmit-receive-symbolic", L10n.T("Connected to malachid %s (pid %d)", c.Info.Version, c.Info.Pid)),
            ConnectionState.ProtocolMismatch m => ("network-transmit-receive-symbolic", L10n.T("Protocol mismatch: UI %d, backend %d", API.ProtocolVersion, m.Daemon)),
            ConnectionState.InfoFailed => ("network-transmit-receive-symbolic", L10n.T("Connected, but system.info failed")),
            _ => ("network-offline-symbolic", L10n.T("Backend unavailable")),
        };
    }

    /// <summary>
    /// Puts the connection over the sync state (status.go <c>statusLineFor</c>;
    /// <paramref name="text"/> and <paramref name="spinning"/> from
    /// <c>syncStatusText</c>). Without a connection the line says so, with an
    /// icon, and cannot be clicked: there is no account state to show. A
    /// daemon of another protocol version, or a failed <c>sync.status</c>,
    /// takes the line over as well; the mismatch cannot be clicked either,
    /// since the handshake refused that daemon and there is no connection.
    /// An empty line (no account at all) cannot be clicked either. The
    /// popover's foot names the daemon (<see cref="ConnectionStatusLine"/>),
    /// or that <c>system.info</c> failed; it is empty while the line says the
    /// protocols do not match.
    /// </summary>
    public static StatusLine StatusLineFor(ConnView c, string text, bool spinning)
    {
        ArgumentNullException.ThrowIfNull(text);
        switch (c.State)
        {
            case ConnectionState.ProtocolMismatch:
                return new StatusLine(ConnectionStatusLine(c.State).Text);
            case ConnectionState.Connected or ConnectionState.InfoFailed:
                var line = new StatusLine(text, spinning, Daemon: ConnectionStatusLine(c.State).Text);
                if (c.SyncFailed)
                {
                    line = line with { Text = L10n.T("Not syncing"), Spinning = false };
                }
                // An empty line (no account at all) is no button to tab to.
                return line with { Active = line.Text.Length > 0 };
            default:
                // Connecting, unavailable, stopping.
                var (icon, conn) = ConnectionStatusLine(c.State);
                return new StatusLine(conn, Icon: icon);
        }
    }

    /// <summary>Stops the timers; nothing is emitted afterwards.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        closed = true;
        fallback?.Cancel();
        fallback = null;
        refresher?.Cancel();
        refresher = null;
        scope.Close();
    }

    /// <summary>Closes the controller (<see cref="Close"/>).</summary>
    public void Dispose() => Close();

    /// <summary>
    /// Redraws the line every <paramref name="every"/>
    /// (<see cref="RefreshInterval"/>) without a state change (window.go
    /// <c>New</c>, the <c>statusRefreshSeconds</c> timeout): it names the
    /// time of the last check ("Up to date · 15:04"), which a day later has
    /// to be a date. Calling it again restarts the timer; once closed it
    /// starts none.
    /// </summary>
    public void StartRefreshing(TimeSpan? every = null)
    {
        scope.VerifyAccess();
        if (closed)
        {
            return;
        }
        var interval = every ?? RefreshInterval;
        refresher?.Cancel();
        var timer = new CancellationTokenSource();
        refresher = timer;
        scope.RunDetached(async lifetime =>
        {
            try
            {
                using var linked = CancellationTokenSource.CreateLinkedTokenSource(lifetime, timer.Token);
                while (true)
                {
                    await Task.Delay(interval, time, linked.Token);
                    if (timer.IsCancellationRequested || closed)
                    {
                        return;
                    }
                    // A tick that fails (an account lookup of the mailbox's)
                    // is reported, and the next one comes all the same.
                    scope.Guard(RefreshFooter);
                }
            }
            catch (OperationCanceledException) when (timer.IsCancellationRequested)
            {
                // Restarted or closed.
            }
            finally
            {
                if (refresher == timer)
                {
                    refresher = null;
                }
                timer.Dispose();
            }
        });
    }

    // States

    /// <summary>
    /// Records one account's state and refreshes the line (the first half
    /// of sync.go <c>applySyncState</c>): a failed <c>sync.status</c> no
    /// longer holds the line, and the banner hides when its account left the
    /// sign-in state. Returns the state it replaced (null for the first) and
    /// the new one, for the caller's <c>onSyncFinished</c> /
    /// <c>onOutboxChanged</c>.
    /// </summary>
    public (SyncState? Prev, SyncState Cur) Apply(SyncState s)
    {
        ArgumentNullException.ThrowIfNull(s);
        scope.VerifyAccess();
        var prev = states.GetValueOrDefault(s.AccountId);
        states[s.AccountId] = s;
        Connection = Connection with { SyncFailed = false };
        RefreshFooter();
        if (s.AccountId == AuthBannerAccount && s.Status.Value != SyncStatus.AuthRequired)
        {
            HideAuthBanner();
        }
        return (prev, s);
    }

    /// <summary>The cached state of an account, if any arrived (Swift <c>state(of:)</c>).</summary>
    public SyncState? StateOf(AccountId acc) => states.GetValueOrDefault(acc);

    /// <summary>
    /// Runs <c>sync.status</c> and hands every state to <see cref="Apply"/>
    /// (sync.go <c>loadSyncStatus</c>); <paramref name="each"/> replaces that
    /// step for a caller that does more per state (the mailbox controller's
    /// <c>handleSyncState</c>). notImplemented is the expected answer from a
    /// daemon without a syncer: the line says "Not syncing" until a state
    /// arrives after all (<see cref="StatusLineFor"/>), and nothing else
    /// happens.
    /// </summary>
    public void LoadSyncStatus(RpcClient client, Action<SyncState>? each = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        scope.VerifyAccess();
        scope.Perform(client, API.SyncStatus, new SyncStatusParams(), outcome =>
        {
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogSyncStatusFailed(logger, err!);
                Connection = Connection with { SyncFailed = true };
                RefreshFooter();
                return;
            }
            foreach (var s in res.Accounts)
            {
                if (each is not null)
                {
                    each(s);
                }
                else
                {
                    Apply(s);
                }
            }
        });
    }

    // Status line

    /// <summary>
    /// The footer line for the cached states over <paramref name="accounts"/>
    /// (sync.go <c>syncStatusText</c>, see
    /// <see cref="SyncStatusTexts.SyncStatusText"/>), against the clock
    /// (Swift <c>footerState(accounts:folderName:)</c>).
    /// </summary>
    public FooterState FooterStateFor(IReadOnlyList<Account> accounts, Func<AccountId, FolderId, string>? folderName)
    {
        var (text, spinning) = SyncStatusTexts.SyncStatusText(states, accounts, folderName, time.GetUtcNow());
        return new FooterState(text, spinning);
    }

    /// <summary>
    /// Recomputes the line from the cached states and the connection
    /// (sync.go <c>refreshSyncLabel</c>) and emits it, and with it the
    /// certificate banner. Enabled accounts without a cached state fall
    /// back to the state <c>account.list</c> reported, so the line is right
    /// before <c>sync.status</c> answered.
    /// </summary>
    public void RefreshFooter()
    {
        scope.VerifyAccess();
        var accounts = Accounts();
        var lookup = FolderName;
        var f = FooterStateFor(accounts, (acc, id) => lookup(acc, id));
        SetFooter(f);
        SetLine(StatusLineFor(Connection, f.Text, f.Spinning));
        RefreshCertBanner(accounts);
    }

    /// <summary>
    /// The connection changed (window.go <c>showConnectionState</c>): what
    /// <c>sync.status</c> said is forgotten with it, and the line follows.
    /// </summary>
    public void SetConnection(ConnectionState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        scope.VerifyAccess();
        Connection = new ConnView(state);
        RefreshFooter();
    }

    /// <summary>
    /// The popover's rows for the cached states (status.go
    /// <c>refreshStatusPopover</c>'s <c>accountStatuses</c>), over
    /// <see cref="Accounts"/> and against the clock.
    /// </summary>
    public IReadOnlyList<AccountStatus> AccountStatuses()
    {
        var lookup = FolderName;
        return SyncStatusTexts.AccountStatuses(states, Accounts(), (acc, id) => lookup(acc, id), time.GetUtcNow());
    }

    /// <summary>
    /// The line of a refresh the user asked for (sync.go <c>startSync</c>):
    /// "Checking for new mail…" with the spinner at once, and a fallback
    /// timer that recomputes the line after <see cref="FallbackDelay"/> in
    /// case no <c>notify.syncState</c> follows. Calling it again restarts the
    /// timer. As in GTK only the text and the spinner change: the
    /// connection's icon and the rest of the line stay as they are. Once
    /// closed it does nothing.
    /// </summary>
    public void BeginChecking()
    {
        scope.VerifyAccess();
        if (closed)
        {
            return;
        }
        var text = L10n.T("Checking for new mail…");
        SetFooter(new FooterState(text, true));
        SetLine(Line with { Text = text, Spinning = true });
        fallback?.Cancel();
        var timer = new CancellationTokenSource();
        fallback = timer;
        var delay = FallbackDelay;
        scope.RunDetached(async lifetime =>
        {
            try
            {
                using var linked = CancellationTokenSource.CreateLinkedTokenSource(lifetime, timer.Token);
                await Task.Delay(delay, time, linked.Token);
                if (timer.IsCancellationRequested || closed)
                {
                    return;
                }
                fallback = null;
                RefreshFooter();
            }
            catch (OperationCanceledException) when (timer.IsCancellationRequested)
            {
                // Restarted or closed.
            }
            finally
            {
                // What is disposed must not stay behind for close() to cancel.
                if (fallback == timer)
                {
                    fallback = null;
                }
                timer.Dispose();
            }
        });
    }

    private void SetFooter(FooterState f)
    {
        if (closed)
        {
            return;
        }
        Footer = f;
        scope.Raise(FooterChanged, this, f);
    }

    private void SetLine(StatusLine l)
    {
        if (closed)
        {
            return;
        }
        Line = l;
        scope.Raise(StatusLineChanged, this, l);
    }

    // Certificate banner

    /// <summary>
    /// The certificate banner (sync.go <c>refreshCertBanner</c>): up for the
    /// first enabled account, in account order, whose server's certificate
    /// was refused or has changed (<c>certProblemAccount</c>), hidden when
    /// there is none. Emits only a change.
    /// </summary>
    private void RefreshCertBanner(IReadOnlyList<Account> accounts)
    {
        if (closed)
        {
            return;
        }
        if (SyncStatusTexts.CertProblemAccount(states, accounts) is not { } found)
        {
            if (CertBannerAccount is null)
            {
                return;
            }
            CertBannerAccount = null;
            certBannerTitle = null;
            scope.Raise(CertBannerChanged, this, SyncBanner.Hidden);
            return;
        }
        var a = found.Account;
        var title = SyncStatusTexts.CertBannerText(found.Problem.Category, AccountsPage.AccountRowTitle(a));
        if (CertBannerAccount == a.Id && certBannerTitle == title)
        {
            return;
        }
        CertBannerAccount = a.Id;
        certBannerTitle = title;
        // TRANSLATORS: banner button
        scope.Raise(CertBannerChanged, this, new SyncBanner(a.Id, title, WithoutMnemonic(L10n.T("_Edit Account…"))));
    }

    // Sign-in banner

    /// <summary>
    /// The banner for a <c>notify.authRequired</c> (sync.go
    /// <c>showAuthRequired</c>): the sentence and the button label.
    /// <paramref name="account"/> is the notified account when known;
    /// without it the id stands in for the name. For an account whose
    /// sign-in lives in GNOME Online Accounts the button opens that panel
    /// instead of the preferences (never the case on Windows, kept for the
    /// text table's parity); an account of the browser sign-in is signed in
    /// again from the button; a password account whose password is missing
    /// or was refused says so and edits the account (Swift
    /// <c>authBanner(for:account:)</c>).
    /// </summary>
    public static (string Title, string Button) AuthBannerFor(AuthRequiredNotification n, Account? account)
    {
        ArgumentNullException.ThrowIfNull(n);
        var name = account is null ? n.AccountId.Value : AccountsPage.AccountRowTitle(account);
        var kind = AuthBannerKind(n, account);
        // The banner's button shows no mnemonic.
        var button = WithoutMnemonic(SyncStatusTexts.AuthBannerButton(kind, n.Reason));
        return (SyncStatusTexts.AuthBannerTitle(kind, n.Reason, name), button);
    }

    /// <summary>
    /// The button's action for the notified account (sync.go
    /// <c>showAuthRequired</c>'s kind; Swift <c>authBannerAction(for:account:)</c>).
    /// </summary>
    public static AuthBannerAction AuthBannerActionFor(AuthRequiredNotification n, Account? account)
    {
        ArgumentNullException.ThrowIfNull(n);
        switch (AuthBannerKind(n, account))
        {
            case SignInKind.Goa:
                return new AuthBannerAction.OpenOnlineAccounts();
            case SignInKind.OAuth:
                var url = n.AuthUrl ?? "";
                return new AuthBannerAction.SignInAgain(n.AccountId, url.Length == 0 ? null : url);
            default:
                if (SyncStatusTexts.EditsPassword(SignInKind.Password, n.Reason))
                {
                    return new AuthBannerAction.EditAccount(n.AccountId, n.Reason);
                }
                return new AuthBannerAction.OpenPreferences();
        }
    }

    /// <summary>
    /// Reveals the banner for the notified account. The <c>authUrl</c> of an
    /// account of the browser sign-in is kept as the fallback of its button;
    /// it is never logged.
    /// </summary>
    public void ShowAuthRequired(AuthRequiredNotification n, Account? account)
    {
        ArgumentNullException.ThrowIfNull(n);
        scope.VerifyAccess();
        var action = AuthBannerActionFor(n, account);
        LogAuthRequired(logger, n.AccountId.Value, n.Reason.Name, n.AuthUrl is not null);
        AuthBannerAccount = n.AccountId;
        AuthBannerAction = action;
        var (title, button) = AuthBannerFor(n, account);
        scope.Raise(AuthBannerChanged, this, new SyncBanner(n.AccountId, title, button));
    }

    /// <summary>Hides the banner and forgets its account (sync.go <c>hideAuthBanner</c>).</summary>
    public void HideAuthBanner()
    {
        scope.VerifyAccess();
        var wasShown = AuthBannerAccount is not null;
        AuthBannerAccount = null;
        AuthBannerAction = null;
        if (wasShown)
        {
            scope.Raise(AuthBannerChanged, this, SyncBanner.Hidden);
        }
    }

    /// <summary>
    /// The banner's Sign In for an account of the browser sign-in (sync.go
    /// <c>signInInBrowser</c>): a fresh page from <c>account.oauthStart</c>
    /// with the account's id (the daemon hands back the session it is already
    /// waiting on), or the notification's <c>authUrl</c> when the daemon
    /// cannot answer; only an https address is opened. The daemon completes
    /// the sign-in by itself and the banner goes with the next
    /// <c>notify.syncState</c>. Awaited by its caller, so the call is made
    /// directly rather than through the scope. A caller that cancels
    /// <paramref name="cancellationToken"/> gets the
    /// <see cref="OperationCanceledException"/>, not the fallback: it
    /// abandoned the request, and nothing is to be opened for it.
    /// </summary>
    public async Task<SignInUrl> RequestSignInUrlAsync(
        RpcClient client, AccountId accountId, string? fallbackUrl, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(client);
        var url = "";
        Exception? failure = null;
        try
        {
            var parameters = new AccountOAuthStartParams { AccountId = accountId, BrowserPage = OAuth.BrowserPage() };
            url = (await client.CallAsync(API.AccountOAuthStart, parameters, RpcTimeouts.OAuthStart, cancellationToken)).AuthUrl;
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
#pragma warning disable CA1031 // Every failure of the start falls back to the notification's page, as Swift's catch-all does.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogOAuthStartFailed(logger, accountId.Value, e);
            failure = e;
            url = fallbackUrl ?? "";
        }
        if (url.Length == 0)
        {
            return new SignInUrl.Failed(RpcErrorText.Text(L10n.T("Starting the sign-in"), failure));
        }
        if (!OAuth.IsBrowserUrl(url))
        {
            LogSignInUrlRefused(logger);
            return new SignInUrl.Failed(OAuth.RefusedBrowserUrlText());
        }
        return new SignInUrl.Open(url);
    }

    /// <summary>
    /// Notifies the bindings of a change; a handler that throws is reported
    /// and does not keep the change's other outputs from following
    /// (<see cref="ControllerEvents"/>).
    /// </summary>
    protected override void OnPropertyChanged(PropertyChangedEventArgs e) => scope.Guard(() => base.OnPropertyChanged(e));

    /// <summary>
    /// Notifies the bindings of a change to come; a handler that throws is
    /// reported and does not keep the change from being made.
    /// </summary>
    protected override void OnPropertyChanging(PropertyChangingEventArgs e) => scope.Guard(() => base.OnPropertyChanging(e));

    /// <summary>
    /// How the notified account signs in: by its config, or, for an account
    /// not listed yet, the daemon's own sign-in when the notification carries
    /// a URL (only that sign-in has one).
    /// </summary>
    private static SignInKind AuthBannerKind(AuthRequiredNotification n, Account? account)
    {
        if (account is not null)
        {
            return Provider.SignInKindOf(account.Config);
        }
        return string.IsNullOrEmpty(n.AuthUrl) ? SignInKind.Password : SignInKind.OAuth;
    }

    /// <summary>
    /// A GTK label with its mnemonic marker removed: <c>_Next</c> → <c>Next</c>,
    /// <c>__</c> → <c>_</c> (WizardController.swift <c>withoutMnemonic</c>).
    /// </summary>
    private static string WithoutMnemonic(string s)
    {
        var @out = new StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '_')
            {
                if (i + 1 < s.Length)
                {
                    @out.Append(s[++i]);
                }
                continue;
            }
            @out.Append(s[i]);
        }
        return @out.ToString();
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "sync.status failed")]
    private static partial void LogSyncStatusFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "auth required: account {Account} reason {Reason} authUrl {HasAuthUrl}")]
    private static partial void LogAuthRequired(ILogger logger, string account, string reason, bool hasAuthUrl);

    [LoggerMessage(Level = LogLevel.Warning, Message = "account.oauthStart for {Account} failed")]
    private static partial void LogOAuthStartFailed(ILogger logger, string account, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "sign-in address refused: not https")]
    private static partial void LogSignInUrlRefused(ILogger logger);
}
