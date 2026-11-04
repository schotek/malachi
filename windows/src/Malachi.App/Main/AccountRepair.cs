// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration.swift (authBannerButton,
// signInAgain, signIn, certBannerButton, editAccount, statusAction); GTK:
// ui/internal/window/sync.go (onAuthBannerButton, signInAgain,
// signInInBrowser, onCertBannerButton, editAccount) and status.go
// (onStatusAction). What the main window's banners and the status flyout's
// rows do for an account that needs attention: sign it in again the way it
// signs in (the account's edit wizard asking for a missing or refused
// password, the browser for the daemon's own sign-in, the preferences
// otherwise, GNOME Online Accounts having no panel on Windows), edit it for
// a certificate, or check it for new mail. The wizard is opened through the
// application's EditAccount hook (wave 2, E6); the browser only gets an
// https address, and a failure is a toast over the main window.

using System;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Transport;

namespace Malachi.App.Main;

/// <summary>The banners' and the status flyout's account actions.</summary>
internal sealed class AccountRepair
{
    private readonly AppState state;
    private readonly MailboxController mailbox;
    private readonly SyncController sync;
    private readonly RpcClient client;
    private readonly Action<string> toast;

    /// <param name="state">The hooks (EditAccount, OpenPreferences) and the browser.</param>
    /// <param name="mailbox">The accounts and the sync trigger.</param>
    /// <param name="sync">The banners' state and the sign-in session.</param>
    /// <param name="toast">The main window's toast.</param>
    public AccountRepair(AppState state, MailboxController mailbox, SyncController sync, Action<string> toast)
    {
        this.state = state;
        this.mailbox = mailbox;
        this.sync = sync;
        client = state.Client;
        this.toast = toast;
    }

    /// <summary>
    /// The sign-in banner's button (sync.go onAuthBannerButton): signs the
    /// banner's account in again for the notification's reason; the
    /// preferences for an account the banner has no route for.
    /// </summary>
    public void AuthBannerButton()
    {
        switch (sync.AuthBannerAction)
        {
            case AuthBannerAction.EditAccount edit:
                SignInAgain(SignInKind.Password, edit.Reason, edit.Account);
                break;
            case AuthBannerAction.SignInAgain again:
                SignInAgain(SignInKind.OAuth, default, again.Account);
                break;
            default:
                state.Hooks.OpenPreferences?.Invoke();
                break;
        }
    }

    /// <summary>The certificate banner's Edit Account… (sync.go onCertBannerButton).</summary>
    public void CertBannerButton()
    {
        if (sync.CertBannerAccount is { } id)
        {
            EditAccount(id, null);
        }
    }

    /// <summary>
    /// The action of an account's row in the status flyout (status.go
    /// onStatusAction): check or try again, sign in as the banner does, or
    /// the account's settings.
    /// </summary>
    public void StatusAction(AccountStatus st)
    {
        ArgumentNullException.ThrowIfNull(st);
        switch (st.Action)
        {
            case Core.Model.StatusAction.Check or Core.Model.StatusAction.Retry:
                mailbox.TriggerSync(st.Account);
                break;
            case Core.Model.StatusAction.SignIn:
                SignInAgain(st.SignIn, st.Reason, st.Account);
                break;
            case Core.Model.StatusAction.Edit:
                EditAccount(st.Account, null);
                break;
        }
    }

    // sync.go signInAgain: the edit wizard asking for the password when it
    // is missing or refused (editsPassword), the browser for the daemon's
    // own sign-in (the banner's page as the fallback when it is up for this
    // account), the preferences otherwise.
    private void SignInAgain(SignInKind kind, ErrorCode reason, AccountId id)
    {
        if (SyncStatusTexts.EditsPassword(kind, reason) && mailbox.Model.Account(id) is not null)
        {
            EditAccount(id, reason);
            return;
        }
        if (kind != SignInKind.OAuth)
        {
            state.Hooks.OpenPreferences?.Invoke();
            return;
        }
        string? fallback = null;
        if (sync.AuthBannerAction is AuthBannerAction.SignInAgain banner && banner.Account == id)
        {
            fallback = banner.FallbackUrl;
        }
        _ = SignInAsync(id, fallback);
    }

    // sync.go signInInBrowser: a fresh session's page, or the notification's.
    private async System.Threading.Tasks.Task SignInAsync(AccountId id, string? fallback)
    {
        switch (await sync.RequestSignInUrlAsync(client, id, fallback))
        {
            case SignInUrl.Open open:
                var owner = state.MainWindow is { } w ? WindowPresenter.Handle(w) : 0;
                try
                {
                    await state.Launcher.OpenUrlAsync(open.Url, owner);
                }
                catch (Exception e) when (e is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
                {
                    var detail = e is ArgumentException ? "not an https address" : e.Message; // Windows-only string (the technical detail, as in GTK)
                    // TRANSLATORS: %s is a technical error message.
                    toast(L10n.T("The link could not be opened: %s", detail));
                }
                break;
            case SignInUrl.Failed failed:
                toast(failed.Text);
                break;
        }
    }

    // sync.go editAccount: the wizard in edit mode for the account, asking
    // for the password with a reason.
    private void EditAccount(AccountId id, ErrorCode? requestPassword)
    {
        if (mailbox.Model.Account(id) is null)
        {
            return;
        }
        // Over the main window (null: the wizard picks it), as the banners and the status flyout live there.
        state.Hooks.EditAccount?.Invoke(null, id, requestPassword);
    }
}
