// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraTransitionsTests.swift, the
// counterpart of ui/internal/jira/transitions_test.go (TestCanTransition,
// TestTransitions, TestTransitionsCap, TestTransitionTexts,
// TestStatusChanged, TestTransitionFailed), with the English catalogue; the
// Czech hint and toast are in JiraTranslationTests.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed class JiraTransitionsTests
{
    private static Account Account(Capability[]? capabilities) => new()
    {
        Id = "j",
        Config = new AccountConfig { Name = "", Email = "" },
        Enabled = true,
        State = new SyncState { AccountId = "j", Status = SyncStatus.Idle },
        Capabilities = capabilities,
    };

    private static IssueInfo Issue(string status) => new() { Key = "ITSD-42", Url = "", Summary = "", Status = status };

    private static IssueTransition T(string id, string name, string to, bool? needsInput = null) =>
        new() { Id = id, Name = name, To = to, NeedsInput = needsInput };

    [Fact]
    public void CanTransition()
    {
        Assert.True(Jira.CanTransition(Account([Capability.Comment, Capability.Forward, Capability.Transition])));
        Assert.False(Jira.CanTransition(Account([Capability.Comment, Capability.Forward])));
        Assert.False(Jira.CanTransition(Account([]))); // an empty list offers nothing
        Assert.False(Jira.CanTransition(Account(null))); // a mail account never changes statuses
    }

    internal static IssueTransitionsResult Sample() => new()
    {
        Issue = Issue("To Do") with { StatusCategory = IssueStatusCategory.Todo },
        Transitions =
        [
            T("11", "Start Progress", "In Progress") with { ToCategory = IssueStatusCategory.InProgress },
            T("21", "Done", "Done") with { ToCategory = IssueStatusCategory.Done },
            T("31", "Resolve", "Resolved", true),
            T("41", " " + JiraTests.Rlo + "Escalate\n", "escalated" + JiraTests.Zwsp, true),
            T("  ", "No id", "Nowhere"),
            T("51", "", "Closed"),
            T("61", JiraTests.Zwsp + JiraTests.Shy, JiraTests.Rlo),
            T("71", "DONE", "Done"),
            T("81", "Back to the backlog", "to do"),
        ],
    };

    [Fact]
    public void Transitions()
    {
        Assert.Equal<JiraTransitionItem>(
            [
                new() { Id = "11", Title = "Start Progress", Target = "In Progress", Subtitle = "In Progress", Enabled = true },
                new() { Id = "21", Title = "Done", Target = "Done", Enabled = true },
                new() { Id = "31", Title = "Resolve", Target = "Resolved", Subtitle = "Resolved", Hint = "Needs fields in Jira" },
                new() { Id = "41", Title = "Escalate", Target = "escalated", Subtitle = "escalated", Hint = "Needs fields in Jira" },
                new() { Id = "51", Title = "Closed", Target = "Closed", Enabled = true },
                new() { Id = "71", Title = "DONE", Target = "Done", Enabled = true },
            ],
            Jira.Transitions(Sample()));
        Assert.Empty(Jira.Transitions(new IssueTransitionsResult { Issue = Issue("") }));
    }

    [Fact]
    public void TransitionsCap()
    {
        var res = new IssueTransitionsResult
        {
            Issue = Issue(""),
            Transitions = [.. Enumerable.Range(0, API.Limits.MaxIssueTransitions + 5).Select(i => T(new string('1', i + 1), "t", ""))],
        };
        Assert.Equal(API.Limits.MaxIssueTransitions, Jira.Transitions(res).Count);
    }

    [Fact]
    public void TransitionTexts()
    {
        Assert.Equal("Change Status", Jira.ChangeStatusLabel());
        Assert.Equal("Needs fields in Jira", Jira.NeedsInputHint());
        Assert.Equal("Loading…", Jira.TransitionsLoading());
        Assert.Equal("No status change is available", Jira.NoTransitions());
        Assert.Equal("Loading the status changes", Jira.LoadTransitionsAction());
        Assert.Equal("Changing the status", Jira.TransitionAction());
    }

    public static TheoryData<string, JiraTransitionItem, string, string> StatusChangedCases => new()
    {
        { "the target wins", new() { Title = "Start Progress", Target = "In Progress" }, "In Progress", "Status changed to In Progress" },
        { "even over a stale issue", new() { Title = "Start Progress", Target = "In Progress" }, "To Do", "Status changed to In Progress" },
        { "no target: the issue's status", new() { Title = "Start Progress" }, "In Progress", "Status changed to In Progress" },
        { "neither: the transition's name", new() { Title = "Start Progress" }, JiraTests.Rlo + " ", "Status changed to Start Progress" },
    };

    [Theory]
    [MemberData(nameof(StatusChangedCases))]
    public void StatusChanged(string name, JiraTransitionItem chosen, string issueStatus, string want) =>
        Assert.True(want == Jira.StatusChanged(chosen, Issue(issueStatus)), $"{name}: {Jira.StatusChanged(chosen, Issue(issueStatus))}");

    private const string Fallback = "Changing the status failed: the server returned an error";

    public static TheoryData<string, int, string, string> FailedCases => new()
    {
        { "the site's reason", ErrorCode.ServerError, "Transition is not allowed by the workflow", "The status could not be changed: Transition is not allowed by the workflow" },
        { "cleaned", ErrorCode.ServerError, " " + JiraTests.Rlo + "Not\nallowed" + JiraTests.Zwsp, "The status could not be changed: Not allowed" },
        { "no reason: the usual sentence", ErrorCode.ServerError, JiraTests.Zwsp, Fallback },
        { "another code: the usual sentence", ErrorCode.InvalidArgument, "needs input", Fallback },
        { "network", ErrorCode.NetworkError, "dial tcp: refused", Fallback },
    };

    [Theory]
    [MemberData(nameof(FailedCases))]
    public void TransitionFailed(string name, int code, string message, string want) =>
        Assert.True(want == Jira.TransitionFailed(new ErrorCode(code), message, Fallback), $"{name}: {Jira.TransitionFailed(new ErrorCode(code), message, Fallback)}");

    [Fact]
    public void TransitionFailedCapsTheReason()
    {
        var longText = Jira.TransitionFailed(ErrorCode.ServerError, new string('x', 512 + 10), Fallback);
        Assert.True(Encoding.UTF8.GetByteCount(longText) <= "The status could not be changed: ".Length + 512, $"{longText.Length} bytes");
    }

    // Windows addition: Swift's transitionFailed(_ error:), the toast for
    // whatever the call threw.
    [Fact]
    public void TransitionFailedOfAnError()
    {
        var refused = new RpcException(new RpcError { Code = ErrorCode.ServerError, Message = "Not allowed" });
        Assert.Equal("The status could not be changed: Not allowed", Jira.TransitionFailed(refused));
        var other = new RpcException(new RpcError { Code = ErrorCode.InvalidArgument, Message = "needs input" });
        Assert.StartsWith("Changing the status", Jira.TransitionFailed(other), StringComparison.Ordinal);
        Assert.StartsWith("Changing the status", Jira.TransitionFailed(new TimeoutException()), StringComparison.Ordinal);
    }
}
