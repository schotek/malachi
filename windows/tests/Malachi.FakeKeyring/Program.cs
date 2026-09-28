// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A keyring helper for the UI tests, the protocol of
// backend/internal/auth/helper (MALACHI_KEYRING=helper): `get|set|delete`
// in argv, one JSON line {"account","key"[,"value"]} on stdin, {"value"} on
// stdout for get; exit 0 done, 2 not found, 3 a malformed request, 1 any
// other failure. The items live in the JSON file named by
// MALACHI_FAKE_KEYRING_FILE, which the test creates in its temporary
// folder and deletes with it: a test account's password never reaches
// Credential Manager. The daemon runs one helper at a time per account;
// a named mutex keeps concurrent runs of different accounts apart.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;

namespace Malachi.FakeKeyring;

/// <summary>The helper's entry point.</summary>
public static class Program
{
    /// <summary>The environment variable naming the store file.</summary>
    public const string FileEnv = "MALACHI_FAKE_KEYRING_FILE";

    private const int Done = 0;
    private const int Failed = 1;
    private const int NotFound = 2;
    private const int BadRequest = 3;

    /// <summary>Runs one operation.</summary>
    public static int Main(string[] args)
    {
        ArgumentNullException.ThrowIfNull(args);
        var path = Environment.GetEnvironmentVariable(FileEnv);
        if (string.IsNullOrEmpty(path) || args.Length != 1)
        {
            return BadRequest;
        }
        string account;
        string key;
        string? value;
        try
        {
            using var request = JsonDocument.Parse(Console.In.ReadToEnd());
            account = request.RootElement.GetProperty("account").GetString() ?? "";
            key = request.RootElement.GetProperty("key").GetString() ?? "";
            value = request.RootElement.TryGetProperty("value", out var v) ? v.GetString() : null;
        }
        catch (Exception e) when (e is JsonException or KeyNotFoundException or InvalidOperationException)
        {
            return BadRequest;
        }
        if (account.Length == 0 || key.Length == 0)
        {
            return BadRequest;
        }
        using var gate = new Mutex(false, @"Local\io.github.schotek.Malachi.fake-keyring");
        bool owned;
        try
        {
            owned = gate.WaitOne(TimeSpan.FromSeconds(10));
        }
        catch (AbandonedMutexException)
        {
            // The previous run died holding it; the file is rewritten whole.
            owned = true;
        }
        if (!owned)
        {
            Console.Error.WriteLine("fake keyring: busy");
            return Failed;
        }
        try
        {
            return Run(args[0], path, account + "/" + key, value);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or JsonException)
        {
            Console.Error.WriteLine("fake keyring: " + e.GetType().Name);
            return Failed;
        }
        finally
        {
            gate.ReleaseMutex();
        }
    }

    private static int Run(string operation, string path, string id, string? value)
    {
        var items = File.Exists(path)
            ? JsonSerializer.Deserialize<Dictionary<string, string>>(File.ReadAllText(path)) ?? []
            : [];
        switch (operation)
        {
            case "get":
                if (!items.TryGetValue(id, out var found))
                {
                    return NotFound;
                }
                Console.Out.WriteLine(JsonSerializer.Serialize(new Dictionary<string, string> { ["value"] = found }));
                return Done;
            case "set":
                if (value is null)
                {
                    return BadRequest;
                }
                items[id] = value;
                break;
            case "delete":
                if (!items.Remove(id))
                {
                    return NotFound;
                }
                break;
            default:
                return BadRequest;
        }
        File.WriteAllText(path, JsonSerializer.Serialize(items));
        return Done;
    }
}
