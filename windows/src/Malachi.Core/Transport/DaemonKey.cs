// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/DaemonKey.swift; Go:
// backend/pkg/api/auth.go (ReadKeyFile).
//
// Darwin's open/fstat/read become a handle from the IKeyFilePolicy and
// File/RandomAccess over it. The owner and mode checks of Swift are the
// policy's (Core has no P/Invoke); a sharing violation, which Windows has
// and Darwin does not, is retried briefly. Async where Swift is
// synchronous, so that the retry waits on the TimeProvider and the
// handshake's token.

using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Microsoft.Win32.SafeHandles;

namespace Malachi.Core.Transport;

/// <summary>
/// Reads malachid's connection key (docs/api.md §1.4): the file beside the
/// socket (<see cref="RpcAuth.KeyPath"/>) that the daemon writes anew at
/// every start. <see cref="RpcClient"/> reads it on every connection, only
/// after the daemon answered <c>system.hello</c> in this client's protocol,
/// and never keeps it.
/// </summary>
/// <remarks>
/// The file must be a regular file (no symbolic link, directory, pipe or
/// device) of exactly 65 bytes in the key format, as api.ReadKeyFile asks,
/// and pass the <see cref="IKeyFilePolicy"/>: on Windows the user's own file
/// that grants nobody else access (the deviation table in
/// windows/README.md). The file is opened once and everything is checked on
/// the open handle. The key is never logged, and no reason quotes the file.
/// </remarks>
public static class DaemonKey
{
    /// <summary>
    /// How long to wait before each new attempt when another process holds
    /// the file without sharing it (an antivirus scan, the daemon replacing
    /// it): four retries within about a third of a second.
    /// </summary>
    public static IReadOnlyList<TimeSpan> SharingRetryDelays { get; } =
    [
        TimeSpan.FromMilliseconds(20),
        TimeSpan.FromMilliseconds(40),
        TimeSpan.FromMilliseconds(80),
        TimeSpan.FromMilliseconds(160),
    ];

    // ERROR_SHARING_VIOLATION and ERROR_LOCK_VIOLATION as HRESULTs.
    private const int SharingViolation = unchecked((int)0x80070020);
    private const int LockViolation = unchecked((int)0x80070021);

    /// <summary>
    /// The key in the file at <paramref name="path"/>, 32 bytes; the caller
    /// zeroes it when done. Throws <see cref="KeyUnavailableException"/> with
    /// the reason; <see cref="OperationCanceledException"/> when
    /// <paramref name="cancellationToken"/> ends a wait between attempts.
    /// </summary>
    public static async Task<byte[]> ReadAsync(
        string path, IKeyFilePolicy policy, TimeProvider timeProvider, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(path);
        ArgumentNullException.ThrowIfNull(policy);
        ArgumentNullException.ThrowIfNull(timeProvider);
        using var handle = await OpenAsync(path, policy, timeProvider, cancellationToken).ConfigureAwait(false);
        return ReadOpen(handle, path, policy);
    }

    /// <summary>Whether <paramref name="e"/> is Windows refusing a file another process holds.</summary>
    public static bool IsSharingViolation(IOException e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return e.HResult is SharingViolation or LockViolation;
    }

    private static async Task<SafeFileHandle> OpenAsync(
        string path, IKeyFilePolicy policy, TimeProvider timeProvider, CancellationToken cancellationToken)
    {
        for (var attempt = 0; ; attempt++)
        {
            try
            {
                return policy.Open(path);
            }
            catch (IOException e) when (IsSharingViolation(e) && attempt < SharingRetryDelays.Count)
            {
                await Task.Delay(SharingRetryDelays[attempt], timeProvider, cancellationToken).ConfigureAwait(false);
            }
            catch (IOException e) when (IsSharingViolation(e))
            {
                throw new KeyUnavailableException(KeyFileReason.CannotOpen(path, "in use by another process"), e);
            }
            catch (FileNotFoundException e)
            {
                throw new KeyUnavailableException(KeyFileReason.DoesNotExist(path), e);
            }
            catch (DirectoryNotFoundException e)
            {
                throw new KeyUnavailableException(KeyFileReason.DoesNotExist(path), e);
            }
            catch (UnauthorizedAccessException e)
            {
                throw new KeyUnavailableException(KeyFileReason.CannotOpen(path, "access denied"), e);
            }
            catch (IOException e)
            {
                throw new KeyUnavailableException(KeyFileReason.CannotOpen(path, Detail(e)), e);
            }
        }
    }

    // Everything on the open handle, so that a path replaced in between
    // cannot slip another file in.
    private static byte[] ReadOpen(SafeFileHandle handle, string path, IKeyFilePolicy policy)
    {
        if (policy.Check(handle, path) is { } refused)
        {
            throw new KeyUnavailableException(refused);
        }
        FileAttributes attributes;
        long length;
        try
        {
            attributes = File.GetAttributes(handle);
            if ((attributes & FileAttributes.ReparsePoint) != 0)
            {
                throw new KeyUnavailableException(KeyFileReason.SymbolicLink(path));
            }
            if ((attributes & (FileAttributes.Directory | FileAttributes.Device)) != 0)
            {
                throw new KeyUnavailableException(KeyFileReason.NotRegular(path));
            }
            length = RandomAccess.GetLength(handle);
        }
        catch (IOException e)
        {
            throw new KeyUnavailableException(KeyFileReason.CannotInspect(path, Detail(e)), e);
        }
        catch (UnauthorizedAccessException e)
        {
            throw new KeyUnavailableException(KeyFileReason.CannotInspect(path, "access denied"), e);
        }
        if (length != RpcAuth.KeyFileSize)
        {
            throw new KeyUnavailableException(KeyFileReason.WrongSize(path));
        }

        // One byte more than a key file: a file that grew since the length
        // was read is refused by ParseKey rather than cut short.
        var buffer = new byte[RpcAuth.KeyFileSize + 1];
        try
        {
            var filled = 0;
            while (filled < buffer.Length)
            {
                int n;
                try
                {
                    n = RandomAccess.Read(handle, buffer.AsSpan(filled), filled);
                }
                catch (IOException e)
                {
                    throw new KeyUnavailableException(KeyFileReason.CannotRead(path, Detail(e)), e);
                }
                if (n == 0)
                {
                    break;
                }
                filled += n;
            }
            return RpcAuth.ParseKey(buffer.AsSpan(0, filled)) ?? throw new KeyUnavailableException(KeyFileReason.NotAKeyFile(path));
        }
        finally
        {
            CryptographicOperations.ZeroMemory(buffer);
        }
    }

    // Fixed text for an I/O failure: its code, never its message, which
    // may quote the path in the system's language.
    private static string Detail(IOException e) => $"error 0x{e.HResult:x8}";
}
