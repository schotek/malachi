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
// is being read. A line that cannot be written is dropped: the log never
// stops the daemon's output from being read.

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
                if (file.Length > 0 && file.Length + bytes.Length > MaxBytes)
                {
                    Rotate();
                    file = Open();
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

    // name.(Keep-1) → name.Keep, ..., name → name.1; with Keep 0 the file
    // just starts again.
    private void Rotate()
    {
        stream?.Dispose();
        stream = null;
        if (Keep == 0)
        {
            File.Delete(FilePath);
            return;
        }
        File.Delete(Numbered(Keep));
        for (var i = Keep - 1; i >= 1; i--)
        {
            if (File.Exists(Numbered(i)))
            {
                File.Move(Numbered(i), Numbered(i + 1), overwrite: true);
            }
        }
        File.Move(FilePath, Numbered(1), overwrite: true);
    }

    private string Numbered(int n) => FilePath + "." + n.ToString(System.Globalization.CultureInfo.InvariantCulture);
}
