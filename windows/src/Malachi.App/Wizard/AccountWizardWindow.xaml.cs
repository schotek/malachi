// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/AccountWizardController.swift
// (present, dismiss, WizardSheetWindow's cancelOperation,
// WizardRootViewController: the header, showStack, applyHeader, the
// pages' transition by rank, wire); GTK: ui/internal/accountwizard/
// wizard.go (New, NewEdit, NewEditSignIn, RequestPassword, Present, the
// closed handler, toast). The flow, the texts and the calls are
// WizardController's; this window shows its pages and feeds the fields
// back.
//
// Windows (docs/windows-port.md §11.3, windows/README.md): an owned modal
// window over the window it was opened from (ModalDialog), 520×640
// effective pixels like the Adw.Dialog. Its title bar carries Back and the
// visible page's title (the header bar each GTK page has); its close button
// and Escape (and Ctrl+W, the tracked window's Close) cancel the wizard,
// which also cancels a sign-in the browser still has
// (WizardController.Close, as GTK's closed handler). A save that succeeded
// closes it after the caller's completion ran. The certificate confirmation
// is the shell's ContentDialog on this window. Nothing here logs a field:
// the password goes from its box to the controller only. The window's name
// stays the wizard's title (Add Account, Edit Account, Sign In) while the
// header shows the page's; Alt+Left and the mouse's back button go back a
// page, as in Adw.NavigationView.

using System;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media.Animation;
using WizardPage = Malachi.Core.Controllers.WizardController.WizardPage;

namespace Malachi.App.Wizard;

/// <summary>The "Add Account" / "Edit Account" / "Sign In" wizard.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The wizard's controller is closed with the window (Closed).")]
public sealed partial class AccountWizardWindow : Window
{
    /// <summary>account_wizard.blp content-width.</summary>
    public const int ContentWidth = 520;

    /// <summary>account_wizard.blp content-height.</summary>
    public const int ContentHeight = 640;

    private readonly WizardController wizard;
    private readonly Action<AccountId, AccountConfig>? done;
    private readonly ILogger logger;
    private readonly IdentityPage identity;
    private readonly ServersPage servers;
    private readonly SignInHintPage signInHint;
    private readonly OAuthPage oauth;
    private readonly TestingPage testing;
    private readonly ModalDialog modal;
    private WizardPage? visible;

    // A focus the controller asked for before the identity page was shown.
    private WizardController.IdentityField? focusRequest;

    // The page whose Loaded will place the focus (FocusPage).
    private UserControl? focusOnLoad;

    private AccountWizardWindow(
        AppState state, Account? editing, bool signIn, ErrorCode? requestPassword, Action<AccountId, AccountConfig>? done)
    {
        this.done = done;
        logger = state.Logs.CreateLogger<AccountWizardWindow>();
        InitializeComponent();
        wizard = new WizardController(state.Client, state.Launcher, editing, signIn, logger: state.Logs.CreateLogger<WizardController>());
        if (requestPassword is { } reason)
        {
            // Before the pages are wired, so that the wizard opens on the identity.
            wizard.RequestPassword(reason);
        }
        identity = new IdentityPage(wizard);
        servers = new ServersPage(wizard);
        signInHint = new SignInHintPage(wizard);
        oauth = new OAuthPage(wizard);
        testing = new TestingPage(wizard);

        Title = wizard.Title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        // The TitleBar names the window after its title when it comes up
        // and whenever the title changes: the window keeps the wizard's
        // (Alt+Tab, the UIA window name), the header shows the page's.
        Header.Loaded += (_, _) => Title = wizard.Title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);

