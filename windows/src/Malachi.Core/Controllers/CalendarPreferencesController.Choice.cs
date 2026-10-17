// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarPreferencesController.swift
// (Choice); Swift-first, no GTK controller yet (the rules are
// ui/internal/calendar/preferences.go, Cal.Prefs).

namespace Malachi.Core.Controllers;

/// <content>A popup's choices.</content>
public sealed partial class CalendarPreferencesController
{
    /// <summary>A popup choice (or a working day's toggle): its value and its title.</summary>
    /// <typeparam name="T">The value's type.</typeparam>
    /// <param name="Value">What the choice sets.</param>
    /// <param name="Title">What the popup shows, translated.</param>
    public sealed record Choice<T>(T Value, string Title);
}
