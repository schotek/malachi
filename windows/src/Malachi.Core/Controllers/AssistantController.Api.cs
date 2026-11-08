// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantController+API.swift;
// GTK: ui/internal/window/ai_provider.go (source, usesAPI, apiAccount,
// apiReady, apiProblem, rebuildAPI, apiProviderOrUnavailable, consent,
// refreshAIAccounts, setAIAccounts, providerChoices, triageSource,
// WaitAPISessions). The third provider of the assistant in the app
// (docs/plans/ai-api.md §7.4, D14): an AI provider account of the daemon
// (kind ai), over which the daemon runs the assistant itself
// (DaemonAssistantProvider). The assistant-provider setting names it by its
// account id; the controller keeps the daemon's ai accounts as last listed
// (account.list, again on notify.accountsChanged and on connecting:
// AccountsChanged) and builds one provider over the chosen one per runtime
// generation (RebuildApi). An id of an account the daemon no longer has
// reads as Claude (D14) once the list is known; while it is not, the
// account counts as the provider, unavailable, so that nothing runs Claude
// Code meanwhile.
//
// The account behaves like ChatGPT for the rest of the controller: no
// registration of the bridge in the Claude apps is needed (the daemon
// starts the bridge itself), the consent is the account's
// (AiConfig.Consent, one for the panel and the board, never a settings key),
// "not found" means the account is switched off, "not signed in" that the
// provider refused its key (the account's state authRequired). The
// application wires the rest: it gives the client (Attach), feeds
// ApiSessions from its notification dispatcher, calls AccountsChanged and
// ApiDisconnected with the connection, uses ApiProviderOrUnavailable as the
// in-app provider while UsesApi, and hears ProviderChanged.
//
// Windows: Swift's providerChanged is called by the application for a key
// change too; here the controller hears assistant-provider itself and only
// a change the account listing (or Attach) brought raises ProviderChanged,
// since the application already hears the key (AppState.InAppProviderChanged).
// ChatGPT's state for ProviderChoices is the one the application reports
// (SetInAppProviderAvailable). The availability of In App over an account is
// read from the account as listed (Swift's handlers table would keep an
// account's state change only after the next lookup).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.ApiAssistants;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantController
{
    /// <summary>
    /// How long <see cref="WaitApiSessionsAsync"/> waits at most (a
    /// sessionStop answers in milliseconds; a daemon that does not answer is
    /// not waited for; Swift <c>apiSessionStopWait</c>).
    /// </summary>
    public static readonly TimeSpan ApiSessionStopWait = TimeSpan.FromSeconds(2);

    private List<Account> aiAccounts = [];
    private int aiAccountsGen;

    /// <summary>
    /// The daemon's RPC client, for the AI provider accounts: account.list
    /// and the provider over the chosen account. Null until the application
    /// gives it (the constructor or <see cref="Attach"/>); nothing is listed
    /// before.
    /// </summary>
    public RpcClient? Client { get; private set; }

    /// <summary>
    /// The daemon's ai accounts as last listed, in the daemon's order (Swift
    /// <c>aiAccounts</c>).
    /// </summary>
    public IReadOnlyList<Account> AiAccounts => aiAccounts;

    /// <summary>Whether any listing answered (Swift <c>aiAccountsKnown</c>).</summary>
    public bool AiAccountsKnown { get; private set; }

    /// <summary>
    /// The provider over the chosen ai account for this runtime generation
    /// (<see cref="RebuildApi"/>); null while another source is chosen, the
    /// account is not listed, or there is no client.
    /// </summary>
    public DaemonAssistantProvider? ApiProvider { get; private set; }

    /// <summary>
    /// The sessions of the daemon's assistant, one registry per controller:
    /// the application feeds it the daemon's notifications
    /// (<see cref="AssistantSessions.Handle(DaemonNotification)"/>),
    /// <see cref="ApiDisconnected"/> ends them with the connection,
    /// <see cref="WaitApiSessionsAsync"/> lets their stops reach the daemon at
    /// quit.
    /// </summary>
    public AssistantSessions ApiSessions { get; }

    /// <summary>
    /// The runtime generation: one more every time the provider in effect
    /// changed (the <c>assistant-provider</c> key, the chosen account's
    /// configuration or switch, a client attached; Swift
    /// <c>providerGeneration</c>).
    /// </summary>
    public int ProviderGeneration { get; private set; }

    /// <summary>
    /// The provider in effect changed through the accounts (the chosen ai
    /// account appeared, went, was switched or reconfigured, its consent
    /// version moved) or a client attached: <see cref="ApiProvider"/> was
    /// built anew, so a request, a triage run or a suggested reply under way
    /// must end and the in-app provider be taken again, as on a change of
    /// the <c>assistant-provider</c> key (which the application hears from
    /// the settings, so it does not come here). Raised after
    /// <see cref="Changed"/>.
    /// </summary>
    public event EventHandler? ProviderChanged;

    // The source in effect

    /// <summary>
    /// Which provider the assistant runs on now (<see cref="Assistant.SourceOf"/>
    /// with the account rule: an account the daemon no longer lists reads as
    /// Claude once the list is known).
    /// </summary>
    public AssistantProviderSource Source
    {
        get
        {
            var src = Assistant.SourceOf(Settings.AssistantProvider);
            if (src == AssistantProviderSource.Account && AiAccountsKnown && ApiAccount is null)
            {
                return AssistantProviderSource.Claude;
            }
            return src;
        }
    }

    /// <summary>Whether an AI provider account is the provider in effect.</summary>
    public bool UsesApi => Source == AssistantProviderSource.Account;

    /// <summary>
    /// The account the <c>assistant-provider</c> key names, as last listed;
    /// null while there is none (another provider, or an account the daemon
    /// no longer has).
    /// </summary>
    public Account? ApiAccount
    {
        get
        {
            if (Assistant.AccountOf(Settings.AssistantProvider) is not { } id)
            {
                return null;
            }
            return aiAccounts.FirstOrDefault(a => a.Id == id);
        }
    }

    /// <summary>
    /// Whether the chosen account can run now: it exists, is switched on and
    /// the provider has not refused its key. Its consent is asked when a
    /// request runs, as Claude Code's.
    /// </summary>
    public bool ApiReady => ApiAccount is { Enabled: true } a && a.State.Status != SyncStatus.AuthRequired;

    /// <summary>
    /// Why the chosen account cannot run the message actions; null when it
    /// can (Go <c>apiProblem</c> returns ""). The words are the board's too.
    /// </summary>
    public string? ApiProblem
    {
        get
        {
            var x = Assistant.ApiText();
            if (DaemonBridge is null)
            {
                return Assistant.PanelTexts().ToolsMissing;
            }
            if (ApiAccount is not { } a)
            {
                return x.AccountMissing;
            }
            if (!a.Enabled)
            {
                return x.BoardAccountOff;
            }
            if (a.State.Status == SyncStatus.AuthRequired)
            {
                return x.BoardKeyRefused;
            }
            return null;
        }
    }

    /// <summary>
    /// The bridge the daemon starts for a session over an ai account (GTK
    /// <c>daemonBridge</c>): <c>malachi-mcp.exe</c> beside the application,
    /// the same as the panel's; null without one.
    /// </summary>
    public string? DaemonBridge { get; }

    // The provider over the account

    /// <summary>
    /// Gives the daemon's RPC client (the application's, once it exists):
    /// the provider over the chosen ai account is built anew and the accounts
    /// listed (<see cref="RefreshAiAccounts"/>). Over an account a new
    /// runtime generation (<see cref="ProviderChanged"/>).
    /// </summary>
    public void Attach(RpcClient client)
    {
        ArgumentNullException.ThrowIfNull(client);
        scope.VerifyAccess();
        if (Closed || ReferenceEquals(client, Client))
        {
            return;
        }
        Client = client;
        if (Assistant.IsAccountProvider(Settings.AssistantProvider))
        {
            RuntimeChanged(raise: true);
        }
        else
        {
            RebuildApi();
        }
        RefreshAiAccounts();
    }

    /// <summary>
    /// Makes the provider over the chosen account anew (a runtime
    /// generation): one <see cref="DaemonAssistantProvider"/> while an account
    /// is chosen, listed and the client exists, null otherwise.
    /// </summary>
    public void RebuildApi()
    {
        ApiProvider = null;
        if (Source != AssistantProviderSource.Account || ApiAccount is not { } a || Client is not { } client)
        {
            return;
        }
        // The daemon starts its own bridge, whatever bridge the panel or a
        // request names.
        ApiProvider = new DaemonAssistantProvider(client, a, ApiSessions, apiLogger) { BridgePath = DaemonBridge };
    }

    /// <summary>
    /// The provider over the chosen account, or one whose sessions fail with
    /// "api_unavailable" while there is none (no client yet, the account not
    /// listed yet), named after the account (<see cref="ApiBrand"/>).
    /// </summary>
    public IAssistantProvider ApiProviderOrUnavailable()
    {
        if (ApiProvider is { } p && Assistant.AccountOf(Settings.AssistantProvider) == p.AccountId)
        {
            return p;
        }
        // The key named another account since the provider was built (its
        // observer has not run yet).
        RebuildApi();
        return ApiProvider ?? (IAssistantProvider)new UnavailableApiProvider(ApiBrand);
    }

    /// <summary>
    /// Names an AI provider account as the Assistant Uses row does
    /// (<see cref="Assistant.ProviderChoices"/>): its name, else its provider
    /// kind.
    /// </summary>
    public static string AiAccountName(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var choices = Assistant.ProviderChoices(new ProviderChoiceState(), new ProviderChoiceState(), [a]);
        return choices.Count >= 3 ? choices[2].Name : Assistant.ApiText().AccountMissing;
    }

    /// <summary>
    /// The consent of the chosen account: the provider's (which counts an
    /// accepted one at once and writes it to the daemon), else the account's
    /// as listed.
    /// </summary>
    public bool ApiConsent => ApiProvider is { } p ? p.HasConsent : ApiAccount?.Config.Ai?.Consented == true;

    /// <summary>Records the user's consent on the chosen account (account.update through the provider).</summary>
    public void AcceptApiConsent() => ApiProvider?.AcceptConsent();

    /// <summary>
    /// The board's consent sentence over account <paramref name="a"/> (the
    /// board's heading is <see cref="Boards.Board.Text.TriageConsentHeading"/>).
    /// </summary>
    public static string ApiBoardConsentBody(Account a) => GettextFormat.Format(Assistant.ApiText().BoardConsentBody, [Assistant.Destination(a)]);

    /// <summary>The assistant panel's footer over account <paramref name="a"/>.</summary>
    public static string ApiFooter(Account a) => GettextFormat.Format(Assistant.ApiText().Footer, [Assistant.Destination(a)]);

    /// <summary>
    /// Names the provider in the compose window's assistant bar and the
    /// panel's subtitle while no provider exists yet: the chosen account's
    /// name, or that the account was removed.
    /// </summary>
    public string ApiBrand => ApiAccount is { } a ? AiAccountName(a) : Assistant.ApiText().AccountMissing;

    // The accounts

    /// <summary>
    /// The daemon's account set changed (notify.accountsChanged, a connection
    /// made): the AI provider accounts are listed again.
    /// </summary>
    public void AccountsChanged() => RefreshAiAccounts();

    /// <summary>
    /// Lists the accounts again and keeps the ai ones. Nothing before the
    /// client exists; a late answer of an older listing is dropped.
    /// </summary>
    public void RefreshAiAccounts()
    {
        scope.VerifyAccess();
        if (Closed || Client is not { } client)
        {
            return;
        }
        var gen = ++aiAccountsGen;
        scope.Perform(client, API.AccountList, new EmptyParams(), outcome =>
        {
            if (Closed || gen != aiAccountsGen)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogAccountListFailed(logger, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);
                return;
            }
            SetAiAccounts(res.Accounts);
        });
    }

    /// <summary>
    /// Keeps the ai accounts among <paramref name="accounts"/> and tells what
    /// changed: the chosen account's configuration or switch (a runtime
    /// generation: its provider is built anew and a request under way is
    /// discarded, as a model change of ChatGPT is; <see cref="ProviderChanged"/>),
    /// its state alone or the other accounts (<see cref="Changed"/> only:
    /// availability, the choice's entries).
    /// </summary>
    public void SetAiAccounts(IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        scope.VerifyAccess();
        if (Closed)
        {
            return;
        }
        var ai = accounts.Where(a => a.Config.ProtocolKind == AccountKind.Ai).ToList();
        var before = ApiAccount;
        var knownBefore = AiAccountsKnown;
        var wasApi = Source == AssistantProviderSource.Account;
        var same = AiAccountsKnown && ai.Count == aiAccounts.Count && ai.Zip(aiAccounts).All(p => SameAiAccount(p.First, p.Second));
        aiAccounts = ai;
        AiAccountsKnown = true;
        if (same)
        {
            return;
        }
        var after = ApiAccount;
        var configChanged = before is not null && after is not null && !SameAiAccountConfig(before, after);
        if ((!knownBefore && Assistant.IsAccountProvider(Settings.AssistantProvider))
            || (before is null) != (after is null)
            || wasApi != (Source == AssistantProviderSource.Account)
            || configChanged)
        {
            RuntimeChanged(raise: true);
        }
        else
        {
            Notify();
        }
    }

    /// <summary>
    /// Whether two listings of an account agree in what the controller looks
    /// at: the configuration, the switch and the state.
    /// </summary>
    public static bool SameAiAccount(Account x, Account y)
    {
        ArgumentNullException.ThrowIfNull(x);
        ArgumentNullException.ThrowIfNull(y);
        return SameAiAccountConfig(x, y) && x.State.Status == y.State.Status;
    }

    /// <summary>
    /// Whether two listings of an account agree in its id, name, switch and
    /// AI configuration (the consent by its version, not by when it was
    /// accepted).
    /// </summary>
    public static bool SameAiAccountConfig(Account x, Account y)
    {
        ArgumentNullException.ThrowIfNull(x);
        ArgumentNullException.ThrowIfNull(y);
        if (x.Id != y.Id || x.Enabled != y.Enabled || x.Config.Name != y.Config.Name)
        {
            return false;
        }
        switch (x.Config.Ai, y.Config.Ai)
        {
            case (null, null):
                return true;
            case (null, _) or (_, null):
                return false;
            case ({ } p, { } q):
                if ((p.Consent is null) != (q.Consent is null) || p.Consent?.Version != q.Consent?.Version)
                {
                    return false;
                }
                return p with { Consent = null } == q with { Consent = null };
        }
    }

    // The sessions

    /// <summary>
    /// Lets the sessionStop calls of the daemon's assistant reach the daemon
    /// as the application quits: called after the panel, the requests and
    /// the board ended their sessions and before the connection closes;
    /// waits at most <see cref="ApiSessionStopWait"/>. True when every stop
    /// answered.
    /// </summary>
    public Task<bool> WaitApiSessionsAsync() => ApiSessions.WaitAsync(ApiSessionStopWait);

    /// <summary>The connection to the daemon went: every session of the daemon's assistant with it.</summary>
    public void ApiDisconnected() => ApiSessions.Disconnected();

    // The choice and the board

    /// <summary>
    /// The entries of the Assistant Uses row: Claude Code, ChatGPT and every
    /// AI provider account, each with why it cannot be chosen
    /// (<see cref="Assistant.ProviderChoices"/>). ChatGPT's state is the one
    /// the application reported (<see cref="SetInAppProviderAvailable"/>).
    /// </summary>
    public IReadOnlyList<ProviderChoice> ProviderChoices()
    {
        var claude = new ProviderChoiceState(true);
        if (bridge is null || Locator?.Locate() is null)
        {
            claude = new ProviderChoiceState(false, bridge is null
                ? Assistant.PanelTexts().ToolsMissing
                : Assistant.Problem(AssistantTarget.App, new AssistantAvailability(false, false)));
        }
        var chatGpt = new ProviderChoiceState(inAppProviderAvailable);
        if (!inAppProviderAvailable)
        {
            chatGpt = chatGpt with
            {
                Problem = inAppProviderProblem.Length == 0 || inAppProviderProblem.StartsWith("chatgpt_", StringComparison.Ordinal)
                    ? L10n.T("Reconnect to ChatGPT") : inAppProviderProblem,
            };
        }
        return Assistant.ProviderChoices(claude, chatGpt, aiAccounts);
    }

    /// <summary>
    /// The board run's source as the daemon records it
    /// (<see cref="BoardTriageController.Source"/>): Claude Code's, ChatGPT's,
    /// or "api:&lt;provider&gt;" of the account.
    /// </summary>
    public string TriageSource => Source switch
    {
        AssistantProviderSource.ChatGpt => BoardTriageController.ChatGptSource,
        AssistantProviderSource.Account => BoardTriageController.ApiSource(ApiAccount?.Config.Ai?.Provider ?? AiProvider.OpenAiCompatible),
        _ => Assistant.TriageSource,
    };

    // The assistant-provider key changed: the provider over the account is
    // built anew, the handlers looked up again, the observers told; the
    // application hears the key itself, so no ProviderChanged.
    private void ProviderKeyChanged() => RuntimeChanged(raise: false);

    // A new runtime generation (Swift providerChanged).
    private void RuntimeChanged(bool raise)
    {
        if (Closed)
        {
            return;
        }
        ProviderGeneration++;
        RebuildApi();
        RefreshHandlers();
        Notify();
        if (raise && ProviderChanged is { } changed)
        {
            foreach (var handler in changed.GetInvocationList())
            {
                scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
            }
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "account.list for the assistant failed, code {Code}")]
    private static partial void LogAccountListFailed(ILogger logger, int code);
}
