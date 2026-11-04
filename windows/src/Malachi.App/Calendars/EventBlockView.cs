// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventBlockView.swift
// (EventBlockView); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// One timed occurrence in the Week or Day grid (docs/calendar.md §5.3): its
// calendar's colour with a 3 DIP bar at the left (Windows: the colour
// itself with white or near-black texts, BlockBackground), the title in
// bold, the time, the recurring symbol and the attendee count; in the Day
// view also the place and the video call's symbol. An unanswered
// invitation is a dashed outline with a "?" badge in the colour, tentative
// a light fill with a hatched bar (Windows: Outlook's), declined and
// cancelled are struck through on a faint tint (cancelled in fainter text,
// never see-through), the texts and symbols in the ink
// BlockBackground.Paint gives. What does not fit the block's
// height is left out, never squeezed. Named by Cal.BlockLabel; its
// AutomationId is "CalendarBlock:" and the occurrence's key.

using Malachi.App.Resources;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using Windows.UI.Text;

namespace Malachi.App.Calendars;

/// <summary>A timed occurrence of the Week and Day views (macOS <c>EventBlockView</c>).</summary>
internal sealed partial class EventBlockView : OccurrenceView
{
    /// <summary>The AutomationId's prefix of a block.</summary>
    public const string AutomationPrefix = "CalendarBlock:";

    // The heights of the title's line and of a small line.
    private const double TitleLine = 16;
    private const double SmallLine = 15;

    private readonly BlockBackground background = new();
    private readonly TextBlock title = new();
    private readonly TextBlock time = new();
    private readonly TextBlock place = new();
    private readonly StackPanel symbols = new() { Orientation = Orientation.Horizontal, Spacing = 3 };
    private readonly FontIcon question;

    /// <summary>
    /// The block of <paramref name="block"/>; <paramref name="wide"/> is the
    /// Day view, with room for the place and the video call.
    /// </summary>
    public EventBlockView(Cal.Block block, Color color, string label, Cal.Env env, bool wide)
        : base(block.Occurrence, block.State, color, label, AutomationPrefix)
    {
        Block = block;
        var o = Block.Occurrence;
        // Opaque: a cancelled event's title is fainter, never a see-through block.
        title.Style = CalendarStyles.Get(Block.State == Cal.BlockState.Cancelled ? "CalendarBlockTitleDimStyle" : "CalendarBlockTitleStyle");
        time.Style = CalendarStyles.Get("CalendarSmallTextStyle");
        place.Style = CalendarStyles.Get("CalendarSmallTextStyle");
        title.Text = CalendarDisplay.Title(o.Title);
        if (Block.State is Cal.BlockState.Declined or Cal.BlockState.Cancelled)
        {
            title.TextDecorations = TextDecorations.Strikethrough;
        }
        time.Text = Cal.Text.TimeRangeText(Block.Start, Block.End, env);
        place.Text = wide ? CalendarDisplay.Line(o.Location) : "";
        if (o.Recurring == true)
        {
            symbols.Children.Add(Symbol("view-refresh"));
        }
        if (wide && o.VideoCall == true)
        {
            symbols.Children.Add(Symbol("insert-link"));
        }
        if (o.AttendeeCount is > 0 and var n)
        {
            symbols.Children.Add(Symbol("system-users"));
            var count = new TextBlock { Style = CalendarStyles.Get("CalendarSmallTextStyle") };
            count.Text = n.ToString(System.Globalization.CultureInfo.CurrentCulture);
            symbols.Children.Add(count);
        }
        question = Icons.Create("dialog-question", 12);
        question.HorizontalAlignment = HorizontalAlignment.Right;
        question.VerticalAlignment = VerticalAlignment.Top;
        question.Margin = new Thickness(0, 3, 3, 0);
        question.IsHitTestVisible = false;
        question.Visibility = Block.State == Cal.BlockState.Unanswered ? Visibility.Visible : Visibility.Collapsed;

        var lines = new StackPanel
        {
            Margin = new Thickness(BlockBackground.BarWidth(Block.State) + 4, 2, question.Visibility == Visibility.Visible ? 17 : 3, 0),
            IsHitTestVisible = false,
        };
        lines.Children.Add(title);
        lines.Children.Add(time);
        lines.Children.Add(place);
        lines.Children.Add(symbols);
        var root = new Grid { Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
        root.Children.Add(background);
        root.Children.Add(lines);
        root.Children.Add(question);
        root.SizeChanged += (_, e) => Fit(e.NewSize.Height);
        Content = root;
        Repaint();
    }

    /// <summary>What Core placed: the occurrence, its minutes and its column.</summary>
    public Cal.Block Block { get; }

    /// <inheritdoc/>
    protected override void Paint(ElementTheme theme)
    {
        var ink = background.Paint(Color, State, Selected, theme, 4);
        BlockBackground.Ink(ink, title, time, place, symbols);
        // The "?" of an unanswered invitation: in the colour, shaded as its texts.
        question.Foreground = new SolidColorBrush(ink ?? Color);
    }

    // What does not fit the block's height is left out (macOS layout()).
    private void Fit(double height)
    {
        var room = height - 2;
        var y = 2 + TitleLine;
        time.Visibility = y + SmallLine <= room ? Visibility.Visible : Visibility.Collapsed;
        if (time.Visibility == Visibility.Visible)
        {
            y += SmallLine;
        }
        place.Visibility = place.Text.Length > 0 && y + SmallLine <= room ? Visibility.Visible : Visibility.Collapsed;
        if (place.Visibility == Visibility.Visible)
        {
            y += SmallLine;
        }
        symbols.Visibility = symbols.Children.Count > 0 && y + SmallLine <= room ? Visibility.Visible : Visibility.Collapsed;
    }

    private static FontIcon Symbol(string name)
    {
        var icon = Icons.Create(name, 10);
        icon.Style = CalendarStyles.Get("CalendarSymbolStyle");
        return icon;
    }
}
