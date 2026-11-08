// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantTriage.swift
// (triageAutomaticDrafts, triageDrafts(for:), triageAnnotateTool,
// triageMaxRange, clampTriageMax, triageSource, triageTimeout, triageBatch);
// GTK: ui/internal/assistant/triage.go.
//
// The board's triage run (docs/mcp.md "Triage of the board",
// docs/security.md §10.2): the pure half of the one-shot request the
// board's triage controller sends the user's assistant. It is the panel's
// command line (Args: no built-in tool, nothing of the user's setup, no
// session on disk) with the bridge started for the run as the daemon's
// prompt of purpose triage says (docs/api.md §4.18: its bridgeArgs are
// --in-app --allow-triage --triage-run <runId> --triage-max <limit>, never
// --allow-modify or --allow-send, and its tools the bridge's read tools,
// create_draft for a manual run only (TriageAutomaticDrafts) and the three
// triage tools). The procedure and the rules live in the bridge's tool
// descriptions and server instructions; the system prompt and the message
// are the daemon's. Here stay the rules of the run that the client keeps:
// whether a trigger's run may create drafts, the accepted-note parsing, the
// bridge's limit, the run's source, timeout and batch. Swift's
// triageDrafts(for:) takes the board's trigger; here it takes whether the
// run is manual. This file holds no translatable text.

using System;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// The rule (kept when suggested replies became local drafts): an
    /// automatic run creates no drafts. Nobody watches it;
    /// a suggested reply stays on this device as the case's local draft, but
    /// it is still written to the recipients the original names, maybe from
    /// a hostile Reply-To, on a Jira case it is a public comment draft. So an
    /// automatic run gets no create_draft and is told to skip suggested
    /// replies; a manual run, which the user started and watches, keeps it.
    /// </summary>
    public const bool TriageAutomaticDrafts = false;

    /// <summary>
    /// The triage tool whose accepted calls are the run's progress (without
    /// the bridge's prefix, as <see cref="AssistantEvent.Tool"/> names it).
    /// </summary>
    public const string TriageAnnotateTool = "annotate_case";

    // The start of the text of every accepted annotate_case result:
    // "annotated case <caseId>: …" (the bridge's annotateCase).
    private const string TriageAnnotatedPrefix = "annotated case ";

    /// <summary>
    /// The case an accepted annotate_case result names (its text,
    /// <see cref="AssistantEvent.ResultText"/>); null when the text does not
    /// start the bridge's way or the id is not one: 1 to 64 ASCII letters,
    /// digits, "_" and "-". The bridge accepts a second annotation of a case
    /// without charging another of the run's cases, so the run counts the
    /// distinct cases.
    /// </summary>
    public static string? TriageAnnotatedCase(string? result)
    {
        if (result is null || !result.StartsWith(TriageAnnotatedPrefix, StringComparison.Ordinal))
        {
            return null;
        }
        var rest = result[TriageAnnotatedPrefix.Length..];
        var colon = rest.IndexOf(':', StringComparison.Ordinal);
        if (colon <= 0 || colon > 64)
        {
            return null;
        }
        var id = rest[..colon];
        foreach (var c in id)
        {
            if (!(char.IsAsciiLetterOrDigit(c) || c is '_' or '-'))
            {
                return null;
            }
        }
        return id;
    }

    /// <summary>
    /// The run's <c>source</c> for <c>board.runStart</c>: what the bridge
    /// reports as its client's name for the annotations of the run.
    /// </summary>
    public const string TriageSource = "claude-code";

    /// <summary>
    /// The most cases one run is asked to triage (a manual run, and the most
    /// an automatic run takes of the day's remaining cases).
    /// </summary>
    public const int TriageBatch = 40;

    /// <summary>The smallest limit the bridge's <c>--triage-max</c> accepts.</summary>
    public const int TriageMaxLow = 1;

    /// <summary>The largest limit the bridge's <c>--triage-max</c> accepts.</summary>
    public const int TriageMaxHigh = 200;

    /// <summary>How long a triage run may take before it is ended as a timeout.</summary>
    public static readonly TimeSpan TriageTimeout = TimeSpan.FromMinutes(15);

    /// <summary>Whether a run may create drafts: a manual one does, an automatic one only with <see cref="TriageAutomaticDrafts"/>.</summary>
    public static bool TriageDrafts(bool manual) => manual || TriageAutomaticDrafts;

    /// <summary><paramref name="n"/> within <see cref="TriageMaxLow"/>…<see cref="TriageMaxHigh"/>.</summary>
    public static int ClampTriageMax(int n) => Math.Clamp(n, TriageMaxLow, TriageMaxHigh);
}
