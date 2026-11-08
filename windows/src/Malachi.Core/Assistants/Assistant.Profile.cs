// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantProfile.swift
// (proposeProfileTool, parseProposalResult, profileTexts, profileCounter,
// profileProposalCount); Go: ui/internal/assistant/profile.go. The user's
// profile for the assistant (docs/api.md §4.18,
// docs/plans/assistant-profile.md). The daemon appends the profile to every
// system prompt it builds (assistant.prompt; the note of the account a
// one-shot request names), so nothing of it is composed here any more: this
// file keeps the proposal a propose_profile_note result names and the texts
// of Preferences → AI → About You.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

/// <summary>
/// assistant.ProfileStrings: the fixed texts of the profile (Preferences →
/// AI → About You and the panel's proposal card).
/// </summary>
public sealed record ProfileStrings
{
    /// <summary>The group's title, About You.</summary>
    public required string Group { get; init; }

    /// <summary>The description under the group.</summary>
    public required string GroupDescription { get; init; }

    /// <summary>The row I Write As.</summary>
    public required string GenderRow { get; init; }

    /// <summary>The choice Not Specified.</summary>
    public required string GenderUnset { get; init; }

    /// <summary>The choice of masculine forms.</summary>
    public required string GenderMasculine { get; init; }

    /// <summary>The choice of feminine forms.</summary>
    public required string GenderFeminine { get; init; }

    /// <summary>The choice of neutral forms.</summary>
    public required string GenderNeutral { get; init; }

    /// <summary>The placeholder of the text.</summary>
    public required string TextPlaceholder { get; init; }

    /// <summary>The placeholder of an account's note.</summary>
    public required string NotePlaceholder { get; init; }

    /// <summary>An account's row without a note.</summary>
    public required string NoNote { get; init; }

    /// <summary>The switch Send the Profile to Apps over MCP.</summary>
    public required string ShareRow { get; init; }

    /// <summary>The switch's subtitle.</summary>
    public required string ShareSubtitle { get; init; }

    /// <summary>The heading of the pending proposals.</summary>
    public required string Pending { get; init; }

    /// <summary>Edit What the Assistant Knows About You… (the panel's command).</summary>
    public required string EditProfile { get; init; }

    /// <summary>The proposal card's title.</summary>
    public required string ProposalTitle { get; init; }

    /// <summary>Save a proposal.</summary>
    public required string Save { get; init; }

    /// <summary>Edit a proposal first.</summary>
    public required string Edit { get; init; }

    /// <summary>Dismiss a proposal.</summary>
    public required string Dismiss { get; init; }

    /// <summary>A proposal from the panel.</summary>
    public required string FromPanel { get; init; }

    /// <summary>A proposal from another app over MCP.</summary>
    public required string FromOtherApp { get; init; }

    /// <summary>"For the account %s" (a gettext format with one %s).</summary>
    public required string AccountProposal { get; init; }

    /// <summary>The confirmation after a proposal was saved.</summary>
    public required string ProposalOK { get; init; }

    /// <summary>The label of a gender in Preferences.</summary>
    public string GenderLabel(ProfileGender g) => g.Value switch
    {
        ProfileGender.Masculine => GenderMasculine,
        ProfileGender.Feminine => GenderFeminine,
        ProfileGender.Neutral => GenderNeutral,
        _ => GenderUnset,
    };
}

public static partial class Assistant
{
    /// <summary>
    /// The tool whose result names a proposal (<see cref="ParseProposalResult"/>),
    /// without the bridge's prefix, as an event names it.
    /// </summary>
    public const string ProposeProfileTool = "propose_profile_note";

    // The head of a propose_profile_note result,
    // backend/cmd/malachi-mcp/tools_profile.go:
    // "profile note proposal <id> is pending; …".
    private static ReadOnlySpan<byte> ProposalHead => "profile note proposal "u8;

    private static ReadOnlySpan<byte> ProposalTail => " is pending;"u8;

    /// <summary>
    /// assistant.ParseProposalResult: the proposal a propose_profile_note
    /// result names; null otherwise. Only the result's first line counts,
    /// and only when it starts exactly with the head the bridge writes,
    /// "profile note proposal &lt;id&gt; is pending;", followed by the end
    /// of the line or a space; the id holds no white space or control
    /// characters. The application reads the proposal's text from the
    /// daemon, never from the result.
    /// </summary>
    public static string? ParseProposalResult(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var b = Utf8(text);
        var lineEnd = b.AsSpan().IndexOf((byte)0x0A);
        if (lineEnd < 0)
        {
            lineEnd = b.Length;
        }
        if (lineEnd < ProposalHead.Length || !b.AsSpan(0, ProposalHead.Length).SequenceEqual(ProposalHead))
        {
            return null;
        }
        var idStart = ProposalHead.Length;
        var idEnd = IndexOf(b, ProposalTail, idStart, lineEnd);
        if (idEnd < 0)
        {
            return null;
        }
        var rest = idEnd + ProposalTail.Length;
        if (!ValidId(b, idStart, idEnd) || !(rest == lineEnd || b[rest] == 0x20))
        {
            return null;
        }
        return FromUtf8(b.AsSpan(idStart, idEnd - idStart));
    }

    /// <summary>assistant.ProfileTexts: the fixed texts of the profile, translated.</summary>
    public static ProfileStrings ProfileTexts() => new()
    {
        // TRANSLATORS: A group of Preferences → AI: what the user tells the assistant about themselves.
        Group = L10n.T("About You"),
        GroupDescription = L10n.T("The assistant gets this in the app (Claude Code, ChatGPT) and, if you allow it below, in apps connected over MCP; only with your consent."),
        GenderRow = L10n.T("I Write As"),
        GenderUnset = L10n.T("Not Specified"),
        GenderMasculine = L10n.T("A Man"),
        GenderFeminine = L10n.T("A Woman"),
        GenderNeutral = L10n.T("Gender-Neutral"),
        TextPlaceholder = L10n.T("For example: I write briefly · suggest meetings on Mondays and Wednesdays"),
        NotePlaceholder = L10n.T("For example: sign as the studio"),
        NoNote = L10n.T("No Note"),
        ShareRow = L10n.T("Send the Profile to Apps over MCP"),
        ShareSubtitle = L10n.T("Claude Desktop and other apps registered over MCP; a change applies after they restart."),
        Pending = L10n.T("Suggested by the Assistant"),
        EditProfile = L10n.T("Edit What the Assistant Knows About You…"),
        ProposalTitle = L10n.T("The assistant suggests remembering:"),
        Save = L10n.T("Save"),
        Edit = L10n.T("Edit…"),
        Dismiss = L10n.T("Dismiss"),
        FromPanel = L10n.T("From the assistant panel"),
        FromOtherApp = L10n.T("From another app over MCP"),
        AccountProposal = L10n.T("For the account %s"),
        ProposalOK = L10n.T("Saved to your profile"),
    };

    /// <summary>assistant.ProfileCounter: "12 / 2000".</summary>
    public static string ProfileCounter(int n, int limit) =>
        // TRANSLATORS: The character counter under a text field: %d characters of at most %d.
        L10n.T("%d / %d", n, limit);

    /// <summary>
    /// assistant.ProfileProposalCount: the panel's command with pending
    /// proposals, "Edit What the Assistant Knows About You… (2)"; the plain
    /// command for 0.
    /// </summary>
    public static string ProfileProposalCount(int n)
    {
        var item = ProfileTexts().EditProfile;
        return n <= 0 ? item : L10n.T("%s (%d)", item, n);
    }
}
