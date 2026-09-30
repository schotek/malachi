// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/Jira/JiraWizardWindowController.swift
// (present, dismiss, JiraWizardRootViewController: wire, showStack,
// applyHeader); GTK: ui/internal/accountwizard/jira.go (NewJira,
// NewJiraEdit, Present, wire, showPages, showBusy, showBanner, showProblems,
// focus, openURL). The flow, its texts and its calls are Core's
// JiraWizardController; this window shows its pages and feeds the fields
// back. Adding opens on the site; editing (the token) on the credentials,
// with the reason a token is asked for in its banner and the token field
// flagged and focused.
//
// Windows (docs/windows-port.md §11.3): the account wizard's window
// (AccountWizardWindow), an owned modal window over the window it was
// opened from (ModalDialog), 520×640 effective pixels like the Adw.Dialog.
// Its title bar carries Back and the visible page's title; its close
// button and Escape (and Ctrl+W) cancel the assistant, whose late replies
// are dropped from then on (JiraWizardController.Close). A save that
// succeeded closes it after the caller's completion ran. Alt+Left and the
// mouse's back button go back a page. Nothing here logs a field: the token
// goes from its box to the controller only.

using System;
using System.Collections.Generic;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media.Animation;
using Field = Malachi.Core.Controllers.JiraWizardController.Field;

namespace Malachi.App.Wizard;

/// <summary>The "Add Jira Account" / "Edit Account" assistant.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The assistant's controller is closed with the window (Closed).")]
public sealed partial class JiraWizardWindow : Window
{
    /// <summary>jira_wizard.blp content-width.</summary>
    public const int ContentWidth = 520;

    /// <summary>jira_wizard.blp content-height.</summary>
    public const int ContentHeight = 640;

    private readonly JiraWizardController wizard;
    private readonly Action<AccountId, AccountConfig>? done;
    private readonly ILogger logger;
    private readonly ModalDialog modal;
    private readonly JiraSitePage site;
    private readonly JiraCredentialsPage credentials;
    private readonly JiraSpacesPage spaces;
    private JiraWizardPage? visible;

    // A focus the controller asked for before its page was shown.
    private Field? focusRequest;

    // The page whose Loaded will place the focus (FocusPage).
    private UserControl? focusOnLoad;

    private JiraWizardWindow(AppState state, Account? editing, ErrorCode? requestToken, Action<AccountId, AccountConfig>? done)
    {
        this.done = done;
        logger = state.Logs.CreateLogger<JiraWizardWindow>();
        InitializeComponent();
        wizard = new JiraWizardController(state.Client, state.Launcher, editing, logger: state.Logs.CreateLogger<JiraWizardController>());
        if (requestToken is { } reason)
        {
            wizard.RequestToken(reason);
        }
        site = new JiraSitePage(wizard);
        credentials = new JiraCredentialsPage(wizard);
        spaces = new JiraSpacesPage(wizard);

        Title = wizard.Title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        // The window keeps the assistant's name (Alt+Tab, UIA), the header
        // shows the page's (AccountWizardWindow).
        Header.Loaded += (_, _) => Title = wizard.Title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);
        wizard.Owner = WindowPresenter.Handle(this);
        Wire();
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

    /// <summary>
    /// Opens the assistant over <paramref name="owner"/> (the main window
    /// when null or hidden, shown first). <paramref name="editing"/> replaces
    /// the token of a Jira account (NewJiraEdit); <paramref name="requestToken"/>
    /// is the notify.authRequired reason that led there (<c>authRequired</c>,
    /// <c>authFailed</c>), which the page's banner explains.
    /// <paramref name="done"/> runs after account.add or account.update
    /// succeeded, before the assistant closes. Null when there is no window
    /// to open it over.
    /// </summary>
    public static JiraWizardWindow? Show(
        AppState state,
        Window? owner,
        Account? editing = null,
        ErrorCode? requestToken = null,
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
        var w = new JiraWizardWindow(state, editing, requestToken, done);
        w.wizard.Start();
        w.modal.Present(owner, ContentWidth, ContentHeight);
        LogPresented(w.logger, w.visible ?? JiraWizardPage.Site, w.wizard.IsEditing);
        return w;
    }

