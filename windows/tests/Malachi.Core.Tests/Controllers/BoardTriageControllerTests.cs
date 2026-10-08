// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardTriageControllerTests.swift and
// of ui/internal/boardtriage/controller_test.go and provider_test.go, whose
// Go-only cases end the file (TestAutoPauseInTheView,
// TestRunCapturesProviderSource; TestLateBoardConsentDoesNotApproveNewProvider
// is Swift's providerSwitchDuringConsentWriteCannotApproveNewProvider): the
// board's triage run against the fake daemon of the preferences' tests
// (BoardTriageDaemon: preferences, board.runStart, board.runEnd) and the
// stand-in claude.exe of Malachi.FakeClaude. No real Claude Code, daemon
// or bridge is ever run: the bridge's path is only passed on.
//
// Windows differences, as AssistantRequestTests': the run's timeout, the
// grace at its limit, the clock of relative times, cancelAndEnd's bound and
// the kill of a claude that does not end run on a FakeTimeProvider, so
// Swift's "sleep 30" is FakeClaudeStep.Hang and a timeout is the clock
// advanced once the request is known to wait (its events arrived, or its
// prompt was read and the UI turn that sent it is over). Swift's short real
// sleeps that check that nothing else happens are a wait until everything
// is idle. The tests wait for a state on the UI thread where Swift polls.
// Swift's fixed now is the fake clock's start; a state's time is the clock's
// when it ended.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Assistants;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Platform;
using Malachi.FakeClaude;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Boards.Board;
using C = Malachi.Core.Tests.Assistants.CannedStreamJson;
using D = Malachi.Core.Tests.Controllers.BoardTriageDaemon;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class BoardTriageControllerTests
{
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_848_800);

    private static readonly TimeSpan KillGrace = TimeSpan.FromMilliseconds(300);

    private static void RequireWindows() => Assert.SkipUnless(OperatingSystem.IsWindows(), "the stand-in claude is a Windows program");

    /// <summary>An annotate_case the bridge accepted (<paramref name="ok"/>) or refused.</summary>
    private static string[] Annotate(string id, bool ok = true) =>
        [C.ToolUse(id, "annotate_case"), C.ToolResult(id, ok ? "annotated" : "conflict", error: !ok)];

    /// <summary>
    /// An annotate_case call in an API message <paramref name="msg"/> that
    /// reports <paramref name="input"/> input and <paramref name="read"/>
    /// cache-read tokens (output 1, cache writes 10), with the bridge's acceptance.
    /// </summary>
    private static string[] AnnotateUsing(string id, string msg, int input, int read) =>
    [
        $$$"""{"type":"assistant","message":{"id":"{{{msg}}}","role":"assistant","content":[{"type":"tool_use","id":"{{{id}}}","name":"mcp__malachi__annotate_case","input":{}}],"usage":{"input_tokens":{{{input}}},"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":{{{read}}}}},"parent_tool_use_id":null}""",
        C.ToolResult(id, "annotated"),
    ];

    /// <summary>A result line with its usage; <paramref name="success"/> false is an error result.</summary>
    private static string ResultUsing(int input, int output, int write, int read, bool success = true) =>
        $$$"""{"type":"result","subtype":"{{{(success ? "success" : "error_during_execution")}}}","is_error":{{{(success ? "false" : "true")}}},"result":"ok","usage":{"input_tokens":{{{input}}},"output_tokens":{{{output}}},"cache_creation_input_tokens":{{{write}}},"cache_read_input_tokens":{{{read}}}}}""";

    /// <summary>A turn of <paramref name="lines"/>, then <paramref name="then"/> (Swift's shell snippet).</summary>
    private static IReadOnlyList<FakeClaudeStep> Turn(IEnumerable<string> lines, params FakeClaudeStep[] then) =>
        [FakeClaudeStep.Lines([.. lines]), .. then];

    private static FakeClaudeScript Fake(params IReadOnlyList<FakeClaudeStep>[] turns) => new(turns);

    private static string[] Lines(params IEnumerable<string>[] parts) => [.. parts.SelectMany(p => p)];

    /// <summary>The <c>--model</c> of a command line.</summary>
    private static string? Model(IReadOnlyList<string> args)
    {
        var i = args.ToList().IndexOf("--model");
        return i >= 0 && i + 1 < args.Count ? args[i + 1] : null;
    }

    private static BoardRunEndParams End(BoardRunError? error = null, BoardUsage? usage = null) =>
        new() { RunId = new BoardRunId("run_1"), Error = error, Usage = usage };

    private static BoardUsage Usage(long input, long output, long write, long read) =>
        new() { InputTokens = input, OutputTokens = output, CacheCreationInputTokens = write, CacheReadInputTokens = read };

    private static AssistantOptions Options(int max, bool drafts = true) => new()
    {
        Bridge = "/b/malachi-mcp",
        Socket = "/s.sock",
        Model = AssistantModel.Opus,
        SystemPrompt = Assistant.TriageSystemPrompt("Czech", "2026-10-01"),
        BridgeArgs = ["--allow-triage", "--triage-run", "run_1", "--triage-max", max.ToString(System.Globalization.CultureInfo.InvariantCulture)],
        Tools = Assistant.TriageTools(drafts),
    };

    // A run

    /// <summary>A manual run: runStart, the request with the bridge for the run, progress from the accepted annotate_case calls, runEnd, a refresh.</summary>
    [Fact]
    public async Task ManualRun()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(Lines(
            [C.Init, C.ToolUse("q", "list_triage_queue"), C.ToolResult("q", "3 cases")],
            Annotate("a1"), Annotate("a2", ok: false), Annotate("a3"), [C.Text("Done."), C.Result("Done.")]))));
        await h.BoardAsync(queue: 3);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.C.Start(TriageTrigger.Manual));
            Assert.Equal(new TriageState.Starting(TriageTrigger.Manual), h.C.State);
        });
        await h.EndedAsync();
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 2, 1, T0), await h.StateAsync());
        Assert.Equal(
            [
                new TriageState.Starting(TriageTrigger.Manual), new TriageState.Running(TriageTrigger.Manual, 0, 3),
                new TriageState.Running(TriageTrigger.Manual, 1, 3), new TriageState.Running(TriageTrigger.Manual, 2, 3),
                new TriageState.Finished(TriageTrigger.Manual, 2, 1, T0),
            ],
            await h.Ui.RunAsync(() => h.States.Where(s => s is not TriageState.Idle).ToArray()));
        Assert.Equal(
            "Triage finished: 2 conversations refined. The board refused 1 of the assistant’s notes.",
            await h.Ui.RunAsync(() => h.C.View.Result));
        Assert.Equal([new BoardRunStartParams { Trigger = BoardTrigger.Manual, Source = "claude-code" }], h.D.RunStartCalls);
        Assert.Equal([End()], h.D.RunEndCalls);
        await h.IdleAsync();
        Assert.Equal(1, h.Refreshes);
        Assert.Equal([new BoardTriageEnd(TriageTrigger.Manual, null)], h.Ends);
        // The command line: the bridge for this run and its limit, the
        // triage's tools with create_draft.
        var args = FakeClaudeScript.Args(h.Dir.Path);
        Assert.Equal(Assistant.Args(Options(40)), args);
        Assert.Contains("mcp__malachi__create_draft", string.Join(' ', args), StringComparison.Ordinal);
        Assert.Equal([Assistant.TriageMessage(40)], FakeClaudeScript.Prompts(h.Dir.Path));
        Assert.True(await h.Ui.RunAsync(() => h.C.SignedIn));
        // Nothing of consent was asked or written.
        Assert.Empty(h.D.SetCalls);
    }

    /// <summary>
    /// A second note on the same case costs the run no case (the bridge's
    /// rule): Done counts distinct cases (Go TestReannotationCountsOnce).
    /// </summary>
    [Fact]
    public async Task ReannotationCountsOnce()
    {
        RequireWindows();
        static string[] Same(string id) => [C.ToolUse(id, "annotate_case"), C.ToolResult(id, "annotated case c_1: state in effect hot")];
        await using var h = await Harness.StartAsync(Fake(Turn(Lines([C.Init], Same("a1"), Same("a2"), Annotate("a3"), [C.Result("ok")]))));
        await h.BoardAsync(queue: 3);
        await h.Ui.RunAsync(() => Assert.True(h.C.Start(TriageTrigger.Manual)));
        await h.EndedAsync();
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 2, 0, T0), await h.StateAsync());
    }

    /// <summary>
    /// An automatic run asks for its limit; the progress counts against the
    /// smaller of the queue and the limit. It gets no create_draft and is
    /// told so.
    /// </summary>
    [Fact]
    public async Task AutomaticRun()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(Lines([C.Init], Annotate("a1"), [C.Result("ok")]))));
        await h.BoardAsync(queue: 12);
        Assert.True(await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 5)));
        await h.EndedAsync();
        Assert.Contains(new TriageState.Running(TriageTrigger.Automatic, 0, 5), await h.Ui.RunAsync(() => h.States.ToArray()));
        Assert.Equal(new TriageState.Finished(TriageTrigger.Automatic, 1, 0, T0), await h.StateAsync());
        Assert.Equal([new BoardRunStartParams { Trigger = BoardTrigger.Auto, Source = "claude-code" }], h.D.RunStartCalls);
        Assert.Equal([Assistant.TriageMessage(5, drafts: false)], FakeClaudeScript.Prompts(h.Dir.Path));
        var args = FakeClaudeScript.Args(h.Dir.Path);
        Assert.Equal(Assistant.Args(Options(5, drafts: false)), args);
        Assert.DoesNotContain("create_draft", string.Join(' ', args), StringComparison.Ordinal);
    }

    /// <summary>
    /// The run's limit is a hard one: once the accepted notes reach it the run
    /// has succeeded, whatever the model does next (later tool calls are not
    /// counted); without a result in the grace period it ends so.
    /// </summary>
    [Fact]
    public async Task LimitEndsTheRun()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(
            Lines([C.Init], Annotate("a1"), Annotate("a2", ok: false), Annotate("a3"), Annotate("a4"), Annotate("a5", ok: false)),
            FakeClaudeStep.Hang())));
        await h.Ui.RunAsync(() => h.C.Grace = TimeSpan.FromMilliseconds(300));
        await h.BoardAsync(queue: 12);
        Assert.True(await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 2)));
        await h.UntilAsync(() => h.C.State is TriageState.Running { Done: 2 });
        h.Clock.Advance(TimeSpan.FromMilliseconds(300));
        await h.EndedAsync();
        Assert.Equal(new TriageState.Finished(TriageTrigger.Automatic, 2, 1, T0.AddMilliseconds(300)), await h.StateAsync());
        Assert.DoesNotContain(await h.Ui.RunAsync(() => h.States.ToArray()), s => s is TriageState.Running { Done: 3 });
        Assert.Equal([End()], h.D.RunEndCalls);
        Assert.Equal([new BoardTriageEnd(TriageTrigger.Automatic, null)], h.Ends);
        Assert.True(await h.Ui.RunAsync(() => h.C.SignedIn));
    }

    /// <summary>The run takes the board's own model, not the panel's, read when it starts: a change applies to the next run, not to the one under way.</summary>
    [Fact]
    public async Task BoardsOwnModel()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn([C.Init], FakeClaudeStep.Sleep(300), FakeClaudeStep.Lines(C.Result("ok")))));
        await h.BoardAsync(queue: 3);
        Assert.Equal(AssistantModel.Haiku, h.Settings.AssistantModel);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await Eventually.Holds(() => FakeClaudeScript.Starts(h.Dir.Path) == 1 && FakeClaudeScript.Args(h.Dir.Path).Count > 0);
        await h.Ui.RunAsync(() => h.Settings.BoardTriageModel = AssistantModel.Sonnet);
        await h.EndedAsync();
        Assert.Equal("opus", Model(FakeClaudeScript.Args(h.Dir.Path)));
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 0, 0, T0), await h.StateAsync());
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 3));
        await h.EndedAsync();
        Assert.Equal(2, FakeClaudeScript.Starts(h.Dir.Path));
        Assert.Equal("sonnet", Model(FakeClaudeScript.Args(h.Dir.Path)));
        Assert.Equal(AssistantModel.Haiku, h.Settings.AssistantModel);
    }

    public static TheoryData<string, TriageTrigger, string[], TriageFailure?> EmptyRunCases => new()
    {
        { "refused, automatic", TriageTrigger.Automatic, Lines(Annotate("a1", ok: false), Annotate("a2", ok: false)), TriageFailure.NotesRefused },
        { "refused, manual", TriageTrigger.Manual, Annotate("a1", ok: false), TriageFailure.NotesRefused },
        { "nothing, automatic", TriageTrigger.Automatic, [], TriageFailure.NoProgress },
        { "nothing, manual", TriageTrigger.Manual, [], null },
    };

    /// <summary>
    /// A run that ends with no note accepted: refused notes fail it (both
    /// triggers); an automatic run whose queue had cases and that tried
    /// nothing fails as no progress (the schedule backs off); a manual run
    /// that tried nothing has simply finished.
    /// </summary>
    [Theory]
    [MemberData(nameof(EmptyRunCases))]
    public async Task EmptyRuns(string name, TriageTrigger trigger, string[] lines, TriageFailure? failure)
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(Lines([C.Init], lines, [C.Result("ok")]))));
        await h.BoardAsync(queue: 3);
        await h.Ui.RunAsync(() => h.C.Start(trigger, 3));
        await h.EndedAsync();
        TriageState want = failure is { } f ? new TriageState.Failed(trigger, f, T0) : new TriageState.Finished(trigger, 0, 0, T0);
        Assert.True(want == await h.StateAsync(), name);
        var error = failure is null ? default(BoardRunError?) : new BoardRunError(BoardRunError.Failed);
        Assert.Equal([End(error)], h.D.RunEndCalls);
        if (failure is { } g)
        {
            Assert.True(AutoTriage.CountsAsFailure(g), name);
        }
        Assert.Equal("Triage failed: the board refused the assistant’s notes.", Board.Text.TriageFailed(TriageFailure.NotesRefused));
        Assert.Equal("Triage failed: the assistant added no notes.", Board.Text.TriageFailed(TriageFailure.NoProgress));
    }

    /// <summary>A queue the board has not reported with the assistant on is not known: the run asks for its limit and counts against it.</summary>
    [Fact]
    public async Task UnknownQueue()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.Turn(C.Init, C.Result("ok"))));
        await h.BoardAsync(queue: 0, assistantOn: false);
        Assert.True(await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual)));
        await h.EndedAsync();
        Assert.Contains(new TriageState.Running(TriageTrigger.Manual, 0, 40), await h.Ui.RunAsync(() => h.States.ToArray()));
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 0, 0, T0), await h.StateAsync());
    }

    /// <summary>One run at a time.</summary>
    [Fact]
    public async Task OneRunAtATime()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(Lines([C.Init], Annotate("a1")), FakeClaudeStep.Hang())));
        await h.BoardAsync(queue: 2);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.C.Start(TriageTrigger.Manual));
            Assert.False(h.C.Start(TriageTrigger.Manual));
            Assert.False(h.C.Start(TriageTrigger.Automatic, 3));
        });
        await h.UntilAsync(() => h.C.State == new TriageState.Running(TriageTrigger.Manual, 1, 2));
        Assert.False(await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual)));
        Assert.Single(h.D.RunStartCalls);
        Assert.Equal(1, FakeClaudeScript.Starts(h.Dir.Path));
    }

    /// <summary>Stop: the request ends, the run is recorded as cancelled, the board asked again; nothing of the request comes later.</summary>
    [Fact]
    public async Task Cancel()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(Lines([C.Init], Annotate("a1")), FakeClaudeStep.Hang())));
        await h.BoardAsync(queue: 4);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.UntilAsync(() => h.C.State == new TriageState.Running(TriageTrigger.Manual, 1, 4));
        await h.Ui.RunAsync(() =>
        {
            h.C.Cancel();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Cancelled, T0), h.C.State);
        });
        await h.EndedAsync();
        Assert.Equal([End(BoardRunError.Cancelled)], h.D.RunEndCalls);
        // The process ends at the kill; nothing of it comes later.
        h.Clock.Advance(KillGrace);
        await h.IdleAsync();
        Assert.Equal(1, h.Refreshes);
        Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Cancelled, T0), await h.StateAsync());
        Assert.Single(h.Ends);
    }

    /// <summary>Cancelled while board.runStart is on its way: the run it started is ended as cancelled, and no request starts.</summary>
    [Fact]
    public async Task CancelWhileStarting()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")));
        await h.BoardAsync(queue: 1);
        h.D.RunStarts.Hold(true);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await D.UntilAsync(h.Ui, () => h.D.RunStarts.Waiting == 1);
        await h.Ui.RunAsync(h.C.Cancel);
        h.D.RunStarts.Hold(false);
        await D.UntilAsync(h.Ui, () => h.D.RunEndCalls.Count == 1);
        Assert.Equal([End(BoardRunError.Cancelled)], h.D.RunEndCalls);
        Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Cancelled, T0), await h.StateAsync());
        await h.IdleAsync();
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
    }

    // Failures

    /// <summary>What fails before the daemon's run starts records no run.</summary>
    [Fact]
    public async Task FailuresBeforeTheRun()
    {
        RequireWindows();
        // Claude Code not found.
        await using (var h1 = await Harness.StartAsync(Fake(C.AnswerTurn("x"))))
        {
            h1.Flags.Found = false;
            await h1.BoardAsync(queue: 1);
            await h1.Ui.RunAsync(() => h1.C.Start(TriageTrigger.Manual));
            await h1.EndedAsync();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.NotFound, T0), await h1.StateAsync());
            Assert.Empty(h1.D.RunStartCalls);
        }
        // Signed out.
        await using (var h2 = await Harness.StartAsync(new FakeClaudeScript([C.AnswerTurn("x")], auth: FakeClaudeScript.SignedIn(false))))
        {
            await h2.BoardAsync(queue: 1);
            await h2.Ui.RunAsync(() => h2.C.Start(TriageTrigger.Automatic, 3));
            await h2.EndedAsync();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Automatic, TriageFailure.NotSignedIn, T0), await h2.StateAsync());
            Assert.False(await h2.Ui.RunAsync(() => h2.C.SignedIn));
            Assert.Empty(h2.D.RunStartCalls);
            Assert.Equal(0, FakeClaudeScript.Starts(h2.Dir.Path));
        }
        // No bridge.
        await using (var h3 = await Harness.StartAsync(Fake(C.AnswerTurn("x")), bridge: null))
        {
            await h3.Ui.RunAsync(() => h3.C.Start(TriageTrigger.Manual));
            await h3.EndedAsync();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.ToolsMissing, T0), await h3.StateAsync());
        }
        // The assistant off.
        await using (var h4 = await Harness.StartAsync(Fake(C.AnswerTurn("x"))))
        {
            h4.Flags.Available = false;
            await h4.Ui.RunAsync(() => h4.C.Start(TriageTrigger.Manual));
            await h4.EndedAsync();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.AssistantOff, T0), await h4.StateAsync());
        }
        // Nothing waits.
        await using (var h5 = await Harness.StartAsync(Fake(C.AnswerTurn("x"))))
        {
            await h5.BoardAsync(queue: 0);
            await h5.Ui.RunAsync(() => h5.C.Start(TriageTrigger.Manual));
            await h5.EndedAsync();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.NothingToDo, T0), await h5.StateAsync());
            Assert.Empty(h5.D.RunStartCalls);
        }
        // The daemon refuses the run.
        await using (var h6 = await Harness.StartAsync(Fake(C.AnswerTurn("x"))))
        {
            h6.D.RunStartFailure = D.Error(ErrorCode.StorageError, "disk");
            await h6.BoardAsync(queue: 1);
            await h6.Ui.RunAsync(() => h6.C.Start(TriageTrigger.Manual));
            await h6.EndedAsync();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Backend, T0), await h6.StateAsync());
            Assert.Empty(h6.D.RunEndCalls);
            Assert.Equal(0, FakeClaudeScript.Starts(h6.Dir.Path));
        }
    }

    public static TheoryData<string, int, TriageFailure, string> DuringTheRunCases => new()
    {
        { "the bridge not connected", 0, TriageFailure.ToolsMissing, BoardRunError.Failed },
        { "an error result", 1, TriageFailure.Stopped, BoardRunError.Failed },
        { "Claude Code exits", 2, TriageFailure.Stopped, BoardRunError.Failed },
        { "the API refused the sign-in", 3, TriageFailure.NotSignedIn, BoardRunError.SignedOut },
        { "too long", 4, TriageFailure.Timeout, BoardRunError.Timeout },
    };

    /// <summary>What fails during the run is recorded with its class.</summary>
    [Theory]
    [MemberData(nameof(DuringTheRunCases))]
    public async Task FailuresDuringTheRun(string name, int turn, TriageFailure failure, string runError)
    {
        RequireWindows();
        IReadOnlyList<FakeClaudeStep>[] turns =
        [
            C.Turn(C.InitFailed, C.Result("x")),
            C.Turn(C.Init, C.Result("error_max_turns", success: false)),
            Turn([C.Init], FakeClaudeStep.Exit(3)),
            C.Turn(C.Init, C.Failure("authentication_failed", "Please run /login"), C.Result("x", success: false)),
            Turn([C.Init], FakeClaudeStep.Hang()),
        ];
        await using var h = await Harness.StartAsync(Fake(turns[turn]));
        var timesOut = failure == TriageFailure.Timeout;
        if (timesOut)
        {
            await h.Ui.RunAsync(() => h.C.Timeout = TimeSpan.FromMilliseconds(500));
        }
        await h.BoardAsync(queue: 2);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 2));
        var end = T0;
        if (timesOut)
        {
            await h.RequestWaitsAsync();
            h.Clock.Advance(TimeSpan.FromMilliseconds(500));
            end = T0.AddMilliseconds(500);
        }
        await h.EndedAsync();
        Assert.True(new TriageState.Failed(TriageTrigger.Automatic, failure, end) == await h.StateAsync(), name);
        Assert.Equal([End(new BoardRunError(runError))], h.D.RunEndCalls);
        await h.IdleWithKillAsync();
        Assert.Equal(1, h.Refreshes);
    }

    // Consent

    /// <summary>
    /// Without consent a manual run asks; declined, nothing is written or
    /// started; allowed, both consents are kept and the assistant preference
    /// goes on before the run.
    /// </summary>
    [Fact]
    public async Task Consent()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.Turn(C.Init, C.Result("ok"))), consent: false, prefs: D.Prefs());
        Assert.True(await h.Ui.RunAsync(() => h.C.NeedsConsent && !h.C.ConsentGiven && h.C.View.NeedsConsent));
        // No sheet: declined.
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.EndedAsync();
        Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Declined, T0), await h.StateAsync());
        // Declined in the sheet.
        var asked = 0;
        await h.Ui.RunAsync(() => h.C.Consent = () =>
        {
            asked++;
            return Task.FromResult(false);
        });
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.EndedAsync();
        Assert.Equal(1, asked);
        Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Declined, T0), await h.StateAsync());
        Assert.True(await h.Ui.RunAsync(() => !h.Settings.BoardTriageConsent && !h.Settings.AssistantConsent));
        Assert.Empty(h.D.SetCalls);
        Assert.Empty(h.D.RunStartCalls);
        // An automatic run never asks.
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 3));
        await h.EndedAsync();
        Assert.Equal(1, asked);
        Assert.Equal(new TriageState.Failed(TriageTrigger.Automatic, TriageFailure.Declined, T0), await h.StateAsync());
        // Allowed.
        await h.Ui.RunAsync(() => h.C.Consent = () =>
        {
            asked++;
            return Task.FromResult(true);
        });
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.EndedAsync();
        Assert.Equal(2, asked);
        Assert.True(await h.Ui.RunAsync(() => h.Settings.BoardTriageConsent && h.Settings.AssistantConsent));
        D.Same([D.Prefs(assistant: true)], h.D.SetCalls);
        Assert.True(await h.Ui.RunAsync(() => h.C.ConsentGiven && !h.C.NeedsConsent));
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 0, 0, T0), await h.StateAsync());
        Assert.Single(h.D.RunStartCalls);
    }

    /// <summary>The panel's consent alone is not enough, nor the keys without the board's assistant preference.</summary>
    [Fact]
    public async Task WhatConsentNeeds()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), prefs: D.Prefs());
        Assert.True(await h.Ui.RunAsync(() => h.C.NeedsConsent));
        await h.Ui.InvokeAsync(() => h.P.UpdateAsync(p => p with { Assistant = true }));
        Assert.True(await h.Ui.RunAsync(() => h.C.ConsentGiven));
        Assert.True(await h.Ui.RunAsync(() =>
        {
            h.Settings.BoardTriageConsent = false;
            return h.C.NeedsConsent;
        }));
    }

    /// <summary>Withdrawing stops a run, drops the board's consent and turns the assistant preference off; the panel's consent stays.</summary>
    [Fact]
    public async Task Withdraw()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn([C.Init], FakeClaudeStep.Hang())));
        await h.BoardAsync(queue: 2);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.RunningAsync();
        await h.Ui.RunAsync(() =>
        {
            h.C.WithdrawConsent();
            Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Cancelled, T0), h.C.State);
            Assert.True(!h.Settings.BoardTriageConsent && h.Settings.AssistantConsent);
        });
        await D.UntilAsync(h.Ui, () => h.D.SetCalls.Count == 1 && h.P.IsIdle);
        D.Same([D.Prefs(assistant: false)], h.D.SetCalls);
        Assert.True(await h.Ui.RunAsync(() => h.C.NeedsConsent));
        await h.EndedAsync();
    }

    /// <summary>Withdrawing also turns automatic triage off: a consent given again later does not bring back runs the user did not turn on again.</summary>
    [Fact]
    public async Task WithdrawTurnsAutomaticTriageOff()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), prefs: D.Prefs(assistant: true, autoTriage: true));
        await h.Ui.RunAsync(h.C.WithdrawConsent);
        await D.UntilAsync(h.Ui, () => h.D.SetCalls.Count == 1 && h.P.IsIdle);
        D.Same([D.Prefs(assistant: false, autoTriage: false)], h.D.SetCalls);
        Assert.True(await h.Ui.RunAsync(() => h.P.Preferences?.AutoTriage == false && !h.C.WantsBoardData));
    }

    /// <summary>A board the daemon does not have, or has turned off, hides the triage; a board that lists again brings it back.</summary>
    [Fact]
    public async Task BoardPhaseHidesTheTriage()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")));
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.C.View.Offered);
            h.C.BoardChanged(new Snapshot { Phase = Phase.Unsupported });
            Assert.True(h.C.BoardPhase == Phase.Unsupported && !h.C.View.Offered);
            // Transient phases change nothing.
            h.C.BoardChanged(new Snapshot { Phase = Phase.Unavailable });
            Assert.False(h.C.View.Offered);
        });
        await h.BoardAsync(queue: 1);
        Assert.True(await h.Ui.RunAsync(() => h.C.BoardPhase == Phase.Ready && h.C.View.Offered));
        // Turned off in the daemon's preferences: hidden at once.
        await h.Ui.InvokeAsync(() => h.P.UpdateAsync(p => p with { Enabled = false }));
        Assert.False(await h.Ui.RunAsync(() => h.C.View.Offered));
        await h.Ui.InvokeAsync(() => h.P.UpdateAsync(p => p with { Enabled = true }));
        Assert.True(await h.Ui.RunAsync(() => h.C.View.Offered));
        // A snapshot of the board off, with preferences that say it is on
        // again (newer), does not hide it.
        await h.Ui.RunAsync(() => h.C.BoardChanged(new Snapshot { Phase = Phase.Off }));
        Assert.True(await h.Ui.RunAsync(() => h.C.View.Offered));
    }

    // The schedule's view of it

    /// <summary>The inputs the schedule reads.</summary>
    [Fact]
    public async Task AutoTriageInputs()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(
            Fake(C.AnswerTurn("x")), prefs: D.Prefs(assistant: true, autoTriage: true, autoTriageMinutes: 45, autoTriageDailyCases: 7));
        var run = new Run { Model = "claude-code", Date = T0, Trigger = "auto", Started = T0.AddSeconds(-60) };
        await h.BoardAsync(queue: 3, annotatedToday: 2, run: run);
        var rev = await h.Ui.RunAsync(() => h.C.BoardRevision);
        await h.Ui.RunAsync(h.C.CheckSignIn);
        await h.UntilAsync(() => h.C.SignedIn == true);
        var i = await h.Ui.RunAsync(() => h.C.AutoTriageInputs);
        Assert.True(i.Enabled && i.Available && i.SignedIn == true && i.Consent && !i.Running);
        Assert.True(i.Queue == 3 && i.AnnotatedToday == 2 && i.CountedAt == T0);
        Assert.True(i.Minutes == 45 && i.DailyCap == 7);
        Assert.Equal(T0.AddSeconds(-60), i.LastAttempt);
        // The same board again is no new revision; another queue is.
        await h.BoardAsync(queue: 3, annotatedToday: 2, run: run);
        Assert.Equal(rev, await h.Ui.RunAsync(() => h.C.BoardRevision));
        await h.BoardAsync(queue: 4, annotatedToday: 2, run: run);
        Assert.Equal(rev + 1, await h.Ui.RunAsync(() => h.C.BoardRevision));
        // A manual last run is no automatic attempt; a board that could not be
        // listed changes nothing.
        await h.BoardAsync(queue: 4, run: new Run { Model = "m", Date = T0, Trigger = "manual", Started = T0 });
        Assert.Null(await h.Ui.RunAsync(() => h.C.AutoTriageInputs.LastAttempt));
        await h.Ui.RunAsync(() => h.C.BoardChanged(new Snapshot { Phase = Phase.Unavailable }));
        Assert.Equal(4, await h.Ui.RunAsync(() => h.C.Board.Queue));
        // With the assistant off the queue counts as empty.
        await h.BoardAsync(queue: 4, assistantOn: false);
        Assert.Equal(0, await h.Ui.RunAsync(() => h.C.AutoTriageInputs.Queue));
    }

    /// <summary>The schedule with the real controller: one automatic run after the board's debounce, recorded as auto.</summary>
    [Fact]
    public async Task ScheduledRun()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(
            Fake(Turn(Lines([C.Init], Annotate("a1"), [C.Result("ok")]))), prefs: D.Prefs(assistant: true, autoTriage: true));
        await h.Ui.RunAsync(h.C.CheckSignIn);
        await h.UntilAsync(() => h.C.SignedIn == true);
        var scheduler = await h.Ui.RunAsync(() =>
        {
            var s = new BoardAutoTriageScheduler(h.C, h.Clock, TimeZoneInfo.Utc, pending: h.Pending);
            s.Start();
            Assert.Equal(new AutoTriage.Decision.Off(AutoTriage.OffReason.EmptyQueue), s.Decision);
            return s;
        });
        await h.BoardAsync(queue: 2);
        Assert.True(await h.Ui.RunAsync(() => scheduler.Debouncing));
        h.Clock.Advance(BoardAutoTriageScheduler.DefaultDebounce);
        var at = T0 + BoardAutoTriageScheduler.DefaultDebounce;
        await h.UntilAsync(() => h.C.State == new TriageState.Finished(TriageTrigger.Automatic, 1, 0, at) && h.C.IsIdle);
        Assert.Equal([new BoardRunStartParams { Trigger = BoardTrigger.Auto, Source = "claude-code" }], h.D.RunStartCalls);
        Assert.Equal([Assistant.TriageMessage(40, drafts: false)], FakeClaudeScript.Prompts(h.Dir.Path));
        Assert.True(await h.Ui.RunAsync(() => scheduler.LastAttempt == at && scheduler.Failures == 0));
        await h.Ui.RunAsync(scheduler.Dispose);
    }

    // Losing what a run needs

    public static TheoryData<string, TriageTrigger, bool> LosingCases => new()
    {
        { "not available", TriageTrigger.Automatic, true },
        { "the panel's consent withdrawn", TriageTrigger.Manual, true },
        { "the board's consent withdrawn", TriageTrigger.Automatic, true },
        { "the assistant preference off", TriageTrigger.Manual, true },
        { "the board off", TriageTrigger.Manual, true },
        { "automatic triage off", TriageTrigger.Automatic, true },
        { "automatic triage off, a manual run", TriageTrigger.Manual, false },
    };

    /// <summary>
    /// A run under way stops when triage stops being available, a consent
    /// goes, the board or its assistant preference goes off, and (an
    /// automatic one) when automatic triage is switched off; a manual run does
    /// not care about the switch.
    /// </summary>
    [Theory]
    [MemberData(nameof(LosingCases))]
    public async Task LosingWhatItNeeds(string name, TriageTrigger trigger, bool stops)
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn([C.Init], FakeClaudeStep.Hang())), prefs: D.Prefs(assistant: true, autoTriage: true));
        await h.BoardAsync(queue: 2);
        await h.Ui.RunAsync(() => h.C.Start(trigger, 2));
        await h.RunningAsync();
        await h.Ui.RunAsync(() =>
        {
            switch (name)
            {
                case "not available":
                    h.Flags.Available = false;
                    h.C.AvailabilityChanged();
                    break;
                case "the panel's consent withdrawn":
                    h.Settings.AssistantConsent = false;
                    break;
                case "the board's consent withdrawn":
                    h.Settings.BoardTriageConsent = false;
                    break;
                case "the assistant preference off":
                    h.P.Update(p => p with { Assistant = false });
                    break;
                case "the board off":
                    h.P.Update(p => p with { Enabled = false });
                    break;
                default:
                    h.P.Update(p => p with { AutoTriage = false });
                    break;
            }
        });
        if (stops)
        {
            Assert.True(new TriageState.Failed(trigger, TriageFailure.Cancelled, T0) == await h.StateAsync(), name);
            await h.EndedAsync();
            Assert.Equal([End(BoardRunError.Cancelled)], h.D.RunEndCalls);
        }
        else
        {
            await D.UntilAsync(h.Ui, () => h.P.IsIdle);
            Assert.True((await h.StateAsync()).IsActive, name);
        }
    }

    // The In App target

    /// <summary>
    /// Triage needs the In App target: with another target the control is
    /// hidden and nothing can run; a change of the target is reported.
    /// </summary>
    [Fact]
    public async Task NeedsTheInAppTarget()
    {
        RequireWindows();
        Assert.True(BoardTriageController.TriageNeedsInAppTarget);
        Assert.True(BoardTriageController.TriageAvailable(true, AssistantTarget.App));
        Assert.False(BoardTriageController.TriageAvailable(true, AssistantTarget.Desktop));
        Assert.False(BoardTriageController.TriageAvailable(true, AssistantTarget.Code));
        Assert.False(BoardTriageController.TriageAvailable(false, AssistantTarget.App));

        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")));
        var (assistant, c) = await h.Ui.RunAsync(() =>
        {
            h.Settings.AssistantMenu = true;
            var a = new AssistantController(null, h.Settings, _ => false, locator: h.Locator, pending: h.Pending);
            a.Apply(McpStatus.Decode(System.Text.Encoding.UTF8.GetBytes(
                """{"command": "C:\\b\\malachi-mcp.exe", "clients": [{"id": "claude-code", "name": "Claude Code", "present": true, "registered": true}]}""")));
            Assert.True(a.Shown);
            var request = new AssistantRequest(
                h.Settings, h.Locator, Path.Combine(h.Dir.Path, "work2"), C.Environment(("USERPROFILE", h.Dir.Path)),
                killGrace: KillGrace, time: h.Clock, directories: new FakePrivateDirectories(), pending: h.Pending);
            var c = new BoardTriageController(
                h.D.Client, h.Settings, h.Locator, h.P, request, a, "/b/malachi-mcp", "/s.sock", h.Clock, pending: h.Pending);
            return (a, c);
        });
        var reports = 0;
        await h.Ui.RunAsync(() =>
        {
            c.Observe(() => reports++);
            h.Settings.AssistantTarget = AssistantTarget.Desktop;
            Assert.True(c.View.Control == TriageControl.Hidden && !c.CanRun);
            h.Settings.AssistantTarget = AssistantTarget.App;
            Assert.True(reports > 0);
            Assert.True(c.View.Control != TriageControl.Hidden && c.CanRun);
            h.Settings.AssistantTarget = AssistantTarget.Code;
            Assert.True(c.View.Control == TriageControl.Hidden && !c.CanRun);
            Assert.False(c.AutoTriageInputs.Available);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            c.Close();
            c.Request.Close();
            assistant.Close();
        });
    }

    // Consent and the daemon

    /// <summary>
    /// Consent is kept only once the daemon stored the assistant preference: a
    /// refusal sets no key and is reported once (a run's own failure, or the
    /// preferences' toast from Preferences → AI).
    /// </summary>
    [Fact]
    public async Task ConsentOnlyOnceStored()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), consent: false, prefs: D.Prefs());
        var errors = new List<string>();
        await h.Ui.RunAsync(() => h.P.ToastRequested += (_, e) => errors.Add(e));
        h.D.SetFailure = D.Error(ErrorCode.StorageError, "disk");
        await h.Ui.RunAsync(() =>
        {
            h.C.Consent = () => Task.FromResult(true);
            h.C.Start(TriageTrigger.Manual);
        });
        await h.EndedAsync();
        Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Backend, T0), await h.StateAsync());
        Assert.True(await h.Ui.RunAsync(() => !h.Settings.BoardTriageConsent && !h.Settings.AssistantConsent));
        Assert.Empty(await h.Ui.RunAsync(() => errors.ToArray())); // the run reports it
        // From Preferences → AI: reported through the toast, no key either.
        Assert.False(await h.Ui.InvokeAsync(() => h.C.GiveConsentAsync()));
        Assert.True(await h.Ui.RunAsync(() => !h.Settings.BoardTriageConsent && !h.Settings.AssistantConsent));
        Assert.Single(await h.Ui.RunAsync(() => errors.ToArray()));
        // Stored: both keys.
        h.D.SetFailure = null;
        Assert.True(await h.Ui.InvokeAsync(() => h.C.GiveConsentAsync()));
        Assert.True(await h.Ui.RunAsync(() => h.Settings.BoardTriageConsent && h.Settings.AssistantConsent));
    }

    /// <summary>
    /// A provider switched while the consent's write is on its way approves
    /// nothing: no key of either provider, and the assistant preference and
    /// automatic triage go off (Go TestLateBoardConsentDoesNotApproveNewProvider).
    /// </summary>
    [Fact]
    public async Task ProviderSwitchDuringConsentWriteCannotApproveNewProvider()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("synthetic")), consent: false, prefs: D.Prefs(autoTriage: true));
        h.D.Sets.Hold(true);
        var grant = await h.Ui.RunAsync(() => h.C.GiveConsentAsync());
        await D.UntilAsync(h.Ui, () => h.D.Sets.Waiting > 0);
        await h.Ui.RunAsync(() => h.Settings.AssistantProvider = AssistantProviderID.ChatGpt);
        h.D.Sets.Hold(false);
        Assert.False(await grant);
        await D.UntilAsync(h.Ui, () => !h.P.Writing);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.Settings.BoardTriageConsent);
            Assert.Equal(0, h.Settings.BoardChatGptConsentVersion);
            Assert.Equal(0, h.Settings.AssistantChatGptConsentVersion);
            Assert.False(h.P.Preferences?.AutoTriage);
            Assert.False(h.P.Preferences?.Assistant);
        });
    }

    /// <summary>
    /// A daemon that does not answer the board's preferences: a manual run
    /// fails without the sheet, and the control is unavailable after it, so
    /// the failure is not repeated on every click.
    /// </summary>
    [Fact]
    public async Task NoPreferencesNoSheet()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), consent: false, load: false);
        h.D.GetFailure = D.Error(ErrorCode.MethodNotFound, "no board");
        var asked = 0;
        await h.Ui.RunAsync(() =>
        {
            h.C.Consent = () =>
            {
                asked++;
                return Task.FromResult(true);
            };
            h.C.Start(TriageTrigger.Manual);
        });
        await h.EndedAsync();
        Assert.Equal(new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Backend, T0), await h.StateAsync());
        Assert.Equal(0, asked);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.Settings.BoardTriageConsent);
            Assert.True(h.C.View.Control == TriageControl.Unavailable && !h.C.View.Enabled);
            Assert.Equal("the mail backend did not answer", h.C.View.ToolTip);
        });
    }

    /// <summary>
    /// The daemon's assistant preference on while the board's consent is not
    /// given here (a withdrawal whose write failed) is turned off at the next
    /// load.
    /// </summary>
    [Fact]
    public async Task StrayAssistantPreferenceIsRepaired()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), consent: false, load: false);
        h.D.Preferences = D.Prefs(assistant: true, autoTriage: true);
        await h.Ui.RunAsync(() => h.P.ConnectionChanged(connected: true));
        await D.UntilAsync(h.Ui, () => h.D.SetCalls.Count == 1 && h.P.IsIdle);
        D.Same([D.Prefs(assistant: false, autoTriage: true)], h.D.SetCalls);
        // With the consent given nothing is touched.
        await h.Ui.RunAsync(() => h.Settings.BoardTriageConsent = true);
        h.D.Preferences = D.Prefs(assistant: true);
        Assert.True(await h.Ui.InvokeAsync(h.P.LoadNowAsync));
        await D.UntilAsync(h.Ui, () => h.P.IsIdle);
        await h.IdleAsync();
        Assert.Single(h.D.SetCalls);
    }

    // Sign-in

    /// <summary>A sign-in check answered after a run learnt otherwise is dropped.</summary>
    [Fact]
    public async Task LateSignInCheckIsDropped()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")));
        await h.Ui.RunAsync(() =>
        {
            // The check yields before it asks, so what the run learnt in the
            // same turn overtakes it (Swift holds the answer with a file).
            h.C.CheckSignIn();
            h.C.LearnSignedIn(false);
        });
        await h.UntilAsync(() => h.C.SignInAnswers == 1);
        Assert.False(await h.Ui.RunAsync(() => h.C.SignedIn));
        // A check of its own still counts.
        await h.Ui.RunAsync(h.C.CheckSignIn);
        await h.UntilAsync(() => h.C.SignInAnswers == 2);
        Assert.True(await h.Ui.RunAsync(() => h.C.SignedIn));
    }

    // Usage

    /// <summary>The result's usage is the run's, sent with board.runEnd.</summary>
    [Fact]
    public async Task UsageFromTheResult()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(Lines(
            [C.Init], AnnotateUsing("a1", "m1", 5, 100), [ResultUsing(input: 40, output: 900, write: 1200, read: 50000)]))));
        await h.BoardAsync(queue: 3);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.EndedAsync();
        Assert.Equal([End(usage: Usage(40, 900, 1200, 50000))], h.D.RunEndCalls);
    }

    private static string[] UsageLines() => Lines(
        [C.Init], AnnotateUsing("a1", "m1", 5, 100), AnnotateUsing("a2", "m1", 5, 100), AnnotateUsing("a3", "m2", 7, 300));

    private static readonly BoardUsage UsageSum = Usage(12, 2, 20, 400);

    /// <summary>Without a result: the distinct API messages seen, each once. A cancelled run.</summary>
    [Fact]
    public async Task UsageWithoutAResultCancelled()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(UsageLines(), FakeClaudeStep.Hang())));
        await h.BoardAsync(queue: 4);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.UntilAsync(() => h.C.State == new TriageState.Running(TriageTrigger.Manual, 3, 4));
        await h.Ui.RunAsync(h.C.Cancel);
        await h.EndedAsync();
        Assert.Equal([End(BoardRunError.Cancelled, UsageSum)], h.D.RunEndCalls);
    }

    /// <summary>At its limit, no result within the grace period: what the API messages reported, the one after the limit too.</summary>
    [Fact]
    public async Task UsageWithoutAResultAtTheLimit()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(
            Lines(UsageLines(), AnnotateUsing("a4", "m3", 1000, 1000)), FakeClaudeStep.Hang())));
        await h.Ui.RunAsync(() => h.C.Grace = TimeSpan.FromMilliseconds(300));
        await h.BoardAsync(queue: 12);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 3));
        await h.UntilAsync(() => h.C.RunUsage?.InputTokens == 1012);
        h.Clock.Advance(TimeSpan.FromMilliseconds(300));
        await h.EndedAsync();
        Assert.Equal([End(usage: Usage(1012, 3, 30, 1400))], h.D.RunEndCalls);
    }

    /// <summary>Without a result: a run that timed out.</summary>
    [Fact]
    public async Task UsageWithoutAResultTimedOut()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(UsageLines(), FakeClaudeStep.Hang())));
        await h.Ui.RunAsync(() => h.C.Timeout = TimeSpan.FromMilliseconds(800));
        await h.BoardAsync(queue: 12);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.UntilAsync(() => h.C.State == new TriageState.Running(TriageTrigger.Manual, 3, 12));
        h.Clock.Advance(TimeSpan.FromMilliseconds(800));
        await h.EndedAsync();
        Assert.Equal([End(BoardRunError.Timeout, UsageSum)], h.D.RunEndCalls);
    }

    /// <summary>Without a result: a run the application quit (CancelAndEndAsync).</summary>
    [Fact]
    public async Task UsageWithoutAResultQuit()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(UsageLines(), FakeClaudeStep.Hang())));
        await h.BoardAsync(queue: 12);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.UntilAsync(() => h.C.State == new TriageState.Running(TriageTrigger.Manual, 3, 12));
        await h.Ui.InvokeAsync(() => h.C.CancelAndEndAsync());
        Assert.Equal([End(BoardRunError.Cancelled, UsageSum)], h.D.RunEndCalls);
    }

    /// <summary>
    /// At its limit the run waits for Claude Code's result and sends its
    /// usage, the whole run's; the run is a success, ended once, and a note
    /// the bridge refused after the limit is not counted.
    /// </summary>
    [Fact]
    public async Task LimitWaitsForTheResult()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(
            Lines([C.Init], AnnotateUsing("a1", "m1", 5, 100), AnnotateUsing("a2", "m2", 7, 300)),
            FakeClaudeStep.Sleep(300),
            FakeClaudeStep.Lines(C.ToolUse("a3", "annotate_case"), C.ToolResult("a3", "limit", error: true),
                ResultUsing(input: 40, output: 2500, write: 1200, read: 50000)))));
        await h.BoardAsync(queue: 12);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual, 2));
        await h.EndedAsync();
        Assert.Contains(new TriageState.Running(TriageTrigger.Manual, 2, 2), await h.Ui.RunAsync(() => h.States.ToArray()));
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 2, 0, T0), await h.StateAsync());
        Assert.Equal([End(usage: Usage(40, 2500, 1200, 50000))], h.D.RunEndCalls);
        await h.IdleWithKillAsync();
        Assert.Equal([new BoardTriageEnd(TriageTrigger.Manual, null)], h.Ends);
        Assert.Single(h.D.RunEndCalls);
    }

    /// <summary>An error result after the limit does not turn the run into a failure; its usage still counts.</summary>
    [Fact]
    public async Task ErrorResultAfterTheLimit()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(
            Lines([C.Init], Annotate("a1"), Annotate("a2")),
            FakeClaudeStep.Sleep(300),
            FakeClaudeStep.Lines(ResultUsing(input: 3, output: 700, write: 80, read: 9000, success: false)))));
        await h.BoardAsync(queue: 12);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Automatic, 2));
        await h.EndedAsync();
        Assert.Equal(new TriageState.Finished(TriageTrigger.Automatic, 2, 0, T0), await h.StateAsync());
        Assert.Equal([End(usage: Usage(3, 700, 80, 9000))], h.D.RunEndCalls);
        await h.IdleWithKillAsync();
        Assert.Equal([new BoardTriageEnd(TriageTrigger.Automatic, null)], h.Ends);
    }

    /// <summary>Stop while a run at its limit waits for its result ends it at once, as the success it is, with the usage seen so far.</summary>
    [Fact]
    public async Task StopWhileWaitingForTheResult()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn(
            Lines([C.Init], AnnotateUsing("a1", "m1", 5, 100), AnnotateUsing("a2", "m2", 7, 300)), FakeClaudeStep.Hang())));
        await h.Ui.RunAsync(() => h.C.Grace = TimeSpan.FromSeconds(60));
        await h.BoardAsync(queue: 12);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual, 2));
        await h.UntilAsync(() => h.C.State == new TriageState.Running(TriageTrigger.Manual, 2, 2));
        await h.Ui.RunAsync(() =>
        {
            h.C.Cancel();
            Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 2, 0, T0), h.C.State);
        });
        await h.EndedAsync();
        Assert.Equal([End(usage: Usage(12, 2, 20, 400))], h.D.RunEndCalls);
        await h.IdleWithKillAsync();
        Assert.Equal([new BoardTriageEnd(TriageTrigger.Manual, null)], h.Ends);
        Assert.Single(h.D.RunEndCalls);
    }

    /// <summary>A run that reported nothing sends no usage, and a new run does not carry the last one's.</summary>
    [Fact]
    public async Task NoUsageIsLeftOut()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(
            Turn(Lines([C.Init], AnnotateUsing("a1", "m1", 5, 100), [C.Result("ok")])),
            Turn(Lines([C.Init], Annotate("a2"), [C.Result("ok")]))));
        await h.BoardAsync(queue: 3);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.EndedAsync();
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.EndedAsync();
        var ends = h.D.RunEndCalls;
        Assert.Equal(2, ends.Count);
        Assert.Equal(Usage(5, 1, 10, 100), ends[0].Usage);
        Assert.Null(ends[1].Usage);
    }

    /// <summary>The board's usage of the last 24 hours reaches the view; unknown before a board.list, "None" without usage.</summary>
    [Fact]
    public async Task UsageInTheView()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")));
        Assert.False(await h.Ui.RunAsync(() => h.C.View.UsageShown));
        await h.BoardAsync(queue: 1);
        await h.Ui.RunAsync(() =>
        {
            var v = h.C.View;
            Assert.True(v.UsageShown && v.UsageValue == "None" && v.UsageDetail.Length == 0);
            var reports = 0;
            var token = h.C.Observe(() => reports++);
            h.C.BoardChanged(new Snapshot
            {
                Annotated = true,
                Phase = Phase.Ready,
                Triage = new Triage
                {
                    Queue = 1,
                    Usage24h = new BoardUsageTotal { InputTokens = 10, OutputTokens = 0, CacheCreationInputTokens = 0, CacheReadInputTokens = 5000, Runs = 2 },
                },
            });
            Assert.Equal(1, reports);
            Assert.True(h.C.View.UsageShown);
            Assert.EndsWith("From 2 triage runs", h.C.View.UsageDetail, StringComparison.Ordinal);
            token.Cancel();
        });
    }

    // For the views

    /// <summary>Quitting waits for board.runEnd, but never longer than its bound.</summary>
    [Fact]
    public async Task CancelAndEnd()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(Turn([C.Init], FakeClaudeStep.Hang())));
        await h.BoardAsync(queue: 2);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.RunningAsync();
        await h.Ui.InvokeAsync(() => h.C.CancelAndEndAsync());
        Assert.Equal([End(BoardRunError.Cancelled)], h.D.RunEndCalls);
        Assert.True(await h.Ui.RunAsync(() => h.C.IsIdle), "runEnd answered before it returned");
        // A daemon that does not answer: it returns anyway.
        h.D.RunEnds.Hold(true);
        await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual));
        await h.RunningAsync();
        var quit = await h.Ui.RunAsync(() => h.C.CancelAndEndAsync(TimeSpan.FromMilliseconds(200)));
        h.Clock.Advance(TimeSpan.FromMilliseconds(200));
        await quit.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await D.UntilAsync(h.Ui, () => h.D.RunEnds.Waiting == 1);
        h.D.RunEnds.Hold(false);
        await h.EndedAsync();
        // Nothing under way: it returns at once.
        await h.Ui.InvokeAsync(() => h.C.CancelAndEndAsync());
    }

    /// <summary>The view is published again once a minute only while it names a relative time.</summary>
    [Fact]
    public async Task ClockOnlyForRelativeTimes()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")));
        await h.BoardAsync(queue: 1);
        Assert.True(await h.Ui.RunAsync(() => !h.C.View.RelativeTime && !h.C.ClockRunning));
        await h.BoardAsync(queue: 1, run: new Run { Model = "claude-code", Date = T0.AddSeconds(-300), Trigger = "auto", Started = T0 });
        Assert.True(await h.Ui.RunAsync(() => h.C.View.RelativeTime && h.C.ClockRunning));
        var reports = 0;
        var token = await h.Ui.RunAsync(() => h.C.Observe(() => reports++));
        h.Clock.Advance(BoardTriageController.ClockTick);
        await h.UntilAsync(() => reports == 1);
        Assert.True(await h.Ui.RunAsync(() => h.C.ClockRunning), "still relative: the next tick");
        // No relative time any more: the clock stops.
        await h.BoardAsync(queue: 1, assistantOn: false);
        Assert.True(await h.Ui.RunAsync(() => !h.C.View.RelativeTime && !h.C.ClockRunning));
        await h.Ui.RunAsync(token.Cancel);
    }

    /// <summary>The board source should run while automatic triage is on, consent is given and triage can run.</summary>
    [Fact]
    public async Task WantsBoardData()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), prefs: D.Prefs(assistant: true, autoTriage: true));
        Assert.True(await h.Ui.RunAsync(() => h.C.WantsBoardData));
        h.Flags.Available = false;
        Assert.False(await h.Ui.RunAsync(() => h.C.WantsBoardData));
        h.Flags.Available = true;
        Assert.False(await h.Ui.RunAsync(() =>
        {
            h.Settings.BoardTriageConsent = false;
            return h.C.WantsBoardData;
        }));
        await h.Ui.RunAsync(() => h.Settings.BoardTriageConsent = true);
        await h.Ui.InvokeAsync(() => h.P.UpdateAsync(p => p with { AutoTriage = false }));
        Assert.False(await h.Ui.RunAsync(() => h.C.WantsBoardData));
    }

    /// <summary>Today's automatic count is in the view while automatic triage is on.</summary>
    [Fact]
    public async Task TodayInTheView()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), prefs: D.Prefs(assistant: true, autoTriage: true));
        Assert.True(await h.Ui.RunAsync(() => h.C.View.AnnotatedToday is null && h.C.View.TodayLine.Length == 0));
        await h.BoardAsync(queue: 1, annotatedToday: 4);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(4, h.C.View.AnnotatedToday);
            Assert.Equal("4 conversations triaged automatically today", h.C.View.TodayLine);
        });
        await h.Ui.InvokeAsync(() => h.P.UpdateAsync(p => p with { AutoTriage = false }));
        Assert.True(await h.Ui.RunAsync(() => h.C.View.AnnotatedToday is null && h.C.View.TodayLine.Length == 0));
    }

    // Go only

    /// <summary>Go TestAutoPauseInTheView: the pause the schedule sets reaches the view, and only a change is reported.</summary>
    [Fact]
    public async Task AutoPauseInTheView()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("x")), prefs: D.Prefs(assistant: true, autoTriage: true));
        await h.Ui.RunAsync(() =>
        {
            var reports = 0;
            var token = h.C.Observe(() => reports++);
            h.C.SetAutoPause(new AutoTriagePause.SignedOut());
            h.C.SetAutoPause(new AutoTriagePause.SignedOut());
            Assert.Equal(1, reports);
            Assert.Equal("Automatic triage paused: Claude Code is not signed in", h.C.View.Paused);
            h.C.SetAutoPause(null);
            Assert.True(reports == 2 && h.C.View.Paused.Length == 0 && h.C.AutoPause is null);
            token.Cancel();
        });
    }

    /// <summary>
    /// Go TestRunCapturesProviderSource: a run of the ChatGPT provider is
    /// recorded with its source, and its session gets the board's model, the
    /// board's consent and the triage's tools.
    /// </summary>
    [Fact]
    public async Task RunCapturesProviderSource()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("unused")));
        var provider = new FakeProvider();
        await h.Ui.RunAsync(() =>
        {
            h.Settings.BoardChatGptModel = "board-model";
            h.Settings.BoardChatGptConsentVersion = 1;
            h.Request.Provider = provider;
            h.Settings.AssistantProvider = AssistantProviderID.ChatGpt;
        });
        await D.UntilAsync(h.Ui, () => h.P.IsIdle);
        await h.BoardAsync(queue: 1);
        Assert.True(await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual, 1)));
        await h.EndedAsync();
        Assert.Equal([new BoardRunStartParams { Trigger = BoardTrigger.Manual, Source = "malachi-chatgpt" }], h.D.RunStartCalls);
        Assert.Equal(new TriageState.Finished(TriageTrigger.Manual, 0, 0, T0), await h.StateAsync());
        var spec = Assert.IsType<AssistantSessionSpec>(provider.Spec);
        Assert.True(spec.ModelId == "board-model" && spec.BoardConsent && spec.ToolPolicy == AssistantToolPolicy.TriageDrafts);
        Assert.Equal(Assistant.TriageBridgeArgs("run_1", 1), spec.BridgeArgs);
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
    }

    /// <summary>
    /// A ChatGPT model changed while a run is under way applies from the next
    /// run and stops nothing (Swift providerChangeConcernsActive); the Codex
    /// executable of the selected provider still ends the run.
    /// </summary>
    [Fact]
    public async Task AModelChangeDoesNotStopARun()
    {
        RequireWindows();
        await using var h = await Harness.StartAsync(Fake(C.AnswerTurn("unused")));
        var provider = new FakeProvider(hold: true);
        await h.Ui.RunAsync(() =>
        {
            h.Settings.BoardChatGptConsentVersion = 1;
            h.Request.Provider = provider;
            h.Settings.AssistantProvider = AssistantProviderID.ChatGpt;
        });
        await D.UntilAsync(h.Ui, () => h.P.IsIdle);
        await h.BoardAsync(queue: 1);
        Assert.True(await h.Ui.RunAsync(() => h.C.Start(TriageTrigger.Manual, 1)));
        await D.UntilAsync(h.Ui, () => provider.Session is not null);
        await h.Ui.RunAsync(() =>
        {
            h.Settings.BoardChatGptModel = "another-board-model";
            h.Settings.AssistantChatGptModel = "another-panel-model";
        });
        Assert.True((await h.StateAsync()).IsActive);
        Assert.True(provider.Session!.IsRunning);
        await h.Ui.RunAsync(() => h.Settings.AssistantCodexPath = "C:\\elsewhere\\codex.exe");
        await D.UntilAsync(h.Ui, () => !provider.Session!.IsRunning);
    }

    /// <summary>Switches the stand-ins read from any thread.</summary>
    private sealed class Flags
    {
        private volatile bool found = true;
        private volatile bool available = true;

        public bool Found
        {
            get => found;
            set => found = value;
        }

        public bool Available
        {
            get => available;
            set => available = value;
        }
    }

    /// <summary>A ChatGPT provider whose session answers at once (<paramref name="hold"/>: never, until terminated).</summary>
    private sealed class FakeProvider(bool hold = false) : IAssistantProvider
    {
        public AssistantProviderID Id => AssistantProviderID.ChatGpt;

        public string Model => "test-model";

        public bool Available => true;

        public bool Connected => true;

        public bool HasConsent => true;

        public bool HasBoardConsent => true;

        public AssistantSessionSpec? Spec { get; private set; }

        public AnsweringSession? Session { get; private set; }

        public void AcceptConsent()
        {
        }

        public void AcceptBoardConsent()
        {
        }

        public Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken)
        {
            Spec = spec;
            Session = new AnsweringSession(hold);
            return Task.FromResult<IAssistantSession>(Session);
        }
    }

    private sealed class AnsweringSession(bool hold = false) : IAssistantSession
    {
        private readonly TaskCompletionSource completed = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public event EventHandler<IReadOnlyList<AssistantEvent>>? EventsReceived;

        public event EventHandler<AssistantSessionExit>? Exited;

        public Task Completion => completed.Task;

        public bool IsRunning => !completed.Task.IsCompleted;

        public Task SubmitAsync(string input, CancellationToken cancellationToken)
        {
            if (hold)
            {
                EventsReceived?.Invoke(this, [new AssistantEvent(AssistantEventKind.SystemInit) { BridgeConnected = true }]);
                return Task.CompletedTask;
            }
            EventsReceived?.Invoke(this, [
                new AssistantEvent(AssistantEventKind.SystemInit) { BridgeConnected = true },
                new AssistantEvent(AssistantEventKind.Result) { Success = true, ResultText = "ok" },
            ]);
            return Task.CompletedTask;
        }

        public void Terminate()
        {
            if (completed.TrySetResult())
            {
                Exited?.Invoke(this, new AssistantSessionExit(0, "terminated"));
            }
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private Harness(D daemon, FakeClaudeScript script)
        {
            D = daemon;
            Claude = script.CreateIn(Dir.Path);
        }

        public TemporaryDirectory Dir { get; } = new();

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Clock { get; } = new(T0);

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public Flags Flags { get; } = new();

        public D D { get; }

        public string Claude { get; }

        public ClaudeCodeLocator Locator { get; private set; } = null!;

        public AssistantRequest Request { get; private set; } = null!;

        public BoardPreferencesController P { get; private set; } = null!;

        public BoardTriageController C { get; private set; } = null!;

        /// <summary>Every state reported, without repeats (UI thread).</summary>
        public List<TriageState> States { get; } = [];

        /// <summary>The runs that ended, as reported (UI thread).</summary>
        public List<BoardTriageEnd> Ends { get; } = [];

        public int Refreshes { get; private set; }

        public static async Task<Harness> StartAsync(
            FakeClaudeScript script, bool consent = true, BoardPreferences? prefs = null, string? bridge = "/b/malachi-mcp", bool load = true)
        {
            var h = new Harness(await BoardTriageDaemon.StartAsync(), script);
            h.D.Preferences = prefs ?? D.Prefs(assistant: true);
            await h.Ui.RunAsync(() =>
            {
                var s = h.Settings;
                s.AssistantClaudePath = h.Claude;
                s.AssistantConsent = consent;
                s.BoardTriageConsent = consent;
                // The panel's model and the board's apart: the run takes the board's.
                s.AssistantModel = AssistantModel.Haiku;
                s.BoardTriageModel = AssistantModel.Opus;
                var prefix = h.Dir.Path + @"\";
                var flags = h.Flags;
                h.Locator = new ClaudeCodeLocator(
                    s,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path)),
                    timeout: TimeSpan.FromSeconds(60),
                    usable: p => flags.Found && p.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) && ClaudeCodeLocator.IsExecutableFile(p));
                h.Request = new AssistantRequest(
                    s, h.Locator, Path.Combine(h.Dir.Path, "work"), CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path)),
                    killGrace: KillGrace, timeout: TimeSpan.FromSeconds(10), time: h.Clock, directories: new FakePrivateDirectories(),
                    pending: h.Pending);
                h.P = new BoardPreferencesController(h.D.Client, pending: h.Pending);
                h.C = new BoardTriageController(
                    h.D.Client, s, h.Locator, h.P, h.Request, bridge, "/s.sock", () => flags.Available, h.Clock, TimeZoneInfo.Utc,
                    language: () => "Czech", today: () => "2026-10-01", pending: h.Pending)
                {
                    Timeout = TimeSpan.FromSeconds(10),
                };
                h.C.RefreshRequested += (_, _) => h.Refreshes++;
                h.C.Observe(() =>
                {
                    if (h.States.Count == 0 || h.States[^1] != h.C.State)
                    {
                        h.States.Add(h.C.State);
                    }
                });
                h.C.ObserveEnded(() =>
                {
                    if (h.C.LastEnded is { } e)
                    {
                        h.Ends.Add(e);
                    }
                });
            });
            if (load)
            {
                await h.Ui.InvokeAsync(h.P.LoadNowAsync);
                await D.UntilAsync(h.Ui, () => h.P.IsIdle);
            }
            return h;
        }

        public Task<TriageState> StateAsync() => Ui.RunAsync(() => C.State);

        /// <summary>The board reported <paramref name="queue"/> cases for the assistant.</summary>
        public Task BoardAsync(int queue, bool assistantOn = true, int annotatedToday = 0, Run? run = null) => Ui.RunAsync(() =>
            C.BoardChanged(new Snapshot
            {
                Annotated = assistantOn,
                Run = run,
                Phase = Phase.Ready,
                Triage = new Triage { Queue = queue, AnnotatedToday = annotatedToday },
            }));

        public Task UntilAsync(Func<bool> condition) => D.UntilAsync(Ui, condition);

        /// <summary>Waits until a run is running.</summary>
        public Task RunningAsync() => UntilAsync(() => C.State is TriageState.Running);

        /// <summary>Waits until the run ended and its board.runEnd was answered.</summary>
        public Task EndedAsync() => UntilAsync(() => !C.State.IsActive && C.IsIdle);

        /// <summary>
        /// Waits until the request's process read its prompt and the UI turn
        /// that sent it is over: the request's timeout waits on the clock then.
        /// </summary>
        public async Task RequestWaitsAsync()
        {
            await Eventually.Holds(() => FakeClaudeScript.Prompts(Dir.Path).Count > 0);
            await Ui.DrainAsync();
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, D.Fake, timeout: TimeSpan.FromSeconds(60));

        /// <summary>Kills what is left after the grace and waits until everything is idle.</summary>
        public async Task IdleWithKillAsync()
        {
            await Ui.DrainAsync();
            Clock.Advance(KillGrace);
            await IdleAsync();
        }

        // Cancels, kills what is left and waits for it, so the directory can go.
        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                C.Cancel();
                C.Close();
                P.Close();
                Request.Close();
            });
            D.Sets.Hold(false);
            D.RunStarts.Hold(false);
            D.RunEnds.Hold(false);
            Clock.Advance(TimeSpan.FromDays(1));
            try
            {
                await IdleAsync();
            }
            catch (Exception e) when (e is TimeoutException or AggregateException)
            {
                // A test's own failure, reported there.
            }
            await D.DisposeAsync();
            Ui.Dispose();
            Dir.Dispose();
        }
    }
}
