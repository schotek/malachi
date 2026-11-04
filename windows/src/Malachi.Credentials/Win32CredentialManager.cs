// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: ICredentialManager over the Win32
// credential functions (CsWin32, NativeMethods.txt), the counterpart of the
// SecItem calls in macos/Sources/MalachiKeychain/Keychain.swift. Generic
// credentials, persisted per user on this computer: secrets do not roam to
// machines where the store does not exist. Every credential blob this reads
// is zeroed in native memory before CredFree.

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Security.Credentials;

namespace Malachi.Credentials;

/// <summary>The user's Credential Manager.</summary>
internal sealed class Win32CredentialManager : ICredentialManager
{
    /// <summary>
    /// The keyword of the one application attribute an item may carry, the
    /// digest of <see cref="ICredentialManager.Write"/> (the
    /// <c>&lt;CompanyName&gt;_&lt;Name&gt;</c> form CREDENTIAL_ATTRIBUTEW
    /// asks for). Credential Manager's own dialogs do not show attributes.
    /// </summary>
    public const string DigestKeyword = "Malachi_SHA256";

    /// <inheritdoc/>
    public unsafe int Read(string targetName, out GenericCredential? credential)
    {
        credential = null;
        if (!PInvoke.CredRead(targetName, CRED_TYPE.CRED_TYPE_GENERIC, out var native))
        {
            return Marshal.GetLastPInvokeError();
        }
        try
        {
            var blob = Blob(native);
            credential = new GenericCredential(
                native->TargetName.ToString() ?? targetName,
                native->UserName.Value is null ? null : native->UserName.ToString(),
                native->Comment.Value is null ? null : native->Comment.ToString(),
                blob.ToArray(),
                Digest(native));
            return (int)WIN32_ERROR.ERROR_SUCCESS;
        }
        finally
        {
            Blob(native).Clear();
            PInvoke.CredFree(native);
        }
    }

    /// <inheritdoc/>
    public unsafe int Write(string targetName, string userName, string comment, ReadOnlySpan<byte> blob, ReadOnlySpan<byte> digest)
    {
        fixed (char* target = targetName)
        fixed (char* user = userName)
        fixed (char* note = comment)
        fixed (char* keyword = DigestKeyword)
        fixed (byte* data = blob)
        fixed (byte* check = digest)
        {
            var attribute = new CREDENTIAL_ATTRIBUTEW
            {
                Keyword = keyword,
                ValueSize = (uint)digest.Length,
                Value = check,
            };
            var credential = new CREDENTIALW
            {
                Type = CRED_TYPE.CRED_TYPE_GENERIC,
                TargetName = target,
                Comment = note,
                CredentialBlobSize = (uint)blob.Length,
                CredentialBlob = data,
                Persist = CRED_PERSIST.CRED_PERSIST_LOCAL_MACHINE,
                AttributeCount = digest.IsEmpty ? 0u : 1u,
                Attributes = digest.IsEmpty ? null : &attribute,
                UserName = user,
            };
            return PInvoke.CredWrite(&credential, 0) ? (int)WIN32_ERROR.ERROR_SUCCESS : Marshal.GetLastPInvokeError();
        }
    }

    /// <inheritdoc/>
    public int Delete(string targetName) =>
        PInvoke.CredDelete(targetName, CRED_TYPE.CRED_TYPE_GENERIC) ? (int)WIN32_ERROR.ERROR_SUCCESS : Marshal.GetLastPInvokeError();

    /// <inheritdoc/>
    public unsafe int List(string prefix, out IReadOnlyList<string> targetNames)
    {
        targetNames = [];
        if (!PInvoke.CredEnumerate(prefix + "*", out var count, out var items))
        {
            var error = Marshal.GetLastPInvokeError();
            return error == (int)WIN32_ERROR.ERROR_NOT_FOUND ? (int)WIN32_ERROR.ERROR_SUCCESS : error;
        }
        try
        {
            var names = new List<string>((int)count);
            for (var i = 0; i < count; i++)
            {
                var item = items[i];
                // Only the names are wanted; the secrets were read anyway.
                Blob(item).Clear();
                if (item->Type == CRED_TYPE.CRED_TYPE_GENERIC && item->TargetName.Value is not null)
                {
                    names.Add(item->TargetName.ToString());
                }
            }
            targetNames = names;
            return (int)WIN32_ERROR.ERROR_SUCCESS;
        }
        finally
        {
            PInvoke.CredFree(items);
        }
    }

    private static unsafe Span<byte> Blob(CREDENTIALW* credential) =>
        credential->CredentialBlob is null
            ? []
            : new Span<byte>(credential->CredentialBlob, checked((int)credential->CredentialBlobSize));

    // The value of the DigestKeyword attribute, or null.
    private static unsafe byte[]? Digest(CREDENTIALW* credential)
    {
        for (var i = 0; i < credential->AttributeCount; i++)
        {
            var attribute = &credential->Attributes[i];
            if (attribute->Keyword.Value is not null
                && string.Equals(attribute->Keyword.ToString(), DigestKeyword, StringComparison.OrdinalIgnoreCase))
            {
                return attribute->Value is null
                    ? []
                    : new ReadOnlySpan<byte>(attribute->Value, checked((int)attribute->ValueSize)).ToArray();
            }
        }
        return null;
    }
}
