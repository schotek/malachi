// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarAccountController.swift,
// with the two calls macOS makes in its pane
// (MalachiMail/Preferences/CalendarPaneViewController.swift removeAccount,
// signInForCalendar); Swift-first, no GTK controller yet: the rows' texts
// and rules are ui/internal/calendar/preferences.go (Cal.Prefs).
//
// One account's section of Preferences → Calendar (docs/calendar.md §5.8,
// "As built (M9)") over account.update and calendar.list: Sync This
// Account's Calendars (a Graph account; switching on when the grant lacks
// the calendar scopes ends in state consentRequired, and the controller
// then asks with Cal.Prefs.SyncConsentNote and hands the account to the
// re-sign-in with calendar: true), the calendars with their checkboxes
// (disabledCalendars), the default calendar for new events, Keep Events,
// and the invitation switches with After Answering; for a CalDAV account
// its connection (name, server, username, Replace Password…, the mail
// account for invitations, the certificate with Forget) and Remove Account.
// Each change is one account.update of the whole configuration with empty
// credentials (the keyring keeps the secret), except Replace Password….
//
// The calendars' checkboxes and the default are keyed by the calendars'
// remote ids (CalendarRecord.remoteId; calendar.list lists the disabled
// calendars too, with disabled), for Graph and CalDAV alike; a CalDAV
// account also lists the server's calendars (account.listCalDAVCalendars
// with accountId) so that one the daemon does not store yet can be
// switched on. Preferences → Calendar is the only place that lists the
// disabled calendars.
//
// Windows: Swift's onChange is Changed, onSignIn SignInRequested,
// onAddCalDAV AddCalDAVRequested, onQuestion the AskQuestion hook (null
// answers Cancel). What macOS does in its pane moves here (docs/windows-port.md
// §7.4): Remove asks the Accounts page's question (ConfirmAccountRemoval,
// the check box Also delete drafts and downloaded data) and calls
// account.remove, RequestSignInUrlAsync asks account.oauthStart {accountId,
// calendar: true} for the page the view opens on SignInRequested (as
// SyncController.RequestSignInUrlAsync does for the mail); both need the
// daemon's client, which the calendar source does not carry, and say "The
// preview cannot do that." without one (the samples). Their failures are
// ToastRequested. Deviation (named): Saving counts the account.update
// calls under way, where Swift's flag drops with the first answer, so the
// source's configuration is taken only once none is under way. The
// records compare their lists by reference, so the configurations are
// compared with their calendar lists by value (SameConfig). A write is a
// mutation: it is sent even when the section stops meanwhile, its answer
// then dropped (PerformPastClose); the server's calendars are not asked
// for once it stopped.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;

namespace Malachi.Core.Controllers;

/// <summary>
/// One account's Calendar and Invitations groups, or a CalDAV account's
/// section, in Preferences → Calendar (Swift <c>CalendarAccountController</c>).
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>; <see cref="Stop"/> when the
/// section goes. After every <see cref="Changed"/> the section reads the
/// controller again: which rows apply (<see cref="Cal.Prefs.PreferencesLayout"/>,
/// <see cref="ShowsSyncSwitch"/>, <see cref="ShowsNoCalendar"/>), their
/// values, <see cref="Saving"/>, <see cref="Removing"/> and <see cref="Error"/>
/// (the section's status line).
/// </remarks>
public sealed partial class CalendarAccountController : IDisposable
{
    private readonly ControllerScope scope;
    private readonly RpcClient? client;
    private BoardObserverToken? observer;

    // Sync was switched on: ask for the consent when the state says it lacks.
    private bool askConsent;

    // The account.update calls under way.
    private int writes;

