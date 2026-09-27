// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Keychain.swift (KeychainStore): the
// store half of the helper, over Windows Credential Manager instead of the
// login keychain, named after its store as malachi-keychain is. One generic
// credential per account id and key: target <service>/<account>/<key>, user
// name <account>/<key>, comment "Malachi Mail: <account> (<key>)", the
// value's UTF-8 as the blob.
//
// A credential holds at most 2560 bytes (docs/windows-port.md §10). A longer
// value goes into chunks <target>#1..#n, written first; the main item is
// written last and carries a ChunkHeader (n, the length, the SHA-256)
// instead of the value. Until the main item is written a reader finds the
// previous value, or chunks that do not match the previous header; a
// missing or damaged chunk, or any mismatch, is a corrupt item, never a
// wrong value. Chunks beyond the current n (a longer value before, or a set
// that failed before its main item) are removed by the next set; delete
// removes them all.
//
// Credential Manager matches target names without regard to case, so two
// identifiers that differ only in case would share an item; the daemon's
// ids (acc_ and lower-case hex) and keys are lower case.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Security.Cryptography;
using System.Text.Unicode;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.Credentials;

/// <summary>Reads, writes and removes the items. Never prints anything itself.</summary>
/// <param name="manager">The credentials: Credential Manager, or items in memory in the tests.</param>
/// <param name="service">The first part of every target name.</param>
internal sealed class CredentialStore(ICredentialManager manager, string service = Request.Service)
{
    /// <summary>
    /// The longest comment Credential Manager takes; a longer label is cut.
    /// CRED_MAX_STRING_LENGTH counts the terminating NUL: 256 characters
    /// fail with RPC_S_INVALID_BOUND (measured). The longest user name, 260
    /// characters, is well inside its limit of 512.
    /// </summary>
    public const int MaxComment = (int)PInvoke.CRED_MAX_STRING_LENGTH - 1;

    private const int Success = (int)WIN32_ERROR.ERROR_SUCCESS;
    private const int NotFound = (int)WIN32_ERROR.ERROR_NOT_FOUND;

    /// <summary>The first part of every target name.</summary>
    public string Service { get; } = service;

    /// <summary>
    /// The stored value, as UTF-8 the caller zeroes after use; or
    /// <see cref="CredentialFailure.NotFound"/>, or another failure.
    /// </summary>
    public CredentialFailure? Get(Request request, out byte[]? value)
    {
        value = null;
        var error = manager.Read(TargetName(request), out var item);
        if (error != Success || item is null)
        {
            return error == NotFound ? CredentialFailure.NotFound : CredentialFailure.ApiFailed("CredReadW", error);
        }
        var blob = item.Blob;
        if (!ChunkHeader.IsHeader(blob))
        {
            if (!Utf8.IsValid(blob))
            {
                CryptographicOperations.ZeroMemory(blob);
                return CredentialFailure.Corrupt("the value is not UTF-8");
            }
            value = blob;
            return null;
        }
        if (!ChunkHeader.TryDecode(blob, out var header, out var problem))
        {
            return CredentialFailure.Corrupt(problem);
        }
        return ReadChunks(request, header, out value);
    }

    /// <summary>
    /// Stores the value, replacing an existing item; a value longer than one
    /// credential holds is chunked, one longer than
    /// <see cref="ChunkHeader.MaxLength"/> is refused.
    /// </summary>
    /// <param name="request">Whose value.</param>
    /// <param name="value">The value; it must be valid UTF-8 (so no value begins with <see cref="ChunkHeader.Marker"/>).</param>
    public CredentialFailure? Set(Request request, ReadOnlySpan<byte> value)
    {
        if (!Utf8.IsValid(value))
        {
            throw new ArgumentException("the value is not UTF-8", nameof(value));
        }
        if (value.Length > ChunkHeader.MaxLength)
        {
            return CredentialFailure.TooLarge;
        }
        var count = ChunkHeader.ChunksFor(value.Length);
        int error;
        if (count == 0)
        {
            error = manager.Write(TargetName(request), request.ItemAccount, Comment(request.Label), value);
        }
        else
        {
            for (var index = 1; index <= count; index++)
            {
                var start = (index - 1) * ChunkHeader.ChunkSize;
                var chunk = value.Slice(start, Math.Min(ChunkHeader.ChunkSize, value.Length - start));
                error = manager.Write(
                    ChunkTargetName(request, index),
                    Numbered(request.ItemAccount, index),
                    Comment(string.Create(CultureInfo.InvariantCulture, $"{request.Label}, part {index} of {count}")),
                    chunk);
                if (error != Success)
                {
                    return CredentialFailure.ApiFailed("CredWriteW", error);
                }
            }
            error = manager.Write(TargetName(request), request.ItemAccount, Comment(request.Label), ChunkHeader.For(value).Encode());
        }
        if (error != Success)
        {
            return CredentialFailure.ApiFailed("CredWriteW", error);
        }
        return RemoveChunks(request, after: count);
    }

