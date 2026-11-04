// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (Banner); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time).

using System.Collections.Generic;

namespace Malachi.Core.Updates;

/// <summary>
/// The update bar of the main window (updates.Banner): the default value is
/// "no banner". The title may be a notice's text: plain text only.
/// </summary>
public sealed record UpdateBanner
{
    /// <summary>What it is; <see cref="BannerKind.None"/> hides it.</summary>
    public BannerKind Kind { get; init; }

    /// <summary>The sentence, plain text, never markup.</summary>
    public string Title { get; init; } = "";

    /// <summary>The buttons in display order; the first is the default one.</summary>
    public IReadOnlyList<BannerButton> Buttons { get; init; } = [];

    /// <summary>The offered version (<see cref="BannerKind.Update"/>, <see cref="BannerKind.Security"/>).</summary>
    public string Version { get; init; } = "";

    /// <summary>The package, or the notice's page; "" when none.</summary>
    public string Url { get; init; } = "";

    /// <summary>The notice (<see cref="BannerKind.Notice"/>).</summary>
    public string NoticeId { get; init; } = "";

    /// <summary>A banner to show (updates.Banner.Shown).</summary>
    public bool Shown => Kind != BannerKind.None;
}
