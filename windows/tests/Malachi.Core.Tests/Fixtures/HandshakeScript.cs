// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/Fixtures/FakeDaemon.swift
// (FakeDaemon.Script). Swift's hello closure gets the right result only;
// here it gets the connection's nonces and key as well (HelloContext),
// which the cases of backend/pkg/api/handshake_test.go that Swift left out
// need (a reflected proof, swapped nonces). Added: Latin1, for lines that
// are not UTF-8, which a C# string cannot carry.

using System;
using System.Text;

namespace Malachi.Core.Tests.Fixtures;

/// <summary>
/// What a scripted <see cref="FakeDaemon"/> writes
/// (<see cref="HandshakeMode.Raw"/>), as exact text: each line with its
/// "\n", or a line cut short.
/// </summary>
internal sealed record HandshakeScript
{
    /// <summary>
    /// Written for <c>system.hello</c>, given the right result and what it is
    /// made of (<see cref="HelloContext"/>); null writes nothing.
    /// </summary>
    public required Func<HelloContext, string?> Hello { get; init; }

    /// <summary>Closes the connection's write side after the <c>system.hello</c> answer.</summary>
    public bool CloseAfterHello { get; init; }

    /// <summary>
    /// Written for a <c>system.authenticate</c> with the right proof instead
    /// of its answer, given the client's nonce as hex; null writes nothing.
    /// </summary>
    public Func<string, string?> Authenticate { get; init; } = _ => null;

    /// <summary>Closes the connection's write side after that.</summary>
    public bool CloseAfterAuthenticate { get; init; }

    /// <summary>
    /// Writes each char of the script as one byte (ISO-8859-1) instead of
    /// UTF-8: "ÿ" is the byte 0xFF, which no UTF-8 text has. The right
    /// result is ASCII, the same either way.
    /// </summary>
    public bool Latin1 { get; init; }

    /// <summary>The bytes <paramref name="text"/> of the script is written as.</summary>
    public byte[] Encode(string text) => Latin1 ? Encoding.Latin1.GetBytes(text) : Encoding.UTF8.GetBytes(text);
}
