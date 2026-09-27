// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiKeychainTests/RequestTests.swift
// (KeychainRoundTripTests): a real round trip through the store, here
// Windows Credential Manager instead of the login keychain; the chunked
// values and the longest identifiers are Windows additions. It writes to
// the user's Credential Manager, only under account ids that begin with
// malachi-test-, removes everything it wrote, and runs only on request
// (MALACHI_CREDENTIALS_TEST=1, as MALACHI_KEYCHAIN_TEST=1 on macOS).

using System;
using System.Text;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class CredentialRoundTripTests
{
    private const string TestAccountPrefix = "malachi-test-";

    [Fact]
    public void StoresReadsReplacesAndDeletes()
    {
        SkipUnlessEnabled();
        var store = new CredentialStore(new Win32CredentialManager());
        var request = new Request(TestAccount(), "password");
        try
        {
            Assert.Equal(CredentialFailure.NotFound, store.Get(request, out _));
            Assert.Null(store.Set(request, "first"u8));
            Assert.Equal("first", Get(store, request));
            Assert.Null(store.Set(request, "second"u8));
            Assert.Equal("second", Get(store, request));
            Assert.Null(store.Delete(request));
            Assert.Equal(CredentialFailure.NotFound, store.Get(request, out _));
            // Deleting a missing item is success.
            Assert.Null(store.Delete(request));
        }
        finally
        {
            RemoveAll(request.Account);
        }
    }

    [Fact]
    public void KeepsTheItemAsDescribed()
    {
        SkipUnlessEnabled();
        var manager = new Win32CredentialManager();
        var store = new CredentialStore(manager);
        var request = new Request(TestAccount(), "password");
        try
        {
            Assert.Null(store.Set(request, Encoding.UTF8.GetBytes("pa\"ss wörd\n")));
            Assert.Equal(0, manager.Read(store.TargetName(request), out var item));
            Assert.NotNull(item);
            Assert.Equal($"{Request.Service}/{request.Account}/password", item.TargetName);
            Assert.Equal($"{request.Account}/password", item.UserName);
            Assert.Equal($"Malachi Mail: {request.Account} (password)", item.Comment);
            // UTF-8, not the UTF-16 the Credential Manager dialogs write.
            Assert.Equal(Encoding.UTF8.GetBytes("pa\"ss wörd\n"), item.Blob);
        }
        finally
        {
            RemoveAll(request.Account);
        }
    }

    [Theory]
    [InlineData(0)]
    [InlineData(2560)]
    [InlineData(2561)]
    [InlineData(4096)]
    [InlineData(6144)]
    [InlineData(40960)]
    public void StoresValuesOfEveryLength(int length)
    {
        SkipUnlessEnabled();
        var manager = new Win32CredentialManager();
        var store = new CredentialStore(manager);
        var request = new Request(TestAccount(), "oauth2.refresh_token");
        var value = Value(length);
        try
        {
            Assert.Null(store.Set(request, value));
            Assert.Equal(value, GetBytes(store, request));
            Assert.Equal(0, manager.List(store.TargetName(request) + "#", out var chunks));
            Assert.Equal(ChunkHeader.ChunksFor(length), chunks.Count);

            // A shorter value leaves no chunk behind, and delete leaves nothing.
            Assert.Null(store.Set(request, "short"u8));
            Assert.Equal("short", Get(store, request));
            Assert.Equal(0, manager.List(store.TargetName(request) + "#", out chunks));
            Assert.Empty(chunks);
            Assert.Null(store.Set(request, value));
            Assert.Null(store.Delete(request));
            Assert.Equal(0, manager.List(store.TargetName(request), out var left));
            Assert.Empty(left);
        }
        finally
        {
            RemoveAll(request.Account);
        }
    }

    [Fact]
    public void TakesTheLongestIdentifiers()
    {
        SkipUnlessEnabled();
        var store = new CredentialStore(new Win32CredentialManager());
        var account = TestAccount().PadRight(Request.MaxIdentifier, 'a');
        var request = new Request(account, new string('k', Request.MaxIdentifier));
        Assert.True(Request.IsIdentifier(request.Account) && Request.IsIdentifier(request.Key));
        try
        {
            Assert.Null(store.Set(request, Value(6144)));
            Assert.Equal(Value(6144), GetBytes(store, request));
            Assert.Null(store.Delete(request));
        }
        finally
        {
            RemoveAll(account);
        }
    }

    [Fact]
    public void AnUnknownTargetIsNotFound()
    {
        SkipUnlessEnabled();
        var manager = new Win32CredentialManager();
        var name = $"{Request.Service}/{TestAccount()}/password";
        Assert.Equal(1168, manager.Read(name, out var item));
        Assert.Null(item);
        Assert.Equal(1168, manager.Delete(name));
        Assert.Equal(0, manager.List(name, out var names));
        Assert.Empty(names);
    }

    /// <summary>Skips a test that would touch Credential Manager unless it was asked for.</summary>
    internal static void SkipUnlessEnabled() =>
        Assert.SkipUnless(
            Environment.GetEnvironmentVariable("MALACHI_CREDENTIALS_TEST") == "1",
            "writes to Credential Manager; set MALACHI_CREDENTIALS_TEST=1 to run it");

    /// <summary>An account id no real account has: malachi-test- and a GUID.</summary>
    internal static string TestAccount() => TestAccountPrefix + Guid.NewGuid().ToString("N");

    /// <summary>Removes every item of a test account (main items and chunks of every key).</summary>
    internal static void RemoveAll(string account)
    {
        Assert.StartsWith(TestAccountPrefix, account, StringComparison.Ordinal);
        var manager = new Win32CredentialManager();
        var prefix = $"{Request.Service}/{account}/";
        Assert.Equal(0, manager.List(prefix, out var names));
        foreach (var name in names)
        {
            manager.Delete(name);
        }
        Assert.Equal(0, manager.List(prefix, out names));
        Assert.Empty(names);
    }

    private static byte[] Value(int length)
    {
        var value = new byte[length];
        for (var i = 0; i < length; i++)
        {
            value[i] = (byte)('A' + (i % 58));
        }
        return value;
    }

    private static byte[]? GetBytes(CredentialStore store, Request request)
    {
        Assert.Null(store.Get(request, out var value));
        return value;
    }

    private static string Get(CredentialStore store, Request request) => Encoding.UTF8.GetString(GetBytes(store, request) ?? []);
}
