// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/OpenDir.swift; GTK:
// ui/internal/window/attachments.go (openDir, writeOpenFile, sweepOpenDir,
// SweepOpenedAttachments). On Windows the directory is
// %LOCALAPPDATA%\Malachi Mail\open (docs/windows-port.md §1, §10), made
// private by a protected DACL instead of mode 0700 (IPrivateDirectoryFactory),
// and the sweeps leave alone what a viewer still holds open.

using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;

namespace Malachi.Core.Platform;

/// <summary>
/// Where attachments being opened are written (attachments.go
/// <c>openDir</c>, <c>writeOpenFile</c>, <c>sweepOpenDir</c>,
/// <c>SweepOpenedAttachments</c>; docs/security.md §8): a private directory,
/// one fresh private subdirectory per file, each file created exclusively.
/// Entries older than <see cref="OpenMaxAge"/> are swept before every
/// write; the whole directory goes when the application starts and when it
/// exits (<see cref="RemoveAll"/>). Synchronous file work: callers run it
/// off the UI thread.
/// </summary>
public sealed class OpenDir
{
    /// <summary>
    /// How long a file written for opening is kept before the next open
    /// sweeps it: the viewer may still be reading it lazily (attachments.go
    /// <c>openMaxAge</c>, AttachmentChips.swift <c>openMaxAge</c>).
    /// </summary>
    public static readonly TimeSpan OpenMaxAge = TimeSpan.FromHours(1);

    /// <summary>The directory's name inside the data directory.</summary>
    public const string DirectoryName = "open";

    // A path that a program which knows no long paths still opens has at
    // most 259 characters (MAX_PATH with its terminating NUL); a file name
    // is kept within what is left of it where it can be
    // (MaxFileNameLength).
    private const int MaxPath = 260;

    // mkdtemp's ten random characters; lower case, as Windows compares
    // names without regard to case.
    private const string SubdirectoryAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789";
    private const int SubdirectoryNameLength = 10;
    private const int SubdirectoryAttempts = 8;

    private readonly IPrivateDirectoryFactory directories;
    private readonly TimeProvider time;
    private readonly IReadOnlySet<char>? lookAlikes;

    /// <summary>
    /// The open directory at <paramref name="path"/>, which must be fully
    /// qualified; <paramref name="directories"/> makes it and its
    /// subdirectories private, and <paramref name="lookAlikes"/> are the
    /// characters this machine's ANSI code page turns into reserved ones
    /// (<see cref="WindowsFileNames.Sanitize(string, int, IReadOnlySet{char})"/>).
    /// </summary>
    public OpenDir(string path, IPrivateDirectoryFactory directories, TimeProvider time, IReadOnlySet<char>? lookAlikes = null)
    {
        ArgumentNullException.ThrowIfNull(path);
        ArgumentNullException.ThrowIfNull(directories);
        ArgumentNullException.ThrowIfNull(time);
        if (!System.IO.Path.IsPathFullyQualified(path))
        {
            throw new ArgumentException("the open directory must be a fully qualified path", nameof(path));
        }
        Path = System.IO.Path.TrimEndingDirectorySeparator(System.IO.Path.GetFullPath(path));
        this.directories = directories;
        this.time = time;
        this.lookAlikes = lookAlikes;
    }

    /// <summary>The directory (OpenDir.swift <c>url</c>).</summary>
    public string Path { get; }

    /// <summary>
    /// The open directory of a data directory: <c>&lt;data&gt;\open</c>, so
    /// <c>%LOCALAPPDATA%\Malachi Mail\open</c> by default (OpenDir.swift
    /// <c>default</c>). The caller resolves the data directory, including
    /// the <c>MALACHI_DATA_DIR</c> override.
    /// </summary>
    public static OpenDir InDataDirectory(
        string dataDirectory, IPrivateDirectoryFactory directories, TimeProvider time, IReadOnlySet<char>? lookAlikes = null)
    {
        ArgumentNullException.ThrowIfNull(dataDirectory);
        return new OpenDir(System.IO.Path.Combine(dataDirectory, DirectoryName), directories, time, lookAlikes);
    }

    /// <summary>
    /// The longest file name <see cref="Write"/> gives a file: what keeps
    /// its whole path within MAX_PATH, so that a program which knows no
    /// long paths still opens it, but never less than
    /// <see cref="WindowsFileNames.MinLength"/>.
    /// </summary>
    public int MaxFileNameLength =>
        Math.Clamp(MaxPath - 1 - (Path.Length + 1 + SubdirectoryNameLength + 1), WindowsFileNames.MinLength, WindowsFileNames.MaxLength);

