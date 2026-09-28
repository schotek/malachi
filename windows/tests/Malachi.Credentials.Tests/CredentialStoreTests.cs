// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart (KeychainStore is tested only against
// the real Keychain, CredentialRoundTripTests here): CredentialStore against
// Credential Manager in memory, above all the chunking of values longer
// than one credential holds, the two chunk slots, torn writes, stale
// chunks, and items some other program wrote.

using System;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Text.Unicode;
using Windows.Win32;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class CredentialStoreTests
{
    private const string Main = "io.github.schotek.Malachi/acc_1/password";

    private const string NotWritten = "the item was not written by malachi-credentials";

    private static readonly Request Password = new("acc_1", "password");

    private readonly MemoryCredentialManager manager = new();

    private readonly CredentialStore store;

    public CredentialStoreTests()
    {
        store = new CredentialStore(manager);
    }

    [Fact]
    public void NamesItemsAfterTheServiceAccountAndKey()
    {
        Assert.Equal(Main, store.TargetName(Password));
        Assert.Equal(Main + "#2", store.ChunkTargetName(Password, 2));
        Assert.Equal("elsewhere/acc_1/password", new CredentialStore(manager, "elsewhere").TargetName(Password));

        Assert.Null(store.Set(Password, "hunter2"u8));
        var item = manager.Item(Main);
        Assert.NotNull(item);
        Assert.Equal("acc_1/password", item.UserName);
        Assert.Equal("Malachi Mail: acc_1 (password)", item.Comment);
        Assert.Equal("hunter2"u8.ToArray(), item.Blob);
        Assert.Equal(SHA256.HashData("hunter2"u8), item.Digest);
        AssertNames(Main);
    }

    [Theory]
    [InlineData(0)]
    [InlineData(1)]
    [InlineData(2559)]
    [InlineData(2560)]
    public void StoresAValueOneItemHoldsAsItIs(int length)
    {
        var value = Value(length);
        Assert.Null(store.Set(Password, value));
        AssertNames(Main);
        Assert.Equal(value, manager.Item(Main)?.Blob);
        Assert.Equal(SHA256.HashData(value), manager.Item(Main)?.Digest);
        Assert.Equal(value, Get(Password));
    }

    [Fact]
    public void KeepsEveryCharacter()
    {
        // As the Keychain does: whatever the daemon hands over, U+0000 too.
        foreach (var text in new[] { "", "a\0b", "pa\"ss wörd\n", "пароль", "\t\r\n", "😀" })
        {
            var value = Encoding.UTF8.GetBytes(text);
            Assert.Null(store.Set(Password, value));
            Assert.Equal(SHA256.HashData(value), manager.Item(Main)?.Digest);
            Assert.Equal(value, Get(Password));
        }
    }

    [Theory]
    [InlineData(2561, new[] { 2560, 1 })]
    [InlineData(4096, new[] { 2560, 1536 })]
    [InlineData(5120, new[] { 2560, 2560 })]
    [InlineData(6144, new[] { 2560, 2560, 1024 })]
    public void ChunksALongerValue(int length, int[] chunks)
    {
        var value = Value(length);
        Assert.Null(store.Set(Password, value));

        AssertNames([Main, .. chunks.Select((_, i) => Main + "#" + (i + 1))]);
        for (var i = 0; i < chunks.Length; i++)
        {
            var chunk = manager.Item(Main + "#" + (i + 1));
            Assert.NotNull(chunk);
            Assert.Equal(chunks[i], chunk.Blob.Length);
            Assert.Equal("acc_1/password#" + (i + 1), chunk.UserName);
            Assert.Equal($"Malachi Mail: acc_1 (password), part {i + 1} of {chunks.Length}", chunk.Comment);
            Assert.Null(chunk.Digest);
        }
        var main = manager.Item(Main);
        Assert.NotNull(main);
        Assert.Equal("acc_1/password", main.UserName);
        Assert.Equal("Malachi Mail: acc_1 (password)", main.Comment);
        Assert.Equal(ChunkHeader.Size, main.Blob.Length);
        Assert.True(ChunkHeader.IsHeader(main.Blob));
        // The header carries the SHA-256 itself.
        Assert.Null(main.Digest);
        Assert.Equal(value, Get(Password));
    }

    [Fact]
    public void TakesTheLongestValueAndRefusesALongerOne()
    {
        var longest = Value(ChunkHeader.MaxLength);
        Assert.Null(store.Set(Password, longest));
        Assert.Equal(1 + ChunkHeader.MaxChunks, manager.Names.Count);
        Assert.Equal(longest, Get(Password));

        manager.Calls.Clear();
        Assert.Equal(CredentialFailure.TooLarge, store.Set(Password, Value(ChunkHeader.MaxLength + 1)));
        Assert.DoesNotContain(manager.Calls, call => call.StartsWith("write", StringComparison.Ordinal));
        Assert.Equal(longest, Get(Password));
    }

    [Fact]
    public void RefusesAValueWhoseAnswerTheDaemonWouldCut()
    {
        // A backslash is two bytes of the answer: 13 + 2 × 32761 + 1 is
        // exactly what the daemon keeps.
        var fits = Encoding.ASCII.GetBytes(new string('\\', 32761) + "x");
        Assert.Equal(Request.MaxAnswer, Request.ValueLineLength(fits));
        Assert.Null(store.Set(Password, fits));
        Assert.Equal(fits, Get(Password));
        Assert.Equal(Request.MaxAnswer, Request.ValueLine(fits).Length);

        manager.Calls.Clear();
        byte[][] cut =
        [
            Encoding.ASCII.GetBytes(new string('\\', 32761) + "xy"),
            // 32 KiB is short enough for an item, not for its answer.
            Encoding.ASCII.GetBytes(new string('\\', 32 * 1024)),
            // A control character is six bytes.
            Enumerable.Repeat((byte)1, 11000).ToArray(),
        ];
        foreach (var value in cut)
        {
            Assert.True(value.Length <= ChunkHeader.MaxLength);
            Assert.Equal(CredentialFailure.AnswerTooLarge, store.Set(Password, value));
        }
        Assert.DoesNotContain(manager.Calls, call => call.StartsWith("write", StringComparison.Ordinal));
        Assert.Equal(fits, Get(Password));
        Assert.Equal(CredentialFailureKind.TooLarge, CredentialFailure.AnswerTooLarge.Kind);
    }

    [Fact]
    public void WritesTheChunksFirstAndTheMainItemLast()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        string[] first =
        [
            "read " + Main,
            "write " + Main + "#1",
            "write " + Main + "#2",
            "write " + Main + "#3",
            "write " + Main,
            "list " + Main + "#",
        ];
        Assert.Equal(first, manager.Calls);

        // The next value goes into the other slot, and the previous chunks
        // go only after the main item names the new ones.
        manager.Calls.Clear();
        Assert.Null(store.Set(Password, Value(5000)));
        string[] second =
        [
            "read " + Main,
            "write " + Main + "#17",
            "write " + Main + "#18",
            "write " + Main,
            "list " + Main + "#",
            "delete " + Main + "#3",
            "delete " + Main + "#2",
            "delete " + Main + "#1",
        ];
        Assert.Equal(second, manager.Calls);
    }

    [Fact]
    public void ReplacingAChunkedValueWritesTheOtherSlot()
    {
        Assert.Null(store.Set(Password, Value(6144, 'a')));
        AssertNames(Main, Main + "#1", Main + "#2", Main + "#3");

        Assert.Null(store.Set(Password, Value(5000, 'b')));
        AssertNames(Main, Main + "#17", Main + "#18");
        Assert.Equal(Value(5000, 'b'), Get(Password));

        Assert.Null(store.Set(Password, Value(7000, 'c')));
        AssertNames(Main, Main + "#1", Main + "#2", Main + "#3");
        Assert.Equal(Value(7000, 'c'), Get(Password));

        var longest = Value(ChunkHeader.MaxLength, 'd');
        Assert.Null(store.Set(Password, longest));
        Assert.Equal(1 + ChunkHeader.MaxChunks, manager.Names.Count);
        Assert.Equal(Main + "#17", manager.Names[1]);
        Assert.Equal(Main + "#32", manager.Names[^1]);
        Assert.Equal(longest, Get(Password));
    }

    [Fact]
    public void SplitsBytesNotCharacters()
    {
        // ö is two bytes, and the first chunk ends between them.
        var value = Encoding.UTF8.GetBytes(new string('a', ChunkHeader.ChunkSize - 1) + "ö" + new string('b', 100));
        Assert.Null(store.Set(Password, value));
        Assert.Equal(0xC3, manager.Item(Main + "#1")!.Blob[^1]);
        Assert.Equal(0xB6, manager.Item(Main + "#2")!.Blob[0]);
        Assert.Equal(value, Get(Password));
    }

    [Fact]
    public void ReplacingAValueRemovesTheChunksItNoLongerNeeds()
    {
        Assert.Null(store.Set(Password, Value(6144, 'x')));
        Assert.Null(store.Set(Password, Value(3000, 'y')));
        AssertNames(Main, Main + "#17", Main + "#18");
        Assert.Equal(Value(3000, 'y'), Get(Password));

        Assert.Null(store.Set(Password, "short"u8));
        AssertNames(Main);
        Assert.Equal("short"u8.ToArray(), Get(Password));

        Assert.Null(store.Set(Password, Value(6144, 'z')));
        AssertNames(Main, Main + "#1", Main + "#2", Main + "#3");
        Assert.Equal(Value(6144, 'z'), Get(Password));
    }

    [Fact]
    public void DeleteRemovesTheItemAndEveryChunk()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        Assert.Null(store.Set(Password, Value(6144)));
        Assert.Null(store.Set(new Request("acc_2", "password"), "other"u8));
        Assert.Null(store.Delete(Password));
        AssertNames("io.github.schotek.Malachi/acc_2/password");
        Assert.Equal(CredentialFailure.NotFound, store.Get(Password, out _));
        // Deleting a missing item is success.
        Assert.Null(store.Delete(Password));
    }

    [Fact]
    public void AStaleChunkIsIgnoredAndThenRemoved()
    {
        var value = Value(6144);
        Assert.Null(store.Set(Password, value));
        manager.Put(Main + "#4", Value(2560, 'q'));
        manager.Put(Main + "#7", Value(10, 'q'));
        manager.Put(Main + "#20", Value(10, 'q'));
        Assert.Equal(value, Get(Password));

        Assert.Null(store.Set(Password, Value(5000)));
        AssertNames(Main, Main + "#17", Main + "#18");
        Assert.Equal(Value(5000), Get(Password));

        manager.Put(Main + "#3", Value(10, 'q'));
        Assert.Null(store.Delete(Password));
        Assert.Empty(manager.Names);
    }

    [Fact]
    public void LeavesNamesThatAreNotItsChunksAlone()
    {
        string[] others = [Main + "#", Main + "#0", Main + "#01", Main + "#1a", Main + "#-1", Main + "#99999999999", Main + "x#1"];
        foreach (var name in others)
        {
            manager.Put(name, [1]);
        }
        Assert.Null(store.Set(Password, "v"u8));
        Assert.Null(store.Set(Password, Value(6144)));
        Assert.Null(store.Delete(Password));
        Assert.Equal(others.Order(StringComparer.Ordinal), manager.Names);
    }

    [Fact]
    public void ATornWriteLeavesThePreviousValue()
    {
        var before = Value(6144, 'a');
        Assert.Null(store.Set(Password, before));
        manager.Inject = (call, name) => call == "write" && name == Main + "#18" ? MemoryCredentialManager.ErrorNoSuchLogonSession : 0;

        var failure = store.Set(Password, Value(6144, 'b'));
        Assert.Equal(CredentialFailureKind.ApiFailed, failure?.Kind);

        manager.Inject = null;
        Assert.Equal(before, Get(Password));
        AssertNames(Main, Main + "#1", Main + "#17", Main + "#2", Main + "#3");

        // The next set writes over what the torn one left, and clears the rest.
        Assert.Null(store.Set(Password, Value(4000, 'c')));
        AssertNames(Main, Main + "#17", Main + "#18");
        Assert.Equal(Value(4000, 'c'), Get(Password));
    }

    [Fact]
    public void ASetThatFailsBeforeItsMainItemLeavesThePreviousValue()
    {
        Assert.Null(store.Set(Password, "old"u8));
        manager.Inject = (call, name) => call == "write" && name == Main ? MemoryCredentialManager.ErrorNoSuchLogonSession : 0;
        Assert.Equal(CredentialFailureKind.ApiFailed, store.Set(Password, Value(6144))?.Kind);
        Assert.Equal(CredentialFailureKind.ApiFailed, store.Set(Password, "new"u8)?.Kind);
        manager.Inject = null;

        Assert.Equal("old"u8.ToArray(), Get(Password));
        Assert.Equal(4, manager.Names.Count);
        // The next set clears the chunks the failed one left behind.
        Assert.Null(store.Set(Password, "new"u8));
        AssertNames(Main);
        Assert.Equal("new"u8.ToArray(), Get(Password));
    }

    [Fact]
    public void ASetThatFailsAfterItsMainItemKeepsTheNewValue()
    {
        Assert.Null(store.Set(Password, Value(6144, 'a')));
        manager.Inject = (call, _) => call == "delete" ? MemoryCredentialManager.ErrorNoSuchLogonSession : 0;
        var failure = store.Set(Password, Value(5000, 'b'));
        Assert.Equal(CredentialFailureKind.ApiFailed, failure?.Kind);
        manager.Inject = null;

        Assert.Equal(Value(5000, 'b'), Get(Password));
        Assert.Null(store.Set(Password, "short"u8));
        AssertNames(Main);
    }

    [Fact]
    public void InterleavedSetsAreCorruptNeverAWrongValue()
    {
        // Two sets of one item at once (the daemon serialises its own):
        // both find slot 0 in use and write slot 1, and the one whose header
        // lands last describes the other's chunks.
        Assert.Null(store.Set(Password, Value(6144, 'a')));
        var interleaved = false;
        manager.Inject = (call, name) =>
        {
            if (call == "write" && name == Main && !interleaved)
            {
                interleaved = true;
                Assert.Null(store.Set(Password, Value(6144, 'c')));
            }
            return 0;
        };
        Assert.Null(store.Set(Password, Value(6144, 'b')));
        manager.Inject = null;

        Assert.True(interleaved);
        Assert.Equal(CredentialFailure.Corrupt("the chunks do not match the header's SHA-256"), store.Get(Password, out var value));
        Assert.Null(value);
    }

    [Fact]
    public void AnItemWrittenByAnotherProgramIsCorrupt()
    {
        // cmdkey and the Credential Manager dialogs store a password as
        // UTF-16LE: "abc" is a\0b\0c\0, UTF-8 that is not the password.
        manager.Put(Main, Encoding.Unicode.GetBytes("abc"));
        Assert.Equal(CredentialFailure.Corrupt(NotWritten), store.Get(Password, out var value));
        Assert.Null(value);
        Assert.All(manager.HandedOut.Single(), b => Assert.Equal(0, b));

        // Text in a script above Latin-1 has no zero byte in UTF-16LE.
        manager.Put(Main, Encoding.Unicode.GetBytes("пароль"));
        Assert.True(Utf8.IsValid(manager.Item(Main)!.Blob));
        Assert.Equal(CredentialFailure.Corrupt(NotWritten), store.Get(Password, out value));
        Assert.Null(value);

        // Nor are bytes taken that would make a fine value.
        manager.Put(Main, "hunter2"u8.ToArray());
        Assert.Equal(CredentialFailure.Corrupt(NotWritten), store.Get(Password, out value));
        Assert.Null(value);

        // The next set replaces the item.
        Assert.Null(store.Set(Password, "hunter2"u8));
        Assert.Equal("hunter2"u8.ToArray(), Get(Password));
    }

    [Fact]
    public void AnItemChangedInPlaceIsCorrupt()
    {
        // An edit that keeps the item's attributes but not its blob.
        Assert.Null(store.Set(Password, "hunter2"u8));
        var digest = manager.Item(Main)!.Digest;
        manager.Put(Main, Encoding.Unicode.GetBytes("hunter3"), digest);
        Assert.Equal(CredentialFailure.Corrupt("the value does not match the item's SHA-256"), store.Get(Password, out var value));
        Assert.Null(value);
        Assert.All(manager.HandedOut.Single(), b => Assert.Equal(0, b));

        manager.Put(Main, "hunter2"u8.ToArray(), [1, 2, 3]);
        Assert.Equal(CredentialFailure.Corrupt("the value does not match the item's SHA-256"), store.Get(Password, out value));
        Assert.Null(value);
    }

    [Fact]
    public void AMissingChunkIsCorrupt()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        manager.Remove(Main + "#2");
        Assert.Equal(CredentialFailure.Corrupt("chunk 2 of 3 is missing"), store.Get(Password, out var value));
        Assert.Null(value);

        Assert.Null(store.Set(Password, Value(6144)));
        manager.Remove(Main + "#19");
        Assert.Equal(CredentialFailure.Corrupt("chunk 3 of 3 is missing"), store.Get(Password, out value));
        Assert.Null(value);
    }

    [Fact]
    public void AChunkOfTheWrongSizeIsCorrupt()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        manager.Put(Main + "#3", Value(1000));
        Assert.Equal(CredentialFailure.Corrupt("chunk 3 of 3 has the wrong size"), store.Get(Password, out var value));
        Assert.Null(value);
    }

    [Fact]
    public void AChangedChunkIsCorrupt()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        var chunk = manager.Item(Main + "#2")!.Blob;
        chunk[7] = (byte)'!';
        manager.Put(Main + "#2", chunk);
        Assert.Equal(CredentialFailure.Corrupt("the chunks do not match the header's SHA-256"), store.Get(Password, out var value));
        Assert.Null(value);
    }

    [Fact]
    public void AHeaderItCannotReadIsCorrupt()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        var header = manager.Item(Main)!.Blob;
        header[1] = 9;
        manager.Put(Main, header);
        Assert.Equal(CredentialFailure.Corrupt("header version 9 is unknown"), store.Get(Password, out var value));
        Assert.Null(value);

        // A set does not trust it either: it writes slot 0 and replaces it.
        Assert.Null(store.Set(Password, Value(5000)));
        AssertNames(Main, Main + "#1", Main + "#2");
        Assert.Equal(Value(5000), Get(Password));
    }

    [Fact]
    public void AStoredValueThatIsNotUtf8IsCorrupt()
    {
        manager.PutValue(Main, [(byte)'a', 0xC3, 0x28]);
        Assert.Equal(CredentialFailure.Corrupt("the value is not UTF-8"), store.Get(Password, out var value));
        Assert.Null(value);

        // Chunks that match their header but do not make UTF-8.
        var bytes = Value(3000);
        bytes[2999] = 0xC3;
        var header = HeaderFor(bytes);
        manager.Put(Main, header);
        manager.Put(Main + "#1", bytes[..2560]);
        manager.Put(Main + "#2", bytes[2560..]);
        Assert.Equal(CredentialFailure.Corrupt("the value is not UTF-8"), store.Get(Password, out value));
        Assert.Null(value);
    }

    [Fact]
    public void OnlyUtf8IsStored()
    {
        // What keeps a stored value apart from a header (ChunkHeader.Marker).
        Assert.Throws<ArgumentException>(() => store.Set(Password, [0xFF, (byte)'a']));
        Assert.Empty(manager.Names);
    }

    [Theory]
    [InlineData("get", "read", "main", "CredReadW")]
    [InlineData("get", "read", "chunk", "CredReadW")]
    [InlineData("set", "read", "main", "CredReadW")]
    [InlineData("set", "write", "main", "CredWriteW")]
    [InlineData("set", "write", "chunk", "CredWriteW")]
    [InlineData("set", "list", "chunk", "CredEnumerateW")]
    [InlineData("set", "delete", "chunk", "CredDeleteW")]
    [InlineData("delete", "delete", "main", "CredDeleteW")]
    [InlineData("delete", "delete", "chunk", "CredDeleteW")]
    [InlineData("delete", "list", "chunk", "CredEnumerateW")]
    public void AFailedCallNamesTheFunctionAndTheError(string operation, string call, string item, string function)
    {
        // A secret long enough to be chunked, stored first unless set is tested.
        var secret = Encoding.UTF8.GetBytes("s3cret-" + new string('v', 6000));
        if (operation != "set")
        {
            Assert.Null(store.Set(Password, secret));
        }
        else if (call == "delete")
        {
            // A stale chunk for the set to remove.
            manager.Put(Main + "#9", [1]);
        }
        // The prefix of a list call ends in #, so it counts as a chunk's.
        manager.Inject = (c, name) => c == call && name.Contains('#', StringComparison.Ordinal) == (item == "chunk")
            ? MemoryCredentialManager.ErrorNoSuchLogonSession
            : 0;

        var failure = operation switch
        {
            "get" => store.Get(Password, out _),
            "set" => store.Set(Password, secret),
            _ => store.Delete(Password),
        };

        Assert.NotNull(failure);
        Assert.Equal(CredentialFailureKind.ApiFailed, failure.Kind);
        Assert.Equal(MemoryCredentialManager.ErrorNoSuchLogonSession, failure.Error);
        Assert.StartsWith(function + " failed: ", failure.Message, StringComparison.Ordinal);
        Assert.EndsWith("(Win32 error 1312)", failure.Message, StringComparison.Ordinal);
        Assert.DoesNotContain("s3cret", failure.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void NotFoundIsItsOwnFailure()
    {
        Assert.Equal(CredentialFailure.NotFound, store.Get(Password, out var value));
        Assert.Null(value);
        Assert.Equal(CredentialFailureKind.NotFound, CredentialFailure.NotFound.Kind);
        Assert.Equal("item not found", CredentialFailure.NotFound.Message);
    }

    [Fact]
    public void GetZeroesEveryCopyItDoesNotHandOver()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        var value = Get(Password);
        Assert.Equal(4, manager.HandedOut.Count);
        foreach (var blob in manager.HandedOut.Skip(1))
        {
            // The chunks; the header (HandedOut[0]) is no secret.
            Assert.All(blob, b => Assert.Equal(0, b));
        }
        Assert.DoesNotContain(manager.HandedOut, blob => ReferenceEquals(blob, value));

        manager.HandedOut.Clear();
        Assert.Null(store.Set(Password, "hunter2"u8));
        value = Get(Password);
        Assert.Same(manager.HandedOut.Single(), value);

        manager.HandedOut.Clear();
        manager.Put(Main, [(byte)'x', 0xC3]);
        Assert.NotNull(store.Get(Password, out _));
        Assert.All(manager.HandedOut.Single(), b => Assert.Equal(0, b));
    }

    [Fact]
    public void SetZeroesThePreviousValueItReads()
    {
        // A chunked set reads the main item for its slot, which may be the
        // previous value.
        Assert.Null(store.Set(Password, "hunter2"u8));
        manager.HandedOut.Clear();
        Assert.Null(store.Set(Password, Value(6144)));
        Assert.All(manager.HandedOut.Single(), b => Assert.Equal(0, b));
    }

    [Fact]
    public void ACorruptValueIsZeroedToo()
    {
        Assert.Null(store.Set(Password, Value(6144)));
        manager.Remove(Main + "#3");
        Assert.NotNull(store.Get(Password, out _));
        Assert.Equal(3, manager.HandedOut.Count);
        Assert.All(manager.HandedOut[1], b => Assert.Equal(0, b));
        Assert.All(manager.HandedOut[2], b => Assert.Equal(0, b));
    }

    [Fact]
    public void CutsACommentLongerThanCredentialManagerKeeps()
    {
        // The limit counts the terminating NUL (measured: 256 fails).
        Assert.Equal(255, CredentialStore.MaxComment);
        var request = new Request(new string('a', Request.MaxIdentifier), new string('k', Request.MaxIdentifier));
        Assert.Null(store.Set(request, Value(6144)));
        foreach (var name in manager.Names)
        {
            var comment = manager.Item(name)?.Comment;
            Assert.NotNull(comment);
            Assert.Equal(CredentialStore.MaxComment, comment.Length);
            Assert.StartsWith("Malachi Mail: aaaa", comment, StringComparison.Ordinal);
        }
        Assert.Equal(Value(6144), Get(request));
    }

    [Fact]
    public void NoItemCarriesTheValueOutsideItsBlob()
    {
        var value = Encoding.UTF8.GetBytes("s3cret-" + new string('v', 6000));
        Assert.Null(store.Set(Password, value));
        Assert.Null(store.Set(new Request("acc_2", "password"), "s3cret"u8));
        foreach (var name in manager.Names)
        {
            var item = manager.Item(name)!;
            Assert.DoesNotContain("s3cret", item.TargetName + item.UserName + item.Comment, StringComparison.Ordinal);
            Assert.True(item.Digest is null || item.Digest.Length == SHA256.HashSizeInBytes);
        }
    }

    [Fact]
    public void ADigestFitsAnAttribute()
    {
        Assert.True(SHA256.HashSizeInBytes <= (int)PInvoke.CRED_MAX_VALUE_SIZE);
    }

    // ASCII: fill, fill + 1, fill + 2, fill, …
    private static byte[] Value(int length, char fill = 'v')
    {
        var value = new byte[length];
        for (var i = 0; i < length; i++)
        {
            value[i] = (byte)(fill + (i % 3));
        }
        return value;
    }

    private void AssertNames(params string[] names) => Assert.Equal(names, manager.Names);

    private static byte[] HeaderFor(byte[] value)
    {
        // ChunkHeader.For needs no UTF-8, so a header can describe bytes the
        // store would never write.
        return ChunkHeader.For(value, 0).Encode();
    }

    private byte[]? Get(Request request)
    {
        var failure = store.Get(request, out var value);
        Assert.Null(failure);
        return value;
    }
}
