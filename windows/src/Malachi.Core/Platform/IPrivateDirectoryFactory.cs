// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the private directories of the client, the counterpart of
// the 0700 directories of macos/Sources/MalachiCore/Platform/OpenDir.swift
// (createDirectory with 0o700, mkdtemp) and ui/internal/window/
// attachments.go (writeOpenFile: MkdirAll 0o700, MkdirTemp). Implemented by
// Malachi.Platform.Windows.Files.PrivateDirectory (a protected DACL), and
// used for the open directory and the daemon's socket directory
// (docs/windows-port.md §1, §5, §10).

namespace Malachi.Core.Platform;

/// <summary>
/// Creates directories only the current user (and the system) can open.
/// Paths must be fully qualified.
/// </summary>
public interface IPrivateDirectoryFactory
{
    /// <summary>
    /// Makes <paramref name="path"/> a private directory: creates it (its
    /// missing parents as ordinary directories) or makes an existing one
    /// private again. Refuses a link (a reparse point) or anything that is
    /// not a directory with an <see cref="System.IO.IOException"/>.
    /// </summary>
    void Ensure(string path);

    /// <summary>
    /// Creates a new private directory at <paramref name="path"/>, whose
    /// parent exists; fails with an <see cref="System.IO.IOException"/> when
    /// anything is there already, so nothing that was there before is ever
    /// reused (the <c>mkdtemp</c> of the open directory).
    /// </summary>
    void CreateNew(string path);
}
