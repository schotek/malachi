// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/EWayWizardController.swift;
// GTK: ui/internal/accountwizard/eway_flow.go (ewayFlow,
// detectEwayTimeout, ewayTestTimeout, ewaySaveTimeout). Its pages, texts
// and rules are ui/internal/crm text.go (CrmText.WizardTexts,
// CheckServiceInput, FailureOf).
//
// The Swift callbacks are events of the same words, as in
// JiraWizardController: onPages is PagesChanged, onBusy BusyChanged,
// onServiceCheck ServiceChecked, onName NameChanged, onBanner
// BannerChanged, onProblem ProblemShown, onFocus FocusRequested, onDone
// Done. Every call goes through the controller's ControllerScope with the
// op counter of eway_flow.go; Go's empty strings for "none" (a banner, the
// progress) are nulls, as in Swift.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Crm;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The flow of the assistant that adds an eWay-CRM account (kind
/// <c>eway</c>, docs/eway.md §2.4), with the widgets replaced by events.
/// Two pages: the web service address (<c>account.detectEway</c>, which
/// also says whether the instance signs in with Microsoft, which this
/// version cannot do: the assistant says so there, before any password is
/// asked), and the sign-in with the account's name, user name and password
/// and the check box "Also save later messages of saved conversations" (on
/// for a new account, docs/eway.md D22) (<c>account.test</c>, one sign-in,
/// then <c>account.add</c>). Editing an account (Integrations.Editor.Eway)
/// opens on the sign-in page with the account's address, name and user
/// name; the password may stay empty to keep the stored one, unless the
/// user name changed (the stored password never goes to another user). It
/// saves through <c>account.update</c>.
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>. Every reply is dropped once the
/// assistant closed or anything else started since (<see cref="Op"/>: Back,
/// another call). Nothing typed here is logged; the password leaves only in
/// <see cref="Credentials"/>.
/// </remarks>
public sealed partial class EwayWizardController : IDisposable
{
    // The messages of the controller's own RpcErrors: technical and never
    // shown, like the daemon's (RpcErrorText speaks for the code).
    private const string NoServiceAddressCode = "no service address";
    private const string MicrosoftSignInCode = "microsoft sign-in";
    private const string NoEwayResultCode = "no eway result";
    private const string EwayTestFailedCode = "eway test failed";

    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private string password = "";
    private bool nameTyped;
    private CrmWizardField problem = CrmWizardField.None;

    /// <summary>An assistant over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="editing">
    /// The account being edited, null when adding one: it opens on the
    /// sign-in page and saves with <c>account.update</c>.
    /// </param>
    /// <param name="logger">Receives steps and error codes, never an address, a name or a password.</param>
    /// <param name="pending">Counts the assistant's background work; one of its own when null.</param>
    public EwayWizardController(
        RpcClient client,
        Account? editing = null,
        ILogger<EwayWizardController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        Client = client;
        Editing = editing;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        if (editing is null)
        {
            Pages = [CrmWizardPage.Service];
            return;
        }
        Pages = [CrmWizardPage.Credentials];
        Name = editing.Config.Name;
        nameTyped = true;
        if (editing.Config.Eway is { } e)
        {
            ServiceInput = e.ServiceUrl;
            Service = new AccountDetectEwayResult { ServiceUrl = e.ServiceUrl, MicrosoftSignIn = false };
            User = e.Username;
            FollowThreads = e.FollowThreads;
        }
        else
        {
            FollowThreads = false;
        }
    }

    // Outputs

    /// <summary>The page stack changed; the last page is the visible one (Swift <c>onPages</c>).</summary>
    public event EventHandler<IReadOnlyList<CrmWizardPage>>? PagesChanged;

    /// <summary>A call started, with its progress text, or finished (null; Swift <c>onBusy</c>).</summary>
    public event EventHandler<string?>? BusyChanged;

    /// <summary>
    /// What the address field allows now: Next, and the text under the field
    /// ("" for none; <see cref="CrmText.CheckServiceInput"/>; Swift
    /// <c>onServiceCheck</c>).
    /// </summary>
    public event EventHandler<(bool Ok, string Problem)>? ServiceChecked;

    /// <summary>
    /// The name field's text: the default name once the address was looked
    /// up, while the user did not type one (Swift <c>onName</c>).
    /// </summary>
    public event EventHandler<string>? NameChanged;

    /// <summary>A page's banner; a null text hides it (Swift <c>onBanner</c>).</summary>
    public event EventHandler<(CrmWizardPage Page, string? Text)>? BannerChanged;

    /// <summary>The field to flag; <see cref="CrmWizardField.None"/> clears the flag (Swift <c>onProblem</c>).</summary>
    public event EventHandler<CrmWizardField>? ProblemShown;

