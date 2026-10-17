// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardTodayCalendarStrip.swift
// (attach, refresh, sourceChanged, tick, update, show, rebuild, makeRow,
// openCalendar, respond) and the strip's part of
// BoardTodayViewController.swift (wireCalendarStrip, attachCalendarStrip);
// GTK: none yet (ui/internal/calendar/today.go, docs/calendar.md §5.9).
//
// The strip follows the main window's calendar source (started here, as the
// Calendar mode starts it, MainWindow.StartCalendar) while it is loaded:
// today's event.list, loaded once a day and again when the source drops an
// overlapping range or its snapshot changes, and a minute's tick that moves
// "took place" on and brings the next day at midnight. Open Calendar › is
// the Calendar's Day view of today; Respond selects the invitation's message
// in Mail, where its card answers it (Integration.ShowInvitationInMail, as
// macOS's reveal).
//
// Windows differences (the owner's wireframe, board 8): the unanswered
// invitations of the snapshot that today's event.list does not list are
// rows as well (Cal.OccurrenceOf, as the calendar's Unanswered filter makes
// them), with a hollow dashed dot and a small Invitation pill; a row is a
// button that shows its event in the Calendar (MainWindow.ShowInCalendar);
// a failed load still shows the invitations. A UserControl has no
// automation peer of its own: the strip is a group with its AutomationId
// (GroupPeer).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Controls;
using Malachi.App.Resources;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;

namespace Malachi.App.Boards;

/// <summary>The Board → Today strip of today's events.</summary>
public sealed partial class BoardTodayCalendarStrip : UserControl
{
    private const double TimeWidth = 96;
    private const double RowHeight = 28;
    private static readonly TimeSpan TickInterval = TimeSpan.FromMinutes(1);

    private readonly Cal.Env env = AppState.CalendarEnv();
    private AppState? state;
    private MainWindow? window;
    private ICalendarDataSource? source;
    private BoardObserverToken? token;
    private DispatcherQueueTimer? dayTimer;
    private EventListResult? lastResult;
    private DateTimeOffset? loadedDay;
    private bool loading;

    // Bumped when the strip lets go of its source: a load that answers
    // after it is dropped.
    private int generation;
    private IReadOnlyList<Cal.TodayRow> rows = [];

