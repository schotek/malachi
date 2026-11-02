// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (Button); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time).

namespace Malachi.Core.Updates;

/// <summary>One button of the update banner, in display order; the first is the default one (updates.Button).</summary>
/// <param name="Action">What it does.</param>
/// <param name="Label">Its label.</param>
public readonly record struct BannerButton(BannerAction Action, string Label);
