// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Native stdio transport from docs/chatgpt-integration.md §5; parallels
// ui/internal/assistantpanel/process.go and MalachiCore/ClaudeCodeProcess,
// with correlated JSON-RPC instead of Claude's stream-json. No shell/PInvoke.

using System;
using System.Buffers;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Platform;

namespace Malachi.Core.ChatGPT;

/// <summary>Bounded JSON-RPC framing shared by Codex and its clean MCP peer.</summary>
public sealed class CodexJsonRpc : IAsyncDisposable
{
    /// <summary>Largest accepted frame, including MCP attachment text.</summary>
    public const int FrameLimit = 16 * 1024 * 1024;
    private readonly Process process;
    private readonly SemaphoreSlim writes = new(1, 1);
    private readonly ConcurrentDictionary<long, TaskCompletionSource<JsonElement>> pending = new();
    private readonly CancellationTokenSource stopped = new();
    private readonly Task completion;
    private long next;
    private readonly ConcurrentDictionary<long, byte> abandoned = new();

    /// <summary>Launches only the supplied executable with an explicit environment.</summary>
    public CodexJsonRpc(string executable, IReadOnlyList<string> arguments, IReadOnlyDictionary<string, string> environment, string directory)
    {
        ArgumentException.ThrowIfNullOrEmpty(executable);
        ArgumentNullException.ThrowIfNull(arguments);
        ArgumentNullException.ThrowIfNull(environment);
        ArgumentException.ThrowIfNullOrEmpty(directory);
        var start = new ProcessStartInfo(executable)
        {
            UseShellExecute = false,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            CreateNoWindow = true,
            WorkingDirectory = directory,
        };
        foreach (var arg in arguments)
        {
            start.ArgumentList.Add(arg);
        }
        start.Environment.Clear();
        foreach (var pair in environment)
        {
            start.Environment[pair.Key] = pair.Value;
        }
        using (SpawnGate.Enter())
        {
            process = Process.Start(start) ?? throw new AssistantProviderException("codex_launch_failed");
        }
        completion = PumpAsync();
    }

    /// <summary>Notifications arrive in wire order; the callback must not block.</summary>
    public Action<string, JsonElement>? Notification { get; set; }

    /// <summary>Handles server requests; omitted handlers explicitly deny them.</summary>
    public Func<string, JsonElement, Task<JsonElement>>? ServerRequest { get; set; }

    /// <summary>Completes when stdout is drained and the process exits.</summary>
    public Task Completion => completion;

    /// <summary>The exit status, only available after completion.</summary>
    public int ExitCode => process.HasExited ? process.ExitCode : -1;

    /// <summary>Sends one correlated request. Cancellation never resends it.</summary>
    public async Task<JsonElement> CallAsync(string method, Action<Utf8JsonWriter> parameters, CancellationToken cancellationToken)
    {
        if (pending.Count >= 32)
        {
            throw new AssistantProviderException("codex_pending_limit");
        }
        var id = Interlocked.Increment(ref next);
        var answer = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        pending[id] = answer;
        try
        {
            await WriteAsync(w =>
            {
                w.WriteString("jsonrpc", "2.0");
                w.WriteNumber("id", id);
                w.WriteString("method", method);
                w.WritePropertyName("params");
                parameters(w);
            }, cancellationToken).ConfigureAwait(false);
            return await answer.Task.WaitAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            if (pending.TryRemove(id, out _)) { abandoned[id] = 0; }
        }
    }

    /// <summary>A protocol notification with an object payload.</summary>
    public Task NotifyAsync(string method, CancellationToken cancellationToken) => WriteAsync(w =>
    {
        w.WriteString("jsonrpc", "2.0");
        w.WriteString("method", method);
        w.WriteStartObject("params");
        w.WriteEndObject();
    }, cancellationToken);

