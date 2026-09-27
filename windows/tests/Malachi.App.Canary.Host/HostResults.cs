// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What the canary host reports (HostConfig.Results).

using System.Collections.Generic;

namespace Malachi.App.Canary.Host;

/// <summary>The record of one canary run.</summary>
public sealed record HostResults
{
    /// <summary>Whether every step ran.</summary>
    public bool Completed { get; init; }

    /// <summary>The WebView2 runtime's version.</summary>
    public string? BrowserVersion { get; init; }

    /// <summary>Whether the browser process exited before the host did (so the NetLog is complete).</summary>
    public bool BrowserExited { get; init; }

    /// <summary>What happened, in order.</summary>
    public IReadOnlyList<HostEvent> Events { get; init; } = [];
}
