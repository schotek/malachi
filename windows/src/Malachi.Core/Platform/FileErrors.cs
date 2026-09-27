// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the errors of files written out of a message, without their
// path. .NET's messages name the path, and that path carries the
// attachment's name, which is mail content and never logged
// (docs/windows-port.md §3.1); macos/Sources/MalachiMail/Attachments/
// AttachmentActions.swift logs such errors only as private.

using System;
using System.IO;

namespace Malachi.Core.Platform;

/// <summary>File-system exceptions with the path taken out of them.</summary>
public static class FileErrors
{
    /// <summary>
    /// An exception of the kind of <paramref name="error"/> (a file or
    /// directory not found, a path too long, access denied, any other I/O
    /// error) with its HResult and <paramref name="message"/> in place of
    /// one naming the path; the original is not kept as the inner
    /// exception, whose message would name it still. Any other exception is
    /// returned as it is.
    /// </summary>
    public static Exception WithoutPath(Exception error, string message)
    {
        ArgumentNullException.ThrowIfNull(error);
        Exception bare = error switch
        {
            FileNotFoundException => new FileNotFoundException(message),
            DirectoryNotFoundException => new DirectoryNotFoundException(message),
            PathTooLongException => new PathTooLongException(message),
            IOException => new IOException(message),
            UnauthorizedAccessException => new UnauthorizedAccessException(message),
            _ => error,
        };
        if (!ReferenceEquals(bare, error))
        {
            bare.HResult = error.HResult;
        }
        return bare;
    }
}
