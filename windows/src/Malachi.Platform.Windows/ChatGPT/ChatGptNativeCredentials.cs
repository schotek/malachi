// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC, no Swift/Go counterpart. Follows the native calls of
// Malachi.Credentials/Win32CredentialManager.cs, with a distinct namespace
// and no use of the daemon's keyring-helper protocol.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Security.Credentials;

namespace Malachi.Platform.Windows.ChatGPT;

internal static class ChatGptNativeCredentials
{
    internal static unsafe byte[]? Read(string name)
    {
        if (!PInvoke.CredRead(name, CRED_TYPE.CRED_TYPE_GENERIC, out var item))
        {
            var error = Marshal.GetLastPInvokeError();
            if (error == (int)WIN32_ERROR.ERROR_NOT_FOUND) return null;
            throw new Win32Exception(error);
        }
        try { return new ReadOnlySpan<byte>(item->CredentialBlob, checked((int)item->CredentialBlobSize)).ToArray(); }
        finally
        {
            new Span<byte>(item->CredentialBlob, checked((int)item->CredentialBlobSize)).Clear();
            PInvoke.CredFree(item);
        }
    }

    internal static unsafe void Write(string name, ReadOnlySpan<byte> value)
    {
        fixed (char* target = name)
        fixed (byte* blob = value)
        fixed (char* user = "Malachi Mail ChatGPT")
        {
            var item = new CREDENTIALW
            {
                Type = CRED_TYPE.CRED_TYPE_GENERIC, TargetName = target,
                CredentialBlob = blob, CredentialBlobSize = (uint)value.Length,
                Persist = CRED_PERSIST.CRED_PERSIST_LOCAL_MACHINE, UserName = user,
            };
            if (!PInvoke.CredWrite(&item, 0)) throw new Win32Exception(Marshal.GetLastPInvokeError());
        }
    }

    internal static void Delete(string name)
    {
        if (PInvoke.CredDelete(name, CRED_TYPE.CRED_TYPE_GENERIC)) return;
        var error = Marshal.GetLastPInvokeError();
        if (error != (int)WIN32_ERROR.ERROR_NOT_FOUND) throw new Win32Exception(error);
    }

    internal static unsafe List<string> List(string prefix)
    {
        if (!PInvoke.CredEnumerate(prefix + "*", out var count, out var items))
        {
            var error = Marshal.GetLastPInvokeError();
            if (error == (int)WIN32_ERROR.ERROR_NOT_FOUND) return [];
            throw new Win32Exception(error);
        }
        try
        {
            var result = new List<string>();
            for (var index = 0u; index < count; index++)
            {
                var item = items[index];
                new Span<byte>(item->CredentialBlob, checked((int)item->CredentialBlobSize)).Clear();
                if (item->Type == CRED_TYPE.CRED_TYPE_GENERIC) result.Add(item->TargetName.ToString());
            }
            return result;
        }
        finally { PInvoke.CredFree(items); }
    }
}