    /// <summary>The section of the account <paramref name="accountId"/>, on the calling (UI) thread.</summary>
    /// <param name="source">The calendar's data and its writes.</param>
    /// <param name="accountId">The account.</param>
    /// <param name="client">
    /// The daemon, for what the calendar source does not carry: Remove
    /// Account (<c>account.remove</c>) and the calendar's sign-in
    /// (<c>account.oauthStart</c>); null for the samples, which refuse both.
    /// </param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public CalendarAccountController(ICalendarDataSource source, AccountId accountId, RpcClient? client = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        AccountId = accountId;
        this.client = client;
        scope = new ControllerScope(pending);
        Config = source.Snapshot.FindAccount(accountId)?.Config;
    }

    // Outputs

    /// <summary>What the section shows changed: it reads the controller again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>
    /// The re-sign-in of the account with the calendar scopes (Swift
    /// <c>onSignIn</c>): the view asks <see cref="RequestSignInUrlAsync"/>
    /// for the page and opens it, or shows its failure.
    /// </summary>
    public event EventHandler<AccountId>? SignInRequested;

    /// <summary>Add CalDAV Calendar… of an account without a calendar (Swift <c>onAddCalDAV</c>).</summary>
    public event EventHandler? AddCalDAVRequested;

    /// <summary>A short sentence for a toast: Remove Account failed (Windows; macOS's pane toasts it).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>Asks a question (the consent explanation) and completes with the answer; null answers Cancel (Swift <c>onQuestion</c>).</summary>
    public Func<Cal.Question, Task<Cal.Answer>>? AskQuestion { get; set; }

    // State

    /// <summary>The calendar source.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The account.</summary>
    public AccountId AccountId { get; }

    /// <summary>An account.update is under way.</summary>
    public bool Saving => writes > 0;

    /// <summary>account.remove is under way (Remove Account waits).</summary>
    public bool Removing { get; private set; }

    /// <summary>Why the last change was refused, for the section's status line; "" for none.</summary>
    public string Error { get; private set; } = "";

    /// <summary>The CalDAV calendars as account.listCalDAVCalendars listed them; null before (and for any other account).</summary>
    public IReadOnlyList<CalDAVCalendar>? Listed { get; private set; }

    /// <summary>The account's configuration as shown (the written change before its answer); null for an unknown account.</summary>
    public AccountConfig? Config { get; private set; }

    /// <summary>Whether <see cref="Stop"/> ran: answers are dropped.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Follows the source; for a CalDAV account also lists the server's calendars.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (observer is not null || IsClosed)
        {
            return;
        }
        observer = Source.Observe(c =>
        {
            if (c is Cal.SourceChange.SnapshotChanged)
            {
                SnapshotChanged();
            }
        });
        if (IsCalDAV)
        {
            LoadCalDAV();
        }
        SnapshotChanged();
    }

    /// <summary>The section went: nothing is taken any more (a change already sent still reaches the daemon).</summary>
    public void Stop()
    {
        scope.VerifyAccess();
        observer?.Cancel();
        observer = null;
        scope.Close();
    }

    /// <summary>Stops the controller.</summary>
    public void Dispose() => Stop();

    // Derived

    /// <summary>The account as the source lists it.</summary>
    public Account? Account => Source.Snapshot.FindAccount(AccountId);

    /// <summary>The account's calendars (the disabled ones too) and state.</summary>
    public CalendarAccount? CalendarAccount => Source.Snapshot.FindCalendarAccount(AccountId);

    /// <summary>A CalDAV account.</summary>
    public bool IsCalDAV => Kind is { } k && k == AccountKind.CalDAV;

    /// <summary>A Graph (Microsoft 365) account.</summary>
    public bool IsGraph => Kind is { } k && k == AccountKind.Graph;

    /// <summary>The account can have calendars (capability <c>calendar</c>).</summary>
    public bool HasCalendarCapability => Account is { Capabilities: { } caps } && caps.Contains(Capability.Calendar);

    /// <summary>An account without the capability (IMAP): "This account has no calendar." with Add CalDAV Calendar….</summary>
    public bool ShowsNoCalendar => !HasCalendarCapability;

    /// <summary>The calendar part of the configuration as shown (sync off for a Graph account without one).</summary>
    public CalendarConfig CalendarConfig => Config?.Calendar ?? new CalendarConfig { Sync = IsCalDAV };

    /// <summary>The Sync switch (a CalDAV account always synchronises: no switch).</summary>
    public bool ShowsSyncSwitch => IsGraph;

    /// <summary>The Sync switch's state.</summary>
    public bool SyncOn => IsCalDAV || CalendarConfig.Sync;

    /// <summary>The account's grant lacks the calendar scopes (the consent row with Sign In…).</summary>
    public bool ConsentRequired => CalendarAccount?.State.Status == CalendarSyncStatus.ConsentRequired;

    /// <summary>
    /// The calendars of calendar.list (the disabled ones too, by their
    /// remote ids), the checkboxes as the shown configuration says; for a
    /// CalDAV account also the server's calendars the daemon does not
    /// store yet (account.listCalDAVCalendars).
    /// </summary>
    public IReadOnlyList<CalendarRow> Rows
    {
        get
        {
            var cal = CalendarConfig;
            var disabled = new HashSet<string>(cal.DisabledCalendars ?? [], StringComparer.Ordinal);
            var def = cal.DefaultCalendar;
            var output = new List<CalendarRow>();
            foreach (var c in CalendarAccount?.Calendars ?? [])
            {
                var remote = c.RemoteId.Length == 0 ? null : c.RemoteId;
                var isDefault = remote is not null && def is not null ? def == remote : c.IsDefault == true;
                output.Add(new CalendarRow
                {
                    RemoteId = remote,
                    Name = Cal.CleanLine(c.Name),
                    Color = c.Color,
                    Enabled = remote is not null ? !disabled.Contains(remote) : c.Disabled != true,
                    Note = Cal.Prefs.CalendarRowNote(isDefault, c.Access, c.Owner),
                    Writable = Cal.Prefs.Writable(c),
                    IsDefault = isDefault,
                });
            }
            var known = output.Where(r => r.RemoteId is not null).Select(r => r.RemoteId!).ToHashSet(StringComparer.Ordinal);
            foreach (var c in Listed ?? [])
            {
                if (known.Contains(c.Href))
                {
                    continue;
                }
                var isDefault = def is not null ? def == c.Href : c.IsDefault == true;
                output.Add(new CalendarRow
                {
                    RemoteId = c.Href,
                    Name = Cal.CleanLine(c.Name),
                    Color = c.Color ?? "",
                    Enabled = !disabled.Contains(c.Href),
                    Note = Cal.Prefs.CalendarRowNote(isDefault, c.Access, null),
                    Writable = c.Access == CalendarAccess.Owner || c.Access == CalendarAccess.Write,
                    IsDefault = isDefault,
                });
            }
            return output;
        }
    }

    /// <summary>The Default Calendar popup's choices: the enabled writable rows with a remote id.</summary>
    public IReadOnlyList<CalendarRow> DefaultChoices => [.. Rows.Where(r => r.Enabled && r.Writable && r.CanToggle)];

    /// <summary>Keep Events' choices in days (<see cref="Cal.Prefs.KeepTitle"/>).</summary>
    public static IReadOnlyList<int> KeepChoices => Cal.Prefs.KeepChoices;

    /// <summary>Keep Events, in days (0 read as <see cref="API.Limits.DefaultCalendarKeepDays"/>).</summary>
    public int KeepDays => CalendarConfig.KeepDays is { } k and not 0 ? k : API.Limits.DefaultCalendarKeepDays;

    /// <summary>Show the Invitation Card above the Message in Mail.</summary>
    public bool InvitationCard => CalendarConfig.ShowsInvitationCard;

    /// <summary>Show Unanswered Invitations Tentatively in the Calendar.</summary>
    public bool ShowUnanswered => CalendarConfig.ShowsUnanswered;

    /// <summary>After Answering, Move the Invitation Message (<see cref="Cal.Prefs.AfterAnswers"/>).</summary>
    public AfterAnswer AfterAnswer => CalendarConfig.AfterAnswer ?? new AfterAnswer(AfterAnswer.Keep);

    // The account's kind as shown: the configuration's, else the source's.
    private AccountKind? Kind => Config?.Kind ?? Account?.Config.Kind;

    // Changes

    /// <summary>Sync This Account's Calendars; switching on asks for the consent when the state then lacks it.</summary>
    public void SetSync(bool on)
    {
        if (!ShowsSyncSwitch || on == CalendarConfig.Sync)
        {
            return;
        }
        askConsent = on;
        Change(c => c with { Sync = on });
    }

    /// <summary>A calendar's checkbox, by its remote id; switching the default off drops it.</summary>
    public void SetEnabled(bool on, string remoteId)
    {
        ArgumentNullException.ThrowIfNull(remoteId);
        if (!Rows.Any(r => r.RemoteId == remoteId))
        {
            return;
        }
        Change(c =>
        {
            var d = (c.DisabledCalendars ?? []).Where(x => !string.Equals(x, remoteId, StringComparison.Ordinal)).ToList();
            if (!on)
            {
                d.Add(remoteId);
            }
            return c with
            {
                DisabledCalendars = d.Count == 0 ? null : d,
                DefaultCalendar = !on && c.DefaultCalendar == remoteId ? null : c.DefaultCalendar,
            };
        });
    }

    /// <summary>Default Calendar for New Events, by remote id; null is the server's default.</summary>
    public void SetDefaultCalendar(string? remoteId)
    {
        if (remoteId != CalendarConfig.DefaultCalendar)
        {
            Change(c => c with { DefaultCalendar = remoteId });
        }
    }

    /// <summary>Keep Events, one of <see cref="KeepChoices"/>.</summary>
    public void SetKeepDays(int days)
    {
        if (days != KeepDays && Cal.Prefs.KeepChoices.Contains(days))
        {
            Change(c => c with { KeepDays = days });
        }
    }

    /// <summary>Show the Invitation Card above the Message in Mail.</summary>
    public void SetInvitationCard(bool on)
    {
        if (on != InvitationCard)
        {
            Change(c => c with { InvitationCard = on });
        }
    }

    /// <summary>Show Unanswered Invitations Tentatively in the Calendar.</summary>
    public void SetShowUnanswered(bool on)
    {
        if (on != ShowUnanswered)
        {
            Change(c => c with { ShowUnanswered = on });
        }
    }

    /// <summary>After Answering, Move the Invitation Message.</summary>
    public void SetAfterAnswer(AfterAnswer a)
    {
        if (a != AfterAnswer)
        {
            Change(c => c with { AfterAnswer = a });
        }
    }

    /// <summary>The consent row's Sign In… (and a confirmed consent question): <see cref="SignInRequested"/>.</summary>
    public void SignIn()
    {
        scope.VerifyAccess();
        scope.Raise(SignInRequested, this, AccountId);
    }

    /// <summary>Add CalDAV Calendar… of an account without a calendar: <see cref="AddCalDAVRequested"/>.</summary>
    public void AddCalDAV()
    {
        scope.VerifyAccess();
        RaiseEmpty(AddCalDAVRequested);
    }

    // The CalDAV account's group

    /// <summary>The CalDAV connection as shown; null for any other account.</summary>
    public CalDAVConfig? CalDAV => IsCalDAV ? Config?.CalDAV : null;

    /// <summary>The account's name as typed.</summary>
    public string Name => Config?.Name ?? "";

    /// <summary>The server's host (the Server row; the address cleaned when it has none).</summary>
    public string ServerHost
    {
        get
        {
            if (CalDAV?.ServerUrl is not { } u)
            {
                return "";
            }
            var h = Cal.CalDAVWizard.Host(u);
            return h.Length == 0 ? Cal.CleanLine(u) : h;
        }
    }

    /// <summary>The Username row, cleaned.</summary>
    public string Username => Cal.CleanLine(CalDAV?.Username);

    /// <summary>The accounts invitations can leave from (the popup after None).</summary>
    public IReadOnlyList<CalDAVWizardController.MailAccount> MailAccounts => CalDAVWizardController.MailAccountsOf(Source.Snapshot);

    /// <summary>The mail account invitations leave from; null for None.</summary>
    public AccountId? MailAccountId => CalDAV?.MailAccountId is { } m && !string.IsNullOrEmpty(m.Value) ? m : default(AccountId?);

    /// <summary>Whether the server leaves invitations to the client (account.listCalDAVCalendars); null before it answered.</summary>
    public CalDAVScheduling? Scheduling { get; private set; }

    /// <summary>A certificate is pinned (the Certificate row offers Forget).</summary>
    public bool CertificatePinned => !string.IsNullOrEmpty(CalDAV?.CertificateSha256);

    /// <summary>The Certificate row: "Trusted by you", "Verified", "Not encrypted (local server)".</summary>
    public string CertificateText
    {
        get
        {
            if (CertificatePinned)
            {
                return L10n.T("Trusted by you");
            }
            if ((CalDAV?.ServerUrl ?? "").StartsWith("http://", StringComparison.OrdinalIgnoreCase))
            {
                return L10n.T("Not encrypted (local server)");
            }
            return L10n.T("Verified");
        }
    }

    /// <summary>The Account Name field; an empty name is not written.</summary>
    public void SetName(string name)
    {
        var n = Cal.TrimSpace(name);
        if (IsCalDAV && n.Length > 0 && !string.Equals(n, Name, StringComparison.Ordinal))
        {
            ChangeConfig(c => c with { Name = n });
        }
    }

    /// <summary>Mail Account for Invitations; null is None.</summary>
    public void SetMailAccount(AccountId? id)
    {
        if (IsCalDAV && id != MailAccountId)
        {
            ChangeConfig(c => c with { CalDAV = c.CalDAV is { } d ? d with { MailAccountId = id } : null });
        }
    }

    /// <summary>The Certificate row's Forget: the pin goes; the server's certificate must verify again.</summary>
    public void ForgetCertificate()
    {
        if (CertificatePinned)
        {
            ChangeConfig(c => c with { CalDAV = c.CalDAV is { } d ? d with { CertificateSha256 = null } : null });
        }
    }

    /// <summary>
    /// Replace Password…: the new password goes to the keyring with
    /// account.update; it is never kept here.
    /// </summary>
    public void ReplacePassword(string password)
    {
        ArgumentNullException.ThrowIfNull(password);
        if (IsCalDAV && password.Length > 0)
        {
            ChangeConfig(c => c, new Credentials { Password = password });
        }
    }

    /// <summary>
    /// Remove Account (a CalDAV account's button): the Integrations page's
    /// question through <paramref name="confirm"/>, then account.remove with
    /// its check box as <c>deleteLocalData</c>; the section goes with the
    /// source's next snapshot (asked for at once). A failure is
    /// <see cref="ToastRequested"/>.
    /// </summary>
    public void Remove(ConfirmAccountRemoval confirm)
    {
        ArgumentNullException.ThrowIfNull(confirm);
        scope.VerifyAccess();
        if (IsClosed || Removing || Account is not { } account)
        {
            return;
        }
        if (client is not { } c)
        {
            scope.Raise(ToastRequested, this, Cal.Text.PreviewOnly);
            return;
        }
        var prompt = new AccountRemovalPrompt(
            L10n.T("Remove this account?"),
            // The CalDAV sentence of the Integrations page (preferences_calendar.go removeAccount).
            Integrations.RowFor(account).RemoveBody,
            L10n.T("_Remove"),
            L10n.T("Also delete _drafts and downloaded data"),
            ExtraDefault: true);
        scope.Perform(_ => confirm(prompt), answer =>
        {
            if (!answer.TryGetValue(out var a, out _) || !a.Confirmed || Removing)
            {
                return;
            }
            Removing = true;
            RaiseEmpty(Changed);
            // A mutation the user confirmed: sent even when the section goes meanwhile.
            scope.PerformPastClose(c, API.AccountRemove, new AccountRemoveParams { AccountId = AccountId, DeleteLocalData = a.DeleteLocalData }, outcome =>
            {
                Removing = false;
                if (outcome.Error is { } error)
                {
                    RaiseEmpty(Changed);
                    scope.Raise(ToastRequested, this, RpcErrorText.Text(L10n.T("Removing the account"), error));
                    return;
                }
                RaiseEmpty(Changed);
                Source.Refresh();
            });
        });
    }

    /// <summary>
    /// The page of the calendar's sign-in for <see cref="SignInRequested"/>
    /// (macOS's signInForCalendar): <c>account.oauthStart</c> with the
    /// account's id and <c>calendar: true</c>, so the grant asks for the
    /// calendar scopes too; the daemon finishes the sign-in by itself and the
    /// state follows notify.calendarState. Only an https page is handed out.
    /// A caller that cancels <paramref name="cancellationToken"/> gets the
    /// <see cref="OperationCanceledException"/>.
    /// </summary>
    public async Task<SignInUrl> RequestSignInUrlAsync(CancellationToken cancellationToken = default)
    {
        scope.VerifyAccess();
        if (client is null)
        {
            return new SignInUrl.Failed(Cal.Text.PreviewOnly);
        }
        string url;
        try
        {
            var parameters = new AccountOAuthStartParams { AccountId = AccountId, BrowserPage = OAuth.BrowserPage(), Calendar = true };
            url = (await client.CallAsync(API.AccountOAuthStart, parameters, RpcTimeouts.OAuthStart, cancellationToken)).AuthUrl;
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
        {
            return new SignInUrl.Failed(RpcErrorText.Text(L10n.T("Starting the sign-in"), e));
        }
        return OAuth.IsBrowserUrl(url) ? new SignInUrl.Open(url) : new SignInUrl.Failed(OAuth.RefusedBrowserUrlText());
    }

    /// <summary>
    /// Whether <paramref name="a"/> and <paramref name="b"/> hold the same
    /// configuration, the calendar part's list compared by value (Swift's
    /// Equatable for what the section changes).
    /// </summary>
    public static bool SameConfig(AccountConfig? a, AccountConfig? b)
    {
        if (a is null || b is null)
        {
            return ReferenceEquals(a, b);
        }
        return SameCalendar(a.Calendar, b.Calendar) && (a with { Calendar = b.Calendar }) == b;
    }

    private static bool SameCalendar(CalendarConfig? a, CalendarConfig? b)
    {
        if (a is null || b is null)
        {
            return ReferenceEquals(a, b);
        }
        var listsEqual = a.DisabledCalendars is null || b.DisabledCalendars is null
            ? ReferenceEquals(a.DisabledCalendars, b.DisabledCalendars)
            : a.DisabledCalendars.SequenceEqual(b.DisabledCalendars, StringComparer.Ordinal);
        return listsEqual && (a with { DisabledCalendars = b.DisabledCalendars }) == b;
    }

    // One account.update with the calendar part changed by f.
    private void Change(Func<CalendarConfig, CalendarConfig> f)
    {
        var cal = f(CalendarConfig);
        ChangeConfig(c => c with { Calendar = cal });
    }

    // One account.update of the whole configuration changed by f;
    // credentials null (empty) keep the stored secret.
    private void ChangeConfig(Func<AccountConfig, AccountConfig> f, Credentials? credentials = null)
    {
        scope.VerifyAccess();
        if (IsClosed || Config is not { } before)
        {
            return;
        }
        var cfg = f(before);
        var secrets = credentials ?? new Credentials();
        if (SameConfig(cfg, before) && secrets == new Credentials())
        {
            return;
        }
        Config = cfg;
        writes++;
        Error = "";
        RaiseEmpty(Changed);
        var parameters = new AccountUpdateParams { AccountId = AccountId, Config = cfg, Credentials = secrets };
        scope.PerformPastClose(
            async () =>
            {
                await Source.UpdateAccountAsync(parameters);
                return true;
            },
            outcome =>
            {
                writes--;
                if (outcome.Error is { } error)
                {
                    Config = before;
                    askConsent = false;
                    Error = Cal.ErrorMessage(error, L10n.T("Saving the account"));
                }
                RaiseEmpty(Changed);
                SnapshotChanged();
            });
    }

    // account.listCalDAVCalendars with the account's stored password; a
    // failure leaves the rows of calendar.list (Swift's try?).
    private void LoadCalDAV()
    {
        if (Config is not { } cfg)
        {
            return;
        }
        var parameters = new AccountListCalDAVCalendarsParams { AccountId = AccountId, Config = cfg, Credentials = new Credentials() };
        scope.Perform(
            ct =>
            {
                ct.ThrowIfCancellationRequested();
                return Source.ListCalDAVCalendarsAsync(parameters);
            },
            outcome =>
            {
                if (outcome.Error is not null || outcome.Value is not { } r)
                {
                    return;
                }
                Listed = r.Calendars;
                Scheduling = r.Scheduling;
                RaiseEmpty(Changed);
            });
    }

    // The source's snapshot: its configuration while nothing is being
    // written, and the consent question once switching sync on ended in
    // consentRequired.
    private void SnapshotChanged()
    {
        if (writes == 0 && Source.Snapshot.FindAccount(AccountId)?.Config is { } c && !SameConfig(c, Config))
        {
            Config = c;
        }
        if (askConsent && ConsentRequired)
        {
            askConsent = false;
            if (AskQuestion is { } ask)
            {
                var q = new Cal.Question { Heading = Cal.Text.ConsentLine, Body = Cal.Prefs.SyncConsentNote, Confirm = Cal.Text.SignIn };
                scope.Perform(_ => ask(q), answer =>
                {
                    if (answer.TryGetValue(out var a, out _) && a == Cal.Answer.Confirm)
                    {
                        SignIn();
                    }
                });
            }
        }
        RaiseEmpty(Changed);
    }

    // Raises a parameterless event unless stopped, every handler guarded (ControllerEvents).
    private void RaiseEmpty(EventHandler? handlers)
    {
        if (IsClosed || handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }
}
