// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Coding.swift; Go: encoding/json and
// time.Time as backend/pkg/api uses them.
//
// Helpers that make the Go contract's JSON habits decode into plain C#: a
// nil slice that encoding/json writes as null, members Go decodes as their
// zero value when they are null (CertificateInfo), and RFC 3339 dates with
// or without fractional seconds and with Z or a numeric offset.
//
// Two traps of System.Text.Json's source generation shape the records that
// use them. It reads an init-only property like a constructor argument, so
// a member absent from the JSON gets its type's default, not the
// property's initializer: every list and string that must never be null
// coalesces in its init accessor. And records compare lists by reference,
// unlike Swift's structs: compare the members of a list, not two records
// holding lists.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.Json.Serialization.Metadata;

namespace Malachi.Core.Api;

/// <summary>
/// A list the daemon may send as null or leave out (Go's nil slice on a field
/// without omitempty), read as an empty list (Swift <c>@NullAsEmpty</c>).
/// Always encodes as an array. The property also coalesces null in its init
/// accessor, for the member that is absent altogether.
/// </summary>
public sealed class NullAsEmptyListConverter<T> : JsonConverter<IReadOnlyList<T>>
{
    /// <inheritdoc/>
    public override bool HandleNull => true;

    /// <inheritdoc/>
    public override IReadOnlyList<T> Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType == JsonTokenType.Null)
        {
            return [];
        }
        if (reader.TokenType != JsonTokenType.StartArray)
        {
            throw new JsonException($"expected an array, not {reader.TokenType}");
        }
        var element = ElementInfo(options);
        var list = new List<T>();
        while (reader.Read() && reader.TokenType != JsonTokenType.EndArray)
        {
            var item = JsonSerializer.Deserialize(ref reader, element);
            if (item is null)
            {
                throw new JsonException("null element in an array");
            }
            list.Add(item);
        }
        return list;
    }

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, IReadOnlyList<T> value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        var element = ElementInfo(options);
        writer.WriteStartArray();
        foreach (var item in value ?? [])
        {
            JsonSerializer.Serialize(writer, item, element);
        }
        writer.WriteEndArray();
    }

    private static JsonTypeInfo<T> ElementInfo(JsonSerializerOptions options) =>
        (JsonTypeInfo<T>)options.GetTypeInfo(typeof(T));
}

/// <summary>A string Go decodes as "" when it is null (Swift <c>decodeIfPresent ?? ""</c>).</summary>
public sealed class NullAsEmptyStringConverter : JsonConverter<string>
{
    /// <inheritdoc/>
    public override bool HandleNull => true;

    /// <inheritdoc/>
    public override string Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        reader.TokenType switch
        {
            JsonTokenType.Null => "",
            JsonTokenType.String => reader.GetString()!,
            _ => throw new JsonException($"expected a string, not {reader.TokenType}"),
        };

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, string value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        writer.WriteStringValue(value ?? "");
    }
}

/// <summary>A bool Go decodes as false when it is null (Swift <c>decodeIfPresent ?? false</c>).</summary>
public sealed class NullAsFalseConverter : JsonConverter<bool>
{
    /// <inheritdoc/>
    public override bool HandleNull => true;

    /// <inheritdoc/>
    public override bool Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        reader.TokenType switch
        {
            JsonTokenType.Null => false,
            JsonTokenType.True => true,
            JsonTokenType.False => false,
            _ => throw new JsonException($"expected a bool, not {reader.TokenType}"),
        };

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, bool value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        writer.WriteBooleanValue(value);
    }
}

/// <summary>
/// An RFC 3339 date Go decodes as its zero time when it is null (Swift
/// <c>decodeIfPresent ?? .goZero</c>).
/// </summary>
public sealed class NullAsGoZeroConverter : JsonConverter<DateTimeOffset>
{
    /// <inheritdoc/>
    public override bool HandleNull => true;

    /// <inheritdoc/>
    public override DateTimeOffset Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        reader.TokenType == JsonTokenType.Null ? DateTimeOffset.GoZero : Rfc3339Converter.ReadDate(ref reader);

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, DateTimeOffset value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        writer.WriteStringValue(Rfc3339.Format(value));
    }
}

