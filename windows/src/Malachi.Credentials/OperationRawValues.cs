// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Request.swift (Operation's String
// raw values): Operation(rawValue:) and .rawValue as C# extension members,
// so the call sites read as they do in Swift.

using System;

namespace Malachi.Credentials;

/// <summary>The command-line words of <see cref="Operation"/>.</summary>
internal static class OperationRawValues
{
    extension(Operation operation)
    {
        /// <summary>The word on the command line: get, set or delete.</summary>
        public string RawValue => operation switch
        {
            Operation.Get => "get",
            Operation.Set => "set",
            Operation.Delete => "delete",
            _ => throw new ArgumentOutOfRangeException(nameof(operation)),
        };
    }

    extension(Operation)
    {
        /// <summary>The operation a word names, or null; the words are case-sensitive.</summary>
        public static Operation? FromRawValue(string? rawValue) => rawValue switch
        {
            "get" => Operation.Get,
            "set" => Operation.Set,
            "delete" => Operation.Delete,
            _ => null,
        };
    }
}
