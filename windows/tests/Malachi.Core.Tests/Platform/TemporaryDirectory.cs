// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A scratch directory for one test, under the system's temporary directory
// (Swift: FileManager.temporaryDirectory with a random name; Go: t.TempDir).

using System;
using System.IO;

namespace Malachi.Core.Tests.Platform;

/// <summary>A fresh directory, removed with everything in it on dispose.</summary>
internal sealed class TemporaryDirectory : IDisposable
{
    public TemporaryDirectory()
    {
        Path = System.IO.Path.Combine(System.IO.Path.GetTempPath(), "malachi-test-" + Guid.NewGuid().ToString("N")[..12]);
        Directory.CreateDirectory(Path);
    }

    public string Path { get; }

    public void Dispose() => Remove(Path);

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
