// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tools and bridge arguments of an AssistantToolPolicy, the authority a
// provider session (docs/chatgpt-integration.md §6) checks before it starts
// anything. The lists are those of the Claude command line (AllowedTools,
// TriageTools, SuggestReplyTools; macos/Sources/MalachiCore/Assistant/
// AssistantPanel.swift, AssistantTriage.swift, AssistantSuggestReply.swift);
// Swift's CodexPolicy strips the bridge's prefix as SessionTools does.
// Windows-first: Swift and Go take the request's tools as they come. This
// file holds no translatable text.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// All the tools <paramref name="policy"/> allows, with the bridge's
    /// prefix, in the order of their Claude command line; empty for
    /// <see cref="AssistantToolPolicy.None"/> and a value outside the enum.
    /// </summary>
    public static IReadOnlyList<string> PolicyTools(AssistantToolPolicy policy) => policy switch
    {
        AssistantToolPolicy.Panel => AllowedTools,
        AssistantToolPolicy.Triage => TriageTools(drafts: false),
        AssistantToolPolicy.TriageDrafts => TriageTools(drafts: true),
        AssistantToolPolicy.ReplyOnly => SuggestReplyTools,
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
    /// <see cref="AssistantToolPolicy.None"/>, at least one otherwise); and
    /// exactly the bridge arguments the policy starts the bridge with: none
    /// for the panel and without a bridge, <see cref="TriageBridgeArgs"/>
    /// (a run id that is no flag, a limit the bridge accepts) for triage,
    /// <see cref="SuggestReplyBridgeArgs"/> (a message id that is no flag)
    /// for a suggested reply. Never <c>--allow-modify</c> or <c>--allow-send</c>.
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
        var a = spec.BridgeArgs;
        return spec.ToolPolicy switch
        {
            AssistantToolPolicy.Triage or AssistantToolPolicy.TriageDrafts =>
                a.Count == 5 && a[0] == "--allow-triage" && a[1] == "--triage-run" && IsArgumentValue(a[2])
                && a[3] == "--triage-max" && IsTriageMax(a[4]),
            AssistantToolPolicy.ReplyOnly => a.Count == 2 && a[0] == "--reply-only" && IsArgumentValue(a[1]),
            _ => a.Count == 0,
        };
    }

    // A value of a flag: not empty and no flag itself.
    private static bool IsArgumentValue(string s) => s.Length > 0 && s[0] != '-';

    // The decimal digits of a limit within TriageMaxLow…TriageMaxHigh, as
    // TriageBridgeArgs writes it.
    private static bool IsTriageMax(string s) =>
        s.Length is > 0 and <= 3 && s.All(char.IsAsciiDigit) && s[0] != '0'
        && int.Parse(s, NumberStyles.None, CultureInfo.InvariantCulture) is >= TriageMaxLow and <= TriageMaxHigh;
}
