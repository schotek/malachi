// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController: the state, the presentation, the lifecycle, the
// identity and Servers pages, the navigation, the linked accounts;
// saveErrorText, withoutMnemonic); GTK: ui/internal/accountwizard/wizard.go
// (New, NewEdit, NewEditSignIn, RequestPassword, askPassword, onNext,
// onTest, onEdit, saveErrorText) and linked.go (loadLinked, useLinked,
// startLinked, showGOAHint, onGOABrowser, onGOARecheck). The browser
// sign-in is WizardController.OAuth.cs, the pinned certificates
// WizardController.Trust.cs, the test and the save WizardController.Results.cs.
//
// The Swift callbacks are events of the same words: onPages is
// PagesChanged, onBusy BusyChanged, onIdentityProblems
// IdentityProblemsShown, onServerProblems ServerProblemsShown, onIdentity
// IdentityChanged, onApplyConfig ConfigApplied, onLinked LinkedChanged,
// onGOAHint GoaHintShown, onOAuth OAuthViewChanged, onOAuthStarting
// OAuthStartingChanged, onTesting TestingChanged, onFocus FocusRequested,
// onToast ToastRequested, onDone Done, onPins PinsChanged, and the async
// onConfirmTrust is the ConfirmTrust hook. onOpenURL is the injected
// ILauncher (docs/windows-port.md §10: the app opens the https address
// through its launcher), whose failure is a toast, as in GTK's launch. The
// state the callbacks report is observable as well, for bindings; Swift's
// oauthView is CurrentOAuthView (C# allows no member named like the nested
// type OAuthView). Every call goes through the controller's ControllerScope
// with the op counter of wizard.go; the GOA pages keep their logic as on
// macOS although the daemon reports no linked account on Windows (M20: the
// window shows neither the group nor the hint's buttons).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using LinkedRules = Malachi.Core.Wizard.Linked;
using OAuthTexts = Malachi.Core.Wizard.OAuth;
using TestOutcome = Malachi.Core.Wizard.Outcome;

namespace Malachi.Core.Controllers;

/// <summary>
/// The flow of the "Add Account" wizard (accountwizard's wizard.go,
/// linked.go, oauth.go and trust.go with the widgets replaced by events):
/// the page stack (<see cref="Pages"/>, mirroring <c>AdwNavigationView</c>),
/// the identity and server fields the page reports into it, the RPC calls
/// with their <c>closed</c>/<c>op</c> guards, and every user-facing string
/// of the flow; the window only shows what the events deliver and feeds the
/// fields back.
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>.
/// </remarks>
public sealed partial class WizardController : ObservableObject, IDisposable
{
    private readonly ControllerScope scope;
    private readonly ILauncher launcher;
    private readonly TimeProvider time;
    private readonly ILogger logger;

    private ResultsView? lastResults;

    // The endpoint configuration each pin was set for (certtrust pinned):
    // the fields carry its pin while their host and port match
    // (CertTrust.KeepPin); Forget drops it.
    private readonly Dictionary<Endpoint, ServerConfig> pinned = [];

    // What the last results offer to trust, per endpoint: the tested
    // configuration and the problem with its certificate.
    private readonly Dictionary<Endpoint, (ServerConfig Server, CertTrust.Problem Problem)> trustOffers = [];

    // The last test of a browser sign-in account found a sign-in problem
    // (the results offer "Sign In Again").
    private bool lastSignInProblem;

    // The GNOME Online Accounts hint's discovery, for "Use the Browser
    // Instead".
    private Discovery? goaHint;

    // The app-password account chosen instead of the browser sign-in
    // (oauth.go appPassword): Next continues with it while the address
    // stays the same.
    private AccountConfig? appPassword;

    private bool identityProblemsShown;

    // RequestPassword's reason, asked by Start.
    private ErrorCode? passwordRequest;
    private bool started;

