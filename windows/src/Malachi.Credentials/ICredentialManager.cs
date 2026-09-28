// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart (KeychainStore calls the Security
// framework directly): the seam between CredentialStore, which knows
// requests and chunks, and Credential Manager, so that the chunking can be
// tested against items in memory.

using System;
using System.Collections.Generic;

namespace Malachi.Credentials;

/// <summary>
/// The generic credentials of the user's Credential Manager, raw: items
/// named by their target name, which is matched without regard to case.
/// Every call returns a Win32 error: 0 on success,
/// <c>ERROR_NOT_FOUND</c> (1168) when there is no such item, or another.
/// </summary>
internal interface ICredentialManager
{
    /// <summary>CredReadW: the item, or ERROR_NOT_FOUND.</summary>
    int Read(string targetName, out GenericCredential? credential);

    /// <summary>
    /// CredWriteW: creates the item or replaces it, persisted for this user
    /// on this computer (CRED_PERSIST_LOCAL_MACHINE), with the digest as its
    /// one attribute; an empty digest writes an item without attributes.
    /// </summary>
    int Write(string targetName, string userName, string comment, ReadOnlySpan<byte> blob, ReadOnlySpan<byte> digest);

    /// <summary>CredDeleteW: removes the item, or ERROR_NOT_FOUND.</summary>
    int Delete(string targetName);

    /// <summary>
    /// CredEnumerateW with the filter <c>prefix*</c>: the names of the
    /// generic credentials that begin with the prefix; none is success with
    /// an empty list.
    /// </summary>
    int List(string prefix, out IReadOnlyList<string> targetNames);
}
