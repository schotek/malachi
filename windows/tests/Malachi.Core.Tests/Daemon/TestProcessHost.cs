// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A portable process host for the supervisor tests, which run on any OS:
// System.Diagnostics.Process, and the end of stdin as the stop request
// (.NET sends no signal; the stand-in daemon treats stdin's end as one when
// TestDaemonSettings.StopOnEofEnv is set). The Windows host, CTRL_BREAK
// and all, is tested in Malachi.Platform.Windows.Tests.

using System.Collections.Concurrent;
using System.ComponentModel;
using System.Diagnostics;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.TestDaemon;

namespace Malachi.Core.Tests.Daemon;

/// <summary>Starts processes and keeps what they print.</summary>
internal sealed class TestProcessHost : IDaemonProcessHost
{
    /// <summary>Every line the processes printed, stdout and stderr.</summary>
    public ConcurrentQueue<string> Lines { get; } = new();

    /// <summary>What each start was asked for.</summary>
    public ConcurrentQueue<DaemonStartInfo> Starts { get; } = new();

    public IDaemonProcess Start(DaemonStartInfo startInfo)
    {
        Starts.Enqueue(startInfo);
        var start = new ProcessStartInfo(startInfo.Executable)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardOutputEncoding = new UTF8Encoding(false),
            StandardErrorEncoding = new UTF8Encoding(false),
        };
        foreach (var argument in startInfo.Arguments)
        {
            start.ArgumentList.Add(argument);
        }
        start.Environment.Clear();
        foreach (var (name, value) in startInfo.Environment)
        {
            start.Environment[name] = value;
        }
        start.Environment[TestDaemonSettings.StopOnEofEnv] = "1";
        var process = Process.Start(start) ?? throw new Win32Exception("no process");
        process.OutputDataReceived += (_, e) =>
        {
            if (e.Data is not null)
            {
                Lines.Enqueue(e.Data);
            }
        };
        process.ErrorDataReceived += (_, e) =>
        {
            if (e.Data is not null)
            {
                Lines.Enqueue(e.Data);
            }
        };
        process.BeginOutputReadLine();
        process.BeginErrorReadLine();
        return new TestProcess(process);
    }

    private sealed class TestProcess : IDaemonProcess
    {
        private readonly Process process;

        public TestProcess(Process process)
        {
            this.process = process;
            Id = process.Id;
            Exited = WaitAsync();
        }

        public int Id { get; }

        public Task<int> Exited { get; }

        public bool RequestStop()
        {
            try
            {
                process.StandardInput.Close();
                return true;
            }
            catch (System.IO.IOException)
            {
                return false;
            }
        }

        public void Kill()
        {
            try
            {
                process.Kill();
            }
            catch (System.InvalidOperationException)
            {
                // Gone already.
            }
        }

        public void Dispose() => process.Dispose();

        private async Task<int> WaitAsync()
        {
            await process.WaitForExitAsync().ConfigureAwait(false);
            return process.ExitCode;
        }
    }
}
