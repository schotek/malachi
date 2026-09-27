// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SyncController.swift
// (StatusLine); GTK: ui/internal/window/status.go (statusLine).

namespace Malachi.Core.Controllers;

/// <summary>
/// What the status bar shows (status.go <c>statusLine</c>): the line,
/// whether the spinner turns, the connection icon (a GTK icon name, "" for
/// none, which the view maps to a glyph), whether the line can be clicked,
/// and the foot of its popover ("" for none).
/// </summary>
/// <param name="Text">The line.</param>
/// <param name="Spinning">Whether the spinner turns.</param>
/// <param name="Icon">The connection icon, a GTK name; "" for none.</param>
/// <param name="Active">Whether the line is a button that opens the popover.</param>
/// <param name="Daemon">The foot of the popover; "" for none.</param>
public readonly record struct StatusLine(string Text, bool Spinning = false, string Icon = "", bool Active = false, string Daemon = "");