    /// <summary>Removes the item and its chunks; a missing one is success.</summary>
    public CredentialFailure? Delete(Request request)
    {
        var error = manager.Delete(TargetName(request));
        if (error != Success && error != NotFound)
        {
            return CredentialFailure.ApiFailed("CredDeleteW", error);
        }
        return RemoveChunks(request, after: 0);
    }

    /// <summary>The main item's name: <c>&lt;service&gt;/&lt;account&gt;/&lt;key&gt;</c>.</summary>
    internal string TargetName(Request request) => $"{Service}/{request.ItemAccount}";

    /// <summary>The name of chunk <paramref name="index"/> (1-based) of a chunked value.</summary>
    internal string ChunkTargetName(Request request, int index) => Numbered(TargetName(request), index);

    private static string Numbered(string name, int index) =>
        string.Create(CultureInfo.InvariantCulture, $"{name}#{index}");

    private static string Comment(string label) => label.Length <= MaxComment ? label : label[..MaxComment];

    // Reads chunks 1..n into one array, checks it against the header and
    // hands it over; every other copy is zeroed.
    private CredentialFailure? ReadChunks(Request request, ChunkHeader header, out byte[]? value)
    {
        value = null;
        byte[]? assembled = new byte[header.Length];
        try
        {
            for (var index = 1; index <= header.Count; index++)
            {
                var error = manager.Read(ChunkTargetName(request, index), out var chunk);
                if (error != Success || chunk is null)
                {
                    return error == NotFound
                        ? CredentialFailure.Corrupt(string.Create(CultureInfo.InvariantCulture, $"chunk {index} of {header.Count} is missing"))
                        : CredentialFailure.ApiFailed("CredReadW", error);
                }
                try
                {
                    if (chunk.Blob.Length != header.ChunkLength(index))
                    {
                        return CredentialFailure.Corrupt(string.Create(CultureInfo.InvariantCulture, $"chunk {index} of {header.Count} has the wrong size"));
                    }
                    chunk.Blob.CopyTo(assembled, (index - 1) * ChunkHeader.ChunkSize);
                }
                finally
                {
                    CryptographicOperations.ZeroMemory(chunk.Blob);
                }
            }
            if (!header.Matches(assembled))
            {
                return CredentialFailure.Corrupt("the chunks do not match the header's SHA-256");
            }
            if (!Utf8.IsValid(assembled))
            {
                return CredentialFailure.Corrupt("the value is not UTF-8");
            }
            value = assembled;
            assembled = null;
            return null;
        }
        finally
        {
            if (assembled is not null)
            {
                CryptographicOperations.ZeroMemory(assembled);
            }
        }
    }

    // Removes the chunks numbered above `after`, highest first. Only names
    // of the form <target>#<number> are touched.
    private CredentialFailure? RemoveChunks(Request request, int after)
    {
        var prefix = TargetName(request) + "#";
        var error = manager.List(prefix, out var names);
        if (error != Success)
        {
            return CredentialFailure.ApiFailed("CredEnumerateW", error);
        }
        var stale = new List<(int Index, string Name)>();
        foreach (var name in names)
        {
            var index = ChunkIndex(name, prefix);
            if (index > after)
            {
                stale.Add((index, name));
            }
        }
        stale.Sort((a, b) => b.Index.CompareTo(a.Index));
        foreach (var (_, name) in stale)
        {
            error = manager.Delete(name);
            if (error != Success && error != NotFound)
            {
                return CredentialFailure.ApiFailed("CredDeleteW", error);
            }
        }
        return null;
    }

    // The chunk number of a name that is <prefix><number> (no sign, no
    // leading zero), or 0.
    private static int ChunkIndex(string name, string prefix)
    {
        if (!name.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
        {
            return 0;
        }
        var digits = name.AsSpan(prefix.Length);
        if (digits.IsEmpty || digits.Length > 9 || digits[0] == '0' || digits.ContainsAnyExceptInRange('0', '9'))
        {
            return 0;
        }
        return int.Parse(digits, NumberStyles.None, CultureInfo.InvariantCulture);
    }
}