    /// <summary>A wizard over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="launcher">Opens the provider's sign-in page in the browser.</param>
    /// <param name="editing">
    /// The account being changed, null when adding one. It prefills the
    /// pages, skips discovery, keeps the stored password on an empty one and
    /// saves with <c>account.update</c>; the wizard then opens on the Servers
    /// page with Back leading to the identity (NewEdit).
    /// </param>
    /// <param name="signIn">
    /// With <paramref name="editing"/> an account of the browser sign-in:
    /// open straight on the sign-in and end with <c>account.update</c>
    /// (NewEditSignIn). Ignored for any other account.
    /// </param>
    /// <param name="time">The clock of the certificate's validity in the trust prompt.</param>
    /// <param name="logger">Receives methods, codes and sources, never an address, a password or a session.</param>
    /// <param name="pending">Counts the wizard's background work; one of its own when null.</param>
    public WizardController(
        RpcClient client,
        ILauncher launcher,
        Account? editing = null,
        bool signIn = false,
        TimeProvider? time = null,
        ILogger<WizardController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(launcher);
        Client = client;
        this.launcher = launcher;
        Editing = editing;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        Identity = new Identity();
        AccountName = "";
        Imap = new ServerFields { Port = 993, Security = Security.Tls };
        Smtp = new ServerFields { Port = 587, Security = Security.Starttls };
        Linked = [];
        LastOutcome = TestOutcome.Failed;
        Pages = [WizardPage.Identity];
        PinnedFingerprints = ("", "");
        if (editing is null)
        {
            return;
        }
        var kind = Provider.SignInKindOf(editing.Config);
        SignInMode = signIn && kind == SignInKind.OAuth;
        Identity = new Identity { DisplayName = editing.Config.DisplayName ?? "", Email = editing.Config.Email, Password = "" };
        if (kind != SignInKind.Password)
        {
            // The address and the sign-in belong to GNOME Online Accounts
            // or to the browser sign-in; only the name can change here, and
            // the test re-checks the sign-in.
            LinkedCfg = editing.Config;
            if (SignInMode)
            {
                var provider = Provider.AccountProvider(editing.Config);
                OAuth = new OAuthState { Provider = provider, Name = OAuthTexts.OAuthProviderLabel(provider, editing.Config.Email) };
                CurrentOAuthView = PromptView(OAuth.Name);
                Pages = [WizardPage.OAuth];
            }
            return;
        }
        SetFields(editing.Config);
        Pages = [WizardPage.Identity, WizardPage.Servers];
    }

    /// <summary>The page stack changed; the last page is the visible one (Swift <c>onPages</c>).</summary>
    public event EventHandler<IReadOnlyList<WizardPage>>? PagesChanged;

    /// <summary>An RPC started or finished; the editable pages follow it (Swift <c>onBusy</c>).</summary>
    public event EventHandler<bool>? BusyChanged;

    /// <summary>Which identity fields to flag, and the banner to show, null to hide it (Swift <c>onIdentityProblems</c>).</summary>
    public event EventHandler<(IdentityProblems Problems, string? Banner)>? IdentityProblemsShown;

    /// <summary>Which server rows to flag (Swift <c>onServerProblems</c>).</summary>
    public event EventHandler<ServerProblems>? ServerProblemsShown;

    /// <summary>The identity fields were set from here (a linked account, a sign-in); show them (Swift <c>onIdentity</c>).</summary>
    public event EventHandler<Identity>? IdentityChanged;

    /// <summary>Fill the Servers page without triggering the port logic (Swift <c>onApplyConfig</c>).</summary>
    public event EventHandler<AccountConfig>? ConfigApplied;

    /// <summary>
    /// The accounts signed in elsewhere on the desktop (Swift <c>onLinked</c>):
    /// always delivered, empty on Windows; the window keeps the group hidden.
    /// </summary>
    public event EventHandler<IReadOnlyList<LinkedAccount>>? LinkedChanged;

    /// <summary>
    /// The text of the sign-in hint page, and whether it offers the browser
    /// sign-in instead, "Use the Browser Instead" (Swift <c>onGOAHint</c>).
    /// </summary>
    public event EventHandler<(string Text, bool Browser)>? GoaHintShown;

    /// <summary>The browser sign-in page's content (Swift <c>onOAuth</c>).</summary>
    public event EventHandler<OAuthView>? OAuthViewChanged;

