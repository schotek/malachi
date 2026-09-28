// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift (the
// browser sign-in: promptView, showOAuthView, showOAuthPrompt,
// signInWithProvider, started, waitOAuth, oauthCompleted, oauthFailed,
// launch, showOAuthUnavailable, reopenBrowser, cancelOAuth, useAppPassword,
// continueWithAppPassword, cancelSession, cancelInBackground); GTK:
// ui/internal/accountwizard/oauth.go (showOAuthPrompt, onOAuthSignIn,
// waitOAuth, oauthComplete, oauthFailed, onOAuthCancel, cancelSession,
// launch, showOAuthUnavailable, onOAuthPassword, useAppPassword).
//
// account.oauthWait answers "pending" after at most a minute and is asked
// again (75 s per call, the descriptor's RpcTimeouts.OAuthWaitCall) while
// the wizard is open and nothing else started. account.oauthStart is not
// sent once the wizard closed (docs/windows-port.md §7.2); once sent, its
// answer is awaited even after a close, so that a session nobody waits for
// any more is cancelled, as Swift cancels it. account.oauthCancel is fire
// and forget and goes out even while the wizard closes (PerformPastClose).
// The provider's page is opened by the injected ILauncher (Swift's
// onOpenURL); a launch that fails says why in a toast, as GTK's launch does.

