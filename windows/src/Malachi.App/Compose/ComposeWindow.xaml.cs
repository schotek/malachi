// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeWindowController.swift
// (init, wireToolbar, windowShouldClose, windowWillClose, ComposePaneHost,
// ComposeWindowHandle, the toolbar's actions); GTK:
// ui/internal/compose/compose.go (Window, newWindow, wireActions, the
// pane's hooks) and draft.go (closeRequest, cleanup as the window runs
// them). The window's content is a ComposePane (Layout.Window, the window
// owner), which holds the fields, the editor, the attachments and the draft
// (Core's ComposeDraftController over the pane's IComposeForm); the window
// keeps its title bar (the title, Attach, the rewrite, the Draft Menu,
// Send), the close question, Quit and its commands' keys, and hears from the
// pane through IComposePaneHost. The account list arrives from
// ComposeController through IComposeWindowHandle and goes to the pane. A
// comment on an issue is written in the same window, in the pane's comment
// mode; the window takes away what attaches or saves a draft
// (ApplyCommentMode).
//
// Windows specifics (docs/windows-port.md §6.5, §11.3, §11.5):
// - the window is tracked (WindowTracker.Track, WindowKind.Compose): its
//   colour scheme, its toasts (the pane's overlay), and its CommandRouter,
//   which runs Ctrl+Enter (Send), Ctrl+S (Save Draft), Escape and Ctrl+W
//   (the close request, not while a popup of the window is open), and the
//   application's keys; the editor's WebView2 is marked as an editor, so its
//   Ctrl+B, I, U, K and Escape stay with the bridge, which posts Escape
//   (ComposePane.EscapePressed) and Ctrl+K back;
// - closing (the caption's button, Alt+F4, Escape, Ctrl+W) is the close
//   request of draft.go: nothing at stake closes at once, otherwise "Save
//   changes to this draft?" (AlertService: Save Draft the default, Discard,
//   Cancel), and the window closes when the answer lets it. As Swift's
//   windowShouldClose, AppWindow.Closing lets a close with nothing at stake
//   go on and cancels only to ask, and the window is never closed from
//   inside that event;
// - Quit saves the draft without asking (SaveForQuitAsync) and asks the
//   close question only when that failed (CloseForQuitAsync);
// - the confirmation of a send ("Message queued for sending") goes to the
//   main window through the manager (the pane's window layout reports it
//   to ComposeController.ReportSent).

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Compose;

/// <summary>One compose window (compose.blp, compose.Window).</summary>
public sealed partial class ComposeWindow : Window, IComposeWindowHandle, IComposePaneHost
{
    /// <summary>compose.blp default-width.</summary>
    public const int DefaultWidth = 760;

    /// <summary>compose.blp default-height.</summary>
    public const int DefaultHeight = 640;

    /// <summary>compose.blp width-request.</summary>
    public const int MinWidth = 360;

    /// <summary>compose.blp height-request.</summary>
    public const int MinHeight = 420;

    private readonly AppState state;
    private readonly ComposeController compose;
    private readonly ComposeParams parameters;
    private readonly ComposePane pane;
    private readonly ILogger logger;

    // The window is closing for good: no question any more.
    private bool closing;

    // The close question while it is up; a second request waits for it
    // (Swift's closeQuestionPending).
    private Task<bool>? closeQuestion;

    /// <summary>
    /// compose.newWindow: builds a window prefilled from <paramref name="p"/>
    /// (not shown yet; ComposeManager places and presents it).
    /// </summary>
    public ComposeWindow(AppState state, ComposeController compose, ComposeParams p)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(compose);
        ArgumentNullException.ThrowIfNull(p);
        this.state = state;
        this.compose = compose;
        parameters = p;
        logger = state.Logs.CreateLogger<ComposeWindow>();
        InitializeComponent();

        ExtendsContentIntoTitleBar = true;
        SetTitleBar(ComposeTitleBar);
        AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        if (!MicaController.IsSupported())
        {
            SolidBackground.Visibility = Visibility.Visible;
        }
        ApplySize();
        // The pane first: its toast overlay is the window's. A pane that
        // cannot be built lets go of what it made itself.
        pane = new ComposePane(state, compose, p, new ComposePane.Options { Layout = ComposePane.Layout.Window, Owner = DraftOwner.Window });
        PaneSlot.Child = pane;
        Tracked = state.Windows.Track(this, WindowKind.Compose, Root, pane.Toasts);
        try
        {
            var send = Mnemonic.Parse(L10n.T("_Send"));
            SendLabel.Text = send.Label;
            SendButton.AccessKey = send.AccessKey ?? "";
            AutomationProperties.SetName(SendButton, send.Label);

            pane.Host = this;
            pane.EscapePressed += (_, _) => RequestClose();
            UpdateTitle();
            WireCommands();
            WireHeaderBar();
            WireRewrite();
            InsertImageItem.IsEnabled = ComposeDraftController.RichText;
            ApplyCommentMode();
        }
        catch
        {
            // Tracked, the window counts for the app's life until it closes:
            // one that could not be built must not hold the app (what opened
            // it logs the failure, ComposeController.Open).
            pane.Close();
            Close();
            throw;
        }

