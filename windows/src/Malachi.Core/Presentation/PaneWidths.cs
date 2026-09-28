// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.1): the widths
// PaneLayout.Widths gives the sidebar and the list for a window's width.

namespace Malachi.Core.Presentation;

/// <summary>The sidebar's and the list's widths in effective pixels.</summary>
/// <param name="Sidebar">The sidebar, inline or as the overlay.</param>
/// <param name="List">The list; the whole width while the list and the message are one stack.</param>
public readonly record struct PaneWidths(double Sidebar, double List);
