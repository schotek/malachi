// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups.go and
// macos/Sources/MalachiCore/Model/MailDateGroups.swift.

using System.Globalization;
using Malachi.Core.I18n;

namespace Malachi.Core.Model;

/// <summary>A stable section identity, independent of its translated label.</summary>
public readonly record struct MailDateGroup(MailDateGroupKind Kind, int Year = 0)
{
    /// <summary>The shared GTK label, or the older calendar year.</summary>
    public string Title => Kind switch
    {
        MailDateGroupKind.Flagged => L10n.T("Flagged"),
        MailDateGroupKind.Today => L10n.T("Today"),
        MailDateGroupKind.Yesterday => L10n.T("Yesterday"),
        MailDateGroupKind.ThisWeek => L10n.T("This week"),
        MailDateGroupKind.LastWeek => L10n.T("Last week"),
        MailDateGroupKind.ThisMonth => L10n.T("This month"),
        MailDateGroupKind.LastMonth => L10n.T("Last month"),
        MailDateGroupKind.ThisYear => L10n.T("This year"),
        _ => Year.ToString(CultureInfo.InvariantCulture),
    };
}
