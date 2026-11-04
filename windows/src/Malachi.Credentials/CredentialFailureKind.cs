// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Keychain.swift (KeychainFailure's
// cases); GTK: none. Corrupt and TooLarge are Windows additions of the
// chunked store.

namespace Malachi.Credentials;

/// <summary>What went wrong in <see cref="CredentialStore"/>.</summary>
internal enum CredentialFailureKind
{
    /// <summary>No such item (Swift <c>notFound</c>).</summary>
    NotFound,

    /// <summary>A Credential Manager call failed (Swift <c>status(OSStatus)</c>).</summary>
    ApiFailed,

    /// <summary>
    /// The item cannot be read back as the value that was stored: it was
    /// written or changed by something else (no digest, or one that does not
    /// match), a chunk is missing or damaged, the header is not one this
    /// store writes, or the bytes are not UTF-8. Never answered with a value.
    /// </summary>
    Corrupt,

    /// <summary>
    /// The value is longer than <see cref="ChunkHeader.MaxLength"/> bytes, or
    /// its get answer longer than <see cref="Request.MaxAnswer"/>.
    /// </summary>
    TooLarge,
}
