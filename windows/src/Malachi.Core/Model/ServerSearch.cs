// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SearchModel.swift (ServerSearch,
// ServerSearchOffer); GTK: ui/internal/window/search_model.go (serverSearch,
// serverOffer). Swift's enum with a payload is a record struct with a
// phase here.

namespace Malachi.Core.Model;

/// <summary>Where the search on the server stands.</summary>
public enum ServerSearchPhase
{
    /// <summary>Not asked for the results on show.</summary>
    Idle,

    /// <summary>Under way.</summary>
    Searching,

    /// <summary>Over; what it found is listed.</summary>
    Done,
}

/// <summary>
/// The search on the server (search.server) for the results on show. Once
/// <see cref="Phase"/> is Done, <see cref="Found"/> counts the messages the
/// servers found (listed already or added) and <see cref="Failed"/> the accounts whose server
/// could not be searched.
/// </summary>
public readonly record struct ServerSearch(ServerSearchPhase Phase = ServerSearchPhase.Idle, int Found = 0, bool Truncated = false, int Failed = 0);

/// <summary>What the list offers of the search on the server.</summary>
public enum ServerSearchOffer
{
    /// <summary>Nothing.</summary>
    None,

    /// <summary>The button.</summary>
    Offer,

    /// <summary>The spinner while it runs.</summary>
    Searching,
}
