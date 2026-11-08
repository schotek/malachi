// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/APIAssistant/DaemonAssistantProvider.swift;
// GTK: ui/internal/apiassistant/provider.go (Provider, New, Name, Model,
// HasConsent, AcceptConsent, awaitConsent, Open, failure, reasonOf,
// callTimeout, startTimeout). The assistant the daemon runs itself over an
// ai account (docs/api.md §4.18, docs/plans/ai-api.md D7, E3) as a provider
// of the panel's protocol (IAssistantProvider): OpenAsync is
// assistant.sessionStart, a session's SubmitAsync assistant.sessionSend,
// Terminate assistant.sessionStop, and every notify.assistantEvent of the
// session, routed to it by AssistantSessions, is turned into the
// AssistantEvents the panel, the one-shot requests, the board's triage and
// the suggested reply already read (what CodexAssistantSession emits for
// Codex). The daemon builds the prompts, calls the model and runs the bridge;
// nothing here sees a key or a model's text beyond passing the events on.
//
// Windows: Swift's ChatGPTFailure is AssistantProviderException, whose code is
// the reason AssistantRequest.ProviderFailure reads. Every failure of a call
// becomes one (the daemon's refusals by their code, any other by its
// message), because the requests catch that type, not the client's;
// a cancellation by the caller stays an OperationCanceledException. The
// account has one consent (docs/api.md §4.18), so the board's consent is the
// account's. A refused consent write stays until a start read it (Go and
// Swift forget it once it ended, so a start that came after the refusal did
// not see it). The provider may be used from any thread; its sessions
// deliver their events on the synchronization context OpenAsync ran on.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.ApiAssistants;

/// <summary>
/// The daemon's assistant over one ai account (apiassistant.Provider). One
/// per account and application; the application makes it from
/// <c>account.list</c> and replaces it when the account changes (its name,
/// model or consent).
/// </summary>
public sealed partial class DaemonAssistantProvider : IAssistantProvider
{
    /// <summary>
    /// apiassistant.callTimeout: one call to the daemon other than a
    /// session's start (the start waits for the bridge:
    /// <see cref="Platform.RpcTimeouts.AssistantSessionStart"/>).
    /// </summary>
    internal static readonly TimeSpan CallTimeout = TimeSpan.FromSeconds(15);

    /// <summary>The reason of a start without the account's consent.</summary>
    public const string ReasonConsentRequired = "api_consent_required";

    /// <summary>The reason of a start the daemon answered without a session.</summary>
    public const string ReasonProtocol = "api_protocol";

    /// <summary>The reason of a session that ended (stopped, gone, the connection closed).</summary>
    public const string ReasonStopped = "api_stopped";

    private readonly Lock gate = new();
    private Account account;
    // The account.update of AcceptConsent on its way, or refused and not yet
    // read (null when none): OpenAsync waits for it before it starts a
    // session, so the daemon sees the consent, and fails with a refusal
    // once.
    private Task? consentWrite;

