// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (BannerKind); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time).

namespace Malachi.Core.Updates;

/// <summary>What the update banner is, which picks its look (updates.BannerKind).</summary>
public enum BannerKind
{
    /// <summary>No banner.</summary>
    None,

    /// <summary>A newer version (information).</summary>
    Update,

    /// <summary>A security release (warning); it stays until the update is installed.</summary>
    Security,

    /// <summary>A message of the project (information).</summary>
    Notice,
}
