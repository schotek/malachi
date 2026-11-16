// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+List.swift
// (ListState); GTK: ui/data/ui/window.blp (list_stack) and
// ui/internal/window/messages.go (showListState), search.go
// (showSearchState). Swift's enum with an associated value is a closed
// record hierarchy here, as DaemonNotification is.

using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the list pane shows (window.blp <c>list_stack</c>): the rows with
/// their footer, or a status page for the times there are none (nothing
/// selected, loading, empty, an error with a Try Again button). Icons are
/// GTK names; the WinUI layer maps them to glyphs. An empty icon means none
/// (the "Loading…" page).
/// </summary>
public abstract record ListState
{
    // Only the cases below derive from it.
    private ListState()
    {
    }

    /// <summary>The rows (Swift <c>.messages</c>).</summary>
    public sealed record Messages : ListState;

    /// <summary>
    /// A status page (Swift <c>.status(icon:title:description:retry:)</c>).
    /// Plain text, never markup.
    /// </summary>
    /// <param name="Icon">A GTK icon name; empty for none.</param>
    /// <param name="Title">The page's title.</param>
    /// <param name="Description">The sentence under it.</param>
    /// <param name="Retry">Whether the page offers Try Again (<see cref="ListController.Retry"/>).</param>
    public sealed record Status(string Icon, string Title, string Description, bool Retry) : ListState;

    /// <summary>
    /// A search without results in the store (Swift
    /// <c>.searchEmpty(description:server:)</c>): the explanation, and what
    /// the page offers of the search on the server.
    /// </summary>
    /// <param name="Description">The sentence under the title.</param>
    /// <param name="Server">The offer of the search on the server.</param>
    public sealed record SearchEmpty(string Description, ServerSearchOffer Server) : ListState;
}
