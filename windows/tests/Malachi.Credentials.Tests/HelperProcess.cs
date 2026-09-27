// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only: runs malachi-credentials.exe as the daemon does (Go's
// os/exec): no window, stdin, stdout and stderr as pipes. The build puts
// the helper beside the tests; MALACHI_CREDENTIALS_EXE names another build,
// such as the NativeAOT one that build.ps1 app publishes.

using System;
using System.Diagnostics;
using System.IO;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Credentials.Tests;

/// <summary>One run of the helper as a process.</summary>
internal sealed record HelperProcess(int Exit, byte[] Stdout, byte[] Stderr)
{
    /// <summary>The helper under test.</summary>
    public static string Path
    {
        get
        {
            var fromEnvironment = Environment.GetEnvironmentVariable("MALACHI_CREDENTIALS_EXE");
            return string.IsNullOrEmpty(fromEnvironment)
                ? System.IO.Path.Combine(AppContext.BaseDirectory, "malachi-credentials.exe")
                : fromEnvironment;
        }
    }

    /// <summary>Runs the helper with the arguments and stdin, and waits for it (at most 30 s).</summary>
    public static async Task<HelperProcess> RunAsync(string[] args, byte[] stdin, CancellationToken cancellationToken)
    {
        var start = new ProcessStartInfo(Path)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var arg in args)
        {
            start.ArgumentList.Add(arg);
        }
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(TimeSpan.FromSeconds(30));
        using var process = Process.Start(start) ?? throw new InvalidOperationException("the helper did not start");
        try
        {
            var stdout = ReadAllAsync(process.StandardOutput.BaseStream, timeout.Token);
            var stderr = ReadAllAsync(process.StandardError.BaseStream, timeout.Token);
            try
            {
                await process.StandardInput.BaseStream.WriteAsync(stdin, timeout.Token);
            }
            catch (IOException)
            {
                // It stopped reading: after the first byte too many, or at once
                // for a usage error.
            }
            try
            {
                process.StandardInput.Close();
            }
            catch (IOException)
            {
                // The same pipe, already broken.
            }
            await process.WaitForExitAsync(timeout.Token);
            return new HelperProcess(process.ExitCode, await stdout, await stderr);
        }
        finally
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
            }
        }
    }

    private static async Task<byte[]> ReadAllAsync(Stream stream, CancellationToken cancellationToken)
    {
        using var buffer = new MemoryStream();
        await stream.CopyToAsync(buffer, cancellationToken);
        return buffer.ToArray();
    }
}
