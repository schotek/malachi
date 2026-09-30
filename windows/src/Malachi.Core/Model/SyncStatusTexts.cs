// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SyncStatus.swift (syncStatusText,
// sendingText, notSentText, accountStatuses, accountDetail,
// statusButtonLabel, statusButtonMnemonic, sameAccounts, authBannerButton,
// authBannerText, editsPassword, goaAuthBannerText, oauthAuthBannerText,
// certStatusText, certProblemAccount, certBannerText); GTK:
// ui/internal/window/sync.go (syncStatusText, sendingText, notSentText,
// certStatusText, certProblemAccount, certBannerText, accountAuthBannerTitle, authBannerTitle,
// authBannerButton, editsPassword, authBannerText, goaAuthBannerText,
// oauthAuthBannerText) and status.go (accountStatuses, accountDetail,
// statusButtonLabel, statusButtonMnemonic, sameAccounts). authBannerTitle,
// which macOS keeps in its sync controller, is here with the texts it
// chooses between. StatusAction and AccountStatus have files of their own;
// the class is not named SyncStatus, which is the wire enum's name.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Text;
using Malachi.Core.Wizard;

namespace Malachi.Core.Model;

/// <summary>
/// The status line, its popover and the sign-in and certificate banners,
/// the pure parts. The daemon owns the sync state; these only mirror the
/// last <c>sync.status</c> / <c>notify.syncState</c> per account into text.
/// </summary>
public static class SyncStatusTexts
{
    /// <summary>
    /// The status line for the given states (sync.go <c>syncStatusText</c>).
    /// Only enabled accounts count; an account missing from
    /// <paramref name="states"/> uses the state embedded in its
    /// <c>account.list</c> entry. The most pressing state wins: syncing (with
    /// the folder or account name and the progress when known), then sign-in
    /// required, a changed certificate, a certificate problem
    /// (<see cref="CertTrust.FromSyncState"/>: an offline or failed account
    /// whose server's certificate was refused, instead of "Offline"), sending
    /// (the pending outbox messages of every account added up; sending is not
    /// a sync status, so it shows while the status is idle), messages that
    /// were not sent (failed, added up the same way), error, offline, and
    /// finally "Up to date" with the time of the newest last check. Sign-in
    /// required, error and offline name the account when exactly one is in
    /// that state and more than one is enabled; with a single account the
    /// name would say nothing, with several it could not be one (the popover
    /// lists them). The certificate states leave the name to the certificate
    /// banner. When every account is paused the line says so; with no
    /// account at all it is empty. <paramref name="folderName"/> returns the
    /// display name of a folder or "" when unknown; <paramref name="now"/> is
    /// the moment the time of the last check is shown against.
    /// </summary>
    public static (string Text, bool Spinning) SyncStatusText(
        IReadOnlyDictionary<AccountId, SyncState> states, IReadOnlyList<Account> accounts,
        Func<AccountId, FolderId, string>? folderName, DateTimeOffset now)
    {
        ArgumentNullException.ThrowIfNull(states);
        ArgumentNullException.ThrowIfNull(accounts);
        SyncState? syncing = null;
        var syncingName = "";
        var authRequired = new List<Account>();
        var certChanged = false;
        var certProblem = false;
        var syncError = new List<Account>();
        var offline = new List<Account>();
        var enabled = 0;
        var pending = 0;
        var failed = 0;
        DateTimeOffset? lastSync = null;
        foreach (var a in accounts)
        {
            if (!a.Enabled)
            {
                continue;
            }
            enabled++;
            var s = states.GetValueOrDefault(a.Id) ?? a.State;
            pending += s.PendingOutbox;
            failed += s.FailedOutbox;
            if (s.LastSync is { } t && !t.IsGoZero && (lastSync is null || t > lastSync))
            {
                lastSync = t;
            }
            if (CertTrust.FromSyncState(s) is { } p)
            {
                if (p.Category == CertTrust.Category.Changed)
                {
                    certChanged = true;
                }
                else
                {
                    certProblem = true;
                }
            }
            switch (s.Status.Value)
            {
                case SyncStatus.Syncing:
                    if (syncing is null)
                    {
                        syncing = s;
                        if (s.FolderId is { } folderId && folderName is not null)
                        {
                            syncingName = folderName(a.Id, folderId);
                        }
                        if (syncingName.Length == 0)
                        {
                            syncingName = AccountsPage.AccountRowTitle(a);
                        }
                    }
                    break;
                case SyncStatus.AuthRequired:
                    authRequired.Add(a);
                    break;
                case SyncStatus.Error:
                    syncError.Add(a);
                    break;
                case SyncStatus.Offline:
                    offline.Add(a);
                    break;
            }
        }
        // The one account in a state, when naming it helps.
        string? Named(List<Account> list) => list.Count == 1 && enabled >= 2 ? AccountsPage.AccountRowTitle(list[0]) : null;
        if (enabled == 0)
        {
            return accounts.Count == 0 ? ("", false) : (L10n.T("Paused"), false);
        }
        if (syncing is not null)
        {
            if (syncing.Progress >= 0)
            {
                // TRANSLATORS: %s is a folder or account name, %d the progress in percent.
                return (L10n.T("Syncing %s… %d %%", syncingName, syncing.Progress), true);
            }
            // TRANSLATORS: %s is a folder or account name.
            return (L10n.T("Syncing %s…", syncingName), true);
        }
        if (authRequired.Count > 0)
        {
            if (Named(authRequired) is { } name)
            {
                // TRANSLATORS: status line; %s is an account name.
                return (L10n.T("Sign-in required: %s", name), false);
            }
            return (L10n.T("Sign-in required"), false);
        }
        if (certChanged)
        {
            return (CertStatusText(CertTrust.Category.Changed), false);
        }
        if (certProblem)
        {
            return (CertStatusText(CertTrust.Category.Certificate), false);
        }
        if (pending > 0)
        {
            return (SendingText(pending), true);
        }
        if (failed > 0)
        {
            return (NotSentText(failed), false);
        }
        if (syncError.Count > 0)
        {
            if (Named(syncError) is { } name)
            {
                // TRANSLATORS: status line; %s is an account name.
                return (L10n.T("Sync error: %s", name), false);
            }
            return (L10n.T("Sync error"), false);
        }
        if (offline.Count > 0)
        {
            if (Named(offline) is { } name)
            {
                // TRANSLATORS: status line of an account that cannot reach its
                // server and keeps trying; %s is an account name.
                return (L10n.T("Offline: %s", name), false);
            }
            return (L10n.T("Offline, retrying"), false);
        }
        if (lastSync is { } last)
        {
            // TRANSLATORS: status line; %s is the time of the last check for
            // new mail, e.g. "15:04", or its date when that was before today.
            return (L10n.T("Up to date · %s", Format.FormatDate(last, now)), false);
        }
        return (L10n.T("Up to date"), false);
    }

