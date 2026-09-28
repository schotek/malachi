// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the (String, String) pair that
// macos/Sources/MalachiCore/Controllers/MailboxController.swift hands to
// onListTitleChanged; GTK: ui/internal/window/window.go
// (refreshListTitle: the list page's title and its counts). The caption
// that follows the title is Windows-only (windows/README.md, the deviation
// table; docs/windows-port.md §11.1).

using Malachi.Core.Text;

namespace Malachi.Core.Controllers;

/// <summary>
/// The title over the message list and the counts under it (window.go
/// <c>refreshListTitle</c>): the selected folder's name and counts,
/// "Messages" and nothing while none is selected, "Search" and the number
/// of results while searching. The window's caption follows the title.
/// </summary>
/// <param name="Title">The title.</param>
/// <param name="Subtitle">The counts; empty for none.</param>
public readonly record struct ListHeading(string Title, string Subtitle)
{
    /// <summary>
    /// The main window's caption: "&lt;title&gt; – Malachi Mail", the title
    /// isolated (<see cref="DisplayText.Isolate"/>) because a folder's name
    /// is the server's text, so that a right-to-left name cannot move the
    /// app's name; the app's name alone while the title shows nothing.
    /// </summary>
    public string Caption =>
        DisplayText.CleanTrimmed(Title).Length == 0
            ? AppIdentity.DisplayName
            // Windows-only string: the caption "<folder> – Malachi Mail".
            : DisplayText.Isolate(Title) + " – " + AppIdentity.DisplayName;
}