    /// <summary>
    /// <c>account.oauthStart</c> runs (true) or answered (false): only the
    /// prompt's Sign In button waits for it (oauth.go sets
    /// <c>oauth_sign_in</c> insensitive); the other pages and Back stay
    /// usable (Swift <c>onOAuthStarting</c>).
    /// </summary>
    public event EventHandler<bool>? OAuthStartingChanged;

    /// <summary>The testing page's content (Swift <c>onTesting</c>).</summary>
    public event EventHandler<TestingView>? TestingChanged;

    /// <summary>Move the keyboard focus to an identity field (Swift <c>onFocus</c>).</summary>
    public event EventHandler<IdentityField>? FocusRequested;

    /// <summary>Show a toast inside the wizard (Swift <c>onToast</c>).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The account was stored; the window closes the wizard (Swift <c>onDone</c>).</summary>
    public event EventHandler<(AccountId Id, AccountConfig Config)>? Done;

    /// <summary>
    /// The certificates pinned to the endpoints changed: their fingerprints
    /// as shown (<see cref="CertTrust.FormatFingerprint"/>), "" for none
    /// (Swift <c>onPins</c>). The Servers page shows a "Pinned Certificate"
    /// row per pin.
    /// </summary>
    public event EventHandler<(string Imap, string Smtp)>? PinsChanged;

    /// <summary>
    /// Asks "Trust This Certificate?" and completes with true when the user
    /// confirmed (Swift <c>onConfirmTrust</c>). Without it nothing is ever
    /// pinned.
    /// </summary>
    public Func<TrustPrompt, Task<bool>>? ConfirmTrust { get; set; }

    /// <summary>The window the browser's own dialogs belong to (the wizard's), 0 for none.</summary>
    public nint Owner { get; set; }

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The account being changed; null when adding a new one.</summary>
    public Account? Editing { get; }

    /// <summary>
    /// The wizard only signs <see cref="Editing"/> in again
    /// (NewEditSignIn): it opens on the browser sign-in and ends with
    /// <c>account.update</c>.
    /// </summary>
    public bool SignInMode { get; }

    /// <summary>The identity rows as last typed or set.</summary>
    [ObservableProperty]
    public partial Identity Identity { get; private set; }

    /// <summary>The Servers page's account name.</summary>
    [ObservableProperty]
    public partial string AccountName { get; private set; }

    /// <summary>The IMAP rows, with the pin trusted for their host and port.</summary>
    [ObservableProperty]
    public partial ServerFields Imap { get; private set; }

    /// <summary>The SMTP rows, with the pin trusted for their host and port.</summary>
    [ObservableProperty]
    public partial ServerFields Smtp { get; private set; }

    /// <summary>The accounts signed in elsewhere on the desktop (empty on Windows).</summary>
    [ObservableProperty]
    public partial IReadOnlyList<LinkedAccount> Linked { get; private set; }

    /// <summary>
    /// Set for an account whose sign-in lives in GNOME Online Accounts or
    /// comes from the browser sign-in: the daemon built it, there is no
    /// password and the servers are not the user's to edit. Null is the
    /// password path.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(EmailEditable), nameof(PasswordVisible), nameof(NextLabel))]
    public partial AccountConfig? LinkedCfg { get; private set; }

    /// <summary>The browser sign-in, from the moment its prompt is shown.</summary>
    [ObservableProperty]
    public partial OAuthState? OAuth { get; private set; }

    /// <summary>What the browser sign-in page shows now (Swift <c>oauthView</c>).</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanGoBack))]
    public partial OAuthView? CurrentOAuthView { get; private set; }

    /// <summary>The outcome of the last connection test.</summary>
    [ObservableProperty]
    public partial TestOutcome LastOutcome { get; private set; }

    /// <summary>The page stack; the last page is the visible one.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanGoBack))]
    public partial IReadOnlyList<WizardPage> Pages { get; private set; }

    /// <summary>An RPC of the pages runs (discovery).</summary>
    [ObservableProperty]
    public partial bool Busy { get; private set; }

    /// <summary><c>account.oauthStart</c> is under way (<see cref="OAuthStartingChanged"/>).</summary>
    [ObservableProperty]
    public partial bool OAuthStarting { get; private set; }

    /// <summary>The fingerprints pinned now, as shown ("" for none; <see cref="PinsChanged"/>).</summary>
    [ObservableProperty]
    public partial (string Imap, string Smtp) PinnedFingerprints { get; private set; }

