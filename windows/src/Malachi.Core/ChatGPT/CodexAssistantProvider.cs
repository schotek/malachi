// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Runtime adapter from docs/chatgpt-integration.md §3, §5–6. The normalized
// event semantics match ui/internal/assistantpanel/process.go and
// macos/Sources/MalachiCore/Platform/ClaudeCodeProcess.swift.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Platform;

namespace Malachi.Core.ChatGPT;

/// <summary>ChatGPT plan provider; secrets remain in the connection service.</summary>
public sealed class CodexAssistantProvider : IAssistantProvider
{
    private readonly CodexAssistantOptions options;
    private readonly IChatGptAccessTokenSource tokens;
    private readonly IPrivateDirectoryFactory? directories;
    private readonly TimeProvider time;

    /// <summary>Creates the runtime with platform path/directory services.</summary>
    public CodexAssistantProvider(CodexAssistantOptions options, IChatGptAccessTokenSource tokens,
        IPrivateDirectoryFactory? directories = null, TimeProvider? time = null)
    {
        ArgumentNullException.ThrowIfNull(options);
        ArgumentNullException.ThrowIfNull(tokens);
        this.options = options;
        this.tokens = tokens;
        this.directories = directories;
        this.time = time ?? TimeProvider.System;
    }

    /// <inheritdoc/>
    public AssistantProviderID Id => AssistantProviderID.ChatGpt;

    /// <inheritdoc/>
    public string Model => options.Model();

    /// <inheritdoc/>
    public bool HasConsent => options.HasConsent();

    /// <inheritdoc/>
    public void AcceptConsent() => options.AcceptConsent();

    /// <summary>Sweeps only abandoned app-owned profiles, retaining active sessions.</summary>
    public void CleanAbandonedSessions() => CodexSessionDirectories.Sweep(options.Directory, directories);

    /// <summary>Models exposed by this installed runtime; no mail or inference is sent.</summary>
    public async Task<IReadOnlyList<CodexModel>> GetModelsAsync(CancellationToken cancellationToken = default)
    {
        var executable = options.Executable();
        if (executable is null || !Path.IsPathFullyQualified(executable) || !File.Exists(executable))
        {
            throw new AssistantProviderException("codex_not_found");
        }
        var session = new CodexAssistantSession(options, tokens, new AssistantSessionSpec
        {
            SystemPrompt = "Model discovery", ToolPolicy = AssistantToolPolicy.None,
        }, executable, "", directories, time, SynchronizationContext.Current);
        try
        {
            await session.InitializeAsync(cancellationToken).ConfigureAwait(false);
            return await session.GetModelsAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            session.Terminate();
            await session.Completion.ConfigureAwait(false);
        }
    }

    /// <inheritdoc/>
    public async Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(spec);
        if (!Enum.IsDefined(spec.ToolPolicy)) { throw new AssistantProviderException("chatgpt_invalid_tool_policy"); }
        if (spec.JsonSchema.Length > 0)
        {
            try
            {
                using var schema = JsonDocument.Parse(spec.JsonSchema);
                if (schema.RootElement.ValueKind != JsonValueKind.Object) { throw new AssistantProviderException("chatgpt_invalid_output_schema"); }
            }
            catch (JsonException) { throw new AssistantProviderException("chatgpt_invalid_output_schema"); }
        }
        if (!HasConsent)
        {
            throw new AssistantProviderException("chatgpt_consent_required");
        }
        var executable = options.Executable();
        if (executable is null || !Path.IsPathFullyQualified(executable) || !File.Exists(executable))
        {
            throw new AssistantProviderException("codex_not_found");
        }
        if (spec.ToolPolicy == AssistantToolPolicy.Panel && (!Path.IsPathFullyQualified(options.Bridge) || !File.Exists(options.Bridge)))
        {
            throw new AssistantProviderException("chatgpt_tools_unavailable");
        }
        var context = SynchronizationContext.Current;
        // Validates/refreshes the grant before a process or a tool exists.
        var model = Model;
        try { _ = await tokens.GetAccessTokenAsync(cancellationToken).ConfigureAwait(false); }
        catch (ChatGptAuthException failure) { throw new AssistantProviderException("chatgpt_" + failure.Error.ToString().ToLowerInvariant()); }
        var session = new CodexAssistantSession(options, tokens, spec, executable, model, directories, time, context);
        try
        {
            await session.InitializeAsync(cancellationToken).ConfigureAwait(false);
            return session;
        }
        catch
        {
            session.Terminate();
            await session.Completion.ConfigureAwait(false);
            throw;
        }
    }
}