    /// <summary>A collapsed strip; it attaches itself when loaded.</summary>
    public BoardTodayCalendarStrip()
    {
        InitializeComponent();
        Heading.Text = Cal.TodayHeading.ToUpper(CultureInfo.CurrentCulture);
        AutomationProperties.SetName(Heading, Cal.TodayHeading);
        AutomationProperties.SetHeadingLevel(Heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level2);
        var open = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 2 };
        open.Children.Add(new TextBlock { Text = Cal.OpenCalendar, VerticalAlignment = VerticalAlignment.Center });
        open.Children.Add(new FontIcon { Glyph = Icons.Glyph("go-next"), FontSize = 10, VerticalAlignment = VerticalAlignment.Center });
        OpenButton.Content = open;
        AutomationProperties.SetName(OpenButton, Cal.OpenCalendar);
        Loaded += OnLoaded;
        Unloaded += OnUnloaded;
    }

    /// <summary>Some account has the calendar capability: the strip shows.</summary>
    public bool IsShown { get; private set; }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new GroupPeer(this);

    // attachCalendarStrip: the main window's source, started as the Calendar
    // mode starts it.
    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        if ((Application.Current as global::Malachi.App.App)?.State is not { } s || s.MainWindow is not { } main)
        {
            return;
        }
        state = s;
        window = main;
        main.StartCalendar();
        Attach(s.CalendarSourceFor(main));
        if (dayTimer is null)
        {
            // Midnight brings another day, and "took place" moves on.
            dayTimer = DispatcherQueue.CreateTimer();
            dayTimer.Interval = TickInterval;
            dayTimer.Tick += (_, _) => Tick();
        }
        dayTimer.Start();
        Update();
    }

    private void OnUnloaded(object sender, RoutedEventArgs e)
    {
        token?.Cancel();
        token = null;
        source = null;
        dayTimer?.Stop();
        generation++;
        loading = false;
    }

    // attach: follows next from now on.
    private void Attach(ICalendarDataSource next)
    {
        if (ReferenceEquals(source, next) && token is not null)
        {
            return;
        }
        token?.Cancel();
        source = next;
        loadedDay = null;
        token = next.Observe(SourceChanged);
    }

    private void SourceChanged(Cal.SourceChange change)
    {
        switch (change)
        {
            case Cal.SourceChange.SnapshotChanged:
                // The accounts, the visible calendars, the invitations
                // (notify.invitationsChanged reloads the snapshot).
                loadedDay = null;
                Update();
                break;
            case Cal.SourceChange.OccurrencesChanged(var from, var to):
                var day = Cal.StartOfDay(Now(), env.Zone);
                var end = Cal.AddDays(day, 1, env.Zone);
                if ((to is null || day < to) && (from is null || end > from))
                {
                    loadedDay = null;
                    Update();
                }
                break;
        }
    }

    private void Tick()
    {
        if (!IsShown)
        {
            return;
        }
        if (loadedDay != Cal.StartOfDay(Now(), env.Zone))
        {
            loadedDay = null;
        }
        Update();
    }

    // update: shown or collapsed, and today's occurrences loaded when they
    // are not loaded for today.
    private void Update()
    {
        if (source is not { } src)
        {
            return;
        }
        var shown = Cal.ShowsTodayStrip(src.Snapshot.Accounts);
        if (shown != IsShown)
        {
            IsShown = shown;
            Visibility = shown ? Visibility.Visible : Visibility.Collapsed;
        }
        if (!shown)
        {
            return;
        }
        var now = Now();
        var day = Cal.StartOfDay(now, env.Zone);
        var range = new Cal.Range(day, Cal.AddDays(day, 1, env.Zone), env.Zone);
        if (src.CachedOccurrences(range) is { } hit)
        {
            loadedDay = day;
            Show(hit, now);
            return;
        }
        if (loadedDay == day)
        {
            Show(null, now);
            return;
        }
        if (loading)
        {
            return;
        }
        loading = true;
        _ = LoadAsync(src, range, day, generation);
    }

    private async Task LoadAsync(ICalendarDataSource src, Cal.Range range, DateTimeOffset day, int gen)
    {
        EventListResult? result = null;
        try
        {
            result = await src.OccurrencesAsync(range);
        }
#pragma warning disable CA1031 // A failed load leaves the strip as it was (the source reports its errors itself).
        catch (Exception e) when (e is not OutOfMemoryException)
#pragma warning restore CA1031
        {
            result = null;
        }
        if (gen != generation)
        {
            return;
        }
        loading = false;
        if (result is not null)
        {
            loadedDay = day;
        }
        Show(result, Now());
    }

    // show: today's rows of what the source has, the hidden calendars left
    // out, the snapshot's unanswered invitations added.
    private void Show(EventListResult? result, DateTimeOffset now)
    {
        if (result is not null)
        {
            lastResult = result;
        }
        if (source is not { } src)
        {
            return;
        }
        var snapshot = src.Snapshot;
        var visible = snapshot.VisibleCalendars;
        var occurrences = (lastResult?.Occurrences ?? [])
            .Where(o => o.CalendarId is not { } id || visible.Contains(id))
            .ToList();
        var listed = occurrences
            .Where(o => o.Invitation is not null)
            .Select(o => (o.Invitation!.AccountId, o.Invitation.MessageId))
            .ToHashSet();
        foreach (var i in snapshot.Invitations)
        {
            if (!listed.Contains((i.AccountId, i.MessageId)))
            {
                occurrences.Add(Cal.OccurrenceOf(i));
            }
        }
        rows = Cal.TodayRows(occurrences, now, env, snapshot.Preferences.ShowDeclined);
        Rebuild(snapshot);
    }

    private void Rebuild(Cal.Snapshot snapshot)
    {
        Rows.Children.Clear();
        if (rows.Count == 0)
        {
            Rows.Children.Add(new TextBlock
            {
                Text = Cal.NothingToday,
                MinHeight = RowHeight,
                Padding = new Thickness(4, 4, 4, 4),
                Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"],
                Style = (Style)Application.Current.Resources["BodyLabelStyle"],
            });
            return;
        }
        foreach (var row in rows)
        {
            Rows.Children.Add(MakeRow(row, snapshot));
        }
    }

    // makeRow: the time, the dot, the title (with the pill of an invitation
    // not answered yet) in the row's button, then "took place", Respond or
    // the place.
    private Grid MakeRow(Cal.TodayRow row, Cal.Snapshot snapshot)
    {
        var o = row.Occurrence;
        var secondary = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"];
        var line = new Grid { ColumnSpacing = 8 };
        line.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(TimeWidth) });
        line.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        line.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        line.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });

        var time = new TextBlock
        {
            Text = row.Time,
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
        };
        if (row.TookPlace)
        {
            time.Foreground = secondary;
        }
        line.Children.Add(time);

        var unanswered = row.State == Cal.BlockState.Unanswered;
        var color = new SolidColorBrush(Calendars.CalendarPalette.Of(o, snapshot));
        var dot = new Ellipse
        {
            Width = 8,
            Height = 8,
            VerticalAlignment = VerticalAlignment.Center,
            Fill = unanswered ? null : color,
            Stroke = unanswered ? color : null,
            StrokeThickness = unanswered ? 1.5 : 0,
            StrokeDashArray = unanswered ? new DoubleCollection { 1.5, 1 } : null,
        };
        AutomationProperties.SetAccessibilityView(dot, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
        Grid.SetColumn(dot, 1);
        line.Children.Add(dot);

        var titleLine = new Grid { ColumnSpacing = 6 };
        titleLine.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        titleLine.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var title = new TextBlock
        {
            Text = row.Title,
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
        };
        if (row.TookPlace)
        {
            title.Foreground = secondary;
        }
        titleLine.Children.Add(title);
        if (row.Respond && o.Invitation is not null)
        {
            var pill = new InvitationPill { Kind = Cal.InvitationPill.Invitation };
            AutomationProperties.SetAccessibilityView(pill, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
            Grid.SetColumn(pill, 1);
            titleLine.Children.Add(pill);
        }
        Grid.SetColumn(titleLine, 2);
        line.Children.Add(titleLine);

        // The row's button: its event in the Calendar.
        var open = new Button
        {
            Content = line,
            Padding = new Thickness(4, 2, 4, 2),
            MinHeight = RowHeight,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
        };
        var trailing = row.TookPlace ? Cal.TookPlace : row.Respond && o.Invitation is not null ? "" : row.Place;
        AutomationProperties.SetAutomationId(open, "TodayCalendarRow:" + Cal.OccurrenceKey(o));
        AutomationProperties.SetName(open, string.Join(", ", new[] { row.Time, row.Title, trailing }.Where(t => t.Length > 0)));
        open.Click += (_, _) => ShowInCalendar(o);

        var r = new Grid { ColumnSpacing = 8 };
        r.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        r.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        r.Children.Add(open);
        FrameworkElement? end = null;
        if (row.TookPlace)
        {
            end = Trailing(Cal.TookPlace, secondary);
        }
        else if (row.Respond && o.Invitation is { } invitation)
        {
            var respond = new Button
            {
                Content = Cal.Respond,
                Padding = new Thickness(8, 1, 8, 2),
                FontSize = 12,
                VerticalAlignment = VerticalAlignment.Center,
            };
            AutomationProperties.SetAutomationId(respond, "TodayCalendarRespond:" + Cal.OccurrenceKey(o));
            respond.Click += (_, _) => state?.Integration?.ShowInvitationInMail(invitation);
            end = respond;
        }
        else if (row.Place.Length > 0)
        {
            end = Trailing(row.Place, secondary);
        }
        if (end is not null)
        {
            Grid.SetColumn(end, 1);
            r.Children.Add(end);
        }
        return r;
    }

    private static TextBlock Trailing(string text, Brush brush) => new()
    {
        Text = text,
        MaxWidth = 220,
        VerticalAlignment = VerticalAlignment.Center,
        MaxLines = 1,
        TextTrimming = TextTrimming.CharacterEllipsis,
        TextAlignment = TextAlignment.Right,
        Foreground = brush,
        Style = (Style)Application.Current.Resources["CaptionLabelStyle"],
    };

    // A row's click: the Calendar's Day view of its day with its event.
    private void ShowInCalendar(Occurrence o) =>
        window?.ShowInCalendar(o.EventId, o.RecurrenceId, o.Start ?? Cal.ParseDate(o.StartDate, env.Zone));

    // Open Calendar ›: the Calendar's Day view of today.
    private void OnOpenCalendarClick(object sender, RoutedEventArgs e) => window?.ShowInCalendar(null, null, Now());

    private static DateTimeOffset Now() => TimeProvider.System.GetUtcNow();

    // The strip as one group of the automation tree, named by its heading.
    private sealed partial class GroupPeer(BoardTodayCalendarStrip owner) : FrameworkElementAutomationPeer(owner)
    {
        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Group;

        protected override string GetClassNameCore() => nameof(BoardTodayCalendarStrip);

        protected override string GetNameCore() => Cal.TodayHeading;

        protected override bool IsControlElementCore() => true;
    }
}
