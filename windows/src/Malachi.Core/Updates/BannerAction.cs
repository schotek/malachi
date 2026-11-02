// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (Action); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time). Go's
// values start at 1 (iota + 1); nothing reads them as numbers.

namespace Malachi.Core.Updates;

/// <summary>What a banner button does (updates.Action).</summary>
public enum BannerAction
{
    /// <summary>Opens <see cref="UpdateBanner.Url"/> (the package) in the browser.</summary>
    Download,

    /// <summary>Shows <see cref="UpdateCheck.NotesFor"/>.</summary>
    WhatsNew,

    /// <summary>Calls <c>update.dismiss {version, "later"}</c>.</summary>
    Later,

    /// <summary>Calls <c>update.dismiss {version, "skip"}</c>.</summary>
    Skip,

    /// <summary>Opens <see cref="UpdateBanner.Url"/> (the notice's page).</summary>
    MoreInfo,

    /// <summary>Calls <c>update.dismissNotice {noticeId}</c>.</summary>
    Close,
}
