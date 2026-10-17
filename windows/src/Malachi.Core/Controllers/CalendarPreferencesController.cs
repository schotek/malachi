// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarPreferencesController.swift;
// Swift-first, no GTK controller yet: the rows' texts and choices are
// ui/internal/calendar/preferences.go (Cal.Prefs) and editor.go
// (Cal.DurationChoices, Cal.AlertChoices).
//
// Preferences → Calendar → Display (docs/calendar.md §5.8, "As built (M9)")
// over calendar.preferences / calendar.setPreferences: Week Starts On,
// Default Duration, Default Alert, Show Declined Events, Working Hours (a
// start and an end at half hours plus seven day toggles, the last day
// staying on). A change shows at once and is written; a refusal takes it
// back and says why (Error). The daemon keeps the preferences (meta), not
// the settings.
//
// Windows: Swift's onChange is Changed. Swift sends every write at once
// and counts them; here the writes go one after another, as the board's
// preferences do (BoardPreferencesController), so calendar.setPreferences
// calls never overtake each other in the daemon. Each write is a partial
// update (a member left null keeps its value), laid over the preferences
// last known (the daemon's answer, else the source's snapshot) until its
// answer comes: Preferences is that with the writes under way. Deviation
// (named): a refused write takes back only its own change, the writes after
// it stay shown, where Swift falls back to the preferences before the
// refused one; with nothing else under way both show the source's snapshot.
// A write is a mutation: it is sent even when the controller is disposed
// meanwhile (PerformPastClose), its answer then dropped. Stop only stops
// following the source, as Swift's stop(); Dispose also drops the answers.
// The record compares its working days by reference, so the preferences are
// compared member by member (Same).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Controllers;

/// <summary>
/// The group Display of Preferences → Calendar over the daemon's calendar
/// preferences (Swift <c>CalendarPreferencesController</c>).
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; <see cref="Changed"/> is raised
/// there. Subscribe, then call <see cref="Start"/>. After every
/// <see cref="Changed"/> the page reads <see cref="Preferences"/>, the
/// choices, <see cref="Saving"/> and <see cref="Error"/> (the group's status
/// line) again.
/// </remarks>
public sealed partial class CalendarPreferencesController : IDisposable
{
    private static readonly IReadOnlyList<string> NoDays = [];

    private readonly ControllerScope scope;

    // The writes under way or waiting, in order; laid over stored.
    private readonly List<(int Token, CalendarPreferencesUpdate Update)> writes = [];

    // The preferences last known: the daemon's last answer, else the snapshot's.
    private Api.CalendarPreferences stored;
    private BoardObserverToken? observer;

    // The last write sent; the next one waits for it.
    private Task chain = Task.CompletedTask;
    private int nextToken;

