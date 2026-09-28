// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift (the
// test and the save: runTest, showResults, buttons(for:), save); GTK:
// ui/internal/accountwizard/wizard.go (runTest, showResults, onAdd) and
// trust.go (offerFor).

using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using OAuthTexts = Malachi.Core.Wizard.OAuth;
using TestOutcome = Malachi.Core.Wizard.Outcome;

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    // Calls account.test with the current settings (wizard.go runTest).
    private void RunTest()
    {
        TestingChanged?.Invoke(this, new TestingView.Progress(L10n.T("Testing Connection…")));
        trustOffers.Clear();
        var parameters = new AccountTestParams { Config = AssembleConfig(), Credentials = Credentials() };
        if (Editing is { } editing)
        {
            parameters = parameters with { AccountId = editing.Id }; // an empty password means "use the stored one"
        }
        var op = ++Op;
        scope.Perform(Client, API.AccountTest, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            ShowResults(outcome, parameters.Config);
        });
    }

    private void ShowResults(Outcome<AccountTestResult> outcome, AccountConfig tested)
    {
        // A Graph account has one endpoint, the mailbox; a Google account is
        // tested like any IMAP one, only without a password to correct.
        var linked = LinkedCfg is not null;
        var browser = SignsInWithBrowser;
        var graph = linked && LinkedCfg!.ProtocolKind == AccountKind.Graph;
        var view = new ResultsView { Icon = "dialog-warning-symbolic", Title = L10n.T("Connection Failed"), Buttons = WizardButtons.None };
        TestOutcome result;
        // A password account with no password stored and none typed.
        var missingPassword = false;
        if (!outcome.TryGetValue(out var res, out var error))
        {
            var row = new EndpointRow("dialog-warning-symbolic", RpcErrorText.Text(L10n.T("Testing the connection"), error));
            view = graph ? view with { Graph = row } : view with { Imap = row, Smtp = row };
            result = TestOutcome.Failed;
            if (Results.PasswordMissing(error, linked))
            {
                // No password stored and none typed: ask for it like for a
                // refused one, and offer no "Save Anyway" without it.
                result = TestOutcome.AuthFailed;
                missingPassword = true;
            }
        }
        else if (graph)
        {
            var (icon, text) = Results.EndpointSummary(res.Graph);
            view = view with { Graph = new EndpointRow(icon, text) };
            result = Results.Classify(res);
        }
        else
        {
            // A certificate the user may trust (trust.go offerFor) puts "Trust
            // Certificate…" into its row; never for an account the daemon
            // built.
            trustOffers.Clear();
            if (!linked)
            {
                if (tested.Imap is { } imap && CertTrust.Trustable(res.Imap, imap) is { } imapProblem)
                {
                    trustOffers[Endpoint.Imap] = (imap, imapProblem);
                }
                if (tested.Smtp is { } smtp && CertTrust.Trustable(res.Smtp, smtp) is { } smtpProblem)
                {
                    trustOffers[Endpoint.Smtp] = (smtp, smtpProblem);
                }
            }
            var (imapIcon, imapText) = Results.EndpointSummary(res.Imap);
            var (smtpIcon, smtpText) = Results.EndpointSummary(res.Smtp);
            view = view with
            {
                Imap = new EndpointRow(imapIcon, imapText, trustOffers.ContainsKey(Endpoint.Imap)),
                Smtp = new EndpointRow(smtpIcon, smtpText, trustOffers.ContainsKey(Endpoint.Smtp)),
            };
            result = Results.Classify(res);
        }
        SetLastSignInProblem(false);
        if (browser)
        {
            // The sign-in, or the permissions granted in the browser, are the
            // problem: offered again instead of a password.
            if (OAuthTexts.IsSignInProblem(outcome.Value, outcome.Error))
            {
                result = TestOutcome.Failed;
                SetLastSignInProblem(true);
                view = view with { Description = L10n.T("The server refused the sign-in. Sign in again and make sure access to mail is allowed.") };
            }
        }
        else if (linked && result == TestOutcome.AuthFailed)
        {
            // There is no password to correct here: the sign-in, or the
            // permissions it was granted, live in GNOME Online Accounts.
            result = TestOutcome.Failed;
            view = view with
            {
                Description = L10n.T("The server refused the sign-in. Sign in to the account again in Settings → Online Accounts and make sure access to mail is allowed."),
            };
        }
        LastOutcome = result;

        switch (result)
        {
            case TestOutcome.Ok:
                view = view with { Icon = "emblem-ok-symbolic", Title = IsEditing ? L10n.T("Ready to Save") : L10n.T("Ready to Add") };
                break;
            case TestOutcome.AuthFailed:
                PopTo(WizardPage.Identity);
                AskPassword(missingPassword ? ErrorCode.AuthRequired : ErrorCode.AuthFailed);
                break;
        }
        view = view with { Buttons = ButtonsFor(result) };
        lastResults = view;
        TestingChanged?.Invoke(this, new TestingView.Results(view));
    }

    // The edit button is Edit Servers for a password account and Sign In
    // Again for a browser sign-in with a sign-in problem; a GNOME Online
    // Accounts account has neither.
    private WizardButtons ButtonsFor(TestOutcome o)
    {
        var edit = LinkedCfg is null || (SignsInWithBrowser && lastSignInProblem);
        return new WizardButtons(Edit: edit, Retry: o == TestOutcome.Failed, AddAnyway: o == TestOutcome.Failed, Add: o == TestOutcome.Ok);
    }

    // Stores the account (account.add, or account.update when editing) and
    // reports Done on success (wizard.go onAdd).
    private void Save()
    {
        var cfg = AssembleConfig();
        var creds = Credentials();
        var editing = Editing;
        TestingChanged?.Invoke(this, new TestingView.Progress(ProgressTitle));
        var op = ++Op;
        scope.Perform(
            async ct =>
            {
                if (editing is not null)
                {
                    await Client.CallAsync(API.AccountUpdate, new AccountUpdateParams { AccountId = editing.Id, Config = cfg, Credentials = creds }, ct);
                    return editing.Id;
                }
                var res = await Client.CallAsync(API.AccountAdd, new AccountAddParams { Config = cfg, Credentials = creds }, ct);
                return res.AccountId;
            },
            outcome =>
            {
                if (op != Op)
                {
                    return;
                }
                if (!outcome.TryGetValue(out var id, out var error))
                {
                    if (lastResults is { } view)
                    {
                        lastResults = view with { Buttons = ButtonsFor(LastOutcome) };
                        TestingChanged?.Invoke(this, new TestingView.Results(lastResults));
                    }
                    ToastRequested?.Invoke(this, SaveErrorText(error, editing is not null));
                    return;
                }
                LogSaved(logger, id.Value, editing is not null);
                // The daemon consumed the sign-in; closing must not cancel it.
                OAuth = null;
                Done?.Invoke(this, (id, cfg));
            });
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "account saved: {AccountId}, edit {Editing}")]
    private static partial void LogSaved(ILogger logger, string accountId, bool editing);
}
