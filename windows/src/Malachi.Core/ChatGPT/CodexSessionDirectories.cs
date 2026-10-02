// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Private runtime retention from docs/chatgpt-integration.md §6. Mirrors
// the app-owned temporary directory cleanup of Go/Swift attachment views,
// with an exclusive lease so another app instance's active session survives.

using System;
using System.IO;
using Malachi.Core.Platform;

namespace Malachi.Core.ChatGPT;

/// <summary>Only application-created session GUID directories are swept.</summary>
public static class CodexSessionDirectories
{
    /// <summary>Removes abandoned owned directories; refuses links at every level.</summary>
    public static void Sweep(string root, IPrivateDirectoryFactory? directories = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(root);
        if (!Path.IsPathFullyQualified(root)) { throw new IOException("codex state path is not absolute"); }
        if (directories is null) { Directory.CreateDirectory(root); }
        else { directories.Ensure(root); }
        if ((File.GetAttributes(root) & FileAttributes.ReparsePoint) != 0) { throw new IOException("codex state path is a link"); }
        foreach (var path in Directory.EnumerateDirectories(root, "session-*"))
        {
            var name = Path.GetFileName(path);
            if (!Guid.TryParseExact(name["session-".Length..], "N", out _) || HasLink(path)) { continue; }
            try
            {
                var lease = Path.Combine(path, "lease");
                // No lease means creation has not reached its first atomic file
                // yet: it has no prompt/state, and another instance may own it.
                if (!File.Exists(lease)) { continue; }
                using (var probe = new FileStream(lease, FileMode.Open, FileAccess.ReadWrite, FileShare.None)) { }
                Directory.Delete(path, recursive: true);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                // A live session has the lease, or an abandoned path is inaccessible.
            }
        }
    }

    private static bool HasLink(string directory)
    {
        if ((File.GetAttributes(directory) & FileAttributes.ReparsePoint) != 0) { return true; }
        foreach (var path in Directory.EnumerateFileSystemEntries(directory))
        {
            var attributes = File.GetAttributes(path);
            if ((attributes & FileAttributes.ReparsePoint) != 0) { return true; }
            if ((attributes & FileAttributes.Directory) != 0 && HasLink(path)) { return true; }
        }
        return false;
    }
}
