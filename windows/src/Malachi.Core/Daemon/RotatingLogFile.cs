// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: where the daemon's output goes. GTK and macOS hand the
// daemon their own stderr (ui/internal/daemon/daemon.go spawn,
// DaemonSupervisor.swift spawn), which reaches the terminal or the journal;
// a WinUI app started from the Start menu has neither, so the lines are
// kept in %LOCALAPPDATA%\Malachi Mail\logs (docs/windows-port.md §1, §5)
// and, when the app is attached to a terminal, shown there too. The file is
// bounded: past MaxBytes it becomes name.1, the older ones move up, and the
// oldest beyond Keep goes. It is opened for appending with read, write and
// delete sharing, so it can be followed while it grows and rotated while it
// is being read. A reader that holds a file without delete sharing (Windows
// then refuses to rename or delete it) never costs a line or the newest
// logs: the current file is moved out of the way before anything else is
// touched, so a reader of it stops the rotation at the start and the file
// is appended to until a later try, after another eighth of MaxBytes, goes
// through; an older file that is held stays, and the newer one that cannot
// move up in its place goes instead. A line that cannot be written is
// dropped: the log never stops the daemon's output from being read.
//
// Several processes may append to one file (the app's log is also opened by
// a second launch that hands its activation to the first and exits): every
// line is written at the file's end as it is at that moment, never at the
// position this handle last wrote to, which another process's lines may
// have passed (FileMode.Append only seeks to the end once, when the file is
// opened). Two processes writing in the same instant could still meet
// between the seek and the write; the second launch writes a line or two.

using System;
using System.IO;
using System.Text;
using System.Threading;

namespace Malachi.Core.Daemon;

/// <summary>A size-bounded log file with numbered predecessors; safe for use from any thread.</summary>
public sealed class RotatingLogFile : IDisposable
{
    /// <summary>The size past which the file is rotated.</summary>
    public const long DefaultMaxBytes = 4L << 20;

    /// <summary>How many rotated files are kept.</summary>
    public const int DefaultKeep = 2;

    private static readonly UTF8Encoding Utf8 = new(encoderShouldEmitUTF8Identifier: false);

    private readonly Lock gate = new();
    private FileStream? stream;
    private bool disposed;

    // After a rotation that could not be made: the size the file must pass
    // before the next try (0 otherwise).
    private long retryAt;

    /// <summary>A log at <paramref name="path"/>, which is created (with its directory) on the first line.</summary>
    public RotatingLogFile(string path, long maxBytes = DefaultMaxBytes, int keep = DefaultKeep)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        ArgumentOutOfRangeException.ThrowIfLessThan(maxBytes, 1);
        ArgumentOutOfRangeException.ThrowIfNegative(keep);
        FilePath = Path.GetFullPath(path);
        MaxBytes = maxBytes;
        Keep = keep;
    }

    /// <summary>The current file.</summary>
    public string FilePath { get; }

    /// <summary>The size past which the file is rotated.</summary>
    public long MaxBytes { get; }

    /// <summary>How many rotated files (<c>name.1</c> to <c>name.N</c>) are kept.</summary>
    public int Keep { get; }

    /// <summary>
    /// Appends <paramref name="line"/> and a line break; false when it could
    /// not be written (it is dropped).
    /// </summary>
    public bool WriteLine(string line)
    {
        ArgumentNullException.ThrowIfNull(line);
        var bytes = Utf8.GetBytes(line + Environment.NewLine);
        lock (gate)
        {
            if (disposed)
            {
                return false;
            }
            try
            {
                var file = Open();
                // At the end, past what other processes appended meanwhile.
                var length = file.Seek(0, SeekOrigin.End);
                if (length > 0 && length + bytes.Length > Math.Max(MaxBytes, retryAt))
                {
                    file = Rotate(length);
                }
                file.Write(bytes);
                file.Flush();
                return true;
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                stream?.Dispose();
                stream = null;
                return false;
            }
        }
    }

    /// <summary>Closes the file; later lines are dropped.</summary>
    public void Dispose()
    {
        lock (gate)
        {
            disposed = true;
            stream?.Dispose();
            stream = null;
        }
    }

    private FileStream Open()
    {
        if (stream is not null)
        {
            return stream;
        }
        Directory.CreateDirectory(Path.GetDirectoryName(FilePath)!);
        stream = new FileStream(
            FilePath,
            new FileStreamOptions
            {
                Mode = FileMode.Append,
                Access = FileAccess.Write,
                Share = FileShare.ReadWrite | FileShare.Delete,
            });
        return stream;
    }

    // Starts a new file, or, when the current one cannot be moved out of the
    // way, goes on with it and tries again later. Returns the file to write
    // to.
    private FileStream Rotate(long length)
    {
        stream?.Dispose();
        stream = null;
        if (MoveOut())
        {
            retryAt = 0;
        }
        else
        {
            retryAt = length + Math.Max(1, MaxBytes / 8);
        }
        return Open();
    }

    // name → name.1 after name.1 → name.2, ..., name.(Keep-1) → name.Keep;
    // with Keep 0 the file just starts again. The current file goes first,
    // to a name of its own: when that fails nothing has changed. False when
    // the current file is still where it was.
    private bool MoveOut()
    {
        if (Keep == 0)
        {
            return TryDelete(FilePath);
        }
        var staged = FilePath + ".rotating";
        if (!TryMove(FilePath, staged, overwrite: true))
        {
            return false;
        }
        if (Vacate(1) && TryMove(staged, Numbered(1), overwrite: false))
        {
            return true;
        }
        // name.1 is held: the current file comes back and grows on (should
        // even that fail, it stays as name.rotating and a new one starts).
        return !TryMove(staged, FilePath, overwrite: false);
    }

    // Frees name.n: moves it up (after freeing the place above it) or, when
    // it cannot move, deletes it; name.Keep is only deleted. False when
    // name.n stays (it is held).
    private bool Vacate(int n)
    {
        var path = Numbered(n);
        if (!File.Exists(path))
        {
            return true;
        }
        if (n < Keep && Vacate(n + 1) && TryMove(path, Numbered(n + 1), overwrite: false))
        {
            return true;
        }
        return TryDelete(path);
    }

    private static bool TryMove(string from, string to, bool overwrite)
    {
        try
        {
            File.Move(from, to, overwrite);
            return true;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    private static bool TryDelete(string path)
    {
        try
        {
            File.Delete(path);
            return true;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    private string Numbered(int n) => FilePath + "." + n.ToString(System.Globalization.CultureInfo.InvariantCulture);
}