    /// <summary>The group over <paramref name="source"/>, on the calling (UI) thread.</summary>
    /// <param name="source">The calendar's data and its writes.</param>
    /// <param name="env">The zone and names of the titles; the local zone and the current culture when null.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public CalendarPreferencesController(ICalendarDataSource source, Cal.Env? env = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        Env = env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);
        scope = new ControllerScope(pending);
        stored = source.Snapshot.Preferences;
        Preferences = stored;
    }

    /// <summary>What the group shows changed: it reads the controller again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>The calendar source.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The zone and names of the titles.</summary>
    public Cal.Env Env { get; }

    /// <summary>The preferences as shown: the written changes before their answers.</summary>
    public Api.CalendarPreferences Preferences { get; private set; }

    /// <summary>A write is under way or waiting.</summary>
    public bool Saving => writes.Count > 0;

    /// <summary>Why the last write was refused, for the group's status line; "" for none.</summary>
    public string Error { get; private set; } = "";

    /// <summary>Whether <see cref="Dispose"/> ran: answers are dropped.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Follows the source's snapshot while nothing is being written.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (observer is not null || IsClosed)
        {
            return;
        }
        observer = Source.Observe(c =>
        {
            if (c is not Cal.SourceChange.SnapshotChanged || writes.Count > 0 || Same(Preferences, Source.Snapshot.Preferences))
            {
                return;
            }
            stored = Source.Snapshot.Preferences;
            Publish();
        });
    }

    /// <summary>Stops following the source (Swift <c>stop()</c>); writes under way still report their answers.</summary>
    public void Stop()
    {
        scope.VerifyAccess();
        observer?.Cancel();
        observer = null;
    }

    /// <summary>Stops, and drops the answers of the writes under way (which are still sent).</summary>
    public void Dispose()
    {
        Stop();
        scope.Close();
    }

    // Rows

    /// <summary>Week Starts On: System, then the weekdays (<see cref="Cal.Prefs.WeekStarts"/>).</summary>
    public IReadOnlyList<Choice<Api.WeekStart>> WeekStartChoices =>
        [.. Cal.Prefs.WeekStarts.Select(w => new Choice<Api.WeekStart>(w, Cal.Prefs.WeekStartTitle(w, Env)))];

    /// <summary>Default Duration's choices, with the stored value when it is not one.</summary>
    public IReadOnlyList<Choice<int>> DurationChoices =>
        [.. WithValue(Cal.DurationChoices, Preferences.DefaultDurationMinutes).Select(m => new Choice<int>(m, Cal.DurationTitle(m)))];

    /// <summary>Default Alert's choices (-1 None), with the stored value when it is not one.</summary>
    public IReadOnlyList<Choice<int>> AlertChoices =>
        [.. WithValue(Cal.AlertChoices, Preferences.DefaultAlertMinutes).Select(m => new Choice<int>(m, Cal.AlertTitle(m)))];

    /// <summary>The working hours' value, "Every weekday · 08:00–17:00".</summary>
    public string WorkingHoursText => Cal.Prefs.WorkingHoursText(Preferences.WorkingHours, Env);

    // Changes

    /// <summary>Week Starts On.</summary>
    public void SetWeekStart(Api.WeekStart w)
    {
        if (w != Preferences.WeekStart)
        {
            Write(new CalendarPreferencesUpdate { WeekStart = w });
        }
    }

    /// <summary>Default Duration, in minutes.</summary>
    public void SetDefaultDuration(int minutes)
    {
        if (minutes != Preferences.DefaultDurationMinutes)
        {
            Write(new CalendarPreferencesUpdate { DefaultDurationMinutes = minutes });
        }
    }

    /// <summary>Default Alert, in minutes before (-1 none).</summary>
    public void SetDefaultAlert(int minutes)
    {
        if (minutes != Preferences.DefaultAlertMinutes)
        {
            Write(new CalendarPreferencesUpdate { DefaultAlertMinutes = minutes });
        }
    }

    /// <summary>Show Declined Events.</summary>
    public void SetShowDeclined(bool on)
    {
        if (on != Preferences.ShowDeclined)
        {
            Write(new CalendarPreferencesUpdate { ShowDeclined = on });
        }
    }

    /// <summary>The working hours, replaced whole.</summary>
    public void SetWorkingHours(WorkingHours wh)
    {
        ArgumentNullException.ThrowIfNull(wh);
        if (!SameWorkingHours(wh, Preferences.WorkingHours))
        {
            Write(new CalendarPreferencesUpdate { WorkingHours = wh });
        }
    }

    // Working hours

    /// <summary>The working days' toggles from Monday: the day code (<c>MO</c>) and the short weekday name.</summary>
    public IReadOnlyList<Choice<string>> WorkingDayChoices =>
    [
        // 2026-10-05 is a Monday.
        .. Enumerable.Range(0, 7).Select(i => new Choice<string>(
            Cal.DayCode((Cal.Weekday)((i + 1) % 7)),
            Cal.Text.ShortWeekdayText(Cal.Date(2026, 10, 5 + i, 12, 0, Env.Zone), Env))),
    ];

    /// <summary>Whether the day <paramref name="code"/> (<c>MO</c>) is a working day.</summary>
    public bool IsWorkingDay(string code) => Preferences.WorkingHours.Days.Contains(code, StringComparer.Ordinal);

    /// <summary>A day's toggle; the last working day stays on.</summary>
    public void SetWorkingDay(string code, bool on)
    {
        ArgumentNullException.ThrowIfNull(code);
        if (Cal.ParseDayCode(code) is not { Ordinal: 0 } || on == IsWorkingDay(code))
        {
            return;
        }
        var wh = Preferences.WorkingHours;
        IReadOnlyList<string> days;
        if (on)
        {
            var order = WorkingDayChoices.Select(c => c.Value).ToList();
            int Rank(string d) => order.IndexOf(d) is var i and >= 0 ? i : 7;
            days = [.. wh.Days.Append(code).OrderBy(Rank)];
        }
        else
        {
            if (wh.Days.Count <= 1)
            {
                return;
            }
            days = [.. wh.Days.Where(d => !string.Equals(d, code, StringComparison.Ordinal))];
        }
        SetWorkingHours(wh with { Days = days });
    }

    /// <summary>The start's and the end's choices: every half hour, with <paramref name="current"/> when it is not one.</summary>
    public IReadOnlyList<Choice<string>> WorkingTimeChoices(string? current)
    {
        var minutes = Enumerable.Range(0, 48).Select(i => i * 30).ToList();
        if (Cal.ParseClock(current) is { } m && !minutes.Contains(m))
        {
            minutes.Add(m);
            minutes.Sort();
        }
        return [.. minutes.Select(x => new Choice<string>(Clock(x), Cal.Text.TimeText(Cal.Date(2026, 10, 5, x / 60, x % 60, Env.Zone), Env)))];
    }

    /// <summary>The start (<c>HH:MM</c>); refused when it is not before the end.</summary>
    public void SetWorkingStart(string s)
    {
        if (Cal.ParseClock(s) is { } a && Cal.ParseClock(Preferences.WorkingHours.End) is { } b && a < b)
        {
            SetWorkingHours(Preferences.WorkingHours with { Start = s });
        }
    }

    /// <summary>The end (<c>HH:MM</c>); refused when it is not after the start.</summary>
    public void SetWorkingEnd(string s)
    {
        if (Cal.ParseClock(s) is { } b && Cal.ParseClock(Preferences.WorkingHours.Start) is { } a && a < b)
        {
            SetWorkingHours(Preferences.WorkingHours with { End = s });
        }
    }

    /// <summary>Whether <paramref name="a"/> and <paramref name="b"/> hold the same preferences, the working days compared in order (Swift's Equatable).</summary>
    public static bool Same(Api.CalendarPreferences? a, Api.CalendarPreferences? b)
    {
        if (a is null || b is null)
        {
            return ReferenceEquals(a, b);
        }
        return SameWorkingHours(a.WorkingHours, b.WorkingHours)
            && (a with { WorkingHours = b.WorkingHours }) == b;
    }

    // Swift's Equatable WorkingHours: the days in order.
    private static bool SameWorkingHours(WorkingHours a, WorkingHours b) =>
        a.Days.SequenceEqual(b.Days, StringComparer.Ordinal) && (a with { Days = NoDays }) == (b with { Days = NoDays });

    // "HH:MM" of minutes after midnight.
    private static string Clock(int m) => string.Create(CultureInfo.InvariantCulture, $"{m / 60:00}:{m % 60:00}");

    // The choices with value among them, sorted, when it is not one.
    private static List<int> WithValue(IReadOnlyList<int> choices, int value)
    {
        var v = choices.ToList();
        if (!v.Contains(value))
        {
            v.Add(value);
            v.Sort();
        }
        return v;
    }

    // An update laid over preferences: what it sets replaces, the rest stays.
    private static Api.CalendarPreferences Apply(Api.CalendarPreferences p, CalendarPreferencesUpdate u) => p with
    {
        WeekStart = u.WeekStart ?? p.WeekStart,
        DefaultDurationMinutes = u.DefaultDurationMinutes ?? p.DefaultDurationMinutes,
        DefaultAlertMinutes = u.DefaultAlertMinutes ?? p.DefaultAlertMinutes,
        ShowDeclined = u.ShowDeclined ?? p.ShowDeclined,
        WorkingHours = u.WorkingHours ?? p.WorkingHours,
    };

    // Shows the change at once and sends it after the writes before it.
    private void Write(CalendarPreferencesUpdate update)
    {
        scope.VerifyAccess();
        var token = ++nextToken;
        writes.Add((token, update));
        Error = "";
        Publish();
        var previous = chain;
        chain = scope.PerformPastClose(
            async () =>
            {
                // Its outcome was taken already; a failure of it is not this write's.
                await Task.WhenAny(previous);
                return await Source.SetPreferencesAsync(update);
            },
            outcome => Written(token, outcome));
    }

    // A write's answer: the daemon's preferences, or the refusal with the
    // write's change taken back.
    private void Written(int token, Outcome<Api.CalendarPreferences> outcome)
    {
        writes.RemoveAll(w => w.Token == token);
        if (outcome.Error is { } error)
        {
            if (writes.Count == 0)
            {
                stored = Source.Snapshot.Preferences;
            }
            Error = Cal.ErrorMessage(error);
        }
        else if (outcome.Value is { } answer)
        {
            stored = answer;
        }
        Publish();
    }

    // Preferences from what is known and the writes under way; Changed.
    private void Publish()
    {
        var p = stored;
        foreach (var w in writes)
        {
            p = Apply(p, w.Update);
        }
        Preferences = p;
        if (IsClosed || Changed is not { } handlers)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }
}
