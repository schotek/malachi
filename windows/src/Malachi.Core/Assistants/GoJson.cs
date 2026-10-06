// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantEvents.swift
// (GoJSON); GTK: what ui/internal/assistant/events.go asks of Go's
// encoding/json (json.Valid, and Unmarshal into json.RawMessage,
// map[string]json.RawMessage, []json.RawMessage, string, bool and float64).
// Not System.Text.Json, which differs from Go where hostile input lives:
// it refuses bad UTF-8 and lone surrogates in strings that Go reads as
// U+FFFD, and its depth limit is not Go's. Here, as in Swift's GoJSON: the
// same validity (json.Valid, nesting up to 10 000), the last of duplicate
// keys wins, a member of another type reads as its zero value (a number is
// never a bool, true never a number), bad UTF-8 in a string becomes U+FFFD,
// and a value stays the raw bytes of the line until it is read by type.
// Validation is iterative, so a deeply nested line never grows the stack
// of the thread that reads Claude Code's output. No texts here.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Runtime.InteropServices;

namespace Malachi.Core.Assistants;

/// <summary>
/// JSON read as Go's encoding/json reads it into <c>json.RawMessage</c> and
/// <c>map[string]json.RawMessage</c>: a value is a byte range of the line
/// (<c>Lo</c> inclusive, <c>Hi</c> exclusive), read by type only when asked for.
/// </summary>
internal static class GoJson
{
    /// <summary>encoding/json's maxNestingDepth.</summary>
    internal const int MaxDepth = 10000;

    // What json.Valid expects next: a value; a key (in an object); or what
    // follows a value.
    private enum Next
    {
        Value,
        Key,
        AfterValue,
    }

    /// <summary>
    /// json.Valid of <paramref name="b"/>[<paramref name="lo"/>..<paramref name="hi"/>):
    /// exactly one JSON value, white space around it allowed.
    /// </summary>
    internal static bool Valid(ReadOnlySpan<byte> b, int lo, int hi)
    {
        var i = lo;
        var stack = new List<bool>(); // true: an object
        var next = Next.Value;
        while (true)
        {
            i = SkipWs(b, i, hi);
            if (next == Next.Value)
            {
                if (i >= hi)
                {
                    return false;
                }
                if (b[i] is not ((byte)'{' or (byte)'['))
                {
                    i = ScalarEnd(b, i, hi);
                    if (i < 0)
                    {
                        return false;
                    }
                    next = Next.AfterValue;
                    continue;
                }
                var opensObject = b[i] == (byte)'{';
                stack.Add(opensObject);
                if (stack.Count > MaxDepth)
                {
                    return false;
                }
                i = SkipWs(b, i + 1, hi);
                if (i < hi && b[i] == Closer(opensObject))
                {
                    stack.RemoveAt(stack.Count - 1);
                    i++;
                    next = Next.AfterValue;
                }
                else
                {
                    next = opensObject ? Next.Key : Next.Value;
                }
            }
            else if (next == Next.Key)
            {
                if (i >= hi || b[i] != (byte)'"')
                {
                    return false;
                }
                var e = StringEnd(b, i, hi);
                if (e < 0)
                {
                    return false;
                }
                i = SkipWs(b, e, hi);
                if (i >= hi || b[i] != (byte)':')
                {
                    return false;
                }
                i++;
                next = Next.Value;
            }
            else
            {
                if (stack.Count == 0)
                {
                    return i == hi;
                }
                if (i >= hi)
                {
                    return false;
                }
                var inObject = stack[^1];
                var c = b[i];
                i++;
                if (c == (byte)',')
                {
                    next = inObject ? Next.Key : Next.Value;
                }
                else if (c == Closer(inObject))
                {
                    stack.RemoveAt(stack.Count - 1);
                    next = Next.AfterValue;
                }
                else
                {
                    return false;
                }
            }
        }
    }

