// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiKeychain/Keychain.swift (KeychainStore);
// GTK: none (on Linux the daemon keeps secrets in the Secret Service
// itself). The store half of the helper, over Windows Credential Manager
// instead of the login keychain, named after its store as malachi-keychain
// is. One generic credential per account id and key: target
// <service>/<account>/<key>, user name <account>/<key>, comment
// "Malachi Mail: <account> (<key>)", the value's UTF-8 as the blob.
//
// Every value get hands over matches a SHA-256 the helper wrote with it
// (docs/windows-port.md §10). An item that holds the value itself carries
// the SHA-256 of its blob in an attribute; anything else that writes the
// item (cmdkey, the Credential Manager dialogs, which store UTF-16) leaves
// no attribute or one that does not match, and the item is corrupt rather
// than a wrong password.
//
// A credential holds at most 2560 bytes. A longer value goes into chunks
// <target>#<number>, written first; the main item is written last and
// carries a ChunkHeader (the slot, n, the length, the SHA-256) instead of
// the value. The chunks alternate between two slots, #1..#16 and #17..#32:
// a set writes the slot the current header does not name, so until its
// header replaces the old one a reader finds the previous value whole, and
// a set that fails or is killed half-way keeps it, as SecItemUpdate does.
// A missing or damaged chunk, or any mismatch, is a corrupt item, never a
// wrong value. Every other chunk (the other slot, a longer value's tail)
// is removed after the header is written; delete removes them all.
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

    // No slot: the current item is missing, holds the value itself, or
    // carries a header this store cannot read.
    private const int NoSlot = -1;

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
            var problem = ValueProblem(blob, item.Digest);
            if (problem is not null)
            {
                CryptographicOperations.ZeroMemory(blob);
                return CredentialFailure.Corrupt(problem);
            }
            value = blob;
            return null;
        }
        if (!ChunkHeader.TryDecode(blob, out var header, out var headerProblem))
        {
            return CredentialFailure.Corrupt(headerProblem);
        }
        return ReadChunks(request, header, out value);
    }

    /// <summary>
    /// Stores the value, replacing an existing item; a value longer than one
    /// credential holds is chunked. A value longer than
    /// <see cref="ChunkHeader.MaxLength"/>, or one whose get answer would be
    /// longer than <see cref="Request.MaxAnswer"/>, is refused before
    /// anything is written.
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
        if (Request.ValueLineLength(value) > Request.MaxAnswer)
        {
            return CredentialFailure.AnswerTooLarge;
        }
        var count = ChunkHeader.ChunksFor(value.Length);
        if (count == 0)
        {
            Span<byte> digest = stackalloc byte[SHA256.HashSizeInBytes];
            SHA256.HashData(value, digest);
            var written = manager.Write(TargetName(request), request.ItemAccount, Comment(request.Label), value, digest);
            CryptographicOperations.ZeroMemory(digest);
            if (written != Success)
            {
                return CredentialFailure.ApiFailed("CredWriteW", written);
            }
            return RemoveChunks(request, keep: null);
        }
        var failure = CurrentSlot(request, out var current);
        if (failure is not null)
        {
            return failure;
        }
        var header = ChunkHeader.For(value, current == 0 ? 1 : 0);
        for (var index = 1; index <= count; index++)
        {
            var start = (index - 1) * ChunkHeader.ChunkSize;
            var chunk = value.Slice(start, Math.Min(ChunkHeader.ChunkSize, value.Length - start));
            var number = header.ChunkNumber(index);
            var error = manager.Write(
                ChunkTargetName(request, number),
                Numbered(request.ItemAccount, number),
                Comment(string.Create(CultureInfo.InvariantCulture, $"{request.Label}, part {index} of {count}")),
                chunk,
                []);
            if (error != Success)
            {
                return CredentialFailure.ApiFailed("CredWriteW", error);
            }
        }
        var wrote = manager.Write(TargetName(request), request.ItemAccount, Comment(request.Label), header.Encode(), []);
        if (wrote != Success)
        {
            return CredentialFailure.ApiFailed("CredWriteW", wrote);
        }
        return RemoveChunks(request, header);
    }

    /// <summary>Removes the item and its chunks; a missing one is success.</summary>
    public CredentialFailure? Delete(Request request)
    {
        var error = manager.Delete(TargetName(request));
        if (error != Success && error != NotFound)
        {
            return CredentialFailure.ApiFailed("CredDeleteW", error);
        }
        return RemoveChunks(request, keep: null);
    }

    /// <summary>The main item's name: <c>&lt;service&gt;/&lt;account&gt;/&lt;key&gt;</c>.</summary>
    internal string TargetName(Request request) => $"{Service}/{request.ItemAccount}";

    /// <summary>The name of the chunk numbered <paramref name="number"/>: <c>&lt;target&gt;#&lt;number&gt;</c>.</summary>
    internal string ChunkTargetName(Request request, int number) => Numbered(TargetName(request), number);

    private static string Numbered(string name, int number) =>
        string.Create(CultureInfo.InvariantCulture, $"{name}#{number}");

    private static string Comment(string label) => label.Length <= MaxComment ? label : label[..MaxComment];

    // Why a blob that is not a header is not a value this store wrote, or
    // null: it must carry the SHA-256 of its bytes, and be UTF-8.
    private static string? ValueProblem(ReadOnlySpan<byte> blob, byte[]? digest)
    {
        if (digest is null)
        {
            return "the item was not written by malachi-credentials";
        }
        Span<byte> hash = stackalloc byte[SHA256.HashSizeInBytes];
        SHA256.HashData(blob, hash);
        var matches = CryptographicOperations.FixedTimeEquals(hash, digest);
        CryptographicOperations.ZeroMemory(hash);
        CryptographicOperations.ZeroMemory(digest);
        if (!matches)
        {
            return "the value does not match the item's SHA-256";
        }
        return Utf8.IsValid(blob) ? null : "the value is not UTF-8";
    }

    // The slot the current item's header names, or NoSlot; a failure only
    // when the item cannot be read at all. A value stored as it is gets
    // zeroed: it is the previous secret.
    private CredentialFailure? CurrentSlot(Request request, out int slot)
    {
        slot = NoSlot;
        var error = manager.Read(TargetName(request), out var item);
        if (error == NotFound)
        {
            return null;
        }
        if (error != Success || item is null)
        {
            return CredentialFailure.ApiFailed("CredReadW", error);
        }
        if (ChunkHeader.TryDecode(item.Blob, out var header, out _))
        {
            slot = header.Slot;
        }
        CryptographicOperations.ZeroMemory(item.Blob);
        if (item.Digest is not null)
        {
            CryptographicOperations.ZeroMemory(item.Digest);
        }
        return null;
    }

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
                var error = manager.Read(ChunkTargetName(request, header.ChunkNumber(index)), out var chunk);
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

    // Removes every chunk but the header's (all of them without one),
    // highest number first. Only names of the form <target>#<number> are
    // touched.
    private CredentialFailure? RemoveChunks(Request request, ChunkHeader? keep)
    {
        var prefix = TargetName(request) + "#";
        var error = manager.List(prefix, out var names);
        if (error != Success)
        {
            return CredentialFailure.ApiFailed("CredEnumerateW", error);
        }
        var stale = new List<(int Number, string Name)>();
        foreach (var name in names)
        {
            var number = ChunkNumber(name, prefix);
            if (number > 0 && keep?.HasChunk(number) != true)
            {
                stale.Add((number, name));
            }
        }
        stale.Sort((a, b) => b.Number.CompareTo(a.Number));
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
    private static int ChunkNumber(string name, string prefix)
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
