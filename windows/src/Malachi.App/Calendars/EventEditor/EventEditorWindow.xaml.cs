// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorWindowController.swift
// (init, edits, present, closeUnasked, ask, askDelete, findFreeSlot,
// askAssistantAboutEvent, windowShouldClose, windowWillClose, the menu
// bar's Save, Send and Attach, the validations, menuNeedsUpdate); GTK: the
// event editor follows (docs/calendar.md §6.4).
//
// One New Event / Edit Event window over its EventEditorController
// (docs/calendar.md §5.6, M7): the title bar with Attach and ✦ (Find a Free
// Slot; Ask the Assistant…, only while the assistant panel exists, opens
// the main window's panel and hands it nothing), the form (EventEditorPane)
// and the window's toasts. A save, a discard and a delete close the window
// (the controller's Saved and CloseRequested); the calendar refreshes by the
// daemon's notification. Closing with changes asks Save / Discard / Cancel
// (Cal.CloseQuestion, Save the default); nothing is stored before Save
// (D6), so Discard just closes. Delete asks what the detail's Remove asks,
// with Cancel the default (Cal.RemoveQuestion, else Cal.DeleteQuestion
// under the Mail preference confirm-delete). A conflict on Save offers
// Reload Event: this window closes without asking and the event opens as
// the daemon has it, in its place (the manager).
//
// Windows (docs/windows-port.md §11.3): the window is tracked as a compose
// window (WindowKind.Compose): its colour scheme, its toasts and its
// CommandRouter, whose Ctrl+Enter is the primary (Save / Save and Send
// Invitations), Ctrl+S Save while there are no attendees, and Escape and
// Ctrl+W the close request (not while a popup of the form is open). As the
// compose window, AppWindow.Closing lets a close with nothing at stake go on
// and cancels only to ask, the window closing after the answer; the
// question is asked once. Quit asks the same question of a window with
// changes, through Quit's step for the compose windows whose save failed
// (an event cannot be saved without asking: Save sends the invitations);
// Cancel abandons the Quit (IComposeWindowHandle, EventEditorManager.ForQuit).

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Calendars;

/// <summary>One New Event / Edit Event window (artboard 5).</summary>
public sealed partial class EventEditorWindow : Window, IComposeWindowHandle
{
    /// <summary>The window's width (EventEditorWindowController.defaultSize).</summary>
    public const int DefaultWidth = 760;

    /// <summary>The window's height.</summary>
    public const int DefaultHeight = 800;

    /// <summary>The window's smallest width (EventEditorWindowController.minimumSize).</summary>
    public const int MinWidth = 560;

    /// <summary>The window's smallest height.</summary>
    public const int MinHeight = 520;

    private readonly AppState state;
    private readonly EventEditorManager manager;
    private readonly EventEditorController controller;
    private readonly ILogger logger;

    // The window is closing for good: no question any more.
    private bool closing;

    // The close question while it is up; a second request waits for it.
    private Task<bool>? closeQuestion;

    /// <summary>The window of <paramref name="editor"/>, not shown yet (the manager places and presents it).</summary>
    internal EventEditorWindow(AppState state, EventEditorManager manager, EventEditorController editor)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(manager);
        ArgumentNullException.ThrowIfNull(editor);
        this.state = state;
        this.manager = manager;
        controller = editor;
        logger = state.Logs.CreateLogger<EventEditorWindow>();
        InitializeComponent();

        ExtendsContentIntoTitleBar = true;
        SetTitleBar(EditorTitleBar);
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
        Tracked = state.Windows.Track(this, WindowKind.Compose, Root, ToastsHost);
        try
        {
            Title = editor.WindowTitle;
            TitleText.Text = editor.WindowTitle;
            AutomationProperties.SetName(AttachButton, Cal.Attach);
            ToolTipService.SetToolTip(AttachButton, Cal.Attach);
            var assistant = Assistant.Texts().Assistant;
            AutomationProperties.SetName(AssistantButton, assistant);
            ToolTipService.SetToolTip(AssistantButton, assistant);
            FindFreeSlotItem.Text = L10n.T("Find a Free Slot");
            AskAssistantItem.Text = L10n.T("Ask the Assistant…");

            Pane.HostWindow = this;
            Pane.ToastHandler = Toast;
            Pane.CancelRequested += (_, _) => RequestClose();
            Pane.ReloadRequested += (_, _) => _ = ReloadAsync();
            Pane.Attach(state, editor);
            WireController();
            WireCommands();
        }
        catch
        {
            // Tracked, the window counts for the app's life until it closes.
            Pane.Close();
            Close();
            throw;
        }

