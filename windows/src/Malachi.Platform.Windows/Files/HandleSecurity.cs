// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the security descriptor of a directory read and written
// through a handle, which PrivateDirectory opens without following a
// reparse point (DirectorySecurity has only the path, which it follows).

using System.Runtime.InteropServices;
using System.Security.AccessControl;

namespace Malachi.Platform.Windows.Files;

/// <summary>
/// A directory's owner and DACL, read from and written to a handle
/// (GetSecurityInfo, SetSecurityInfo).
/// </summary>
internal sealed class HandleSecurity : ObjectSecurity<FileSystemRights>
{
    /// <summary>An empty descriptor to fill in and write.</summary>
    public HandleSecurity()
        : base(isContainer: true, ResourceType.FileObject)
    {
    }

    /// <summary>The owner and DACL of the directory behind <paramref name="handle"/>.</summary>
    public HandleSecurity(SafeHandle handle)
        : base(isContainer: true, ResourceType.FileObject, handle, AccessControlSections.Owner | AccessControlSections.Access)
    {
    }

    /// <summary>
    /// Writes <paramref name="sections"/> to the directory behind
    /// <paramref name="handle"/>; a protected DACL goes as protected, and
    /// the system passes its inheritable entries on to what the directory
    /// holds.
    /// </summary>
    public void Write(SafeHandle handle, AccessControlSections sections) => Persist(handle, sections);
}
