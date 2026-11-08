// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tools and bridge arguments of an AssistantToolPolicy, the authority a
// provider session (docs/chatgpt-integration.md §6) checks before it starts
// anything. The lists are the local copies of the daemon's tools of each
// purpose (backend/internal/assistant tools.go) that Go's chatgpt
// toolPolicy and Swift's CodexPolicy keep (panelTools, triageTools,
// replyTools): the policy accepts no other, whatever the daemon's prompt
// lists. The daemon's bridge arguments start with --in-app (Go inAppArg);
// without it, it is put first (PolicyBridgeArgs). Windows-first: the policy
// is chosen by the caller (the panel, a triage with or without drafts, a
// suggested reply), where Go and Swift read it from the bridge's arguments.
// This file holds no translatable text.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// The bridge's first argument whenever the application starts it (the
    /// daemon's <see cref="Api.PromptResult.BridgeArgs"/> start with it; Go
    /// <c>inAppArg</c>): the prompts carry the user's profile, so the bridge
    /// keeps it out of its instructions.
    /// </summary>
    public const string InAppArg = "--in-app";

    // The bridge's tools each purpose may call, by their bare names, as the
    // daemon names them (Go panelTools, triageTools, replyTools).
    private static readonly string[] PanelPolicyTools =
    [
        "list_accounts", "list_folders", "list_messages", "search_messages", "read_message", "get_attachment",
        "create_draft", "list_calendars", "list_events", "get_event", "find_free_time", "list_invitations",
        "get_user_profile", "propose_profile_note",
    ];

    private static readonly string[] TriagePolicyTools =
    [
        "list_accounts", "list_folders", "list_messages", "search_messages", "read_message", "get_attachment",
        "create_draft", "list_triage_queue", "annotate_case", "add_commitment",
    ];

    private static readonly string[] ReplyPolicyTools = ["read_message", "list_messages", "create_draft"];

    /// <summary>
    /// All the tools <paramref name="policy"/> allows, with the bridge's
    /// prefix, in the daemon's order; empty for
    /// <see cref="AssistantToolPolicy.None"/> and a value outside the enum. A
    /// triage without drafts goes without create_draft
    /// (<see cref="TriageAutomaticDrafts"/>).
    /// </summary>
    public static IReadOnlyList<string> PolicyTools(AssistantToolPolicy policy) => policy switch
    {
        AssistantToolPolicy.Panel => PrefixTools(PanelPolicyTools),
        AssistantToolPolicy.Triage => PrefixTools([.. TriagePolicyTools.Where(t => t != "create_draft")]),
        AssistantToolPolicy.TriageDrafts => PrefixTools(TriagePolicyTools),
        AssistantToolPolicy.ReplyOnly => PrefixTools(ReplyPolicyTools),
        _ => [],
    };

    /// <summary>
    /// The bare tool names (without the bridge's prefix) the session of
    /// <paramref name="spec"/> exposes: its <see cref="AssistantSessionSpec.Tools"/>,
    /// or all of its policy's. Check <see cref="PolicyAllows"/> first.
    /// </summary>
    public static IReadOnlySet<string> SessionTools(AssistantSessionSpec spec)
    {
        ArgumentNullException.ThrowIfNull(spec);
        return (spec.Tools ?? PolicyTools(spec.ToolPolicy)).Select(StripBridgePrefix).ToHashSet(StringComparer.Ordinal);
    }

    /// <summary>
    /// Whether <paramref name="spec"/> stays within its policy: a policy of
    /// the enum; tools that are all the policy's (none for
    /// <see cref="AssistantToolPolicy.None"/>, at least one otherwise); and,
    /// after a leading <see cref="InAppArg"/>, exactly the bridge arguments
    /// the policy starts the bridge with: none for the panel and without a
    /// bridge, <c>--allow-triage --triage-run &lt;runId&gt; --triage-max
    /// &lt;n&gt;</c> (a run id that is no flag, a limit the bridge accepts)
    /// for triage, <c>--reply-only &lt;messageId&gt;</c> (a message id that is
    /// no flag) for a suggested reply. Never <c>--allow-modify</c> or
    /// <c>--allow-send</c>.
    /// </summary>
    public static bool PolicyAllows(AssistantSessionSpec spec)
    {
        ArgumentNullException.ThrowIfNull(spec);
        if (!Enum.IsDefined(spec.ToolPolicy))
        {
            return false;
        }
        var allowed = PolicyTools(spec.ToolPolicy).Select(StripBridgePrefix).ToHashSet(StringComparer.Ordinal);
        var tools = SessionTools(spec);
        if (!tools.All(allowed.Contains) || (spec.ToolPolicy != AssistantToolPolicy.None && tools.Count == 0))
        {
            return false;
        }
        var a = WithoutInApp(spec.BridgeArgs);
        return spec.ToolPolicy switch
        {
            AssistantToolPolicy.Triage or AssistantToolPolicy.TriageDrafts =>
                a.Count == 5 && a[0] == "--allow-triage" && a[1] == "--triage-run" && IsArgumentValue(a[2])
                && a[3] == "--triage-max" && IsTriageMax(a[4]),
            AssistantToolPolicy.ReplyOnly => a.Count == 2 && a[0] == "--reply-only" && IsArgumentValue(a[1]),
            _ => a.Count == 0,
        };
    }

    /// <summary>
    /// The bridge's arguments after <c>--socket</c> as a session starts it
    /// with: <see cref="InAppArg"/> first, then those of
    /// <paramref name="spec"/> without a leading one (Go <c>toolPolicy</c>'s
    /// arguments). Check <see cref="PolicyAllows"/> first.
    /// </summary>
    public static IReadOnlyList<string> PolicyBridgeArgs(AssistantSessionSpec spec)
    {
        ArgumentNullException.ThrowIfNull(spec);
        return [InAppArg, .. WithoutInApp(spec.BridgeArgs)];
    }

    // The arguments without a leading --in-app.
    private static IReadOnlyList<string> WithoutInApp(IReadOnlyList<string> args) =>
        args.Count > 0 && args[0] == InAppArg ? [.. args.Skip(1)] : args;

    // A value of a flag: not empty and no flag itself.
    private static bool IsArgumentValue(string s) => s.Length > 0 && s[0] != '-';

    // The decimal digits of a limit within TriageMaxLow…TriageMaxHigh, as
    // the daemon writes it.
    private static bool IsTriageMax(string s) =>
        s.Length is > 0 and <= 3 && s.All(char.IsAsciiDigit) && s[0] != '0'
        && int.Parse(s, NumberStyles.None, CultureInfo.InvariantCulture) is >= TriageMaxLow and <= TriageMaxHigh;
}
