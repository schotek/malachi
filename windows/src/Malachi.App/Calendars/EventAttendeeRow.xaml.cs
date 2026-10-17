// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventDetailViews.swift
// (EventDetailAttendeeRow: init, symbol); GTK: the event detail follows
// (docs/calendar.md §6.4), the row is ui/internal/calendar/detail.go
// (AttendeeRow, ResponseLabel, OrganizerLabel, OptionalLabel).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Calendars;

/// <summary>One attendee of the event detail.</summary>
public sealed partial class EventAttendeeRow : UserControl
{
    /// <summary>The row of <paramref name="a"/> (its strings are Core's, cleaned).</summary>
    public EventAttendeeRow(Cal.AttendeeRow a)
    {
        ArgumentNullException.ThrowIfNull(a);
        InitializeComponent();
        Icon(a.Response).Visibility = Visibility.Visible;
        var name = CalendarDisplay.Line(a.Name.Length == 0 ? a.Address : a.Name);
        NameText.Text = name;
        if (a.Address.Length > 0)
        {
            ToolTipService.SetToolTip(NameText, CalendarDisplay.Line(a.Address));
        }
        var labels = new List<string>();
        if (a.Organizer)
        {
            labels.Add(Cal.OrganizerLabel);
        }
        if (a.Optional)
        {
            labels.Add(Cal.OptionalLabel);
        }
        var extra = string.Join(", ", labels);
        ExtraText.Text = extra;
        ExtraText.Visibility = extra.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        // What Narrator reads: the name, the answer and the labels (macOS's
        // accessibility label).
        var label = Cal.Text.Dot(name, a.ResponseText);
        if (extra.Length > 0)
        {
            label = Cal.Text.Dot(label, extra);
        }
        AutomationProperties.SetName(NameText, label);
    }

    // EventDetailAttendeeRow.symbol: the glyph of an answer.
    private FontIcon Icon(EventResponse r) => r.Value switch
    {
        EventResponse.Accepted => AcceptedIcon,
        EventResponse.Declined => DeclinedIcon,
        EventResponse.Tentative => TentativeIcon,
        EventResponse.Delegated => DelegatedIcon,
        _ => NoReplyIcon,
    };
}
