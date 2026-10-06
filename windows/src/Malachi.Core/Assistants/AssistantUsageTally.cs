// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantUsage.swift
// (Assistant.UsageTally, maxUsageTokens, addTokens); GTK:
// ui/internal/assistant/usage.go (UsageTally, MaxUsageTokens). Swift's
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
/// </remarks>
public sealed class AssistantUsageTally
{
    private readonly Dictionary<string, AssistantUsage> messages = new(StringComparer.Ordinal);
    private AssistantUsage? result;

    /// <summary>
    /// The run's usage, each counter at most
    /// <see cref="Assistant.MaxUsageTokens"/>; null when nothing reported any.
    /// </summary>
    public AssistantUsage? Total
    {
        get
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
            if (result is { } r && (!r.IsZero || sum.IsZero))
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
    }

    /// <summary>a + b of two counters from 0 up, at most <see cref="Assistant.MaxUsageTokens"/>.</summary>
    private static long AddTokens(long a, long b)
    {
        const long m = Assistant.MaxUsageTokens;
        return Math.Min(Math.Min(Math.Max(a, 0), m) + Math.Min(Math.Max(b, 0), m), m);
    }
}
