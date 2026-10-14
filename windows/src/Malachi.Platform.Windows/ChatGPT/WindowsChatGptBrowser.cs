// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC browser callback, no existing Swift/Go counterpart.
// A socket is reserved before shell launch; HTTP.sys URL reservations are not needed.

using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.ChatGPT;
using Malachi.Platform.Windows.Launch;

namespace Malachi.Platform.Windows.ChatGPT;

public sealed class WindowsChatGptBrowser : IChatGptBrowser
{
    private readonly Func<Uri, CancellationToken, Task> launch;

    public WindowsChatGptBrowser() : this(LaunchAsync) { }
    internal WindowsChatGptBrowser(Func<Uri, CancellationToken, Task> launch) => this.launch = launch;

    public async Task<Uri> AuthorizeAsync(Func<Uri, Uri> authorizationUrl, string state, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(authorizationUrl);
        using var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Server.ExclusiveAddressUse = true;
        listener.Start(4);
        var port = ((IPEndPoint)listener.LocalEndpoint).Port;
        var redirect = new Uri($"http://127.0.0.1:{port}{ChatGptOAuth.CallbackPath}");
        await launch(authorizationUrl(redirect), cancellationToken).ConfigureAwait(false);
        while (true)
        {
            using var client = await listener.AcceptTcpClientAsync(cancellationToken).ConfigureAwait(false);
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(TimeSpan.FromSeconds(3));
            await using var stream = client.GetStream();
            try
            {
                var target = await RequestTargetAsync(stream, port, timeout.Token).ConfigureAwait(false);
                Uri? callback = null;
                if (target is not null && Uri.TryCreate(redirect, target, out var parsed))
                {
                    try { ChatGptOAuth.ParseCallback(parsed, redirect, state); callback = parsed; }
                    catch (ChatGptAuthException) { }
                }
                await ReplyAsync(stream, callback is not null, timeout.Token).ConfigureAwait(false);
                if (callback is not null) return callback;
            }
            catch (Exception e) when (e is IOException or OperationCanceledException)
            { cancellationToken.ThrowIfCancellationRequested(); }
        }
    }

    private static async Task<string?> RequestTargetAsync(NetworkStream stream, int port, CancellationToken ct)
    {
        var bytes = new byte[16 * 1024];
        var length = 0;
        while (length < bytes.Length)
        {
            var count = await stream.ReadAsync(bytes.AsMemory(length, 1), ct).ConfigureAwait(false);
            if (count == 0) return null;
            length += count;
            if (length >= 4 && bytes[length - 4] == 13 && bytes[length - 3] == 10 && bytes[length - 2] == 13 && bytes[length - 1] == 10) break;
        }
        if (length == bytes.Length) return null;
        var headers = Encoding.ASCII.GetString(bytes, 0, length).Split("\r\n", StringSplitOptions.None);
        var first = headers[0].Split(' ');
        if (first.Length != 3 || first[0] != "GET" || first[2] != "HTTP/1.1"
            || !first[1].StartsWith(ChatGptOAuth.CallbackPath + "?", StringComparison.Ordinal)) return null;
        var hostCount = 0;
        foreach (var header in headers.AsSpan(1))
        {
            if (header.StartsWith("Host:", StringComparison.OrdinalIgnoreCase))
            {
                if (header[5..].Trim() != "127.0.0.1:" + port) return null;
                hostCount++;
            }
            if (header.StartsWith("Transfer-Encoding:", StringComparison.OrdinalIgnoreCase)) return null;
        }
        return hostCount == 1 ? first[1] : null;
    }

    private static async Task ReplyAsync(NetworkStream stream, bool accepted, CancellationToken ct)
    {
        // Windows-only string: the ephemeral browser response has no widgets.
        var body = accepted ? "Return to Malachi Mail to finish signing in." : "Invalid sign-in callback.";
        var bytes = Encoding.ASCII.GetBytes($"HTTP/1.1 {(accepted ? "200 OK" : "400 Bad Request")}\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: {body.Length}\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer\r\nContent-Security-Policy: default-src 'none'\r\nConnection: close\r\n\r\n{body}");
        await stream.WriteAsync(bytes, ct).ConfigureAwait(false);
    }

    private static async Task LaunchAsync(Uri uri, CancellationToken ct)
    {
        if (uri.Scheme != "https" || uri.Host != "auth.openai.com" || uri.AbsolutePath != "/api/accounts/authorize")
            throw new ChatGptAuthException(ChatGptAuthError.Browser);
        try
        {
            await StaThread.RunAsync(() =>
            {
                using var process = Process.Start(new ProcessStartInfo(uri.AbsoluteUri) { UseShellExecute = true, ErrorDialog = false });
                return true;
            }, ct).ConfigureAwait(false);
        }
        catch (Win32Exception) { throw new ChatGptAuthException(ChatGptAuthError.Browser); }
    }
}
