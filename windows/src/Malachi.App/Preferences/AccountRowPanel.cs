// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the layout of an Accounts row: GTK's Adw.ActionRow of
// ui/internal/window/accounts_page.go (newAccountRow: the handle and the
// icon as prefixes, the title and subtitle, then the suffixes: the status
// caption, Sign In…, the Enabled switch, Edit and Remove) and macOS's
// AccountRowCell.swift. An Adw.ActionRow gives its title what the suffixes
// leave and lets the title wrap; a row of the Windows list is as wide as
// the page, and at the Preferences window's narrow widths (docs/windows-
// port.md §11.3) the suffixes do not fit beside the name. So the row
// wraps as a SettingsCard does: below the width it needs the status and
// the controls go to a second line under the name, the controls at the
// row's end where the rows that did not wrap have them, the status
// beside them cut to what is left (its tooltip shows it whole).
//
// Children, in this order: the drag handle and the provider's icon (the
// leading ones, centred on the row), the name over the address, the
// status, then the controls (Sign In…, the switch, Edit and Remove).

using System;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Preferences;

/// <summary>Lays out one account row on one line, or on two when the line is too narrow.</summary>
public sealed partial class AccountRowPanel : Panel
{
    // The children's roles by index.
    private const int LeadingCount = 2;
    private const int TextIndex = 2;
    private const int StatusIndex = 3;
    private const int FirstControl = 4;

    // The gap between the row's parts (the GTK row's 12 px spacing), the
    // gap between the two lines of a wrapped row, and the narrowest the
    // name gets before the row wraps.
    private const double ColumnSpacing = 12;
    private const double RowSpacing = 4;
    private const double MinTextWidth = 120;

    // The last measure put the status and the controls on a second line.
    private bool wrapped;

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var infinite = new Size(double.PositiveInfinity, double.PositiveInfinity);
        var lead = 0.0;
        var leadHeight = 0.0;
        for (var i = 0; i < Math.Min(LeadingCount, Children.Count); i++)
        {
            var c = Children[i];
            c.Measure(infinite);
            if (Shown(c))
            {
                lead += c.DesiredSize.Width + ColumnSpacing;
                leadHeight = Math.Max(leadHeight, c.DesiredSize.Height);
            }
        }
        var (controls, controlsHeight) = MeasureControls(infinite);
        var status = Child(StatusIndex);
        status?.Measure(infinite);
        var statusWidth = Shown(status) ? status!.DesiredSize.Width : 0;
        var trailing = Join(statusWidth, controls);

        var text = Child(TextIndex);
        var width = availableSize.Width;
        wrapped = !double.IsInfinity(width) && width < lead + MinTextWidth + Gap(trailing) + trailing;
        if (!wrapped)
        {
            var textWidth = double.IsInfinity(width) ? width : Math.Max(0, width - lead - Gap(trailing) - trailing);
            text?.Measure(new Size(textWidth, availableSize.Height));
            var textSize = text?.DesiredSize ?? default;
            var height = Math.Max(Math.Max(leadHeight, textSize.Height), Math.Max(controlsHeight, Shown(status) ? status!.DesiredSize.Height : 0));
            return new Size(double.IsInfinity(width) ? lead + textSize.Width + Gap(trailing) + trailing : width, height);
        }

        // The second line starts under the name; the status gets what the
        // controls leave of it.
        var line = Math.Max(0, width - lead);
        text?.Measure(new Size(line, availableSize.Height));
        var statusRoom = Math.Max(0, line - controls - (controls > 0 ? ColumnSpacing : 0));
        status?.Measure(new Size(statusRoom, double.PositiveInfinity));
        var second = Math.Max(controlsHeight, Shown(status) ? status!.DesiredSize.Height : 0);
        var block = (text?.DesiredSize.Height ?? 0) + (second > 0 ? RowSpacing + second : 0);
        return new Size(width, Math.Max(leadHeight, block));
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        var height = finalSize.Height;
        var x = 0.0;
        for (var i = 0; i < Math.Min(LeadingCount, Children.Count); i++)
        {
            var c = Children[i];
            if (Shown(c))
            {
                Centre(c, x, 0, height);
                x += c.DesiredSize.Width + ColumnSpacing;
            }
        }
        var text = Child(TextIndex);
        var status = Child(StatusIndex);
        if (!wrapped)
        {
            var (end, any) = ArrangeControls(finalSize.Width, 0, height);
            if (any)
            {
                end -= ColumnSpacing;
            }
            if (Shown(status))
            {
                end -= status!.DesiredSize.Width;
                Centre(status, end, 0, height);
                end -= ColumnSpacing;
            }
            if (text is not null)
            {
                var h = text.DesiredSize.Height;
                text.Arrange(new Rect(x, (height - h) / 2, Math.Max(0, end - x), h));
            }
            return finalSize;
        }

        var textHeight = text?.DesiredSize.Height ?? 0;
        var second = SecondLineHeight(status);
        var block = textHeight + (second > 0 ? RowSpacing + second : 0);
        var top = Math.Max(0, (height - block) / 2);
        text?.Arrange(new Rect(x, top, Math.Max(0, finalSize.Width - x), textHeight));
        var lineTop = top + textHeight + RowSpacing;
        ArrangeControls(finalSize.Width, lineTop, second);
        if (Shown(status))
        {
            Centre(status!, x, lineTop, second);
        }
        return finalSize;
    }

    private static bool Shown(UIElement? e) => e is { Visibility: Visibility.Visible };

    private UIElement? Child(int index) => index < Children.Count ? Children[index] : null;

    private static double Gap(double trailing) => trailing > 0 ? ColumnSpacing : 0;

    private static double Join(double a, double b) => a > 0 && b > 0 ? a + ColumnSpacing + b : a + b;

    // The controls' width with their gaps, and their height.
    private (double Width, double Height) MeasureControls(Size available)
    {
        var width = 0.0;
        var height = 0.0;
        for (var i = FirstControl; i < Children.Count; i++)
        {
            var c = Children[i];
            c.Measure(available);
            if (Shown(c))
            {
                width = Join(width, c.DesiredSize.Width);
                height = Math.Max(height, c.DesiredSize.Height);
            }
        }
        return (width, height);
    }

    private double SecondLineHeight(UIElement? status)
    {
        var height = Shown(status) ? status!.DesiredSize.Height : 0;
        for (var i = FirstControl; i < Children.Count; i++)
        {
            if (Shown(Children[i]))
            {
                height = Math.Max(height, Children[i].DesiredSize.Height);
            }
        }
        return height;
    }

    // The controls from the row's end leftwards, centred on the band
    // [top, top + band]: the x where the leftmost one begins, and whether
    // any is shown.
    private (double Start, bool Any) ArrangeControls(double end, double top, double band)
    {
        var any = false;
        for (var i = Children.Count - 1; i >= FirstControl; i--)
        {
            var c = Children[i];
            if (!Shown(c))
            {
                continue;
            }
            if (any)
            {
                end -= ColumnSpacing;
            }
            any = true;
            end -= c.DesiredSize.Width;
            Centre(c, end, top, band);
        }
        return (end, any);
    }

    private static void Centre(UIElement c, double x, double top, double band)
    {
        var size = c.DesiredSize;
        c.Arrange(new Rect(x, top + Math.Max(0, (band - size.Height) / 2), size.Width, size.Height));
    }
}
