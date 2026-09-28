// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What Malachi.App.Canary hands its host (docs/windows-port.md §12), as a
// JSON file named on the command line. The test project compiles this file
// too, so both sides read the same shape.

using System.Collections.Generic;

namespace Malachi.App.Canary.Host;

/// <summary>One run of the canary host.</summary>
public sealed record HostConfig
{
    /// <summary>
    /// <see cref="Modes.Protected"/>: the app's views in the app's environment;
    /// <see cref="Modes.Control"/>: a plain WebView2 without any protection,
    /// which must leak (the harness's own check).
    /// </summary>
    public string Mode { get; init; } = Modes.Protected;

    /// <summary>The WebView2 user data folder (a fresh temporary one).</summary>
    public required string UserDataFolder { get; init; }

    /// <summary>Where the browser process writes its NetLog.</summary>
    public required string NetLog { get; init; }

    /// <summary>Where the host writes its <see cref="HostResults"/>.</summary>
    public required string Results { get; init; }

    /// <summary>The profiles' download folder, which must stay empty.</summary>
    public required string Downloads { get; init; }

    /// <summary>Whether the window is shown on screen (for looking at it); off screen and not activated otherwise.</summary>
    public bool Visible { get; init; }

    /// <summary>The width of each view in DIPs (the documents' coordinates depend on it).</summary>
    public int ViewWidth { get; init; } = 800;

    /// <summary>What to do, in order.</summary>
    public IReadOnlyList<HostStep> Steps { get; init; } = [];

    /// <summary>The modes of a run.</summary>
    public static class Modes
    {
        /// <summary>The app's views.</summary>
        public const string Protected = "protected";

        /// <summary>An unprotected WebView2.</summary>
        public const string Control = "control";
    }
}
