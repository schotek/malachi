// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Unicode-scalar view of a C# string that the ports of
// macos/Sources/MalachiCore/Compose/AddressList.swift and Suggest.swift
// iterate (Swift's String.unicodeScalars, Go's range over a string). A lone
// surrogate, which neither a Swift string nor valid UTF-8 can hold, is one
// code unit that reads as -1: never a separator, never part of a name.

using System;
using System.Text;

namespace Malachi.Core.Compose;

/// <summary>Scalar iteration and offsets over UTF-16 strings.</summary>
internal static class ScalarText
{
    /// <summary>What a lone surrogate reads as.</summary>
    public const int Invalid = -1;

    /// <summary>
    /// The scalar at <paramref name="index"/> (or <see cref="Invalid"/>)
    /// and the number of code units it takes.
    /// </summary>
    public static int At(string s, int index, out int length)
    {
        if (Rune.DecodeFromUtf16(s.AsSpan(index), out var rune, out length) == System.Buffers.OperationStatus.Done)
        {
            return rune.Value;
        }
        length = 1;
        return Invalid;
    }

    /// <summary>The scalars of <paramref name="s"/>, a lone surrogate as <see cref="Invalid"/>.</summary>
    public static int[] Of(string s)
    {
        var output = new int[s.Length];
        var n = 0;
        for (var i = 0; i < s.Length;)
        {
            output[n++] = At(s, i, out var length);
            i += length;
        }
        return output[..n];
    }

    /// <summary>
    /// The number of scalars before the code unit <paramref name="index"/>
    /// (Swift: the scalar distance of a String.Index).
    /// </summary>
    public static int CountBefore(string s, int index)
    {
        var count = 0;
        for (var i = 0; i < index && i < s.Length;)
        {
            At(s, i, out var length);
            i += length;
            count++;
        }
        return count;
    }

    /// <summary>
    /// The code unit index of the scalar at <paramref name="offset"/>,
    /// clamped: a negative offset is the start, one past the end the end.
    /// </summary>
    public static int IndexOf(string s, int offset)
    {
        var i = 0;
        for (var n = 0; n < offset && i < s.Length; n++)
        {
            At(s, i, out var length);
            i += length;
        }
        return i;
    }

    /// <summary>Appends a scalar (never <see cref="Invalid"/>) to <paramref name="b"/>.</summary>
    public static void Append(StringBuilder b, int scalar)
    {
        if (scalar < 0x10000)
        {
            b.Append((char)scalar);
        }
        else
        {
            b.Append(char.ConvertFromUtf32(scalar));
        }
    }
}
