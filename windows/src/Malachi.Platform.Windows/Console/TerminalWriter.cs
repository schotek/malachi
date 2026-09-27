// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the terminal side of the log of make run-windows
// (docs/windows-port.md §5 Console; the stderr that ui/main.go and the
// macOS app share with the daemon). Two kinds of target: the console the
// app attached to (CONOUT$), written with WriteConsoleW so that any text
// shows whatever the console's code page (UTF-8 bytes would be read in that
// code page), or a pipe or file a launcher handed the app as its stderr,
// written as UTF-8 with WriteFile on that handle, at its own file pointer
// (a file the launcher shares between stdout and stderr is appended to, not
// overwritten from the start). Every write holds the process-wide console
// gate, so no line is written while the daemon's stop moves the process
// between consoles, and a write that fails (the terminal is gone) is
// dropped.

using System;
using System.IO;
using System.Text;
using Microsoft.Win32.SafeHandles;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Storage.FileSystem;

namespace Malachi.Platform.Windows.Consoles;

/// <summary>A thread-safe writer to the terminal the app was started from.</summary>
public sealed class TerminalWriter : TextWriter
{
    private static readonly UTF8Encoding Utf8 = new(encoderShouldEmitUTF8Identifier: false);

    private SafeFileHandle? handle;
    private bool console;

    private TerminalWriter(SafeFileHandle handle, bool console)
    {
        this.handle = handle;
        this.console = console;
    }

    /// <inheritdoc/>
    public override Encoding Encoding => Utf8;

    /// <summary>Whether it writes to a console (CONOUT$) rather than a pipe or file.</summary>
    public bool IsConsole
    {
        get
        {
            lock (ConsoleAttachment.Gate)
            {
                return console;
            }
        }
    }

    /// <inheritdoc/>
    public override void Write(char value) => Write(value.ToString());

    /// <inheritdoc/>
    public override void Write(char[] buffer, int index, int count) => Write(new string(buffer, index, count));

    /// <inheritdoc/>
    public override void Write(string? value)
    {
        if (string.IsNullOrEmpty(value))
        {
            return;
        }
        lock (ConsoleAttachment.Gate)
        {
            if (handle is null || handle.IsClosed)
            {
                return;
            }
            if (console)
            {
                WriteConsole(handle, value);
            }
            else
            {
                WriteFile(handle, Utf8.GetBytes(value));
            }
        }
    }

    /// <inheritdoc/>
    public override void WriteLine(string? value) => Write(value + NewLine);

    /// <summary>The console's output buffer of the console this process is attached to, or null without one.</summary>
    internal static TerminalWriter? OpenConsoleOutput()
    {
        var output = OpenConout();
        return output is null ? null : new TerminalWriter(output, console: true);
    }

    /// <summary>A pipe or file this process got as a standard handle; the handle stays the process's.</summary>
    internal static TerminalWriter ForInheritedHandle(nint handle) =>
        new(new SafeFileHandle(handle, ownsHandle: false), console: false);

    /// <summary>
    /// After the process left its console and came back (the daemon's
    /// stop), a fresh CONOUT$ of the console it is attached to now. Called
    /// with the console gate held.
    /// </summary>
    internal void ReopenConsoleOutput()
    {
        if (!console)
        {
            return;
        }
        var output = OpenConout();
        if (output is null)
        {
            return;
        }
        handle?.Dispose();
        handle = output;
    }

    /// <inheritdoc/>
    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            lock (ConsoleAttachment.Gate)
            {
                handle?.Dispose();
                handle = null;
            }
        }
        base.Dispose(disposing);
    }

    private static SafeFileHandle? OpenConout()
    {
        var output = PInvoke.CreateFile(
            "CONOUT$",
            (uint)(GENERIC_ACCESS_RIGHTS.GENERIC_READ | GENERIC_ACCESS_RIGHTS.GENERIC_WRITE),
            FILE_SHARE_MODE.FILE_SHARE_READ | FILE_SHARE_MODE.FILE_SHARE_WRITE,
            null,
            FILE_CREATION_DISPOSITION.OPEN_EXISTING,
            0,
            null);
        if (output.IsInvalid)
        {
            output.Dispose();
            return null;
        }
        return output;
    }

    private static void WriteConsole(SafeFileHandle target, string value)
    {
        var rest = value.AsSpan();
        while (!rest.IsEmpty)
        {
            var chunk = rest[..Math.Min(rest.Length, 8192)].ToString();
            if (!PInvoke.WriteConsole(target, chunk, (uint)chunk.Length, out var written) || written == 0)
            {
                return;
            }
            rest = rest[(int)written..];
        }
    }

    private static unsafe void WriteFile(SafeFileHandle target, byte[] bytes)
    {
        var added = false;
        target.DangerousAddRef(ref added);
        try
        {
            fixed (byte* start = bytes)
            {
                var offset = 0;
                while (offset < bytes.Length)
                {
                    uint written;
                    if (!PInvoke.WriteFile((HANDLE)target.DangerousGetHandle(), start + offset, (uint)(bytes.Length - offset), &written, null)
                        || written == 0)
                    {
                        return;
                    }
                    offset += (int)written;
                }
            }
        }
        finally
        {
            if (added)
            {
                target.DangerousRelease();
            }
        }
    }
}
