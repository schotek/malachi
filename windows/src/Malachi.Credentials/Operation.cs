// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Request.swift (Operation); GTK:
// none. The raw values are in OperationRawValues.

namespace Malachi.Credentials;

/// <summary>What the daemon asks for: argv[1].</summary>
internal enum Operation
{
    /// <summary><c>get</c>: print the stored value.</summary>
    Get,

    /// <summary><c>set</c>: store the value, replacing an existing item.</summary>
    Set,

    /// <summary><c>delete</c>: remove the item; a missing one is success.</summary>
    Delete,
}
