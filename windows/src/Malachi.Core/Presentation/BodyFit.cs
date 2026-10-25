// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/message_view_fit.go (fitsBody, bodyFit.apply)
// and macos/Sources/MalachiMail/MessageView/MessageViewController.swift
// (textFit, htmlFit, fillHeight).
//
// The body area of a message view takes no more room than its body needs
// while that fits, so the "•••" of the quoted history under it follows the
// body's last line; a longer body takes the whole area and scrolls, the
// button then at the bottom. The text page takes what its scroller asks for
// (its content, at most the room); the HTML page the height the viewer
// measured of its document (MessageWebView.ContentHeight), the whole area
// while that is not known yet; the spinner the whole area.

using System;

namespace Malachi.Core.Presentation;

/// <summary>How tall the body area of a message view is.</summary>
public enum BodyFitMode
{
    /// <summary>The whole room (the spinner, an HTML document not measured yet).</summary>
    Fill,

    /// <summary>What the body asks for in the room (the text page's scroller).</summary>
    Content,

    /// <summary>A height the owner knows (an HTML document's), at most the room.</summary>
    Height,
}

/// <summary>The height of the body area of a message view (bodyFit).</summary>
public static class BodyFit
{
    /// <summary>
    /// The mode of body page <paramref name="page"/>; <paramref name="html"/>
    /// is the HTML document's height in device-independent pixels, null while
    /// it is not known.
    /// </summary>
    public static BodyFitMode ModeFor(ReaderBodyPage page, double? html) => page switch
    {
        ReaderBodyPage.Text => BodyFitMode.Content,
        ReaderBodyPage.Html when html is { } h && double.IsFinite(h) && h >= 0 => BodyFitMode.Height,
        _ => BodyFitMode.Fill,
    };

    /// <summary>
    /// The body area's height in <paramref name="room"/> (the area minus what
    /// comes under it): the room for <see cref="BodyFitMode.Fill"/>, otherwise
    /// <paramref name="desired"/> (the body's own ask) or
    /// <paramref name="height"/> (the known height), at most the room. An
    /// infinite room (a measure without a bound) gives the body what it asks.
    /// </summary>
    public static double Height(BodyFitMode mode, double room, double desired, double height)
    {
        var r = double.IsNaN(room) || room < 0 ? 0 : room;
        var want = mode switch
        {
            BodyFitMode.Content => desired,
            BodyFitMode.Height => height,
            _ => r,
        };
        if (double.IsNaN(want) || want < 0)
        {
            want = 0;
        }
        if (double.IsPositiveInfinity(r))
        {
            return double.IsPositiveInfinity(want) ? 0 : want;
        }
        return Math.Min(want, r);
    }
}