using System;
using System.Threading;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using LinkedRules = Malachi.Core.Wizard.Linked;
using OAuthTexts = Malachi.Core.Wizard.OAuth;

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>
    /// The prompt's "Sign In with …" (oauth.go <c>onOAuthSignIn</c>): opens a
    /// session, hands its page to the browser and waits for it. An existing
    /// account is signed in again by its id. Only the button waits for the
    /// answer; Back stays possible and drops it.
    /// </summary>
    public void SignInWithProvider()
    {
        scope.VerifyAccess();
        if (OAuth is not { } st || OAuthStarting)
        {
            return;
        }
        CancelSession();
        var parameters = new AccountOAuthStartParams { BrowserPage = OAuthTexts.BrowserPage() };
        if (Editing is { } editing)
        {
            parameters = parameters with { AccountId = editing.Id };
        }
        else if (st.Config is { } cfg)
        {
            parameters = parameters with { Config = LinkedRules.WithIdentity(cfg, Identity) };
        }
        else
        {
            return;
        }
        SetOAuthStarting(true);
        var op = ++Op;
        scope.Perform(
            async lifetime =>
            {
                // Closed before it went out: no session to let go.
                lifetime.ThrowIfCancellationRequested();
                var res = await Client.CallAsync(API.AccountOAuthStart, parameters, CancellationToken.None);
                if (IsClosed)
                {
                    // Nobody waits for this session any more: the wizard closed.
                    CancelInBackground(res.SessionId);
                }
                return res;
            },
            outcome =>
            {
                if (op != Op)
                {
                    // The wizard moved on: the session is let go.
                    if (outcome.IsSuccess)
                    {
                        CancelInBackground(outcome.Value!.SessionId);
                    }
                    return;
                }
                SetOAuthStarting(false);
                if (OAuth is null || Pages[^1] != WizardPage.OAuth)
                {
                    // Back left the sign-in while it started: its answer,
                    // success or failure, is dropped (oauth.go oauthShown).
                    if (outcome.IsSuccess)
                    {
                        CancelInBackground(outcome.Value!.SessionId);
                    }
                    return;
                }
                Started(outcome);
            });
    }

    /// <summary>The waiting page's "Open the Browser Again".</summary>
    public void ReopenBrowser()
    {
        scope.VerifyAccess();
        if (CurrentOAuthView is not OAuthView.Waiting || OAuth?.AuthUrl is not { } url)
        {
            return;
        }
        Launch(url);
    }

    /// <summary>
    /// The waiting page's Cancel (oauth.go <c>onOAuthCancel</c>): the session
    /// is cancelled and the prompt comes back, without a toast.
    /// </summary>
    public void CancelOAuth()
    {
        scope.VerifyAccess();
        if (CurrentOAuthView is not OAuthView.Waiting)
        {
            return;
        }
        Op++;
        CancelSession();
        ShowOAuthView(PromptView(OAuth?.Name ?? ""));
    }

    /// <summary>
    /// "Use an App Password Instead" (oauth.go <c>onOAuthPassword</c>): the
    /// IMAP/SMTP account of the alternative with the identity's password,
    /// tested at once when one is typed; otherwise back to the identity for
    /// it.
    /// </summary>
    public void UseAppPassword()
    {
        scope.VerifyAccess();
        if (OAuth?.PasswordAlt is not { } alt)
        {
            return;
        }
        CancelSession();
        appPassword = alt;
        OAuth = null;
        LinkedCfg = null;
        ContinueWithAppPassword();
    }

    private static OAuthView.Prompt PromptView(string name) =>
        new OAuthView.Prompt(OAuthTexts.OAuthPromptText(name), WithoutMnemonic(OAuthTexts.OAuthSignInLabel(name)));

    // Switches the sign-in page; its Sign In button is usable again
    // (oauth.go showOAuthStack).
    private void ShowOAuthView(OAuthView view)
    {
        CurrentOAuthView = view;
        SetOAuthStarting(false);
        OAuthViewChanged?.Invoke(this, view);
    }

    // Opens the browser sign-in's prompt (oauth.go showOAuthPrompt): config
    // is the new account to sign in, null when editing (the account's id is
    // signed in again); passwordAlt the app-password account the page may
    // offer instead. A sign-in of before is dropped.
    private void ShowOAuthPrompt(LinkedProvider? provider, AccountConfig? config, AccountConfig? passwordAlt)
    {
        CancelSession();
        var name = OAuthTexts.OAuthProviderLabel(provider, config?.Email ?? Identity.Email);
        OAuth = new OAuthState { Provider = provider, Name = name, Config = config, PasswordAlt = passwordAlt };
        ShowOAuthView(PromptView(name));
        Replace(SignInMode ? [WizardPage.OAuth] : [WizardPage.Identity, WizardPage.OAuth]);
    }

    private void Started(Outcome<AccountOAuthStartResult> outcome)
    {
        if (!outcome.TryGetValue(out var res, out var error))
        {
            LogCallFailure(LogLevel.Debug, API.AccountOAuthStart.Name, error);
            if (SignIn.IsClientMissing(error))
            {
                ShowOAuthUnavailable();
                return;
            }
            ToastRequested?.Invoke(this, RpcErrorText.Text(L10n.T("Starting the sign-in"), error));
            return;
        }
        if (!OAuthTexts.IsBrowserUrl(res.AuthUrl))
        {
            // Only an https address from the daemon is opened: the session is
            // let go and the prompt stays, as nothing could come back from the
            // browser.
            LogRefusedAddress(logger);
            CancelInBackground(res.SessionId);
            ToastRequested?.Invoke(this, OAuthTexts.RefusedBrowserUrlText());
            return;
        }
        OAuth = OAuth! with { SessionId = res.SessionId, AuthUrl = res.AuthUrl, Complete = false };
        ShowOAuthView(new OAuthView.Waiting());
        Launch(res.AuthUrl);
        WaitOAuth(res.SessionId);
    }

    // Calls account.oauthWait until the session ends (oauth.go waitOAuth):
    // the daemon answers pending after at most a minute, and the next call
    // goes out only while the wizard is open and nothing else started.
    private void WaitOAuth(string sessionId) => PollOAuth(sessionId, ++Op);

    private void PollOAuth(string sessionId, int op) =>
        scope.Perform(Client, API.AccountOAuthWait, new AccountOAuthWaitParams { SessionId = sessionId }, outcome =>
        {
            if (op != Op || OAuth?.SessionId != sessionId)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogCallFailure(LogLevel.Information, API.AccountOAuthWait.Name, error);
                OAuthFailed(OAuthTexts.OAuthErrorText(OAuth?.Name ?? "", error));
                return;
            }
            if (res.Status == OAuthSessionStatus.Pending)
            {
                PollOAuth(sessionId, op);
                return;
            }
            if (res.Status == OAuthSessionStatus.Complete && res.Config is { } cfg)
            {
                OAuthCompleted(cfg);
                return;
            }
            LogUnexpectedStatus(logger, res.Status.Value);
            OAuthFailed(RpcErrorText.Text(L10n.T("Signing in"), (Exception?)null));
        });

    // The browser came back signed in: the account is the daemon's, as for
    // GNOME Online Accounts (StartLinked), and its connection is tested with
    // the session.
    private void OAuthCompleted(AccountConfig config)
    {
        LogSignedIn(logger);
        LinkedCfg = LinkedRules.WithIdentity(config, Identity);
        appPassword = null;
        if (OAuth is { } st)
        {
            OAuth = st with { Complete = true };
        }
        Identity = Identity with { Password = "" };
        IdentityChanged?.Invoke(this, Identity);
        // Back from the test leads to a prompt, ready to sign in again.
        ShowOAuthView(PromptView(OAuth?.Name ?? ""));
        Replace(SignInMode ? [WizardPage.OAuth, WizardPage.Testing] : [WizardPage.Identity, WizardPage.OAuth, WizardPage.Testing]);
        RunTest();
    }

    // The session ended without a sign-in (oauth.go oauthFailed): a session
    // the daemon may still hold is cancelled, the prompt comes back and the
    // toast says why.
    private void OAuthFailed(string text)
    {
        CancelSession();
        ShowOAuthView(PromptView(OAuth?.Name ?? ""));
        ToastRequested?.Invoke(this, text);
    }

    // Hands the provider's page to the browser (oauth.go launch): only an
    // https address from the daemon (Started refused any other; this guards
    // "Open the Browser Again" too).
    private void Launch(string url)
    {
        if (!OAuthTexts.IsBrowserUrl(url))
        {
            LogRefusedAddress(logger);
            ToastRequested?.Invoke(this, OAuthTexts.RefusedBrowserUrlText());
            return;
        }
        var owner = Owner;
        scope.Perform(
            ct => launcher.OpenUrlAsync(url, owner, ct),
            outcome =>
            {
                if (outcome.Error is not { } error)
                {
                    return;
                }
                // The address stays out of the log: it carries the session's state.
                LogLaunchFailed(logger, error.GetType().Name);
                var detail = error is ArgumentException ? "not an https address" : error.Message;
                // TRANSLATORS: %s is a technical error message.
                ToastRequested?.Invoke(this, L10n.T("The link could not be opened: %s", detail));
            });
    }

    // No OAuth client for the provider (oauth.go showOAuthUnavailable).
    private void ShowOAuthUnavailable() =>
        ShowOAuthView(new OAuthView.Unavailable(OAuthTexts.OAuthUnavailableText(OAuth?.Name ?? ""), OAuth?.PasswordAlt is not null));

    // Continues with the chosen app-password account (oauth.go
    // useAppPassword): to the connection test when a password is typed,
    // else back to the identity page for one (Next then continues here).
    private void ContinueWithAppPassword()
    {
        if (appPassword is not { } alt)
        {
            return;
        }
        var id = Identity with { Email = Fields.ValidateEmail(Identity.Email) ?? Identity.Email };
        ApplyConfig(Fields.MergeIdentity(alt, id));
        if (id.Password.Length == 0)
        {
            Replace([WizardPage.Identity]);
            _ = RequirePassword(L10n.T("Enter the app password for this account"));
            return;
        }
        Replace([WizardPage.Identity, WizardPage.Servers, WizardPage.Testing]);
        RunTest();
    }

    // Drops the session of the browser sign-in, if any (oauth.go
    // cancelSession): account.oauthCancel, fire and forget.
    private void CancelSession()
    {
        if (OAuth is not { SessionId: { } session } st)
        {
            return;
        }
        OAuth = st with { SessionId = null, AuthUrl = null, Complete = false };
        CancelInBackground(session);
    }

    // account.oauthCancel for a session, sent even when the wizard closes
    // right after; its failure is only logged.
    private void CancelInBackground(string session)
    {
        if (session.Length == 0)
        {
            return;
        }
        scope.PerformPastClose(async () =>
        {
            try
            {
                return await Client.CallAsync(API.AccountOAuthCancel, new AccountOAuthCancelParams { SessionId = session }, CancellationToken.None);
            }
            catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException)
            {
                LogCallFailure(LogLevel.Debug, API.AccountOAuthCancel.Name, e);
                return new EmptyResult();
            }
        });
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "sign-in address refused: not https")]
    private static partial void LogRefusedAddress(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "account.oauthWait: unexpected status {Status}")]
    private static partial void LogUnexpectedStatus(ILogger logger, string? status);

    [LoggerMessage(Level = LogLevel.Information, Message = "browser sign-in complete")]
    private static partial void LogSignedIn(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "open sign-in page failed: {Failure}")]
    private static partial void LogLaunchFailed(ILogger logger, string failure);
}