    /// <summary>
    /// The status of <paramref name="n"/> messages waiting in the outbox
    /// (sync.go <c>sendingText</c>: the status line, an account's row in its
    /// popover).
    /// </summary>
    public static string SendingText(int n) =>
        // TRANSLATORS: %d is the number of messages waiting in the outbox.
        L10n.N("Sending %d message…", "Sending %d messages…", n);

    /// <summary>
    /// The status of <paramref name="n"/> messages in the outbox whose
    /// delivery failed (sync.go <c>notSentText</c>: the status line, the
    /// popover's link to the outbox).
    /// </summary>
    public static string NotSentText(int n) =>
        // TRANSLATORS: status line; %d is the number of messages in the outbox
        // whose sending failed.
        L10n.N("%d message not sent", "%d messages not sent", n);

    /// <summary>
    /// The status popover's content (status.go <c>accountStatuses</c>): one
    /// entry per account, in <c>account.list</c> order, paused accounts
    /// included. Whether an account is paused is decided by
    /// <see cref="Account.Enabled"/>, not by <paramref name="states"/>:
    /// pausing sends no <c>notify.syncState</c>, so
    /// <paramref name="states"/> keeps what the account said before. A state
    /// that came afterwards (an outbox change of the paused account) says
    /// "disabled" and is used for its failed messages; otherwise they come
    /// from the <c>account.list</c> entry, as does the state of an enabled
    /// account missing from <paramref name="states"/>.
    /// <paramref name="folderName"/> and <paramref name="now"/> are as for
    /// <see cref="SyncStatusText"/>.
    /// </summary>
    public static IReadOnlyList<AccountStatus> AccountStatuses(
        IReadOnlyDictionary<AccountId, SyncState> states, IReadOnlyList<Account> accounts,
        Func<AccountId, FolderId, string>? folderName, DateTimeOffset now)
    {
        ArgumentNullException.ThrowIfNull(states);
        ArgumentNullException.ThrowIfNull(accounts);
        var output = new List<AccountStatus>(accounts.Count);
        foreach (var a in accounts)
        {
            var st = new AccountStatus
            {
                Account = a.Id,
                Title = AccountsPage.AccountRowTitle(a),
                Detail = "",
                SignIn = Provider.SignInKindOf(a.Config),
            };
            if (!a.Enabled)
            {
                var paused = a.State;
                if (states.GetValueOrDefault(a.Id) is { } cached && cached.Status == SyncStatus.Disabled)
                {
                    paused = cached;
                }
                output.Add(st with { Detail = L10n.T("Paused"), Failed = paused.FailedOutbox });
                continue;
            }
            var s = states.GetValueOrDefault(a.Id) ?? a.State;
            var (detail, action) = AccountDetail(a.Id, s, folderName, now);
            st = st with { Detail = detail, Action = action, Failed = s.FailedOutbox };
            if (action == StatusAction.SignIn && s.Error is { } e)
            {
                st = st with { Reason = e.Code };
            }
            output.Add(st);
        }
        return output;
    }

