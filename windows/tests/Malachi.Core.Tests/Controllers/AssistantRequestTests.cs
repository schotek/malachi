// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantRequestTests.swift (the
// one-shot requests: AssistantRequest, the compose window's
// ComposeRewriteController and the search field's SearchConversion) and of
// ui/internal/assistantpanel/oneshot_test.go, against the stand-in
// claude.exe of Malachi.FakeClaude. No real Claude Code is ever run here.
//
// Windows differences, as the controllers': the timeout and the kill grace
// run on a fake clock, so a claude that never answers (Swift's "sleep 30",
// FakeClaudeStep.Hang here) is timed out and killed by advancing it; the
// tests wait for a report (UiConditions) or until the request's work and
// processes are done (IdleAsync, which counts a process until its end was
// reported), never for time. The private directory is shown made by the
// injected factory where Swift checks mode 0700. Swift's "sleep 1" before
// the late answer of rewriteCancelled stays a sleep of the stand-in: the
// test waits for that process to end by itself. Added: Close, and a
// consent hook that fails.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
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
using Outcome = Malachi.Core.Controllers.AssistantRequest.Outcome;
using RewriteState = Malachi.Core.Controllers.ComposeRewriteController.RewriteState;
using SearchOutcome = Malachi.Core.Controllers.SearchConversion.Outcome;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class AssistantRequestTests
{
    private static readonly TimeSpan Grace = TimeSpan.FromMilliseconds(300);

    private static void RequireWindows() => Assert.SkipUnless(OperatingSystem.IsWindows(), "the stand-in claude is a Windows program");

    private static FakeClaudeScript Fake(params IReadOnlyList<FakeClaudeStep>[] turns) => new(turns);

    private static Outcome.Failed Stopped(string reason) => new(new AssistantRequest.Failure.Stopped(reason));

    // AssistantRequest

    /// <summary>One turn without the bridge: the one-shot command line, the message on stdin, stdin closed, the streamed text, then the result.</summary>
    [Fact]
    public async Task AnswersOnce()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init,
            CannedStreamJson.Delta("Dear "),
            CannedStreamJson.Delta("Jana"),
            CannedStreamJson.Text("Dear Jana,"),
            CannedStreamJson.Result("Dear Jana,"))));
        await h.Ui.RunAsync(() =>
        {
            h.Request.Start("SYS", "line one\nline two", h.Complete, onText: h.Text);
            Assert.True(h.Request.Running);
        });
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        await h.IdleAsync();
        Assert.Equal([new Outcome.Answered("Dear Jana,", null)], h.Outcomes);
        Assert.Equal(["Dear ", "Dear Jana", "Dear Jana,"], h.Texts);
        Assert.False(h.Request.Running);
        Assert.Equal(0, h.ConsentAsked);
        Assert.Equal(1, FakeClaudeScript.Starts(h.Dir.Path));
        Assert.Equal(["line one\nline two"], FakeClaudeScript.Prompts(h.Dir.Path));
        var args = FakeClaudeScript.Args(h.Dir.Path);
        Assert.Equal(Assistant.Args(new AssistantOptions { Bridge = "", Model = AssistantModel.Haiku, SystemPrompt = "SYS" }), args);
        Assert.DoesNotContain("--mcp-config", args);
        Assert.DoesNotContain("--allowedTools", args);
        var env = FakeClaudeScript.Env(h.Dir.Path);
        Assert.DoesNotContain(env.Keys, k => k.StartsWith("ANTHROPIC", StringComparison.OrdinalIgnoreCase) || k.StartsWith("CLAUDECODE", StringComparison.OrdinalIgnoreCase));
        Assert.Equal("cs_CZ.UTF-8", env["LANG"]);
        Assert.Equal(h.Work, FakeClaudeScript.Cwd(h.Dir.Path), ignoreCase: true);
        // Swift checks mode 0700: the directory was made private by the factory.
        Assert.Equal([h.Work], h.Directories.Ensured);
    }

    /// <summary>stdin is closed after the one message: a Claude Code that answers nothing ends at once, and that is a failure with its status.</summary>
    [Fact]
    public async Task StdinIsClosed()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(CannedStreamJson.Init)));
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        Assert.Equal([Stopped("claude exited with status 0")], h.Outcomes);
    }

    /// <summary>A schema goes on the command line; the structured_output comes back as it was written.</summary>
    [Fact]
    public async Task StructuredAnswer()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.StructuredResult("""{"query":"x"}"""))));
        await h.StartAsync("S", "m", Assistant.SearchSchema);
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        Assert.Equal([new Outcome.Answered("", Encoding.UTF8.GetBytes("""{"query":"x"}"""))], h.Outcomes);
        Assert.Equal(["--json-schema", Assistant.SearchSchema], FakeClaudeScript.Args(h.Dir.Path).TakeLast(2));
    }

    /// <summary>Consent: declined, nothing starts; allowed, it is kept, and the answer counts even when the request was cancelled meanwhile.</summary>
    [Fact]
    public async Task Consent()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")), consent: false);
        h.ConsentAnswer = false;
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        await h.IdleAsync();
        Assert.Equal([new Outcome.Declined()], h.Outcomes);
        Assert.Equal(1, h.ConsentAsked);
        Assert.False(h.Settings.AssistantConsent);
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));

        // Allowed while the request was cancelled: kept, nothing sent.
        var answer = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        var asked = false;
        await h.Ui.RunAsync(() => h.Request.Consent = () =>
        {
            asked = true;
            h.Changed();
            return answer.Task;
        });
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => asked, "the question");
        await h.Ui.RunAsync(h.Request.Cancel);
        answer.SetResult(true);
        await h.IdleAsync();
        Assert.True(h.Settings.AssistantConsent);
        Assert.Single(h.Outcomes);
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));

        // From now on nobody is asked.
        await h.Ui.RunAsync(() => h.Request.Consent = null);
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count == 2, "the second outcome");
        Assert.Equal(new Outcome.Answered("done", null), h.Outcomes[^1]);
    }

    /// <summary>Without a consent hook nothing is ever sent.</summary>
    [Fact]
    public async Task NoConsentHookSendsNothing()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")), consent: false);
        await h.Ui.RunAsync(() => h.Request.Consent = null);
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        Assert.Equal([new Outcome.Declined()], h.Outcomes);
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
    }

    /// <summary>Windows: a consent hook that fails declines, and its failure is reported as a callback's.</summary>
    [Fact]
    public async Task AFailingConsentHookDeclines()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")), consent: false);
        await h.Ui.RunAsync(() => h.Request.Consent = () => throw new InvalidOperationException("no window"));
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        var failures = await Assert.ThrowsAsync<AggregateException>(h.IdleAsync);
        Assert.IsType<InvalidOperationException>(Assert.Single(failures.InnerExceptions));
        Assert.Equal([new Outcome.Declined()], h.Outcomes);
        Assert.False(h.Settings.AssistantConsent);
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
    }

    /// <summary>Claude Code missing or signed out: the panel's texts.</summary>
    [Fact]
    public async Task NotFoundAndNotSignedIn()
    {
        RequireWindows();
        await using var missing = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("x")), found: false);
        await missing.StartAsync("S", "m");
        await missing.WhenAsync(() => missing.Outcomes.Count > 0, "the outcome");
        Assert.Equal([new Outcome.Failed(new AssistantRequest.Failure.NotFound())], missing.Outcomes);
        Assert.Equal("Claude Code was not found on this computer", new AssistantRequest.Failure.NotFound().Text);
        Assert.Equal(0, FakeClaudeScript.Starts(missing.Dir.Path));

        await using var signedOut = await Harness.CreateAsync(new FakeClaudeScript([CannedStreamJson.AnswerTurn("x")], auth: FakeClaudeScript.SignedIn(false)));
        await signedOut.StartAsync("S", "m");
        await signedOut.WhenAsync(() => signedOut.Outcomes.Count > 0, "the outcome");
        Assert.Equal([new Outcome.Failed(new AssistantRequest.Failure.NotSignedIn())], signedOut.Outcomes);
        Assert.Equal("Claude Code is not signed in. Sign in under AI in the preferences.", new AssistantRequest.Failure.NotSignedIn().Text);
        Assert.Equal("Claude Code is not signed in. Sign in under AI in the preferences.", new AssistantRequest.Failure.NotSignedIn().Reason);
        Assert.Equal("The assistant stopped: x", new AssistantRequest.Failure.Stopped("x").Text);
        Assert.Equal("x", new AssistantRequest.Failure.Stopped("x").Reason);
        Assert.Equal("Claude Code was not found on this computer", new AssistantRequest.Failure.NotFound().Reason);
        Assert.Equal(0, FakeClaudeScript.Starts(signedOut.Dir.Path));
    }

    /// <summary>
    /// oneshot_test.go TestRequestRefusedSignIn: the API refuses the sign-in
    /// although auth status says loggedIn, and the request ends as not signed
    /// in; Claude Code's own message is no answer. Another refusal is the
    /// result's.
    /// </summary>
    [Fact]
    public async Task RefusedSignIn()
    {
        RequireWindows();
        const string refused = "Failed to authenticate. API Error: 401";
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init, CannedStreamJson.Failure("authentication_failed", refused), CannedStreamJson.Result(refused, success: false))));
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        Assert.Equal([new Outcome.Failed(new AssistantRequest.Failure.NotSignedIn())], h.Outcomes);
        Assert.Empty(h.Texts);

        const string limit = "API Error: Rate limit reached";
        await using var other = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init, CannedStreamJson.Failure("rate_limit", limit), CannedStreamJson.Result(limit, success: false))));
        await other.StartAsync("S", "m");
        await other.WhenAsync(() => other.Outcomes.Count > 0, "the outcome");
        Assert.Equal([Stopped(limit)], other.Outcomes);
        Assert.Empty(other.Texts);
    }

    /// <summary>A result that is not a success, and an exit before the result.</summary>
    [Fact]
    public async Task Failures()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(new FakeClaudeScript(
            [CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.ErrorResult)],
            onStart: [FakeClaudeStep.IfStart(2, FakeClaudeStep.ReadLine(), FakeClaudeStep.Stderr("Error: boom\n"), FakeClaudeStep.Exit(1))]));
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count == 1, "the first outcome");
        Assert.Equal(Stopped("error_during_execution"), h.Outcomes[0]);
        await h.StartAsync("S", "m");
        await h.WhenAsync(() => h.Outcomes.Count == 2, "the second outcome");
        Assert.Equal(Stopped("Error: boom"), h.Outcomes[1]);
    }

    /// <summary>No answer in time: the process is ended and the request fails.</summary>
    [Fact]
    public async Task Timeout()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake([FakeClaudeStep.Lines(CannedStreamJson.Init, CannedStreamJson.Delta("thinking")), FakeClaudeStep.Hang()]));
        await h.Ui.RunAsync(() =>
        {
            h.Request.Timeout = TimeSpan.FromMilliseconds(500);
            h.Request.Start("S", "m", h.Complete, onText: h.Text);
        });
        await h.WhenAsync(() => h.Texts.Count == 1, "the streamed text");
        h.Time.Advance(TimeSpan.FromMilliseconds(500));
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        Assert.Equal([Stopped(AssistantRequest.TimedOut)], h.Outcomes);
        Assert.Equal(["thinking"], h.Texts);
        Assert.False(await h.Ui.RunAsync(() => h.Request.Running));
        // The process is killed after the grace.
        h.Time.Advance(Grace);
        await h.IdleAsync();
    }

    /// <summary>Cancelled: no completion, the process ended; a new request cancels the one under way.</summary>
    [Fact]
    public async Task CancelAndReplace()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            [FakeClaudeStep.Lines(CannedStreamJson.Init, CannedStreamJson.Delta("slow")), FakeClaudeStep.Hang()],
            CannedStreamJson.AnswerTurn("second")));
        await h.Ui.RunAsync(() => h.Request.Start("S", "one", h.Complete, onText: h.Text));
        await h.WhenAsync(() => h.Texts.SequenceEqual(["slow"]), "the streamed text");
        await h.Ui.RunAsync(() =>
        {
            h.Request.Cancel();
            Assert.False(h.Request.Running);
        });
        h.Time.Advance(Grace);
        await h.IdleAsync();
        Assert.Empty(h.Outcomes);

        // The fake numbers its turns over every start: the second start gets
        // "second".
        await h.Ui.RunAsync(() =>
        {
            h.Request.Start("S", "two", h.Complete);
            h.Request.Start("S", "two again", h.Complete);
        });
        await h.WhenAsync(() => h.Outcomes.Count > 0, "the outcome");
        await h.IdleAsync();
        Assert.Equal([new Outcome.Answered("done", null)], h.Outcomes);
        var prompts = FakeClaudeScript.Prompts(h.Dir.Path);
        Assert.Equal("one", prompts[0]);
        Assert.Equal("two again", prompts[^1]);
        Assert.DoesNotContain("two", prompts);
    }

    /// <summary>Windows: a closed request starts nothing (the window that owns it closed).</summary>
    [Fact]
    public async Task AClosedRequestStartsNothing()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        await h.Ui.RunAsync(() =>
        {
            h.Request.Close();
            h.Request.Start("S", "m", h.Complete);
            Assert.False(h.Request.Running);
            Assert.True(h.Request.IsClosed);
        });
        await h.IdleAsync();
        Assert.Empty(h.Outcomes);
        Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
    }

    // ComposeRewriteController

    [Fact]
    public async Task RewriteRunsAndCleans()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init,
            CannedStreamJson.Delta(@"```\n„Dobrý"),
            CannedStreamJson.Delta(@" den.“\n```"),
            CannedStreamJson.Text(@"```\n„Dobrý den.“\n```"),
            CannedStreamJson.Result(@"```\n„Dobrý den.“\n```"))));
        var states = new List<RewriteState>();
        var rewrite = await h.Ui.RunAsync(() => new ComposeRewriteController(h.Request, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            rewrite.StateChanged += (_, s) =>
            {
                states.Add(s);
                h.Changed();
            };
            Assert.True(rewrite.Start(AssistantRewrite.Politer, "", "  Ahoj.\n"));
            Assert.True(rewrite.Running);
        });
        await h.WhenAsync(() => !rewrite.Running, "the end of the rewrite");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(new RewriteState.Done("Dobrý den."), rewrite.State);
            Assert.Equal(new RewriteState.Running(""), states[0]);
            Assert.Contains(new RewriteState.Running("```\n„Dobrý"), states);
            Assert.Equal(new RewriteState.Done("Dobrý den."), states[^1]);
        });
        Assert.Equal([Assistant.RewriteMessage(AssistantRewrite.Politer, "", "Ahoj.")], FakeClaudeScript.Prompts(h.Dir.Path));
        Assert.Equal(
            Assistant.Args(new AssistantOptions { Bridge = "", Model = AssistantModel.Haiku, SystemPrompt = Assistant.RewriteSystemPrompt() }),
            FakeClaudeScript.Args(h.Dir.Path));
    }

    [Fact]
    public async Task RewriteRefusesAndFails()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.Result("""  \"\"  """))));
        var rewrite = await h.Ui.RunAsync(() => new ComposeRewriteController(h.Request, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            rewrite.StateChanged += (_, _) => h.Changed();
            // Nothing to ask: nothing happens.
            Assert.False(rewrite.Start(AssistantRewrite.Fix, "", " \n "));
            Assert.False(rewrite.Start(AssistantRewrite.Custom, "  ", "text"));
            Assert.Equal(new RewriteState.Idle(), rewrite.State);
            // Too long: fails at once.
            Assert.True(rewrite.Start(AssistantRewrite.Fix, "", new string('a', Assistant.MaxPassage + 1)));
            Assert.Equal(
                new RewriteState.Failed("The assistant stopped: assistant: the passage is too long: 20001 characters, at most 20000"),
                rewrite.State);
            Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
            // An empty answer is a failure, never an empty replacement.
            Assert.True(rewrite.Start(AssistantRewrite.Custom, "Make it shorter", "text"));
        });
        await h.WhenAsync(() => !rewrite.Running, "the end of the rewrite");
        await h.Ui.RunAsync(() => Assert.Equal(new RewriteState.Failed("The assistant stopped: the answer is empty"), rewrite.State));
        Assert.Equal(["Follow this instruction: Make it shorter\n\nPassage:\n<<<\ntext\n>>>"], FakeClaudeScript.Prompts(h.Dir.Path));
        Assert.Equal(1, FakeClaudeScript.Starts(h.Dir.Path));
        // Cancelled: idle.
        await h.Ui.RunAsync(() =>
        {
            rewrite.Cancel();
            Assert.Equal(new RewriteState.Idle(), rewrite.State);
        });
    }

    [Fact]
    public async Task RewriteErrorsAndDeclinedConsent()
    {
        RequireWindows();
        await using var missing = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("x")), found: false);
        var rewrite = await missing.Ui.RunAsync(() => new ComposeRewriteController(missing.Request, missing.Pending));
        await missing.Ui.RunAsync(() =>
        {
            rewrite.StateChanged += (_, _) => missing.Changed();
            rewrite.Start(AssistantRewrite.Shorter, "", "text");
        });
        await missing.WhenAsync(() => !rewrite.Running, "the end of the rewrite");
        await missing.Ui.RunAsync(() => Assert.Equal(new RewriteState.Failed("Claude Code was not found on this computer"), rewrite.State));

        await using var asked = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("x")), consent: false);
        asked.ConsentAnswer = false;
        var declined = await asked.Ui.RunAsync(() => new ComposeRewriteController(asked.Request, asked.Pending));
        await asked.Ui.RunAsync(() =>
        {
            declined.StateChanged += (_, _) => asked.Changed();
            declined.Start(AssistantRewrite.Shorter, "", "text");
        });
        await asked.WhenAsync(() => !declined.Running, "the end of the rewrite");
        await asked.Ui.RunAsync(() => Assert.Equal(new RewriteState.Idle(), declined.State));
        Assert.Equal(1, asked.ConsentAsked);
    }

    /// <summary>Cancelled while running: idle, and the late answer is dropped.</summary>
    [Fact]
    public async Task RewriteCancelled()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake([
            FakeClaudeStep.Lines(CannedStreamJson.Init, CannedStreamJson.Delta("x")),
            FakeClaudeStep.Sleep(1000),
            FakeClaudeStep.Lines(CannedStreamJson.Result("late")),
        ]));
        var rewrite = await h.Ui.RunAsync(() => new ComposeRewriteController(h.Request, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            rewrite.StateChanged += (_, _) => h.Changed();
            rewrite.Start(AssistantRewrite.Fix, "", "text");
        });
        await h.WhenAsync(() => rewrite.State == new RewriteState.Running("x"), "the streamed text");
        await h.Ui.RunAsync(() =>
        {
            rewrite.Cancel();
            Assert.Equal(new RewriteState.Idle(), rewrite.State);
        });
        // The stand-in answers late and ends by itself (its input is closed).
        await h.IdleAsync();
        await h.Ui.RunAsync(() => Assert.Equal(new RewriteState.Idle(), rewrite.State));
    }

    // SearchConversion

    [Fact]
    public async Task SearchConverts()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.StructuredResult("""{"query":"from:jan  faktur\nafter:2026-03-01"}""")),
            // No structured_output: the text is read the same way.
            CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.Result("""{\"query\":\"is:unread\"}""")),
            CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.StructuredResult("""{"q":"x"}""", "no JSON"))));
        var got = new List<SearchOutcome>();
        var search = await h.Ui.RunAsync(() => new SearchConversion(h.Request, h.Pending) { Today = () => "2026-09-29" });
        void Record(SearchOutcome o)
        {
            got.Add(o);
            h.Changed();
        }
        await h.Ui.RunAsync(() =>
        {
            Assert.True(search.Convert("  faktury od Jany z března  ", Record));
            Assert.True(search.Running);
        });
        await h.WhenAsync(() => got.Count == 1, "the first query");
        Assert.Equal([new SearchOutcome.Query("from:jan faktur after:2026-03-01")], got);
        Assert.Equal(["faktury od Jany z března"], FakeClaudeScript.Prompts(h.Dir.Path));
        Assert.Equal(
            Assistant.Args(new AssistantOptions
            {
                Bridge = "",
                Model = AssistantModel.Haiku,
                SystemPrompt = Assistant.SearchSystemPrompt("2026-09-29"),
                JsonSchema = Assistant.SearchSchema,
            }),
            FakeClaudeScript.Args(h.Dir.Path));

        await h.Ui.RunAsync(() => search.Convert("unread", Record));
        await h.WhenAsync(() => got.Count == 2, "the second query");
        Assert.Equal(new SearchOutcome.Query("is:unread"), got[1]);

        await h.Ui.RunAsync(() => search.Convert("x", Record));
        await h.WhenAsync(() => got.Count == 3, "the third answer");
        Assert.Equal(new SearchOutcome.Failed("The search could not be converted: the answer holds no query"), got[2]);
    }

    [Fact]
    public async Task SearchRefusesAndFails()
    {
        RequireWindows();
        var got = new List<SearchOutcome>();
        await using var missing = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("x")), found: false);
        var search = await missing.Ui.RunAsync(() => new SearchConversion(missing.Request, missing.Pending));
        void Record(SearchOutcome o)
        {
            got.Add(o);
            missing.Changed();
        }
        await missing.Ui.RunAsync(() =>
        {
            Assert.False(search.Convert("   ", Record));
            Assert.True(search.Convert(new string('a', Assistant.MaxSearchWords + 1), Record));
            Assert.Empty(got); // later, never inside Convert
        });
        await missing.WhenAsync(() => got.Count == 1, "the failure");
        Assert.Equal(new SearchOutcome.Failed("The search could not be converted: assistant: the words are too long: 501 characters, at most 500"), got[0]);
        await missing.Ui.RunAsync(() => search.Convert("faktury", Record));
        await missing.WhenAsync(() => got.Count == 2, "the second failure");
        Assert.Equal(new SearchOutcome.Failed("The search could not be converted: Claude Code was not found on this computer"), got[1]);

        await using var signedOut = await Harness.CreateAsync(new FakeClaudeScript([CannedStreamJson.AnswerTurn("x")], auth: FakeClaudeScript.SignedIn(false)));
        var other = await signedOut.Ui.RunAsync(() => new SearchConversion(signedOut.Request, signedOut.Pending));
        void RecordOther(SearchOutcome o)
        {
            got.Add(o);
            signedOut.Changed();
        }
        await signedOut.Ui.RunAsync(() => other.Convert("faktury", RecordOther));
        await signedOut.WhenAsync(() => got.Count == 3, "the third failure");
        Assert.Equal(
            new SearchOutcome.Failed("The search could not be converted: Claude Code is not signed in. Sign in under AI in the preferences."), got[2]);
    }

    [Fact]
    public void SearchOutcomes()
    {
        Assert.Equal(new SearchOutcome.Declined(), SearchConversion.OutcomeOf(new Outcome.Declined()));
        Assert.Equal(
            new SearchOutcome.Failed("The search could not be converted: API Error: 500"),
            SearchConversion.OutcomeOf(Stopped("API Error: 500\nmore")));
        Assert.Equal(new SearchOutcome.Query("x"), SearchConversion.OutcomeOf(new Outcome.Answered("", """{"query":" x "}"""u8.ToArray())));
        // The structured answer wins over the text.
        Assert.Equal(
            new SearchOutcome.Query("structured"),
            SearchConversion.OutcomeOf(new Outcome.Answered("""{"query":"text"}""", """{"query":"structured"}"""u8.ToArray())));
        Assert.Equal(
            new SearchOutcome.Query("text"),
            SearchConversion.OutcomeOf(new Outcome.Answered("""{"query":"text"}""", """{"query":""}"""u8.ToArray())));
        Assert.Equal(
            new SearchOutcome.Failed("The search could not be converted: the answer holds no query"),
            SearchConversion.OutcomeOf(new Outcome.Answered("", null)));
        // oneshot_test.go: the failures' reasons as the panel words them.
        Assert.Equal(
            new SearchOutcome.Failed("The search could not be converted: Claude Code was not found on this computer"),
            SearchConversion.OutcomeOf(new Outcome.Failed(new AssistantRequest.Failure.NotFound())));
    }

    /// <summary>
    /// A request over a stand-in claude in a directory of its own, on the
    /// test's UI thread (Swift's RequestHarness): consent given unless
    /// asked, the model Haiku, the locator kept to the directory, the
    /// environment with what must never reach claude.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly UiConditions conditions = new();

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

        public FakePrivateDirectories Directories { get; } = new();

        public string Claude { get; }

        public string Work { get; }

        public AssistantRequest Request { get; private set; } = null!;

        public int ConsentAsked { get; private set; }

        public bool ConsentAnswer { get; set; } = true;

        public List<Outcome> Outcomes { get; } = [];

        public List<string> Texts { get; } = [];

        public static async Task<Harness> CreateAsync(FakeClaudeScript script, bool consent = true, bool found = true)
        {
            var h = new Harness(script);
            h.Request = await h.Ui.RunAsync(() =>
            {
                h.Settings.AssistantClaudePath = h.Claude;
                h.Settings.AssistantConsent = consent;
                h.Settings.AssistantModel = AssistantModel.Haiku;
                var prefix = h.Dir.Path + @"\";
                var locator = new ClaudeCodeLocator(
                    h.Settings,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path)),
                    timeout: TimeSpan.FromSeconds(60),
                    usable: p => found && p.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) && ClaudeCodeLocator.IsExecutableFile(p));
                var request = new AssistantRequest(
                    h.Settings,
                    locator,
                    h.Work,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path), ("LANG", "cs_CZ.UTF-8"), ("ANTHROPIC_API_KEY", "sk-never"), ("CLAUDECODE", "1")),
                    killGrace: Grace,
                    timeout: TimeSpan.FromSeconds(10),
                    time: h.Time,
                    directories: h.Directories,
                    pending: h.Pending);
                request.Consent = () =>
                {
                    h.ConsentAsked++;
                    return Task.FromResult(h.ConsentAnswer);
                };
                return request;
            });
            return h;
        }

        /// <summary>A completion that records the outcome.</summary>
        public void Complete(Outcome outcome)
        {
            Outcomes.Add(outcome);
            conditions.Changed();
        }

        /// <summary>An onText that records the text.</summary>
        public void Text(string text)
        {
            Texts.Add(text);
            conditions.Changed();
        }

        /// <summary>Something the conditions look at changed (on the UI thread).</summary>
        public void Changed() => conditions.Changed();

        public Task StartAsync(string systemPrompt, string message, string jsonSchema = "") =>
            Ui.RunAsync(() => Request.Start(systemPrompt, message, Complete, jsonSchema));

        public Task WhenAsync(Func<bool> condition, string what) => conditions.WhenAsync(Ui, condition, what);

        // The request's work and processes are done; a process ends by
        // itself or at the kill, after the grace on the fake clock.
        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, timeout: TimeSpan.FromSeconds(60));

        // Cancels, kills what is left and waits for it, so the directory can go.
        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(Request.Close);
            Time.Advance(TimeSpan.FromDays(1));
            try
            {
                await IdleAsync();
            }
            catch (AggregateException)
            {
                // A test's own failure, reported there.
            }
            Ui.Dispose();
            Dir.Dispose();
        }
    }
}