    /// <summary>
    /// A raw value as a string, null when it is not one (null is not):
    /// escapes decoded, a lone surrogate and bad UTF-8 as U+FFFD, as Go's
    /// unquote does.
    /// </summary>
    internal static string? String(ReadOnlySpan<byte> b, (int Lo, int Hi)? r)
    {
        if (r is not { } range)
        {
            return null;
        }
        var start = First(b, range);
        if (start < 0 || b[start] != (byte)'"')
        {
            return null;
        }
        var close = StringEnd(b, start, range.Hi);
        var end = (close < 0 ? range.Hi : close) - 1;
        var output = new List<byte>(end - start);
        var i = start + 1;
        while (i < end)
        {
            var c = b[i];
            if (c == (byte)'\\' && b[i + 1] == (byte)'u')
            {
                var rr = U4(b, i, end);
                if (rr < 0)
                {
                    rr = 0xFFFD;
                }
                i += 6;
                if (rr is >= 0xD800 and <= 0xDFFF)
                {
                    var rr1 = U4(b, i, end);
                    if (rr < 0xDC00 && rr1 is >= 0xDC00 and < 0xE000)
                    {
                        rr = 0x10000 + (((rr - 0xD800) << 10) | (rr1 - 0xDC00));
                        i += 6;
                    }
                    else
                    {
                        rr = 0xFFFD;
                    }
                }
                Assistant.AppendUtf8(rr, output);
                continue;
            }
            if (c == (byte)'\\')
            {
                output.Add(b[i + 1] switch
                {
                    (byte)'b' => 0x08,
                    (byte)'f' => 0x0C,
                    (byte)'n' => 0x0A,
                    (byte)'r' => 0x0D,
                    (byte)'t' => 0x09,
                    var other => other, // " \ /
                });
                i += 2;
                continue;
            }
            if (c < 0x80)
            {
                output.Add(c);
                i++;
                continue;
            }
            var (r0, w) = Assistant.DecodeRune(b, i, end);
            if (r0 == 0xFFFD && w == 1)
            {
                Assistant.AppendUtf8(0xFFFD, output);
            }
            else
            {
                for (var k = i; k < i + w; k++)
                {
                    output.Add(b[k]);
                }
            }
            i += w;
        }
        return Assistant.FromUtf8(CollectionsMarshal.AsSpan(output));
    }

    /// <summary>A raw value as an array's elements; empty when it is not an array.</summary>
    internal static List<(int Lo, int Hi)> Array(ReadOnlySpan<byte> b, (int Lo, int Hi)? r)
    {
        var output = new List<(int Lo, int Hi)>();
        if (r is not { } range)
        {
            return output;
        }
        var i = First(b, range);
        if (i < 0 || b[i] != (byte)'[')
        {
            return output;
        }
        i++;
        while (i < range.Hi)
        {
            while (i < range.Hi && (IsWs(b[i]) || b[i] == (byte)','))
            {
                i++;
            }
            if (i >= range.Hi || b[i] == (byte)']')
            {
                break;
            }
            var e = SkipValue(b, i, range.Hi);
            output.Add((i, e));
            i = e;
        }
        return output;
    }

    /// <summary>A raw value as a bool: true for <c>true</c> only.</summary>
    internal static bool Boolean(ReadOnlySpan<byte> b, (int Lo, int Hi)? r)
    {
        if (r is not { } range)
        {
            return false;
        }
        var i = First(b, range);
        return i >= 0 && Literal(b, i, range.Hi, "true"u8);
    }

    /// <summary>A raw value as a float64: a number that fits, else 0.</summary>
    internal static double Number(ReadOnlySpan<byte> b, (int Lo, int Hi)? r)
    {
        if (r is not { } range)
        {
            return 0;
        }
        var i = First(b, range);
        if (i < 0 || !(b[i] == (byte)'-' || IsDigit(b[i])))
        {
            return 0;
        }
        var e = NumberEnd(b, i, range.Hi);
        if (e < 0)
        {
            e = i;
        }
        if (!double.TryParse(b[i..e], NumberStyles.Float, CultureInfo.InvariantCulture, out var d) || !double.IsFinite(d))
        {
            return 0;
        }
        return d;
    }

    /// <summary>Whether a raw value is missing or <c>null</c>.</summary>
    internal static bool IsNull(ReadOnlySpan<byte> b, (int Lo, int Hi)? r)
    {
        if (r is not { } range)
        {
            return true;
        }
        var i = First(b, range);
        return i < 0 || Literal(b, i, range.Hi, "null"u8);
    }

    /// <summary>
    /// A counter of usage: 0 for a missing or null value, else digits only
    /// (the line is valid JSON, so without a leading zero) that fit an
    /// Int64; null for anything else (Go's strconv.ParseInt of the digits).
    /// </summary>
    internal static long? Count(ReadOnlySpan<byte> b, (int Lo, int Hi)? r)
    {
        if (r is not { } range)
        {
            return 0;
        }
        var i = First(b, range);
        if (i < 0 || Literal(b, i, range.Hi, "null"u8))
        {
            return 0;
        }
        if (!IsDigit(b[i]))
        {
            return null;
        }
        var e = NumberEnd(b, i, range.Hi);
        if (e < 0)
        {
            return null;
        }
        for (var j = i; j < e; j++)
        {
            if (!IsDigit(b[j]))
            {
                return null;
            }
        }
        return long.TryParse(b[i..e], NumberStyles.None, CultureInfo.InvariantCulture, out var v) ? v : null;
    }

    private static bool IsWs(byte c) => c is 0x20 or 0x09 or 0x0A or 0x0D;

