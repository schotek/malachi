// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md W.4, §17): the
// panel a customizable bar draws its section of Toolbar.PresentFor with,
// as GTK's header bars pack the items of their part of the layout and
// macOS's NSToolbar its identifiers. A WinUI bar has no toolbar of its own,
// so the bars keep their controls (made once, in their XAML or code, with
// their commands, ActionPresentation and automation ids) and name each with
// the attached ToolbarStrip.Item, the catalog's id; Apply puts them in the
// order of the layout's run, a space as a 10 DIP gap (with the spacing on
// either side, the 14 DIP of a trash button's old margin; two spaces with
// nothing shown between them make one), a flexible space as a share of the
// room left. An item the layout does not have is taken out of the panel
// and kept: its owner still sets its state, so it comes back as it should
// be. An item the layout has keeps its own visibility (an action the
// account does not offer, ✦ without the Assistant: hidden in place, T7).
// The controls of Windows alone (the "…" menus, the title, Close) are not
// in the catalog: ToolbarStrip.Pin keeps them at the start, the end, or
// after or before the first of a list of items. ToolbarStrip.Stretch makes
// a child take the room a flexible space would (a title). When the strip
// is narrower than its items, Toolbar.Fit picks what gives way from the
// widths last measured; those leave the panel too (after the layout pass,
// so the measure never changes the tree it measures) and Overflow names
// them for More Actions (T5, T8). A right click on an empty spot of a bar
// offers Customize Toolbar… (T15, WireCustomizeMenu).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;

namespace Malachi.App.Toolbars;

/// <summary>A bar's row of items in the order of the user's toolbar layout.</summary>
public sealed partial class ToolbarStrip : Panel
{
    /// <summary>The catalog id of a child (Toolbar.Reply, Toolbar.BoardStyle…).</summary>
    public static readonly DependencyProperty ItemProperty = DependencyProperty.RegisterAttached(
        "Item", typeof(string), typeof(ToolbarStrip), new PropertyMetadata(null));

    /// <summary>
    /// Where a child the catalog does not have stands: "start", "end",
    /// "after:a,b" (after the first of the items present, else at the start)
    /// or "before:a,b" (before the first present, else at the end).
    /// </summary>
    public static readonly DependencyProperty PinProperty = DependencyProperty.RegisterAttached(
        "Pin", typeof(string), typeof(ToolbarStrip), new PropertyMetadata(null));

    /// <summary>Whether a child takes the free room as a flexible space does (a title).</summary>
    public static readonly DependencyProperty StretchProperty = DependencyProperty.RegisterAttached(
        "Stretch", typeof(bool), typeof(ToolbarStrip), new PropertyMetadata(false));

    /// <summary>The space between two shown children (2 DIP, the header bars' StackPanel spacing).</summary>
    public static readonly DependencyProperty SpacingProperty = DependencyProperty.Register(
        nameof(Spacing), typeof(double), typeof(ToolbarStrip), new PropertyMetadata(2.0, (d, _) => ((ToolbarStrip)d).InvalidateMeasure()));

    // The width of a space between its two spacings.
    private const double SpaceWidth = 10;

    private readonly Dictionary<string, UIElement> items = new(StringComparer.Ordinal);
    private readonly List<UIElement> pinned = [];
    private readonly HashSet<UIElement> spacers = [];
    private readonly HashSet<UIElement> flexes = [];
    private readonly List<Border> spacePool = [];
    private readonly List<Border> flexPool = [];
    private readonly Dictionary<UIElement, double> widths = [];
    private readonly HashSet<string> overflow = new(StringComparer.Ordinal);

    // The run Apply was given last, null until then.
    private IReadOnlyList<Toolbar.Shown>? run;

    // The last measure's widths per child, for the arrange.
    private double[] measured = [];
    private double measuredTotal;
    private bool collected;
    private bool rebuildQueued;

