// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift
// (WebHeightGovernor); GTK: ui/internal/window/conversation_layout.go
// (webHeightGovernor, newWebHeightGovernor, content, fits, reset,
// widthChanged, setZoom, report, apply).
//
// The web view of a card follows the height of its document. The host reads
// the document's height in CSS pixels whenever it may have changed; the view
// becomes that tall, at the page zoom, up to MaxHeight (beyond it the card
// scrolls inside). Content sized by the viewport (100vh, height: 100%) would
// grow with every step the view grows: a report made because the view's
// height changed (viewport) that grows it again counts, and after
// GrowthLimit of those in a row the height stays where it is (frozen) until
// the document, the width or the zoom changes.

using System;

namespace Malachi.Core.Model;

/// <summary>A card's web view height.</summary>
public sealed class WebHeightGovernor
{
    /// <summary>webMaxHeight: the tallest a card's web view gets, in pixels.</summary>
    public const double DefaultMaxHeight = 4000.0;

    /// <summary>webGrowthLimit: how many growths in a row caused by the view's own growth are accepted before the height is frozen.</summary>
    public const int DefaultGrowthLimit = 3;

    private readonly double maxHeight;
    private readonly int growthLimit;

    // The document's height as last reported, in CSS pixels; null before
    // the first report.
    private double? css;
    private double zoom;
    private int streak;

    /// <summary>newWebHeightGovernorWith: a governor at <paramref name="zoom"/> (0 or less is 100 %).</summary>
    public WebHeightGovernor(double zoom = 1, double maxHeight = DefaultMaxHeight, int growthLimit = DefaultGrowthLimit)
    {
        this.zoom = zoom > 0 ? zoom : 1;
        this.maxHeight = maxHeight;
        this.growthLimit = growthLimit;
    }

    /// <summary>The height the view was given, in pixels; null before the first.</summary>
    public double? Applied { get; private set; }

    /// <summary>The height stays where it is until the document, the width or the zoom changes.</summary>
    public bool Frozen { get; private set; }

    /// <summary>The page zoom the view shows the document at (1 = 100 %).</summary>
    public double Zoom => zoom;

    /// <summary>content: the document's height in pixels at the current zoom; null before the first report.</summary>
    public double? Content => css is { } c ? Math.Ceiling(c * zoom) : null;

    /// <summary>fits: the document fits the view (nothing to scroll inside): the scroll wheel goes to the conversation.</summary>
    public bool Fits => Content is not { } c || Applied is not { } a || c <= a + 1;

    /// <summary>reset: a new document, measured from scratch. The view keeps the height it has until the first report.</summary>
    public void Reset()
    {
        css = null;
        Frozen = false;
        streak = 0;
    }

    /// <summary>widthChanged: the view's width changed, the document reflows, and a frozen height is measured again.</summary>
    public void WidthChanged()
    {
        Frozen = false;
        streak = 0;
    }

    /// <summary>
    /// setZoom: changes the zoom and returns the height the view should get
    /// at once (the last report scaled); null when there is none yet or it
    /// does not change. A frozen height is measured again.
    /// </summary>
    public double? SetZoom(double zoom)
    {
        if (!(zoom > 0))
        {
            zoom = 1;
        }
        if (zoom == this.zoom)
        {
            return null;
        }
        this.zoom = zoom;
        Frozen = false;
        streak = 0;
        return Apply();
    }

    /// <summary>
    /// report: takes the document's height of <paramref name="cssHeight"/>
    /// CSS pixels; <paramref name="viewport"/> says the report followed a
    /// change of the view's height alone. Returns the height the view should
    /// get, null to keep the one it has.
    /// </summary>
    public double? Report(double cssHeight, bool viewport)
    {
        if (double.IsNaN(cssHeight) || double.IsInfinity(cssHeight) || cssHeight < 0)
        {
            return null;
        }
        css = cssHeight;
        if (Frozen)
        {
            return null;
        }
        var target = Math.Min(Math.Max(Math.Ceiling(cssHeight * zoom), 1), maxHeight);
        if (viewport && Applied is { } applied && target > applied)
        {
            streak++;
            if (streak > growthLimit)
            {
                Frozen = true;
                return null;
            }
        }
        else
        {
            streak = 0;
        }
        return Apply();
    }

    // apply: the last report at the current zoom.
    private double? Apply()
    {
        if (Content is not { } c)
        {
            return null;
        }
        var target = Math.Min(Math.Max(c, 1), maxHeight);
        if (Applied == target)
        {
            return null;
        }
        Applied = target;
        return target;
    }
}
