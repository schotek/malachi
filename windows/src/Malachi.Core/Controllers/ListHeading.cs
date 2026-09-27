// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the (String, String) pair that
// macos/Sources/MalachiCore/Controllers/MailboxController.swift hands to
// onListTitleChanged; GTK: ui/internal/window/window.go
// (refreshListTitle: the list page's title and its counts).

namespace Malachi.Core.Controllers;

/// <summary>
/// The title over the message list and the counts under it (window.go
/// <c>refreshListTitle</c>): the selected folder's name and counts,
/// "Messages" and nothing while none is selected, "Search" and the number
/// of results while searching. The window's caption follows the title.
/// </summary>
/// <param name="Title">The title.</param>
/// <param name="Subtitle">The counts; empty for none.</param>
public readonly record struct ListHeading(string Title, string Subtitle);
