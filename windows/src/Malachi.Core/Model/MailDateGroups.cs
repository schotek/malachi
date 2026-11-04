// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups.go and
// macos/Sources/MalachiCore/Model/MailDateGroups.swift.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>Calendar grouping of the inbox's loaded rows; never changes mail data.</summary>
public static class MailDateGroups
{
    /// <summary>Uses local calendar dates, including across daylight-saving transitions.</summary>
    public static MailDateGroup Containing(DateTimeOffset date, DateTimeOffset now, TimeZoneInfo zone, DayOfWeek firstDay)
    {
        ArgumentNullException.ThrowIfNull(zone);
        var local = TimeZoneInfo.ConvertTime(date, zone).Date;
        var today = TimeZoneInfo.ConvertTime(now, zone).Date;
        var week = today.AddDays(-((7 + (int)today.DayOfWeek - (int)firstDay) % 7));
        var month = new DateTime(today.Year, today.Month, 1);
        (DateTime Start, MailDateGroupKind Kind)[] boundaries =
        [
            (today, MailDateGroupKind.Today),
            (today.AddDays(-1), MailDateGroupKind.Yesterday),
            (week, MailDateGroupKind.ThisWeek),
            (week.AddDays(-7), MailDateGroupKind.LastWeek),
            (month, MailDateGroupKind.ThisMonth),
            (month.AddMonths(-1), MailDateGroupKind.LastMonth),
            (new DateTime(today.Year, 1, 1), MailDateGroupKind.ThisYear),
        ];
        foreach (var (start, kind) in boundaries)
        {
            if (local >= start)
            {
                return new MailDateGroup(kind);
            }
        }
        return new MailDateGroup(MailDateGroupKind.OlderYear, local.Year);
    }

    /// <summary>Preserves source order within sections; flagged parents come first.</summary>
    public static IReadOnlyList<MailDateSection> Sections(IReadOnlyList<ListRow> rows, DateTimeOffset now, TimeZoneInfo zone, DayOfWeek firstDay)
    {
        ArgumentNullException.ThrowIfNull(rows);
        ArgumentNullException.ThrowIfNull(zone);
        var groups = new List<MailDateGroup>();
        var members = new Dictionary<MailDateGroup, List<ListRow>>();
        MailDateGroup? parent = null;
        foreach (var row in rows)
        {
            var group = row.Member && parent is { } inherited ? inherited
                : FolderTree.HasFlag(row.Summary?.Flags ?? row.Message.Flags, Flag.Flagged)
                    ? new MailDateGroup(MailDateGroupKind.Flagged)
                    : Containing(row.Summary?.LatestDate ?? row.Message.Date, now, zone, firstDay);
            if (!row.Member || parent is null)
            {
                parent = group;
            }
            if (!members.TryGetValue(group, out var items))
            {
                items = [];
                members.Add(group, items);
                if (group.Kind == MailDateGroupKind.Flagged)
                {
                    groups.Insert(0, group);
                }
                else
                {
                    groups.Add(group);
                }
            }
            items.Add(row);
        }
        var result = new List<MailDateSection>();
        foreach (var group in groups)
        {
            result.Add(new MailDateSection(group, members[group]));
        }
        return result;
    }
}
