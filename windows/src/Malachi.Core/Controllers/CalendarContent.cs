// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.calendar); Swift-first, no GTK
// counterpart yet (docs/calendar.md §5.9, "As built on macOS (M10)").

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

/// <summary>
/// The first event a list_events or get_event result of the turn named,
/// with Show in Calendar (<see cref="AssistantPanelController.OpenCalendarItem"/>).
/// </summary>
/// <param name="Calendar">The event, as the bridge's own lines name it (<see cref="Assistant.ParseCalendarResult"/>).</param>
public sealed record CalendarContent(Assistant.CalendarRef Calendar) : AssistantPanelContent;
