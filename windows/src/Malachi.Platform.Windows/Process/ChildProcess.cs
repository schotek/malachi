// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: how the daemon's process is created (docs/windows-port.md
// §5), the counterpart of Foundation.Process.run in
// macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift (spawn) and
// exec.Cmd.Start in ui/internal/daemon/daemon.go. Not Process.Start: .NET
// creates every child with bInheritHandles and no list, so any inheritable
// handle of the app goes along (a launcher's pipe kept PowerShell waiting
// for ever in the measured runs, INPUT-SPIKES.md §4). CreateProcess here
// passes PROC_THREAD_ATTRIBUTE_HANDLE_LIST with exactly two handles: NUL as
// stdin (the daemon never reads it; Go's nil Stdin) and the write end of
// one pipe as both stdout and stderr, so the daemon's lines stay in their
// order. The pipe's read end is the parent's and never inheritable. The
// command line follows the rules every Windows C runtime and Go parse it
// by (argv[0] quoted, the other arguments escaped only where they need it),
// and the environment block is sorted as CreateProcess expects. The
// namespace is not Process, which would hide System.Diagnostics.Process in
// every Malachi.Platform.Windows namespace.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Security;
using Windows.Win32.Storage.FileSystem;
using Windows.Win32.System.Threading;

namespace Malachi.Platform.Windows.Processes;

/// <summary>A process started with only the handles it is meant to have.</summary>
internal sealed class ChildProcess : IDisposable
{
    private ChildProcess(SafeProcessHandle handle, int id, SafeFileHandle output)
    {
        Handle = handle;
        Id = id;
        Output = output;
    }

    /// <summary>The process ID.</summary>
    public int Id { get; }

    /// <summary>The process handle (SYNCHRONIZE, exit code, terminate).</summary>
    public SafeProcessHandle Handle { get; }

    /// <summary>The read end of the pipe that is the process's stdout and stderr.</summary>
    public SafeFileHandle Output { get; }

    /// <summary>
    /// Starts <paramref name="executable"/> (a path) with
    /// <paramref name="arguments"/> and exactly <paramref name="environment"/>;
    /// in a new process group (the target of CTRL_BREAK, spared the
    /// terminal's Ctrl+C) when <paramref name="newProcessGroup"/>, without a
    /// console window when <paramref name="noWindow"/>. Throws
    /// <see cref="Win32Exception"/> with the system's reason when it cannot.
    /// </summary>
    public static unsafe ChildProcess Start(
        string executable,
        IReadOnlyList<string> arguments,
        IReadOnlyDictionary<string, string> environment,
        bool newProcessGroup,
        bool noWindow)
    {
        ArgumentException.ThrowIfNullOrEmpty(executable);
        ArgumentNullException.ThrowIfNull(arguments);
        ArgumentNullException.ThrowIfNull(environment);
        var application = Path.GetFullPath(executable);
        var commandLine = (CommandLine(application, arguments) + "\0").ToCharArray();
        var environmentBlock = EnvironmentBlock(environment);

        var inheritable = new SECURITY_ATTRIBUTES
        {
            nLength = (uint)sizeof(SECURITY_ATTRIBUTES),
            bInheritHandle = true,
        };
        using var input = PInvoke.CreateFile(
            "NUL",
            (uint)GENERIC_ACCESS_RIGHTS.GENERIC_READ,
            FILE_SHARE_MODE.FILE_SHARE_READ | FILE_SHARE_MODE.FILE_SHARE_WRITE,
            inheritable,
            FILE_CREATION_DISPOSITION.OPEN_EXISTING,
            0,
            null);
        if (input.IsInvalid)
        {
            throw Failure(executable);
        }
        if (!PInvoke.CreatePipe(out var read, out var write, inheritable, 0))
        {
            throw Failure(executable);
        }
        using (write)
        {
            var started = false;
            try
            {
                if (!PInvoke.SetHandleInformation(read, (uint)HANDLE_FLAGS.HANDLE_FLAG_INHERIT, 0))
                {
                    throw Failure(executable);
                }
                var info = CreateProcess(application, commandLine, environmentBlock, input, write, newProcessGroup, noWindow, executable);
                PInvoke.CloseHandle(info.hThread);
                started = true;
                return new ChildProcess(new SafeProcessHandle(info.hProcess, ownsHandle: true), (int)info.dwProcessId, read);
            }
            finally
            {
                if (!started)
                {
                    read.Dispose();
                }
            }
        }
    }

    /// <summary>
    /// The command line for <paramref name="application"/> and
    /// <paramref name="arguments"/>: argv[0] in quotes (a path holds none),
    /// then each argument as CommandLineToArgvW and the C runtimes read it
    /// back unchanged (quoted when empty or when it holds white space or a
    /// quote; backslashes doubled before a quote).
    /// </summary>
    internal static string CommandLine(string application, IReadOnlyList<string> arguments)
    {
        if (application.Contains('"', StringComparison.Ordinal))
        {
            throw new ArgumentException("a program path holds no quote", nameof(application));
        }
        var line = new StringBuilder();
        line.Append('"').Append(application).Append('"');
        foreach (var argument in arguments)
        {
            line.Append(' ');
            AppendArgument(line, argument);
        }
        return line.ToString();
    }