        AppWindow.Closing += OnClosing;
        Root.Loaded += OnRootLoaded;
        Closed += OnClosed;
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The window's content.</summary>
    public ComposePane Pane => pane;

    /// <summary>comment.go <c>isComment</c>: the window writes a comment on an issue.</summary>
    public bool IsComment => pane.IsComment;

    /// <inheritdoc/>
    Window? IComposePaneHost.HostWindow => this;

    // Whether a popup of the window is open: Escape and Ctrl+W close it,
    // not the window (macOS EscapeCloser.shouldClose).
    private bool PopupOpen => pane.PopupOpen || DraftMenuButton.Flyout?.IsOpen == true || RewriteFlyout.IsOpen;

    private nint Handle => WindowPresenter.Handle(this);

    /// <summary>Shows a transient message over the window's content.</summary>
    public void Toast(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        pane.Toasts.Show(text);
    }

    /// <inheritdoc/>
    void IComposePaneHost.TitleChanged(ComposePane pane) => UpdateTitle();

    /// <inheritdoc/>
    void IComposePaneHost.SendEnabledChanged(ComposePane pane, bool enabled)
    {
        SendButton.IsEnabled = enabled;
        Tracked.Commands.Send.Refresh();
    }

    /// <inheritdoc/>
    void IComposePaneHost.Ended(ComposePane pane, ComposePane.EndKind kind) => CloseForGood();

    /// <inheritdoc/>
    void IComposePaneHost.HeightChanged(ComposePane pane)
    {
        // The window's editor fills the window: there is no height to follow.
    }

