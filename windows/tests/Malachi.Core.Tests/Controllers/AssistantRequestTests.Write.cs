// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistantpanel/writer_test.go (TestWriterRunsAndCleans,
// TestWriterRefusesAndFails, TestWriterDeclinedAndCancelled): the compose
// window's assistant bar (ComposeWriteController) against the stand-in
// claude.exe of Malachi.FakeClaude, over AssistantRequestTests' harness, as
// the rewrite's tests are; macOS keeps the writing in its window, untested.
// As there, the tests wait for a report (UiConditions) or until the
// request's work and processes are done, never for time; Go's "sleep 1"
// before the late answer stays a sleep of the stand-in. Windows adds the
// mode of each kind of compose window (Swift's String(describing:)).

using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Assistants;
using Malachi.FakeClaude;
using Xunit;
using RewriteState = Malachi.Core.Controllers.ComposeRewriteController.RewriteState;

namespace Malachi.Core.Tests.Controllers;

public sealed partial class AssistantRequestTests
{
    // ComposeWriteController

    /// <summary>The bar's request: the JSON message under the writing's system prompt, the answer streamed and cleaned, then done.</summary>
    [Fact]
    public async Task WriterRunsAndCleans()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init,
            CannedStreamJson.Delta("„Dobrý"),
            CannedStreamJson.Delta(" den.“"),
            CannedStreamJson.Text("„Dobrý den.“"),
            CannedStreamJson.Result(@"„Dobrý den.“\nS pozdravem"))));
        var states = new List<RewriteState>();
        var writer = await h.Ui.RunAsync(() => new ComposeWriteController(h.Request, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            writer.StateChanged += (_, s) =>
            {
                states.Add(s);
                h.Changed();
            };
            writer.Start(AssistantWriteMode.Reply, "Accept", "Meeting", "", "Can you come?");
            Assert.True(writer.Running);
        });
        await h.WhenAsync(() => !writer.Running, "the end of the writing");
        var done = new RewriteState.Done("„Dobrý den.“\nS pozdravem");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(done, writer.State);
            Assert.Equal(new RewriteState.Running(""), states[0]);
            Assert.Contains(new RewriteState.Running("„Dobrý"), states);
            Assert.Equal(done, states[^1]);
        });
        Assert.Equal(
            [Assistant.WriteMessage(AssistantWriteMode.Reply, "Accept", "Meeting", "", "Can you come?")],
            FakeClaudeScript.Prompts(h.Dir.Path));
        Assert.Equal(
            Assistant.Args(new AssistantOptions { Bridge = "", Model = AssistantModel.Haiku, SystemPrompt = Assistant.WriteSystemPrompt() }),
            FakeClaudeScript.Args(h.Dir.Path));
    }

    /// <summary>
    /// Nothing to work from fails at once and sends nothing; an empty answer
    /// is a failure, never an empty replacement; Cancel goes back to idle.
    /// </summary>
    [Fact]
    public async Task WriterRefusesAndFails()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.Result("""  \"\"  """))));
        var writer = await h.Ui.RunAsync(() => new ComposeWriteController(h.Request, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            writer.StateChanged += (_, _) => h.Changed();
            writer.Start(AssistantWriteMode.New, "  ", "Subject", "", "");
            Assert.Equal(new RewriteState.Failed("The assistant stopped: assistant: an empty passage"), writer.State);
            writer.Start(AssistantWriteMode.Forward, "", "", "", new string('a', Assistant.MaxPassage));
            Assert.Equal(
                new RewriteState.Failed("The assistant stopped: assistant: the passage is too long: 20007 characters, at most 20000"),
                writer.State);
            Assert.Equal(0, FakeClaudeScript.Starts(h.Dir.Path));
            writer.Start(AssistantWriteMode.New, "Say hello", "", "", "");
        });
        await h.WhenAsync(() => !writer.Running, "the end of the writing");
        await h.Ui.RunAsync(() => Assert.Equal(new RewriteState.Failed("The assistant stopped: the answer is empty"), writer.State));
        Assert.Equal(1, FakeClaudeScript.Starts(h.Dir.Path));
        await h.Ui.RunAsync(() =>
        {
            writer.Cancel();
            Assert.Equal(new RewriteState.Idle(), writer.State);
        });
    }

    /// <summary>Declined consent: idle, nothing sent; cancelled while running: idle, and the late answer is dropped.</summary>
    [Fact]
    public async Task WriterDeclinedAndCancelled()
    {
        RequireWindows();
        await using (var asked = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("x")), consent: false))
        {
            asked.ConsentAnswer = false;
            var declined = await asked.Ui.RunAsync(() => new ComposeWriteController(asked.Request, asked.Pending));
            await asked.Ui.RunAsync(() =>
            {
                declined.StateChanged += (_, _) => asked.Changed();
                declined.Start(AssistantWriteMode.Reply, "Hi", "", "", "");
            });
            await asked.WhenAsync(() => !declined.Running, "the end of the writing");
            await asked.Ui.RunAsync(() => Assert.Equal(new RewriteState.Idle(), declined.State));
            Assert.Equal(1, asked.ConsentAsked);
            Assert.Equal(0, FakeClaudeScript.Starts(asked.Dir.Path));
        }

        await using var h = await Harness.CreateAsync(Fake([
            FakeClaudeStep.Lines(CannedStreamJson.Init, CannedStreamJson.Delta("x")),
            FakeClaudeStep.Sleep(1000),
            FakeClaudeStep.Lines(CannedStreamJson.Result("late")),
        ]));
        var writer = await h.Ui.RunAsync(() => new ComposeWriteController(h.Request, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            writer.StateChanged += (_, _) => h.Changed();
            writer.Start(AssistantWriteMode.Reply, "Hi", "", "", "");
        });
        await h.WhenAsync(() => writer.State == new RewriteState.Running("x"), "the streamed text");
        await h.Ui.RunAsync(() =>
        {
            writer.Cancel();
            Assert.Equal(new RewriteState.Idle(), writer.State);
        });
        // The stand-in answers late and ends by itself (its input is closed).
        await h.IdleAsync();
        await h.Ui.RunAsync(() => Assert.Equal(new RewriteState.Idle(), writer.State));
    }

    /// <summary>Windows: the mode of each kind of compose window, as Swift names its ComposeKind.</summary>
    [Theory]
    [InlineData(ComposeKind.New, AssistantWriteMode.New)]
    [InlineData(ComposeKind.Reply, AssistantWriteMode.Reply)]
    [InlineData(ComposeKind.ReplyAll, AssistantWriteMode.ReplyAll)]
    [InlineData(ComposeKind.Forward, AssistantWriteMode.Forward)]
    [InlineData(ComposeKind.Edit, AssistantWriteMode.Edit)]
    public void WriterModeOfAComposeKind(ComposeKind kind, AssistantWriteMode want) =>
        Assert.Equal(want, ComposeWriteController.Mode(kind));
}
