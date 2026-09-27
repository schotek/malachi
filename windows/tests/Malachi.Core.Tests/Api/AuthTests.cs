// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AuthTests.swift (the suite
// AuthTests; its DaemonKeyTests belong to the transport's key-file reader).
//
// The handshake's building blocks (docs/api.md §1.4; backend/pkg/api
// auth_test.go): the proofs against the documented test vectors, the strict
// parsing of hex and of the key file, and the key file's path.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class AuthTests
{
    // The test vectors, copied verbatim from the table in docs/api.md §1.4
    // (the same as backend/pkg/api/auth_test.go): key = bytes 0x00…0x1f,
    // clientNonce = 0x20…0x3f, daemonNonce = 0x40…0x5f.
    private const string VectorKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f";
    private const string VectorClientNonce = "202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f";
    private const string VectorDaemonNonce = "404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f";
    private const string VectorLabel = "6d616c616368692d7270632d617574682d7631";
    private const string VectorDaemonRole = "6461656d6f6e";
    private const string VectorClientRole = "636c69656e74";
    private const string VectorDaemonProof = "04abc851d52b40dc687920756f15f42f44da2635331732bf02be0a0b01de2a1f";
    private const string VectorClientProof = "024f86a00c241237f4556a27e83f8e41b13053bbcfd2980e029c300a5a2e84b2";

    // 32 bytes counting up from first.
    private static byte[] Bytes(int first) => [.. Enumerable.Range(0, 32).Select(i => (byte)(first + i))];

    // The bytes with one bit flipped.
    private static byte[] Flip(byte[] data, int bit)
    {
        var d = data.ToArray();
        d[bit / 8] ^= (byte)(1 << (bit % 8));
        return d;
    }

    [Fact]
    public void ProofsMatchTheDocumentedVectors()
    {
        var key = Bytes(0x00);
        var clientNonce = Bytes(0x20);
        var daemonNonce = Bytes(0x40);
        Assert.Equal(VectorKey, RpcAuth.Hex(key));
        Assert.Equal(VectorClientNonce, RpcAuth.Hex(clientNonce));
        Assert.Equal(VectorDaemonNonce, RpcAuth.Hex(daemonNonce));
        Assert.Equal(VectorLabel, RpcAuth.Hex(Encoding.UTF8.GetBytes(RpcAuth.Label)));
        Assert.Equal(VectorDaemonRole, RpcAuth.Hex(Encoding.UTF8.GetBytes(RpcAuth.RoleDaemon)));
        Assert.Equal(VectorClientRole, RpcAuth.Hex(Encoding.UTF8.GetBytes(RpcAuth.RoleClient)));
        Assert.Equal(VectorDaemonProof, RpcAuth.Hex(RpcAuth.DaemonProof(key, clientNonce, daemonNonce)));
        Assert.Equal(VectorClientProof, RpcAuth.Hex(RpcAuth.ClientProof(key, clientNonce, daemonNonce)));
        // 91 bytes: the label, NUL, a six-letter role, NUL, two 32-byte nonces.
        Assert.Equal(91, RpcAuth.Message(RpcAuth.RoleDaemon, clientNonce, daemonNonce).Length);
        Assert.Equal(91, RpcAuth.Message(RpcAuth.RoleClient, clientNonce, daemonNonce).Length);

        // Verification: the daemon's proof holds, the client's does not,
        // and swapped nonces do not either.
        var daemonProof = RpcAuth.DecodeHex32(VectorDaemonProof);
        var clientProof = RpcAuth.DecodeHex32(VectorClientProof);
        Assert.NotNull(daemonProof);
        Assert.NotNull(clientProof);
        Assert.True(RpcAuth.IsValidDaemonProof(daemonProof, key, clientNonce, daemonNonce));
        Assert.False(RpcAuth.IsValidDaemonProof(clientProof, key, clientNonce, daemonNonce), "a reflected client proof");
        Assert.False(RpcAuth.IsValidDaemonProof(daemonProof, key, daemonNonce, clientNonce), "swapped nonces");
        Assert.False(RpcAuth.IsValidDaemonProof(new byte[32], key, clientNonce, daemonNonce));
        Assert.False(RpcAuth.IsValidDaemonProof(daemonProof.AsSpan(0, 31), key, clientNonce, daemonNonce));

        // The key file of the vector key.
        Assert.Equal(key, RpcAuth.ParseKey(Encoding.UTF8.GetBytes(VectorKey + "\n")));
    }

    [Fact]
    public void ProofsSeparateRolesAndInputs()
    {
        var key = RpcAuth.NewNonce();
        var clientNonce = RpcAuth.NewNonce();
        var daemonNonce = RpcAuth.NewNonce();
        var d = RpcAuth.DaemonProof(key, clientNonce, daemonNonce);
        var c = RpcAuth.ClientProof(key, clientNonce, daemonNonce);
        Assert.Equal(32, d.Length);
        Assert.Equal(32, c.Length);
        Assert.NotEqual(d, c); // the daemon's and the client's proofs differ
        Assert.Equal(d, RpcAuth.DaemonProof(key, clientNonce, daemonNonce)); // deterministic
        foreach (var bit in new[] { 0, 7, 8, 131, 255 })
        {
            Assert.False(RpcAuth.IsValidDaemonProof(d, Flip(key, bit), clientNonce, daemonNonce));
            Assert.False(RpcAuth.IsValidDaemonProof(d, key, Flip(clientNonce, bit), daemonNonce));
            Assert.False(RpcAuth.IsValidDaemonProof(d, key, clientNonce, Flip(daemonNonce, bit)));
            Assert.False(RpcAuth.IsValidDaemonProof(Flip(d, bit), key, clientNonce, daemonNonce));
        }
    }

    [Fact]
    public void NoncesAreFresh()
    {
        var seen = new HashSet<string>(StringComparer.Ordinal);
        for (var i = 0; i < 1000; i++)
        {
            var n = RpcAuth.NewNonce();
            Assert.Equal(32, n.Length);
            seen.Add(RpcAuth.Hex(n));
        }
        Assert.Equal(1000, seen.Count); // a nonce repeated
        Assert.DoesNotContain(RpcAuth.Hex(new byte[32]), seen);
    }

    [Fact]
    public void HexIsExactlySixtyFourLowercaseDigits()
    {
        var good = string.Concat(Enumerable.Repeat("0123456789abcdef", 4));
        var decoded = RpcAuth.DecodeHex32(good);
        Assert.NotNull(decoded);
        Assert.Equal(32, decoded.Length);
        Assert.Equal(good, RpcAuth.Hex(decoded));
        var head62 = good[..62];
        var head63 = good[..63];
        var bad = new Dictionary<string, string>(StringComparer.Ordinal)
        {
            ["empty"] = "",
            ["63 digits"] = head63,
            ["65 digits"] = good + "0",
            ["128 digits"] = good + good,
            ["upper case"] = good.ToUpperInvariant(),
            ["mixed case"] = good[..60] + "ABcd",
            ["0x prefix"] = "0x" + head62,
            ["0x prefix, 66 bytes"] = "0x" + good,
            ["leading space"] = " " + good[1..],
            ["trailing space"] = head63 + " ",
            ["surrounding space"] = " " + good + " ",
            ["tab"] = "\t" + good[1..],
            ["newline"] = head63 + "\n",
            ["trailing newline"] = good + "\n",
            ["NUL"] = head63 + "\0",
            ["g"] = head63 + "g",
            ["full-width digits"] = new string('\uFF10', 64),
            ["full-width, 64 bytes"] = new string('\uFF10', 21) + "0",
            ["two-byte character, 64 bytes"] = head62 + "\u00E9",
        };
        foreach (var (name, s) in bad)
        {
            if (name.EndsWith("64 bytes", StringComparison.Ordinal))
            {
                Assert.Equal(64, Encoding.UTF8.GetByteCount(s)); // the case is what its name says
            }
            Assert.True(RpcAuth.DecodeHex32(s) is null, $"{name} accepted");
        }
    }

    [Fact]
    public void KeyFileContentIsStrict()
    {
        var key = RpcAuth.NewNonce();
        var good = RpcAuth.Hex(key);
        Assert.Equal(key, RpcAuth.ParseKey(Encoding.UTF8.GetBytes(good + "\n")));
        var head63 = good[..63];
        var bad = new Dictionary<string, byte[]>(StringComparer.Ordinal)
        {
            ["empty"] = [],
            ["newline only"] = "\n"u8.ToArray(),
            ["missing newline"] = Encoding.UTF8.GetBytes(good),
            ["CRLF"] = Encoding.UTF8.GetBytes(good + "\r\n"),
            ["CR instead of newline"] = Encoding.UTF8.GetBytes(good + "\r"),
            ["two newlines"] = Encoding.UTF8.GetBytes(good + "\n\n"),
            ["newline first"] = Encoding.UTF8.GetBytes("\n" + good),
            ["leading space"] = Encoding.UTF8.GetBytes(" " + good + "\n"),
            ["leading space, 65 bytes"] = Encoding.UTF8.GetBytes(" " + good[1..] + "\n"),
            ["trailing space"] = Encoding.UTF8.GetBytes(good + " \n"),
            ["space before newline"] = Encoding.UTF8.GetBytes(head63 + " \n"),
            ["BOM"] = Encoding.UTF8.GetBytes("\uFEFF" + good + "\n"),
            ["BOM, 65 bytes"] = Encoding.UTF8.GetBytes("\uFEFF" + good[3..] + "\n"),
            ["upper case"] = Encoding.UTF8.GetBytes(good.ToUpperInvariant() + "\n"),
            ["NUL"] = Encoding.UTF8.GetBytes(head63 + "\0\n"),
            ["NUL instead of newline"] = Encoding.UTF8.GetBytes(good + "\0"),
            ["1 MiB"] = Enumerable.Repeat((byte)'a', 1 << 20).ToArray(),
            ["key and garbage"] = Encoding.UTF8.GetBytes(good + "\ngarbage"),
            ["two keys"] = Encoding.UTF8.GetBytes(good + "\n" + good + "\n"),
        };
        foreach (var (name, content) in bad)
        {
            if (name.EndsWith("65 bytes", StringComparison.Ordinal))
            {
                Assert.Equal(65, content.Length); // the case is what its name says
            }
            Assert.True(RpcAuth.ParseKey(content) is null, $"{name} accepted");
        }
    }

    [Fact]
    public void KeyPathLiesBesideTheSocket()
    {
        Assert.Equal("/Users/u/.cache/malachi/run/rpc.sock.key", RpcAuth.KeyPath("/Users/u/.cache/malachi/run/rpc.sock"));
        Assert.Equal("/run/user/1000/malachi/rpc.sock.key", RpcAuth.KeyPath("/run/user/1000/malachi/rpc.sock"));
        Assert.Equal("rpc.sock.key", RpcAuth.KeyPath("rpc.sock"));
        // Windows addition: the daemon's default path on Windows.
        Assert.Equal(@"C:\Users\u\.cache\malachi\run\rpc.sock.key", RpcAuth.KeyPath(@"C:\Users\u\.cache\malachi\run\rpc.sock"));
    }
}
