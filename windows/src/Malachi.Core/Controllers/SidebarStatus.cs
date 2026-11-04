// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController.swift
// (SidebarStatus); GTK: ui/data/ui/window.blp (folder_stack) and
// ui/internal/window/folders.go (showFolderStatus). Swift's enum with an
// associated value is a closed record hierarchy here, as DaemonNotification
// is: equal values compare equal, so a view (and a test) can tell a change.

namespace Malachi.Core.Controllers;

/// <summary>
/// What the sidebar shows in place of the folder list while there is
/// nothing to list (window.blp <c>folder_stack</c>): the list itself, or a
/// status page. Icons are GTK names; the WinUI layer maps them to glyphs. An
/// empty icon means none (the "Loading…" page).
/// </summary>
public abstract record SidebarStatus
{
    // Only the cases below derive from it.
    private SidebarStatus()
    {
    }

    /// <summary>The folder list (Swift <c>.folders</c>).</summary>
    public sealed record Folders : SidebarStatus;

    /// <summary>A status page (Swift <c>.status(icon:title:description:)</c>). Plain text, never markup.</summary>
    /// <param name="Icon">A GTK icon name; empty for none.</param>
    /// <param name="Title">The page's title.</param>
    /// <param name="Description">The sentence under it.</param>
    public sealed record Status(string Icon, string Title, string Description) : SidebarStatus;
}
