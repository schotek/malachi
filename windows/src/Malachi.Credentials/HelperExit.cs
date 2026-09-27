// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Request.swift (HelperExit).

namespace Malachi.Credentials;

/// <summary>
/// Exit statuses of the helper protocol (backend/internal/auth/helper). The
/// daemon reads 2 as "no such item" and 3 as a rejected request; every other
/// status is a keyringError carrying the helper's scrubbed stderr.
/// </summary>
internal enum HelperExit
{
    /// <summary>Done; for get, stdout holds the value line.</summary>
    Ok = 0,

    /// <summary>The store failed; stderr says why, never with the value.</summary>
    Failure = 1,

    /// <summary>No such item.</summary>
    NotFound = 2,

    /// <summary>Not a request of the protocol.</summary>
    BadRequest = 3,
}