    private static bool IsDigit(byte c) => c is >= 0x30 and <= 0x39;

    private static bool IsHex(byte c) => c is (>= 0x30 and <= 0x39) or (>= 0x41 and <= 0x46) or (>= 0x61 and <= 0x66);

    private static int HexValue(byte c) => c switch
    {
        <= 0x39 => c - 0x30,
        <= 0x46 => c - 0x41 + 10,
        _ => c - 0x61 + 10,
    };

    private static int SkipWs(ReadOnlySpan<byte> b, int i, int end)
    {
        while (i < end && IsWs(b[i]))
        {
            i++;
        }
        return i;
    }

    private static bool Literal(ReadOnlySpan<byte> b, int i, int end, ReadOnlySpan<byte> word) =>
        end - i >= word.Length && b.Slice(i, word.Length).SequenceEqual(word);

    /// <summary>
    /// The end of the string starting at the quote <paramref name="b"/>[<paramref name="start"/>]
    /// (after its closing quote); -1 when it is not a valid string (an
    /// unescaped control byte, a bad escape, no closing quote). Bytes from
    /// 0x20 up, bad UTF-8 included, stand for themselves, as in Go's scanner.
    /// </summary>
    private static int StringEnd(ReadOnlySpan<byte> b, int start, int end)
    {
        var i = start + 1;
        while (i < end)
        {
            var c = b[i];
            if (c == (byte)'"')
            {
                return i + 1;
            }
            if (c == (byte)'\\')
            {
                if (i + 1 >= end)
                {
                    return -1;
                }
                switch (b[i + 1])
                {
                    case (byte)'"' or (byte)'\\' or (byte)'/' or (byte)'b' or (byte)'f' or (byte)'n' or (byte)'r' or (byte)'t':
                        i += 2;
                        break;
                    case (byte)'u':
                        if (i + 5 >= end || !IsHex(b[i + 2]) || !IsHex(b[i + 3]) || !IsHex(b[i + 4]) || !IsHex(b[i + 5]))
                        {
                            return -1;
                        }
                        i += 6;
                        break;
                    default:
                        return -1;
                }
                continue;
            }
            if (c < 0x20)
            {
                return -1;
            }
            i++;
        }
        return -1;
    }

    /// <summary>
    /// The end of the number starting at <paramref name="b"/>[<paramref name="start"/>],
    /// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?; -1 when there is none.
    /// </summary>
    private static int NumberEnd(ReadOnlySpan<byte> b, int start, int end)
    {
        var i = start;
        if (b[i] == (byte)'-')
        {
            i++;
        }
        if (i >= end || !IsDigit(b[i]))
        {
            return -1;
        }
        if (b[i] == (byte)'0')
        {
            i++;
        }
        else
        {
            while (i < end && IsDigit(b[i]))
            {
                i++;
            }
        }
        if (i < end && b[i] == (byte)'.')
        {
            i++;
            if (i >= end || !IsDigit(b[i]))
            {
                return -1;
            }
            while (i < end && IsDigit(b[i]))
            {
                i++;
            }
        }
        if (i < end && (b[i] == (byte)'e' || b[i] == (byte)'E'))
        {
            i++;
            if (i < end && (b[i] == (byte)'+' || b[i] == (byte)'-'))
            {
                i++;
            }
            if (i >= end || !IsDigit(b[i]))
            {
                return -1;
            }
            while (i < end && IsDigit(b[i]))
            {
                i++;
            }
        }
        return i;
    }

    /// <summary>The end of the (valid) value that starts at <paramref name="b"/>[<paramref name="start"/>].</summary>
    private static int SkipValue(ReadOnlySpan<byte> b, int start, int end)
    {
        var i = start;
        if (b[i] == (byte)'"')
        {
            var e = StringEnd(b, i, end);
            return e < 0 ? end : e;
        }
        if (b[i] is (byte)'{' or (byte)'[')
        {
            var depth = 0;
            while (i < end)
            {
                var c = b[i];
                if (c == (byte)'"')
                {
                    var e = StringEnd(b, i, end);
                    i = e < 0 ? end : e;
                    continue;
                }
                if (c is (byte)'{' or (byte)'[')
                {
                    depth++;
                }
                else if (c is (byte)'}' or (byte)']')
                {
                    depth--;
                    if (depth == 0)
                    {
                        return i + 1;
                    }
                }
                i++;
            }
            return end;
        }
        while (i < end && !(IsWs(b[i]) || b[i] is (byte)',' or (byte)'}' or (byte)']'))
        {
            i++;
        }
        return i;
    }

