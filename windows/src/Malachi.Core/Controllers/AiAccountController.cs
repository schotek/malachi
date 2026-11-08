// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AIAccountController.swift;
// GTK: ui/internal/aiaccount/controller.go (Controller, Env, Form,
// TestOutcome, NewController, Check, Start, Close, the setters, Test, Save,
// testTimeout, saveTimeout, modelsTimeout). Its texts and rules are
// ui/internal/aiaccount text.go and failure.go (Model/AiAccount.cs).
//
// The Swift callbacks are events of the same words, as in
// JiraAccountController: onChange is Changed, onBusy BusyChanged, onBanner
// BannerChanged, onProblem ProblemShown, onDone Done, onClose
// CloseRequested. Every call goes through the controller's ControllerScope
// with the op counter of controller.go. Swift's form fields are the record
// AiAccountController.Form; a named provider's BaseUrl and an absent pin are
// null in the AiConfig, as on the wire.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The dialog that adds or edits an AI provider account (kind <c>ai</c>,
/// docs/api.md §4.1; the design is docs/plans/ai-api.md §7.1) with the
/// widgets replaced by events (aiaccount.Controller).
/// </summary>
/// <remarks>
/// <para>
/// The provider is fixed (the catalogue entry on Preferences → Integrations
/// chose it, and an existing account keeps its own); the dialog edits the
/// name, the server address of an OpenAI-compatible server with the examples
/// under it and a warning for http outside a loopback address (D8), the API
/// key (write-only: an existing account shows "Key stored · Replace Key…"),
/// Test (<c>account.test</c>, which lists the provider's models and says what
/// went wrong), the model to pick from them or type, the board's model, the
/// certificate pin of an https server, and the consent to sending mail to
/// the provider (D7, Q17), which Save needs.
/// </para>
/// <para>
/// Everything typed is the form until Save, which is <c>account.add</c> (a
/// new account, with the key as <c>credentials.password</c> and the consent)
/// or <c>account.update</c> (the edited account, with credentials only when a
/// new key was typed, and the consent only when it was given now). Test is
/// <c>account.test</c> with the form, or with the account's stored key when
/// editing without a replacement. The form is checked here, by the daemon's
/// rules, before any call. Every reply is dropped once the dialog closed or
/// another call started since (<see cref="Op"/>). Nothing typed here is
/// logged.
/// </para>
/// <para>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>.
/// </para>
/// </remarks>
public sealed partial class AiAccountController : IDisposable
{
    /// <summary>aiaccount testTimeout: account.test asks the provider for its models once.</summary>
    public static readonly TimeSpan TestTimeout = RpcTimeouts.Test;

    /// <summary>aiaccount saveTimeout: account.add lists the models and stores the key, so the keyring may prompt.</summary>
    public static readonly TimeSpan SaveTimeout = TimeSpan.FromSeconds(60);

    /// <summary>aiaccount modelsTimeout: assistant.providerModels answers from the daemon's cache or asks once.</summary>
    public static readonly TimeSpan ModelsTimeout = RpcTimeouts.AssistantModels;

    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly Func<string, Exception, string> errorText;
    // An existing account's key field is open (Replace Key…).
    private bool replacing;
    private string? shownBanner;

    /// <summary>
    /// aiaccount.NewController: adding an account of
    /// <paramref name="provider"/> when <paramref name="editing"/> is null,
    /// else editing that account (its provider wins), on the calling (UI)
    /// thread.
    /// </summary>
    /// <param name="client">The daemon.</param>
    /// <param name="provider">The provider kind of a new account; an unknown one is the OpenAI-compatible server.</param>
    /// <param name="editing">The account being edited; null when adding one.</param>
    /// <param name="errorText">
    /// aiaccount.Env.ErrorText: the client's general sentence for a failed
    /// call with the step's action as what (<see cref="RpcErrorText.Text(string, Exception?)"/>
    /// when null); the tests pass a stand-in.
    /// </param>
    /// <param name="logger">Receives steps and error codes, never a key, an address or a name.</param>
    /// <param name="pending">Counts the dialog's background work; one of its own when null.</param>
    public AiAccountController(
        RpcClient client,
        AiProvider provider,
        Account? editing = null,
        Func<string, Exception, string>? errorText = null,
        ILogger<AiAccountController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        Client = client;
        Account = editing;
        this.errorText = errorText ?? RpcErrorText.Text;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        var p = provider;
        if (editing is not null)
        {
            CurrentForm = CurrentForm with { Name = editing.Config.Name };
            if (editing.Config.Ai is { } ai)
            {
                p = ai.Provider;
                CurrentForm = CurrentForm with
                {
                    BaseUrl = ai.BaseUrl ?? "",
                    Model = ai.Model,
                    BoardModel = ai.BoardModel ?? "",
                    CertificateSha256 = ai.CertificateSha256 ?? "",
                };
            }
        }
        if (!p.IsValid)
        {
            p = AiProvider.OpenAiCompatible;
        }
        Provider = p;
        Consent = AiAccount.ConsentTexts(p);
    }

