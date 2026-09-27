// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: Credential Manager in memory for the
// tests of CredentialStore. Names match without regard to case, as there;
// a failure can be injected into any call, and every blob handed out is
// kept so a test can check that the store zeroed it.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Credentials.Tests;

/// <summary>An <see cref="ICredentialManager"/> over a dictionary.</summary>
internal sealed class MemoryCredentialManager : ICredentialManager
{
    public const int ErrorNotFound = 1168;
    public const int ErrorNoSuchLogonSession = 1312;
    public const int ErrorInvalidBound = 1734;
    public const int ErrorBadStubData = 1783;

    // What Credential Manager refuses (measured on Windows 11 24H2): a
    // comment of 256 characters or a user name of 513 with
    // RPC_S_INVALID_BOUND, a blob beyond CRED_MAX_CREDENTIAL_BLOB_SIZE with
    // RPC_X_BAD_STUB_DATA. An empty blob is taken.
    private const int MaxComment = 255;
    private const int MaxUserName = 512;

    private readonly Dictionary<string, GenericCredential> items = new(StringComparer.OrdinalIgnoreCase);

    /// <summary>Every call in order: "read NAME", "write NAME", "delete NAME", "list PREFIX".</summary>
    public List<string> Calls { get; } = [];

    /// <summary>Every blob <see cref="Read"/> returned.</summary>
    public List<byte[]> HandedOut { get; } = [];

    /// <summary>
    /// Asked before every call with its kind ("read", "write", "delete",
    /// "list") and name; a non-zero answer is returned instead of doing it.
    /// </summary>
    public Func<string, string, int>? Inject { get; set; }

    /// <summary>The stored names, sorted.</summary>
    public IReadOnlyList<string> Names => [.. items.Keys.Order(StringComparer.Ordinal)];

    /// <summary>The stored item, or null.</summary>
    public GenericCredential? Item(string targetName) => items.GetValueOrDefault(targetName);

    /// <summary>Puts an item in place without going through the store.</summary>
    public void Put(string targetName, byte[] blob, string userName = "", string comment = "") =>
        items[targetName] = new GenericCredential(targetName, userName, comment, [.. blob]);

    /// <summary>Removes an item without going through the store.</summary>
    public void Remove(string targetName) => items.Remove(targetName);

    public int Read(string targetName, out GenericCredential? credential)
    {
        credential = null;
        if (Injected("read", targetName) is var error and not 0)
        {
            return error;
        }
        if (!items.TryGetValue(targetName, out var item))
        {
            return ErrorNotFound;
        }
        var blob = item.Blob.ToArray();
        HandedOut.Add(blob);
        credential = item with { Blob = blob };
        return 0;
    }

    public int Write(string targetName, string userName, string comment, ReadOnlySpan<byte> blob)
    {
        if (Injected("write", targetName) is var error and not 0)
        {
            return error;
        }
        if (comment.Length > MaxComment || userName.Length > MaxUserName)
        {
            return ErrorInvalidBound;
        }
        if (blob.Length > ChunkHeader.ChunkSize)
        {
            return ErrorBadStubData;
        }
        items[targetName] = new GenericCredential(targetName, userName, comment, blob.ToArray());
        return 0;
    }

    public int Delete(string targetName)
    {
        if (Injected("delete", targetName) is var error and not 0)
        {
            return error;
        }
        return items.Remove(targetName) ? 0 : ErrorNotFound;
    }

    public int List(string prefix, out IReadOnlyList<string> targetNames)
    {
        targetNames = [];
        if (Injected("list", prefix) is var error and not 0)
        {
            return error;
        }
        targetNames = [.. items.Keys.Where(name => name.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))];
        return 0;
    }

    private int Injected(string call, string name)
    {
        Calls.Add(call + " " + name);
        return Inject?.Invoke(call, name) ?? 0;
    }
}
