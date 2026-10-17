// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorManager.swift
// (openNew, openEdit, remove, reload, open, waitForLoad, place); GTK: the
// event editor follows (docs/calendar.md §6.4).
//
// The New Event / Edit Event windows of the application (docs/calendar.md
// §5.6, M7), a registry like the compose manager: every New Event is a new
// window; an event already being edited (the same event and scope and,
// below All Events, the same occurrence) raises its window instead, and a
// Find Another Time for it runs Next Free Slot there. The windows work over
// the main window's calendar source (AppState.StartedCalendarSourceFor), so
// a save lands in the caches the views read and the views refresh by the
// daemon's notification; a source that has not loaded yet (New Event from
// Mail before the Calendar was ever shown) is started and given up to 5 s
// before the window opens, so that the Calendar popup has the writable
// calendars. Reload Event after a conflict opens the event as the daemon
// has it in a new window where the old one was, which closes unasked. New
// windows cascade 32 px from a first one centred on the display of the
// window that was active, as the compose windows do (ComposeManager).
//
// Windows: Edit takes the event's id (AppState.OpenEditEvent, the detail's
// EditRequested) and asks the source for the event (event.get, which the
// detail has just cached); a failure is a toast over the main window. Quit
// asks the windows with changes their close question (ForQuit, QuitSteps'
// SaveDrafts beside the compose windows').

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Windowing;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Calendars;

/// <summary>Makes, finds and places the event editor windows.</summary>
internal sealed partial class EventEditorManager
{
    /// <summary>How far each new window is moved from the previous one, in effective pixels.</summary>
    public const int CascadeOffset = 32;

    /// <summary>How long a window waits for a source that has not loaded yet.</summary>
    public static readonly TimeSpan LoadWait = TimeSpan.FromSeconds(5);

    private readonly AppState state;
    private readonly ILogger logger;
    private readonly List<EventEditorWindow> windows = [];
    private PointInt32? last;

    /// <summary>The registry of <paramref name="appState"/>'s editor windows.</summary>
    public EventEditorManager(AppState appState)
    {
        ArgumentNullException.ThrowIfNull(appState);
        state = appState;
        logger = appState.Logs.CreateLogger<EventEditorManager>();
    }

    /// <summary>The windows that are open.</summary>
    public IReadOnlyList<EventEditorWindow> Windows => windows;

    /// <summary>
    /// openNew: a New Event window with <paramref name="defaults"/> (New
    /// Event of the window's commands, the calendar's "+", a double click on
    /// empty time).
    /// </summary>
    public void OpenNew(Cal.Defaults defaults)
    {
        ArgumentNullException.ThrowIfNull(defaults);
        _ = OpenAsync(new EventEditorController.Mode.NewEvent(), defaults, findFreeSlot: false, frame: null);
    }

    /// <summary>
    /// openEdit: the Edit Event window of event <paramref name="id"/>
    /// (occurrence <paramref name="recurrenceId"/>) for
    /// <paramref name="scope"/>, or the one editing it brought forward;
    /// <paramref name="findFreeSlot"/> runs Next Free Slot once it is open.
    /// </summary>
    public void OpenEdit(CalendarEventId id, DateOrTime? recurrenceId, EditScope scope, bool findFreeSlot)
    {
        if (Find(id, recurrenceId, scope) is { } open)
        {
            open.Present();
            if (findFreeSlot)
            {
                open.FindFreeSlot();
            }
            return;
        }
        _ = OpenEditAsync(id, recurrenceId, scope, findFreeSlot);
    }

    /// <summary>A window closed (its Closed).</summary>
    public void Remove(EventEditorWindow window) => windows.Remove(window);

    /// <summary>
    /// reload: <paramref name="fresh"/> (the event as the daemon has it) in a
    /// new window where <paramref name="old"/> was; the old one closes
    /// unasked (the user chose to drop the edits).
    /// </summary>
    public void Reopen(EventEditorWindow old, CalendarEvent fresh, EditScope scope)
    {
        ArgumentNullException.ThrowIfNull(old);
        ArgumentNullException.ThrowIfNull(fresh);
        RectInt32? frame = null;
        try
        {
            frame = new RectInt32(old.AppWindow.Position.X, old.AppWindow.Position.Y, old.AppWindow.Size.Width, old.AppWindow.Size.Height);
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or InvalidOperationException)
        {
            LogPlaceFailed(logger, e);
        }
        var source = old.Controller.Source;
        old.CloseUnasked();
        Open(source, new EventEditorController.Mode.EditEvent(fresh, scope), null, findFreeSlot: false, frame);
    }

    /// <summary>
    /// Quit: the windows with changes, which Quit asks their close question
    /// (none is saved without asking).
    /// </summary>
    public IReadOnlyList<IComposeWindowHandle> ForQuit() => [.. windows.Where(w => w.HasChanges)];

    private EventEditorWindow? Find(CalendarEventId id, DateOrTime? recurrenceId, EditScope scope) =>
        windows.FirstOrDefault(w => w.Edits(id, recurrenceId, scope));

    // The main window's calendar source, started; null before the main window exists.
    private ICalendarDataSource? Source() => state.MainWindow is { } main ? state.StartedCalendarSourceFor(main) : null;

