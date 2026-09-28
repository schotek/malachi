// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MCPRegistrationController.swift
// (MCPCallFailure, reason, toast(registering:)); GTK:
// ui/internal/mcpsetup/mcpsetup.go (ExitError, ErrNoClient) and
// ui/internal/window/preferences.go (bindMCP's toasts).

using Malachi.Core.I18n;

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
    /// uninstall. The msgids are the GTK page's.
    /// </summary>
    public string Toast(bool registering) => this switch
    {
        NoClaudeApp => L10n.T("No Claude app was found on this computer"),
        // TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
        _ when registering => L10n.T("The MCP bridge could not be registered: %s", Reason),
        // TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
        _ => L10n.T("The MCP bridge could not be unregistered: %s", Reason),
    };

    /// <summary><c>install</c> found neither Claude app.</summary>
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