    /// <summary>
    /// Writes <paramref name="data"/> into a fresh private subdirectory and
    /// returns the file's path (<c>writeOpenFile</c>). Entries older than
    /// <see cref="OpenMaxAge"/> go first. The name goes through
    /// <see cref="WindowsFileNames.Sanitize(string, int, IReadOnlySet{char})"/>
    /// once more, with this machine's look-alikes and cut to
    /// <see cref="MaxFileNameLength"/>: a long name comes out shorter
    /// than the caller's (never with an extension it did not have), so the
    /// name of the returned path, not <paramref name="name"/>, is the one
    /// to judge (<see cref="IFileTypePolicy"/>) and to pass on
    /// (<see cref="IMarkOfTheWeb"/>, <see cref="ILauncher"/>). The
    /// subdirectory is new (<see cref="IPrivateDirectoryFactory.CreateNew"/>)
    /// and the file is created with <see cref="FileMode.CreateNew"/>, so
    /// nothing that was there before is ever reused or overwritten.
    /// </summary>
    public string Write(string name, ReadOnlySpan<byte> data)
    {
        ArgumentNullException.ThrowIfNull(name);
        Sweep(OpenMaxAge);
        directories.Ensure(Path);
        var sub = NewSubdirectory();
        try
        {
            var fileName = WindowsFileNames.Sanitize(name, MaxFileNameLength, lookAlikes);
            var path = System.IO.Path.Combine(sub, fileName);
            using (var stream = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None))
            {
                stream.Write(data);
            }
            return path;
        }
        catch
        {
            TryDelete(sub);
            throw;
        }
    }

    /// <summary>Removes the entries older than <see cref="OpenMaxAge"/>.</summary>
    public void Sweep() => Sweep(OpenMaxAge);

    /// <summary>
    /// Removes the entries older than <paramref name="maxAge"/>
    /// (<c>sweepOpenDir</c>). A missing directory is a no-op, and so is a
    /// link in its place (what it points to is not ours); an entry a viewer
    /// still holds open stays for the next sweep; a link inside is removed
    /// itself, never followed.
    /// </summary>
    public void Sweep(TimeSpan maxAge)
    {
        if (!IsOwnDirectory(Path))
        {
            return;
        }
        var cutoff = time.GetUtcNow().UtcDateTime - maxAge;
        foreach (var entry in Entries(Path))
        {
            // The time the enumeration read, of the entry itself (a link's
            // own, not its target's).
            if (entry.LastWriteTimeUtc < cutoff)
            {
                TryDelete(entry.FullName);
            }
        }
    }

    /// <summary>
    /// Removes every file written for opening (<c>SweepOpenedAttachments</c>);
    /// the application calls it when it starts and when it exits. What a
    /// viewer still holds open stays (and so do the directories above it);
    /// a link in place of the directory is removed itself, and what it
    /// points to is left alone.
    /// </summary>
    public void RemoveAll() => TryDelete(Path);

    private string NewSubdirectory()
    {
        for (var attempt = 1; ; attempt++)
        {
            var sub = System.IO.Path.Combine(Path, RandomNumberGenerator.GetString(SubdirectoryAlphabet, SubdirectoryNameLength));
            try
            {
                directories.CreateNew(sub);
                return sub;
            }
            catch (IOException) when (attempt < SubdirectoryAttempts && System.IO.Path.Exists(sub))
            {
                // Taken: another name, as mkdtemp tries again.
            }
        }
    }

    // Whether path is a directory of its own: there, and not a link.
    private static bool IsOwnDirectory(string path)
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

    // The entries of a directory, or none when it cannot be read.
    private static FileSystemInfo[] Entries(string directory)
    {
        try
        {
            return new DirectoryInfo(directory).GetFileSystemInfos();
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return [];
        }
    }

    // Removes what is at path as far as it can: a link itself (never what
    // it points to), a directory after its entries, a file after its
    // read-only flag. Whatever is held open stays, and so do the
    // directories above it; nothing there is no error.
    private static void TryDelete(string path)
    {
        try
        {
            // Of the entry itself: a link is not followed.
            var attributes = File.GetAttributes(path);
            if ((attributes & FileAttributes.ReparsePoint) != 0)
            {
                DeleteLink(path, attributes);
                return;
            }
            if ((attributes & FileAttributes.Directory) != 0)
            {
                foreach (var child in Entries(path))
                {
                    TryDelete(child.FullName);
                }
                Directory.Delete(path, recursive: false);
                return;
            }
            if ((attributes & FileAttributes.ReadOnly) != 0)
            {
                File.SetAttributes(path, attributes & ~FileAttributes.ReadOnly);
            }
            File.Delete(path);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            // Held open by a viewer, or gone already: the next sweep.
        }
    }

    private static void DeleteLink(string path, FileAttributes attributes)
    {
        if ((attributes & FileAttributes.Directory) != 0)
        {
            try
            {
                // A junction or a directory link: removes the link only.
                Directory.Delete(path, recursive: false);
                return;
            }
            catch (IOException) when (!OperatingSystem.IsWindows())
            {
                // Elsewhere a link to a directory is a file to unlink.
            }
        }
        File.Delete(path);
    }
}
