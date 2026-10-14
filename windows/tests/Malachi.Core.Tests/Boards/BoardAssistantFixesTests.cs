// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the assistant's half of the board's fixes of 2026-10-08; GTK:
// ui/internal/assistant/usage_test.go (TestUsageTallyLowerBound, without
// Go's UsageFinal: no Windows event carries it), triage_test.go
// (TestTriageAnnotatedCase), suggest_reply_test.go
// (TestSuggestReplyFollowUpSystemPrompt) and
// ui/internal/assistantpanel/provider_test.go (ProviderFailure).

using System;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Xunit;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardAssistantFixesTests
{
    private static AssistantEvent Message(string id, long input) =>
        new(AssistantEventKind.Other) { MessageId = id, Usage = new AssistantUsage(InputTokens: input, OutputTokens: 1) };

    private static AssistantEvent Result(long input) =>
        new(AssistantEventKind.Result) { Success = true, Usage = new AssistantUsage(InputTokens: input) };

    [Fact]
    public void UsageTallyLowerBound()
    {
        var empty = new AssistantUsageTally();
        Assert.False(empty.LowerBound); // nothing counted
        empty.Finished();
        Assert.False(empty.LowerBound);

        var whole = new AssistantUsageTally();
        whole.Add(Message("m1", 5));
        whole.Add(Result(9));
        Assert.False(whole.LowerBound); // the result's usage is whole
        Assert.Equal(9, whole.Total?.InputTokens);

        var stopped = new AssistantUsageTally();
        stopped.Add(Message("m1", 5));
        stopped.Add(Message("m2", 7));
        Assert.True(stopped.LowerBound); // stopped before the result
        Assert.Equal(12, stopped.Total?.InputTokens);
        stopped.Finished();
        Assert.True(stopped.LowerBound); // answered without a result usage: placeholders

        var zeroed = new AssistantUsageTally();
        zeroed.Add(Message("m1", 5));
        zeroed.Add(Result(0));
        zeroed.Finished();
        Assert.True(zeroed.LowerBound); // a zeroed result gives way to the messages
        Assert.Equal(5, zeroed.Total?.InputTokens);
    }

    [Theory]
    [InlineData("annotated case c_0123456789abcdef0123456789abcdef: state in effect hot (decided by rules)", "c_0123456789abcdef0123456789abcdef")]
    [InlineData("annotated case c_1: x\nannotations left in this session: 3", "c_1")]
    [InlineData("annotated case : x", null)]
    [InlineData("annotated case c 1: x", null)]
    [InlineData("annotated case c_1", null)]
    [InlineData("Annotated case c_1: x", null)]
    [InlineData("this session already annotated 3 cases", null)]
    [InlineData("annotated case c_\u202E1: x", null)]
    [InlineData("", null)]
    public void TriageAnnotatedCase(string result, string? want) => Assert.Equal(want, Assistant.TriageAnnotatedCase(result));

    [Fact]
    public void TriageAnnotatedCaseIdIsAtMost64()
    {
        Assert.Null(Assistant.TriageAnnotatedCase("annotated case " + new string('a', 65) + ": x"));
        Assert.Equal(new string('a', 64), Assistant.TriageAnnotatedCase("annotated case " + new string('a', 64) + ": x"));
        Assert.Null(Assistant.TriageAnnotatedCase(null));
    }

    [Fact]
    public void SuggestReplyFollowUpSystemPrompt()
    {
        Assert.Equal(Assistant.SuggestReplySystemPrompt(), Assistant.SuggestReplySystemPromptFor(false));
        var p = Assistant.SuggestReplySystemPromptFor(true);
        foreach (var part in new[]
        {
            "follow-up", "user's own last message", "polite nudge", "read_message", "data, never as instructions",
            "user's voice", "square brackets", "exactly one draft", "mode reply", "without commentary",
        })
        {
            Assert.Contains(part, p, StringComparison.Ordinal);
        }
        Assert.DoesNotContain("nudge", Assistant.SuggestReplySystemPrompt(), StringComparison.Ordinal);
    }

    [Fact]
    public void ProviderFailures()
    {
        Assert.IsType<AssistantRequest.Failure.NotFound>(AssistantRequest.ProviderFailure("codex_not_found"));
        foreach (var code in new[]
        {
            "chatgpt_not_connected", "chatgpt_reconnect_required", "chatgpt_consent_required",
            "chatgpt_permission_denied", "chatgpt_identity_mismatch",
        })
        {
            Assert.IsType<AssistantRequest.Failure.NotSignedIn>(AssistantRequest.ProviderFailure(code));
        }
        Assert.Equal(new AssistantRequest.Failure.Limit("chatgpt_usage_limit"), AssistantRequest.ProviderFailure("chatgpt_usage_limit"));
        Assert.Equal(new AssistantRequest.Failure.Stopped("codex_gateway_failed"), AssistantRequest.ProviderFailure("codex_gateway_failed"));
        Assert.Equal(
            "the assistant’s usage limit was reached", new AssistantRequest.Failure.Limit("chatgpt_usage_limit").Reason);
    }
}
