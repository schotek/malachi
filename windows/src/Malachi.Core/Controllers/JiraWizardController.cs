// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/JiraWizardController.swift;
// GTK: ui/internal/accountwizard/jira_flow.go (jiraFlow). Its pages, texts
// and rules are ui/internal/jira/wizard.go (Jira.Wizard.cs).
//
// The Swift callbacks are events of the same words: onPages is
// PagesChanged, onBusy BusyChanged, onSiteCheck SiteChecked, onDetected
// DetectedChanged, onCredentialPage CredentialPageChanged, onSpaces
// SpacesChanged, onSpacesProblem SpacesProblemChanged, onEmailField
// EmailFieldChanged, onBanner BannerChanged, onProblems ProblemsShown,
// onFocus FocusRequested, onDone Done. onOpenURL is the injected ILauncher
// (docs/windows-port.md §10), whose failure is a toast (ToastRequested), as
// in GTK's openURL. Every call goes through the controller's
// ControllerScope with the op counter of jira_flow.go.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The flow of the assistant that adds a Jira account (kind <c>jira</c>),
/// with the widgets replaced by events, as <see cref="WizardController"/>
/// does for mail accounts. Three pages: the site (<c>account.detectSite</c>),
/// the credentials (checked by <c>account.listSpaces</c>, which also lists
/// the spaces with an estimate of their issues) and the spaces with the
/// offline window and "Only Issues Involving Me" (<c>account.add</c>).
/// Editing an account (its token only) opens on the credentials page and
/// saves the unchanged configuration with the new token through
/// <c>account.update</c>.
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>. Every reply is dropped once the
/// assistant closed or anything else started since (<see cref="Op"/>: Back,
/// another call). Nothing typed here is logged; the token leaves only in
/// <see cref="Credentials"/>.
/// </remarks>
public sealed partial class JiraWizardController : IDisposable
{
    private readonly ControllerScope scope;
    private readonly ILauncher launcher;
    private readonly ILogger logger;
    private readonly HashSet<string> selected = [];
    private HashSet<Field> problems = [];

    // The site Spaces were listed for: a space id means nothing on another
    // site, so the choice starts over there.
    private string? spacesSite;

    // RequestToken's reason, shown by Start.
    private ErrorCode? tokenRequest;
    private bool started;

    /// <summary>An assistant over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="launcher">Opens the page where a Jira Cloud user creates an API token.</param>
    /// <param name="editing">
    /// The account whose token is replaced, null when adding one: it opens on
    /// the credentials page and saves with <c>account.update</c>, the
    /// configuration staying as it is.
    /// </param>
    /// <param name="logger">Receives steps and error classes, never an address or a token.</param>
    /// <param name="pending">Counts the assistant's background work; one of its own when null.</param>
    public JiraWizardController(
        RpcClient client,
        ILauncher launcher,
        Account? editing = null,
        ILogger<JiraWizardController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(launcher);
        Client = client;
        this.launcher = launcher;
        Editing = editing;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        if (editing is null)
        {
            Pages = [JiraWizardPage.Site];
            return;
        }
        Pages = [JiraWizardPage.Credentials];
        Email = editing.Config.Email;
        if (editing.Config.Jira is { } jc)
        {
            SiteInput = jc.SiteUrl;
            Site = new AccountDetectSiteResult { Kind = AccountKind.Jira, SiteUrl = jc.SiteUrl, Deployment = jc.Deployment, CloudId = jc.CloudId };
            Login = jc.Login ?? "";
            OfflineDays = Jira.OfflineChoices[Jira.IndexOfOfflineDays(jc.OfflineDays ?? 0)];
            OnlyMine = jc.OnlyMine ?? false;
        }
    }

    // Outputs

    /// <summary>The page stack changed; the last page is the visible one (Swift <c>onPages</c>).</summary>
    public event EventHandler<IReadOnlyList<JiraWizardPage>>? PagesChanged;

    /// <summary>
    /// A call started, with its progress text, or finished (null). The
    /// visible page waits for it; Back stays usable and drops its reply
    /// (Swift <c>onBusy</c>).
    /// </summary>
    public event EventHandler<string?>? BusyChanged;