    /// <summary>
    /// The sentence and the action for an enabled account in state
    /// <paramref name="s"/> (status.go <c>accountDetail</c>). The most
    /// pressing state wins: syncing (the folder and progress when known,
    /// never the account's name, which is the row's title), sign-in
    /// required, a refused or changed certificate (the reason, and the
    /// account settings, where it can be trusted), sending, error and offline
    /// (the reason when known, and a retry), and finally idle with the time
    /// of the last check. Syncing and sending keep the check button of idle:
    /// it must not vanish, taking the keyboard focus with it, whenever a pass
    /// starts; only a paused account has no action.
    /// </summary>
    public static (string Detail, StatusAction Action) AccountDetail(
        AccountId acc, SyncState s, Func<AccountId, FolderId, string>? folderName, DateTimeOffset now)
    {
        ArgumentNullException.ThrowIfNull(s);
        switch (s.Status.Value)
        {
            case SyncStatus.Syncing:
                var name = s.FolderId is { } id && folderName is not null ? folderName(acc, id) : "";
                if (name.Length == 0)
                {
                    return (L10n.T("Syncing…"), StatusAction.Check);
                }
                if (s.Progress >= 0)
                {
                    // TRANSLATORS: %s is a folder or account name, %d the progress in percent.
                    return (L10n.T("Syncing %s… %d %%", name, s.Progress), StatusAction.Check);
                }
                // TRANSLATORS: %s is a folder or account name.
                return (L10n.T("Syncing %s…", name), StatusAction.Check);
            case SyncStatus.AuthRequired:
                return (L10n.T("Sign-in required"), StatusAction.SignIn);
        }
        if (CertTrust.FromSyncState(s) is not null)
        {
            return (RpcErrorText.EndpointErrorText(s.Error), StatusAction.Edit);
        }
        if (s.PendingOutbox > 0)
        {
            return (SendingText(s.PendingOutbox), StatusAction.Check);
        }
        switch (s.Status.Value)
        {
            case SyncStatus.Error:
                return (s.Error is { } e ? RpcErrorText.EndpointErrorText(e) : L10n.T("Sync error"), StatusAction.Retry);
            case SyncStatus.Offline:
                return (s.Error is { } o ? RpcErrorText.EndpointErrorText(o) : L10n.T("Offline, retrying"), StatusAction.Retry);
        }
        if (s.LastSync is { } t && !t.IsGoZero)
        {
            // TRANSLATORS: state of an account; %s is the time of its last
            // check for new mail, e.g. "15:04", or its date when that was
            // before today.
            return (L10n.T("Last synced %s", Format.FormatDate(t, now)), StatusAction.Check);
        }
        return (L10n.T("Up to date"), StatusAction.Check);
    }