        wizard.Owner = WindowPresenter.Handle(this);
        wizard.ConfirmTrust = p => state.Alerts.ConfirmTrustCertificateAsync(this, p.Heading, p.Body, p.Details, p.ConfirmLabel);
        wizard.PagesChanged += (_, pages) => ShowStack(pages);
        wizard.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName == nameof(WizardController.CanGoBack))
            {
                Header.IsBackButtonVisible = wizard.CanGoBack;
            }
        };
        wizard.ToastRequested += (_, text) => ToastsHost.Show(text);
        wizard.FocusRequested += (_, field) => FocusIdentity(field);
        wizard.Done += (_, saved) =>
        {
            try
            {
                this.done?.Invoke(saved.Id, saved.Config);
            }
            finally
            {
                Close();
            }
        };
        Pages.Navigated += (_, _) => DispatcherQueue.TryEnqueue(FocusPage);
        ModalDialog.AddBackKeys(Root, GoBack);
        Closed += (_, _) =>
        {
            if (modal.ReturnToOwner() is { } activated)
            {
                LogReturned(logger, activated);
            }
            wizard.Close();
        };
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The wizard's flow.</summary>
    public WizardController Wizard => wizard;

    /// <summary>
    /// Opens the wizard over <paramref name="owner"/> (the main window when
    /// null or hidden, shown first). <paramref name="editing"/> changes an
    /// existing account (it opens on the Servers page, NewEdit); with
    /// <paramref name="signIn"/> an account of the browser sign-in is only
    /// signed in again (NewEditSignIn); with
    /// <paramref name="requestPassword"/> a password account opens on the
    /// identity page asking for its password, the reason
    /// (<c>authRequired</c>, <c>authFailed</c>) saying why (RequestPassword).
    /// <paramref name="done"/> runs after account.add or account.update
    /// succeeded, before the wizard closes. Null when there is no window to
    /// open it over.
    /// </summary>
    public static AccountWizardWindow? Show(
        AppState state,
        Window? owner,
        Account? editing = null,
        bool signIn = false,
        ErrorCode? requestPassword = null,
        Action<AccountId, AccountConfig>? done = null)
    {
        ArgumentNullException.ThrowIfNull(state);
        if (owner is null || !owner.AppWindow.IsVisible)
        {
            state.ShowMainWindow();
            owner = state.MainWindow;
        }
        if (owner is null || state.IsStopping)
        {
            return null;
        }
        var w = new AccountWizardWindow(state, editing, signIn, requestPassword, done);
        w.wizard.Start();
        w.modal.Present(owner, ContentWidth, ContentHeight);
        LogPresented(w.logger, w.visible ?? WizardPage.Identity, w.wizard.IsEditing, w.wizard.SignInMode);
        return w;
    }

    // WizardRootViewController.showStack: the last page is visible; a page
    // later in the order slides in from the right, an earlier one from the
    // left, the first one without a transition.
    private void ShowStack(System.Collections.Generic.IReadOnlyList<WizardPage> pages)
    {
        if (pages.Count == 0)
        {
            return;
        }
        var top = pages[^1];
        Header.Title = PageTitle(top);
        Title = wizard.Title;
        Header.IsBackButtonVisible = wizard.CanGoBack;
        if (top == visible)
        {
            return;
        }
        NavigationTransitionInfo transition = visible is not { } from
            ? new SuppressNavigationTransitionInfo()
            : new SlideNavigationTransitionInfo
            {
                Effect = WizardController.Rank(top) > WizardController.Rank(from)
                    ? SlideNavigationTransitionEffect.FromRight
                    : SlideNavigationTransitionEffect.FromLeft,
            };
        visible = top;
        Pages.Navigate(typeof(WizardPageHost), PageView(top), transition);
    }

    private UserControl PageView(WizardPage page) => page switch
    {
        WizardPage.Servers => servers,
        WizardPage.Goa => signInHint,
        WizardPage.OAuth => oauth,
        WizardPage.Testing => testing,
        _ => identity,
    };

    // WizardRootViewController.title(for:): the identity page carries the
    // wizard's title (Add Account, Edit Account, Sign In).
    private string PageTitle(WizardPage page) => page switch
    {
        WizardPage.Servers => L10n.T("Server Settings"),
        WizardPage.Goa or WizardPage.OAuth => L10n.T("Sign In"),
        WizardPage.Testing => L10n.T("Connection Test"),
        _ => wizard.Title,
    };

    private void FocusIdentity(WizardController.IdentityField field)
    {
        if (visible == WizardPage.Identity && identity.IsLoaded)
        {
            identity.FocusField(field);
            return;
        }
        focusRequest = field;
    }

    // After a page came in: the identity page's requested field or its
    // address (focus-widget: email_row), else the first control of the
    // page, so the keyboard stays in the wizard. The Frame reports the
    // navigation before the page is in the tree, where it cannot take the
    // focus yet: then once it is loaded.
    private void FocusPage()
    {
        var view = PageView(visible ?? WizardPage.Identity);
        if (!view.IsLoaded)
        {
            if (!ReferenceEquals(focusOnLoad, view))
            {
                if (focusOnLoad is { } earlier)
                {
                    earlier.Loaded -= OnPageLoaded;
                }
                focusOnLoad = view;
                view.Loaded += OnPageLoaded;
            }
            return;
        }
        if (visible == WizardPage.Identity)
        {
            if (focusRequest is { } field)
            {
                focusRequest = null;
                identity.FocusField(field);
                return;
            }
            identity.InitialFocus.Focus(FocusState.Programmatic);
            return;
        }
        Control? target = visible switch
        {
            WizardPage.OAuth => oauth.InitialFocus,
            WizardPage.Testing => testing.InitialFocus,
            _ => null,
        };
        if (target is null && FocusManager.FindFirstFocusableElement(view) is Control first)
        {
            target = first;
        }
        target?.Focus(FocusState.Programmatic);
    }

    private void OnPageLoaded(object sender, RoutedEventArgs e)
    {
        if (focusOnLoad is { } page)
        {
            page.Loaded -= OnPageLoaded;
            focusOnLoad = null;
        }
        FocusPage();
    }

    private void OnBackRequested(TitleBar sender, object args) => wizard.Back();

    // Alt+Left and the mouse's back button, while there is a page to go
    // back to.
    private void GoBack()
    {
        if (wizard.CanGoBack)
        {
            wizard.Back();
        }
    }


    [LoggerMessage(Level = LogLevel.Information, Message = "account wizard opened on {Page}, edit {Editing}, sign-in {SignIn}")]
    private static partial void LogPresented(ILogger logger, WizardPage page, bool editing, bool signIn);

    [LoggerMessage(Level = LogLevel.Debug, Message = "account wizard closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
