// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the private directories of the client (docs/windows-port.md
// §1, §5, §10), the counterpart of the 0700 directories of
// macos/Sources/MalachiCore/Platform/OpenDir.swift (createDirectory with
// 0o700, mkdtemp) and ui/internal/window/attachments.go (writeOpenFile:
// MkdirAll 0o700, MkdirTemp): a protected DACL that grants the current user
// and SYSTEM, and nobody else, what 0700 grants the owner. Go's MkdirAll
// ignores the mode on Windows, and a directory created there inherits the
// Administrators entry of the profile.

using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using Malachi.Core.Platform;
using Microsoft.Win32.SafeHandles;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Security;
using Windows.Win32.Storage.FileSystem;

namespace Malachi.Platform.Windows.Files;

/// <summary>
/// Creates directories that only the current user and SYSTEM can open: the
/// owner is the user, the DACL is protected (nothing inherited from the
/// parent) and holds two entries, full control for the user and for SYSTEM,
/// both inherited by everything created inside. Links are never followed.
/// </summary>
public sealed class PrivateDirectory : IPrivateDirectoryFactory
{
    private const InheritanceFlags Inherited = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;

    private static readonly SecurityIdentifier LocalSystem = new(WellKnownSidType.LocalSystemSid, null);

    private readonly SecurityIdentifier user;

    /// <summary>Private to the user this process runs as.</summary>
    public PrivateDirectory()
    {
        using var identity = WindowsIdentity.GetCurrent();
        user = identity.User ?? throw new InvalidOperationException("the process has no user");
    }

    /// <inheritdoc/>
    public void Ensure(string path)
    {
        var full = FullPath(path);
        var parent = Path.GetDirectoryName(full)
            ?? throw new ArgumentException("a root cannot be made private", nameof(path));
        Directory.CreateDirectory(parent);
        if (Create(full, path))
        {
            Verify(full, path);
            return;
        }
        // It was there: private already, or made so now.
        using (var handle = Open(full, FileSystemRights.ReadPermissions, path))
        {
            if (IsPrivate(new HandleSecurity(handle)))
            {
                return;
            }
        }
        Protect(full, path);
        Verify(full, path);
    }

    /// <inheritdoc/>
    public void CreateNew(string path)
    {
        var full = FullPath(path);
        if (!Create(full, path))
        {
            throw new IOException(path + " already exists", HResult(WIN32_ERROR.ERROR_ALREADY_EXISTS));
        }
        try
        {
            Verify(full, path);
        }
        catch
        {
            try
            {
                Directory.Delete(full);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                // The verification's error is the one to report.
            }
            throw;
        }
    }

