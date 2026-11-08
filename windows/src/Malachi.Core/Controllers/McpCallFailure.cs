// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MCPRegistrationController.swift
// (MCPCallFailure, reason, toast(registering:)); GTK:
// ui/internal/mcpsetup/mcpsetup.go (ExitError, ErrNoClient) and
// ui/internal/window/claude_integration.go (bindMCP's toasts).

using Malachi.Core.I18n;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>Why a bridge call yielded no status (Swift <c>MCPCallFailure</c>).</summary>
internal abstract record McpCallFailure
{
    // Only the cases below derive from it.
    private McpCallFailure()
    {
    }

    /// <summary>The technical detail for the log and the texts.</summary>
    public string Reason => this switch
    {
        Failed f => f.Detail,
        Run r => r.Detail,
        _ => "no Claude app found",
    };

    /// <summary>
    /// The toast for a failed install (<paramref name="registering"/>) or
    /// uninstall by a controller for <paramref name="client"/> (null: every
    /// app). The msgids are the GTK dialog's: the app's own "not installed"
    /// sentence when the app the call named is not there
    /// (<see cref="Integrations.ClaudeAppTexts.NotInstalled"/>), else the
    /// bridge's reason; GTK has no controller without a client, whose "no
    /// Claude app found" is shown as that reason.
    /// </summary>
    public string Toast(bool registering, string? client = null) => this switch
    {
        NoClaudeApp when Integrations.ClaudeAppText(KindOf(client)) is { } app => app.NotInstalled,
        // TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
        _ when registering => L10n.T("The MCP bridge could not be registered: %s", Reason),
        // TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
        _ => L10n.T("The MCP bridge could not be unregistered: %s", Reason),
    };

    // The local integration of the Claude app client names.
    private static Integrations.LocalKind KindOf(string? client) => client switch
    {
        McpStatus.ClientDesktop => Integrations.LocalKind.ClaudeDesktop,
        McpStatus.ClientCode => Integrations.LocalKind.ClaudeCode,
        _ => Integrations.LocalKind.None,
    };

    /// <summary><c>install</c> found neither Claude app, or not the one <c>--client</c> named.</summary>
    public sealed record NoClaudeApp : McpCallFailure;

    /// <summary>
    /// A non-zero exit with the bridge's reason (or the exit status when it
    /// gave none), a crash, or output that is not a status.
    /// </summary>
    /// <param name="Detail">The reason.</param>
    public sealed record Failed(string Detail) : McpCallFailure;

    /// <summary>The bridge could not be started, or did not finish in time.</summary>
    /// <param name="Detail">The runner's reason.</param>
    public sealed record Run(string Detail) : McpCallFailure;
}
