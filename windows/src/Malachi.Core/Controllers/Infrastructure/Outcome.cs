// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of Swift's Result<M.Result, any Error>, which the
// controllers of macos/Sources/MalachiCore/Controllers hand to their
// callbacks (MailboxController.perform): docs/windows-port.md §7.2. The
// generic value and its non-generic factory share the file, as they share
// the name.

using System;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// How a background call ended: its value, or the exception it failed
/// with (Swift <c>Result</c>). Made by <see cref="Outcome.Success{T}(T)"/>
/// and <see cref="Outcome.Failure{T}(Exception)"/>.
/// </summary>
/// <typeparam name="T">The value of a success.</typeparam>
public readonly record struct Outcome<T>
{
    internal Outcome(T? value, Exception? error)
    {
        Value = value;
        Error = error;
    }

    /// <summary>The value; the type's default for a failure.</summary>
    public T? Value { get; }

    /// <summary>The failure; null for a success.</summary>
    public Exception? Error { get; }

    /// <summary>Whether the call succeeded.</summary>
    public bool IsSuccess => Error is null;

    /// <summary>The value of a success; false with the failure otherwise (Swift's <c>switch</c>).</summary>
    public bool TryGetValue(out T value, out Exception? error)
    {
        value = Value!;
        error = Error;
        return Error is null;
    }
}

/// <summary>Makes <see cref="Outcome{T}"/> values.</summary>
public static class Outcome
{
    /// <summary>A success with <paramref name="value"/>.</summary>
    public static Outcome<T> Success<T>(T value) => new(value, null);

    /// <summary>A failure with <paramref name="error"/>.</summary>
    public static Outcome<T> Failure<T>(Exception error)
    {
        ArgumentNullException.ThrowIfNull(error);
        return new(default, error);
    }
}
