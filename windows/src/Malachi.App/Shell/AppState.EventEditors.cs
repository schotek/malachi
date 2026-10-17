// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift's
// EventEditorManager.shared (its state set when the application is made);
// GTK: the event editor follows (docs/calendar.md §6.4). The application's
// event editor windows (Calendars/EventEditor/EventEditorManager), made on
// first use: AppState.OpenNewEvent and OpenEditEvent open them, Quit asks
// those with changes (App.xaml.cs, QuitSteps.SaveDrafts).

using Malachi.App.Calendars;

namespace Malachi.App.Shell;

/// <summary>The event editor windows of the application.</summary>
public sealed partial class AppState
{
    private EventEditorManager? eventEditors;

    /// <summary>The New Event / Edit Event windows (macOS <c>EventEditorManager.shared</c>).</summary>
    internal EventEditorManager EventEditors => eventEditors ??= new EventEditorManager(this);
}
