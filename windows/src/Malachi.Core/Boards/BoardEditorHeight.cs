// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardEditorHeight.swift
// (EditorHeight); GTK: ui/internal/board/editor_height.go (EditorMinHeight,
// EditorMaxHeight, EditorVisibleShare, EditorHeight, NewEditorHeight,
// EditorHeightCap).
//
// How tall the board's inline reply editor is: as tall as its content,
// never shorter than a few lines, never taller than most of what the detail
// shows (above that it scrolls inside). Heights are in the view's units
// (device-independent pixels on Windows, as the editor reports CSS pixels).

using System;

namespace Malachi.Core.Boards;

/// <summary>
/// The inline reply editor's height for a content height and the height of
/// the detail's visible part.
/// </summary>
public readonly record struct EditorHeight
{
    /// <summary>Never shorter: a few lines to type into, even when empty.</summary>
    public const double Minimum = 160;

    /// <summary>Never taller, however tall the detail.</summary>
    public const double Maximum = 480;

    /// <summary>
    /// Never taller than this share of the detail's visible height (but never
    /// below <see cref="Minimum"/>).
    /// </summary>
    public const double VisibleShare = 0.6;

    /// <summary>
    /// The content height clamped to [<see cref="Minimum"/>,
    /// <see cref="Cap"/>(<paramref name="visible"/>)]. A content height that
    /// is not a finite number counts as empty.
    /// </summary>
    public EditorHeight(double content, double visible)
    {
        var cap = Cap(visible);
        var c = double.IsFinite(content) ? content : 0;
        Height = Math.Min(Math.Max(c, Minimum), cap);
        Scrolls = c > Height;
    }

    /// <summary>The editor's height.</summary>
    public double Height { get; }

    /// <summary>The content is taller than <see cref="Height"/>: the editor scrolls inside.</summary>
    public bool Scrolls { get; }

    /// <summary>
    /// min(<see cref="Maximum"/>, <see cref="VisibleShare"/> × visible),
    /// never below <see cref="Minimum"/>; a visible height that is not a
    /// finite positive number gives <see cref="Maximum"/>.
    /// </summary>
    public static double Cap(double visible) =>
        !double.IsFinite(visible) || visible <= 0 ? Maximum : Math.Max(Minimum, Math.Min(Maximum, VisibleShare * visible));
}
