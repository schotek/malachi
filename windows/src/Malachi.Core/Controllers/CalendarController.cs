// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarController.swift
// (CalendarController; its Cal extension is Calendars/CalendarControllerTypes.cs,
// its nested types CalendarController.Sidebar.cs); Swift-first, no GTK
// counterpart yet (docs/calendar.md §5.3–5.4).
//
// The calendar page of one main window: the view (Day, Week, Month,
// Agenda; Settings calendar-view), the anchor date and its range, the
// occurrences of the range, the selection, the search (from 2 characters,
// 300 ms after the last keystroke), the Unanswered filter (the unanswered
// invitations as an agenda), the sidebar's model and the status bar's
// texts. The views read the state after Changed and send what the user
// did; nothing here knows WinUI. What the views must present (the detail
// flyout, the editor, a re-sign-in) leaves through the hand-off events.
//
// Windows: Swift's onChange, onError, onOpenDetail, onRemove, onNewEvent,
// onClosePopover and onSignIn are the events Changed, ToastRequested,
// OpenDetailRequested, RemoveRequested, NewEventRequested,
// ClosePopoverRequested and SignInRequested, raised so that a view's
// failing handler cannot break the controller (ControllerEvents). Its now
// closure is the injected TimeProvider; its Settings the SettingsStore;
// its locale's first weekday is Cal.LocaleFirstDay of the current culture
// unless passed. The loads and the search go through a ControllerScope:
// the debounce waits detached on the injected sleep (a wait on a fake clock
// must not hold IdleAsync), the calls are tracked. "From 2 characters"
// counts text elements, as Swift's String.count counts characters. Create
// it, and call it, on the UI thread.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>The calendar page of one main window (Swift <c>CalendarController</c>).</summary>
public sealed partial class CalendarController : IDisposable
{
    /// <summary>The minimum of characters the search starts at.</summary>
    public const int SearchMinimum = 2;

    /// <summary>The agenda's page in days (<see cref="Cal.AgendaDays"/>); <see cref="LoadMoreAgenda"/> adds one.</summary>
    public const int AgendaPage = Cal.AgendaDays;

    /// <summary>How long the search waits after the last change of the field.</summary>
    public static readonly TimeSpan SearchDebounce = TimeSpan.FromMilliseconds(300);

    private readonly SettingsStore? settings;
    private readonly TimeProvider time;
    private readonly Cal.Weekday localeFirstDay;
    private readonly Cal.Sleep sleep;
    private readonly ControllerScope scope;

    private BoardObserverToken? observer;
    private int loadGeneration;

    // The search waiting for its debounce or its answer (Swift's searchTask):
    // cancelled when the field changes again; null when none is.
    private CancellationTokenSource? searchStop;
    private int searchGeneration;

