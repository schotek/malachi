// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the reasons of macos/Sources/MalachiCore/Transport/DaemonKey.swift
// (DaemonKey.read); Go: backend/pkg/api/auth.go (ReadKeyFile).

namespace Malachi.Core.Transport;

/// <summary>
/// Why a key file cannot be used: fixed text around the path, never
/// anything of the file's content. The texts are macOS's, which the tests
/// match; every <see cref="IKeyFilePolicy"/> uses them.
/// </summary>
public static class KeyFileReason
{
    /// <summary>No file at the path.</summary>
    public static string DoesNotExist(string path) => $"{path} does not exist";

    /// <summary>A symbolic link, a junction or another reparse point.</summary>
    public static string SymbolicLink(string path) => $"{path} is a symbolic link";

    /// <summary>The file could not be opened; <paramref name="detail"/> is fixed text.</summary>
    public static string CannotOpen(string path, string detail) => $"cannot open {path}: {detail}";

    /// <summary>The open file could not be inspected; <paramref name="detail"/> is fixed text.</summary>
    public static string CannotInspect(string path, string detail) => $"cannot inspect {path}: {detail}";

    /// <summary>The open file could not be read; <paramref name="detail"/> is fixed text.</summary>
    public static string CannotRead(string path, string detail) => $"cannot read {path}: {detail}";

    /// <summary>A directory, a device, a pipe, a socket.</summary>
    public static string NotRegular(string path) => $"{path} is not a regular file";

    /// <summary>The file's owner is not this user.</summary>
    public static string OtherUser(string path) => $"{path} belongs to another user";

    /// <summary>Someone besides this user may read or change the file.</summary>
    public static string AccessibleToOthers(string path) => $"{path} is accessible to other users";

    /// <summary>Not <see cref="Api.RpcAuth.KeyFileSize"/> bytes.</summary>
    public static string WrongSize(string path) => $"{path} is not {Api.RpcAuth.KeyFileSize} bytes";

    /// <summary>The right size, not the key format.</summary>
    public static string NotAKeyFile(string path) => $"{path} is not a key file";
}
