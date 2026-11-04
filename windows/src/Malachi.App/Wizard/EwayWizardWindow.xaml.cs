// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/EWay/EWayWizardWindowController.swift
// (present, EWayWizardRootViewController: wire, showStack, applyHeader);
// GTK: ui/internal/accountwizard/eway.go (written in T7; the flow is
// eway_flow.go). The flow, its texts and its calls are Core's
// EwayWizardController; this window shows its two pages and feeds the
// fields back. Adding opens on the web service address; editing (the
// Integrations row's Edit, Integrations.Editor.Eway) on the sign-in with
// the address fixed. Opened only from Preferences → Integrations (Add… and
// Edit), as on macOS.
//
// Windows (docs/windows-port.md §11.11): the Jira assistant's window
// (JiraWizardWindow), an owned modal window over the window it was opened
// from (ModalDialog), 520×560 effective pixels like the macOS sheet. Its
// title bar carries Back and the visible page's title; its close button and
// Escape cancel the assistant, whose late replies are dropped from then on
// (EwayWizardController.Close). A save that succeeded closes it after the
// caller's completion ran. Nothing here logs a field: the password goes
// from its box to the controller only.

using System;
using System.Collections.Generic;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media.Animation;

namespace Malachi.App.Wizard;

/// <summary>The "Add eWay-CRM Account" / "Edit Account" assistant.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The assistant's controller is closed with the window (Closed).")]
public sealed partial class EwayWizardWindow : Window
{
    /// <summary>The macOS sheet's width.</summary>
    public const int ContentWidth = 520;

    /// <summary>The macOS sheet's height.</summary>
    public const int ContentHeight = 560;

    private readonly EwayWizardController wizard;
    private readonly Action<AccountId, AccountConfig>? done;
    private readonly ILogger logger;
    private readonly ModalDialog modal;
    private readonly EwayServicePage service;
    private readonly EwayCredentialsPage credentials;
    private CrmWizardPage? visible;

    // A focus the controller asked for before its page was shown.
    private CrmWizardField? focusRequest;

    // The page whose Loaded will place the focus (FocusPage).
    private UserControl? focusOnLoad;

    private EwayWizardWindow(AppState state, Account? editing, Action<AccountId, AccountConfig>? done)
    {
        this.done = done;
        logger = state.Logs.CreateLogger<EwayWizardWindow>();
        InitializeComponent();
        wizard = new EwayWizardController(state.Client, editing, logger: state.Logs.CreateLogger<EwayWizardController>());
        service = new EwayServicePage(wizard);
        credentials = new EwayCredentialsPage(wizard);

        Title = wizard.Title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Loaded += (_, _) => Title = wizard.Title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);
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
    /// when null or hidden, shown first). <paramref name="editing"/> changes
    /// an existing eWay-CRM account. <paramref name="done"/> runs after
    /// account.add or account.update succeeded, before the assistant closes.
    /// Null when there is no window to open it over.
    /// </summary>
    public static EwayWizardWindow? Show(
        AppState state,
        Window? owner,
        Account? editing = null,
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
        var w = new EwayWizardWindow(state, editing, done);
        w.wizard.Start();
        w.modal.Present(owner, ContentWidth, ContentHeight);
        LogPresented(w.logger, w.visible ?? CrmWizardPage.Service, w.wizard.IsEditing);
        return w;
    }

    private void Wire()
    {
        wizard.PagesChanged += (_, pages) => ShowStack(pages);
        wizard.BusyChanged += (_, progress) =>
        {
            service.SetBusy(progress);
            credentials.SetBusy(progress);
        };
        wizard.ServiceChecked += (_, check) => service.ShowCheck(check.Ok, check.Problem);
        wizard.NameChanged += (_, name) => credentials.ShowName(name);
        wizard.BannerChanged += (_, b) =>
        {
            if (b.Page == CrmWizardPage.Service)
            {
                service.ShowBanner(b.Text);
            }
            else
            {
                credentials.ShowBanner(b.Text);
            }
        };
        wizard.ProblemShown += (_, field) =>
        {
            service.ShowProblem(field);
            credentials.ShowProblem(field);
        };
        wizard.FocusRequested += (_, field) => Focus(field);
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

    // The last page is visible; a page later in the order slides in from
    // the right, an earlier one from the left, the first one without a
    // transition.
    private void ShowStack(IReadOnlyList<CrmWizardPage> pages)
    {
        if (pages.Count == 0)
        {
            return;
        }
        var top = pages[^1];
        Header.Title = wizard.PageTitle(top);
        Title = wizard.Title;
        Header.IsBackButtonVisible = wizard.CanGoBack;
        credentials.ShowService(wizard.ServiceUrl);
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

    private UserControl PageView(CrmWizardPage page) => page == CrmWizardPage.Credentials ? credentials : service;

    private Control? ControlOf(CrmWizardField field) => service.ControlOf(field) ?? credentials.ControlOf(field);

    // onFocus: the field when its page is up, else once it is.
    private void Focus(CrmWizardField field)
    {
        if (ControlOf(field) is { IsLoaded: true } target)
        {
            target.Focus(FocusState.Programmatic);
            return;
        }
        focusRequest = field;
    }

    // After a page came in: the requested field, or the page's own first
    // field (JiraWizardWindow.FocusPage).
    private void FocusPage()
    {
        var view = PageView(visible ?? CrmWizardPage.Service);
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
        Control? target = visible == CrmWizardPage.Credentials ? credentials.InitialFocus : service.InitialFocus;
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

    [LoggerMessage(Level = LogLevel.Information, Message = "eWay-CRM account assistant opened on {Page}, edit {Editing}")]
    private static partial void LogPresented(ILogger logger, CrmWizardPage page, bool editing);

    [LoggerMessage(Level = LogLevel.Debug, Message = "eWay-CRM account assistant closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
