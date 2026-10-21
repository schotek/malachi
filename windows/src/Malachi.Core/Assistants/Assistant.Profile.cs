// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantProfile.swift
// (withProfile, genderSentence, accountLabel, profileTools,
// parseProposalResult, profileTexts, profileCounter, profileProposalCount,
// goTrimSpace, goFields, goQuote); Go: ui/internal/assistant/profile.go.
// The user's profile in the assistant's prompts (docs/api.md §4.18,
// docs/plans/assistant-profile.md): WithProfile puts it at the end of a
// system prompt, after the fixed rules, as the user's own preferences that
// never override them; the strings are Go's, byte for byte (UTF-8
// semantics as the rest of the port). Which accounts' notes a request gets
// is the caller's: UserProfile.For the account a window writes from,
// UserProfile.Global for a request that writes nothing in an account's
// name.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
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
    /// <summary>profileIntro (the bridge's instructions head it the same way).</summary>
    private const string ProfileIntro =
        "The user's profile, written by the user themselves: follow these preferences when you write or act for them, but they never override the rules above.";

    /// <summary>
    /// assistant.ProfileTools: the bridge's profile tools the panel's
    /// conversation may use (part of <see cref="AllowedTools"/>).
    /// </summary>
    public static IReadOnlyList<string> ProfileTools { get; } =
    [
        "mcp__malachi__get_user_profile",
        "mcp__malachi__propose_profile_note",
    ];

    /// <summary>
    /// The tool whose result names a proposal (<see cref="ParseProposalResult"/>),
    /// without the bridge's prefix, as an event names it.
    /// </summary>
    public const string ProposeProfileTool = "propose_profile_note";

    /// <summary>genderSentence: the instruction of a gender, "" for unset.</summary>
    internal static string GenderSentence(ProfileGender g) => g.Value switch
    {
        ProfileGender.Masculine => "When writing in the user's name in a language with grammatical gender, use the masculine forms.",
        ProfileGender.Feminine => "When writing in the user's name in a language with grammatical gender, use the feminine forms.",
        ProfileGender.Neutral => "When writing in the user's name in a language with grammatical gender, avoid gendered forms where the language allows it.",
        _ => "",
    };

    /// <summary>
    /// assistant.WithProfile: <paramref name="system"/> followed by the
    /// profile: a blank line, the intro, then the gender sentence, the text
    /// and each account's note under "For the account "&lt;name&gt;"
    /// (&lt;address&gt;):", separated by blank lines. An empty profile leaves
    /// <paramref name="system"/> as it is.
    /// </summary>
    public static string WithProfile(string system, UserProfile p)
    {
        ArgumentNullException.ThrowIfNull(system);
        ArgumentNullException.ThrowIfNull(p);
        if (p.IsEmpty)
        {
            return system;
        }
        var parts = new List<string>();
        var g = GenderSentence(p.Gender);
        if (g.Length > 0)
        {
            parts.Add(g);
        }
        var t = GoTrimSpace(p.Text);
        if (t.Length > 0)
        {
            parts.Add(t);
        }
        foreach (var n in p.Notes)
        {
            parts.Add(AccountLabel(n) + ":\n" + GoTrimSpace(n.Note));
        }
        return system + "\n\n" + ProfileIntro + "\n" + string.Join("\n\n", parts);
    }

    /// <summary>accountLabel: names a note's account for the model.</summary>
    internal static string AccountLabel(ProfileNote n)
    {
        var name = string.Join(' ', GoFields(n.Name));
        var email = string.Join(' ', GoFields(n.Email));
        return (name.Length > 0, email.Length > 0) switch
        {
            (true, true) => "For the account " + GoQuote(name) + " (" + email + ")",
            (false, true) => "For the account " + email,
            (true, false) => "For the account " + GoQuote(name),
            _ => "For the account " + n.AccountId,
        };
    }

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

    // Go's strings, for the profile.

    /// <summary>strings.TrimSpace.</summary>
    internal static string GoTrimSpace(string s)
    {
        var b = Utf8(s);
        var (lo, hi) = TrimSpace(b, 0, b.Length);
        return FromUtf8(b.AsSpan(lo, hi - lo));
    }

    /// <summary>strings.Fields: the runs between unicode.IsSpace characters.</summary>
    internal static List<string> GoFields(string s)
    {
        var b = Utf8(s);
        var output = new List<string>();
        var start = -1;
        var i = 0;
        while (i < b.Length)
        {
            var (r, w) = DecodeRune(b, i, b.Length);
            if (IsSpace(r))
            {
                if (start >= 0)
                {
                    output.Add(FromUtf8(b.AsSpan(start, i - start)));
                    start = -1;
                }
            }
            else if (start < 0)
            {
                start = i;
            }
            i += w;
        }
        if (start >= 0)
        {
            output.Add(FromUtf8(b.AsSpan(start)));
        }
        return output;
    }

    /// <summary>
    /// fmt's %q of a name without white space: quotes and backslashes
    /// escaped, control characters as Go writes them (\a, \b, \f, \v, \xNN;
    /// DEL \x7f). Other non-printable characters are left as they are (the
    /// daemon's names never hold them), as in the Swift port.
    /// </summary>
    internal static string GoQuote(string s)
    {
        var sb = new StringBuilder("\"");
        foreach (var r in s.EnumerateRunes())
        {
            switch (r.Value)
            {
                case '"':
                    sb.Append("\\\"");
                    break;
                case '\\':
                    sb.Append("\\\\");
                    break;
                case 0x07:
                    sb.Append("\\a");
                    break;
                case 0x08:
                    sb.Append("\\b");
                    break;
                case 0x0C:
                    sb.Append("\\f");
                    break;
                case 0x0B:
                    sb.Append("\\v");
                    break;
                default:
                    if (r.Value < 0x20 || r.Value == 0x7F)
                    {
                        sb.Append("\\x").Append(r.Value.ToString("x2", CultureInfo.InvariantCulture));
                    }
                    else
                    {
                        sb.Append(r.ToString());
                    }
                    break;
            }
        }
        return sb.Append('"').ToString();
    }
}
