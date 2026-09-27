// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A scratch directory for one test, under the system's temporary directory,
// and the links the tests need (a junction needs no privilege; a symbolic
// link needs Developer Mode or the privilege, and skips without).

using System;
using System.Diagnostics;
using System.IO;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Files;

/// <summary>A fresh directory, removed with everything in it on dispose.</summary>
internal sealed class TestDirectory : IDisposable
{
    public TestDirectory()
    {
        Path = System.IO.Path.Combine(System.IO.Path.GetTempPath(), "malachi-test-" + Guid.NewGuid().ToString("N")[..12]);
        Directory.CreateDirectory(Path);
    }

    public string Path { get; }

    public string Combine(params string[] parts) => System.IO.Path.Combine([Path, .. parts]);

    public void Dispose() => Remove(Path);

    /// <summary>A junction at <paramref name="link"/> to the directory <paramref name="target"/>.</summary>
    public static void CreateJunction(string link, string target)
    {
        var start = new ProcessStartInfo("cmd.exe")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var argument in new[] { "/d", "/c", "mklink", "/J", link, target })
        {
            start.ArgumentList.Add(argument);
        }
        using var process = Process.Start(start)!;
        if (!process.WaitForExit(TimeSpan.FromSeconds(30)))
        {
            process.Kill(entireProcessTree: true);
            Assert.Fail("mklink did not finish");
        }
        Assert.True(process.ExitCode == 0, "mklink /J failed: " + process.StandardError.ReadToEnd());
        Assert.True((File.GetAttributes(link) & FileAttributes.ReparsePoint) != 0);
    }

    /// <summary>A directory symbolic link, or a skip where the user may not make one.</summary>
    public static void CreateSymbolicLinkOrSkip(string link, string target)
    {
        try
        {
            Directory.CreateSymbolicLink(link, target);
        }
        catch (Exception e) when (e is UnauthorizedAccessException or IOException)
        {
            Assert.Skip("symbolic links are not allowed here: " + e.Message);
        }
    }

    // Everything under path, links removed themselves and never followed,
    // read-only files included; what cannot go stays.
    private static void Remove(string path)
    {
        try
        {
            var attributes = File.GetAttributes(path);
            if ((attributes & FileAttributes.ReparsePoint) != 0)
            {
                if ((attributes & FileAttributes.Directory) != 0)
                {
                    Directory.Delete(path, recursive: false);
                }
                else
                {
                    File.Delete(path);
                }
                return;
            }
            if ((attributes & FileAttributes.Directory) != 0)
            {
                foreach (var entry in Directory.EnumerateFileSystemEntries(path))
                {
                    Remove(entry);
                }
                Directory.Delete(path, recursive: false);
                return;
            }
            File.SetAttributes(path, FileAttributes.Normal);
            File.Delete(path);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
        }
    }
}