    /// <summary>
    /// Whether <paramref name="path"/> is a private directory: a directory
    /// of its own (not a link), owned by the user, with the protected DACL
    /// described above.
    /// </summary>
    public bool IsPrivate(string path)
    {
        var full = FullPath(path);
        try
        {
            using var handle = Open(full, FileSystemRights.ReadPermissions, path);
            return IsPrivate(new HandleSecurity(handle));
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    // The descriptor of a private directory.
    private HandleSecurity Descriptor()
    {
        var security = new HandleSecurity();
        security.SetOwner(user);
        security.SetAccessRuleProtection(isProtected: true, preserveInheritance: false);
        security.AddAccessRule(new AccessRule<FileSystemRights>(
            user, FileSystemRights.FullControl, Inherited, PropagationFlags.None, AccessControlType.Allow));
        security.AddAccessRule(new AccessRule<FileSystemRights>(
            LocalSystem, FileSystemRights.FullControl, Inherited, PropagationFlags.None, AccessControlType.Allow));
        return security;
    }

    private bool IsPrivate(HandleSecurity security)
    {
        if (!user.Equals(security.GetOwner(typeof(SecurityIdentifier))) || !security.AreAccessRulesProtected)
        {
            return false;
        }
        bool userFull = false, systemFull = false;
        foreach (AuthorizationRule entry in security.GetAccessRules(true, true, typeof(SecurityIdentifier)))
        {
            if (entry is not AccessRule<FileSystemRights> rule
                || rule.IsInherited
                || rule.AccessControlType != AccessControlType.Allow
                || rule.InheritanceFlags != Inherited
                || rule.PropagationFlags != PropagationFlags.None
                || (rule.Rights & FileSystemRights.FullControl) != FileSystemRights.FullControl)
            {
                return false;
            }
            if (user.Equals(rule.IdentityReference))
            {
                userFull = true;
            }
            else if (LocalSystem.Equals(rule.IdentityReference))
            {
                systemFull = true;
            }
            else
            {
                return false;
            }
        }
        return userFull && systemFull;
    }

    // CreateDirectory with the private descriptor: true when it made the
    // directory, false when something was there already.
    private unsafe bool Create(string full, string shown)
    {
        var descriptor = Descriptor().GetSecurityDescriptorBinaryForm();
        fixed (byte* pointer = descriptor)
        {
            var attributes = new SECURITY_ATTRIBUTES
            {
                nLength = (uint)sizeof(SECURITY_ATTRIBUTES),
                lpSecurityDescriptor = pointer,
                bInheritHandle = false,
            };
            if (PInvoke.CreateDirectory(Extended(full), attributes))
            {
                return true;
            }
        }
        var error = (WIN32_ERROR)Marshal.GetLastPInvokeError();
        if (error is WIN32_ERROR.ERROR_ALREADY_EXISTS or WIN32_ERROR.ERROR_FILE_EXISTS)
        {
            return false;
        }
        throw Failure(error, shown);
    }

    // The DACL first, which the owner can always rewrite; then the owner,
    // which that DACL now lets the user take.
    private void Protect(string full, string shown)
    {
        bool owned;
        using (var handle = Open(full, FileSystemRights.ReadPermissions | FileSystemRights.ChangePermissions, shown))
        {
            owned = user.Equals(new HandleSecurity(handle).GetOwner(typeof(SecurityIdentifier)));
            Descriptor().Write(handle, AccessControlSections.Access);
        }
        if (owned)
        {
            return;
        }
        using (var handle = Open(full, FileSystemRights.ReadPermissions | FileSystemRights.TakeOwnership, shown))
        {
            Descriptor().Write(handle, AccessControlSections.Owner);
        }
    }

    private void Verify(string full, string shown)
    {
        using var handle = Open(full, FileSystemRights.ReadPermissions, shown);
        if (!IsPrivate(new HandleSecurity(handle)))
        {
            throw new UnauthorizedAccessException(shown + " could not be made private");
        }
    }

    // The directory itself, never what a link at its path points to; a
    // link or anything but a directory is refused.
    private static SafeFileHandle Open(string full, FileSystemRights access, string shown)
    {
        var handle = PInvoke.CreateFile(
            Extended(full),
            (uint)(access | FileSystemRights.ReadAttributes),
            FILE_SHARE_MODE.FILE_SHARE_READ | FILE_SHARE_MODE.FILE_SHARE_WRITE | FILE_SHARE_MODE.FILE_SHARE_DELETE,
            null,
            FILE_CREATION_DISPOSITION.OPEN_EXISTING,
            FILE_FLAGS_AND_ATTRIBUTES.FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAGS_AND_ATTRIBUTES.FILE_FLAG_OPEN_REPARSE_POINT,
            null);
        if (handle.IsInvalid)
        {
            var error = (WIN32_ERROR)Marshal.GetLastPInvokeError();
            handle.Dispose();
            throw Failure(error, shown);
        }
        FileAttributes attributes;
        try
        {
            attributes = File.GetAttributes(handle);
        }
        catch
        {
            handle.Dispose();
            throw;
        }
        if ((attributes & FileAttributes.ReparsePoint) != 0)
        {
            handle.Dispose();
            throw new IOException(shown + " is a link, not a directory of its own");
        }
        if ((attributes & FileAttributes.Directory) == 0)
        {
            handle.Dispose();
            throw new IOException(shown + " is not a directory");
        }
        return handle;
    }

    private static string FullPath(string path)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        if (!Path.IsPathFullyQualified(path) || path.StartsWith(@"\\.\", StringComparison.Ordinal))
        {
            throw new ArgumentException(path + " is not a fully qualified path", nameof(path));
        }
        return Path.TrimEndingDirectorySeparator(Path.GetFullPath(path));
    }

    // The form of a full path the file APIs take at any length.
    private static string Extended(string full)
    {
        if (full.StartsWith(@"\\?\", StringComparison.Ordinal))
        {
            return full;
        }
        return full.StartsWith(@"\\", StringComparison.Ordinal) ? @"\\?\UNC\" + full[2..] : @"\\?\" + full;
    }

    private static int HResult(WIN32_ERROR error) => unchecked((int)0x80070000) | (int)error;

    private static Exception Failure(WIN32_ERROR error, string shown)
    {
        var message = shown + ": " + new Win32Exception((int)error).Message;
        return error switch
        {
            WIN32_ERROR.ERROR_FILE_NOT_FOUND or WIN32_ERROR.ERROR_PATH_NOT_FOUND => new DirectoryNotFoundException(message),
            WIN32_ERROR.ERROR_ACCESS_DENIED => new UnauthorizedAccessException(message),
            _ => new IOException(message, HResult(error)),
        };
    }
}
