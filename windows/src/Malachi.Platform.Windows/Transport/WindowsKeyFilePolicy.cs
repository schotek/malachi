// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the owner and mode checks of
// macos/Sources/MalachiCore/Transport/DaemonKey.swift (O_NOFOLLOW, S_IFREG,
// st_uid == geteuid(), st_mode & 077 == 0) for Windows: a listed deviation
// from the Go clients (windows/README.md; docs/windows-port.md §5), the
// counterpart of macOS's M27.

using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using Malachi.Core.Transport;
using Microsoft.Win32.SafeHandles;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Storage.FileSystem;

namespace Malachi.Platform.Windows.Transport;

/// <summary>
/// The Windows client's key-file policy: the file is opened as itself, a
/// link or junction never followed; it must be a file on disk (not a pipe
/// or a device), owned by this user, and its DACL may let nobody but this
/// user, SYSTEM, the Administrators and OWNER RIGHTS read or change it. A
/// NULL DACL, which lets everyone in, is refused. The daemon's key file in
/// the default run directory inherits exactly the user, SYSTEM and
/// Administrators (measured); security.md §8 leaves the latter two outside
/// the threat model, as every default ACL of the profile does.
/// </summary>
/// <remarks>
/// "Read or change" is any right that reaches the key: reading or writing
/// the data (directly or through GENERIC_READ, GENERIC_WRITE, GENERIC_ALL),
/// and changing the DACL or the owner, which grants the rest. Deny entries
/// only take away and are not looked at; inherit-only entries do not apply
/// to the file. When the process runs elevated, a file it creates may be
/// owned by the Administrators group, the token's default owner; that
/// owner counts as this user's, as the daemon started by the same elevated
/// app writes its key file so.
/// </remarks>
public sealed class WindowsKeyFilePolicy : IKeyFilePolicy
{
    // Every right that reads or changes the key, or lets its holder grant
    // itself that: FILE_READ_DATA, FILE_WRITE_DATA, FILE_APPEND_DATA,
    // WRITE_DAC, WRITE_OWNER, GENERIC_ALL, GENERIC_WRITE, GENERIC_READ.
    private const int ReachesTheKey = 0x0001 | 0x0002 | 0x0004 | 0x0004_0000 | 0x0008_0000
        | 0x1000_0000 | 0x4000_0000 | unchecked((int)0x8000_0000);

    private const uint GenericRead = 0x8000_0000;

    private static readonly SecurityIdentifier LocalSystem = new(WellKnownSidType.LocalSystemSid, null);
    private static readonly SecurityIdentifier Administrators = new(WellKnownSidType.BuiltinAdministratorsSid, null);
    private static readonly SecurityIdentifier OwnerRights = new("S-1-3-4");

    private readonly SecurityIdentifier user;
    private readonly SecurityIdentifier? tokenOwner;

    /// <summary>The policy for the current user.</summary>
    public WindowsKeyFilePolicy()
    {
        using var identity = WindowsIdentity.GetCurrent();
        user = identity.User ?? throw new InvalidOperationException("the process token has no user");
        tokenOwner = identity.Owner;
    }

    /// <summary>The policy as if <paramref name="user"/> ran it, for tests.</summary>
    internal WindowsKeyFilePolicy(SecurityIdentifier user, SecurityIdentifier? tokenOwner)
    {
        this.user = user;
        this.tokenOwner = tokenOwner;
    }

    /// <inheritdoc/>
    public SafeFileHandle Open(string path)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        // FILE_FLAG_OPEN_REPARSE_POINT opens a link as itself (O_NOFOLLOW),
        // which DaemonKey then refuses; FILE_FLAG_BACKUP_SEMANTICS lets a
        // directory open as well, to be refused as not a regular file rather
        // than as an access error.
        var handle = PInvoke.CreateFile(
            path,
            GenericRead,
            FILE_SHARE_MODE.FILE_SHARE_READ | FILE_SHARE_MODE.FILE_SHARE_WRITE | FILE_SHARE_MODE.FILE_SHARE_DELETE,
            null,
            FILE_CREATION_DISPOSITION.OPEN_EXISTING,
            FILE_FLAGS_AND_ATTRIBUTES.FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAGS_AND_ATTRIBUTES.FILE_FLAG_BACKUP_SEMANTICS,
            null);
        if (!handle.IsInvalid)
        {
            return handle;
        }
        var error = (WIN32_ERROR)Marshal.GetLastPInvokeError();
        handle.Dispose();
        var message = path + ": " + new Win32Exception((int)error).Message;
        throw error switch
        {
            WIN32_ERROR.ERROR_FILE_NOT_FOUND => new FileNotFoundException(message, path),
            WIN32_ERROR.ERROR_PATH_NOT_FOUND => new DirectoryNotFoundException(message),
            WIN32_ERROR.ERROR_ACCESS_DENIED => new UnauthorizedAccessException(message),
            _ => (Exception)new IOException(message, unchecked((int)(0x8007_0000 | (uint)error))),
        };
    }

    /// <inheritdoc/>
    public string? Check(SafeFileHandle handle, string path)
    {
        ArgumentNullException.ThrowIfNull(handle);
        // A pipe or a console at the path opens like a file.
        if (PInvoke.GetFileType(handle) != FILE_TYPE.FILE_TYPE_DISK)
        {
            return KeyFileReason.NotRegular(path);
        }
        KeyFileSecurity security;
        try
        {
            security = new KeyFileSecurity(handle);
        }
        catch (Exception e) when (e is UnauthorizedAccessException or InvalidOperationException or IOException)
        {
            return KeyFileReason.CannotInspect(path, $"its security descriptor cannot be read ({e.GetType().Name})");
        }
        if (security.GetOwner(typeof(SecurityIdentifier)) is not SecurityIdentifier owner || !(owner == user || owner == tokenOwner))
        {
            return KeyFileReason.OtherUser(path);
        }
        // A NULL DACL (or none) lets everyone in; the managed descriptor
        // would show it as a rule for Everyone, which is looked at below as
        // well, but it is refused here on its own.
        var raw = new RawSecurityDescriptor(security.GetSecurityDescriptorBinaryForm(), 0);
        if ((raw.ControlFlags & ControlFlags.DiscretionaryAclPresent) == 0 || raw.DiscretionaryAcl is null)
        {
            return KeyFileReason.AccessibleToOthers(path);
        }
        foreach (AccessRule<FileSystemRights> rule in security.GetAccessRules(includeExplicit: true, includeInherited: true, typeof(SecurityIdentifier)))
        {
            if (rule.AccessControlType != AccessControlType.Allow
                || (rule.PropagationFlags & PropagationFlags.InheritOnly) != 0
                || ((int)rule.Rights & ReachesTheKey) == 0)
            {
                continue;
            }
            if (rule.IdentityReference is not SecurityIdentifier sid || !IsTrusted(sid))
            {
                return KeyFileReason.AccessibleToOthers(path);
            }
        }
        return null;
    }

    private bool IsTrusted(SecurityIdentifier sid) =>
        sid == user || sid == LocalSystem || sid == Administrators || sid == OwnerRights;
}