    // Outputs

    /// <summary>
    /// What the dialog shows changed (the form's rules, the models, the
    /// test's outcome, a problem): the UI reads the controller again (Swift
    /// <c>onChange</c>).
    /// </summary>
    public event EventHandler? Changed;

    /// <summary>A call started, with its progress text, or finished (null; Swift <c>onBusy</c>).</summary>
    public event EventHandler<string?>? BusyChanged;

    /// <summary>The dialog's banner; null hides it (Swift <c>onBanner</c>).</summary>
    public event EventHandler<string?>? BannerChanged;

    /// <summary>The field to flag and focus; <see cref="AiAccount.Field.None"/> clears the flag (Swift <c>onProblem</c>).</summary>
    public event EventHandler<AiAccount.Field>? ProblemShown;

    /// <summary>The account was stored; the UI closes the dialog (Swift <c>onDone</c>).</summary>
    public event EventHandler<(AccountId Id, AccountConfig Config)>? Done;

    /// <summary>Save had nothing to store; the UI closes the dialog (Swift <c>onClose</c>).</summary>
    public event EventHandler? CloseRequested;

    // State

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The fixed texts.</summary>
    public AiAccount.Strings Texts { get; } = AiAccount.Texts();

    /// <summary>The consent of the provider.</summary>
    public AiAccount.ConsentStrings Consent { get; }

    /// <summary>The provider kind.</summary>
    public AiProvider Provider { get; }

    /// <summary>The account being edited; null when adding one.</summary>
    public Account? Account { get; }

    /// <summary>The fields as typed.</summary>
    public Form CurrentForm { get; private set; } = new();

    /// <summary>
    /// The provider's models to pick from: the last test's, else what the
    /// daemon listed for an existing account; empty before either.
    /// </summary>
    public IReadOnlyList<AiModel> Models { get; private set; } = [];

    /// <summary>What the last Test said; null before one, and again once the server address or the key changed.</summary>
    public TestOutcome? Test { get; private set; }

    /// <summary>The progress text of the call under way; null when none runs.</summary>
    public string? Progress { get; private set; }

    /// <summary>account.add or account.update is under way: the dialog waits.</summary>
    public bool Saving { get; private set; }

    /// <summary>The dialog went away: late replies are dropped.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>The field flagged, <see cref="AiAccount.Field.None"/> for none.</summary>
    public AiAccount.Field Problem { get; private set; }

    /// <summary>The banner shown, null for none.</summary>
    public string? Banner { get; private set; }

    /// <summary>Bumped per RPC so that stale replies bail out.</summary>
    public int Op { get; private set; }

    // Presentation

    /// <summary>The dialog edits an existing account.</summary>
    public bool IsEditing => Account is not null;

    /// <summary>The name of the provider kind.</summary>
    public string ProviderName => Assistant.ProviderName(Provider);

    /// <summary>The dialog's title.</summary>
    public string Title => IsEditing ? Texts.EditTitle : Texts.AddTitle;

    /// <summary>The primary button: Add Account, or Save when editing.</summary>
    public string SaveLabel => IsEditing ? Texts.Save : Texts.Add;

    /// <summary>A provider with a server address of its own (the OpenAI-compatible server); the named providers have none.</summary>
    public bool UsesServer => Provider == AiProvider.OpenAiCompatible;

    /// <summary>A provider that may have no key (a local server).</summary>
    public bool KeyOptional => Provider == AiProvider.OpenAiCompatible;

