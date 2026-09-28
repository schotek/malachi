// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A Chromium NetLog (--log-net-log, --net-log-capture-mode=Everything) read
// as spikes/netlog.ps1 read it: event types by name from the log's
// constants, events grouped by source. It is the only reliable view of DNS:
// WebView2's own resolver never shows in the Windows DNS client cache
// (SPIKES.md §0). A log the browser did not finish (killed) lacks its
// closing brackets and is read up to its last complete event.
//
// The canary must not pass for want of evidence after a runtime bump, so
// every event and source type the reader relies on must be among the log's
// constants: a renamed one fails the read instead of never matching.

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
    // the built-in DNS client and its transactions.
    private static readonly string[] LookupEvents =
    [
        "HOST_RESOLVER_MANAGER_JOB", "HOST_RESOLVER_SYSTEM_TASK", "HOST_RESOLVER_DNS_TASK", "DNS_TRANSACTION",
        "DNS_TRANSACTION_QUERY", "DNS_TRANSACTION_ATTEMPT",
    ];

    // Multicast DNS: counted when the runtime still has it (153 has not).
    private const string MdnsEvent = "HOST_RESOLVER_MDNS_TASK";

    private const string ResolverRequest = "HOST_RESOLVER_MANAGER_REQUEST";
    private const string SocketConnect = "SOCKET_CONNECT";
    private const string UdpConnect = "UDP_CONNECT";
    private const string UrlRequestStart = "URL_REQUEST_START_JOB";

    private static readonly string[] TcpConnectEvents = ["TCP_CONNECT", "TCP_CONNECT_ATTEMPT"];

    // The source types UDP sockets log under; SOCKET_CONNECT of any other
    // source is a TCP socket's.
    private const string UdpSourcePrefix = "UDP_";
    private static readonly string[] RequiredSourceTypes = ["SOCKET", "UDP_SOCKET", "UDP_CLIENT_SOCKET", "URL_REQUEST"];

    /// <summary>Every event type the reader looks for, which must exist in the log.</summary>
    public static readonly IReadOnlyList<string> RequiredEventTypes =
        [.. LookupEvents, ResolverRequest, .. TcpConnectEvents, SocketConnect, UdpConnect, UrlRequestStart];

    private readonly string text;

    private NetLog(string text, int eventCount, IReadOnlyList<string> lookups, IReadOnlyList<string> requestedHosts,
        IReadOnlyList<string> tcpConnects, IReadOnlyList<string> udpConnects, IReadOnlyList<string> urlRequests)
    {
        this.text = text;
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

    /// <summary>
    /// Whether <paramref name="value"/> appears anywhere in the log (any
    /// parameter of any event: a stream job's URL, a socket group): a name
    /// the browser did something with past the page, whatever the event.
    /// </summary>
    public bool Mentions(string value) => text.Contains(value, StringComparison.OrdinalIgnoreCase);

    /// <summary>Reads the log at <paramref name="path"/>.</summary>
    /// <exception cref="InvalidDataException">The log lacks an event or source type the reader relies on.</exception>
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
            var missing = RequiredEventTypes.Except(names.Values).Concat(RequiredSourceTypes.Except(sourceNames.Values)).ToList();
            if (missing.Count > 0)
            {
                throw new InvalidDataException("the NetLog of this runtime lacks " + string.Join(", ", missing)
                    + "; update NetLog.cs to what the runtime logs instead, or the canary would see nothing");
            }
            string[] lookupEvents = names.ContainsValue(MdnsEvent) ? [.. LookupEvents, MdnsEvent] : LookupEvents;
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
                var udpSource = sourceType.StartsWith(UdpSourcePrefix, StringComparison.Ordinal);
                if (lookupEvents.Contains(name))
                {
                    lookups.Add(name + " " + (Param("host") ?? Param("hostname") ?? ""));
                }
                else if (name == ResolverRequest && Param("host") is { } host)
                {
                    hosts.Add(host);
                }
                else if (TcpConnectEvents.Contains(name) && (Param("address") ?? Param("address_list")) is { } address)
                {
                    tcp.Add(name + " " + address);
                }
                else if (name == SocketConnect && !udpSource)
                {
                    tcp.Add(name + " " + (Param("address") ?? ""));
                }
                else if ((name == UdpConnect || name == SocketConnect) && udpSource)
                {
                    // UDP_CONNECT begins with the address and ends with the
                    // result; SOCKET_CONNECT carries both at once.
                    if (Param("address") is { } a)
                    {
                        udpAddress[sourceId] = a;
                    }
                    var phase = e.TryGetProperty("phase", out var ph) ? ph.GetInt32() : 0;
                    if (name == SocketConnect || phase == 2)
                    {
                        udp.Add(udpAddress.GetValueOrDefault(sourceId, "?") + " " + (Param("net_error") ?? "ok"));
                    }
                }
                else if (name == UrlRequestStart && Param("url") is { } url)
                {
                    urls.Add(url);
                }
            }
            return new NetLog(text, count, lookups, hosts, tcp, udp, urls);
        }
    }
}