    /// <summary>
    /// The end of the string, number, <c>true</c>, <c>false</c> or
    /// <c>null</c> that starts at <paramref name="b"/>[<paramref name="i"/>];
    /// -1 when there is none.
    /// </summary>
    private static int ScalarEnd(ReadOnlySpan<byte> b, int i, int end) => b[i] switch
    {
        (byte)'"' => StringEnd(b, i, end),
        (byte)'-' or (>= (byte)'0' and <= (byte)'9') => NumberEnd(b, i, end),
        (byte)'t' => Literal(b, i, end, "true"u8) ? i + 4 : -1,
        (byte)'f' => Literal(b, i, end, "false"u8) ? i + 5 : -1,
        (byte)'n' => Literal(b, i, end, "null"u8) ? i + 4 : -1,
        _ => -1,
    };

    /// <summary>The byte that closes an object (<paramref name="isObject"/>) or an array.</summary>
    private static byte Closer(bool isObject) => isObject ? (byte)'}' : (byte)']';

    /// <summary>The first non-white-space position of a (valid) raw value; -1 when there is none.</summary>
    private static int First(ReadOnlySpan<byte> b, (int Lo, int Hi) r)
    {
        var i = r.Lo;
        while (i < r.Hi && IsWs(b[i]))
        {
            i++;
        }
        return i < r.Hi ? i : -1;
    }

    /// <summary>The value of a backslash-u escape with four hex digits at <paramref name="at"/>, before <paramref name="end"/>; -1 when there is none.</summary>
    private static int U4(ReadOnlySpan<byte> b, int at, int end)
    {
        if (at + 5 >= end || b[at] != (byte)'\\' || b[at + 1] != (byte)'u'
            || !IsHex(b[at + 2]) || !IsHex(b[at + 3]) || !IsHex(b[at + 4]) || !IsHex(b[at + 5]))
        {
            return -1;
        }
        return (HexValue(b[at + 2]) << 12) | (HexValue(b[at + 3]) << 8) | (HexValue(b[at + 4]) << 4) | HexValue(b[at + 5]);
    }

    /// <summary>A raw JSON object, its members by key (the last of duplicates).</summary>
    internal sealed class Object
    {
        private readonly Dictionary<string, (int Lo, int Hi)> members;

        private Object(byte[] b, Dictionary<string, (int Lo, int Hi)> members)
        {
            B = b;
            this.members = members;
        }

        /// <summary>The line the members are ranges of.</summary>
        internal byte[] B { get; }

        /// <summary>
        /// <paramref name="r"/> of <paramref name="b"/> as an object; null
        /// when <paramref name="r"/> is missing or not an object (null
        /// included).
        /// </summary>
        internal static Object? Of(byte[] b, (int Lo, int Hi)? r)
        {
            if (r is not { } range)
            {
                return null;
            }
            var i = First(b, range);
            if (i < 0 || b[i] != (byte)'{')
            {
                return null;
            }
            var members = new Dictionary<string, (int Lo, int Hi)>(StringComparer.Ordinal);
            i++;
            while (i < range.Hi)
            {
                while (i < range.Hi && (IsWs(b[i]) || b[i] == (byte)','))
                {
                    i++;
                }
                if (i >= range.Hi || b[i] != (byte)'"')
                {
                    break;
                }
                var keyEnd = StringEnd(b, i, range.Hi);
                if (keyEnd < 0)
                {
                    keyEnd = range.Hi;
                }
                var key = GoJson.String(b, (i, keyEnd)) ?? "";
                i = keyEnd;
                while (i < range.Hi && (IsWs(b[i]) || b[i] == (byte)':'))
                {
                    i++;
                }
                if (i >= range.Hi)
                {
                    break;
                }
                var e = SkipValue(b, i, range.Hi);
                members[key] = (i, e);
                i = e;
            }
            return new Object(b, members);
        }

        /// <summary>The member <paramref name="key"/> as a raw value; null when it is missing.</summary>
        internal (int Lo, int Hi)? Member(string key) => members.TryGetValue(key, out var r) ? r : null;

        /// <summary>The member as a string; "" when it is missing or not a string.</summary>
        internal string Str(string key) => GoJson.String(B, Member(key)) ?? "";

        /// <summary>The member as an object; null when it is missing or not an object.</summary>
        internal Object? Obj(string key) => Of(B, Member(key));

        /// <summary>The member as an array's elements; empty when it is missing or not an array.</summary>
        internal List<(int Lo, int Hi)> Array(string key) => GoJson.Array(B, Member(key));

        /// <summary>The member as a bool; false unless it is <c>true</c>.</summary>
        internal bool Boolean(string key) => GoJson.Boolean(B, Member(key));

        /// <summary>The member as a float64; 0 unless it is a number that fits.</summary>
        internal double Number(string key) => GoJson.Number(B, Member(key));
    }
}