    /// <summary>Move the keyboard focus to a field (Swift <c>onFocus</c>).</summary>
    public event EventHandler<CrmWizardField>? FocusRequested;

    /// <summary>The account was stored; the window closes the assistant (Swift <c>onDone</c>).</summary>
    public event EventHandler<(AccountId Id, AccountConfig Config)>? Done;

    // State

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The account being edited; null when adding one.</summary>
    public Account? Editing { get; }

    /// <summary>The fixed texts (<see cref="CrmText.WizardTexts"/>).</summary>
    public CrmWizardStrings Texts { get; } = CrmText.WizardTexts();

    /// <summary>The page stack; the last page is the visible one.</summary>
    public IReadOnlyList<CrmWizardPage> Pages { get; private set; }

    /// <summary>The assistant went away: late replies are dropped.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Bumped per RPC and by Back so stale replies bail out (eway_flow.go <c>op</c>).</summary>
    public int Op { get; private set; }

    /// <summary>The progress text of the call under way; null when none runs.</summary>
    public string? Progress { get; private set; }

    /// <summary>The address field as typed.</summary>
    public string ServiceInput { get; private set; } = "";

    /// <summary>
    /// What <c>account.detectEway</c> found for <see cref="ServiceInput"/>;
    /// null until it answered and again once the address changed. When
    /// editing, the account's service.
    /// </summary>
    public AccountDetectEwayResult? Service { get; private set; }

    /// <summary>The account's name as typed.</summary>
    public string Name { get; private set; } = "";

    /// <summary>The user name as typed.</summary>
    public string User { get; private set; } = "";

    /// <summary>
    /// The check box "Also save later messages of saved conversations"
    /// (<see cref="EwayConfig.FollowThreads"/>, docs/eway.md D22): on for a
    /// new account, the account's value when editing.
    /// </summary>
    public bool FollowThreads { get; private set; } = true;

    // Presentation

    /// <summary>An existing account is being edited.</summary>
    public bool IsEditing => Editing is not null;

    /// <summary>The window's title.</summary>
    public string Title => IsEditing ? Texts.EditTitle : Texts.Title;

    /// <summary>The header's Back is offered.</summary>
    public bool CanGoBack => Pages.Count > 1;

    /// <summary>The visible page.</summary>
    public CrmWizardPage Top => Pages[^1];

    /// <summary>A call runs.</summary>
    public bool Busy => Progress is not null;

    /// <summary>The password field's label: optional when editing.</summary>
    public string PasswordLabel => IsEditing ? Texts.NewPasswordLabel : Texts.PasswordLabel;

    /// <summary>The address the sign-in page shows (read-only there).</summary>
    public string ServiceUrl => Service?.ServiceUrl ?? "";

    /// <summary>The credentials as sent: the password as typed (an empty one keeps the stored one when editing).</summary>
    public Credentials Credentials => new() { Password = password.Length > 0 ? password : null };

    private string TrimmedUser => User.Trim();

    private string EditedUser => (Editing?.Config.Eway?.Username ?? "").Trim();

    private bool UserChanged => Editing is null || TrimmedUser != EditedUser;

    /// <summary>A page's title in its header.</summary>
    public string PageTitle(CrmWizardPage page) => page == CrmWizardPage.Credentials ? Texts.CredentialsTitle : Texts.Title;

    /// <summary>A page's button, without its mnemonic: Next on the address, Add Account on the sign-in, Save when editing.</summary>
    public string NextLabel(CrmWizardPage page) => WizardController.WithoutMnemonic(page == CrmWizardPage.Service
        ? Texts.Next
        : IsEditing ? Texts.Save : Texts.Add);

    // Lifecycle