    /// <summary>The key row of an existing account shows "Key stored" with Replace Key…, instead of the field.</summary>
    public bool KeyStored => IsEditing && !replacing;

    /// <summary>The key field's label: New API Key while replacing, API Key (optional) for a server, else API Key.</summary>
    public string KeyLabel => IsEditing ? Texts.NewKeyLabel : KeyOptional ? Texts.KeyOptionalLabel : Texts.KeyLabel;

    /// <summary>
    /// The TLS problem of the last test whose certificate the dialog may
    /// offer to trust (an https server whose certificate was refused or
    /// changed); null otherwise. <see cref="Trust"/> takes it.
    /// </summary>
    public CertTrust.Problem? TrustOffer => Test?.Trust;

    /// <summary>A certificate pin in the form.</summary>
    public bool Pinned => CurrentForm.CertificateSha256.Length > 0;

    /// <summary>The pinned fingerprint as shown ("AB12 CD34 …").</summary>
    public string PinText => CertTrust.FormatFingerprint(CurrentForm.CertificateSha256);

    /// <summary>
    /// The host of the address the account talks to: the provider's fixed
    /// one, or the server's as typed ("" while it is not an address).
    /// </summary>
    public string Host => UsesServer
        ? AiAccount.HostOf(CurrentForm.BaseUrl)
        : AiAccount.HostOf(new AiConfig { Provider = Provider }.EffectiveBaseUrl);

    /// <summary>The warning under the server address: plain http to a host that is not a loopback address (D8).</summary>
    public bool HttpWarning =>
        UsesServer && AiAccount.NormalizeBaseUrl(CurrentForm.BaseUrl) is { } u
        && u.StartsWith("http://", StringComparison.Ordinal) && !AiAccount.IsLoopback(AiAccount.HostOf(u));

    /// <summary>The warning's text; "" when there is none.</summary>
    public string HttpWarningText => HttpWarning ? AiAccount.HttpWarning(Host) : "";

    /// <summary>The consent check box is offered: a new account, or an existing one without the current consent text's version.</summary>
    public bool NeedsConsent => Account?.Config.Ai is not { Consented: true };

    /// <summary>The consent's text, with the http warning added for a server reached over plain http.</summary>
    public string ConsentBody
    {
        get
        {
            var w = HttpWarningText;
            return w.Length > 0 ? Consent.Body + " " + w : Consent.Body;
        }
    }

    /// <summary>A call is under way.</summary>
    public bool Busy => Progress is not null;

    /// <summary>Test is offered: no call runs.</summary>
    public bool CanTest => !Busy;

    /// <summary>Save is offered: no call runs, and the provider was reached.</summary>
    public bool CanSave => !Busy && Connected;

    /// <summary>
    /// The provider was reached with the form's key and server: a test of
    /// this dialog that succeeded (a changed key or server takes it back), or
    /// an existing account kept on its stored key and server, which connected
    /// when it was added. Until then only the name, the server, the key, the
    /// pin and Test are available: the
    /// models come from the test, so the model, the board's model, the
    /// consent and Save wait for it (<see cref="Unlocked"/>).
    /// </summary>
    public bool Connected
    {
        get
        {
            if (Test is { Ok: true })
            {
                return true;
            }
            if (!KeyStored || Account?.Config.Ai is not { } ai)
            {
                return false;
            }
            return AiAccount.HostOf(CurrentForm.BaseUrl) == AiAccount.HostOf(ai.BaseUrl);
        }
    }

    /// <summary>The model group and the consent are available: connected and not saving.</summary>
    public bool Unlocked => Connected && !Saving;

    /// <summary>
    /// The form differs from the stored account in what the daemon acts on:
    /// the name, the server, the models, the pin, a new key, or a consent
    /// given now. Always true when adding.
    /// </summary>
    public bool IsChanged
    {
        get
        {
            if (Account is not { } a)
            {
                return true;
            }
            if (replacing && CurrentForm.Key.Length > 0)
            {
                return true;
            }
            if (NeedsConsent && CurrentForm.Consent)
            {
                return true;
            }
            var cfg = Config();
            if (a.Config.Name.Trim() != cfg.Name)
            {
                return true;
            }
            if (a.Config.Ai is not { } x || cfg.Ai is not { } y)
            {
                return true;
            }
            return x with { Consent = null } != y with { Consent = null };
        }
    }

