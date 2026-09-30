// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ClaudeCodeProcessTests in macos/Tests/MalachiCoreTests/
// ClaudeCodeProcessTests.swift (its ClaudeCodeLocatorTests are
// ClaudeCodeLocatorTests.cs) and of ui/internal/assistantpanel/
// process_test.go (with TestProcessDropsAnOverlongLine, which Swift did not
// port), against the stand-in claude.exe of Malachi.FakeClaude instead of
// #!/bin/sh scripts. No real Claude Code is ever run here.
//
// Windows differences, as the process's: there is no SIGTERM, so Swift's
// sigkillAfterTheGrace is KilledAfterTheGrace, a claude that does not end
// at the end of its input (FakeClaudeStep.Hang) killed after the grace,
// with -1, and sigtermEndsAWaitingProcess is
// TheEndOfInputEndsAWaitingProcess, a claude that ends at the end of its
// input with 0. The grace runs on a fake clock, so the kill is shown to come
// from it, not timed. The environment of the child is Windows' (USERPROFILE
// and a PATH of claude's directory and the system's; the stand-in needs
// SystemRoot). A test waits for a report (UiConditions), never for time.
// Added: the start twice, the input closed, the events on the UI thread,
// and arguments that cmd.exe could not carry, passed unchanged.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Platform;
using Malachi.Core.Tests.Assistants;
using Malachi.Core.Tests.Controllers;
using Malachi.Core.Tests.Fixtures;
using Malachi.FakeClaude;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Platform;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class ClaudeCodeProcessTests
{
    // ClaudeCodeProcessTests.swift's initLine.
    private const string InitLine = """{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"connected"}],"tools":[]}""";

    private static readonly TimeSpan Grace = TimeSpan.FromMilliseconds(300);

    private static void RequireWindows() => Assert.SkipUnless(OperatingSystem.IsWindows(), "the stand-in claude is a Windows program");

    /// <summary>Each stdin line is a turn: its events arrive in order, one process keeps them all, and the end is reported once.</summary>
    [Fact]
    public async Task TurnsArriveInOrder()
    {
        RequireWindows();
        static IReadOnlyList<FakeClaudeStep> Turn(int t) => CannedStreamJson.Turn(
            InitLine,
            CannedStreamJson.Delta("turn " + t),
            "not json at all",
            "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"turn " + t + " done\"}]}}",
            "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok " + t + "\"}");
        await using var h = new Harness(new FakeClaudeScript([Turn(1), Turn(2), Turn(3)]));
        await h.StartAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Process.Running);
            for (var i = 1; i <= 3; i++)
            {
                Assert.True(h.Process.Send(Assistant.UserMessage("question " + i)));
            }
        });
        await h.WhenAsync(() => h.Count(AssistantEventKind.Result) == 3, "three results");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(["turn 1 done", "turn 2 done", "turn 3 done"], h.Events.Where(e => e.Kind == AssistantEventKind.Text).Select(e => e.Text));
            Assert.Equal(
                [AssistantEventKind.SystemInit, AssistantEventKind.TextDelta, AssistantEventKind.Text, AssistantEventKind.Result],
                h.Events.Take(4).Select(e => e.Kind));
            Assert.All(h.EventThreads, id => Assert.Equal(h.Ui.ThreadId, id));
            Assert.Empty(h.Exits);
        });
        var stdin = FakeClaudeScript.StdinLines(h.Dir.Path);
        Assert.Equal(3, stdin.Count);
        Assert.Contains(stdin, l => l.Contains("question 2", StringComparison.Ordinal));

        await h.Ui.RunAsync(h.Process.Terminate);
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() =>
        {
            Assert.Single(h.Exits);
            Assert.False(h.Process.Running);
            Assert.False(h.Process.Send(Assistant.UserMessage("late")));
            h.Process.Terminate(); // no second report
        });
        await h.Ui.DrainAsync();
        await h.Ui.RunAsync(() => Assert.Single(h.Exits));
    }

    /// <summary>An early exit reports the status and stderr's first line, after the events it printed.</summary>
    [Fact]
    public async Task EarlyExitReportsStderr()
    {
        RequireWindows();
        await using var h = new Harness(new FakeClaudeScript([], onStart: [
            FakeClaudeStep.Lines(InitLine),
            FakeClaudeStep.Stderr("Error: Invalid API key · Please run /login\n"),
            FakeClaudeStep.Stderr("second line\n"),
            FakeClaudeStep.Exit(3),
        ]));
        await h.StartAsync();
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([new ClaudeCodeExit(3, "Error: Invalid API key · Please run /login")], h.Exits);
            Assert.Equal([AssistantEventKind.SystemInit], h.Events.Select(e => e.Kind));
            Assert.Equal(3, h.Process.Ended?.Status);
            Assert.False(h.Process.Send("x"u8.ToArray()));
        });
    }

    /// <summary>Many lines and then the exit: every event comes first.</summary>
    [Fact]
    public async Task EveryEventBeforeTheExit()
    {
        RequireWindows();
        var lines = Enumerable.Range(1, 200).Select(i => CannedStreamJson.Delta(i + " ")).ToArray();
        await using var h = new Harness(new FakeClaudeScript([], onStart: [FakeClaudeStep.Lines(lines), FakeClaudeStep.Exit(0)]));
        await h.StartAsync();
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Enumerable.Range(1, 200).Select(i => i + " "), h.Events.Select(e => e.Text));
            Assert.Equal([new ClaudeCodeExit(0, "")], h.Exits);
            Assert.Equal("claude exited with status 0", h.Exits[0].Description);
        });
    }

    /// <summary>stderr is kept bounded, the reason is its first line cut at 400 bytes.</summary>
    [Fact]
    public async Task StderrIsBounded()
    {
        RequireWindows();
        await using var h = new Harness(new FakeClaudeScript([], onStart: [FakeClaudeStep.FillStderr(200_000), FakeClaudeStep.Exit(1)]));
        await h.StartAsync();
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(1, h.Exits[0].Status);
            Assert.Equal(ClaudeCodeProcess.ReasonLimit, Encoding.UTF8.GetByteCount(h.Exits[0].Reason));
        });
    }

    /// <summary>process_test.go: a line longer than the limit is dropped; the lines around it are not.</summary>
    [Fact]
    public async Task DropsAnOverlongLine()
    {
        RequireWindows();
        using var bigDir = new TemporaryDirectory();
        var big = Path.Combine(bigDir.Path, "big");
        File.WriteAllText(big, "{\"x\":\"" + new string('y', ClaudeCodeProcess.MaxLine) + "\"}\n");
        await using var h = new Harness(new FakeClaudeScript([], onStart: [
            FakeClaudeStep.Lines(InitLine),
            FakeClaudeStep.PrintFile(big),
            FakeClaudeStep.Lines(CannedStreamJson.Result("ok")),
            FakeClaudeStep.Exit(0),
        ]));
        await h.StartAsync();
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() => Assert.Equal([AssistantEventKind.SystemInit, AssistantEventKind.Result], h.Events.Select(e => e.Kind)));
    }

    /// <summary>
    /// Swift sigkillAfterTheGrace: a claude that does not end at the end of
    /// its input is killed with its tree after the grace, and its end is
    /// still reported once.
    /// </summary>
    [Fact]
    public async Task KilledAfterTheGrace()
    {
        RequireWindows();
        await using var h = new Harness(new FakeClaudeScript([], onStart: [FakeClaudeStep.Lines(InitLine), FakeClaudeStep.Hang()]));
        await h.StartAsync();
        await h.WhenAsync(() => h.Events.Count == 1, "init");
        await h.Ui.RunAsync(h.Process.Terminate);
        // Its input is closed; nothing kills it before the grace is over.
        h.Time.Advance(Grace - TimeSpan.FromMilliseconds(1));
        await h.Ui.RunAsync(() => Assert.True(h.Process.Running));
        h.Time.Advance(TimeSpan.FromMilliseconds(1));
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(-1, h.Exits[0].Status);
            Assert.Equal("claude was killed", h.Exits[0].Description);
            h.Process.Terminate();
        });
        h.Time.Advance(TimeSpan.FromDays(1));
        await h.Ui.DrainAsync();
        await h.Ui.RunAsync(() => Assert.Single(h.Exits));
    }

    /// <summary>
    /// Swift sigtermEndsAWaitingProcess: a claude waiting for its next turn
    /// ends at the end of its input, before any kill.
    /// </summary>
    [Fact]
    public async Task TheEndOfInputEndsAWaitingProcess()
    {
        RequireWindows();
        await using var h = new Harness(new FakeClaudeScript([], onStart: [FakeClaudeStep.Lines(InitLine)]));
        await h.StartAsync();
        await h.WhenAsync(() => h.Events.Count == 1, "init");
        await h.Ui.RunAsync(h.Process.Terminate);
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() => Assert.Equal([new ClaudeCodeExit(0, "")], h.Exits));
    }

    /// <summary>The environment is exactly the one given, the working directory the private one.</summary>
    [Fact]
    public async Task EnvironmentAndDirectory()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var claude = new FakeClaudeScript([]).CreateIn(dir.Path);
        var work = Path.Combine(dir.Path, "work");
        Directory.CreateDirectory(work);
        var env = Assistant.ChildEnvironment(
            CannedStreamJson.Environment(("USERPROFILE", dir.Path), ("ANTHROPIC_API_KEY", "sk-test"), ("CLAUDECODE", "1"), ("LANG", "cs_CZ.UTF-8")),
            claude);
        await using var h = new Harness(dir, claude, env, work, []);
        await h.StartAsync();
        await h.Ui.RunAsync(h.Process.CloseInput);
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        var seen = FakeClaudeScript.Env(dir.Path);
        Assert.Equal(dir.Path, seen["USERPROFILE"]);
        Assert.Equal("cs_CZ.UTF-8", seen["LANG"]);
        Assert.StartsWith(dir.Path + ";", seen["PATH"], StringComparison.OrdinalIgnoreCase);
        Assert.Equal(env["PATH"], seen["PATH"]);
        Assert.DoesNotContain(seen.Keys, k => k.StartsWith("ANTHROPIC", StringComparison.OrdinalIgnoreCase));
        Assert.DoesNotContain(seen.Keys, k => k.StartsWith("CLAUDECODE", StringComparison.OrdinalIgnoreCase));
        Assert.Equal(env.Keys.Order(StringComparer.OrdinalIgnoreCase), seen.Keys.Order(StringComparer.OrdinalIgnoreCase), StringComparer.OrdinalIgnoreCase);
        Assert.Equal(Path.TrimEndingDirectorySeparator(work), Path.TrimEndingDirectorySeparator(FakeClaudeScript.Cwd(dir.Path)), ignoreCase: true);
    }

    /// <summary>
    /// Windows: the arguments reach claude as they were, JSON and quotes,
    /// spaces, backslashes and line breaks included: the process is started
    /// directly, never through cmd.exe.
    /// </summary>
    [Fact]
    public async Task ArgumentsPassUnchanged()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var claude = new FakeClaudeScript([]).CreateIn(dir.Path);
        string[] arguments =
        [
            "-p", "", "--mcp-config", """{"mcpServers":{"malachi":{"command":"C:\\Program Files\\x.exe","args":["--socket","a b"]}}}""",
            "line one\nline two", "\"quoted\"", @"trailing\", "%PATH%", "a & b | c > d",
        ];
        await using var h = new Harness(dir, claude, CannedStreamJson.Environment(("USERPROFILE", dir.Path)), dir.Path, arguments);
        await h.StartAsync();
        await h.Ui.RunAsync(h.Process.CloseInput);
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        Assert.Equal(arguments, FakeClaudeScript.Args(dir.Path));
    }

    [Fact]
    public async Task LaunchFailureThrows()
    {
        using var dir = new TemporaryDirectory();
        using var ui = new TestUIContext();
        var p = new ClaudeCodeProcess(Path.Combine(dir.Path, "missing.exe"), [], new Dictionary<string, string>(), dir.Path);
        await ui.RunAsync(() =>
        {
            var e = Assert.Throws<ClaudeCodeStartException>(p.Start);
            Assert.Equal(ClaudeCodeStartFailure.Launch, e.Failure);
            Assert.StartsWith("claude could not be started: ", e.Message, StringComparison.Ordinal);
            Assert.DoesNotContain(dir.Path, e.Message, StringComparison.OrdinalIgnoreCase);
            Assert.False(p.Running);
            Assert.False(p.Send("x"u8.ToArray()));
        });
    }

    /// <summary>Windows: a second start is refused (Swift's StartError.alreadyStarted, which its suite does not test).</summary>
    [Fact]
    public async Task ASecondStartIsRefused()
    {
        RequireWindows();
        await using var h = new Harness(new FakeClaudeScript([]));
        await h.StartAsync();
        await h.Ui.RunAsync(() =>
        {
            var e = Assert.Throws<ClaudeCodeStartException>(h.Process.Start);
            Assert.Equal(ClaudeCodeStartFailure.AlreadyStarted, e.Failure);
            Assert.Equal("claude was started already", e.Message);
        });
    }

    /// <summary>
    /// Windows: a one-shot turn. The input closed after it, claude answers
    /// and ends by itself, and nothing more can be sent.
    /// </summary>
    [Fact]
    public async Task ClosedInputEndsAfterTheAnswer()
    {
        RequireWindows();
        await using var h = new Harness(new FakeClaudeScript([CannedStreamJson.AnswerTurn("Hi")]));
        await h.StartAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.Process.Send(Assistant.UserMessage("one")));
            h.Process.CloseInput();
            Assert.False(h.Process.Send(Assistant.UserMessage("two")));
        });
        await h.WhenAsync(() => h.Exits.Count > 0, "the exit");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(1, h.Count(AssistantEventKind.Result));
            Assert.Equal([new ClaudeCodeExit(0, "")], h.Exits);
        });
        Assert.Equal(["one"], FakeClaudeScript.Prompts(h.Dir.Path));
    }

    /// <summary>
    /// A stand-in claude in a directory of its own, run with the arguments of
    /// the Swift tests, its reports recorded on the test's UI thread.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly TemporaryDirectory? ownDir;
        private readonly UiConditions conditions = new();

        public Harness(FakeClaudeScript script)
        {
            ownDir = new TemporaryDirectory();
            Dir = ownDir;
            var claude = script.CreateIn(Dir.Path);
            Process = Make(claude, CannedStreamJson.Environment(("USERPROFILE", Dir.Path), ("PATH", @"C:\Windows\System32")), Dir.Path, ["-p", "--verbose"]);
        }

        public Harness(TemporaryDirectory dir, string claude, IReadOnlyDictionary<string, string> env, string work, IReadOnlyList<string> arguments)
        {
            Dir = dir;
            Process = Make(claude, env, work, arguments);
        }

        public TemporaryDirectory Dir { get; }

        public TestUIContext Ui { get; } = new();

        public FakeTimeProvider Time { get; } = new();

        public ClaudeCodeProcess Process { get; }

        public List<AssistantEvent> Events { get; } = [];

        public List<int> EventThreads { get; } = [];

        public List<ClaudeCodeExit> Exits { get; } = [];

        public int Count(AssistantEventKind kind) => Events.Count(e => e.Kind == kind);

        public Task StartAsync() => Ui.RunAsync(Process.Start);

        public Task WhenAsync(Func<bool> condition, string what) => conditions.WhenAsync(Ui, condition, what);

        // Ends a claude that is still there (the kill after the grace, on the
        // fake clock) and waits for its end, so that its directory can go.
        public async ValueTask DisposeAsync()
        {
            var started = await Ui.RunAsync(() =>
            {
                Process.Terminate();
                return Process.Running;
            });
            Time.Advance(TimeSpan.FromDays(1));
            if (started)
            {
                await WhenAsync(() => !Process.Running, "the end at the disposal");
            }
            Ui.Dispose();
            ownDir?.Dispose();
        }

        private ClaudeCodeProcess Make(string claude, IReadOnlyDictionary<string, string> env, string work, IReadOnlyList<string> arguments)
        {
            var p = new ClaudeCodeProcess(claude, arguments, env, work, Grace, Time);
            p.EventsReceived += (_, events) =>
            {
                Events.AddRange(events);
                EventThreads.Add(Environment.CurrentManagedThreadId);
                conditions.Changed();
            };
            p.Exited += (_, exit) =>
            {
                Exits.Add(exit);
                conditions.Changed();
            };
            return p;
        }
    }
}