    /// <summary>compose.go <c>setAccounts</c>: the pane's From row lists <paramref name="accounts"/>.</summary>
    public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder) => pane.SetAccounts(accounts, placeholder);

    /// <summary>
    /// manager.go <c>FindDraft</c>'s comparison: the same saved draft, or the
    /// same Drafts message taken over.
    /// </summary>
    public bool Edits(Draft draft) => pane.Edits(draft);

    /// <summary>Brings the window to the front.</summary>
    public void Present() => WindowPresenter.Present(this);

    /// <summary>Quit: saves the draft without asking; true when nothing unsaved is left.</summary>
    public Task<bool> SaveForQuitAsync() => closing ? Task.FromResult(true) : pane.SaveForQuitAsync();

    /// <summary>
    /// Quit could not save the draft: the close question, and the window
    /// closes when the answer lets it; false (the Quit is abandoned) when it
    /// stays.
    /// </summary>
    public Task<bool> CloseForQuitAsync() => CloseAsync();

    /// <summary>The window's size at its display's scale, and its smallest (compose.blp).</summary>
    private void ApplySize()
    {
        var scale = Scale();
        AppWindow.Resize(new SizeInt32((int)Math.Round(DefaultWidth * scale), (int)Math.Round(DefaultHeight * scale)));
        if (AppWindow.Presenter is OverlappedPresenter overlapped)
        {
            overlapped.PreferredMinimumWidth = (int)Math.Round(MinWidth * scale);
            overlapped.PreferredMinimumHeight = (int)Math.Round(MinHeight * scale);
        }
    }

    private double Scale()
    {
        var dpi = PInvoke.GetDpiForWindow((HWND)Handle);
        return dpi == 0 ? 1.0 : dpi / 96.0;
    }

    // compose.go updateTitle: the pane's title (the subject, or "New
    // Message"; a comment names its issue) as the window's caption (the
    // taskbar, Alt+Tab) and in the header bar.
    private void UpdateTitle()
    {
        var title = pane.TitleText;
        Title = title;
        TitleText.Text = title;
    }

    // compose.go wireActions: compose.send and compose.save through the
    // window's commands (their keys are the router's); the close request.
    private void WireCommands()
    {
        var c = Tracked.Commands;
        c.Send.Handler = pane.Send;
        c.Send.CanExecute = () => pane.SendEnabled;
        c.SaveDraft.Handler = pane.SaveDraft;
        // No Drafts folder keeps a comment: Ctrl+S does nothing.
        c.SaveDraft.CanExecute = () => !IsComment;
        c.CloseWindow.Handler = RequestClose;
        c.CloseWindow.CanExecute = () => !PopupOpen;
    }

    // applyCommentMode, the window's part: nothing attaches, nothing is kept
    // as a draft; the draft menu keeps Discard (compose.blp hidden-when of
    // the disabled actions) and Insert Image where a comment keeps pictures.
    private void ApplyCommentMode()
    {
        if (!IsComment)
        {
            return;
        }
        AttachButton.Visibility = Visibility.Collapsed;
        AttachFilesItem.Visibility = Visibility.Collapsed;
        SaveDraftItem.Visibility = Visibility.Collapsed;
        InsertImageItem.Visibility = Jira.CommentAllows(JiraFormat.Image) ? Visibility.Visible : Visibility.Collapsed;
        DiscardSeparator.Visibility = InsertImageItem.Visibility;
    }

    private void OnAttachClick(object sender, RoutedEventArgs e) => pane.AttachFiles();

    private void OnInsertImageClick(object sender, RoutedEventArgs e) => pane.InsertImage();

    private void OnDiscardClick(object sender, RoutedEventArgs e) => pane.Discard();

    // send_button and the menu's Save Draft run the window's commands (a
    // XamlUICommand would put its own label in place of the button's).
    private void OnSendClick(object sender, RoutedEventArgs e) => Tracked.Commands.Send.TryExecute();

    private void OnSaveDraftClick(object sender, RoutedEventArgs e) => Tracked.Commands.SaveDraft.TryExecute();

    // Once shown, the keyboard is in To (compose.blp focus-widget); a
    // reply's editor takes it at the start of the body once it is ready. A
    // comment has no rows: it is written in the editor.
    private void OnRootLoaded(object sender, RoutedEventArgs e)
    {
        Root.Loaded -= OnRootLoaded;
        pane.FocusInitial();
    }

    // windowShouldClose: the caption's button and Alt+F4. With nothing at
    // stake the window goes on closing (the window is never closed from
    // inside this event); otherwise the close is cancelled for the
    // question, and the window closes once it is answered.
    private void OnClosing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (closing)
        {
            return;
        }
        if (pane.CanCloseWithoutAsking)
        {
            pane.CleanupDraft();
            closing = true;
            return;
        }
        args.Cancel = true;
        // Asked once this event is over: the answer may close the window.
        _ = DispatcherQueue.TryEnqueue(() => _ = AskOnceAsync());
    }

    // closeRequest (Escape, Ctrl+W).
    private void RequestClose() => _ = CloseAsync();

    // closeRequest (Escape, Ctrl+W, Quit; none of them inside
    // AppWindow.Closing): the window goes at once when nothing is at stake;
    // otherwise the draft controller asks, and the window closes when the
    // answer lets it. True when it closed.
    private Task<bool> CloseAsync()
    {
        if (closing)
        {
            return Task.FromResult(true);
        }
        if (pane.CanCloseWithoutAsking)
        {
            pane.CleanupDraft();
            CloseForGood();
            return Task.FromResult(true);
        }
        return AskOnceAsync();
    }

    // The close question, asked once: a request while it is up waits for
    // the same answer. The field is cleared once the answer is in, and only
    // while it still holds this question, so a question that ends at once
    // (no dialog could be shown) leaves no finished task behind to answer
    // every later request.
    private async Task<bool> AskOnceAsync()
    {
        var question = closeQuestion ??= AskAsync();
        try
        {
            return await question;
        }
        finally
        {
            if (ReferenceEquals(closeQuestion, question))
            {
                closeQuestion = null;
            }
        }
    }

    private async Task<bool> AskAsync()
    {
        try
        {
            var allowed = await pane.CloseRequestAsync();
            if (allowed)
            {
                CloseForGood();
            }
            return allowed;
        }
        catch (Exception e) when (e is InvalidOperationException or System.Runtime.InteropServices.COMException or TaskCanceledException)
        {
            // The question could not be shown: the window and its draft stay.
            LogCloseQuestionFailed(logger, e);
            return false;
        }
    }

    private void CloseForGood()
    {
        if (closing)
        {
            return;
        }
        closing = true;
        Close();
    }

    // cleanup: the window really closes. Late replies are dropped, the
    // rewrite ends, the pane lets go (its popups, its draft, whose inline
    // pictures are forgotten before the attachments close, its editor's
    // browser), and the manager forgets the window.
    private void OnClosed(object sender, WindowEventArgs args)
    {
        closing = true;
        CloseRewrite();
        pane.Close();
        compose.Remove(this);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "the close question of a compose window could not be asked")]
    private static partial void LogCloseQuestionFailed(ILogger logger, Exception error);
}