    /// <summary>
    /// aiaccount.Check: the first problem of the form by the daemon's rules
    /// (docs/api.md §4.1), or <see cref="AiAccount.Field.None"/> and "": the
    /// name, the server address (with https for a pin), the key (needed for
    /// a named provider, when adding or replacing), the models, and for Save
    /// the consent.
    /// </summary>
    public (AiAccount.Field Field, string Problem) Check(bool forSave) => Check(forSave, forTest: false);

    // Check; forTest is the Test button, before which the model may still be
    // empty: the test is what lists the models to choose from (account.test
    // accepts an empty model, an invalid one it refuses).
    private (AiAccount.Field Field, string Problem) Check(bool forSave, bool forTest)
    {
        var s = Texts;
        var f = CurrentForm;
        if (f.Name.Trim().Length == 0)
        {
            return (AiAccount.Field.Name, s.NameMissing);
        }
        if (UsesServer)
        {
            var raw = f.BaseUrl.Trim();
            if (raw.Length == 0)
            {
                return (AiAccount.Field.Server, s.ServerMissing);
            }
            if (AiAccount.NormalizeBaseUrl(raw) is not { } u)
            {
                return (AiAccount.Field.Server, s.ServerInvalid);
            }
            if (f.CertificateSha256.Length > 0 && !u.StartsWith("https://", StringComparison.Ordinal))
            {
                return (AiAccount.Field.Server, s.PinNeedsHttps);
            }
        }
        if (!KeyOptional && (!IsEditing || replacing) && f.Key.Trim().Length == 0)
        {
            return (AiAccount.Field.Key, s.KeyMissing);
        }
        switch (AiAccount.CheckModel(f.Model))
        {
            case AiAccount.ModelCheck.Missing when !forTest:
                return (AiAccount.Field.Model, s.ModelMissing);
            case AiAccount.ModelCheck.Invalid:
                return (AiAccount.Field.Model, s.ModelInvalid);
        }
        if (f.BoardModel.Trim().Length > 0 && AiAccount.CheckModel(f.BoardModel) == AiAccount.ModelCheck.Invalid)
        {
            return (AiAccount.Field.BoardModel, s.ModelInvalid);
        }
        if (forSave && NeedsConsent && !f.Consent)
        {
            return (AiAccount.Field.Consent, s.ConsentMissing);
        }
        return (AiAccount.Field.None, "");
    }

    // Lifecycle

    /// <summary>
    /// Delivers the initial state and, for an existing account, asks the
    /// daemon for the provider's models. Call it once, after subscribing.
    /// </summary>
    public void Start()
    {
        scope.VerifyAccess();
        Notify();
        if (Account is not null)
        {
            LoadModels();
        }
    }

    /// <summary>The dialog went away: every late reply is dropped from now on.</summary>
    public void Close()
    {
        Progress = null;
        Saving = false;
        scope.Close();
    }

    /// <inheritdoc/>
    public void Dispose() => Close();

    // Inputs from the UI

    /// <summary>The account's name as typed.</summary>
    public void SetName(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (Saving || value == CurrentForm.Name)
        {
            return;
        }
        CurrentForm = CurrentForm with { Name = value };
        Edited(AiAccount.Field.Name);
    }

    /// <summary>The server address as typed; the last test is about another server now, and a pin of another host goes.</summary>
    public void SetBaseUrl(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (Saving || value == CurrentForm.BaseUrl)
        {
            return;
        }
        var oldHost = AiAccount.HostOf(CurrentForm.BaseUrl);
        var newHost = AiAccount.HostOf(value);
        CurrentForm = CurrentForm with { BaseUrl = value };
        if (CurrentForm.CertificateSha256.Length > 0 && oldHost != newHost)
        {
            CurrentForm = CurrentForm with { CertificateSha256 = "" };
        }
        Test = null;
        Edited(AiAccount.Field.Server);
    }

    /// <summary>The key as typed; the last test is about another key now.</summary>
    public void SetKey(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (Saving || value == CurrentForm.Key)
        {
            return;
        }
        CurrentForm = CurrentForm with { Key = value };
        Test = null;
        Edited(AiAccount.Field.Key);
    }