        AppWindow.Closing += OnClosing;
        Root.Loaded += OnRootLoaded;
        Closed += OnClosed;
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The window's controller.</summary>
    internal EventEditorController Controller => controller;

    /// <summary>Whether the window has changes Quit would lose.</summary>
    internal bool HasChanges => !closing && controller.Dirty;

    private nint Handle => WindowPresenter.Handle(this);

    // Whether a popup of the window is open: Escape and Ctrl+W close it,
    // not the window.
    private bool PopupOpen => Pane.PopupOpen || AssistantMenu.IsOpen;

    /// <summary>
    /// Whether the window edits event <paramref name="id"/> for
    /// <paramref name="scope"/> (and, below All Events, occurrence
    /// <paramref name="recurrenceId"/>): the manager raises it instead of
    /// opening another.
    /// </summary>
    internal bool Edits(CalendarEventId id, DateOrTime? recurrenceId, EditScope scope)
    {
        if (closing || controller.EditorMode is not EventEditorController.Mode.EditEvent { Edited: var e, Scope: var s } || e.EventId is not { } own)
        {
            return false;
        }
        return own == id && s == scope && (scope == EditScope.All || e.RecurrenceId == recurrenceId);
    }

    /// <summary>Brings the window to the front.</summary>
    public void Present() => WindowPresenter.Present(this);

    /// <summary>✦ Find a Free Slot (and the detail's Find Another Time on a window already open).</summary>
    internal void FindFreeSlot() => controller.NextFreeSlot();

    /// <summary>The controller decided (saved, discarded, deleted, reloaded): the window goes.</summary>
    internal void CloseUnasked() => CloseForGood();

    /// <inheritdoc/>
    public void Toast(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        ToastsHost.Show(text);
    }

    /// <inheritdoc/>
    void IComposeWindowHandle.SetAccounts(IReadOnlyList<Account> accounts, bool placeholder)
    {
        // An event editor has no From row.
    }

    /// <summary>
    /// Quit's save: nothing is stored without asking (Save sends the
    /// invitations), so true only when there is nothing to lose; a window
    /// with changes is asked its close question (<see cref="CloseForQuitAsync"/>).
    /// </summary>
    public Task<bool> SaveForQuitAsync() => Task.FromResult(!HasChanges);

    /// <summary>
    /// Quit and the window has changes: the close question, and the window
    /// closes when the answer lets it; false (the Quit is abandoned) when it stays.
    /// </summary>
    public Task<bool> CloseForQuitAsync() => CloseAsync();

    // The window's size at its display's scale, and its smallest.
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

    private void WireController()
    {
        var c = controller;
        c.Saved += (_, _) => CloseForGood();
        c.CloseRequested += (_, _) => CloseForGood();
        // Save / Discard / Cancel: Save the default (the draft question's order).
        c.AskQuestion = q => state.Alerts.CalendarQuestionAsync(this, q, confirmDefault: true);
        // Delete's questions: Cancel the default.
        c.AskDeleteQuestion = q => state.Alerts.CalendarQuestionAsync(this, q, confirmDefault: false);
        var settings = state.Settings;
        c.ConfirmDelete = () => settings.ConfirmDelete;
        c.Changed += (_, _) => RefreshCommands();
        RefreshCommands();
    }

