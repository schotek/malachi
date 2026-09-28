// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A URL request as NetLog reads it: what the canary needs to tell the
// requests of a page from the browser's own background requests.

namespace Malachi.App.Canary;

/// <summary>
/// A URL request of a NetLog: its URL, and its initiator as
/// URL_REQUEST_START_JOB gives it (the origin of the page that started it,
/// "null" for an opaque one, <see cref="NoOrigin"/> when the browser started
/// it itself; null when the event has none, which no check takes for the
/// browser's own).
/// </summary>
internal sealed record UrlRequest(string Url, string? Initiator)
{
    /// <summary>The initiator of a request no page started.</summary>
    public const string NoOrigin = "not an origin";

    /// <summary>Whether the browser started it itself, not a page.</summary>
    public bool IsBrowsersOwn => Initiator == NoOrigin;
}