    /// <summary>The model as typed or picked.</summary>
    public void SetModel(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (Saving || value == CurrentForm.Model)
        {
            return;
        }
        CurrentForm = CurrentForm with { Model = value };
        Edited(AiAccount.Field.Model);
    }

    /// <summary>The board's model as typed or picked.</summary>
    public void SetBoardModel(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (Saving || value == CurrentForm.BoardModel)
        {
            return;
        }
        CurrentForm = CurrentForm with { BoardModel = value };
        Edited(AiAccount.Field.BoardModel);
    }

    /// <summary>The consent check box.</summary>
    public void SetConsent(bool on)
    {
        if (Saving || on == CurrentForm.Consent)
        {
            return;
        }
        CurrentForm = CurrentForm with { Consent = on };
        Edited(AiAccount.Field.Consent);
    }

    /// <summary>Replace Key… of an existing account: the key field opens, empty.</summary>
    public void ReplaceKey()
    {
        if (Saving || !IsEditing || replacing)
        {
            return;
        }
        replacing = true;
        CurrentForm = CurrentForm with { Key = "" };
        Test = null;
        Edited(AiAccount.Field.Key);
    }

    /// <summary>Takes a replacement back: the stored key stays, the field closes.</summary>
    public void KeepKey()
    {
        if (Saving || !replacing)
        {
            return;
        }
        replacing = false;
        CurrentForm = CurrentForm with { Key = "" };
        Test = null;
        Edited(AiAccount.Field.Key);
    }

    /// <summary>
    /// Pins the certificate the last test refused (<see cref="TrustOffer"/>),
    /// after the user confirmed it with its fingerprint.
    /// </summary>
    public void Trust()
    {
        if (Saving || TrustOffer?.Cert is not { } cert)
        {
            return;
        }
        CurrentForm = CurrentForm with { CertificateSha256 = cert.Sha256 };
        Test = null;
        Edited(AiAccount.Field.Server);
    }

    /// <summary>Drops the pin.</summary>
    public void ForgetPin()
    {
        if (Saving || CurrentForm.CertificateSha256.Length == 0)
        {
            return;
        }
        CurrentForm = CurrentForm with { CertificateSha256 = "" };
        Test = null;
        Edited(AiAccount.Field.Server);
    }

