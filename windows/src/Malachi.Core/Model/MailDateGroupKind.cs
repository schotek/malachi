// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups.go and
// macos/Sources/MalachiCore/Model/MailDateGroups.swift.

namespace Malachi.Core.Model;

/// <summary>Inbox sections in priority order.</summary>
public enum MailDateGroupKind
{
    Flagged,
    Today,
    Yesterday,
    ThisWeek,
    LastWeek,
    ThisMonth,
    LastMonth,
    ThisYear,
    OlderYear,
}