    /// <summary>Ends stdin and the owned tree immediately after protocol interrupt.</summary>
    public void Terminate()
    {
        stopped.Cancel();
        try
        {
            process.StandardInput.Close();
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
            }
        }
        catch (Exception e) when (e is IOException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            // Exiting concurrently is the same outcome.
        }
    }

    /// <inheritdoc/>
    public async ValueTask DisposeAsync()
    {
        Terminate();
        await completion.ConfigureAwait(false);
        process.Dispose();
        stopped.Dispose();
        writes.Dispose();
    }

    private async Task WriteAsync(Action<Utf8JsonWriter> members, CancellationToken cancellationToken)
    {
        var buffer = new ArrayBufferWriter<byte>();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            members(writer);
            writer.WriteEndObject();
        }
        if (buffer.WrittenCount > FrameLimit)
        {
            throw new AssistantProviderException("codex_frame_limit");
        }
        await writes.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await process.StandardInput.BaseStream.WriteAsync(buffer.WrittenMemory, cancellationToken).ConfigureAwait(false);
            await process.StandardInput.BaseStream.WriteAsync("\n"u8.ToArray(), cancellationToken).ConfigureAwait(false);
            await process.StandardInput.BaseStream.FlushAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            writes.Release();
        }
    }

    private async Task PumpAsync()
    {
        var stderr = DrainAsync(process.StandardError.BaseStream);
        try
        {
            var frame = new ArrayBufferWriter<byte>();
            var bytes = new byte[8192];
            while (true)
            {
                var count = await process.StandardOutput.BaseStream.ReadAsync(bytes, stopped.Token).ConfigureAwait(false);
                if (count == 0)
                {
                    if (frame.WrittenCount > 0)
                    {
                        throw new AssistantProviderException("codex_truncated_frame");
                    }
                    Terminate();
                    break;
                }
                for (var i = 0; i < count; i++)
                {
                    if (bytes[i] == (byte)'\n')
                    {
                        if (frame.WrittenCount > 0)
                        {
                            await DispatchAsync(frame.WrittenMemory).ConfigureAwait(false);
                        }
                        frame.Clear();
                    }
                    else
                    {
                        if (frame.WrittenCount >= FrameLimit)
                        {
                            throw new AssistantProviderException("codex_frame_limit");
                        }
                        frame.GetSpan(1)[0] = bytes[i];
                        frame.Advance(1);
                    }
                }
            }
        }
        catch (Exception e) when (e is IOException or JsonException or OperationCanceledException or AssistantProviderException or ObjectDisposedException or InvalidOperationException or KeyNotFoundException)
        {
            Terminate();
        }
        finally
        {
            foreach (var reply in pending.Values)
            {
                reply.TrySetException(new AssistantProviderException("codex_protocol_closed"));
            }
            await process.WaitForExitAsync().ConfigureAwait(false);
            await stderr.ConfigureAwait(false);
        }
    }

    private async Task DispatchAsync(ReadOnlyMemory<byte> frame)
    {
        using var document = JsonDocument.Parse(frame, new JsonDocumentOptions { MaxDepth = 64 });
        var root = document.RootElement;
        if (root.ValueKind != JsonValueKind.Object)
        {
            throw new AssistantProviderException("codex_invalid_frame");
        }
        if (!root.TryGetProperty("method", out var method))
        {
            if (!root.TryGetProperty("id", out var id) || !id.TryGetInt64(out var number))
            {
                throw new AssistantProviderException("codex_invalid_response_id");
            }
            if (abandoned.TryRemove(number, out _)) { return; }
            if (!pending.TryRemove(number, out var answer))
            {
                throw new AssistantProviderException("codex_unmatched_response");
            }
            if (root.TryGetProperty("error", out _))
            {
                answer.TrySetException(new AssistantProviderException("codex_rpc_error"));
            }
            else if (root.TryGetProperty("result", out var result))
            {
                answer.TrySetResult(result.Clone());
            }
            else
            {
                answer.TrySetException(new AssistantProviderException("codex_invalid_response"));
            }
            return;
        }
        var name = method.GetString() ?? throw new AssistantProviderException("codex_invalid_method");
        var parameters = root.TryGetProperty("params", out var payload) ? payload.Clone() : Json("{}");
        if (!root.TryGetProperty("id", out var requestId))
        {
            Notification?.Invoke(name, parameters);
            return;
        }
        var response = ServerRequest is { } handler
            ? await handler(name, parameters).ConfigureAwait(false)
            : Json("{\"error\":{\"code\":-32601,\"message\":\"unsupported request\"}}");
        await WriteAsync(w =>
        {
            w.WriteString("jsonrpc", "2.0");
            w.WritePropertyName("id");
            requestId.WriteTo(w);
            if (response.TryGetProperty("error", out var error))
            {
                w.WritePropertyName("error");
                error.WriteTo(w);
            }
            else
            {
                w.WritePropertyName("result");
                response.WriteTo(w);
            }
        }, stopped.Token).ConfigureAwait(false);
    }

    private static async Task DrainAsync(Stream stream)
    {
        var bytes = new byte[8192];
        try
        {
            while (await stream.ReadAsync(bytes).ConfigureAwait(false) != 0)
            {
                // stderr can contain credentials or mail: retain nothing.
            }
        }
        catch (IOException)
        {
            // The child exited.
        }
    }

    /// <summary>Parses trusted protocol literals without reflection serialization.</summary>
    internal static JsonElement Json(string value)
    {
        using var document = JsonDocument.Parse(value);
        return document.RootElement.Clone();
    }
}