    /// <summary>
    /// What the site field allows now: Next, and the text under the field (""
    /// for none; <see cref="Jira.CheckSiteInput"/>; Swift <c>onSiteCheck</c>).
    /// </summary>
    public event EventHandler<(bool Ok, string Problem)>? SiteChecked;

    /// <summary>
    /// The text under the site field once the site answered
    /// (<see cref="Jira.Detected"/>); "" when the address changed since
    /// (Swift <c>onDetected</c>).
    /// </summary>
    public event EventHandler<string>? DetectedChanged;

    /// <summary>
    /// The credentials page for the site's deployment
    /// (<see cref="Jira.CredentialFields"/>; Swift <c>onCredentialPage</c>).
    /// </summary>
    public event EventHandler<JiraCredentialPage>? CredentialPageChanged;

    /// <summary>The spaces page's list, in the daemon's order, with the ids chosen (Swift <c>onSpaces</c>).</summary>
    public event EventHandler<(IReadOnlyList<JiraSpaceRow> Rows, IReadOnlySet<string> Selected)>? SpacesChanged;

    /// <summary>
    /// Why the spaces page cannot add the account now ("" when it can;
    /// <see cref="Jira.SpacesProblem"/>; Swift <c>onSpacesProblem</c>).
    /// </summary>
    public event EventHandler<string>? SpacesProblemChanged;

    /// <summary>
    /// Whether the spaces page asks for the account's e-mail address (a Data
    /// Center site that does not reveal it, <see cref="Jira.NeedsEmail"/>),
    /// and the address to show in the field (Swift <c>onEmailField</c>).
    /// </summary>
    public event EventHandler<(bool Shown, string Email)>? EmailFieldChanged;

    /// <summary>A page's banner; a null text hides it (Swift <c>onBanner</c>).</summary>
    public event EventHandler<(JiraWizardPage Page, string? Text)>? BannerChanged;

    /// <summary>The fields to flag; an empty set clears the flags (Swift <c>onProblems</c>).</summary>
    public event EventHandler<IReadOnlySet<Field>>? ProblemsShown;

    /// <summary>Move the keyboard focus to a field (Swift <c>onFocus</c>).</summary>
    public event EventHandler<Field>? FocusRequested;

    /// <summary>Show a toast inside the assistant: the token page could not be opened.</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The account was stored; the window closes the assistant (Swift <c>onDone</c>).</summary>
    public event EventHandler<(AccountId Id, AccountConfig Config)>? Done;

    // State

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The account whose token is replaced; null when adding one.</summary>
    public Account? Editing { get; }

    /// <summary>The fixed texts (<see cref="Jira.WizardTexts"/>).</summary>
    public JiraWizardStrings Texts { get; } = Jira.WizardTexts();

    /// <summary>The window the browser's own dialogs belong to (the assistant's), 0 for none.</summary>
    public nint Owner { get; set; }

    /// <summary>The page stack; the last page is the visible one.</summary>
    public IReadOnlyList<JiraWizardPage> Pages { get; private set; }

    /// <summary>The assistant went away: late replies are dropped (Swift <c>closed</c>).</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Bumped per RPC and by Back so stale replies bail out (jira_flow.go <c>op</c>).</summary>
    public int Op { get; private set; }

    /// <summary>The progress text of the call under way; null when none runs.</summary>
    public string? Progress { get; private set; }

    /// <summary>The site field as typed.</summary>
    public string SiteInput { get; private set; } = "";

    /// <summary>
    /// What <c>account.detectSite</c> found for <see cref="SiteInput"/>; null
    /// until it answered and again once the address changed. When editing,
    /// the account's site.
    /// </summary>
    public AccountDetectSiteResult? Site { get; private set; }

    /// <summary>The Atlassian account's e-mail address (Jira Cloud's login).</summary>
    public string Login { get; private set; } = "";

    /// <summary>The token as typed; sent trimmed.</summary>
    public string Token { get; private set; } = "";

    /// <summary>The account's address on Data Center (the signed-in user's, or typed when the site hides it).</summary>
    public string Email { get; private set; } = "";

    /// <summary>The user <c>account.listSpaces</c> signed in as.</summary>
    public SiteUser? User { get; private set; }

