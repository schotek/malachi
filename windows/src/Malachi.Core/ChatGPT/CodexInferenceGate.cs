// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Inference policy from docs/chatgpt-integration.md §6. Codex has no global
// tool allow list: this native loopback gateway enforces the model-facing
// catalog and response calls before the child can execute anything. Mirrors
// the authority of Go/Swift Assistant.AllowedTools; no OS or UI dependency.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Runtime.CompilerServices;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;

namespace Malachi.Core.ChatGPT;

/// <summary>A fixed-destination, authenticated loopback Responses gateway.</summary>
public sealed class CodexInferenceGate : IAsyncDisposable
{
    private readonly TcpListener listener = new(IPAddress.Loopback, 0);
    private readonly CancellationTokenSource stopped;
    private readonly IChatGptAccessTokenSource tokens;
    private readonly HashSet<string> tools;
    private readonly HttpClient client;
    private readonly Task run;
    private readonly TimeProvider time;
    private readonly string path = "/" + Convert.ToHexString(RandomNumberGenerator.GetBytes(32)) + "/v1/responses";
    private readonly List<Task> requests = [];

    /// <summary>Only these bare Malachi tool names may cross this gate.</summary>
    public CodexInferenceGate(IChatGptAccessTokenSource tokens, IEnumerable<string> allowedTools, HttpMessageHandler? handler = null, TimeProvider? time = null)
    {
        ArgumentNullException.ThrowIfNull(tokens);
        ArgumentNullException.ThrowIfNull(allowedTools);
        this.tokens = tokens;
        stopped = CancellationTokenSource.CreateLinkedTokenSource(tokens.SessionCancellation);
        tools = new HashSet<string>(allowedTools, StringComparer.Ordinal);
        this.time = time ?? TimeProvider.System;
        client = new HttpClient(handler ?? new HttpClientHandler { AllowAutoRedirect = false, UseCookies = false }, disposeHandler: true);
        client.Timeout = Timeout.InfiniteTimeSpan;
        listener.Start();
        Credential = Convert.ToHexString(RandomNumberGenerator.GetBytes(32));
        BaseUrl = "http://127.0.0.1:" + ((IPEndPoint)listener.LocalEndpoint).Port.ToString(System.Globalization.CultureInfo.InvariantCulture) + path[..^10];
        run = AcceptAsync();
    }

    /// <summary>Opaque local credential, unrelated to the OpenAI token.</summary>
    public string Credential { get; }

    /// <summary>Per-session endpoint; only /responses is accepted.</summary>
    public string BaseUrl { get; }

    /// <summary>Fixed diagnostic code only; never a payload or credential.</summary>
    public string? LastFailure { get; private set; }

