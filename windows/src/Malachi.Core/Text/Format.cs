// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Text/Format.swift; GTK:
// ui/internal/widget/format.go (DisplayName, FormatAddress,
// FormatParticipants, ThreadCountText, FormatDate, FormatTime,
// FormatDateTime, FormatSize, strftime).
//
// The small pure formatting helpers. Every input is attacker-controlled
// text; callers show the results as plain text. Windows-only: names and
// addresses go through DisplayText (docs/security.md §4), so a control or
// an explicit bidi character never reaches the screen, and a name composed
// with other text (the address, the other participants) is isolated, so a
// right-to-left name cannot reorder them. What goes into a draft (a quote's
// header) takes GTK's forms of the received text instead: NameAsReceived
// and AddressAsReceived. Dates are formatted with the
// strftime msgids the GTK UI translates (Strftime), in the given time zone,
// with the day and month names of the given culture (the regional format,
// as LC_TIME is for GLib); numbers are Go's, whatever the culture (3.0 MiB).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Text;

/// <summary>The display formats of addresses, dates and sizes.</summary>
public static class Format
{
    /// <summary>
    /// The short form of an address for lists and avatars: the name if the
    /// backend parsed one, otherwise the bare address; cleaned for display
    /// (<see cref="DisplayText.CleanTrimmed"/>), so a name of only control or
    /// bidi characters, or of characters that draw nothing, counts as none.
    /// </summary>
    public static string DisplayName(Address a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = DisplayText.CleanTrimmed(a.Name);
        return name.Length > 0 ? name : DisplayText.CleanTrimmed(a.Email);
    }

    /// <summary>
    /// The long form: "Name &lt;addr&gt;", or just the address (or just the
    /// name), cleaned for display; with both, the name is isolated
    /// (<see cref="DisplayText.Isolate"/>) so that it cannot reorder the
    /// address after it. For display only: what is copied or written to is
    /// the address as received.
    /// </summary>
    public static string FormatAddress(Address a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = DisplayText.CleanTrimmed(a.Name);
        var addr = DisplayText.CleanTrimmed(a.Email);
        if (name.Length == 0)
        {
            return addr;
        }
        if (addr.Length == 0)
        {
            return name;
        }
        return DisplayText.Isolate(name) + " <" + addr + ">";
    }

    /// <summary>
    /// GTK's <c>DisplayName</c> for text that goes into a draft rather than
    /// onto the screen (the attribution and the forwarded header of a quote):
    /// the name, trimmed, if the backend parsed one, otherwise the bare
    /// address; the received text, not cleaned for display.
    /// </summary>
    public static string NameAsReceived(Address a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = (a.Name ?? "").Trim();
        return name.Length > 0 ? name : (a.Email ?? "").Trim();
    }

    /// <summary>
    /// GTK's <c>FormatAddress</c> for text that goes into a draft: "Name
    /// &lt;addr&gt;", or just the address (or the name), the received text
    /// without isolates.
    /// </summary>
    public static string AddressAsReceived(Address a)
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
    /// entries with neither name nor address are skipped. Of two or more,
    /// each name is isolated (<see cref="DisplayText.Isolate"/>), so that a
    /// right-to-left name cannot reorder the list.
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
            var name = DisplayName(a);
            if (name.Length > 0)
            {
                names.Add(name);
            }
        }
        if (names.Count < 2)
        {
            return names.Count == 0 ? "" : names[0];
        }
        // TRANSLATORS: put between the names of a conversation's participants ("Alice, Bob").
        return string.Join(L10n.C("participant list separator", ", "), names.Select(DisplayText.Isolate));
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

    /// <summary>
    /// Renders a byte count for attachment chips and the disk space of the
    /// preferences: GiB, MiB, KiB or B (IEC units).
    /// </summary>
    public static string FormatSize(long n)
    {
        if (n >= 1L << 30)
        {
            // TRANSLATORS: file size in gibibytes.
            return L10n.T("%.1f GiB", (double)n / (1L << 30));
        }
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
