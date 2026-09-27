// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/I18n/Strftime.swift; GTK:
// ui/internal/widget/format.go (strftime, g_date_time_format).
//
// The GTK UI keeps its date formats as strftime msgids (%H:%M, %-d %b,
// %Y-%m-%d, %a, %-d %b %Y at %H:%M) that translators rewrite; this maps them
// to .NET custom date formats at run time. What .NET does differently from
// ICU and GLib, and how it is met:
// - literal text is always quoted, so "at", "v" or "o'clock" is never read
//   as a field, and ':' and '/' never become the culture's separators; a
//   quote or backslash inside the literal is escaped with a backslash (the
//   ICU form '' ends the quoted run in .NET and drops the apostrophe);
// - a format of one letter is a standard format in .NET ("d" is the short
//   date, "H" throws), so a single field is written with a leading %;
// - an empty format renders the "G" standard format, so it renders as "";
// - %b and %B are GLib's month names "with day": the genitive in Czech
//   (2. ledna, not 2. leden). .NET uses the genitive only when the format
//   also has a day number; Format always does, as GLib and ICU do.

using System;
using System.Globalization;
using System.Runtime.CompilerServices;
using System.Text;

namespace Malachi.Core.I18n;

/// <summary>strftime patterns as .NET custom date and time formats.</summary>
public static class Strftime
{
    private static readonly ConditionalWeakTable<CultureInfo, DateTimeFormatInfo> Infos = new();

    /// <summary>
    /// The .NET custom format of <paramref name="strftime"/>. Directives
    /// that .NET has no field for are kept as text.
    /// </summary>
    public static string ToDateFormat(string strftime)
    {
        ArgumentNullException.ThrowIfNull(strftime);
        var output = new StringBuilder();
        var literal = new StringBuilder();
        var fields = 0;

        void Flush()
        {
            if (literal.Length == 0)
            {
                return;
            }
            output.Append('\'');
            foreach (var c in literal.ToString())
            {
                if (c is '\'' or '\\')
                {
                    output.Append('\\');
                }
                output.Append(c);
            }
            output.Append('\'');
            literal.Clear();
        }

        var i = 0;
        while (i < strftime.Length)
        {
            var c = strftime[i++];
            if (c != '%')
            {
                literal.Append(c);
                continue;
            }
            if (i >= strftime.Length)
            {
                literal.Append('%');
                break;
            }
            var d = strftime[i++];
            var noPad = false;
            if (d == '-')
            {
                noPad = true;
                if (i >= strftime.Length)
                {
                    literal.Append("%-");
                    break;
                }
                d = strftime[i++];
            }
            var field = Field(d, noPad);
            if (field is not null)
            {
                Flush();
                output.Append(field);
                fields++;
            }
            else if (d == '%')
            {
                literal.Append('%');
            }
            else
            {
                // Not a directive we know: keep it as text rather than drop it.
                literal.Append(noPad ? "%-" : "%").Append(d);
            }
        }
        Flush();
        // One letter alone is a standard format; % makes it a custom one.
        return output.Length == 1 && fields == 1 ? "%" + output : output.ToString();
    }

    /// <summary>
    /// Renders <paramref name="value"/> with <paramref name="strftime"/> in
    /// <paramref name="timeZone"/> (the local zone when null): names of days
    /// and months from <paramref name="culture"/>, months in the form used
    /// with a day number.
    /// </summary>
    public static string Format(DateTimeOffset value, string strftime, CultureInfo culture, TimeZoneInfo? timeZone = null)
    {
        ArgumentNullException.ThrowIfNull(culture);
        var format = ToDateFormat(strftime);
        if (format.Length == 0)
        {
            return "";
        }
        DateTimeOffset local;
        try
        {
            local = TimeZoneInfo.ConvertTime(value, timeZone ?? TimeZoneInfo.Local);
        }
        catch (ArgumentOutOfRangeException)
        {
            // Go's zero time west of UTC falls before year 1; show it as it is.
            local = value;
        }
        return local.ToString(format, FormatInfo(culture));
    }

    /// <summary>
    /// The date and time names of <paramref name="culture"/> with the month
    /// names replaced by the genitive ones, which .NET otherwise uses only
    /// when a day number is in the same format.
    /// </summary>
    public static DateTimeFormatInfo FormatInfo(CultureInfo culture)
    {
        ArgumentNullException.ThrowIfNull(culture);
        return Infos.GetValue(culture, static c =>
        {
            var info = (DateTimeFormatInfo)c.DateTimeFormat.Clone();
            info.MonthNames = WithFallback(info.MonthGenitiveNames, info.MonthNames);
            info.AbbreviatedMonthNames = WithFallback(info.AbbreviatedMonthGenitiveNames, info.AbbreviatedMonthNames);
            return DateTimeFormatInfo.ReadOnly(info);
        });
    }

    private static string[] WithFallback(string[] preferred, string[] fallback)
    {
        var result = (string[])fallback.Clone();
        for (var i = 0; i < result.Length && i < preferred.Length; i++)
        {
            if (!string.IsNullOrEmpty(preferred[i]))
            {
                result[i] = preferred[i];
            }
        }
        return result;
    }

    private static string? Field(char d, bool noPad) => d switch
    {
        'a' => "ddd",
        'A' => "dddd",
        'b' or 'h' => "MMM",
        'B' => "MMMM",
        'd' => noPad ? "d" : "dd",
        'e' => "d",
        'm' => noPad ? "M" : "MM",
        'Y' => "yyyy",
        'y' => "yy",
        'H' => noPad ? "H" : "HH",
        'I' => noPad ? "h" : "hh",
        'M' => noPad ? "m" : "mm",
        'S' => noPad ? "s" : "ss",
        'p' => "tt",
        // .NET has neither a zone abbreviation (%Z) nor an offset without a
        // colon (%z): both are the offset, +02:00.
        'Z' or 'z' => "zzz",
        _ => null,
    };
}
