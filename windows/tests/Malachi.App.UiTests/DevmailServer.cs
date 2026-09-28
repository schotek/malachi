// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// devmail, the local IMAP and SMTP test server (a folder outside the
// repository, named by MALACHI_DEVMAIL; its README describes it), started
// for the opt-in suite on ports of its own on 127.0.0.1, so it never meets
// another devmail on the default ones: in memory, seeded with the mailbox
// of test@example.test (password "test") and the MIME corpus of
// backend/testdata/mime, stopped with the suite. Its control endpoint
// answers how many messages each folder holds.

using System;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Text.Json;
using System.Threading;

namespace Malachi.App.UiTests;

/// <summary>A devmail run.</summary>
internal sealed class DevmailServer : IDisposable
{
    /// <summary>The folder with devmail.exe.</summary>
    public const string FolderEnv = "MALACHI_DEVMAIL";

    /// <summary>The seeded user.</summary>
    public const string User = "test@example.test";

    /// <summary>Its password.</summary>
    public const string Password = "test";

    private readonly Process process;
    private readonly HttpClient http = new() { Timeout = TimeSpan.FromSeconds(10) };

    private DevmailServer(Process process, int imap, int smtp, int control)
    {
        this.process = process;
        ImapPort = imap;
        SmtpPort = smtp;
        ControlPort = control;
    }

    /// <summary>IMAP, plaintext.</summary>
    public int ImapPort { get; }

    /// <summary>SMTP submission, plaintext, AUTH required.</summary>
    public int SmtpPort { get; }

    /// <summary>The HTTP control endpoint.</summary>
    public int ControlPort { get; }

    /// <summary>devmail.exe in MALACHI_DEVMAIL; null when the variable is not set.</summary>
    public static string? Executable =>
        Environment.GetEnvironmentVariable(FolderEnv) is { Length: > 0 } folder ? Path.Combine(folder, "devmail.exe") : null;

    /// <summary>Starts devmail on free ports, seeded with <paramref name="corpus"/>, and waits until IMAP answers.</summary>
    public static DevmailServer Start(string corpus)
    {
        var exe = Executable ?? throw new InvalidOperationException(FolderEnv + " is not set");
        var (imap, smtp, control) = (FreePort(), FreePort(), FreePort());
        var start = new ProcessStartInfo(exe)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            WorkingDirectory = Path.GetDirectoryName(exe)!,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var argument in new[]
        {
            "-imap-port", imap.ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-smtp-port", smtp.ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-control-port", control.ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-corpus", corpus,
        })
        {
            start.ArgumentList.Add(argument);
        }
        var process = Process.Start(start) ?? throw new InvalidOperationException("devmail did not start");
        // Drained, never read: its log is for a person.
        process.OutputDataReceived += (_, _) => { };
        process.ErrorDataReceived += (_, _) => { };
        process.BeginOutputReadLine();
        process.BeginErrorReadLine();
        var server = new DevmailServer(process, imap, smtp, control);
        try
        {
            Uia.WaitFor(() => Listening(imap) && Listening(smtp), "devmail to listen", TimeSpan.FromSeconds(30));
        }
        catch
        {
            server.Dispose();
            throw;
        }
        return server;
    }

    /// <summary>How many messages <paramref name="folder"/> of the seeded user holds on the server.</summary>
    public int Total(string folder)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, new Uri($"http://127.0.0.1:{ControlPort}/status"));
        request.Headers.Add("X-Devmail", "1");
        using var response = http.Send(request);
        response.EnsureSuccessStatusCode();
        using var body = response.Content.ReadAsStream();
        using var status = JsonDocument.Parse(body);
        var user = status.RootElement.GetProperty("users").EnumerateArray()
            .First(u => string.Equals(u.GetProperty("user").GetString(), User, StringComparison.OrdinalIgnoreCase));
        return user.GetProperty("folders").EnumerateArray()
            .First(f => f.GetProperty("folder").GetString() == folder)
            .GetProperty("total").GetInt32();
    }

    /// <summary>Stops devmail (everything it held is gone with it).</summary>
    public void Dispose()
    {
        http.Dispose();
        try
        {
            if (!process.HasExited)
            {
                process.Kill();
                process.WaitForExit(5000);
            }
        }
        catch (InvalidOperationException)
        {
            // Exited meanwhile.
        }
        process.Dispose();
    }

    private static int FreePort()
    {
        using var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        return ((IPEndPoint)listener.LocalEndpoint).Port;
    }

    private static bool Listening(int port)
    {
        try
        {
            using var client = new TcpClient();
            client.Connect(IPAddress.Loopback, port);
            return true;
        }
        catch (SocketException)
        {
            Thread.Sleep(100);
            return false;
        }
    }
}