    private void Wire()
    {
        wizard.PagesChanged += (_, pages) => ShowStack(pages);
        wizard.BusyChanged += (_, progress) =>
        {
            site.SetBusy(progress);
            credentials.SetBusy(progress);
            spaces.SetBusy(progress);
        };
        wizard.SiteChecked += (_, check) => site.ShowCheck(check.Ok, check.Problem);
        wizard.DetectedChanged += (_, text) => site.ShowDetected(text);
        wizard.CredentialPageChanged += (_, page) => credentials.Show(page);
        wizard.SpacesChanged += (_, s) => spaces.Show(s.Rows, s.Selected);
        wizard.SpacesProblemChanged += (_, text) => spaces.ShowProblem(text);
        wizard.EmailFieldChanged += (_, e) => spaces.ShowEmail(e.Shown, e.Email);
        wizard.BannerChanged += (_, b) =>
        {
            switch (b.Page)
            {
                case JiraWizardPage.Site:
                    site.ShowBanner(b.Text);
                    break;
                case JiraWizardPage.Credentials:
                    credentials.ShowBanner(b.Text);
                    break;
                default:
                    spaces.ShowBanner(b.Text);
                    break;
            }
        };
        wizard.ProblemsShown += (_, fields) =>
        {
            site.ShowProblems(fields);
            credentials.ShowProblems(fields);
            spaces.ShowProblems(fields);
        };
        wizard.FocusRequested += (_, field) => Focus(field);
        wizard.ToastRequested += (_, text) => ToastsHost.Show(text);
        wizard.Done += (_, saved) =>
        {
            try
            {
                done?.Invoke(saved.Id, saved.Config);
            }
            finally
            {
                Close();
            }
        };
    }

    // showPages: the last page is visible; a page later in the order slides
    // in from the right, an earlier one from the left, the first one without
    // a transition.
    private void ShowStack(IReadOnlyList<JiraWizardPage> pages)
    {
        if (pages.Count == 0)
        {
            return;
        }
        var top = pages[^1];
        Header.Title = wizard.PageTitle(top);
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
                Effect = top > from ? SlideNavigationTransitionEffect.FromRight : SlideNavigationTransitionEffect.FromLeft,
            };
        visible = top;
        Pages.Navigate(typeof(WizardPageHost), PageView(top), transition);
    }

    private UserControl PageView(JiraWizardPage page) => page switch
    {
        JiraWizardPage.Credentials => credentials,
        JiraWizardPage.Spaces => spaces,
        _ => site,
    };

    private Control? ControlOf(Field field) =>
        site.ControlOf(field) ?? credentials.ControlOf(field) ?? spaces.ControlOf(field);

    // onFocus: the field when its page is up, else once it is.
    private void Focus(Field field)
    {
        if (ControlOf(field) is { IsLoaded: true } target)
        {
            target.Focus(FocusState.Programmatic);
            return;
        }
        focusRequest = field;
    }

    // After a page came in: the requested field, or the page's own first
    // field, else its first control, so the keyboard stays in the
    // assistant. The Frame reports the navigation before the page is in the
    // tree, where it cannot take the focus yet: then once it is loaded.
    private void FocusPage()
    {
        var view = PageView(visible ?? JiraWizardPage.Site);
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
        if (focusRequest is { } field && ControlOf(field) is { IsLoaded: true } requested)
        {
            focusRequest = null;
            requested.Focus(FocusState.Programmatic);
            return;
        }
        Control? target = visible switch
        {
            JiraWizardPage.Credentials => credentials.InitialFocus,
            JiraWizardPage.Spaces => spaces.InitialFocus,
            _ => site.InitialFocus,
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

    private void GoBack()
    {
        if (wizard.CanGoBack)
        {
            wizard.Back();
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "Jira account assistant opened on {Page}, edit {Editing}")]
    private static partial void LogPresented(ILogger logger, JiraWizardPage page, bool editing);

    [LoggerMessage(Level = LogLevel.Debug, Message = "Jira account assistant closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