    /// <summary>
    /// <c>account.test</c> with the form: the provider's models and the
    /// status. An existing account without a replaced key is tested with its
    /// stored key. The model may still be empty: the test lists the models.
    /// </summary>
    public void RunTest()
    {
        scope.VerifyAccess();
        if (IsClosed || Busy)
        {
            return;
        }
        var (field, problem) = Check(forSave: false, forTest: true);
        if (field != AiAccount.Field.None)
        {
            Fail(field, problem);
            return;
        }
        Test = null;
        ClearProblem();
        var parameters = new AccountTestParams { AccountId = Account?.Id, Config = Config(), Credentials = CredentialsOfForm() };
        SetBusy(Texts.Testing);
        Notify();
        var op = ++Op;
        scope.Perform(Client, API.AccountTest, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(null);
            if (outcome.TryGetValue(out var res, out var error))
            {
                Tested(res.Ai, null);
            }
            else
            {
                Tested(null, error);
            }
            Notify();
        }, TestTimeout);
    }

    /// <summary>
    /// <c>account.add</c> with the form, the key and the consent, or
    /// <c>account.update</c> of the edited account (credentials only with a
    /// new key, the consent only when given now); nothing
    /// (<see cref="CloseRequested"/>) when an edited form changes nothing.
    /// </summary>
    public void Save()
    {
        scope.VerifyAccess();
        if (IsClosed || Busy || !Connected)
        {
            return;
        }
        var (field, problem) = Check(forSave: true);
        if (field != AiAccount.Field.None)
        {
            Fail(field, problem);
            return;
        }
        var cfg = Config();
        if (NeedsConsent && CurrentForm.Consent && cfg.Ai is { } ai)
        {
            cfg = cfg with { Ai = ai with { Consent = new AiConsent { Version = API.Limits.AssistantConsentVersion } } };
        }
        if (IsEditing && !IsChanged)
        {
            RaiseEmpty(CloseRequested);
            return;
        }
        ClearProblem();
        Saving = true;
        Notify();
        var op = ++Op;
        var editing = Account;
        var credentials = CredentialsOfForm();
        var what = editing is null ? Texts.Adding : Texts.Saving;
        SetBusy(what);
        if (editing is not null)
        {
            scope.Perform(
                Client, API.AccountUpdate, new AccountUpdateParams { AccountId = editing.Id, Config = cfg, Credentials = credentials },
                outcome => Saved(op, what, editing.Id, cfg, outcome.Error), SaveTimeout);
        }
        else
        {
            scope.Perform(
                Client, API.AccountAdd, new AccountAddParams { Config = cfg, Credentials = credentials },
                outcome => Saved(op, what, outcome.Value?.AccountId ?? default, cfg, outcome.Error), SaveTimeout);
        }
    }

    // What account.add or account.update answered.
    private void Saved(int op, string what, AccountId id, AccountConfig cfg, Exception? error)
    {
        if (op != Op)
        {
            return;
        }
        Saving = false;
        SetBusy(null);
        if (error is not null)
        {
            var f = FailureOf(what, error);
            Fail(f.Field, f.Banner);
            Notify();
            return;
        }
        // The key is write-only: it leaves the form once stored.
        CurrentForm = CurrentForm with { Key = "" };
        replacing = false;
        LogSaved(logger, Provider.Value ?? "");
        scope.Raise(Done, this, (id, cfg));
    }

    // Reads what account.test answered.
    private void Tested(AiTestResult? res, Exception? error)
    {
        if (error is null && res is { Ok: true })
        {
            Models = res.Models;
            Test = new TestOutcome(true, Texts.Connected(res.Models.Count));
            LogTested(logger, res.Models.Count);
            return;
        }
        if (error is null)
        {
            if (res?.Error is not { } e)
            {
                Test = new TestOutcome(false, Texts.Unreachable);
                return;
            }
            error = new RpcException(e);
        }
        var f = FailureOf(Texts.Testing, error);
        Test = new TestOutcome(false, f.Banner, Trustable(error));
        Flag(f.Field);
    }

    // The TLS problem of a failed test the dialog may offer to trust: an
    // https server (only its host may be pinned), a certificate the system
    // refused or one other than the pinned (never a connection failure), and
    // not the pinned one.
    private CertTrust.Problem? Trustable(Exception error)
    {
        if (!UsesServer || RpcErrorText.DaemonError(error) is not { } e
            || AiAccount.NormalizeBaseUrl(CurrentForm.BaseUrl) is not { } u || !u.StartsWith("https://", StringComparison.Ordinal)
            || CertTrust.Details(e) is not { Cert: { } cert } p || p.Category == CertTrust.Category.Connection)
        {
            return null;
        }
        return Tls.NormalizeCertificateSha256(CurrentForm.CertificateSha256) is { } pinned && pinned == cert.Sha256 ? null : p;
    }

    /// <summary>
    /// The account configuration of the form: the edited account's with the
    /// name and the AI part replaced, or a new one.
    /// </summary>
    public AccountConfig Config()
    {
        var f = CurrentForm;
        var cfg = (Account?.Config ?? new AccountConfig { Name = "", Email = "" }) with
        {
            Kind = AccountKind.Ai,
            Name = f.Name.Trim(),
        };
        var ai = new AiConfig
        {
            Provider = Provider,
            Model = f.Model.Trim(),
            BoardModel = f.BoardModel.Trim(),
            Consent = Account?.Config.Ai?.Consent,
        };
        if (UsesServer)
        {
            ai = ai with
            {
                BaseUrl = AiAccount.NormalizeBaseUrl(f.BaseUrl),
                CertificateSha256 = f.CertificateSha256,
            };
        }
        return cfg with { Ai = ai };
    }

    // The key as typed when it goes with the call: always when adding, when
    // editing only as a replacement.
    private Credentials CredentialsOfForm() =>
        IsEditing && !replacing ? new Credentials() : new Credentials { Password = CurrentForm.Key.Trim() };

    // assistant.providerModels for the edited account, so that the model row
    // has the provider's models without a test; a failure leaves the row to
    // the typed id.
    private void LoadModels()
    {
        if (Account is not { } a)
        {
            return;
        }
        var op = ++Op;
        scope.Perform(Client, API.AssistantProviderModels, new AssistantProviderModelsParams { AccountId = a.Id }, outcome =>
        {
            if (op != Op || Test is not null || !outcome.TryGetValue(out var res, out _))
            {
                return;
            }
            Models = res.Models;
            Notify();
        }, ModelsTimeout);
    }

    // The banner and field of a failed call: the dialog's own sentence
    // (AiAccount.FailureOf), else the client's for the error. Only the code
    // is logged: the error may carry text from the provider.
    private AiAccount.Failure FailureOf(string what, Exception error)
    {
        LogCallFailed(logger, what, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);
        var f = AiAccount.FailureOf(error);
        if (f.Banner.Length == 0)
        {
            f = f with { Banner = errorText(what, error) };
        }
        if (f.Field == AiAccount.Field.Server && !UsesServer)
        {
            f = f with { Field = AiAccount.Field.None };
        }
        return f;
    }

    // Shows a problem: the banner and the flagged field.
    private void Fail(AiAccount.Field field, string banner)
    {
        Banner = banner;
        Flag(field);
        ShowBanner();
        Notify();
    }

    // A field changed, so the problem of that field and the banner are about
    // another form.
    private void Edited(AiAccount.Field field)
    {
        if (Problem == field || Banner is not null)
        {
            ClearProblem();
        }
        Notify();
    }

    private void ClearProblem()
    {
        Banner = null;
        Flag(AiAccount.Field.None);
        ShowBanner();
    }

    private void Flag(AiAccount.Field field)
    {
        if (Problem == field)
        {
            return;
        }
        Problem = field;
        scope.Raise(ProblemShown, this, field);
    }

    private void SetBusy(string? text)
    {
        if (Progress == text)
        {
            return;
        }
        Progress = text;
        scope.Raise(BusyChanged, this, text);
    }

    // Tells the UI the banner when it changed.
    private void ShowBanner()
    {
        if (shownBanner == Banner)
        {
            return;
        }
        shownBanner = Banner;
        scope.Raise(BannerChanged, this, Banner);
    }

    private void Notify() => RaiseEmpty(Changed);

    // Raises a parameterless event, every handler guarded (ControllerEvents).
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

    [LoggerMessage(Level = LogLevel.Information, Message = "ai account tested: {Count} models")]
    private static partial void LogTested(ILogger logger, int count);

    [LoggerMessage(Level = LogLevel.Information, Message = "ai account call failed: {What}, code {Code}")]
    private static partial void LogCallFailed(ILogger logger, string what, int code);

    [LoggerMessage(Level = LogLevel.Information, Message = "ai account saved: provider {Provider}")]
    private static partial void LogSaved(ILogger logger, string provider);

    /// <summary>
    /// aiaccount.Form: the dialog's fields as typed. <see cref="Key"/> is the
    /// API key field, empty when nothing was typed (an existing account then
    /// keeps its stored key); <see cref="CertificateSha256"/> the pin of an
    /// https server; <see cref="Consent"/> the check box.
    /// </summary>
    public sealed record Form
    {
        /// <summary>The account's name.</summary>
        public string Name { get; init; } = "";

        /// <summary>The server address of an OpenAI-compatible server.</summary>
        public string BaseUrl { get; init; } = "";

        /// <summary>The API key as typed.</summary>
        public string Key { get; init; } = "";

        /// <summary>The model id.</summary>
        public string Model { get; init; } = "";

        /// <summary>The board's model id.</summary>
        public string BoardModel { get; init; } = "";

        /// <summary>The pinned certificate's SHA-256 (hex).</summary>
        public string CertificateSha256 { get; init; } = "";

        /// <summary>The consent check box.</summary>
        public bool Consent { get; init; }
    }

    /// <summary>
    /// aiaccount.TestOutcome: what the last Test said: whether the provider
    /// answered, the status line, and the TLS problem of a failed https
    /// connection whose certificate the user may trust (null otherwise).
    /// </summary>
    /// <param name="Ok">The provider answered.</param>
    /// <param name="Text">The status line.</param>
    /// <param name="Trust">The certificate the user may trust.</param>
    public sealed record TestOutcome(bool Ok = false, string Text = "", CertTrust.Problem? Trust = null);
}
