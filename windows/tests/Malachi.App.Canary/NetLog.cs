// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A Chromium NetLog (--log-net-log, --net-log-capture-mode=Everything) read
// as spikes/netlog.ps1 read it: event types by name from the log's
// constants, events grouped by source. It is the only reliable view of DNS:
// WebView2's own resolver never shows in the Windows DNS client cache
// (SPIKES.md §0). A log the browser did not finish (killed) lacks its
// closing brackets and is read up to its last complete event.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;

namespace Malachi.App.Canary;

/// <summary>What a NetLog says about lookups and connections.</summary>
internal sealed class NetLog
{
    // Resolver work that asks a real resolver: the system's (getaddrinfo),
    // the built-in DNS client, its transactions, multicast DNS.
    private static readonly string[] LookupEvents =
    [
        "HOST_RESOLVER_MANAGER_JOB", "HOST_RESOLVER_SYSTEM_TASK", "HOST_RESOLVER_DNS_TASK", "DNS_TRANSACTION",
        "HOST_RESOLVER_MDNS_TASK", "DNS_TRANSACTION_QUERY", "DNS_TRANSACTION_ATTEMPT",
    ];

    private static readonly string[] TcpConnectEvents = ["TCP_CONNECT", "TCP_CONNECT_ATTEMPT"];

    private NetLog(int eventCount, IReadOnlyList<string> lookups, IReadOnlyList<string> requestedHosts,
        IReadOnlyList<string> tcpConnects, IReadOnlyList<string> udpConnects, IReadOnlyList<string> urlRequests)
    {
        EventCount = eventCount;
        Lookups = lookups;
        RequestedHosts = requestedHosts;
        TcpConnects = tcpConnects;
        UdpConnects = udpConnects;
        UrlRequests = urlRequests;
    }

    /// <summary>How many events the log holds.</summary>
    public int EventCount { get; }

    /// <summary>Resolver jobs and tasks that asked a resolver (event name and host when known).</summary>
    public IReadOnlyList<string> Lookups { get; }

    /// <summary>The host of every resolver request (a mapped one reads <c>~notfound</c>).</summary>
    public IReadOnlyList<string> RequestedHosts { get; }

    /// <summary>TCP connection attempts, with their addresses.</summary>
    public IReadOnlyList<string> TcpConnects { get; }

    /// <summary>UDP socket connects, with their address and error ("ok" when none).</summary>
    public IReadOnlyList<string> UdpConnects { get; }

    /// <summary>The URL of every URL request the network stack started.</summary>
    public IReadOnlyList<string> UrlRequests { get; }

    /// <summary>Reads the log at <paramref name="path"/>.</summary>
    public static NetLog Read(string path)
    {
        var text = File.ReadAllText(path).TrimEnd();
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(text);
        }
        catch (JsonException)
        {
            // An unfinished log: "...,{event},\n" without "]}".
            document = JsonDocument.Parse(text.TrimEnd(',') + "]}");
        }
        using (document)
        {
            var root = document.RootElement;
            var names = new Dictionary<int, string>();
            foreach (var p in root.GetProperty("constants").GetProperty("logEventTypes").EnumerateObject())
            {
                names[p.Value.GetInt32()] = p.Name;
            }
            var sourceNames = new Dictionary<int, string>();
            foreach (var p in root.GetProperty("constants").GetProperty("logSourceType").EnumerateObject())
            {
                sourceNames[p.Value.GetInt32()] = p.Name;
            }
            var lookups = new List<string>();
            var hosts = new List<string>();
            var tcp = new List<string>();
            var udp = new List<string>();
            var urls = new List<string>();
            var udpAddress = new Dictionary<long, string>();
            var count = 0;
            foreach (var e in root.GetProperty("events").EnumerateArray())
            {
                count++;
                var name = names.GetValueOrDefault(e.GetProperty("type").GetInt32(), "?");
                var source = e.GetProperty("source");
                var sourceType = sourceNames.GetValueOrDefault(source.GetProperty("type").GetInt32(), "?");
                var sourceId = source.GetProperty("id").GetInt64();
                var parameters = e.TryGetProperty("params", out var p) ? p : default;
                string? Param(string key) =>
                    parameters.ValueKind == JsonValueKind.Object && parameters.TryGetProperty(key, out var v)
                        ? (v.ValueKind == JsonValueKind.String ? v.GetString() : v.GetRawText())
                        : null;
                if (LookupEvents.Contains(name))
                {
                    lookups.Add(name + " " + (Param("host") ?? Param("hostname") ?? ""));
                }
                else if (name == "HOST_RESOLVER_MANAGER_REQUEST" && Param("host") is { } host)
                {
                    hosts.Add(host);
                }
                else if (TcpConnectEvents.Contains(name) && (Param("address") ?? Param("address_list")) is { } address)
                {
                    tcp.Add(name + " " + address);
                }
                else if (name == "SOCKET_CONNECT" && sourceType.StartsWith("TCP", StringComparison.Ordinal))
                {
                    tcp.Add(name + " " + (Param("address") ?? ""));
                }
                else if ((name == "UDP_CONNECT" || name == "SOCKET_CONNECT") && sourceType.StartsWith("UDP", StringComparison.Ordinal))
                {
                    // UDP_CONNECT begins with the address and ends with the
                    // result; SOCKET_CONNECT carries both at once.
                    if (Param("address") is { } a)
                    {
                        udpAddress[sourceId] = a;
                    }
                    var phase = e.TryGetProperty("phase", out var ph) ? ph.GetInt32() : 0;
                    if (name == "SOCKET_CONNECT" || phase == 2)
                    {
                        udp.Add(udpAddress.GetValueOrDefault(sourceId, "?") + " " + (Param("net_error") ?? "ok"));
                    }
                }
                else if (name == "URL_REQUEST_START_JOB" && Param("url") is { } url)
                {
                    urls.Add(url);
                }
            }
            return new NetLog(count, lookups, hosts, tcp, udp, urls);
        }
    }
}