    /// <summary>The spaces of the last <c>account.listSpaces</c>, in its order.</summary>
    public IReadOnlyList<Space> Spaces { get; private set; } = [];

    /// <summary>The ids of the chosen spaces.</summary>
    public IReadOnlySet<string> Selected => selected;

    /// <summary>"Only Issues Involving Me".</summary>
    public bool OnlyMine { get; private set; }

    /// <summary>The offline window, one of <see cref="Jira.OfflineChoices"/>.</summary>
    public int OfflineDays { get; private set; } = API.Limits.DefaultJiraOfflineDays;

    /// <summary>The spaces page asks for the account's address.</summary>
    public bool NeedsEmail { get; private set; }

    // Presentation

    /// <summary>An existing account's token is being replaced.</summary>
    public bool IsEditing => Editing is not null;

    /// <summary>The window's title.</summary>
    public string Title => IsEditing ? L10n.T("Edit Account") : Texts.Title;

    /// <summary>The deployment the pages are for: the found site's, Jira Cloud before one was found.</summary>
    public JiraDeployment Deployment => Site?.Deployment ?? JiraDeployment.Cloud;

    /// <summary>The credentials page of <see cref="Deployment"/>.</summary>
    public JiraCredentialPage CredentialPage => Jira.CredentialFields(Deployment);

    /// <summary>The login belongs to the account being edited (its address and its uniqueness); only the token changes.</summary>
    public bool LoginEditable => !IsEditing;

    /// <summary>The header's Back is offered.</summary>
    public bool CanGoBack => Pages.Count > 1;

    /// <summary>The labels of the offline window's choices.</summary>
    public static IReadOnlyList<string> OfflineLabels => Jira.OfflineChoiceLabels();

    /// <summary>The offline window's choice shown.</summary>
    public int OfflineIndex => Jira.IndexOfOfflineDays(OfflineDays);

    /// <summary>A call runs.</summary>
    public bool Busy => Progress is not null;

    // The token as sent: a pasted one often carries a line break.
    private string TrimmedToken => Token.Trim();

    /// <summary>A page's title in the header.</summary>
    public string PageTitle(JiraWizardPage page) => page switch
    {
        JiraWizardPage.Site => Texts.SiteTitle,
        JiraWizardPage.Credentials => L10n.T("Sign In"),
        _ => Texts.SpacesTitle,
    };

    /// <summary>A page's button, without its mnemonic: Next, Save when editing, Add Account on the spaces.</summary>
    public string NextLabel(JiraWizardPage page) => WizardController.WithoutMnemonic(page switch
    {
        JiraWizardPage.Site => L10n.T("_Next"),
        JiraWizardPage.Credentials => IsEditing ? L10n.T("_Save") : L10n.T("_Next"),
        _ => L10n.T("_Add Account"),
    });

    /// <summary>The title of the spaces page's address field (jira_flow.go <c>emailLabel</c>).</summary>
    public static string EmailLabel => L10n.T("E-mail Address");

    // Lifecycle

