// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Text/Format.swift; GTK:
// ui/internal/widget/format.go (DisplayName, FormatAddress,
// FormatParticipants, ThreadCountText, FormatDate, FormatTime,
// FormatDateTime, FormatSize, strftime).
//
// The small pure formatting helpers. Every input is attacker-controlled
// text; callers show the results as plain text. Dates are formatted with the
// strftime msgids the GTK UI translates (Strftime), in the given time zone,
// with the day and month names of the given culture (the regional format,
// as LC_TIME is for GLib); numbers are Go's, whatever the culture (3.0 MiB).

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Text;

/// <summary>The display formats of addresses, dates and sizes.</summary>
public static class Format
{
    /// <summary>
    /// The short form of an address for lists and avatars: the name if the
    /// backend parsed one, otherwise the bare address.
    /// </summary>
    public static string DisplayName(Address a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = (a.Name ?? "").Trim();
        return name.Length > 0 ? name : (a.Email ?? "").Trim();
    }

    /// <summary>The long form: "Name &lt;addr&gt;", or just the address.</summary>
    public static string FormatAddress(Address a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = (a.Name ?? "").Trim();
        var addr = (a.Email ?? "").Trim();
        if (name.Length == 0)
        {
            return addr;
        }
        if (addr.Length == 0)
        {
            return name;
        }
        return name + " <" + addr + ">";
    }

    /// <summary>
    /// Joins the display names of a conversation's participants in the order
    /// given (newest first), each address once, compared case-insensitively;
    /// entries with neither name nor address are skipped.
    /// </summary>
    public static string FormatParticipants(IEnumerable<Address> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var names = new List<string>();
        foreach (var a in list)
        {
            var key = (a.Email ?? "").Trim().ToLowerInvariant();
            if (key.Length == 0)
            {
                key = "name:" + (a.Name ?? "").Trim().ToLowerInvariant();
            }
            if (key == "name:" || !seen.Add(key))
            {
                continue;
            }
            names.Add(DisplayName(a));
        }
        // TRANSLATORS: put between the names of a conversation's participants ("Alice, Bob").
        return string.Join(L10n.C("participant list separator", ", "), names);
    }

    /// <summary>The badge of a conversation row: the member count from two on, nothing below.</summary>
    public static string ThreadCountText(int n) => n < 2 ? "" : n.ToString(CultureInfo.InvariantCulture);

    /// <summary>
    /// Renders a message date for the list, relative to <paramref name="now"/>:
    /// the time for today, day and month for the current year, the full date
    /// otherwise. Go's zero time renders as an empty string.
    /// <paramref name="timeZone"/> (the local zone when null) decides what
    /// "today" is, <paramref name="culture"/> (the current culture when null)
    /// the names of months and its calendar the year.
    /// </summary>
    public static string FormatDate(DateTimeOffset t, DateTimeOffset now, CultureInfo? culture = null, TimeZoneInfo? timeZone = null)
    {
        if (IsGoZero(t))
        {
            return "";
        }
        culture ??= CultureInfo.CurrentCulture;
        timeZone ??= TimeZoneInfo.Local;
        var calendar = culture.DateTimeFormat.Calendar;
        var a = Local(t, timeZone);
        var b = Local(now, timeZone);
        var sameYear = calendar.GetYear(a) == calendar.GetYear(b);
        if (sameYear && calendar.GetMonth(a) == calendar.GetMonth(b) && calendar.GetDayOfMonth(a) == calendar.GetDayOfMonth(b))
        {
            return FormatTime(t, culture, timeZone);
        }
        if (sameYear)
        {
            // TRANSLATORS: strftime format for a date in the current year in
            // the message list, e.g. "2 Sep".
            return Strftime.Format(t, L10n.T("%-d %b"), culture, timeZone);
        }
        // TRANSLATORS: strftime format for a date in another year in the
        // message list, e.g. "2025-09-02".
        return Strftime.Format(t, L10n.T("%Y-%m-%d"), culture, timeZone);
    }

    /// <summary>Renders a wall-clock time.</summary>
    public static string FormatTime(DateTimeOffset t, CultureInfo? culture = null, TimeZoneInfo? timeZone = null) =>
        // TRANSLATORS: strftime format for a time of day, e.g. "15:04".
        Strftime.Format(t, L10n.T("%H:%M"), culture ?? CultureInfo.CurrentCulture, timeZone);

    /// <summary>Renders a full date with time (quote headers, "draft saved" status).</summary>
    public static string FormatDateTime(DateTimeOffset t, CultureInfo? culture = null, TimeZoneInfo? timeZone = null) =>
        // TRANSLATORS: strftime format for a full date and time, e.g.
        // "Wed, 2 Sep 2026 at 15:04".
        Strftime.Format(t, L10n.T("%a, %-d %b %Y at %H:%M"), culture ?? CultureInfo.CurrentCulture, timeZone);

    /// <summary>Renders a byte count for attachment chips: MiB, KiB or B.</summary>
    public static string FormatSize(long n)
    {
        if (n >= 1L << 20)
        {
            // TRANSLATORS: file size in mebibytes.
            return L10n.T("%.1f MiB", (double)n / (1L << 20));
        }
        if (n >= 1L << 10)
        {
            // TRANSLATORS: file size in kibibytes.
            return L10n.T("%.0f KiB", (double)n / (1L << 10));
        }
        // TRANSLATORS: file size in bytes.
        return L10n.T("%d B", n);
    }

    // Go's IsZero once the value crossed the wire: any instant in year 1
    // (the check of GoTime.IsGoZero in the API layer).
    private static bool IsGoZero(DateTimeOffset t) => t.UtcDateTime.Year == 1;

    // The wall-clock date and time in zone; the instant itself where the
    // zone would move it out of the representable range.
    private static DateTime Local(DateTimeOffset t, TimeZoneInfo zone)
    {
        try
        {
            return TimeZoneInfo.ConvertTime(t, zone).DateTime;
        }
        catch (ArgumentOutOfRangeException)
        {
            return t.UtcDateTime;
        }
    }
}
