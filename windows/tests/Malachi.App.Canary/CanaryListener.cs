// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A loopback listener per vector (SPIKES.md §2c, CanaryTests.cs Canary): any
// connection to it is a leak of that vector, attributed by its port even when
// no request line follows (a preconnect). It answers 404 and records the
// first line of what it got.

using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.App.Canary;

/// <summary>A TCP listener on 127.0.0.1 that records every connection.</summary>
internal sealed class CanaryListener : IDisposable
{
    private readonly TcpListener listener = new(IPAddress.Loopback, 0);
    private readonly List<string> hits = [];
    private readonly CancellationTokenSource stop = new();

    /// <summary>Starts listening on a free port.</summary>
    public CanaryListener(string vector)
    {
        Vector = vector;
        listener.Start();
        Port = ((IPEndPoint)listener.LocalEndpoint).Port;
        _ = AcceptAsync();
    }

    /// <summary>The vector this listener stands for.</summary>
    public string Vector { get; }

    /// <summary>Its port.</summary>
    public int Port { get; }

    /// <summary>Its origin, <c>http://127.0.0.1:port</c>.</summary>
    public string Origin => "http://127.0.0.1:" + Port;

    /// <summary>What reached it: "connect" and the request line of each connection.</summary>
    public IReadOnlyList<string> Hits
    {
        get
        {
            lock (hits)
            {
                return [.. hits];
            }
        }
    }

    public void Dispose()
    {
        stop.Cancel();
        listener.Stop();
        stop.Dispose();
    }

    private async Task AcceptAsync()
    {
        while (!stop.IsCancellationRequested)
        {
            TcpClient client;
            try
            {
                client = await listener.AcceptTcpClientAsync(stop.Token);
            }
            catch (Exception e) when (e is OperationCanceledException or SocketException or ObjectDisposedException)
            {
                return;
            }
            _ = HandleAsync(client);
        }
    }

    private async Task HandleAsync(TcpClient client)
    {
        Add("connect");
        try
        {
            using (client)
            {
                var stream = client.GetStream();
                var buffer = new byte[8192];
                var n = 0;
                using var timeout = new CancellationTokenSource(2000);
                try
                {
                    while (n < buffer.Length)
                    {
                        var r = await stream.ReadAsync(buffer.AsMemory(n), timeout.Token);
                        if (r == 0)
                        {
                            break;
                        }
                        n += r;
                        if (Encoding.ASCII.GetString(buffer, 0, n).Contains("\r\n\r\n", StringComparison.Ordinal))
                        {
                            break;
                        }
                    }
                }
                catch (OperationCanceledException)
                {
                }
                if (n > 0)
                {
                    Add(Encoding.ASCII.GetString(buffer, 0, n).Split("\r\n")[0]);
                    await stream.WriteAsync("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"u8.ToArray());
                }
            }
        }
        catch (Exception e) when (e is SocketException or System.IO.IOException or ObjectDisposedException)
        {
            Add("error " + e.GetType().Name);
        }
    }

    private void Add(string hit)
    {
        lock (hits)
        {
            hits.Add(hit);
        }
    }
}
