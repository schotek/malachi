// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (Prompt); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time).

namespace Malachi.Core.Updates;

/// <summary>The question a security release asks once (updates.Prompt).</summary>
public sealed record UpdatePrompt
{
    /// <summary>The version.</summary>
    public required string Version { get; init; }

    /// <summary>The package.</summary>
    public required string Url { get; init; }

    /// <summary>The heading.</summary>
    public required string Title { get; init; }

    /// <summary>The body.</summary>
    public required string Body { get; init; }

    /// <summary>The default button.</summary>
    public required string Download { get; init; }

    /// <summary>The other button.</summary>
    public required string Cancel { get; init; }

    /// <summary>
    /// The check box, on by default: after the browser opened the download,
    /// the app quits so that the installer can replace it.
    /// </summary>
    public required string Quit { get; init; }
}
