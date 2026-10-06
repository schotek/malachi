// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardReplyControllerTests.swift
// (every test) and ui/internal/boardreply/controller_test.go (with its
// TestReplyNeverOffersTheSamples). The board's Suggest Reply
// (BoardReplyController) against a fake daemon (board.get, board.setDraft,
// draft.delete) and the stand-in claude.exe of Malachi.FakeClaude. No real
// Claude Code, daemon or bridge is ever run: the bridge's path is only
// passed on.
//
// Windows differences, as AssistantRequestTests': the controller's timeout,
// the quit's bound and the kill grace of the request's process run on a
// fake clock, so a claude that never answers (Swift's "sleep 30",
// FakeClaudeStep.Hang here) is timed out and killed by advancing it, and
// the timeout's length is checked by advancing to just before it (Swift
// records what its gate was asked). A test waits for a report on the UI
// thread, for the request's work, processes and calls to be done
// (IdleAsync), or, for what the controller does not report (the created
// draft, the stand-in's starts), polls on the UI thread; never for time.

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
using Malachi.Core.Transport;
using Malachi.FakeClaude;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using ReplyState = Malachi.Core.Boards.Board.SuggestReplyState;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class BoardReplyControllerTests
{
    private const string Bridge = @"C:\b\malachi-mcp.exe";
    private const string Socket = @"C:\s\rpc.sock";
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_848_800);
    private static readonly TimeSpan Grace = TimeSpan.FromMilliseconds(300);
    private static readonly BoardCaseId C1 = new("c_1");

    private static void RequireWindows() => Assert.SkipUnless(OperatingSystem.IsWindows(), "the stand-in claude is a Windows program");

    private static BoardCase Wire(string n, BoardDraft? draft = null) => new()
    {
        Id = "c_" + n,
        AccountId = "acc_1",
        ThreadId = "t_" + n,
        RuleState = BoardState.You,
        RuleReason = BoardReason.YouAddressed,
        Visibility = BoardVisibility.Live,
        Subject = "Subject " + n,
        Person = new Address { Name = "P", Email = "p@example.invalid" },
        Date = T0,
        Snippet = "",
        Unread = false,
        HasAttachments = false,
        MessageCount = 3,
        ReplyMessageId = "m_" + n,
        ReplyFolderId = "f_inbox",
        LatestMessageId = "m_" + n,
        CanArchive = true,
        Draft = draft,
        Version = 1,
    };

    private static Board.Case BoardCase(string n) => new()
    {
        Id = new BoardCaseId("c_" + n),
        Account = new AccountId("acc_1"),
        Person = "P",
        Date = T0,
        Subject = "Subject " + n,
        RuleState = Board.State.You,
        Reply = new Board.ReplyTarget(new MessageId("m_" + n), new FolderId("f_inbox")),
    };

    // The bridge's create_draft result for draft id.
    private static string Created(string id, string account = "acc_1") =>
        "draft " + id + " (version 1) stored in account " + account + "; it is NOT sent.";

    private static string[] DraftLines(string id = "d_9") =>
    [
        CannedStreamJson.Init,
        CannedStreamJson.ToolUse("r", "read_message"),
        CannedStreamJson.ToolResult("r", "the message"),
        CannedStreamJson.ToolUse("c", "create_draft"),
        CannedStreamJson.ToolResult("c", Created(id)),
    ];

    // A turn that creates the draft and ends with a result.
    private static IReadOnlyList<FakeClaudeStep> DraftTurn() => [FakeClaudeStep.Lines([.. DraftLines(), CannedStreamJson.Result("")])];

    // A turn that creates the draft and then never ends (Swift's "sleep 30").
    private static IReadOnlyList<FakeClaudeStep> DraftThenHang() => [FakeClaudeStep.Lines(DraftLines()), FakeClaudeStep.Hang()];

    private static IReadOnlyList<FakeClaudeStep> InitThenHang() => [FakeClaudeStep.Lines(CannedStreamJson.Init), FakeClaudeStep.Hang()];

    private static FakeClaudeScript Fake(params IReadOnlyList<FakeClaudeStep>[] turns) => new(turns);

    private static DraftDeleteParams Deleted(string id) => new() { AccountId = "acc_1", DraftId = id };

    /// <summary>A reply: board.get, the request with the bridge for this one reply, the draft from create_draft's result linked, the board asked again.</summary>
    [Fact]
    public async Task Success()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()));
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Controller.Start(BoardCase("1"), "  Say yes,\n thanks  "));
            Assert.Equal(new ReplyState.Running(C1), h.Controller.State);
        });
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
        Assert.Equal([new ReplyState.Running(C1), new ReplyState.Idle()], h.States);
        Assert.Equal(["c_1"], h.Script.Gets);
        Assert.Equal([new BoardSetDraftParams { CaseId = "c_1", DraftId = "d_9" }], h.Script.Sets);
        Assert.Empty(h.Script.Deletes);
        Assert.Equal(1, h.Refreshes);
        Assert.Equal(0, h.ConsentAsked);
        // The command line: the bridge for one reply to m_1, three tools, the
        // panel's model.
        var args = FakeClaudeScript.Args(h.Dir.Path);
        Assert.Equal(
            Assistant.Args(new AssistantOptions
            {
                Bridge = Bridge,
                Socket = Socket,
                Model = AssistantModel.Haiku,
                SystemPrompt = Assistant.SuggestReplySystemPrompt(),
                BridgeArgs = ["--reply-only", "m_1"],
                Tools = Assistant.SuggestReplyTools,
            }),
            args);
        var joined = string.Join(' ', args);
        Assert.True(!joined.Contains("--allow-triage", StringComparison.Ordinal) && !joined.Contains("--allow-modify", StringComparison.Ordinal)
            && !joined.Contains("--allow-send", StringComparison.Ordinal));
        var prompts = FakeClaudeScript.Prompts(h.Dir.Path);
        Assert.Equal(
            [Assistant.SuggestReplyMessage("acc_1", "m_1", [.. Enumerable.Range(1, 8).Select(i => "m_1_" + i)], "Say yes, thanks")],
            prompts);
        Assert.True(prompts[0].Contains("m_1_4", StringComparison.Ordinal) && !prompts[0].Contains("m_1_3", StringComparison.Ordinal));
        // The time ran out nowhere: a day later nothing changes.
        await h.AdvanceAsync(TimeSpan.FromDays(1));
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
    }

    [Fact]
    public async Task NoDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init, CannedStreamJson.Text("Here you go"), CannedStreamJson.Result("Here you go"))));
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.NoDraft), await h.StateAsync());
        Assert.Empty(h.Script.Sets);
        Assert.Empty(h.Script.Deletes);
        Assert.EndsWith("The user gave no instruction.", FakeClaudeScript.Prompts(h.Dir.Path)[0], StringComparison.Ordinal);
        Assert.Equal("The suggested reply failed: the assistant wrote no reply.", (await h.ViewAsync(BoardCase("1"))).Note);
    }

    /// <summary>A refused create_draft (an error result) is no draft.</summary>
    [Fact]
    public async Task RefusedCreateIsNoDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init,
            CannedStreamJson.ToolUse("c", "create_draft"),
            CannedStreamJson.ToolResult("c", Created("d_9"), error: true),
            CannedStreamJson.Result("no"))));
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.NoDraft), await h.StateAsync());
        Assert.Empty(h.Script.Sets);
    }

    [Fact]
    public async Task LinkRefusedDeletesTheDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()));
        h.Script.SetFailure = new RpcError { Code = ErrorCode.StorageError, Message = "disk" };
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.Backend), await h.StateAsync());
        Assert.Equal([Deleted("d_9")], h.Script.Deletes);
        Assert.Null(await h.Ui.RunAsync(() => h.Controller.Created));
    }

    /// <summary>The case got a suggested reply meanwhile: ours goes, quietly.</summary>
    [Fact]
    public async Task LinkConflictEndsQuietly()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()));
        h.Script.SetFailure = new RpcError { Code = ErrorCode.Conflict, Message = "linked" };
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
        Assert.Equal([Deleted("d_9")], h.Script.Deletes);
    }

    /// <summary>Stop after the draft was created and before it was linked deletes it.</summary>
    [Fact]
    public async Task StopDeletesTheCreatedDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftThenHang()));
        await h.StartAsync(BoardCase("1"), "");
        await h.UntilAsync(() => h.Controller.Created is not null, "the draft");
        await h.Ui.RunAsync(() =>
        {
            h.Controller.Cancel();
            Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.Cancelled), h.Controller.State);
        });
        await h.KillAsync();
        Assert.Equal([Deleted("d_9")], h.Script.Deletes);
        Assert.Empty(h.Script.Sets);
        Assert.Equal("The suggested reply failed: it was stopped.", (await h.ViewAsync(BoardCase("1"))).Note);
    }

    [Fact]
    public async Task TimeoutDeletesTheCreatedDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftThenHang()));
        await h.StartAsync(BoardCase("1"), "");
        await h.UntilAsync(() => h.Controller.Created is not null, "the draft");
        h.Time.Advance(Assistant.SuggestReplyTimeout);
        await h.UntilAsync(() => !h.Controller.State.IsRunning, "the timeout");
        await h.KillAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.Timeout), await h.StateAsync());
        Assert.Equal([Deleted("d_9")], h.Script.Deletes);
        Assert.Empty(h.Script.Sets);
    }

    /// <summary>The time runs for the reply's timeout (Swift: what the gate was asked).</summary>
    [Fact]
    public async Task TimeoutWithoutDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(InitThenHang()));
        await h.StartAsync(BoardCase("1"), "");
        await h.UntilAsync(() => FakeClaudeScript.Starts(h.Dir.Path) == 1, "the start");
        h.Time.Advance(Assistant.SuggestReplyTimeout - TimeSpan.FromMilliseconds(1));
        await h.Ui.DrainAsync();
        Assert.True((await h.StateAsync()).IsRunning);
        h.Time.Advance(TimeSpan.FromMilliseconds(1));
        await h.UntilAsync(() => !h.Controller.State.IsRunning, "the timeout");
        await h.KillAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.Timeout), await h.StateAsync());
        Assert.Empty(h.Script.Deletes);
    }

    /// <summary>Quitting stops the request and waits for the delete of its draft, at most the bound.</summary>
    [Fact]
    public async Task QuitCleansUp()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftThenHang()));
        await h.StartAsync(BoardCase("1"), "");
        await h.UntilAsync(() => h.Controller.Created is not null, "the draft");
        await (await h.Ui.RunAsync(() => h.Controller.CancelAndCleanUpAsync()));
        Assert.Equal([Deleted("d_9")], h.Script.Deletes);
        Assert.True(await h.Ui.RunAsync(() => h.Controller.IsIdle));
        await h.KillAsync();
    }

    /// <summary>A daemon that does not answer the delete does not hold the quit beyond the bound.</summary>
    [Fact]
    public async Task QuitIsBounded()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftThenHang()));
        h.Script.HoldDeletes(true);
        await h.StartAsync(BoardCase("1"), "");
        await h.UntilAsync(() => h.Controller.Created is not null, "the draft");
        var quitting = await h.Ui.RunAsync(() => h.Controller.CancelAndCleanUpAsync(TimeSpan.FromMilliseconds(50)));
        await Eventually.Holds(() => h.Script.Deletes.Count == 1, what: "the delete");
        Assert.False(quitting.IsCompleted);
        h.Time.Advance(TimeSpan.FromMilliseconds(50));
        await quitting;
        // The delete still waits.
        Assert.False(await h.Ui.RunAsync(() => h.Controller.IsIdle));
        h.Script.HoldDeletes(false);
        await h.KillAsync();
        Assert.True(await h.Ui.RunAsync(() => h.Controller.IsIdle));
    }

    /// <summary>One request at a time; another case shows that one runs elsewhere.</summary>
    [Fact]
    public async Task OneAtATime()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(InitThenHang()));
        var snapshot = new Board.Snapshot { Cases = [BoardCase("1"), BoardCase("2")] };
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Controller.Start(BoardCase("1"), ""));
            Assert.False(h.Controller.Start(BoardCase("2"), ""));
            var here = h.Controller.View(BoardCase("1"), snapshot, samples: false);
            Assert.True(here.Running && !here.Enabled && here.Note.Length == 0);
            var there = h.Controller.View(BoardCase("2"), snapshot, samples: false);
            Assert.True(!there.Running && !there.Enabled && there.Note == Board.Text.SuggestReplyElsewhere);
        });
        await h.UntilAsync(() => FakeClaudeScript.Starts(h.Dir.Path) == 1, "the start");
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.Controller.Start(BoardCase("2"), ""));
            h.Controller.Cancel();
        });
        await h.KillAsync();
        Assert.Equal(["c_1"], h.Script.Gets);
    }

    /// <summary>The assistant's consent is asked first; declined, nothing runs.</summary>
    [Fact]
    public async Task ConsentDeclined()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()), consent: false);
        h.ConsentAnswer = false;
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(1, h.ConsentAsked);
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
        Assert.Empty(h.Script.Gets);
        Assert.False(h.Settings.AssistantConsent);
        Assert.False(h.Settings.BoardTriageConsent);
    }

    /// <summary>Allowed: the assistant's consent is kept, the board's is not touched.</summary>
    [Fact]
    public async Task ConsentGiven()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()), consent: false);
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(1, h.ConsentAsked);
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
        Assert.True(h.Settings.AssistantConsent);
        Assert.False(h.Settings.BoardTriageConsent);
        Assert.Single(h.Script.Sets);
    }

    [Fact]
    public async Task BoardGetFails()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()));
        h.Script.GetFailure = new RpcError { Code = ErrorCode.StorageError, Message = "x" };
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.Backend), await h.StateAsync());
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
    }

    /// <summary>The case has a suggested reply by now: nothing is asked.</summary>
    [Fact]
    public async Task CaseHasDraftAlready()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()));
        h.Script.CaseDraft = new BoardDraft { DraftId = "d_1", Text = "x", Updated = T0 };
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
        Assert.Equal(1, h.Refreshes);
    }

    [Fact]
    public async Task NotSignedIn()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(new FakeClaudeScript([DraftTurn()], auth: FakeClaudeScript.SignedIn(false)));
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.NotSignedIn), await h.StateAsync());
        Assert.False(await h.Ui.RunAsync(() => h.Controller.SignedIn));
        var v = await h.ViewAsync(BoardCase("1"));
        Assert.True(!v.Enabled && v.Note == Assistant.SignInTexts().Hint);
    }

    [Fact]
    public async Task ToolsMissing()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(CannedStreamJson.InitFailed, CannedStreamJson.Result("x"))));
        await h.StartAsync(BoardCase("1"), "");
        await h.IdleAsync();
        Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.ToolsMissing), await h.StateAsync());
    }

    /// <summary>Nothing starts without the feature, the bridge or Claude Code; a request that loses the feature stops.</summary>
    [Fact]
    public async Task Availability()
    {
        RequireWindows();
        await using (var h = await Harness.CreateAsync(Fake(InitThenHang())))
        {
            h.Available = false;
            await h.Ui.RunAsync(() =>
            {
                Assert.False(h.Controller.Start(BoardCase("1"), ""));
                Assert.False(h.Controller.View(BoardCase("1"), new Board.Snapshot(), samples: false).Shown);
            });
            h.Available = true;
            h.Found = false;
            await h.Ui.RunAsync(() =>
            {
                Assert.False(h.Controller.Start(BoardCase("1"), ""));
                var v = h.Controller.View(BoardCase("1"), new Board.Snapshot(), samples: false);
                Assert.True(v.Shown && !v.Enabled && v.Note == Assistant.PanelTexts().NotFound);
            });
            h.Found = true;
            await h.Ui.RunAsync(() =>
            {
                var withDraft = BoardCase("1") with { Draft = new Board.DraftLink("d_1", "x") };
                Assert.False(h.Controller.Start(withDraft, ""));
                Assert.True(h.Controller.Start(BoardCase("1"), ""));
            });
            await h.UntilAsync(() => FakeClaudeScript.Starts(h.Dir.Path) == 1, "the start");
            h.Available = false;
            await h.Ui.RunAsync(() =>
            {
                h.Controller.AvailabilityChanged();
                Assert.Equal(new ReplyState.Failed(C1, Board.SuggestReplyFailure.Cancelled), h.Controller.State);
            });
            await h.KillAsync();
        }

        await using var none = await Harness.CreateAsync(Fake(InitThenHang()), bridge: null);
        await none.Ui.RunAsync(() =>
        {
            Assert.False(none.Controller.Start(BoardCase("1"), ""));
            Assert.False(none.Controller.View(BoardCase("1"), new Board.Snapshot(), samples: false).Shown);
        });
    }

    /// <summary>Go: the samples never reach Start (their reply target is always null).</summary>
    [Fact]
    public async Task NeverOffersTheSamples()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()));
        var sample = BoardCase("1") with { Reply = null };
        Assert.False(await h.Ui.RunAsync(() => h.Controller.Start(sample, "")));
        await h.IdleAsync();
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
        Assert.Empty(h.Script.Gets);
    }

    /// <summary>Windows: a consent hook that fails declines, and its failure is reported as a callback's.</summary>
    [Fact]
    public async Task AFailingConsentHookDeclines()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(DraftTurn()), consent: false);
        await h.Ui.RunAsync(() => h.Controller.Consent = () => throw new InvalidOperationException("no window"));
        await h.StartAsync(BoardCase("1"), "");
        var failures = await Assert.ThrowsAsync<AggregateException>(h.IdleAsync);
        Assert.IsType<InvalidOperationException>(Assert.Single(failures.InnerExceptions));
        Assert.Equal(new ReplyState.Idle(), await h.StateAsync());
        Assert.False(h.Settings.AssistantConsent);
        Assert.Empty(h.Script.Gets);
    }

    /// <summary>The daemon's side: board.get, board.setDraft, draft.delete.</summary>
    private sealed class ReplyScript
    {
        private readonly Lock gate = new();
        private readonly List<string> gets = [];
        private readonly List<BoardSetDraftParams> sets = [];
        private readonly List<DraftDeleteParams> deletes = [];
        private TaskCompletionSource? heldDeletes;

        public RpcError? GetFailure { get; set; }

        public RpcError? SetFailure { get; set; }

        public BoardDraft? CaseDraft { get; set; }

        public IReadOnlyList<string> Gets => Locked(gets);

        public IReadOnlyList<BoardSetDraftParams> Sets => Locked(sets);

        public IReadOnlyList<DraftDeleteParams> Deletes => Locked(deletes);

        public void Install(FakeDaemon fake)
        {
            fake.On(API.BoardGet.Name, Get);
            fake.On(API.BoardSetDraft.Name, SetDraft);
            fake.On(API.DraftDelete.Name, (FakeDaemon.MethodHandler)DeleteAsync);
        }

        public void HoldDeletes(bool on)
        {
            TaskCompletionSource? release;
            lock (gate)
            {
                release = on ? null : heldDeletes;
                heldDeletes = on ? new(TaskCreationOptions.RunContinuationsAsynchronously) : null;
            }
            release?.TrySetResult();
        }

        private string Get(string json)
        {
            var q = JsonCoding.Decode<BoardGetParams>(json);
            lock (gate)
            {
                gets.Add(q.CaseId.Value);
            }
            if (GetFailure is { } f)
            {
                throw new RpcException(f);
            }
            var n = q.CaseId.Value[2..];
            var messages = Enumerable.Range(1, 8).Select(i => new BoardMessage
            {
                Id = "m_" + n + "_" + i,
                FolderId = "f_inbox",
                From = new Address { Email = "x@y" },
                Date = T0,
                Mine = false,
                Text = "t",
            }).ToList();
            return JsonCoding.EncodeToString(new BoardGetResult { Case = Wire(n, CaseDraft), Messages = messages });
        }

        private string SetDraft(string json)
        {
            var q = JsonCoding.Decode<BoardSetDraftParams>(json);
            lock (gate)
            {
                sets.Add(q);
            }
            if (SetFailure is { } f)
            {
                throw new RpcException(f);
            }
            var n = q.CaseId.Value[2..];
            return JsonCoding.EncodeToString(new BoardSetDraftResult { Case = Wire(n, new BoardDraft { DraftId = q.DraftId, Text = "x", Updated = T0 }) });
        }

        private async Task<string> DeleteAsync(string json)
        {
            Task wait;
            lock (gate)
            {
                deletes.Add(JsonCoding.Decode<DraftDeleteParams>(json));
                wait = heldDeletes?.Task ?? Task.CompletedTask;
            }
            await wait;
            return "{}";
        }

        private IReadOnlyList<T> Locked<T>(List<T> list)
        {
            lock (gate)
            {
                return [.. list];
            }
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private int refreshes;
        private int consentAsked;
        private volatile bool available = true;
        private volatile bool found = true;
        private volatile bool consentAnswer = true;

        private Harness(FakeClaudeScript script)
        {
            Claude = script.CreateIn(Dir.Path);
            Work = Path.Combine(Dir.Path, "work");
        }

        public TemporaryDirectory Dir { get; } = new();

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new();

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public FakeDaemon Fake { get; } = new();

        public ReplyScript Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public string Claude { get; }

        public string Work { get; }

        public AssistantRequest Request { get; private set; } = null!;

        public BoardReplyController Controller { get; private set; } = null!;

        public bool Available
        {
            get => available;
            set => available = value;
        }

        public bool Found
        {
            get => found;
            set => found = value;
        }

        public bool ConsentAnswer
        {
            get => consentAnswer;
            set => consentAnswer = value;
        }

        public int ConsentAsked => Volatile.Read(ref consentAsked);

        public int Refreshes => Volatile.Read(ref refreshes);

        /// <summary>Every state the controller reported, as it changed (on the UI thread).</summary>
        public List<ReplyState> States { get; } = [];

        public static async Task<Harness> CreateAsync(FakeClaudeScript script, bool consent = true, string? bridge = Bridge)
        {
            var h = new Harness(script);
            h.Script.Install(h.Fake);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            await h.Ui.RunAsync(() =>
            {
                h.Settings.AssistantClaudePath = h.Claude;
                h.Settings.AssistantConsent = consent;
                h.Settings.BoardTriageConsent = false;
                // The panel's model and the board's apart: the reply takes the panel's.
                h.Settings.AssistantModel = AssistantModel.Haiku;
                h.Settings.BoardTriageModel = AssistantModel.Opus;
                var prefix = h.Dir.Path + @"\";
                var locator = new ClaudeCodeLocator(
                    h.Settings,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path)),
                    timeout: TimeSpan.FromSeconds(60),
                    usable: p => h.found && p.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) && ClaudeCodeLocator.IsExecutableFile(p));
                h.Request = new AssistantRequest(
                    h.Settings,
                    locator,
                    h.Work,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path)),
                    killGrace: Grace,
                    timeout: TimeSpan.FromSeconds(30),
                    time: h.Time,
                    pending: h.Pending);
                h.Controller = new BoardReplyController(
                    h.Client, h.Settings, locator, h.Request, bridge, Socket, () => h.available, time: h.Time, pending: h.Pending)
                {
                    Consent = () =>
                    {
                        Interlocked.Increment(ref h.consentAsked);
                        return Task.FromResult(h.consentAnswer);
                    },
                    OnRefresh = () => Interlocked.Increment(ref h.refreshes),
                };
                h.Controller.Observe(() =>
                {
                    if (h.States.Count == 0 || h.States[^1] != h.Controller.State)
                    {
                        h.States.Add(h.Controller.State);
                    }
                });
            });
            return h;
        }

        public Task StartAsync(Board.Case c, string instruction) => Ui.RunAsync(() => Assert.True(Controller.Start(c, instruction)));

        public Task<ReplyState> StateAsync() => Ui.RunAsync(() => Controller.State);

        public Task<Board.SuggestReplyView> ViewAsync(Board.Case c) => Ui.RunAsync(() => Controller.View(c, new Board.Snapshot(), samples: false));

        // The request's work, processes and calls are done; a process ends by
        // itself or at the kill, after the grace on the fake clock.
        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake, timeout: TimeSpan.FromSeconds(60));

        /// <summary>The stand-in that never ends is killed after the grace; then everything settles.</summary>
        public async Task KillAsync()
        {
            await Ui.DrainAsync();
            Time.Advance(Grace);
            await IdleAsync();
        }

        /// <summary>Moves the clock on, then waits until nothing is left to happen.</summary>
        public async Task AdvanceAsync(TimeSpan d)
        {
            await IdleAsync();
            Time.Advance(d);
            await IdleAsync();
        }

        /// <summary>Waits until <paramref name="condition"/> holds on the UI thread, for what the controller does not report.</summary>
        public async Task UntilAsync(Func<bool> condition, string what)
        {
            var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(30);
            while (!await Ui.RunAsync(condition))
            {
                if (DateTime.UtcNow > deadline)
                {
                    throw new TimeoutException(what);
                }
                await Task.Delay(5);
            }
        }

        // Cancels, kills what is left and waits for it, so the directory can go.
        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                Controller.Dispose();
                Request.Close();
            });
            Time.Advance(TimeSpan.FromDays(1));
            try
            {
                await IdleAsync();
            }
            catch (AggregateException)
            {
                // A test's own failure, reported there.
            }
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
            Dir.Dispose();
        }
    }
}