    /// <summary>
    /// The Unicode environment block of <paramref name="environment"/>:
    /// <c>name=value</c> entries sorted by name without regard to case, each
    /// ending in NUL, and a NUL after the last. Entries CreateProcess cannot
    /// carry (an empty name, "=" inside a name, a NUL) are left out.
    /// </summary>
    internal static string EnvironmentBlock(IReadOnlyDictionary<string, string> environment)
    {
        var block = new StringBuilder();
        foreach (var (name, value) in environment.OrderBy(entry => entry.Key, StringComparer.OrdinalIgnoreCase))
        {
            if (name.Length == 0 || name.IndexOf('=', 1) >= 0 || name.Contains('\0', StringComparison.Ordinal)
                || value.Contains('\0', StringComparison.Ordinal))
            {
                continue;
            }
            block.Append(name).Append('=').Append(value).Append('\0');
        }
        if (block.Length == 0)
        {
            block.Append('\0');
        }
        block.Append('\0');
        return block.ToString();
    }

    /// <inheritdoc/>
    public void Dispose()
    {
        Handle.Dispose();
        Output.Dispose();
    }

    private static unsafe PROCESS_INFORMATION CreateProcess(
        string application,
        char[] commandLine,
        string environmentBlock,
        SafeFileHandle input,
        SafeFileHandle output,
        bool newProcessGroup,
        bool noWindow,
        string shown)
    {
        nuint size = 0;
        PInvoke.InitializeProcThreadAttributeList(default, 1, 0, &size);
        var memory = NativeMemory.Alloc(size);
        var list = new LPPROC_THREAD_ATTRIBUTE_LIST(memory);
        try
        {
            if (!PInvoke.InitializeProcThreadAttributeList(list, 1, 0, &size))
            {
                throw Failure(shown);
            }
            try
            {
                var inherited = stackalloc HANDLE[2];
                inherited[0] = (HANDLE)input.DangerousGetHandle();
                inherited[1] = (HANDLE)output.DangerousGetHandle();
                if (!PInvoke.UpdateProcThreadAttribute(
                    list, 0, PInvoke.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, inherited, (nuint)(2 * sizeof(HANDLE)), null, null))
                {
                    throw Failure(shown);
                }
                var startup = new STARTUPINFOEXW { lpAttributeList = list };
                startup.StartupInfo.cb = (uint)sizeof(STARTUPINFOEXW);
                startup.StartupInfo.dwFlags = STARTUPINFOW_FLAGS.STARTF_USESTDHANDLES;
                startup.StartupInfo.hStdInput = inherited[0];
                startup.StartupInfo.hStdOutput = inherited[1];
                startup.StartupInfo.hStdError = inherited[1];
                var flags = PROCESS_CREATION_FLAGS.CREATE_UNICODE_ENVIRONMENT | PROCESS_CREATION_FLAGS.EXTENDED_STARTUPINFO_PRESENT;
                if (newProcessGroup)
                {
                    flags |= PROCESS_CREATION_FLAGS.CREATE_NEW_PROCESS_GROUP;
                }
                if (noWindow)
                {
                    flags |= PROCESS_CREATION_FLAGS.CREATE_NO_WINDOW;
                }
                PROCESS_INFORMATION info;
                fixed (char* app = application)
                fixed (char* cmd = commandLine)
                fixed (char* env = environmentBlock)
                {
                    if (!PInvoke.CreateProcess(
                        new PCWSTR(app), new PWSTR(cmd), null, null, true, flags, env, default, &startup.StartupInfo, &info))
                    {
                        throw Failure(shown);
                    }
                }
                GC.KeepAlive(input);
                GC.KeepAlive(output);
                return info;
            }
            finally
            {
                PInvoke.DeleteProcThreadAttributeList(list);
            }
        }
        finally
        {
            NativeMemory.Free(memory);
        }
    }

    private static void AppendArgument(StringBuilder line, string argument)
    {
        if (argument.Length > 0 && argument.IndexOfAny([' ', '\t', '\n', '\v', '"']) < 0)
        {
            line.Append(argument);
            return;
        }
        line.Append('"');
        var backslashes = 0;
        foreach (var c in argument)
        {
            if (c == '\\')
            {
                backslashes++;
                continue;
            }
            if (c == '"')
            {
                line.Append('\\', (2 * backslashes) + 1).Append('"');
            }
            else
            {
                line.Append('\\', backslashes).Append(c);
            }
            backslashes = 0;
        }
        line.Append('\\', 2 * backslashes).Append('"');
    }

    private static Win32Exception Failure(string shown)
    {
        var error = Marshal.GetLastPInvokeError();
        return new Win32Exception(error, "start " + shown + ": " + new Win32Exception(error).Message);
    }
}