    /// <summary>Delivers the initial state to the events. Call once, after subscribing.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        var check = Jira.CheckSiteInput(SiteInput);
        scope.Raise(SiteChecked, this, check);
        scope.Raise(CredentialPageChanged, this, CredentialPage);
        scope.Raise(PagesChanged, this, Pages);
        started = true;
        ShowTokenRequest();
    }

    /// <summary>
    /// The edit assistant of an account whose token is missing
    /// (<c>authRequired</c>) or was refused (<c>authFailed</c>): the
    /// credentials page says so in its banner, with the token field flagged
    /// and focused (the sign-in banner's button;
    /// <see cref="WizardController.RequestPassword"/> for mail accounts).
    /// Ignored when adding. Call before <see cref="Start"/>.
    /// </summary>
    public void RequestToken(ErrorCode reason)
    {
        if (!IsEditing)
        {
            return;
        }
        tokenRequest = reason;
        if (started)
        {
            ShowTokenRequest();
        }
    }

    private void ShowTokenRequest()
    {
        if (tokenRequest is not { } reason)
        {
            return;
        }
        tokenRequest = null;
        var f = Jira.FailureOf(JiraWizardStep.Save, Jira.ClassOf(reason), Deployment, editing: true);
        if (f.Banner.Length > 0)
        {
            scope.Raise(BannerChanged, this, (JiraWizardPage.Credentials, (string?)f.Banner));
        }
        Flag([Field.Token]);
        scope.Raise(FocusRequested, this, Field.Token);
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

    /// <summary>The site field as typed. A changed address forgets the site found for the previous one.</summary>
    public void SetSite(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        if (raw == SiteInput)
        {
            return;
        }
        SiteInput = raw;
        scope.Raise(SiteChecked, this, Jira.CheckSiteInput(raw));
        Clear(Field.Site);
        scope.Raise(BannerChanged, this, (JiraWizardPage.Site, (string?)null));
        if (!IsEditing && Site is not null)
        {
            Site = null;
            scope.Raise(DetectedChanged, this, "");
        }
    }

    /// <summary>The credentials as typed. The login of an account being edited stays its own.</summary>
    public void SetCredentials(string login, string token)
    {
        ArgumentNullException.ThrowIfNull(login);
        ArgumentNullException.ThrowIfNull(token);
        var newLogin = IsEditing ? Login : login;
        if (newLogin == Login && token == Token)
        {
            return;
        }
        if (newLogin != Login)
        {
            Clear(Field.Login);
        }
        if (token != Token)
        {
            Clear(Field.Token);
        }
        Login = newLogin;
        Token = token;
        scope.Raise(BannerChanged, this, (JiraWizardPage.Credentials, (string?)null));
    }

    /// <summary>The account's address on the spaces page (Data Center).</summary>
    public void SetEmail(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s == Email)
        {
            return;
        }
        Email = s;
        Clear(Field.Email);
    }

    /// <summary>A space's check box.</summary>
    public void SetSpace(string id, bool on)
    {
        if (!Spaces.Any(s => s.Id == id) || on == selected.Contains(id))
        {
            return;
        }
        if (on)
        {
            selected.Add(id);
        }
        else
        {
            selected.Remove(id);
        }
        scope.Raise(SpacesProblemChanged, this, Jira.SpacesProblem(selected.Count));
        scope.Raise(BannerChanged, this, (JiraWizardPage.Spaces, (string?)null));
    }

    /// <summary>"Only Issues Involving Me".</summary>
    public void SetOnlyMine(bool on) => OnlyMine = on;

    /// <summary>
    /// The offline window's choice (an index of <see cref="Jira.OfflineChoices"/>).
    /// The estimates on the spaces page count the issues of the window, so a
    /// new window asks for them again.
    /// </summary>
    public void SetOfflineIndex(int i)
    {
        if (i < 0 || i >= Jira.OfflineChoices.Count)
        {
            return;
        }
        var days = Jira.OfflineChoices[i];
        if (days == OfflineDays)
        {
            return;
        }
        OfflineDays = days;
        if (!IsEditing && Pages[^1] == JiraWizardPage.Spaces && Spaces.Count > 0)
        {
            RefreshCounts();
        }
    }

    /// <summary>
    /// The visible page's button: look the site up, check the credentials and
    /// list the spaces (or save the token when editing), add the account.
    /// </summary>
    public void Next()
    {
        if (IsClosed || Busy)
        {
            return;
        }
        switch (Pages[^1])
        {
            case JiraWizardPage.Site:
                Detect();
                break;
            case JiraWizardPage.Credentials when IsEditing:
                Save();
                break;
            case JiraWizardPage.Credentials:
                LoadSpaces();
                break;
            default:
                Add();
                break;
        }
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

    /// <summary>
    /// The credentials page's "Create API Token…" (Jira Cloud only): the
    /// token page in the browser; a failure says why in a toast (GTK's
    /// openURL).
    /// </summary>
    public void OpenTokenHelp()
    {
        var url = CredentialPage.HelpUrl;
        if (url.Length == 0)
        {
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
                LogLaunchFailed(logger, error.GetType().Name);
                // TRANSLATORS: %s is a technical error message.
                scope.Raise(ToastRequested, this, L10n.T("The link could not be opened: %s", error.Message));
            });
    }

    // Navigation

    private void Push(JiraWizardPage page)
    {
        var i = IndexOf(page);
        Pages = i >= 0 ? [.. Pages.Take(i + 1)] : [.. Pages, page];
        scope.Raise(PagesChanged, this, Pages);
    }

    private void PopTo(JiraWizardPage page)
    {
        var i = IndexOf(page);
        if (i < 0 || i + 1 >= Pages.Count)
        {
            return;
        }
        Pages = [.. Pages.Take(i + 1)];
        scope.Raise(PagesChanged, this, Pages);
    }

    private int IndexOf(JiraWizardPage page)
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

    // Fields

    private void SetBusy(string? text)
    {
        if (Progress == text)
        {
            return;
        }
        Progress = text;
        scope.Raise(BusyChanged, this, text);
    }

    private void Flag(HashSet<Field> fields)
    {
        problems = fields;
        scope.Raise(ProblemsShown, this, (IReadOnlySet<Field>)new HashSet<Field>(fields));
    }

    private void Clear(Field field)
    {
        if (!problems.Remove(field))
        {
            return;
        }
        scope.Raise(ProblemsShown, this, (IReadOnlySet<Field>)new HashSet<Field>(problems));
    }

    // What the pages collected, for account.listSpaces and account.add.
    private JiraSetup Setup(AccountDetectSiteResult site) => new()
    {
        Site = site,
        Login = Login,
        Email = Email,
        Spaces = [.. Spaces.Where(s => selected.Contains(s.Id))],
        OnlyMine = OnlyMine,
        OfflineDays = OfflineDays,
    };

    // A failed step (Jira.FailureOf): back to its page with the banner, the
    // client's sentence for the error when the step has none; a refused or
    // missing token flags the token field.
    private void Fail(JiraWizardStep step, Exception error) => Fail(step, Jira.Classify(error), error);

    private void Fail(JiraWizardStep step, JiraErrorClass cls, Exception? error)
    {
        LogStepFailed(logger, step, cls);
        var f = Jira.FailureOf(step, cls, Deployment, IsEditing);
        PopTo(f.Page);
        scope.Raise(BannerChanged, this, (f.Page, (string?)(f.Banner.Length == 0 ? RpcErrorText.Text(f.What, error) : f.Banner)));
        switch (f.Page, cls)
        {
            case (JiraWizardPage.Site, JiraErrorClass.Invalid or JiraErrorClass.Server):
                Flag([Field.Site]);
                scope.Raise(FocusRequested, this, Field.Site);
                break;
            case (JiraWizardPage.Credentials, JiraErrorClass.AuthFailed or JiraErrorClass.AuthRequired):
                Flag([Field.Token]);
                scope.Raise(FocusRequested, this, Field.Token);
                break;
            default:
                break;
        }
    }

    // Site

    // account.detectSite for the typed address.
    private void Detect()
    {
        var check = Jira.CheckSiteInput(SiteInput);
        if (!check.Ok)
        {
            scope.Raise(SiteChecked, this, check);
            Flag([Field.Site]);
            scope.Raise(FocusRequested, this, Field.Site);
            return;
        }
        scope.Raise(BannerChanged, this, (JiraWizardPage.Site, (string?)null));
        var parameters = new AccountDetectSiteParams { Url = SiteInput.Trim() };
        SetBusy(Texts.LookingUp);
        var op = ++Op;
        scope.Perform(Client, API.AccountDetectSite, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(null);
            Detected(outcome);
        });
    }

    private void Detected(Outcome<AccountDetectSiteResult> outcome)
    {
        if (!outcome.TryGetValue(out var res, out var error))
        {
            Fail(JiraWizardStep.Detect, error!);
            return;
        }
        if (res.Kind != AccountKind.Jira || res.SiteUrl.Length == 0)
        {
            // Not a site this client can add: as the daemon's serverError
            // says, it is not Jira.
            Fail(JiraWizardStep.Detect, JiraErrorClass.Server, null);
            return;
        }
        LogSiteFound(logger, res.Deployment.Value ?? "");
        Site = res;
        scope.Raise(DetectedChanged, this, Jira.Detected(res));
        scope.Raise(CredentialPageChanged, this, CredentialPage);
        Push(JiraWizardPage.Credentials);
        scope.Raise(FocusRequested, this, res.Deployment == JiraDeployment.Cloud && Login.Length == 0 ? Field.Login : Field.Token);
    }

    // Credentials

    // Checks the typed credentials before a call: Jira Cloud needs the login
    // (an address), every site a token.
    private HashSet<Field> CredentialsProblems()
    {
        var p = new HashSet<Field>();
        if (Deployment == JiraDeployment.Cloud && Fields.ValidateEmail(Login) is null)
        {
            p.Add(Field.Login);
        }
        if (TrimmedToken.Length == 0)
        {
            p.Add(Field.Token);
        }
        return p;
    }

    // Flags what CredentialsProblems found; a missing token alone gets the
    // page's prompt as the banner.
    private void ShowCredentialsProblems(HashSet<Field> p)
    {
        Flag(p);
        if (p.Count == 1 && p.Contains(Field.Token))
        {
            scope.Raise(BannerChanged, this, (JiraWizardPage.Credentials, (string?)CredentialPage.TokenPrompt));
        }
        scope.Raise(FocusRequested, this, p.Contains(Field.Login) ? Field.Login : Field.Token);
    }

    // account.listSpaces with the typed credentials: the sign-in test and the
    // spaces page's list, with the estimates for the offline window.
    private void LoadSpaces()
    {
        if (Site is not { } site)
        {
            PopTo(JiraWizardPage.Site);
            return;
        }
        var p = CredentialsProblems();
        if (p.Count > 0)
        {
            ShowCredentialsProblems(p);
            return;
        }
        scope.Raise(BannerChanged, this, (JiraWizardPage.Credentials, (string?)null));
        ListSpaces(site, refresh: false);
    }

    // The estimates again, for a new offline window, without leaving the
    // spaces page; the old ones are gone meanwhile.
    private void RefreshCounts()
    {
        if (Site is not { } site)
        {
            return;
        }
        Spaces = [.. Spaces.Select(s => s with { Issues = -1 })];
        RaiseSpaces();
        ListSpaces(site, refresh: true);
    }

    private void ListSpaces(AccountDetectSiteResult site, bool refresh)
    {
        var parameters = new AccountListSpacesParams
        {
            Config = (Setup(site) with { Spaces = [] }).Config(),
            Credentials = new Credentials { Password = TrimmedToken },
            Counts = true,
        };
        SetBusy(Texts.LoadingSpaces);
        var op = ++Op;
        scope.Perform(Client, API.AccountListSpaces, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(null);
            if (outcome.TryGetValue(out var res, out var error))
            {
                SpacesLoaded(res, site, refresh);
            }
            else if (refresh)
            {
                // The page stays; only the estimates are missing.
                var cls = Jira.Classify(error);
                LogRecountFailed(logger, cls);
                var f = Jira.FailureOf(JiraWizardStep.Spaces, cls, site.Deployment, editing: false);
                scope.Raise(BannerChanged, this, (JiraWizardPage.Spaces, (string?)(f.Banner.Length == 0 ? RpcErrorText.Text(f.What, error) : f.Banner)));
            }
            else
            {
                Fail(JiraWizardStep.Spaces, error!);
            }
        });
    }

    private void SpacesLoaded(AccountListSpacesResult res, AccountDetectSiteResult site, bool refresh)
    {
        LogSpacesListed(logger, res.Spaces.Count);
        if (spacesSite != site.SiteUrl)
        {
            selected.Clear();
        }
        spacesSite = site.SiteUrl;
        User = res.User;
        Spaces = res.Spaces;
        selected.IntersectWith(Spaces.Select(s => s.Id));
        if (!refresh && selected.Count == 0 && Spaces.Count == 1)
        {
            // A single space is what the user means.
            selected.Add(Spaces[0].Id);
        }
        NeedsEmail = Jira.NeedsEmail(site.Deployment, res.User);
        if (site.Deployment != JiraDeployment.Cloud && !NeedsEmail)
        {
            Email = (res.User.Email ?? "").Trim();
        }
        RaiseSpaces();
        scope.Raise(SpacesProblemChanged, this, Jira.SpacesProblem(selected.Count));
        scope.Raise(EmailFieldChanged, this, (NeedsEmail, Email));
        if (!refresh)
        {
            scope.Raise(BannerChanged, this, (JiraWizardPage.Spaces, (string?)null));
            Push(JiraWizardPage.Spaces);
        }
    }

    private void RaiseSpaces() =>
        scope.Raise(SpacesChanged, this, (Jira.SpaceRows(Spaces), (IReadOnlySet<string>)new HashSet<string>(selected)));

    // Save

    // account.add from the spaces page.
    private void Add()
    {
        if (Site is not { } site)
        {
            PopTo(JiraWizardPage.Site);
            return;
        }
        var problem = Jira.SpacesProblem(selected.Count);
        if (problem.Length > 0)
        {
            scope.Raise(BannerChanged, this, (JiraWizardPage.Spaces, (string?)problem));
            return;
        }
        var s = Setup(site);
        if (NeedsEmail)
        {
            if (Fields.ValidateEmail(Email) is not { } address)
            {
                Flag([Field.Email]);
                scope.Raise(FocusRequested, this, Field.Email);
                return;
            }
            s = s with { Email = address };
        }
        scope.Raise(BannerChanged, this, (JiraWizardPage.Spaces, (string?)null));
        Store(s.Config(), Texts.Adding);
    }

    // account.update of the account being edited: its configuration as it
    // is, with the new token. The token is tried on the site first
    // (account.listSpaces with the account's configuration), so a refused
    // one never replaces the stored token.
    private void Save()
    {
        if (Editing is not { } editing)
        {
            return;
        }
        if (TrimmedToken.Length == 0)
        {
            ShowCredentialsProblems([Field.Token]);
            return;
        }
        scope.Raise(BannerChanged, this, (JiraWizardPage.Credentials, (string?)null));
        var parameters = new AccountListSpacesParams
        {
            AccountId = editing.Id,
            Config = editing.Config,
            Credentials = new Credentials { Password = TrimmedToken },
        };
        SetBusy(Texts.Saving);
        var op = ++Op;
        scope.Perform(Client, API.AccountListSpaces, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            if (outcome.Error is { } error)
            {
                SetBusy(null);
                Fail(JiraWizardStep.Save, error);
                return;
            }
            Store(editing.Config, Texts.Saving);
        });
    }

    private void Store(AccountConfig cfg, string step)
    {
        var creds = new Credentials { Password = TrimmedToken };
        var editing = Editing;
        SetBusy(step);
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
                Fail(JiraWizardStep.Save, error!);
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
                    await Client.CallAsync(API.AccountUpdate, new AccountUpdateParams { AccountId = editing.Id, Config = cfg, Credentials = creds }, RpcTimeouts.Save, ct).ConfigureAwait(false);
                    return editing.Id;
                },
                Stored);
        }
        else
        {
            scope.Perform(
                async ct => (await Client.CallAsync(API.AccountAdd, new AccountAddParams { Config = cfg, Credentials = creds }, RpcTimeouts.Save, ct).ConfigureAwait(false)).AccountId,
                Stored);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "jira assistant step {Step} failed: class {Class}")]
    private static partial void LogStepFailed(ILogger logger, JiraWizardStep step, JiraErrorClass @class);

    [LoggerMessage(Level = LogLevel.Information, Message = "jira spaces recount failed: class {Class}")]
    private static partial void LogRecountFailed(ILogger logger, JiraErrorClass @class);

    [LoggerMessage(Level = LogLevel.Information, Message = "jira site found: {Deployment}")]
    private static partial void LogSiteFound(ILogger logger, string deployment);

    [LoggerMessage(Level = LogLevel.Information, Message = "jira spaces listed: {Count}")]
    private static partial void LogSpacesListed(ILogger logger, int count);

    [LoggerMessage(Level = LogLevel.Information, Message = "jira account saved: {AccountId}, edit {Editing}")]
    private static partial void LogSaved(ILogger logger, string accountId, bool editing);

    [LoggerMessage(Level = LogLevel.Warning, Message = "open token page failed: {Failure}")]
    private static partial void LogLaunchFailed(ILogger logger, string failure);
}