    /// <summary>The wizard went away: late replies are dropped (Swift <c>closed</c>).</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Bumped per RPC so stale replies bail out (wizard.go <c>op</c>).</summary>
    public int Op { get; private set; }

    /// <summary>An existing account is being changed.</summary>
    public bool IsEditing => Editing is not null;

    /// <summary>The e-mail row is read-only for a linked account being edited.</summary>
    public bool EmailEditable => !(IsEditing && LinkedCfg is not null);

    /// <summary>The password row is hidden for a linked account being edited.</summary>
    public bool PasswordVisible => !(IsEditing && LinkedCfg is not null);

    /// <summary>The dialog and identity page title ("Sign In" for NewEditSignIn).</summary>
    public string Title => SignInMode ? L10n.T("Sign In") : IsEditing ? L10n.T("Edit Account") : L10n.T("Add Account");

    /// <summary>The password row's title.</summary>
    public string PasswordTitle => IsEditing ? L10n.T("New Password (leave empty to keep)") : L10n.T("Password");

    /// <summary>The identity page's button.</summary>
    public string NextLabel => WithoutMnemonic(IsEditing && LinkedCfg is not null ? L10n.T("_Test Connection") : L10n.T("_Next"));

    /// <summary>The results page's Add Account / Save.</summary>
    public string AddLabel => WithoutMnemonic(IsEditing ? L10n.T("_Save") : L10n.T("_Add Account"));

    /// <summary>The results page's Add Anyway / Save Anyway.</summary>
    public string AddAnywayLabel => WithoutMnemonic(IsEditing ? L10n.T("Save _Anyway") : L10n.T("Add _Anyway"));

    /// <summary>
    /// The results page's first button: Sign In Again once the browser
    /// sign-in was refused, Edit Servers otherwise.
    /// </summary>
    public string EditLabel => WithoutMnemonic(lastSignInProblem ? L10n.T("_Sign In Again") : L10n.T("_Edit Servers"));

    /// <summary>
    /// The header's Back is offered: not on the first page, and not while the
    /// browser sign-in waits (<c>can-pop: false</c>).
    /// </summary>
    public bool CanGoBack => Pages.Count > 1 && !(Pages[^1] == WizardPage.OAuth && CurrentOAuthView is OAuthView.Waiting);

    /// <summary>The progress title while the account is stored.</summary>
    public string ProgressTitle => IsEditing ? L10n.T("Saving Account…") : L10n.T("Adding Account…");

    // The account under test signs in through the browser.
    private bool SignsInWithBrowser => LinkedCfg is { } cfg && Provider.SignInKindOf(cfg) == SignInKind.OAuth;

    /// <summary>
    /// The toast for a failed <c>account.add</c> / <c>account.update</c>
    /// (wizard.go <c>saveErrorText</c>): a conflict is the one code with its
    /// own sentence.
    /// </summary>
    public static string SaveErrorText(Exception? error, bool editing)
    {
        if (RpcErrorText.DaemonError(error) is { } e && e.Code == ErrorCode.Conflict)
        {
            return L10n.T("An account with this e-mail address already exists");
        }
        return editing ? RpcErrorText.Text(L10n.T("Saving the account"), error) : RpcErrorText.Text(L10n.T("Adding the account"), error);
    }