    private async Task OpenEditAsync(CalendarEventId id, DateOrTime? recurrenceId, EditScope scope, bool findFreeSlot)
    {
        if (Source() is not { } source)
        {
            return;
        }
        CalendarEvent e;
        try
        {
            e = await source.EventAsync(id, recurrenceId);
        }
        catch (Exception error) when (error is RpcException or RpcClientException or TimeoutException or OperationCanceledException or Cal.SourceError)
        {
            state.MainWindow?.Toasts.Show(Cal.ErrorMessage(error));
            return;
        }
        if (state.IsStopping)
        {
            return;
        }
        // Opened meanwhile (a second Edit while event.get was on its way).
        if (Find(id, recurrenceId, scope) is { } open)
        {
            open.Present();
            if (findFreeSlot)
            {
                open.FindFreeSlot();
            }
            return;
        }
        await OpenAsync(new EventEditorController.Mode.EditEvent(e, scope), null, findFreeSlot, frame: null);
    }

    private async Task OpenAsync(EventEditorController.Mode mode, Cal.Defaults? defaults, bool findFreeSlot, RectInt32? frame)
    {
        if (Source() is not { } source)
        {
            return;
        }
        await WaitForLoadAsync(source);
        if (state.IsStopping)
        {
            return;
        }
        Open(source, mode, defaults, findFreeSlot, frame);
    }

    // open: the controller, the window placed (or in frame) and shown, then
    // the controller started (with Next Free Slot for Find Another Time).
    private void Open(ICalendarDataSource source, EventEditorController.Mode mode, Cal.Defaults? defaults, bool findFreeSlot, RectInt32? frame)
    {
        var controller = new EventEditorController(source, mode, defaults, env: AppState.CalendarEnv());
        EventEditorWindow window;
        try
        {
            window = new EventEditorWindow(state, this, controller);
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or InvalidOperationException or ArgumentException)
        {
            controller.Stop();
            LogOpenFailed(logger, e);
            return;
        }
        windows.Add(window);
        try
        {
            try
            {
                if (frame is { } f)
                {
                    window.AppWindow.MoveAndResize(f);
                }
                else
                {
                    Place(window);
                }
            }
            catch (Exception e) when (e is System.Runtime.InteropServices.COMException or ArgumentException or InvalidOperationException)
            {
                LogPlaceFailed(logger, e);
            }
            window.Present();
        }
        catch
        {
            // Made but not shown, the window must not hold the app.
            window.Close();
            throw;
        }
        controller.Start(findFreeSlot);
    }

    // waitForLoad: a source that has nothing yet is started (or refreshed)
    // and given up to LoadWait.
    private static async Task WaitForLoadAsync(ICalendarDataSource source)
    {
        if (source.Snapshot.Phase != Cal.Phase.Loading)
        {
            return;
        }
        if (source is not DaemonCalendarSource)
        {
            source.Refresh();
        }
        var loaded = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var token = source.Observe(_ =>
        {
            if (source.Snapshot.Phase != Cal.Phase.Loading)
            {
                loaded.TrySetResult();
            }
        });
        try
        {
            if (source.Snapshot.Phase == Cal.Phase.Loading)
            {
                await Task.WhenAny(loaded.Task, Task.Delay(LoadWait, TimeProvider.System));
            }
        }
        finally
        {
            token.Cancel();
        }
    }

    // place: cascades new windows from the first one, centred on the display
    // of the window that was active last; a window that would leave the
    // display starts over at the top left of its work area.
    private void Place(EventEditorWindow window)
    {
        var app = window.AppWindow;
        var anchor = state.Windows.Active?.Window.AppWindow ?? state.MainWindow?.AppWindow ?? app;
        var display = DisplayArea.GetFromWindowId(anchor.Id, DisplayAreaFallback.Primary);
        var area = display.WorkArea;
        var size = app.Size;
        var dpi = PInvoke.GetDpiForWindow((HWND)WindowPresenter.Handle(window));
        var step = (int)Math.Round(CascadeOffset * (dpi == 0 ? 1.0 : dpi / 96.0));
        PointInt32 position;
        if (last is { } previous && Contains(area, previous))
        {
            position = new PointInt32(previous.X + step, previous.Y + step);
            if (position.X + size.Width > area.X + area.Width || position.Y + size.Height > area.Y + area.Height)
            {
                position = new PointInt32(area.X + step, area.Y + step);
            }
        }
        else
        {
            position = new PointInt32(
                area.X + Math.Max((area.Width - size.Width) / 2, 0),
                area.Y + Math.Max((area.Height - size.Height) / 2, 0));
        }
        app.Move(position);
        last = position;
    }

    private static bool Contains(RectInt32 area, PointInt32 p) =>
        p.X >= area.X && p.Y >= area.Y && p.X < area.X + area.Width && p.Y < area.Y + area.Height;

    [LoggerMessage(Level = LogLevel.Warning, Message = "an event editor window could not be placed")]
    private static partial void LogPlaceFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "an event editor window could not be made")]
    private static partial void LogOpenFailed(ILogger logger, Exception error);
}