    /// <summary>
    /// The label of the button that repairs an account (status.go
    /// <c>statusButtonLabel</c>): "" when there is nothing to repair
    /// (checking for new mail is an icon of its own). "_Edit Account…"
    /// carries a mnemonic (<see cref="StatusButtonMnemonic"/>).
    /// </summary>
    public static string StatusButtonLabel(AccountStatus st)
    {
        ArgumentNullException.ThrowIfNull(st);
        return st.Action switch
        {
            StatusAction.Retry => L10n.T("Try Again"),
            StatusAction.SignIn => AuthBannerButton(st.SignIn, st.Reason),
            StatusAction.Edit => L10n.T("_Edit Account…"),
            _ => "",
        };
    }

    /// <summary>
    /// Whether <see cref="StatusButtonLabel"/> carries a mnemonic (status.go
    /// <c>statusButtonMnemonic</c>): the label "_Edit Account…", for a
    /// certificate or a password to fix.
    /// </summary>
    public static bool StatusButtonMnemonic(AccountStatus st)
    {
        ArgumentNullException.ThrowIfNull(st);
        return st.Action == StatusAction.Edit || (st.Action == StatusAction.SignIn && EditsPassword(st.SignIn, st.Reason));
    }

    /// <summary>
    /// Whether the popover's rows, built for the accounts in
    /// <paramref name="order"/>, still fit <paramref name="list"/> (status.go
    /// <c>sameAccounts</c>).
    /// </summary>
    public static bool SameAccounts(IReadOnlyList<AccountId> order, IReadOnlyList<AccountStatus> list)
    {
        ArgumentNullException.ThrowIfNull(order);
        ArgumentNullException.ThrowIfNull(list);
        if (order.Count != list.Count)
        {
            return false;
        }
        for (var i = 0; i < order.Count; i++)
        {
            if (order[i] != list[i].Account)
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>
    /// <see cref="AuthBannerTitle"/> for account <paramref name="a"/> (null
    /// while it is not listed yet), which signs in the way
    /// <paramref name="kind"/> says (sync.go <c>accountAuthBannerTitle</c>):
    /// a Jira account names its token (<see cref="Jira.AuthBannerText"/>), a
    /// keyring failure reads as for mail.
    /// </summary>
    public static string AccountAuthBannerTitle(Account? a, SignInKind kind, ErrorCode reason, string account)
    {
        var text = Jira.AuthBannerText(a?.Config.ProtocolKind ?? AccountKind.Imap, reason, account);
        return text.Length > 0 ? text : AuthBannerTitle(kind, reason, account);
    }
    /// <summary>
    /// The sign-in banner's sentence for an account that signs in the way
    /// <paramref name="kind"/> says, after a <c>notify.authRequired</c> with
    /// <paramref name="reason"/> (sync.go <c>authBannerTitle</c>);
    /// <paramref name="account"/> is the account's display name.
    /// </summary>
    public static string AuthBannerTitle(SignInKind kind, ErrorCode reason, string account) => kind switch
    {
        SignInKind.Goa => GoaAuthBannerText(reason, account),
        SignInKind.OAuth => OAuthAuthBannerText(reason, account),
        _ => AuthBannerText(reason, account),
    };

    /// <summary>
    /// The label of the button that repairs a sign-in (sync.go
    /// <c>authBannerButton</c>), by how the account signs in and why, after a
    /// <c>notify.authRequired</c> with <paramref name="reason"/>: the Online
    /// Accounts panel, the browser, the edit wizard asking for a missing or
    /// refused password ("_Edit Account…", with its mnemonic), or the
    /// preferences.
    /// </summary>
    public static string AuthBannerButton(SignInKind kind, ErrorCode reason)
    {
        switch (kind)
        {
            case SignInKind.Goa:
                return L10n.T("Open Online Accounts");
            case SignInKind.OAuth:
                // TRANSLATORS: a button that signs in; the plain "Sign In" is a page title
                return L10n.C("button", "Sign In");
            default:
                if (EditsPassword(kind, reason))
                {
                    // TRANSLATORS: banner button
                    return L10n.T("_Edit Account…");
                }
                return L10n.T("Open Preferences");
        }
    }

    /// <summary>
    /// The banner sentence of a password account for a
    /// <c>notify.authRequired</c> reason (sync.go <c>authBannerText</c>): a
    /// missing password and a refused one are named, since the password is
    /// what the user enters again (the button edits the account,
    /// <see cref="EditsPassword"/>); <paramref name="account"/> is the
    /// account's display name.
    /// </summary>
    public static string AuthBannerText(ErrorCode reason, string account) => reason.Value switch
    {
        // TRANSLATORS: banner; %s is an account name
        ErrorCode.AuthRequired => L10n.T("No password is stored for %s", account),
        // TRANSLATORS: banner; %s is an account name
        ErrorCode.AuthFailed => L10n.T("The server rejected the password of %s", account),
        // TRANSLATORS: %s is an account name.
        ErrorCode.KeyringError => L10n.T("The system keyring is unavailable; %s cannot sign in", account),
        // TRANSLATORS: %s is an account name.
        _ => L10n.T("%s needs attention", account),
    };

    /// <summary>
    /// Whether the sign-in banner's button asks for the password in the
    /// account's edit wizard instead of opening the preferences (sync.go
    /// <c>editsPassword</c>): a password account whose password is missing
    /// (authRequired) or refused (authFailed). A keyring failure is not the
    /// password's fault.
    /// </summary>
    public static bool EditsPassword(SignInKind kind, ErrorCode reason) =>
        kind == SignInKind.Password && (reason == ErrorCode.AuthRequired || reason == ErrorCode.AuthFailed);

    /// <summary>
    /// <see cref="AuthBannerText"/> for an account whose sign-in belongs to
    /// GNOME Online Accounts (sync.go <c>goaAuthBannerText</c>). Such
    /// accounts do not exist on Windows; kept for parity of the text table.
    /// </summary>
    public static string GoaAuthBannerText(ErrorCode reason, string account)
    {
        if (reason == ErrorCode.Unavailable)
        {
            // TRANSLATORS: %s is an account name.
            return L10n.T("GNOME Online Accounts is not available; %s cannot sign in", account);
        }
        // TRANSLATORS: %s is an account name.
        return L10n.T("Sign in to %s again in Settings → Online Accounts", account);
    }

    /// <summary>
    /// <see cref="AuthBannerText"/> for an account of the daemon's own
    /// sign-in (sync.go <c>oauthAuthBannerText</c>): whatever the provider
    /// refused, signing in again in the browser is the repair, unless the
    /// keyring that keeps the sign-in is what failed.
    /// </summary>
    public static string OAuthAuthBannerText(ErrorCode reason, string account)
    {
        if (reason == ErrorCode.KeyringError)
        {
            return AuthBannerText(reason, account);
        }
        // TRANSLATORS: %s is an account name.
        return L10n.T("Sign in to %s again in your browser", account);
    }

    /// <summary>
    /// The short status of an account whose server's certificate was refused
    /// (sync.go <c>certStatusText</c>; Preferences → Accounts, the status
    /// line).
    /// </summary>
    public static string CertStatusText(CertTrust.Category c)
    {
        if (c == CertTrust.Category.Changed)
        {
            // TRANSLATORS: account status (sidebar, Settings → Accounts)
            return L10n.T("Certificate changed");
        }
        // TRANSLATORS: account status (sidebar, Settings → Accounts)
        return L10n.T("Certificate problem");
    }

    /// <summary>
    /// The first enabled account, in account order, whose state is a
    /// certificate problem (sync.go <c>certProblemAccount</c>,
    /// <see cref="CertTrust.FromSyncState"/>); an account missing from
    /// <paramref name="states"/> uses the state of its <c>account.list</c>
    /// entry, as the status line does.
    /// </summary>
    public static (Account Account, CertTrust.Problem Problem)? CertProblemAccount(
        IReadOnlyDictionary<AccountId, SyncState> states, IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(states);
        ArgumentNullException.ThrowIfNull(accounts);
        foreach (var a in accounts)
        {
            if (a.Enabled && CertTrust.FromSyncState(states.GetValueOrDefault(a.Id) ?? a.State) is { } p)
            {
                return (a, p);
            }
        }
        return null;
    }

    /// <summary>
    /// The certificate banner's sentence (sync.go <c>certBannerText</c>):
    /// changed when the account pins another certificate, not trusted
    /// otherwise; <paramref name="account"/> is the account's display name.
    /// </summary>
    public static string CertBannerText(CertTrust.Category c, string account)
    {
        if (c == CertTrust.Category.Changed)
        {
            // TRANSLATORS: banner; %s is an account name
            return L10n.T("The certificate of %s has changed", account);
        }
        // TRANSLATORS: banner; %s is an account name
        return L10n.T("The certificate of %s is not trusted", account);
    }
}