    /// <summary>Filters the complete upstream tool catalog, before inference.</summary>
    public byte[] FilterRequest(ReadOnlyMemory<byte> body)
    {
        using var document = JsonDocument.Parse(body, new JsonDocumentOptions { MaxDepth = 64 });
        var root = document.RootElement;
        if (root.ValueKind != JsonValueKind.Object)
        {
            throw new AssistantProviderException("codex_invalid_inference_request");
        }
        var catalog = new List<JsonElement>();
        if (root.TryGetProperty("tools", out var rootCatalog) && rootCatalog.ValueKind == JsonValueKind.Array)
        {
            catalog.AddRange(rootCatalog.EnumerateArray());
        }
        using var buffer = new MemoryStream();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            foreach (var property in root.EnumerateObject())
            {
                if (property.Name is "previous_response_id" or "conversation")
                {
                    throw new AssistantProviderException("codex_persistent_upstream_history_denied");
                }
                if (property.Name is "tools" or "store" or "stream" or "tool_choice" or "parallel_tool_calls")
                {
                    continue;
                }
                if (property.Name == "input" && property.Value.ValueKind == JsonValueKind.Array)
                {
                    writer.WriteStartArray("input");
                    foreach (var item in property.Value.EnumerateArray())
                    {
                        if (String(item, "type") == "additional_tools")
                        {
                            if (!item.TryGetProperty("tools", out var extra) || extra.ValueKind != JsonValueKind.Array)
                            {
                                throw new AssistantProviderException("codex_invalid_tool_catalog");
                            }
                            catalog.AddRange(extra.EnumerateArray());
                        }
                        else { item.WriteTo(writer); }
                    }
                    writer.WriteEndArray();
                    continue;
                }
                property.WriteTo(writer);
            }
            writer.WriteBoolean("store", false);
            writer.WriteBoolean("stream", true);
            writer.WriteBoolean("parallel_tool_calls", false);
            var advertised = new HashSet<string>(StringComparer.Ordinal);
            writer.WriteStartArray("tools");
            {
                foreach (var entry in catalog)
                {
                    if (entry.ValueKind != JsonValueKind.Object || String(entry, "type") != "namespace" || String(entry, "name") != "malachi")
                    {
                        continue;
                    }
                    if (!entry.TryGetProperty("tools", out var nested) || nested.ValueKind != JsonValueKind.Array)
                    {
                        throw new AssistantProviderException("codex_invalid_tool_catalog");
                    }
                    var accepted = nested.EnumerateArray().Where(t => String(t, "type") == "function" && tools.Contains(String(t, "name"))).ToArray();
                    if (accepted.Length == 0)
                    {
                        continue;
                    }
                    writer.WriteStartObject();
                    writer.WriteString("type", "namespace");
                    writer.WriteString("name", "malachi");
                    writer.WriteString("description", "Malachi Mail read and draft tools");
                    writer.WriteStartArray("tools");
                    foreach (var tool in accepted)
                    {
                        if (!advertised.Add(String(tool, "name"))) { throw new AssistantProviderException("codex_duplicate_tool"); }
                        tool.WriteTo(writer);
                    }
                    writer.WriteEndArray();
                    writer.WriteEndObject();
                }
            }
            writer.WriteEndArray();
            if (!advertised.SetEquals(tools)) { throw new AssistantProviderException("codex_required_tools_missing"); }
            writer.WriteEndObject();
        }
        return buffer.ToArray();
    }

    /// <summary>Rejects built-in, hosted and unlisted tool calls in SSE items.</summary>
    public void ValidateResponseEvent(string data)
    {
        if (data == "[DONE]")
        {
            return;
        }
        using var document = JsonDocument.Parse(data);
        var root = document.RootElement;
        if (root.TryGetProperty("item", out var item))
        {
            ValidateItem(item);
        }
        if (root.TryGetProperty("response", out var response) && response.TryGetProperty("output", out var output) && output.ValueKind == JsonValueKind.Array)
        {
            foreach (var outputItem in output.EnumerateArray())
            {
                ValidateItem(outputItem);
            }
        }
    }

    private void ValidateItem(JsonElement item)
    {
        var type = String(item, "type");
        if (type is "message" or "reasoning" or "compaction")
        {
            return;
        }
        if (type == "function_call" && String(item, "namespace") == "malachi" && tools.Contains(String(item, "name")))
        {
            return;
        }
        throw new AssistantProviderException("codex_disallowed_inference_tool");
    }

    private async Task AcceptAsync()
    {
        try
        {
            while (!stopped.IsCancellationRequested)
            {
                var socket = await listener.AcceptTcpClientAsync(stopped.Token).ConfigureAwait(false);
                requests.RemoveAll(t => t.IsCompleted);
                if (requests.Count >= 4) { socket.Dispose(); continue; }
                var request = ServeAsync(socket);
                requests.Add(request);
                requests.RemoveAll(t => t.IsCompleted);
            }
        }
        catch (Exception e) when (e is OperationCanceledException or SocketException)
        {
            // The owning session closed.
        }
    }

    private async Task ServeAsync(TcpClient socket)
    {
        using (socket)
        {
            var stream = socket.GetStream();
            using var deadline = new CancellationTokenSource(TimeSpan.FromMinutes(3), time);
            using var linked = CancellationTokenSource.CreateLinkedTokenSource(stopped.Token, deadline.Token);
            var requestStop = linked.Token;
            var headerSent = false;
            var awaitingStreamConfirmation = false;
            try
            {
                var header = new MemoryStream();
                var one = new byte[1];
                while (header.Length < 32768)
                {
                    if (await stream.ReadAsync(one, requestStop).ConfigureAwait(false) != 1)
                    {
                        return;
                    }
                    header.WriteByte(one[0]);
                    if (header.Length >= 4 && header.GetBuffer().AsSpan((int)header.Length - 4, 4).SequenceEqual("\r\n\r\n"u8))
                    {
                        break;
                    }
                }
                var lines = Encoding.ASCII.GetString(header.ToArray()).Split("\r\n", StringSplitOptions.None);
                if (lines[0] != "POST " + path + " HTTP/1.1")
                {
                    throw new AssistantProviderException("codex_gate_path_denied");
                }
                var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
                foreach (var line in lines.Skip(1).Where(l => l.Length > 0))
                {
                    var colon = line.IndexOf(':', StringComparison.Ordinal);
                    if (colon < 1 || !headers.TryAdd(line[..colon], line[(colon + 1)..].Trim()))
                    {
                        throw new AssistantProviderException("codex_gate_invalid_headers");
                    }
                }
                if (!headers.TryGetValue("Authorization", out var authorization) || authorization != "Bearer " + Credential
                    || headers.ContainsKey("Transfer-Encoding") || !headers.TryGetValue("Content-Length", out var length)
                    || !int.TryParse(length, System.Globalization.NumberStyles.None, System.Globalization.CultureInfo.InvariantCulture, out var size)
                    || size < 1 || size > CodexJsonRpc.FrameLimit)
                {
                    throw new AssistantProviderException("codex_gate_request_denied");
                }
                var body = new byte[size];
                await stream.ReadExactlyAsync(body, requestStop).ConfigureAwait(false);
                using var request = new HttpRequestMessage(HttpMethod.Post, "https://api.openai.com/v1/responses");
                var accessToken = await tokens.GetAccessTokenAsync(requestStop).ConfigureAwait(false);
                request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", accessToken);
                request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("text/event-stream"));
                request.Content = new ByteArrayContent(FilterRequest(body));
                request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json");
                using var response = await client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, requestStop).ConfigureAwait(false);
                if (!response.IsSuccessStatusCode)
                {
                    LastFailure = response.StatusCode switch
                    {
                        HttpStatusCode.Unauthorized => "chatgpt_reconnect_required",
                        HttpStatusCode.Forbidden => "chatgpt_permission_denied",
                        HttpStatusCode.TooManyRequests => "chatgpt_usage_limit",
                        _ => "chatgpt_inference_refused",
                    };
                    if (response.StatusCode is HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden && tokens is IChatGptInferenceObserver observer)
                    {
                        await observer.InferenceRejectedAsync(accessToken, (int)response.StatusCode, requestStop).ConfigureAwait(false);
                    }
                    headerSent = true;
                    await SendHeaderAsync(stream, (int)response.StatusCode, "application/json").ConfigureAwait(false);
                    await stream.WriteAsync("{\"error\":{\"message\":\"ChatGPT inference refused\"}}"u8.ToArray(), requestStop).ConfigureAwait(false);
                    return;
                }
                var missingType = !response.Content.Headers.Contains("Content-Type");
                awaitingStreamConfirmation = missingType;
                if (!missingType && !string.Equals(response.Content.Headers.ContentType?.MediaType, "text/event-stream", StringComparison.OrdinalIgnoreCase))
                {
                    throw new AssistantProviderException("codex_gate_non_streaming_response");
                }
                await using var upstream = await response.Content.ReadAsStreamAsync(requestStop).ConfigureAwait(false);
                var eventLines = new List<string>();
                var eventSize = 0;
                await foreach (var line in ReadLinesAsync(upstream, requestStop).ConfigureAwait(false))
                {
                    eventSize += line.Length;
                    if (eventSize > CodexJsonRpc.FrameLimit)
                    {
                        throw new AssistantProviderException("codex_gate_response_limit");
                    }
                    if (line.Length > 0)
                    {
                        eventLines.Add(line);
                        continue;
                    }
                    var data = string.Join("\n", eventLines.Where(l => l.StartsWith("data:", StringComparison.Ordinal)).Select(l => l[5..].TrimStart()));
                    if (!headerSent)
                    {
                        if (data.Length == 0)
                        {
                            if (eventLines.Any(l => !l.StartsWith(':'))) { throw new AssistantProviderException("codex_gate_non_streaming_response"); }
                            eventLines.Clear(); eventSize = 0;
                            continue;
                        }
                        if (missingType)
                        {
                            using var first = JsonDocument.Parse(data);
                            if (!String(first.RootElement, "type").StartsWith("response.", StringComparison.Ordinal)) { throw new AssistantProviderException("codex_gate_non_streaming_response"); }
                        }
                        ValidateResponseEvent(data);
                        awaitingStreamConfirmation = false;
                        headerSent = true;
                        await SendHeaderAsync(stream, 200, "text/event-stream").ConfigureAwait(false);
                    }
                    else if (data.Length > 0) { ValidateResponseEvent(data); }
                    await stream.WriteAsync(Encoding.UTF8.GetBytes(string.Join("\n", eventLines) + "\n\n"), requestStop).ConfigureAwait(false);
                    await stream.FlushAsync(requestStop).ConfigureAwait(false);
                    eventLines.Clear();
                    eventSize = 0;
                }
                if (!headerSent) { throw new AssistantProviderException("codex_gate_non_streaming_response"); }
                if (eventLines.Count > 0) { throw new AssistantProviderException("codex_gate_truncated_event"); }
            }
            catch (Exception e) when (e is IOException or SocketException or HttpRequestException or JsonException or OperationCanceledException or AssistantProviderException or ChatGptAuthException or DecoderFallbackException)
            {
                LastFailure = awaitingStreamConfirmation && (e is JsonException || e is AssistantProviderException { Code: "codex_gate_truncated_event" })
                    ? "codex_gate_non_streaming_response"
                    : e is AssistantProviderException failure ? failure.Code : "codex_gateway_failed";
                if (!headerSent)
                {
                    try
                    {
                        await SendHeaderAsync(stream, 502, "application/json").ConfigureAwait(false);
                        await stream.WriteAsync("{\"error\":{\"message\":\"ChatGPT inference refused\"}}"u8.ToArray(), requestStop).ConfigureAwait(false);
                    }
                    catch (Exception writeError) when (writeError is IOException or SocketException or OperationCanceledException) { }
                }
                // Fail closed: never forward an unvalidated event or diagnostic body.
            }
        }
    }

    private static async IAsyncEnumerable<string> ReadLinesAsync(Stream stream, [EnumeratorCancellation] CancellationToken stop)
    {
        var bytes = new byte[8192];
        using var line = new MemoryStream();
        var encoding = new UTF8Encoding(false, true);
        while (true)
        {
            var count = await stream.ReadAsync(bytes, stop).ConfigureAwait(false);
            if (count == 0) { break; }
            for (var i = 0; i < count; i++)
            {
                if (bytes[i] == (byte)'\n')
                {
                    yield return encoding.GetString(line.GetBuffer(), 0, checked((int)line.Length)).TrimEnd('\r');
                    line.SetLength(0);
                }
                else
                {
                    if (line.Length >= CodexJsonRpc.FrameLimit) { throw new AssistantProviderException("codex_gate_line_limit"); }
                    line.WriteByte(bytes[i]);
                }
            }
        }
        if (line.Length > 0) { throw new AssistantProviderException("codex_gate_truncated_event"); }
    }

    private Task SendHeaderAsync(Stream stream, int status, string contentType) => stream.WriteAsync(Encoding.ASCII.GetBytes(
        "HTTP/1.1 " + status.ToString(System.Globalization.CultureInfo.InvariantCulture) + " Response\r\nContent-Type: " + contentType + "\r\nConnection: close\r\n\r\n"), stopped.Token).AsTask();

    internal static string String(JsonElement element, string property) => element.ValueKind == JsonValueKind.Object
        && element.TryGetProperty(property, out var value) && value.ValueKind == JsonValueKind.String ? value.GetString() ?? "" : "";

    /// <inheritdoc/>
    public async ValueTask DisposeAsync()
    {
        stopped.Cancel();
        listener.Stop();
        await run.ConfigureAwait(false);
        await Task.WhenAll(requests).ConfigureAwait(false);
        client.Dispose();
        stopped.Dispose();
    }
}