    /// <summary>A controller over <paramref name="source"/>, on the calling (UI) thread; it asks for nothing before <see cref="Start"/>.</summary>
    /// <param name="source">The calendar's data and writes.</param>
    /// <param name="settings">Where the view is kept (<c>calendar-view</c>); the default view, not kept, when null.</param>
    /// <param name="env">The zone and the date formats; the local zone and the current culture when null.</param>
    /// <param name="time">The clock of "now" and of the debounce; the system's when null.</param>
    /// <param name="localeFirstDay">The locale's first weekday (week start System); the current culture's when null.</param>
    /// <param name="sleep">The search's debounce; <see cref="Cal.SleepOn"/> of <paramref name="time"/> when null.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public CalendarController(
        ICalendarDataSource source,
        SettingsStore? settings = null,
        Cal.Env? env = null,
        TimeProvider? time = null,
        Cal.Weekday? localeFirstDay = null,
        Cal.Sleep? sleep = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        this.settings = settings;
        Env = env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);
        this.time = time ?? TimeProvider.System;
        this.localeFirstDay = localeFirstDay ?? Cal.LocaleFirstDay(CultureInfo.CurrentCulture);
        this.sleep = sleep ?? Cal.SleepOn(this.time);
        scope = new ControllerScope(pending);
        View = settings?.CalendarView ?? Cal.DefaultView;
        var today = Cal.StartOfDay(Now, Env.Zone);
        Anchor = today;
        MiniMonthAnchor = today;
    }

    // Outputs

    /// <summary>The state changed; the views read it again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>A short sentence for a toast: a refused visibility change, a failed load or search (Swift <c>onError</c>).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>Return on the selection, a click on a block: the detail flyout of this occurrence (Swift <c>onOpenDetail</c>).</summary>
    public event EventHandler<Occurrence>? OpenDetailRequested;

    /// <summary>Delete on the selection: the detail's remove flow for this occurrence (Swift <c>onRemove</c>).</summary>
    public event EventHandler<Occurrence>? RemoveRequested;

    /// <summary>New Event (the key, "+", a double click): the editor with these defaults (Swift <c>onNewEvent</c>).</summary>
    public event EventHandler<Cal.Defaults>? NewEventRequested;

    /// <summary>Escape with a flyout open: close it (Swift <c>onClosePopover</c>).</summary>
    public event EventHandler? ClosePopoverRequested;

    /// <summary>
    /// Sign In… of a state row: the existing re-sign-in flow of the account
    /// with <c>calendar: true</c> (<c>account.oauthStart</c>; Swift <c>onSignIn</c>).
    /// </summary>
    public event EventHandler<AccountId>? SignInRequested;

    /// <summary>The calendar's data and writes.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The zone and the date formats.</summary>
    public Cal.Env Env { get; }

    // State

    /// <summary>Day, Week, Month or Agenda.</summary>
    public Cal.View View { get; private set; }

    /// <summary>A day in the shown range (the range is computed from it).</summary>
    public DateTimeOffset Anchor { get; private set; }

    /// <summary>The month the sidebar shows (its arrows change it alone).</summary>
    public DateTimeOffset MiniMonthAnchor { get; private set; }

    /// <summary>What the content shows besides the view.</summary>
    public ContentFilter Filter { get; private set; }

    /// <summary>The occurrences of the range (of the visible calendars).</summary>
    public IReadOnlyList<Occurrence> Occurrences { get; private set; } = [];

    /// <summary>event.list said <c>complete: false</c> for a part of the range.</summary>
    public bool Incomplete { get; private set; }

    /// <summary>event.list said <c>truncated</c>.</summary>
    public bool Truncated { get; private set; }

    /// <summary>An event.list is on its way.</summary>
    public bool Loading { get; private set; }

    /// <summary>The occurrence key of the selection (<see cref="Cal.OccurrenceKey"/>); null for none.</summary>
    public string? Selection { get; private set; }

    /// <summary>The search field as typed.</summary>
    public string SearchText { get; private set; } = "";

    /// <summary>event.search's answer for <see cref="SearchText"/>; null while not searching.</summary>
    public IReadOnlyList<Occurrence>? SearchResults { get; private set; }

    /// <summary>event.search's total.</summary>
    public int SearchTotal { get; private set; }

    /// <summary>The agenda's pages (from the anchor, <see cref="AgendaPage"/> days each).</summary>
    public int AgendaPages { get; private set; } = 1;

    /// <summary>A search is waiting for its debounce or its answer (the tests).</summary>
    public bool SearchPending => searchStop is not null;

    /// <summary>Nothing is on its way: no load, no search (the tests).</summary>
    public bool IsIdle => !Loading && !SearchPending;

    private DateTimeOffset Now => time.GetUtcNow();

    /// <summary>Starts following the source and loads the range.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (observer is not null || scope.IsClosed)
        {
            return;
        }
        observer = Source.Observe(SourceChanged);
        Reload();
    }

    /// <summary>Stops following the source; no answer is taken any more.</summary>
    public void Stop()
    {
        scope.VerifyAccess();
        observer?.Cancel();
        observer = null;
        loadGeneration++;
        CancelSearch();
    }

    /// <summary>Stops, for good.</summary>
    public void Dispose()
    {
        if (scope.IsClosed)
        {
            return;
        }
        Stop();
        scope.Close();
    }

    // Derived state

    /// <summary>What the source knows now.</summary>
    public Cal.Snapshot Snapshot => Source.Snapshot;

    /// <summary>The week's first day: the preference, else the locale's.</summary>
    public Cal.Weekday WeekStart => Cal.WeekStart(Snapshot.Preferences.WeekStart, localeFirstDay);

    /// <summary>The view's range (<see cref="Cal.ViewRange"/>); the agenda's covers its pages.</summary>
    public Cal.Range Range
    {
        get
        {
            var r = Cal.ViewRange(View, Anchor, WeekStart, Env.Zone);
            if (View == Cal.View.Agenda && AgendaPages > 1)
            {
                r = r with { To = Cal.AddDays(r.From, AgendaPage * AgendaPages, Env.Zone) };
            }
            return r;
        }
    }

    /// <summary>The window's title.</summary>
    public string Title => Cal.Title(View, Range, Env);

    /// <summary>The window's subtitle.</summary>
    public string Subtitle => Cal.Subtitle(View, Range, WeekStart, Env);

    /// <summary>The preference: declined events are shown.</summary>
    public bool ShowDeclined => Snapshot.Preferences.ShowDeclined;

    /// <summary>The search field has a query the search runs for.</summary>
    public bool Searching => Characters(Cal.TrimSpace(SearchText)) >= SearchMinimum;

    /// <summary>The selected occurrence among those shown; null for none.</summary>
    public Occurrence? SelectedOccurrence =>
        Selection is { } s ? ShownOccurrences.FirstOrDefault(o => Cal.OccurrenceKey(o) == s) : null;

    /// <summary>
    /// What the content lists: the search results, the unanswered
    /// invitations, else the range's occurrences.
    /// </summary>
    public IReadOnlyList<Occurrence> ShownOccurrences
    {
        get
        {
            if (SearchResults is { } results)
            {
                return results;
            }
            if (Filter == ContentFilter.Unanswered)
            {
                return [.. Snapshot.Invitations.Select(Cal.OccurrenceOf)];
            }
            return Occurrences;
        }
    }

    /// <summary>
    /// The agenda of the Agenda view and of the Unanswered filter (null
    /// otherwise); the search results are listed in their own order.
    /// </summary>
    public IReadOnlyList<Cal.AgendaDay>? Agenda
    {
        get
        {
            if (Filter == ContentFilter.Unanswered)
            {
                var from = Cal.StartOfDay(Now, Env.Zone);
                return Cal.Agenda(ShownOccurrences, from, API.Limits.CalendarFutureDays, Now, Env, showDeclined: true);
            }
            if (View != Cal.View.Agenda)
            {
                return null;
            }
            var r = Range;
            return Cal.Agenda(Occurrences, r.From, Cal.DaysBetween(r.From, r.To, Env.Zone), Now, Env, ShowDeclined);
        }
    }

    /// <summary>The heading above the list in place of the view ("Results", "Invitations"; "" none).</summary>
    public string ListHeading
    {
        get
        {
            if (SearchResults is not null)
            {
                return Cal.ResultsHeading;
            }
            return Filter == ContentFilter.Unanswered ? Cal.UnansweredHeading : "";
        }
    }

    /// <summary>The text of an empty list ("" when there is something to show).</summary>
    public string EmptyListText
    {
        get
        {
            if (SearchResults is { } r)
            {
                return r.Count == 0 ? Cal.NoResults : "";
            }
            if (View == Cal.View.Agenda || Filter == ContentFilter.Unanswered)
            {
                return (Agenda ?? []).Count == 0 ? Cal.AgendaEmpty : "";
            }
            return "";
        }
    }

    /// <summary>Why the page has nothing to show (null: it has).</summary>
    public Cal.EmptyKind? EmptyKind
    {
        get
        {
            var s = Snapshot;
            switch (s.Phase)
            {
                case Cal.Phase.Unavailable or Cal.Phase.Unsupported:
                    if (s.CalendarAccounts.Count == 0)
                    {
                        return Cal.EmptyKind.NoBackend;
                    }
                    break;
                case Cal.Phase.Loading:
                    return Cal.EmptyKind.Loading;
            }
            if (s.CalendarAccounts.Count == 0)
            {
                return s.Phase == Cal.Phase.Failed ? Cal.EmptyKind.NoBackend : Cal.EmptyKind.NoAccounts;
            }
            if (s.CalendarAccounts.All(a => !a.Sync))
            {
                return Cal.EmptyKind.SyncOff;
            }
            return null;
        }
    }

    /// <summary>The sidebar.</summary>
    public SidebarModel Sidebar
    {
        get
        {
            var s = Snapshot;
            // Disabled calendars are only in Preferences → Calendar.
            var accounts = s.ActiveCalendarAccounts.Select(a =>
            {
                var (line, signIn) = StateRow(a.State);
                return new SidebarAccount
                {
                    AccountId = a.AccountId,
                    Account = s.FindAccount(a.AccountId),
                    Name = s.AccountName(a.AccountId),
                    Source = a.Source,
                    Calendars = [.. a.Calendars.Select(c => new SidebarCalendar(c.Id, Cal.CleanLine(c.Name), c.Color, c.Visible, Cal.Prefs.CalendarRowNote(c)))],
                    StateLine = line,
                    OffersSignIn = signIn,
                };
            });
            return new SidebarModel
            {
                MiniMonth = new Cal.MiniMonth(MiniMonthAnchor, WeekStart, Now, Range, Env),
                InvitationsTitle = Cal.UnansweredHeading,
                UnansweredTitle = Cal.Text.UnansweredRow,
                Unanswered = s.Unanswered,
                Accounts = [.. accounts],
                AddCalendar = Cal.Prefs.AddCalendar,
                AddCalDAV = Cal.Prefs.AddCalDAV,
                SyncMicrosoft = Cal.Prefs.SyncMicrosoft,
            };
        }
    }

    /// <summary>The status bar's left text: the calendars' state.</summary>
    public string StatusLine => Cal.Text.StatusLine(Snapshot.States, Now, Env.Zone);

    /// <summary>The status bar's right text: the unanswered count ("" for none).</summary>
    public string UnansweredLine => Snapshot.Unanswered > 0 ? Cal.Text.UnansweredCount(Snapshot.Unanswered) : "";

    // The consent or state row of an account, and whether it offers Sign In….
    private static (string Line, bool SignIn) StateRow(CalendarSyncState state) => state.Status.Value switch
    {
        CalendarSyncStatus.ConsentRequired => (Cal.Text.ConsentLine, true),
        CalendarSyncStatus.AuthRequired => (
            Cal.Text.ErrorText(ErrorCode.AuthRequired) ?? RpcErrorText.Text(Cal.Text.ModeName, new RpcError { Code = ErrorCode.AuthRequired }),
            true),
        CalendarSyncStatus.Offline => (Cal.Text.OfflineLine, false),
        CalendarSyncStatus.Error => (state.Error is { } e ? Cal.ErrorMessage(new RpcException(e)) : "", false),
        _ => ("", false),
    };

    // Navigation

    /// <summary>Day, Week, Month or Agenda (kept in the settings); ends the Unanswered filter.</summary>
    public void SetView(Cal.View v)
    {
        scope.VerifyAccess();
        if (v == View && Filter == ContentFilter.None)
        {
            return;
        }
        // The Agenda starts at today when switched to; every other switch
        // keeps the anchor (Cal.SwitchAnchor).
        Anchor = Cal.SwitchAnchor(View, v, Anchor, Now, Env.Zone);
        MiniMonthAnchor = Anchor;
        View = v;
        Filter = ContentFilter.None;
        AgendaPages = 1;
        if (settings is not null)
        {
            settings.CalendarView = v;
        }
        Reload();
    }

    /// <summary>The key or Today: the range of today.</summary>
    public void Today() => GoTo(Now);

    /// <summary>‹ › and their keys: the previous or next range.</summary>
    public void Step(int dir)
    {
        scope.VerifyAccess();
        Anchor = Cal.Step(View, Anchor, dir < 0 ? -1 : 1, Env.Zone);
        MiniMonthAnchor = Anchor;
        AgendaPages = 1;
        Filter = ContentFilter.None;
        Reload();
    }

    /// <summary>A day of the mini month, the Today strip's Open Calendar, a notification: the range of that day in the current view.</summary>
    public void GoTo(DateTimeOffset day)
    {
        scope.VerifyAccess();
        Anchor = Cal.StartOfDay(day, Env.Zone);
        MiniMonthAnchor = Anchor;
        AgendaPages = 1;
        Filter = ContentFilter.None;
        Reload();
    }

    /// <summary>Shows <paramref name="day"/> in <paramref name="view"/> (the Day view) and selects <paramref name="select"/> there (Show in Calendar, an alert's click).</summary>
    public void Show(DateTimeOffset day, Cal.View view = Cal.View.Day, string? select = null)
    {
        scope.VerifyAccess();
        var target = view == Cal.View.Agenda ? Cal.SwitchAnchor(View, view, day, Now, Env.Zone) : day;
        View = view;
        if (settings is not null)
        {
            settings.CalendarView = view;
        }
        Selection = select;
        GoTo(target);
    }

    /// <summary>The mini month's arrows: its month alone.</summary>
    public void StepMiniMonth(int dir)
    {
        scope.VerifyAccess();
        MiniMonthAnchor = Cal.MiniMonth.Step(MiniMonthAnchor, dir < 0 ? -1 : 1, Env.Zone);
        RaiseChanged();
    }

    /// <summary>Agenda: the next page (infinite scrolling).</summary>
    public void LoadMoreAgenda()
    {
        scope.VerifyAccess();
        if (View != Cal.View.Agenda || Filter != ContentFilter.None || SearchResults is not null)
        {
            return;
        }
        AgendaPages++;
        Reload();
    }

    /// <summary>The INVITATIONS row: the unanswered invitations as an agenda.</summary>
    public void ShowUnanswered()
    {
        scope.VerifyAccess();
        Filter = ContentFilter.Unanswered;
        ClearSearchQuietly();
        Selection = null;
        RaiseChanged();
    }

    /// <summary>Back from the Unanswered filter.</summary>
    public void ClearFilter()
    {
        scope.VerifyAccess();
        if (Filter == ContentFilter.None)
        {
            return;
        }
        Filter = ContentFilter.None;
        RaiseChanged();
    }

    // Calendars

    /// <summary>A calendar's checkbox (calendar.update <c>visible</c>).</summary>
    public void SetVisible(bool visible, CalendarId calendar)
    {
        scope.VerifyAccess();
        Source.SetVisible(visible, calendar);
    }

    /// <summary>Sign In… of a state row.</summary>
    public void SignIn(AccountId account)
    {
        scope.VerifyAccess();
        scope.Raise(SignInRequested, this, account);
    }

    // Selection

    /// <summary>Selects an occurrence by key (null clears).</summary>
    public void Select(string? key)
    {
        scope.VerifyAccess();
        if (key == Selection)
        {
            return;
        }
        Selection = key;
        RaiseChanged();
    }

    /// <summary>The arrows: the previous or next occurrence in time order.</summary>
    public void MoveSelection(int delta)
    {
        var order = Cal.SelectionOrder(ShownOccurrences, Env.Zone);
        var next = Cal.MoveSelection(order, Selection ?? "", delta);
        Select(next.Length == 0 ? null : next);
    }

    /// <summary>Opens the detail of an occurrence (a click, Return).</summary>
    public void OpenDetail(Occurrence o)
    {
        ArgumentNullException.ThrowIfNull(o);
        Select(Cal.OccurrenceKey(o));
        scope.Raise(OpenDetailRequested, this, o);
    }

    // New events

    /// <summary>New Event: defaults for a start at <paramref name="at"/> from <paramref name="origin"/> (the next half hour from now for <see cref="Cal.Origin.Now"/>).</summary>
    public void NewEvent(DateTimeOffset? at = null, Cal.Origin origin = Cal.Origin.Now)
    {
        scope.VerifyAccess();
        scope.Raise(NewEventRequested, this, NewEventDefaults(at, origin));
    }

    /// <summary>The editor's defaults for a start at <paramref name="at"/> (now when null) from <paramref name="origin"/>.</summary>
    public Cal.Defaults NewEventDefaults(DateTimeOffset? at = null, Cal.Origin origin = Cal.Origin.Now) =>
        Cal.NewEventDefaults(at ?? Now, origin, Snapshot.Preferences, Env.Zone);

    // Search

    /// <summary>
    /// The search field changed: event.search <see cref="SearchDebounce"/>
    /// after the last change from <see cref="SearchMinimum"/> characters;
    /// fewer end the search.
    /// </summary>
    public void SetSearch(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        scope.VerifyAccess();
        if (text == SearchText)
        {
            return;
        }
        SearchText = text;
        CancelSearch();
        searchGeneration++;
        var q = Cal.TrimSpace(text);
        if (Characters(q) < SearchMinimum)
        {
            if (SearchResults is not null)
            {
                SearchResults = null;
                SearchTotal = 0;
            }
            RaiseChanged();
            return;
        }
        var generation = searchGeneration;
        var stop = new CancellationTokenSource();
        searchStop = stop;
        // Taken now: the source is disposed when the field changes again,
        // maybe before the work below starts.
        var cancelled = stop.Token;
        // The wait is not tracked (a fake clock would hold IdleAsync); the
        // call after it is.
        scope.RunDetached(async lifetime =>
        {
            await sleep(SearchDebounce, cancelled);
            if (cancelled.IsCancellationRequested || generation != searchGeneration)
            {
                return;
            }
            var p = new EventSearchParams { Query = q, Limit = API.Limits.MaxEventSearchResults };
            _ = scope.Perform(_ => Source.SearchAsync(p), outcome =>
            {
                if (generation != searchGeneration)
                {
                    return;
                }
                if (outcome.TryGetValue(out var r, out var error))
                {
                    SearchResults = r.Occurrences;
                    SearchTotal = r.Total;
                    Selection = null;
                }
                else
                {
                    SearchResults = [];
                    SearchTotal = 0;
                    scope.Raise(ToastRequested, this, Cal.ErrorMessage(error));
                }
                if (ReferenceEquals(searchStop, stop))
                {
                    searchStop = null;
                    stop.Dispose();
                }
                RaiseChanged();
            });
        });
        RaiseChanged();
    }

    /// <summary>Escape after the flyout, or the field's ×.</summary>
    public void ClearSearch()
    {
        scope.VerifyAccess();
        if (SearchText.Length == 0 && SearchResults is null)
        {
            return;
        }
        ClearSearchQuietly();
        RaiseChanged();
    }

    private void ClearSearchQuietly()
    {
        CancelSearch();
        searchGeneration++;
        SearchText = "";
        SearchResults = null;
        SearchTotal = 0;
    }

    private void CancelSearch()
    {
        if (searchStop is not { } stop)
        {
            return;
        }
        searchStop = null;
        stop.Cancel();
        stop.Dispose();
    }

    // Swift's String.count: characters, not UTF-16 units.
    private static int Characters(string s) => new StringInfo(s).LengthInTextElements;

    // Keys

    /// <summary>
    /// A key in Calendar mode (<see cref="Cal.KeyFor"/>); true when it was the
    /// calendar's. The key that shows the Calendar is the window's and returns
    /// false.
    /// </summary>
    public bool HandleKey(Cal.Press p, bool inText = false, bool popoverOpen = false)
    {
        scope.VerifyAccess();
        var ctx = new Cal.KeyContext(Board.Mode.Calendar, inText, SelectedOccurrence is not null);
        if (Cal.KeyFor(p, ctx) is not { } action)
        {
            return false;
        }
        switch (action)
        {
            case Cal.KeyAction.ShowCalendar:
                return false;
            case Cal.KeyAction.Today:
                Today();
                break;
            case Cal.KeyAction.Previous:
                Step(-1);
                break;
            case Cal.KeyAction.Next:
                Step(1);
                break;
            case Cal.KeyAction.DayView:
                SetView(Cal.View.Day);
                break;
            case Cal.KeyAction.WeekView:
                SetView(Cal.View.Week);
                break;
            case Cal.KeyAction.MonthView:
                SetView(Cal.View.Month);
                break;
            case Cal.KeyAction.AgendaView:
                SetView(Cal.View.Agenda);
                break;
            case Cal.KeyAction.NewEvent:
                NewEvent();
                break;
            case Cal.KeyAction.OpenDetail:
                if (SelectedOccurrence is not { } open)
                {
                    return false;
                }
                scope.Raise(OpenDetailRequested, this, open);
                break;
            case Cal.KeyAction.Remove:
                if (SelectedOccurrence is not { } remove)
                {
                    return false;
                }
                scope.Raise(RemoveRequested, this, remove);
                break;
            case Cal.KeyAction.Escape:
                switch (Cal.EscapeFor(popoverOpen, searching: SearchText.Length > 0 || SearchResults is not null))
                {
                    case Cal.EscapeTarget.ClosePopover:
                        Raise(ClosePopoverRequested);
                        break;
                    case Cal.EscapeTarget.ClearSearch:
                        ClearSearch();
                        break;
                    default:
                        if (Filter == ContentFilter.None)
                        {
                            return false;
                        }
                        ClearFilter();
                        break;
                }
                break;
            case Cal.KeyAction.SelectPrevious:
                MoveSelection(-1);
                break;
            case Cal.KeyAction.SelectNext:
                MoveSelection(1);
                break;
        }
        return true;
    }

    // Loading

    /// <summary>Loads the range's occurrences (the cached ones show at once).</summary>
    public void Reload()
    {
        scope.VerifyAccess();
        loadGeneration++;
        var generation = loadGeneration;
        var r = Range;
        if (Source.CachedOccurrences(r) is { } hit)
        {
            Take(hit);
        }
        Loading = true;
        RaiseChanged();
        scope.Perform(_ => Source.OccurrencesAsync(r), outcome =>
        {
            if (generation != loadGeneration)
            {
                return;
            }
            Loading = false;
            if (outcome.TryGetValue(out var res, out var err))
            {
                Take(res);
            }
            else
            {
                scope.Raise(ToastRequested, this, Cal.ErrorMessage(err));
            }
            RaiseChanged();
        });
    }

    private void Take(EventListResult r)
    {
        var visible = Snapshot.VisibleCalendars;
        Occurrences = [.. r.Occurrences.Where(o => o.CalendarId is not { } c || visible.Contains(c))];
        Incomplete = !r.Complete;
        Truncated = r.Truncated;
        if (Selection is { } s && Filter == ContentFilter.None && SearchResults is null
            && !Occurrences.Any(o => Cal.OccurrenceKey(o) == s))
        {
            Selection = null;
        }
    }

    private void SourceChanged(Cal.SourceChange c)
    {
        switch (c)
        {
            case Cal.SourceChange.SnapshotChanged:
                // A calendar shown or hidden: the cached list without it at
                // once, the daemon's answer after its notification.
                if (Source.CachedOccurrences(Range) is { } hit)
                {
                    Take(hit);
                }
                else
                {
                    var visible = Snapshot.VisibleCalendars;
                    Occurrences = [.. Occurrences.Where(o => o.CalendarId is not { } id || visible.Contains(id))];
                }
                RaiseChanged();
                break;
            case Cal.SourceChange.OccurrencesChanged o:
                var r = Range;
                if (Cal.RangesOverlap(r.From, r.To, o.From, o.To))
                {
                    Reload();
                }
                break;
            case Cal.SourceChange.Failed f:
                scope.Raise(ToastRequested, this, f.Message);
                break;
        }
    }

    private void RaiseChanged() => Raise(Changed);

    private void Raise(EventHandler? handlers)
    {
        if (handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }
}