/// <summary>
/// Every date of the contract (Swift <c>JSONCoding</c>'s date strategies):
/// read with <see cref="Rfc3339.Parse(string)"/>, a zone required, and
/// written with <see cref="Rfc3339.Format"/>, in UTC with <c>Z</c>. It is one
/// of the wire options' converters (<see cref="ApiJsonContext"/>), so it
/// applies to every <see cref="DateTimeOffset"/> member.
/// </summary>
public sealed class Rfc3339Converter : JsonConverter<DateTimeOffset>
{
    /// <inheritdoc/>
    public override DateTimeOffset Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        ReadDate(ref reader);

    /// <inheritdoc/>
    public override void Write(Utf8JsonWriter writer, DateTimeOffset value, JsonSerializerOptions options)
    {
        ArgumentNullException.ThrowIfNull(writer);
        writer.WriteStringValue(Rfc3339.Format(value));
    }

    internal static DateTimeOffset ReadDate(ref Utf8JsonReader reader)
    {
        if (reader.TokenType == JsonTokenType.String && Rfc3339.Parse(reader.GetString()!) is { } date)
        {
            return date;
        }
        throw new JsonException("not an RFC 3339 date");
    }
}

/// <summary>
/// RFC 3339 timestamps as Go's <c>time.Time</c> writes them
/// (<c>2026-09-02T10:00:00Z</c>, with up to nine fractional digits, <c>Z</c>
/// or <c>+hh:mm</c>), parsed without a calendar so that the result is the
/// same on every locale and for year 1.
/// </summary>
public static class Rfc3339
{
    private const long TicksPerSecond = TimeSpan.TicksPerSecond;
    private static readonly long UnixEpochTicks = DateTimeOffset.UnixEpoch.UtcTicks;

    /// <summary>
    /// Parses one timestamp; null when the string is not one. The result is
    /// in UTC. Digits beyond the seventh are cut (a DateTimeOffset holds 100 ns
    /// ticks). A timestamp outside what a DateTimeOffset holds, Go's year 0
    /// above all, is clamped to <see cref="DateTimeOffset.MinValue"/> (Go's
    /// zero time) or <see cref="DateTimeOffset.MaxValue"/> instead of failing
    /// the whole result that carries it.
    /// </summary>
    public static DateTimeOffset? Parse(string s) => s is null ? null : Parse(s.AsSpan());

    /// <inheritdoc cref="Parse(string)"/>
    public static DateTimeOffset? Parse(ReadOnlySpan<char> s)
    {
        // 0001-01-01T00:00:00Z is the shortest form.
        if (s.Length < 20)
        {
            return null;
        }
        if (!Digits(s, 0, 4, out var year) || s[4] != '-'
            || !Digits(s, 5, 2, out var month) || s[7] != '-'
            || !Digits(s, 8, 2, out var day) || (s[10] != 'T' && s[10] != 't')
            || !Digits(s, 11, 2, out var hour) || s[13] != ':'
            || !Digits(s, 14, 2, out var minute) || s[16] != ':'
            || !Digits(s, 17, 2, out var second))
        {
            return null;
        }
        if (month is < 1 or > 12 || day is < 1 or > 31 || hour >= 24 || minute >= 60 || second > 60)
        {
            return null;
        }
        var i = 19;
        long fraction = 0;
        if (i < s.Length && s[i] == '.')
        {
            i++;
            var start = i;
            // The first digit is worth a tenth of a second, 1,000,000 ticks;
            // after the seventh a digit is worth nothing.
            var scale = TicksPerSecond / 10;
            while (i < s.Length && char.IsAsciiDigit(s[i]))
            {
                fraction += (s[i] - '0') * scale;
                scale /= 10;
                i++;
            }
            if (i == start)
            {
                return null;
            }
        }
        if (i >= s.Length)
        {
            return null;
        }
        long offset = 0;
        if (s[i] is 'Z' or 'z')
        {
            i++;
        }
        else if (s[i] is '+' or '-')
        {
            var sign = s[i] == '+' ? 1 : -1;
            if (s.Length < i + 6 || !Digits(s, i + 1, 2, out var oh) || s[i + 3] != ':'
                || !Digits(s, i + 4, 2, out var om) || oh >= 24 || om >= 60)
            {
                return null;
            }
            offset = sign * ((oh * 3600L) + (om * 60L));
            i += 6;
        }
        else
        {
            return null;
        }
        if (i != s.Length)
        {
            return null;
        }
        var days = DaysFromCivil(year, month, day);
        var seconds = (days * 86400) + (hour * 3600L) + (minute * 60L) + second - offset;
        var ticks = UnixEpochTicks + (seconds * TicksPerSecond) + fraction;
        ticks = Math.Clamp(ticks, DateTimeOffset.MinValue.UtcTicks, DateTimeOffset.MaxValue.UtcTicks);
        return new DateTimeOffset(ticks, TimeSpan.Zero);
    }

