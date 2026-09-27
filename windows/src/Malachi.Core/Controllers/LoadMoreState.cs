// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+List.swift
// (LoadMoreState); GTK: ui/internal/window/messages.go (showLoadMore) and
// ui/data/ui/window.blp (load_more, search_note).

namespace Malachi.Core.Controllers;

/// <summary>
/// The footer under the rows (messages.go <c>showLoadMore</c>): a further
/// page exists (<see cref="Button"/>), the spinner while it is fetched,
/// and under the last page of search results how far back search reaches
/// (window.blp <c>search_note</c>; empty otherwise). On Windows the list
/// pages itself, so <see cref="Button"/> says that a page can be asked for,
/// and the Load More button shows only to retry a failed page
/// (<see cref="ListController.LoadMoreRetry"/>).
/// </summary>
public sealed record LoadMoreState
{
    /// <summary>A further page is on its way.</summary>
    public bool Spinner { get; init; }

    /// <summary>A further page exists and nothing prevents asking for it.</summary>
    public bool Button { get; init; }

    /// <summary>Under the last page of results: how far back search reaches; empty otherwise.</summary>
    public string Note { get; init => field = value ?? ""; } = "";
}