    // The menu bar's Send (Ctrl+Enter: the primary), Save Draft (Ctrl+S:
    // Save, only without attendees) and the close request.
    private void WireCommands()
    {
        var commands = Tracked.Commands;
        commands.Send.Handler = Pane.Save;
        commands.Send.CanExecute = () => !closing && !controller.Saving;
        commands.SaveDraft.Handler = Pane.Save;
        commands.SaveDraft.CanExecute = () => !closing && !controller.Saving && controller.SaveKeyAllowed;
        commands.CloseWindow.Handler = RequestClose;
        commands.CloseWindow.CanExecute = () => !PopupOpen;
    }

    private void RefreshCommands()
    {
        if (closing)
        {
            return;
        }
        AttachButton.IsEnabled = controller.Attachments.Count < API.Limits.MaxEventAttachments;
        Tracked.Commands.Send.Refresh();
        Tracked.Commands.SaveDraft.Refresh();
    }

    private void OnAttachClick(object sender, RoutedEventArgs e) => Pane.AttachFiles();

    // menuNeedsUpdate and the validations: Find a Free Slot for a timed
    // event with a calendar, Ask the Assistant… while the panel exists.
    private void OnAssistantMenuOpening(object? sender, object e)
    {
        FindFreeSlotItem.IsEnabled = !controller.Input.AllDay && controller.Calendars.Count > 0;
        AskAssistantItem.Visibility = state.Assistant.PanelShown ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnFindFreeSlotClick(object sender, RoutedEventArgs e) => FindFreeSlot();

    // askAssistantAboutEvent: the main window's assistant panel; the event
    // is not handed to it.
    private void OnAskAssistantClick(object sender, RoutedEventArgs e)
    {
        if (state.Assistant.PanelShown && state.MainWindow is { } main)
        {
            main.OpenAssistantPanel();
        }
    }

    // Reload Event (manager.reload): the event as the daemon has it, in a
    // new window where this one was; this one closes unasked.
    private async Task ReloadAsync()
    {
        if (closing || controller.EditorMode is not EventEditorController.Mode.EditEvent { Scope: var scope })
        {
            return;
        }
        var fresh = await controller.ReloadEventAsync();
        if (fresh is null || closing)
        {
            return;
        }
        manager.Reopen(this, fresh, scope);
    }

    // The keyboard is in the title once the window shows.
    private void OnRootLoaded(object sender, RoutedEventArgs e)
    {
        Root.Loaded -= OnRootLoaded;
        Pane.FocusTitle();
    }

    // windowShouldClose: the caption's button and Alt+F4. Without changes
    // the window goes on closing; otherwise the close is cancelled for the
    // question, and the window closes once it is answered.
    private void OnClosing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (closing)
        {
            return;
        }
        if (!controller.Dirty)
        {
            closing = true;
            return;
        }
        args.Cancel = true;
        // Asked once this event is over: the answer may close the window.
        _ = DispatcherQueue.TryEnqueue(() => _ = AskOnceAsync());
    }

    // Escape, Ctrl+W, Cancel.
    private void RequestClose() => _ = CloseAsync();

    // The window goes at once without changes; otherwise the controller
    // asks, and its CloseRequested closes the window. True when it closed.
    private Task<bool> CloseAsync()
    {
        if (closing)
        {
            return Task.FromResult(true);
        }
        if (!controller.Dirty)
        {
            CloseForGood();
            return Task.FromResult(true);
        }
        return AskOnceAsync();
    }

    // The close question, asked once: a request while it is up waits for
    // the same answer.
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
            return await controller.CloseAsync();
        }
        catch (Exception e) when (e is InvalidOperationException or System.Runtime.InteropServices.COMException or TaskCanceledException)
        {
            // The question could not be shown: the window and its changes stay.
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

    // windowWillClose: the controller stops, the form lets go, the manager
    // forgets the window.
    private void OnClosed(object sender, WindowEventArgs args)
    {
        closing = true;
        Pane.Close();
        controller.Stop();
        manager.Remove(this);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "the close question of an event editor could not be asked")]
    private static partial void LogCloseQuestionFailed(ILogger logger, Exception error);
}
