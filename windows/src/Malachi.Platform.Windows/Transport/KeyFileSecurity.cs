// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the security descriptor of the key file read through the
// handle WindowsKeyFilePolicy opened without following a reparse point
// (FileSecurity reads by path, which it follows). The counterpart of
// fstat's st_uid and st_mode in macos/Sources/MalachiCore/Transport/DaemonKey.swift.

using System.Runtime.InteropServices;
using System.Security.AccessControl;

namespace Malachi.Platform.Windows.Transport;

/// <summary>A file's owner and DACL, read from a handle (GetSecurityInfo).</summary>
internal sealed class KeyFileSecurity : ObjectSecurity<FileSystemRights>
{
    /// <summary>
    /// The owner and DACL of the file behind <paramref name="handle"/>. A
    /// NULL DACL reads as one entry that grants everyone everything, which is
    /// how Windows treats it.
    /// </summary>
    public KeyFileSecurity(SafeHandle handle)
        : base(isContainer: false, ResourceType.FileObject, handle, AccessControlSections.Owner | AccessControlSections.Access)
    {
    }
}