    /// <summary>
    /// Formats an instant as Go's <c>time.Time</c> would: RFC 3339 in UTC, the
    /// fraction only when there is one, without trailing zeros. Swift rounds
    /// to microseconds because its Date is a double; a DateTimeOffset is exact
    /// to 100 ns and keeps them.
    /// </summary>
    public static string Format(DateTimeOffset date)
    {
        var sinceEpoch = date.UtcTicks - UnixEpochTicks;
        var seconds = FloorDiv(sinceEpoch, TicksPerSecond);
        var fraction = sinceEpoch - (seconds * TicksPerSecond);
        var days = FloorDiv(seconds, 86400);
        var secondOfDay = seconds - (days * 86400);
        var (year, month, day) = CivilFromDays(days);
        var text = string.Create(
            CultureInfo.InvariantCulture,
            $"{year:D4}-{month:D2}-{day:D2}T{secondOfDay / 3600:D2}:{secondOfDay / 60 % 60:D2}:{secondOfDay % 60:D2}");
        if (fraction != 0)
        {
            text += "." + fraction.ToString("D7", CultureInfo.InvariantCulture).TrimEnd('0');
        }
        return text + "Z";
    }

    /// <summary>
    /// Days since 1970-01-01 of a proleptic Gregorian date (Howard Hinnant's
    /// days_from_civil), valid for every year Go can produce.
    /// </summary>
    internal static long DaysFromCivil(long year, long month, long day)
    {
        var y = month <= 2 ? year - 1 : year;
        var era = (y >= 0 ? y : y - 399) / 400;
        var yoe = y - (era * 400);
        var doy = (((153 * (month + (month > 2 ? -3 : 9))) + 2) / 5) + day - 1;
        var doe = (yoe * 365) + (yoe / 4) - (yoe / 100) + doy;
        return (era * 146097) + doe - 719468;
    }

    /// <summary>The inverse of <see cref="DaysFromCivil"/> (civil_from_days).</summary>
    internal static (long Year, long Month, long Day) CivilFromDays(long days)
    {
        var z = days + 719468;
        var era = (z >= 0 ? z : z - 146096) / 146097;
        var doe = z - (era * 146097);
        var yoe = (doe - (doe / 1460) + (doe / 36524) - (doe / 146096)) / 365;
        var doy = doe - ((365 * yoe) + (yoe / 4) - (yoe / 100));
        var mp = ((5 * doy) + 2) / 153;
        var day = doy - (((153 * mp) + 2) / 5) + 1;
        var month = mp < 10 ? mp + 3 : mp - 9;
        var year = yoe + (era * 400) + (month <= 2 ? 1 : 0);
        return (year, month, day);
    }

    private static long FloorDiv(long a, long b)
    {
        var q = a / b;
        return (a % b != 0 && (a < 0) != (b < 0)) ? q - 1 : q;
    }

    private static bool Digits(ReadOnlySpan<char> s, int start, int count, out int value)
    {
        value = 0;
        if (s.Length < start + count)
        {
            return false;
        }
        for (var i = start; i < start + count; i++)
        {
            if (!char.IsAsciiDigit(s[i]))
            {
                return false;
            }
            value = (value * 10) + (s[i] - '0');
        }
        return true;
    }
}

/// <summary>Swift's <c>extension Date</c> of Coding.swift, as extension members of <see cref="DateTimeOffset"/>.</summary>
public static class GoTime
{
    extension(DateTimeOffset date)
    {
        /// <summary>
        /// Go's zero <c>time.Time</c> (0001-01-01T00:00:00Z), which the daemon
        /// writes for a date it never set (<c>Draft.UpdatedAt</c> in a
        /// <c>draft.create</c> result); <see cref="DateTimeOffset.MinValue"/>.
        /// </summary>
        public static DateTimeOffset GoZero => DateTimeOffset.MinValue;

        /// <summary>
        /// True for any instant in year 1: what Go's <c>time.Time.IsZero</c>
        /// says of the value once it crossed the wire (fractions are lost, not
        /// the year).
        /// </summary>
        public bool IsGoZero => date.UtcDateTime.Year == 1;
    }
}
