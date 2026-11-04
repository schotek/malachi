// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what macos/Sources/MalachiMail/Calendar/EventBlockView.swift,
// CalendarAllDayView.swift (AllDayBarView) and MonthCellView.swift
// (MonthPillView, MonthSpanBarView) share: the occurrence, its key
// (Cal.OccurrenceKey), its state and colour, the spoken label
// (Cal.BlockLabel), the selection's look and the click (mouseDown,
// accessibilityPerformPress); GTK: the calendar's views follow
// (docs/calendar.md §6.4).
//
// An occurrence on screen, the base of the Week's blocks, the all-day
// row's bars, the Month's bars and pills: a keyboard stop (Windows: macOS
// views take no focus; Tab reaches them here, and the focus selects the
// occurrence so that Return opens its detail, CalendarController.HandleKey),
// a click or Space that selects it and opens its detail (Activated), a
// double click that stays its own (no New Event over an event), and a
// repaint with fresh brushes in its window's theme, resolved
// (CalendarPalette.ThemeOf, never ElementTheme.Default), whenever it is
// loaded, first laid out at a size (a view built while its page was
// collapsed), its theme changes or its selection changes: a view is made
// and first painted before it is in a window, so that first paint is only
// provisional (its theme may not be its window's yet). The Week's fills,
// outlines, bars and icons at half brightness that the owner found on
// 2026-10-17 were not this: WinUI darkened them while a tentative bar's
// hatching was a repeating gradient (BlockBackground.Hatch). The keyboard's
// focus visual is drawn 3 DIP outside the view, so that it never covers
// or looks like the selection's accent ring at the view's edge (Windows).
// Its AutomationId is the view's prefix and the key ("CalendarBlock:" +
// key).

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.UI;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Calendars;

/// <summary>An occurrence shown by one of the calendar's views.</summary>
internal abstract partial class OccurrenceView : ContentControl
{
    private bool selected;

    /// <summary>A view of <paramref name="occurrence"/> drawn in <paramref name="color"/>, named <paramref name="label"/>.</summary>
    protected OccurrenceView(Occurrence occurrence, Cal.BlockState state, Color color, string label, string automationPrefix)
    {
        ArgumentNullException.ThrowIfNull(occurrence);
        Occurrence = occurrence;
        State = state;
        Color = color;
        Key = Cal.OccurrenceKey(occurrence);
        IsTabStop = true;
        UseSystemFocusVisuals = true;
        // Outside the block, clear of the selection's ring at its edge (BlockBackground).
        FocusVisualMargin = new Thickness(-CalendarMetrics.FocusVisualOffset);
        HorizontalContentAlignment = HorizontalAlignment.Stretch;
        VerticalContentAlignment = VerticalAlignment.Stretch;
        AutomationProperties.SetName(this, CalendarDisplay.Clean(label));
        AutomationProperties.SetAutomationId(this, automationPrefix + Key);
        Tapped += OnTapped;
        DoubleTapped += (_, e) => e.Handled = true;
        KeyDown += OnKeyDown;
        GotFocus += (_, _) =>
        {
            if (FocusState == FocusState.Keyboard)
            {
                RequestSelection();
            }
        };
        ActualThemeChanged += (_, _) => Repaint();
        Loaded += (_, _) => Repaint();
        SizeChanged += (_, e) =>
        {
            if (e.PreviousSize.Width <= 0 || e.PreviousSize.Height <= 0)
            {
                Repaint();
            }
        };
    }

    /// <summary>A click, Space or Invoke: the page selects the occurrence and opens its detail anchored here.</summary>
    public event EventHandler? Activated;

    /// <summary>The keyboard or UI Automation's Select: the page selects the occurrence (no detail).</summary>
    public event EventHandler? SelectionRequested;

    /// <summary>The occurrence.</summary>
    public Occurrence Occurrence { get; }

    /// <summary>Its <see cref="Cal.OccurrenceKey"/>.</summary>
    public string Key { get; }

    /// <summary>How it is drawn.</summary>
    public Cal.BlockState State { get; }

    /// <summary>Its calendar's colour.</summary>
    public Color Color { get; }

    /// <summary>Whether it is the calendar's selection.</summary>
    public bool Selected
    {
        get => selected;
        set
        {
            if (selected == value)
            {
                return;
            }
            selected = value;
            Repaint();
            if (FrameworkElementAutomationPeer.FromElement(this) is { } peer)
            {
                peer.RaisePropertyChangedEvent(SelectionItemPatternIdentifiers.IsSelectedProperty, !value, value);
            }
        }
    }

    /// <summary>Runs <see cref="Activated"/> (a click, Space, Invoke).</summary>
    internal void Activate() => Activated?.Invoke(this, EventArgs.Empty);

    /// <summary>Runs <see cref="SelectionRequested"/> (the keyboard's focus, Select).</summary>
    internal void RequestSelection() => SelectionRequested?.Invoke(this, EventArgs.Empty);

    /// <summary>Draws the view for its state, its selection and its window's theme, with fresh brushes.</summary>
    protected void Repaint() => Paint(CalendarPalette.ThemeOf(this));

    /// <summary>Draws the view in <paramref name="theme"/>, Light or Dark (resolved, never Default), through <see cref="BlockBackground.Paint"/>.</summary>
    protected abstract void Paint(ElementTheme theme);

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new OccurrenceViewPeer(this);

    private void OnTapped(object sender, TappedRoutedEventArgs e)
    {
        e.Handled = true;
        Activate();
    }

    private void OnKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == WinKey.Space && !e.KeyStatus.WasKeyDown)
        {
            e.Handled = true;
            Activate();
        }
    }
}