    /// <summary>A GTK label with its mnemonic marker removed: <c>_Next</c> → <c>Next</c>, <c>__</c> → <c>_</c>.</summary>
    public static string WithoutMnemonic(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var output = new System.Text.StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '_')
            {
                if (i + 1 < s.Length)
                {
                    output.Append(s[++i]);
                }
                continue;
            }
            output.Append(s[i]);
        }
        return output.ToString();
    }

    // Lifecycle

    /// <summary>
    /// Delivers the initial state to the events and asks the daemon for
    /// linked accounts. Call once, after subscribing.
    /// </summary>
    public void Start()
    {
        scope.VerifyAccess();
        IdentityChanged?.Invoke(this, Identity);
        if (Editing is { } a && LinkedCfg is null)
        {
            ConfigApplied?.Invoke(this, a.Config);
        }
        PinsChanged?.Invoke(this, PinnedFingerprints);
        if (CurrentOAuthView is { } view)
        {
            OAuthViewChanged?.Invoke(this, view);
        }
        PagesChanged?.Invoke(this, Pages);
        started = true;
        ShowPasswordRequest();
        LoadLinked(null);
    }

    /// <summary>
    /// wizard.go <c>RequestPassword</c>: the edit wizard of a password
    /// account whose password is missing (<c>authRequired</c>) or was refused
    /// (<c>authFailed</c>, and any other reason) opens on the identity page
    /// with the password row flagged and focused and the banner saying why;
    /// the banner goes as the password is typed. Ignored for an account the
    /// daemon signs in (GNOME Online Accounts, the browser sign-in) and when
    /// adding one. Call before <see cref="Start"/>.
    /// </summary>
    public void RequestPassword(ErrorCode reason)
    {
        scope.VerifyAccess();
        if (Editing is null || LinkedCfg is not null || SignInMode)
        {
            return;
        }
        Pages = [WizardPage.Identity];
        passwordRequest = reason;
        if (started)
        {
            PagesChanged?.Invoke(this, Pages);
            ShowPasswordRequest();
        }
    }

    /// <summary>
    /// The wizard went away: a sign-in under way is cancelled and every late
    /// reply is dropped from now on (<c>ConnectClosed</c>).
    /// </summary>
    public void Close()
    {
        scope.VerifyAccess();
        CancelSession();
        scope.Close();
    }

    /// <summary>Closes the wizard.</summary>
    public void Dispose() => Close();

    // Inputs from the window

    /// <summary>
    /// The identity rows as typed. A change of the address or password clears
    /// the flagged fields and the banner, as the GTK rows do.
    /// </summary>
    public void SetIdentity(string name, string email, string password)
    {
        scope.VerifyAccess();
        var changed = email != Identity.Email || password != Identity.Password;
        Identity = new Identity { DisplayName = name, Email = email, Password = password };
        if (changed && identityProblemsShown)
        {
            identityProblemsShown = false;
            IdentityProblemsShown?.Invoke(this, (new IdentityProblems(), null));
        }
    }

    /// <summary>
    /// The Servers page rows as typed. The rows know nothing of pins: each
    /// endpoint gets the pin trusted for its host and port, if any.
    /// </summary>
    public void SetServers(string name, ServerFields imap, ServerFields smtp)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(imap);
        ArgumentNullException.ThrowIfNull(smtp);
        AccountName = name ?? "";
        Imap = imap;
        Smtp = smtp;
        ApplyPins();
    }

    /// <summary>
    /// The identity page's Next button (wizard.go <c>onNext</c>): validate,
    /// then ask the daemon for server settings. A Google or Microsoft 365
    /// address goes to the connection test (signed in through GNOME Online
    /// Accounts), to the sign-in hint or to the browser sign-in; an IMAP hit
    /// goes to the connection test; a miss opens the Servers page with
    /// guessed defaults. The password is asked for only once the account
    /// turns out to need one. An address the app password was chosen for
    /// skips discovery.
    /// </summary>
    public void Next()
    {
        scope.VerifyAccess();
        var id = Identity;
        var p = Fields.ValidateIdentity(id, passwordRequired: false);
        if (p.Any)
        {
            ShowIdentityProblems(p, null);
            FocusRequested?.Invoke(this, IdentityField.Email);
            return;
        }
        id = id with { Email = Fields.ValidateEmail(id.Email) ?? id.Email };
        if (Editing is not null)
        {
            if (LinkedCfg is not null)
            {
                Replace([WizardPage.Identity, WizardPage.Testing]);
                RunTest();
                return;
            }
            // The servers are known; only the identity may have changed.
            Push(WizardPage.Servers);
            return;
        }
        // A new account's way is decided afresh: a sign-in of an earlier
        // attempt is dropped.
        LinkedCfg = null;
        CancelSession();
        if (appPassword is { } alt)
        {
            // Swift's caseInsensitiveCompare; the two agree on addresses.
            if (string.Equals(alt.Email, id.Email, StringComparison.OrdinalIgnoreCase))
            {
                if (RequirePassword(L10n.T("Enter the app password for this account")))
                {
                    ContinueWithAppPassword();
                }
                return;
            }
            appPassword = null;
        }
        if (LinkedRules.LinkedMatch(Linked, id.Email) is { Configured: false } l)
        {
            UseLinked(l);
            return;
        }
        SetBusy(true);
        var op = ++Op;
        scope.Perform(Client, API.AccountDiscover, new AccountDiscoverParams { Email = id.Email }, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(false);
            Discovered(outcome, id);
        });
    }

    /// <summary>The Servers page's Test Connection button (wizard.go <c>onTest</c>).</summary>
    public void TestServers()
    {
        scope.VerifyAccess();
        var p = Fields.ValidateServers(Imap, Smtp);
        if (p.Any)
        {
            ServerProblemsShown?.Invoke(this, p);
            return;
        }
        if (Pages[^1] != WizardPage.Testing)
        {
            Push(WizardPage.Testing);
        }
        RunTest();
    }

    /// <summary>
    /// The results page's first button (wizard.go <c>onEdit</c>): back to the
    /// Servers page or, once the browser sign-in was refused, to its prompt
    /// for the same account (oauth.go <c>onSignInAgain</c>).
    /// </summary>
    public void Edit()
    {
        scope.VerifyAccess();
        if (lastSignInProblem)
        {
            ShowOAuthPrompt(LinkedCfg is { } cfg ? Provider.AccountProvider(cfg) : null, OAuth?.Config, OAuth?.PasswordAlt);
            return;
        }
        PopTo(WizardPage.Servers);
    }

    /// <summary>The results page's Retry button.</summary>
    public void Retry()
    {
        scope.VerifyAccess();
        RunTest();
    }

    /// <summary>The results page's Add Account / Save button.</summary>
    public void Add()
    {
        scope.VerifyAccess();
        Save();
    }

    /// <summary>The results page's Add Anyway / Save Anyway button.</summary>
    public void AddAnyway()
    {
        scope.VerifyAccess();
        Save();
    }

    /// <summary>The header's Back button: pops the visible page (not while the browser sign-in waits).</summary>
    public void Back()
    {
        scope.VerifyAccess();
        if (!CanGoBack)
        {
            return;
        }
        Pages = [.. Pages.Take(Pages.Count - 1)];
        PagesChanged?.Invoke(this, Pages);
    }

    /// <summary>
    /// Fills the identity from a linked account and goes straight to the
    /// connection test with the account the daemon built for it: there is
    /// nothing to type and nothing to discover (linked.go <c>useLinked</c>).
    /// </summary>
    public void UseLinked(LinkedAccount l)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(l);
        if (l.Config is not { } cfg)
        {
            // A daemon older than the config field; the sign-in is there, but
            // not the account to add it as.
            ToastRequested?.Invoke(this, L10n.T("The mail service does not describe this account; update it and try again"));
            return;
        }
        Identity = Identity with { Email = l.Email };
        if (string.IsNullOrWhiteSpace(Identity.DisplayName))
        {
            Identity = Identity with { DisplayName = l.Name ?? "" };
        }
        IdentityChanged?.Invoke(this, Identity);
        StartLinked(cfg);
    }

    /// <summary>
    /// Asks the daemon again and continues when the typed address has
    /// appeared among the linked accounts (linked.go <c>onGOARecheck</c>).
    /// </summary>
    public void RecheckLinked()
    {
        scope.VerifyAccess();
        var email = Fields.ValidateEmail(Identity.Email) ?? "";
        LoadLinked(linked =>
        {
            if (LinkedRules.LinkedMatch(linked, email) is { Configured: false } l)
            {
                UseLinked(l);
                return;
            }
            ToastRequested?.Invoke(this, L10n.T("This address is not signed in yet"));
        });
    }

    /// <summary>
    /// The hint page's "Use the Browser Instead" (linked.go
    /// <c>onGOABrowser</c>): the daemon's own sign-in for the address.
    /// </summary>
    public void UseBrowser()
    {
        scope.VerifyAccess();
        if (goaHint is not { OAuthAlt: { } alt } hint)
        {
            return;
        }
        ShowOAuthPrompt(Provider.AccountProvider(alt), alt, hint.PasswordAlt);
    }

    private void ShowPasswordRequest()
    {
        if (passwordRequest is not { } reason)
        {
            return;
        }
        passwordRequest = null;
        AskPassword(reason);
    }

    // wizard.go askPassword: flags the identity page's password row as the
    // thing to fix, says why in its banner (passwordBannerText) and focuses
    // it; typing clears both (SetIdentity).
    private void AskPassword(ErrorCode reason)
    {
        ShowIdentityProblems(new IdentityProblems { Password = true }, Results.PasswordBannerText(reason));
        FocusRequested?.Invoke(this, IdentityField.Password);
    }

    private void Discovered(Outcome<AccountDiscoverResult> outcome, Identity id)
    {
        var res = outcome.Value;
        var d = SignIn.ClassifyDiscovery(res, outcome.Error);
        switch (d.Path)
        {
            case SignIn.Path.Goa:
                // Signed in through GNOME Online Accounts: the daemon's
                // account is complete.
                LogDiscovered(logger, res?.Source.Value ?? "");
                if (d.Config is { } cfg)
                {
                    StartLinked(cfg);
                }
                return;
            case SignIn.Path.GoaHint:
                // The sign-in must happen in GNOME Online Accounts first, or
                // in the browser.
                LogDiscovered(logger, res?.Source.Value ?? "");
                ShowGoaHint(res?.ProviderName, d);
                return;
            case SignIn.Path.OAuth:
                LogDiscoveredBrowser(logger, res?.Source.Value ?? "");
                ShowOAuthPrompt(d.Provider, d.Config, d.PasswordAlt);
                return;
        }
        if (!RequirePassword())
        {
            return;
        }
        if (res is { Config: { } config })
        {
            LogDiscovered(logger, res.Source.Value);
            ApplyConfig(Fields.MergeIdentity(config, id));
            Replace([WizardPage.Identity, WizardPage.Servers, WizardPage.Testing]);
            RunTest();
            return;
        }
        if (res is not null)
        {
            LogNothingFound(logger, res.Source.Value);
        }
        else
        {
            LogCallFailure(LogLevel.Debug, API.AccountDiscover.Name, outcome.Error);
        }
        ApplyConfig(Fields.MergeIdentity(Fields.GuessConfig(id.Email), id));
        Push(WizardPage.Servers);
    }

    // Navigation (AdwNavigationView over tags)

    private void Replace(IReadOnlyList<WizardPage> stack)
    {
        Pages = stack;
        PagesChanged?.Invoke(this, Pages);
    }

    private void Push(WizardPage page)
    {
        if (Pages[^1] == page)
        {
            return;
        }
        var i = IndexOf(page);
        // Already below the top: a navigation view would refuse the push.
        Pages = i >= 0 ? [.. Pages.Take(i + 1)] : [.. Pages, page];
        PagesChanged?.Invoke(this, Pages);
    }

    private void PopTo(WizardPage page)
    {
        var i = IndexOf(page);
        if (i < 0)
        {
            return;
        }
        Pages = [.. Pages.Take(i + 1)];
        PagesChanged?.Invoke(this, Pages);
    }

    private int IndexOf(WizardPage page)
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

    private void SetBusy(bool on)
    {
        Busy = on;
        BusyChanged?.Invoke(this, on);
    }

    private void SetOAuthStarting(bool on)
    {
        if (OAuthStarting == on)
        {
            return;
        }
        OAuthStarting = on;
        OAuthStartingChanged?.Invoke(this, on);
    }

    private void ShowIdentityProblems(IdentityProblems p, string? banner)
    {
        identityProblemsShown = true;
        IdentityProblemsShown?.Invoke(this, (p, banner));
    }

    private void SetLastSignInProblem(bool on)
    {
        if (lastSignInProblem == on)
        {
            return;
        }
        lastSignInProblem = on;
        OnPropertyChanged(nameof(EditLabel));
    }

    // Fills the Servers page without triggering the port logic.
    private void ApplyConfig(AccountConfig cfg)
    {
        SetFields(cfg);
        ConfigApplied?.Invoke(this, cfg);
    }

    private AccountConfig AssembleConfig() =>
        LinkedCfg is { } linked ? LinkedRules.WithIdentity(linked, Identity) : Fields.BuildConfig(Identity, AccountName, Imap, Smtp);

    // Flags the empty password row once discovery has shown the account
    // needs one (a Microsoft 365 account does not); the banner defaults to
    // "Enter the password for this account".
    private bool RequirePassword(string? banner = null)
    {
        if (Editing is not null || Identity.Password.Length > 0)
        {
            return true;
        }
        ShowIdentityProblems(new IdentityProblems { Password = true }, banner ?? L10n.T("Enter the password for this account"));
        FocusRequested?.Invoke(this, IdentityField.Password);
        return false;
    }

    // The secrets for account.test / add / update (oauth.go credentials): the
    // completed browser session, nothing for any other account the daemon
    // built (GNOME Online Accounts, or a browser sign-in the daemon holds
    // already), the typed password otherwise.
    private Credentials Credentials()
    {
        if (LinkedCfg is not { } linked)
        {
            return Fields.CredentialsFor(Identity);
        }
        if (Provider.SignInKindOf(linked) == SignInKind.OAuth && OAuth is { Complete: true, SessionId: { } session })
        {
            return new Credentials { OAuthSession = session };
        }
        return new Credentials();
    }

    // Linked accounts

    // Asks the daemon for accounts signed in elsewhere on the desktop; then
    // runs then with the list (empty on failure: the list is a convenience).
    private void LoadLinked(Action<IReadOnlyList<LinkedAccount>>? then)
    {
        var op = ++Op;
        scope.Perform(Client, API.AccountLinked, new EmptyParams(), outcome =>
        {
            IReadOnlyList<LinkedAccount> accounts = [];
            if (outcome.TryGetValue(out var res, out var error))
            {
                accounts = res.Accounts;
            }
            else
            {
                LogCallFailure(LogLevel.Debug, API.AccountLinked.Name, error);
            }
            if (op != Op)
            {
                return;
            }
            Linked = accounts;
            LinkedChanged?.Invoke(this, accounts);
            then?.Invoke(accounts);
        });
    }

    // Switches to an account whose sign-in belongs to GNOME Online Accounts
    // (no password, no servers to edit) and tests it.
    private void StartLinked(AccountConfig config)
    {
        CancelSession();
        appPassword = null;
        LinkedCfg = LinkedRules.WithIdentity(config, Identity);
        Identity = Identity with { Password = "" };
        IdentityChanged?.Invoke(this, Identity);
        Replace([WizardPage.Identity, WizardPage.Testing]);
        RunTest();
    }

    // Opens the "sign in through GNOME Settings" page for an address of the
    // discovered provider that the desktop is not signed in to yet, with the
    // browser sign-in as the way around when the daemon offers it. The
    // daemon's untrusted providerName names only a provider this client has
    // no name of.
    private void ShowGoaHint(string? untrusted, Discovery discovery)
    {
        goaHint = discovery;
        var name = Provider.ProviderName(discovery.Provider);
        if (name.Length == 0)
        {
            name = untrusted ?? "";
        }
        GoaHintShown?.Invoke(this, (LinkedRules.GoaHintText(name), discovery.OAuthAlt is not null));
        Push(WizardPage.Goa);
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "account discovered: {Source}")]
    private static partial void LogDiscovered(ILogger logger, string source);

    [LoggerMessage(Level = LogLevel.Information, Message = "account discovered: {Source}, browser sign-in")]
    private static partial void LogDiscoveredBrowser(ILogger logger, string source);

    [LoggerMessage(Level = LogLevel.Debug, Message = "account.discover: nothing found ({Source})")]
    private static partial void LogNothingFound(ILogger logger, string source);

    [LoggerMessage(Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogCallFailed(ILogger logger, LogLevel level, string method, RpcErrorText.FailureKind kind, int code);

    // A failed call as the log may show it: the method, what kind of
    // failure and the daemon's code, never a message that could carry an
    // address.
    private void LogCallFailure(LogLevel level, string method, Exception? error)
    {
        if (!logger.IsEnabled(level))
        {
            return;
        }
        var kind = RpcErrorText.Classify(error).Kind;
        var code = RpcErrorText.DaemonError(error)?.Code.Value ?? 0;
        LogCallFailed(logger, level, method, kind, code);
    }
}
