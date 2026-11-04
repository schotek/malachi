// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the owner and mode checks of
// macos/Sources/MalachiCore/Transport/DaemonKey.swift (st_uid, st_mode),
// which Core cannot make without P/Invoke: the platform supplies them.

using Microsoft.Win32.SafeHandles;

namespace Malachi.Core.Transport;

/// <summary>
/// How <see cref="DaemonKey"/> opens the key file and what it asks of it
/// beyond Go's checks (a regular file, not a link, 65 bytes, the key
/// format), which <see cref="DaemonKey"/> makes itself.
/// <see cref="PortableKeyFilePolicy"/> adds nothing (the Go clients' rule);
/// the Windows client's policy, in Malachi.Platform.Windows, opens without
/// following a reparse point and asks for the user's own file that nobody
/// else may read (the counterpart of macOS's owner and mode check, a listed
/// deviation).
/// </summary>
public interface IKeyFilePolicy
{
    /// <summary>
    /// Opens the key file for reading, sharing read, write and delete: a
    /// reader without delete sharing makes the daemon's shutdown fail to
    /// remove the file (measured). Throws
    /// <see cref="System.IO.FileNotFoundException"/> or
    /// <see cref="System.IO.DirectoryNotFoundException"/> for a missing file,
    /// <see cref="System.UnauthorizedAccessException"/> for a refused one, an
    /// <see cref="System.IO.IOException"/> whose <c>HResult</c> is the
    /// sharing violation's for a file another process holds exclusively (the
    /// caller retries), and <see cref="KeyUnavailableException"/> for a file
    /// it refuses on sight.
    /// </summary>
    SafeFileHandle Open(string path);

    /// <summary>
    /// Why the open file must not be used (a <see cref="KeyFileReason"/>
    /// text), or null. Called after <see cref="DaemonKey"/> saw a regular
    /// file and before it looks at the size or reads a byte (Swift's order:
    /// what the file is, whose it is and who may read it, its content). An
    /// exception is a <see cref="KeyUnavailableException"/> "cannot inspect"
    /// all the same.
    /// </summary>
    string? Check(SafeFileHandle handle, string path);
}