    /// <summary>A strip with the default spacing.</summary>
    public ToolbarStrip()
    {
        // An empty spot is hit by a right click (T15).
        Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent);
    }

    /// <summary>The overflow changed: <see cref="Overflow"/> names other items.</summary>
    public event EventHandler? OverflowChanged;

    /// <summary>The space between two shown children.</summary>
    public double Spacing
    {
        get => (double)GetValue(SpacingProperty);
        set => SetValue(SpacingProperty, value);
    }

    /// <summary>The toolbar whose run the strip draws (for Toolbar.Fit's priorities).</summary>
    public Toolbar.Bar Bar { get; set; } = Toolbar.Bar.Mail;

    /// <summary>Whether items give way when the strip is too narrow (off: it is clipped, as before).</summary>
    public bool FitsItems { get; set; } = true;

    /// <summary>The items that gave way for the lack of room, in the layout's order.</summary>
    public IReadOnlyList<string> Overflow
    {
        get
        {
            var output = new List<string>();
            foreach (var e in run ?? [])
            {
                if (overflow.Contains(e.Id))
                {
                    output.Add(e.Id);
                }
            }
            return output;
        }
    }

    /// <summary>The width every item of the run needs, those that gave way included (the pane's minimum width).</summary>
    public double NaturalWidth
    {
        get
        {
            Measure(new Size(double.PositiveInfinity, double.PositiveInfinity));
            double total = measuredTotal;
            foreach (var id in overflow)
            {
                if (items.TryGetValue(id, out var el) && widths.TryGetValue(el, out var w) && w > 0)
                {
                    total += w + Spacing;
                }
            }
            return total;
        }
    }

    /// <summary>The catalog id set on <paramref name="element"/>.</summary>
    public static string? GetItem(DependencyObject element) => (string?)element?.GetValue(ItemProperty);

    /// <summary>Names <paramref name="element"/> as the catalog's <paramref name="id"/>.</summary>
    public static void SetItem(DependencyObject element, string? id) => element?.SetValue(ItemProperty, id);

    /// <summary>The pin of <paramref name="element"/>.</summary>
    public static string? GetPin(DependencyObject element) => (string?)element?.GetValue(PinProperty);

    /// <summary>Pins <paramref name="element"/> (see <see cref="PinProperty"/>).</summary>
    public static void SetPin(DependencyObject element, string? pin) => element?.SetValue(PinProperty, pin);

    /// <summary>Whether <paramref name="element"/> stretches.</summary>
    public static bool GetStretch(DependencyObject element) => element is not null && (bool)element.GetValue(StretchProperty);

    /// <summary>Makes <paramref name="element"/> stretch.</summary>
    public static void SetStretch(DependencyObject element, bool value) => element?.SetValue(StretchProperty, value);

    /// <summary>Adds a control made in code as the catalog's <paramref name="id"/> (it shows once a run has it).</summary>
    public void Add(string id, UIElement element)
    {
        ArgumentNullException.ThrowIfNull(element);
        Collect();
        SetItem(element, id);
        items[id] = element;
        if (run is not null)
        {
            Rebuild();
        }
    }

    /// <summary>
    /// Shows <paramref name="entries"/>, the strip's run of
    /// Toolbar.PresentFor: its items in order, the spaces, the pinned
    /// children where their pins say; separators and ids without a control
    /// here (the title bar's search, the mode switch) are passed over.
    /// </summary>
    public void Apply(IReadOnlyList<Toolbar.Shown> entries)
    {
        ArgumentNullException.ThrowIfNull(entries);
        Collect();
        IReadOnlyList<Toolbar.Shown> next = [.. entries];
        run = next;
        overflow.RemoveWhere(id => !next.Any(e => e.Id == id));
        Rebuild();
    }

    /// <summary>
    /// T15: a right click (or the context key) on an empty spot of
    /// <paramref name="bar"/>, not on one of its controls, offers Customize
    /// Toolbar…, which calls <paramref name="open"/> with the toolbar
    /// <paramref name="which"/> names at that moment.
    /// </summary>
    public static void WireCustomizeMenu(FrameworkElement bar, Func<Toolbar.Bar> which, Action<Toolbar.Bar> open)
    {
        ArgumentNullException.ThrowIfNull(bar);
        ArgumentNullException.ThrowIfNull(which);
        ArgumentNullException.ThrowIfNull(open);
        bar.ContextRequested += (sender, e) =>
        {
            if (e.Handled || OnControl(e.OriginalSource as DependencyObject, bar))
            {
                return;
            }
            var item = new MenuFlyoutItem { Text = Toolbar.CustomizeTitle() };
            Microsoft.UI.Xaml.Automation.AutomationProperties.SetAutomationId(item, "CustomizeToolbar");
            item.Click += (_, _) => open(which());
            var menu = new MenuFlyout();
            menu.Items.Add(item);
            e.Handled = true;
            if (e.TryGetPosition(bar, out var point))
            {
                menu.ShowAt(bar, new FlyoutShowOptions { Position = point });
            }
            else
            {
                menu.ShowAt(bar);
            }
        };
    }

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var infinite = new Size(double.PositiveInfinity, availableSize.Height);
        var n = Children.Count;
        measured = new double[n];
        double total = 0, height = 0;
        var placed = 0;
        var lastSpace = false;
        var stretch = new List<int>();
        for (var i = 0; i < n; i++)
        {
            var child = Children[i];
            if (flexes.Contains(child))
            {
                continue;
            }
            if (spacers.Contains(child))
            {
                var w = lastSpace ? 0 : SpaceWidth;
                measured[i] = w;
                if (w > 0)
                {
                    total += (placed > 0 ? Spacing : 0) + w;
                    placed++;
                }
                lastSpace = true;
                continue;
            }
            if (GetStretch(child))
            {
                stretch.Add(i);
                continue;
            }
            child.Measure(infinite);
            var size = child.DesiredSize;
            measured[i] = size.Width;
            if (size.Width > 0)
            {
                widths[child] = size.Width;
                total += (placed > 0 ? Spacing : 0) + size.Width;
                placed++;
                lastSpace = false;
            }
            height = Math.Max(height, size.Height);
        }
        if (stretch.Count > 0)
        {
            var room = double.IsInfinity(availableSize.Width)
                ? double.PositiveInfinity
                : Math.Max(0, (availableSize.Width - total - (stretch.Count * Spacing)) / stretch.Count);
            foreach (var i in stretch)
            {
                var child = Children[i];
                child.Measure(new Size(room, availableSize.Height));
                measured[i] = child.DesiredSize.Width;
                if (measured[i] > 0)
                {
                    total += (placed > 0 ? Spacing : 0) + measured[i];
                    placed++;
                }
                height = Math.Max(height, child.DesiredSize.Height);
            }
        }
        measuredTotal = total;
        if (FitsItems && !double.IsInfinity(availableSize.Width))
        {
            CheckFit(availableSize.Width);
        }
        var width = double.IsInfinity(availableSize.Width) ? total : Math.Min(total, availableSize.Width);
        return new Size(width, height);
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        var n = Math.Min(Children.Count, measured.Length);
        var flexCount = 0;
        var stretchCount = 0;
        for (var i = 0; i < n; i++)
        {
            if (flexes.Contains(Children[i]))
            {
                flexCount++;
            }
            else if (GetStretch(Children[i]) && Children[i].Visibility == Visibility.Visible)
            {
                stretchCount++;
            }
        }
        var extra = Math.Max(0, finalSize.Width - measuredTotal);
        var flexShare = flexCount > 0 ? extra / flexCount : 0;
        var stretchShare = flexCount == 0 && stretchCount > 0 ? extra / stretchCount : 0;
        double x = 0;
        var placed = 0;
        for (var i = 0; i < Children.Count; i++)
        {
            var child = Children[i];
            if (i >= n)
            {
                child.Arrange(new Rect(0, 0, 0, 0));
                continue;
            }
            if (flexes.Contains(child))
            {
                x += flexShare;
                continue;
            }
            var w = measured[i];
            if (GetStretch(child) && child.Visibility == Visibility.Visible)
            {
                w += stretchShare;
            }
            if (w <= 0)
            {
                child.Arrange(new Rect(x, 0, 0, 0));
                continue;
            }
            if (placed > 0)
            {
                x += Spacing;
            }
            var h = Math.Min(child.DesiredSize.Height, finalSize.Height);
            child.Arrange(new Rect(x, Math.Max(0, (finalSize.Height - h) / 2), w, h));
            x += w;
            placed++;
        }
        return finalSize;
    }

    // Whether source, a part of bar, is (in) one of its controls.
    private static bool OnControl(DependencyObject? source, FrameworkElement bar)
    {
        for (var d = source; d is not null && !ReferenceEquals(d, bar); d = VisualTreeHelper.GetParent(d))
        {
            if (d is ButtonBase or ComboBox or SelectorBar or AutoSuggestBox or TextBox or ListViewBase)
            {
                return true;
            }
        }
        return false;
    }

    // The children the XAML gave the strip: the catalog's by their Item,
    // the others by their Pin (a child with neither is pinned at the end).
    private void Collect()
    {
        if (collected)
        {
            return;
        }
        collected = true;
        foreach (var child in Children)
        {
            if (GetItem(child) is { Length: > 0 } id)
            {
                items[id] = child;
            }
            else
            {
                pinned.Add(child);
            }
        }
    }

    // The children for the run: its items that did not give way, the
    // spaces, the pinned children; nothing changes when they are the
    // children already (no control loses the keyboard for nothing).
    private void Rebuild()
    {
        if (run is null)
        {
            return;
        }
        var order = new List<(UIElement Element, string? Id)>();
        var spaces = 0;
        var flex = 0;
        foreach (var e in run)
        {
            if (Toolbar.IsSeparator(e.Id))
            {
                continue;
            }
            if (e.Id == Toolbar.Space)
            {
                order.Add((Spacer(spacePool, spacers, spaces++), e.Id));
            }
            else if (e.Id == Toolbar.FlexibleSpace)
            {
                order.Add((Spacer(flexPool, flexes, flex++), e.Id));
            }
            else if (items.TryGetValue(e.Id, out var element) && !overflow.Contains(e.Id))
            {
                order.Add((element, e.Id));
            }
        }
        foreach (var p in pinned)
        {
            order.Insert(PinIndex(order, GetPin(p)), (p, null));
        }
        if (order.Count == Children.Count && order.Select(o => o.Element).SequenceEqual(Children))
        {
            return;
        }
        Children.Clear();
        foreach (var (element, _) in order)
        {
            Children.Add(element);
        }
        InvalidateMeasure();
    }

    private static int PinIndex(List<(UIElement Element, string? Id)> order, string? pin)
    {
        if (pin is null || pin == "end")
        {
            return order.Count;
        }
        if (pin == "start")
        {
            return 0;
        }
        var after = pin.StartsWith("after:", StringComparison.Ordinal);
        var before = pin.StartsWith("before:", StringComparison.Ordinal);
        if (!after && !before)
        {
            return order.Count;
        }
        var ids = pin[(pin.IndexOf(':', StringComparison.Ordinal) + 1)..].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
        foreach (var id in ids)
        {
            var i = order.FindIndex(o => o.Id == id);
            if (i >= 0)
            {
                return after ? i + 1 : i;
            }
        }
        return after ? 0 : order.Count;
    }

    // The k-th spacer of a kind, made once and kept.
    private static Border Spacer(List<Border> pool, HashSet<UIElement> set, int k)
    {
        while (pool.Count <= k)
        {
            var b = new Border { Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
            pool.Add(b);
            set.Add(b);
        }
        return pool[k];
    }

    // Toolbar.Fit over the run with the widths last measured: what gives
    // way now, applied after the layout pass when it changed.
    private void CheckFit(double available)
    {
        if (run is null)
        {
            return;
        }
        var shown = new List<Toolbar.Shown>();
        var w = new List<double>();
        foreach (var e in run)
        {
            if (Toolbar.IsSeparator(e.Id))
            {
                continue;
            }
            if (Toolbar.IsSpace(e.Id))
            {
                shown.Add(e);
                w.Add(e.Id == Toolbar.Space ? SpaceWidth + Spacing : 0);
                continue;
            }
            if (!items.TryGetValue(e.Id, out var element))
            {
                continue;
            }
            shown.Add(e);
            var width = 0.0;
            if (element.Visibility == Visibility.Visible && widths.TryGetValue(element, out var known))
            {
                width = known + Spacing;
            }
            w.Add(width);
        }
        // The pinned and stretching children keep their room.
        for (var i = 0; i < Children.Count && i < measured.Length; i++)
        {
            if (pinned.Contains(Children[i]) && !GetStretch(Children[i]) && measured[i] > 0)
            {
                available -= measured[i] + Spacing;
            }
        }
        var fit = Toolbar.Fit(ToolbarVariant.Windows, Bar, shown, w, available);
        var next = new HashSet<string>(fit.Overflow, StringComparer.Ordinal);
        if (next.SetEquals(overflow) || rebuildQueued)
        {
            return;
        }
        rebuildQueued = true;
        DispatcherQueue.TryEnqueue(() =>
        {
            rebuildQueued = false;
            overflow.Clear();
            overflow.UnionWith(next);
            Rebuild();
            OverflowChanged?.Invoke(this, EventArgs.Empty);
        });
    }
}
