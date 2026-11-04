// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of api.ReadKeyFile's opening (backend/pkg/api/auth.go: Lstat, then
// open); the Go clients' rule, with nothing about owner or access.

using System.IO;
using Microsoft.Win32.SafeHandles;

namespace Malachi.Core.Transport;

/// <summary>
/// The key-file policy of the Go clients: the path must not be a link, a
/// directory or a device before it is opened, and nothing is asked of the
/// file's owner or access. Used where no platform policy is given (tests on
/// any OS); the Windows app passes its own.
/// </summary>
/// <remarks>
/// The path is opened with <see cref="File.OpenHandle"/>, which follows a
/// link swapped in between the check and the open; <see cref="DaemonKey"/>
/// checks the open file again. A named pipe at the path is not detected
/// before the open on Unix, where the open would then wait for a writer (the
/// Windows policy has no such gap; Windows has no FIFOs in the file system).
/// </remarks>
public sealed class PortableKeyFilePolicy : IKeyFilePolicy
{
    /// <summary>The one instance; the policy has no state.</summary>
    public static PortableKeyFilePolicy Instance { get; } = new();

    /// <inheritdoc/>
    public SafeFileHandle Open(string path)
    {
        // Lstat's counterpart: the attributes of the path itself, a link not
        // followed.
        var attributes = File.GetAttributes(path);
        if ((attributes & FileAttributes.ReparsePoint) != 0)
        {
            throw new KeyUnavailableException(KeyFileReason.SymbolicLink(path));
        }
        if ((attributes & (FileAttributes.Directory | FileAttributes.Device)) != 0)
        {
            throw new KeyUnavailableException(KeyFileReason.NotRegular(path));
        }
        return File.OpenHandle(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
    }

    /// <inheritdoc/>
    public string? Check(SafeFileHandle handle, string path) => null;
}
