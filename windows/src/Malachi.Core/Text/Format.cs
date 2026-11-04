// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Text/Format.swift; GTK:
// ui/internal/widget/format.go (DisplayName, FormatAddress,
// FormatParticipants, ThreadCountText, FormatDate, FormatTime,
// FormatDateTime, FormatSize, sizeNumber, strftime, AttachmentSummary).
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
// as LC_TIME is for GLib); numbers are Go's, whatever the culture, sizes
// with the translated decimal separator (3.0 MB, in Czech 3,0 MB).

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
    /// preferences in decimal units, like Finder and Outlook: B, kB, MB or
    /// GB, one decimal below 100 ("41.1 kB", "7.2 MB", "123 kB"), with the
    /// translated decimal separator ("41,1 kB" in Czech).
    /// </summary>
    public static string FormatSize(long n)
    {
        if (n < 1000)
        {
            // TRANSLATORS: file size in bytes.
            return L10n.T("%d B", n);
        }
        double v = n;
        if (n >= 999_500_000)
        {
            // TRANSLATORS: file size in gigabytes; %s is the number with its
            // decimal separator, e.g. "1.5".
            return L10n.T("%s GB", SizeNumber(v / 1e9));
        }
        if (n >= 999_500)
        {
            // TRANSLATORS: file size in megabytes; %s is the number with its
            // decimal separator, e.g. "7.2".
            return L10n.T("%s MB", SizeNumber(v / 1e6));
        }
        // TRANSLATORS: file size in kilobytes; %s is the number with its
        // decimal separator, e.g. "41.1".
        return L10n.T("%s kB", SizeNumber(v / 1e3));
    }

    // v with one decimal below 100 and none from there, with the translated
    // decimal separator (format.go sizeNumber).
    private static string SizeNumber(double v)
    {
        var s = v.ToString(v >= 99.95 ? "F0" : "F1", CultureInfo.InvariantCulture);
        // TRANSLATORS: the decimal separator of numbers such as file sizes
        // ("41.1 kB"); "," in Czech.
        var separator = L10n.C("decimal separator", ".");
        var dot = s.IndexOf('.', StringComparison.Ordinal);
        return dot < 0 ? s : string.Concat(s.AsSpan(0, dot), separator, s.AsSpan(dot + 1));
    }

    /// <summary>
    /// The line over the attachment tiles of a message: how many there are
    /// and their total size, "2 attachments · 255 kB" (format.go
    /// AttachmentSummary).
    /// </summary>
    public static string AttachmentSummary(int count, long total)
    {
        var files = L10n.N("%d attachment", "%d attachments", count);
        // TRANSLATORS: the line over the attachments of a message: the number
        // of attachments and their total size, e.g. "2 attachments · 255 kB".
        return L10n.Format(L10n.C("attachment summary", "%s · %s"), files, FormatSize(total));
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
