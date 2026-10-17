// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what the views of macos/Sources/MalachiMail/Calendar/
// CalendarPageViewController.swift's CalendarContentViewController share
// (update(controller:), view(for:)); GTK: the calendar's views follow
// (docs/calendar.md §6.4). The Week and Day view, the Month and the Agenda
// (with the Unanswered filter and the search results) behind the page's
// content, as the Board's styles stand behind IBoardStyleContent.

using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;

namespace Malachi.App.Calendars;

/// <summary>A view of the calendar page's content.</summary>
internal interface ICalendarView
{
    /// <summary>Reads the controller's state after its Changed; rebuilds only what changed.</summary>
    void Update(CalendarController controller);

    /// <summary>The element of the occurrence <paramref name="key"/> on screen (a block, a bar, a pill, a row), for the detail's anchor; null when it is not shown.</summary>
    FrameworkElement? ElementFor(string key);

    /// <summary>Gives the keyboard to the selected occurrence (else the view's first); false when the view has nothing to focus.</summary>
    bool FocusSelection();
}
