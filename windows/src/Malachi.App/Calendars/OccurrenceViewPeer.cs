// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the accessibility of macos/Sources/MalachiMail/Calendar/EventBlockView.swift
// (setAccessibilityRole(.button), setAccessibilityLabel(Cal.blockLabel),
// accessibilityPerformPress) and of AllDayBarView, MonthPillView and
// MonthSpanBarView; GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// An occurrence on screen as UI Automation sees it: a button named by
// Cal.BlockLabel ("Standup, Tuesday 10:00–10:30, awaiting your answer")
// that Invoke opens as a click does (the selection and the detail), and a
// selection item whose IsSelected is the calendar's selection (Windows:
// the selection is the controller's; Select selects without the detail).

using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Automation.Provider;

namespace Malachi.App.Calendars;

/// <summary>The automation peer of an <see cref="OccurrenceView"/>.</summary>
internal sealed partial class OccurrenceViewPeer(OccurrenceView owner)
    : FrameworkElementAutomationPeer(owner), IInvokeProvider, ISelectionItemProvider
{
    /// <inheritdoc/>
    public bool IsSelected => owner.Selected;

    /// <inheritdoc/>
    public IRawElementProviderSimple? SelectionContainer => null;

    /// <inheritdoc/>
    public void Invoke() => owner.Activate();

    /// <inheritdoc/>
    public void Select() => owner.RequestSelection();

    /// <inheritdoc/>
    public void AddToSelection() => owner.RequestSelection();

    /// <inheritdoc/>
    public void RemoveFromSelection()
    {
        // One occurrence is selected at a time; another selection replaces it.
    }

    /// <inheritdoc/>
    protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Button;

    /// <inheritdoc/>
    protected override string GetClassNameCore() => owner.GetType().Name;

    /// <inheritdoc/>
    protected override bool IsControlElementCore() => true;

    /// <inheritdoc/>
    protected override object? GetPatternCore(PatternInterface patternInterface) =>
        patternInterface is PatternInterface.Invoke or PatternInterface.SelectionItem ? this : base.GetPatternCore(patternInterface);
}
