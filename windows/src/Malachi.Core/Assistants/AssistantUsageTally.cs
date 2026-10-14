// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantUsage.swift
// (Assistant.UsageTally, maxUsageTokens, addTokens); GTK:
// ui/internal/assistant/usage.go (UsageTally, Finished, LowerBound,
// MaxUsageTokens). Go's Event.UsageFinal (a provider that reports each
// message's whole usage at its end) has no counterpart here: the Windows
// Codex session reports its turn's total with its result, which the tally
// takes, so every message usage counted here is partial. Swift's
// mutating struct is a class here: one tally per run, filled on the UI
// thread. This file holds no translatable text.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Assistants;

/// <summary>
/// Adds up the usage of one run's events (<see cref="Add"/> each event, in
/// order; assistant.UsageTally).
/// </summary>
/// <remarks>
/// Its <see cref="Total"/> is the result's usage when the run reported one;
/// a run that ended without one (cancelled, timed out, killed, stopped at a
/// limit) has the sum over the distinct API messages seen, each counted once
/// with the last usage seen for its id: a lower bound, whose output tokens
/// are the placeholders of the messages' starts. A result whose counters are
/// all 0 while the messages counted some (Claude Code's crash result may be
/// zeroed) gives way to that sum. A new tally is empty.
/// <see cref="LowerBound"/> says whether <see cref="Total"/> is less than
/// the run used.
/// </remarks>
public sealed class AssistantUsageTally
{
    private readonly Dictionary<string, AssistantUsage> messages = new(StringComparer.Ordinal);
    private AssistantUsage? result;
    private readonly HashSet<string> partial = new(StringComparer.Ordinal);
    private bool finished;

    /// <summary>
    /// Whether <see cref="Total"/> is a lower bound of what the run used: true
    /// unless the result's usage was taken (or nothing was counted); the
    /// messages' sum stays a lower bound even after <see cref="Finished"/>,
    /// since no message usage counted here is final.
    /// </summary>
    public bool LowerBound => !ResultTaken && messages.Count > 0 && (!finished || partial.Count > 0);

    /// <summary>Says the run's final report came (the request answered).</summary>
    public void Finished() => finished = true;

    // Whether Total is the result's usage.
    private bool ResultTaken => result is { } r && (!r.IsZero || Sum().IsZero);

    // The usage of the messages counted.
    private AssistantUsage Sum()
    {
        var sum = default(AssistantUsage);
        foreach (var u in messages.Values)
        {
            sum = new AssistantUsage(
                AddTokens(sum.InputTokens, u.InputTokens),
                AddTokens(sum.OutputTokens, u.OutputTokens),
                AddTokens(sum.CacheCreationInputTokens, u.CacheCreationInputTokens),
                AddTokens(sum.CacheReadInputTokens, u.CacheReadInputTokens));
        }
        return sum;
    }

    /// <summary>
    /// The run's usage, each counter at most
    /// <see cref="Assistant.MaxUsageTokens"/>; null when nothing reported any.
    /// </summary>
    public AssistantUsage? Total
    {
        get
        {
            var sum = Sum();
            if (ResultTaken && result is { } r)
            {
                return new AssistantUsage(
                    AddTokens(0, r.InputTokens),
                    AddTokens(0, r.OutputTokens),
                    AddTokens(0, r.CacheCreationInputTokens),
                    AddTokens(0, r.CacheReadInputTokens));
            }
            return messages.Count == 0 ? null : sum;
        }
    }

    /// <summary>Counts the usage <paramref name="e"/> carries, if any.</summary>
    public void Add(AssistantEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        if (e.Usage is not { } u)
        {
            return;
        }
        if (e.Kind == AssistantEventKind.Result)
        {
            result = u;
            return;
        }
        if (e.MessageId.Length == 0)
        {
            return;
        }
        messages[e.MessageId] = u;
        // Not a message's final usage (see the file's note).
        partial.Add(e.MessageId);
    }

    /// <summary>a + b of two counters from 0 up, at most <see cref="Assistant.MaxUsageTokens"/>.</summary>
    private static long AddTokens(long a, long b)
    {
        const long m = Assistant.MaxUsageTokens;
        return Math.Min(Math.Min(Math.Max(a, 0), m) + Math.Min(Math.Max(b, 0), m), m);
    }
}