    /// <summary>Delivers the initial state to the events. Call once, after subscribing.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        scope.Raise(ServiceChecked, this, CrmText.CheckServiceInput(ServiceInput));
        scope.Raise(NameChanged, this, Name);
        scope.Raise(PagesChanged, this, Pages);
    }

    /// <summary>The assistant went away: every late reply is dropped from now on.</summary>
    public void Close()
    {
        Progress = null;
        scope.Close();
    }

    /// <inheritdoc/>
    public void Dispose() => Close();

    // Inputs from the UI

    /// <summary>The address field as typed. A changed address forgets what was found for the previous one; ignored when editing.</summary>
    public void SetService(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        if (IsEditing || raw == ServiceInput)
        {
            return;
        }
        ServiceInput = raw;
        scope.Raise(ServiceChecked, this, CrmText.CheckServiceInput(raw));
        Clear(CrmWizardField.Service);
        scope.Raise(BannerChanged, this, (CrmWizardPage.Service, (string?)null));
        Service = null;
    }

    /// <summary>The name field as typed; once typed, the default no longer replaces it.</summary>
    public void SetName(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s == Name)
        {
            return;
        }
        Name = s;
        nameTyped = true;
    }

    /// <summary>The user name and password as typed.</summary>
    public void SetCredentials(string user, string pass)
    {
        ArgumentNullException.ThrowIfNull(user);
        ArgumentNullException.ThrowIfNull(pass);
        if (user == User && pass == password)
        {
            return;
        }
        if (user != User)
        {
            Clear(CrmWizardField.User);
        }
        if (pass != password)
        {
            Clear(CrmWizardField.Password);
        }
        User = user;
        password = pass;
        scope.Raise(BannerChanged, this, (CrmWizardPage.Credentials, (string?)null));
    }

    /// <summary>The check box as clicked; it goes with the configuration on account.test, account.add and account.update.</summary>
    public void SetFollowThreads(bool on) => FollowThreads = on;

    /// <summary>The visible page's button: look the address up, or test the sign-in and store the account.</summary>
    public void Next()
    {
        if (IsClosed || Busy)
        {
            return;
        }
        if (Top == CrmWizardPage.Service)
        {
            Detect();
            return;
        }
        Test();
    }

    /// <summary>The header's Back: pops the visible page; a call under way is dropped.</summary>
    public void Back()
    {
        if (!CanGoBack)
        {
            return;
        }
        Op++;
        SetBusy(null);
        Pages = [.. Pages.Take(Pages.Count - 1)];
        scope.Raise(PagesChanged, this, Pages);
    }

    // Navigation and fields

    private void Push(CrmWizardPage page)
    {
        var i = IndexOf(page);
        Pages = i >= 0 ? [.. Pages.Take(i + 1)] : [.. Pages, page];
        scope.Raise(PagesChanged, this, Pages);
    }

    private void PopTo(CrmWizardPage page)
    {
        var i = IndexOf(page);
        if (i < 0 || i + 1 >= Pages.Count)
        {
            return;
        }
        Pages = [.. Pages.Take(i + 1)];
        scope.Raise(PagesChanged, this, Pages);
    }

    private int IndexOf(CrmWizardPage page)
    {
        for (var i = 0; i < Pages.Count; i++)
        {
            if (Pages[i] == page)
            {
                return i;
            }
        }
        return -1;
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

    private void Flag(CrmWizardField field)
    {
        problem = field;
        scope.Raise(ProblemShown, this, field);
        if (field != CrmWizardField.None)
        {
            scope.Raise(FocusRequested, this, field);
        }
    }

    private void Clear(CrmWizardField field)
    {
        if (problem != field || field == CrmWizardField.None)
        {
            return;
        }
        problem = CrmWizardField.None;
        scope.Raise(ProblemShown, this, CrmWizardField.None);
    }

    // A failed step (CrmText.FailureOf): back to its page with the banner,
    // the client's sentence for the error when the step has none, the field
    // flagged and focused.
    private void Fail(CrmWizardStep step, Exception error)
    {
        var f = CrmText.FailureOf(step, error, IsEditing);
        LogStepFailed(logger, step, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);
        PopTo(f.Page);
        var banner = f.Banner.Length > 0 ? f.Banner : RpcErrorText.Text(f.What, error);
        scope.Raise(BannerChanged, this, (f.Page, (string?)banner));
        Flag(f.Field);
    }

    // Steps

    // account.detectEway for the typed address; http is refused before
    // anything is asked.
    private void Detect()
    {
        var check = CrmText.CheckServiceInput(ServiceInput);
        if (!check.Ok)
        {
            scope.Raise(ServiceChecked, this, check);
            Flag(CrmWizardField.Service);
            return;
        }
        scope.Raise(BannerChanged, this, (CrmWizardPage.Service, (string?)null));
        var parameters = new AccountDetectEwayParams { ServiceUrl = ServiceInput.Trim() };
        SetBusy(Texts.Contacting);
        var op = ++Op;
        scope.Perform(Client, API.AccountDetectEway, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(null);
            Detected(outcome);
        });
    }

    private void Detected(Outcome<AccountDetectEwayResult> outcome)
    {
        if (!outcome.TryGetValue(out var res, out var error))
        {
            Fail(CrmWizardStep.Detect, error!);
            return;
        }
        if (res.ServiceUrl.Length == 0)
        {
            Fail(CrmWizardStep.Detect, new RpcException(new RpcError { Code = ErrorCode.ServerError, Message = NoServiceAddressCode }));
            return;
        }
        if (res.MicrosoftSignIn)
        {
            Fail(CrmWizardStep.Detect, new RpcException(new RpcError
            {
                Code = ErrorCode.CrmSignInUnsupported,
                Message = MicrosoftSignInCode,
                Data = System.Text.Json.JsonSerializer.SerializeToElement(
                    new CrmSignInUnsupportedData { Reason = CrmSignInReason.Microsoft }, ApiJsonContext.Wire.CrmSignInUnsupportedData),
            }));
            return;
        }
        LogFound(logger);
        Service = res;
        if (!nameTyped)
        {
            Name = CrmRules.DefaultName(res.ServiceUrl);
            scope.Raise(NameChanged, this, Name);
        }
        Push(CrmWizardPage.Credentials);
        scope.Raise(FocusRequested, this, TrimmedUser.Length == 0 ? CrmWizardField.User : CrmWizardField.Password);
    }

    /// <summary>
    /// The account's configuration from the pages: the edited account's with
    /// the new name, user name and follow switch (its user GUID forgotten
    /// when the user changed), or a new eway account.
    /// </summary>
    public AccountConfig Config()
    {
        var name = Name.Trim();
        if (Editing is { } editing)
        {
            var e = editing.Config.Eway ?? new EwayConfig { ServiceUrl = "" };
            return editing.Config with
            {
                Name = name,
                Eway = e with
                {
                    UserGuid = UserChanged ? null : e.UserGuid,
                    Username = TrimmedUser,
                    FollowThreads = FollowThreads,
                },
            };
        }
        return new AccountConfig
        {
            Kind = AccountKind.Eway,
            Name = name,
            Email = "",
            Eway = new EwayConfig { ServiceUrl = ServiceUrl, Username = TrimmedUser, FollowThreads = FollowThreads },
        };
    }

    // account.test with the typed credentials (with the stored password when
    // editing and it is left empty), then store.
    private void Test()
    {
        if (Service is null)
        {
            PopTo(CrmWizardPage.Service);
            return;
        }
        if (TrimmedUser.Length == 0)
        {
            Flag(CrmWizardField.User);
            return;
        }
        if (password.Length == 0 && UserChanged)
        {
            scope.Raise(BannerChanged, this, (CrmWizardPage.Credentials, (string?)Texts.PasswordPrompt));
            Flag(CrmWizardField.Password);
            return;
        }
        scope.Raise(BannerChanged, this, (CrmWizardPage.Credentials, (string?)null));
        var parameters = new AccountTestParams { AccountId = Editing?.Id, Config = Config(), Credentials = Credentials };
        SetBusy(Texts.SigningIn);
        var op = ++Op;
        scope.Perform(
            async ct =>
            {
                var res = await Client.CallAsync(API.AccountTest, parameters, CrmTimeouts.Test, ct);
                if (res.Eway is not { } eway)
                {
                    throw new RpcException(new RpcError { Code = ErrorCode.ServerError, Message = NoEwayResultCode });
                }
                if (!eway.Ok)
                {
                    throw new RpcException(eway.Error ?? new RpcError { Code = ErrorCode.ServerError, Message = EwayTestFailedCode });
                }
                return res;
            },
            outcome =>
            {
                if (op != Op)
                {
                    return;
                }
                if (outcome.Error is { } error)
                {
                    SetBusy(null);
                    Fail(CrmWizardStep.Test, error);
                    return;
                }
                Store(parameters.Config);
            });
    }

    // account.add, or account.update when editing, of cfg.
    private void Store(AccountConfig cfg)
    {
        var creds = Credentials;
        var editing = Editing;
        SetBusy(editing is null ? Texts.Adding : Texts.Saving);
        var op = ++Op;
        void Stored(Outcome<AccountId> outcome)
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(null);
            if (!outcome.TryGetValue(out var id, out var error))
            {
                Fail(CrmWizardStep.Save, error!);
                return;
            }
            LogSaved(logger, id.Value, editing is not null);
            scope.Raise(Done, this, (id, cfg));
        }
        if (editing is not null)
        {
            scope.Perform(
                async ct =>
                {
                    await Client.CallAsync(API.AccountUpdate, new AccountUpdateParams { AccountId = editing.Id, Config = cfg, Credentials = creds }, CrmTimeouts.Store, ct);
                    return editing.Id;
                },
                Stored);
        }
        else
        {
            scope.Perform(
                async ct => (await Client.CallAsync(API.AccountAdd, new AccountAddParams { Config = cfg, Credentials = creds }, CrmTimeouts.Store, ct)).AccountId,
                Stored);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "eway assistant step {Step} failed: code {Code}")]
    private static partial void LogStepFailed(ILogger logger, CrmWizardStep step, int code);

    [LoggerMessage(Level = LogLevel.Information, Message = "eway instance found")]
    private static partial void LogFound(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "eway account saved: {AccountId}, edit {Editing}")]
    private static partial void LogSaved(ILogger logger, string accountId, bool editing);
}
