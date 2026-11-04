// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §6.1, docs/security.md §3.3): the
// crash dumps of the app's WebView2 browser. Its Crashpad handler writes a
// minidump of every renderer that dies into
// <user data folder>\EBWebView\Crashpad\reports (and what goes with one into
// attachments), and a renderer's memory holds the message it showed or the
// draft being written. With IsCustomCrashReportingEnabled nothing sends them
// to Microsoft, and nothing else ever deletes them, while a hostile body can
// crash a renderer once per document. GTK and macOS leave no such files: a
// WebKit web process that dies leaves its crash to the system's reporter.
// So the app empties both folders when it starts, before the environment
// exists, and when it quits. Best effort: an entry still in use stays for
// the next sweep. The database's own files (metadata, settings) stay, so
// the handler finds its database as it left it.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;

namespace Malachi.Core.Platform;

/// <summary>The WebView2 browser's crash reports, and their removal.</summary>
public static class CrashDumps
{
    // The folders of the Crashpad database that hold dumps and their
    // attachments, under the user data folder.
    private static readonly string[] Database = ["EBWebView", "Crashpad"];
    private static readonly string[] Folders = ["reports", "attachments"];

    /// <summary>
    /// Removes everything in the Crashpad <c>reports</c> and
    /// <c>attachments</c> folders under <paramref name="userDataFolder"/>
    /// (<c>&lt;data dir&gt;\WebView2</c>): the entries removed and the ones
    /// that could not be (in use, or refused). A folder on the way that is a
    /// link or a junction is left alone, never followed: its target is not
    /// the app's. Nothing there, nothing to do.
    /// </summary>
    public static (int Removed, int Failed) Sweep(string userDataFolder)
    {
        ArgumentException.ThrowIfNullOrEmpty(userDataFolder);
        var database = userDataFolder;
        foreach (var name in Database)
        {
            database = Path.Combine(database, name);
            if (!IsPlainDirectory(database))
            {
                return (0, 0);
            }
        }
        var removed = 0;
        var failed = 0;
        foreach (var name in Folders)
        {
            var folder = Path.Combine(database, name);
            if (!IsPlainDirectory(folder))
            {
                continue;
            }
            List<FileSystemInfo> entries;
            try
            {
                entries = new DirectoryInfo(folder).EnumerateFileSystemInfos().ToList();
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                failed++;
                continue;
            }
            foreach (var entry in entries)
            {
                if (Remove(entry))
                {
                    removed++;
                }
                else
                {
                    failed++;
                }
            }
        }
        return (removed, failed);
    }

    // A directory that exists and is no link or junction.
    private static bool IsPlainDirectory(string path)
    {
        try
        {
            var attributes = File.GetAttributes(path);
            return (attributes & FileAttributes.Directory) != 0 && (attributes & FileAttributes.ReparsePoint) == 0;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    // One entry of reports or attachments: a file, a link (the link itself,
    // never its target), or a directory with everything in it, which
    // Directory.Delete removes without following the links inside.
    private static bool Remove(FileSystemInfo entry)
    {
        try
        {
            if ((entry.Attributes & FileAttributes.ReadOnly) != 0)
            {
                entry.Attributes &= ~FileAttributes.ReadOnly;
            }
            if (entry is DirectoryInfo directory && (entry.Attributes & FileAttributes.ReparsePoint) == 0)
            {
                directory.Delete(recursive: true);
            }
            else
            {
                entry.Delete();
            }
            return true;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }
}