    /// <summary>
    /// apiassistant.New: the provider of <paramref name="account"/> (kind ai)
    /// over <paramref name="client"/>; its sessions register with
    /// <paramref name="sessions"/>, which the application feeds with the
    /// daemon's notifications (null registers nothing).
    /// </summary>
    /// <param name="client">The daemon.</param>
    /// <param name="account">The ai account, as account.list gave it.</param>
    /// <param name="sessions">The registry of the application's sessions.</param>
    /// <param name="logger">Receives error codes, never a key or mail data.</param>
    public DaemonAssistantProvider(RpcClient client, Account account, AssistantSessions? sessions, ILogger<DaemonAssistantProvider>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(account);
        Client = client;
        this.account = account;
        Sessions = sessions;
        Logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The registry the sessions register with; null registers nothing.</summary>
    public AssistantSessions? Sessions { get; }

    /// <summary>
    /// The bridge the daemon may start, given to <c>assistant.sessionStart</c>
    /// instead of the spec's <see cref="AssistantSessionSpec.BridgePath"/>;
    /// null passes the spec's (Swift <c>bridgePath</c>).
    /// </summary>
    public string? BridgePath { get; set; }

    internal ILogger Logger { get; }

    /// <summary>The account as the provider has it (the consent as last accepted here).</summary>
    public Account Account
    {
        get
        {
            lock (gate)
            {
                return account;
            }
        }
    }

    /// <summary>The account's id.</summary>
    public AccountId AccountId => Account.Id;

    /// <summary>
    /// apiassistant.Provider.Name: the account's name for the panel's
    /// subtitle (its address, or its id, when it has none).
    /// </summary>
    public string Name
    {
        get
        {
            var a = Account;
            if (a.Config.Name.Length > 0)
            {
                return a.Config.Name;
            }
            return a.Config.Email.Length > 0 ? a.Config.Email : a.Id.Value;
        }
    }

    /// <summary>apiassistant.Provider.Model: the account's model, "" without an AiConfig.</summary>
    public string Model => Account.Config.Ai?.Model ?? "";

    /// <summary>The account is switched on (Swift <c>available</c>; Go has no such gate).</summary>
    public bool Available => Account.Enabled;

    /// <summary>The provider did not refuse the account's key (its state is not authRequired).</summary>
    public bool Connected => Account.State.Status != SyncStatus.AuthRequired;

    /// <summary>
    /// apiassistant.Provider.HasConsent: the user accepted the current
    /// consent text for the account (<see cref="AiConfig.Consented"/>, or
    /// <see cref="AcceptConsent"/> here).
    /// </summary>
    public bool HasConsent => Account.Config.Ai is { Consented: true };

    /// <summary>The account's one consent covers the board too (docs/api.md §4.18).</summary>
    public bool HasBoardConsent => HasConsent;

    /// <summary>
    /// apiassistant.Provider.AcceptConsent: records the user's consent:
    /// <c>account.update</c> with the config as stored and the consent's
    /// version (the daemon sets acceptedAt; no credentials, so the key
    /// stays). The write goes in the background; the provider counts the
    /// consent as given at once, and <see cref="OpenAsync"/> waits for the
    /// write (a refused one fails the session with its reason).
    /// </summary>
    public void AcceptConsent()
    {
        AccountUpdateParams parameters;
        lock (gate)
        {
            if (account.Config.Ai is not { } ai)
            {
                return;
            }
            var config = account.Config with { Ai = ai with { Consent = new AiConsent { Version = API.Limits.AssistantConsentVersion } } };
            account = account with { Config = config };
            parameters = new AccountUpdateParams { AccountId = account.Id, Config = config, Credentials = new Credentials() };
        }
        var write = WriteConsentAsync(parameters);
        lock (gate)
        {
            consentWrite = write;
        }
        _ = ForgetAsync(write);

        // Forgets the write once the daemon took it, unless a later one
        // replaced it; a refused one stays for the next OpenAsync to read.
        async Task ForgetAsync(Task w)
        {
            await w.ConfigureAwait(ConfigureAwaitOptions.SuppressThrowing);
            lock (gate)
            {
                if (w.IsCompletedSuccessfully && ReferenceEquals(consentWrite, w))
                {
                    consentWrite = null;
                }
            }
        }
    }

    /// <summary>The board's consent is the account's (<see cref="AcceptConsent"/>).</summary>
    public void AcceptBoardConsent() => AcceptConsent();

    private async Task WriteConsentAsync(AccountUpdateParams parameters)
    {
        try
        {
            await Client.CallAsync(API.AccountUpdate, parameters, CallTimeout).ConfigureAwait(false);
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException)
        {
            LogConsentRefused(Logger, RpcErrorText.DaemonError(e)?.Code.Value ?? 0);
            throw;
        }
    }

    /// <summary>
    /// apiassistant.Provider.Open: starts a session of
    /// <paramref name="spec"/> over the account (<c>assistant.sessionStart</c>
    /// with the purpose, the language, the bridge's path, the note account,
    /// the triage's trigger and run and the turn's timeout; the input of a
    /// one-shot purpose, which the daemon sends as the first turn at once, so
    /// that the session takes the <see cref="IAssistantSession.SubmitAsync"/>
    /// of it as done; the panel has none and its first turn is SubmitAsync's)
    /// and registers it for the daemon's events (the ones of a turn sent with
    /// the start may arrive before the start answers:
    /// <see cref="AssistantSessions"/> keeps them for the id the answer
    /// names). A failure is an <see cref="AssistantProviderException"/> with
    /// the reason AssistantRequest.ProviderFailure reads (<see cref="Failure"/>).
    /// The session delivers its events on the synchronization context of the
    /// caller.
    /// </summary>
    public async Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(spec);
        var context = SynchronizationContext.Current;
        if (!HasConsent)
        {
            throw new AssistantProviderException(ReasonConsentRequired);
        }
        Task? write;
        lock (gate)
        {
            write = consentWrite;
        }
        if (write is not null)
        {
            try
            {
                await write.WaitAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (Exception e) when (e is not OperationCanceledException || !cancellationToken.IsCancellationRequested)
            {
                // Read once: the next start goes to the daemon, which says
                // whether the consent is there.
                lock (gate)
                {
                    if (ReferenceEquals(consentWrite, write))
                    {
                        consentWrite = null;
                    }
                }
                throw Failure(e);
            }
        }
        var parameters = new AssistantSessionStartParams
        {
            AccountId = AccountId,
            Purpose = spec.Purpose,
            Language = spec.Language,
            Bridge = BridgePath ?? spec.BridgePath,
            NoteAccountId = spec.NoteAccountId,
            Trigger = spec.Trigger,
            RunId = spec.RunId,
            Input = spec.Input,
            TimeoutSeconds = spec.Timeout > TimeSpan.Zero ? SecondsRoundingUp(spec.Timeout) : 0,
        };
        AssistantSessionStartResult res;
        try
        {
            res = await Client.CallAsync(API.AssistantSessionStart, parameters, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception e) when (e is not OperationCanceledException || !cancellationToken.IsCancellationRequested)
        {
            throw Failure(e);
        }
        if (res.SessionId.Length == 0)
        {
            throw new AssistantProviderException(ReasonProtocol);
        }
        var s = new DaemonAssistantSession(this, res.SessionId, res.Tools, spec.Input is not null, context);
        Sessions?.Add(s);
        return s;
    }

    /// <summary>Go's <c>int((d + time.Second - 1) / time.Second)</c>: whole seconds, rounded up.</summary>
    public static int SecondsRoundingUp(TimeSpan d) =>
        (int)Math.Min(int.MaxValue, (d.Ticks + TimeSpan.TicksPerSecond - 1) / TimeSpan.TicksPerSecond);

    /// <summary>
    /// apiassistant.failure: the provider failure of a failed call: the class
    /// the daemon's error code stands for ("api_consent_required",
    /// "api_auth", …), the message of any other daemon error, else the
    /// failure's own message.
    /// </summary>
    internal static AssistantProviderException Failure(Exception error)
    {
        if (error is AssistantProviderException p)
        {
            return p;
        }
        if (RpcErrorText.DaemonError(error) is not { } e)
        {
            return new AssistantProviderException(error.Message);
        }
        return e.Code.Value switch
        {
            ErrorCode.AssistantConsentRequired => new AssistantProviderException(ReasonConsentRequired),
            ErrorCode.AuthRequired => new AssistantProviderException(ReasonOf(AssistantFailureClass.Auth)),
            ErrorCode.AssistantToolsUnsupported => new AssistantProviderException(ReasonOf(AssistantFailureClass.ToolsUnsupported)),
            ErrorCode.AssistantUnavailable => new AssistantProviderException(ReasonOf(AssistantFailureClass.Bridge)),
            ErrorCode.AssistantSessionNotFound => new AssistantProviderException(ReasonStopped),
            _ => new AssistantProviderException(e.Message),
        };
    }

    /// <summary>apiassistant.reasonOf: the reason of a failure class: "api_" and the class.</summary>
    public static string ReasonOf(AssistantFailureClass c) => Assistant.ApiFailurePrefix + c.Value;

    [LoggerMessage(Level = LogLevel.Warning, Message = "ai account: consent refused, code {Code}")]
    private static partial void LogConsentRefused(ILogger logger, int code);
}
