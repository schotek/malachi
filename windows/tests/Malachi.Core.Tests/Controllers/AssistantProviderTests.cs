// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first provider adaptation of AssistantRequestTests and
// ui/internal/assistantpanel/oneshot_test.go. In-memory sessions exercise
// controller consent and generation boundaries independently of either CLI.

using System;
using System.Collections.Generic;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Platform;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class AssistantProviderTests
{
    [Fact]
    public async Task ClaudeConsentDoesNotAuthorizeChatGpt()
    {
        await using var h = await Harness.CreateAsync(consent: false);
        await h.Ui.RunAsync(() =>
        {
            h.Settings.AssistantConsent = true;
            h.ConsentAnswer = false;
            h.Start();
        });
        await h.IdleAsync();
        Assert.IsType<AssistantRequest.Outcome.Declined>(Assert.Single(h.Outcomes));
        Assert.Equal(1, h.ConsentAsked);
        Assert.Equal(0, h.Provider.OpenCalls);
        Assert.False(h.Provider.HasConsent);
        Assert.True(h.Settings.AssistantConsent);
        Assert.Equal(0, h.ClaudeProbes);
    }

    [Fact]
    public async Task AcceptedChatGptConsentDoesNotAuthorizeClaude()
    {
        await using var h = await Harness.CreateAsync(consent: false);
        await h.Ui.RunAsync(h.Start);
        await h.Provider.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() => h.Provider.Session.Emit(Result("OpenAI answer")));
        await h.IdleAsync();
        Assert.Equal(1, h.Provider.AcceptedConsent);
        Assert.True(h.Provider.HasConsent);
        Assert.False(h.Settings.AssistantConsent);
        await h.Ui.RunAsync(() =>
        {
            h.Request.Provider = null;
            h.ConsentAnswer = false;
            h.Start();
        });
        await h.IdleAsync();
        Assert.Equal(2, h.ConsentAsked);
        Assert.IsType<AssistantRequest.Outcome.Answered>(h.Outcomes[0]);
        Assert.IsType<AssistantRequest.Outcome.Declined>(h.Outcomes[1]);
        Assert.False(h.Settings.AssistantConsent);
        Assert.Equal(0, h.ClaudeProbes);
    }

    [Fact]
    public async Task ExistingChatGptConsentSkipsClaudeConsentAndLocator()
    {
        await using var h = await Harness.CreateAsync();
        await h.Ui.RunAsync(() => { h.ConsentAnswer = false; h.Start(); });
        await h.Provider.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() => h.Provider.Session.Emit(Result("answer")));
        await h.IdleAsync();
        Assert.Equal(0, h.ConsentAsked);
        Assert.False(h.Settings.AssistantConsent);
        Assert.Equal(0, h.ClaudeProbes);
        Assert.Equal("answer", Assert.IsType<AssistantRequest.Outcome.Answered>(Assert.Single(h.Outcomes)).Text);
    }

    [Fact]
    public async Task ChangingProviderTerminatesOldSessionAndIgnoresItsLateEvents()
    {
        await using var h = await Harness.CreateAsync();
        var first = h.Provider;
        var second = h.NewProvider();
        await h.Ui.RunAsync(h.Start);
        await first.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() =>
        {
            first.Session.Emit(Delta("before switch"));
            h.Request.Provider = second;
            Assert.False(h.Request.Running);
            h.Start();
        });
        await second.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() =>
        {
            first.Session.Emit(Delta("old text"), Result("old answer"));
            first.Session.EmitExit("old exit");
            Assert.True(h.Request.Running);
            second.Session.Emit(Delta("new text"), Result("new answer"));
        });
        await h.IdleAsync();
        Assert.True(first.OpenCancellation.IsCancellationRequested);
        Assert.True(first.Session.Terminated);
        Assert.Equal(["before switch", "new text"], h.Texts);
        Assert.Equal("new answer", Assert.IsType<AssistantRequest.Outcome.Answered>(Assert.Single(h.Outcomes)).Text);
        Assert.All(h.CallbackThreads, thread => Assert.Equal(h.Ui.ThreadId, thread));
        Assert.Equal(AssistantToolPolicy.None, first.Spec!.ToolPolicy);
        Assert.Equal(AssistantToolPolicy.None, second.Spec!.ToolPolicy);
    }

    [Theory]
    [InlineData("cancel")]
    [InlineData("close")]
    [InlineData("switch")]
    public async Task CancellationDuringOpenTerminatesLateSessionWithoutSubmitting(string action)
    {
        await using var h = await Harness.CreateAsync(delayOpen: true);
        await h.Ui.RunAsync(h.Start);
        await h.Provider.OpenEntered.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() =>
        {
            if (action == "close") h.Request.Close();
            else if (action == "switch") h.Request.Provider = h.NewProvider();
            else h.Request.Cancel();
        });
        Assert.True(h.Provider.OpenCancellation.IsCancellationRequested);
        // The provider deliberately ignores cancellation until acquiring its
        // resource. The controller must dispose this returned session itself.
        h.Provider.ReleaseOpen();
        await h.IdleAsync();
        Assert.True(h.Provider.Session.Terminated);
        Assert.Empty(h.Provider.Session.Inputs);
        Assert.Empty(h.Outcomes);
        Assert.Empty(h.Texts);
    }

    [Fact]
    public async Task LateConsentCannotAuthorizeEitherProviderAfterSwitch()
    {
        await using var h = await Harness.CreateAsync(consent: false);
        var first = h.Provider;
        var second = h.NewProvider();
        var consentAsked = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var decision = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        await h.Ui.RunAsync(() =>
        {
            h.Request.Consent = () => { consentAsked.TrySetResult(); return decision.Task; };
            h.Start();
        });
        await consentAsked.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        await h.Ui.RunAsync(() => { h.Request.Provider = second; h.Start(); });
        await second.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        decision.SetResult(true);
        await h.Ui.RunAsync(() => second.Session.Emit(Result("new provider")));
        await h.IdleAsync();
        Assert.Equal(0, first.AcceptedConsent);
        Assert.False(first.HasConsent);
        Assert.Equal(0, first.OpenCalls);
        Assert.Equal(0, second.AcceptedConsent);
        Assert.False(h.Settings.AssistantConsent);
        Assert.Equal("new provider", Assert.IsType<AssistantRequest.Outcome.Answered>(Assert.Single(h.Outcomes)).Text);
    }

    [Fact]
    public async Task ProviderOpeningCountsTowardRequestTimeoutAndCleansLateSession()
    {
        await using var h = await Harness.CreateAsync(delayOpen: true);
        await h.Ui.RunAsync(() => { h.Request.Timeout = TimeSpan.FromSeconds(1); h.Start(); });
        await h.Provider.OpenEntered.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        h.Time.Advance(TimeSpan.FromSeconds(2));
        await h.Completed.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        h.Provider.ReleaseOpen();
        await h.IdleAsync();
        var failure = Assert.IsType<AssistantRequest.Outcome.Failed>(Assert.Single(h.Outcomes));
        Assert.Equal(AssistantRequest.TimedOut, Assert.IsType<AssistantRequest.Failure.Stopped>(failure.Failure).Reason);
        Assert.True(h.Provider.Session.Terminated);
        Assert.Empty(h.Provider.Session.Inputs);
    }

    [Fact]
    public async Task RewriteUsesSharedPromptAndCleansProviderStreamAndFinalText()
    {
        await using var h = await Harness.CreateAsync();
        var rewrite = await h.Ui.RunAsync(() => new ComposeRewriteController(h.Request, h.Pending));
        try
        {
            await h.Ui.RunAsync(() => Assert.True(rewrite.Start(AssistantRewrite.Politer, "", "  Ahoj.\n")));
            await h.Provider.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
            await h.Ui.RunAsync(() =>
            {
                h.Provider.Session.Emit(Delta("Dobrý"));
                Assert.Equal(new ComposeRewriteController.RewriteState.Running("Dobrý"), rewrite.State);
                h.Provider.Session.Emit(Result("```\n„Dobrý den.“\n```"));
                Assert.Equal(new ComposeRewriteController.RewriteState.Done("Dobrý den."), rewrite.State);
            });
            await h.IdleAsync();
            Assert.Equal(Assistant.RewriteSystemPrompt(), h.Provider.Spec!.SystemPrompt);
            Assert.Empty(h.Provider.Spec.JsonSchema);
            Assert.Equal(AssistantToolPolicy.None, h.Provider.Spec.ToolPolicy);
            Assert.Equal(Assistant.RewriteMessage(AssistantRewrite.Politer, "", "Ahoj."), Assert.Single(h.Provider.Session.Inputs));
            Assert.False(h.Settings.AssistantConsent);
        }
        finally { await h.Ui.RunAsync(rewrite.Dispose); }
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task SearchUsesSharedSchemaAndAcceptsNormalizedStructuredOrTextResults(bool structured)
    {
        await using var h = await Harness.CreateAsync();
        var search = await h.Ui.RunAsync(() => new SearchConversion(h.Request, h.Pending) { Today = () => "2026-10-02" });
        SearchConversion.Outcome? outcome = null;
        try
        {
            await h.Ui.RunAsync(() => Assert.True(search.Convert("unread messages", result => outcome = result)));
            await h.Provider.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
            const string Json = """{"query":"is:unread"}""";
            await h.Ui.RunAsync(() => h.Provider.Session.Emit(Result(structured ? "not JSON" : Json,
                structured ? Encoding.UTF8.GetBytes(Json) : null)));
            await h.IdleAsync();
            Assert.Equal(new SearchConversion.Outcome.Query("is:unread"), outcome);
            Assert.Equal(Assistant.SearchSchema, h.Provider.Spec!.JsonSchema);
            Assert.Equal(Assistant.SearchSystemPrompt("2026-10-02"), h.Provider.Spec.SystemPrompt);
            Assert.Equal(AssistantToolPolicy.None, h.Provider.Spec.ToolPolicy);
            Assert.Equal(Assistant.SearchMessage("unread messages"), Assert.Single(h.Provider.Session.Inputs));
        }
        finally { await h.Ui.RunAsync(search.Dispose); }
    }

    [Fact]
    public async Task PanelSubtitleShowsSelectedProviderBeforeFirstQuestion()
    {
        await using var h = await Harness.CreateAsync();
        var panel = await h.Ui.RunAsync(() => new AssistantPanelController(h.Settings, h.Locator,
            null, "unused-socket", h.Directory.Path, new Dictionary<string, string>(), time: h.Time, pending: h.Pending)
        { Provider = h.Provider });
        try
        {
            await h.Ui.RunAsync(() =>
            {
                Assert.Equal("ChatGPT (Codex, experimental) · test-model", panel.Subtitle);
                Assert.Equal(0, h.Provider.OpenCalls);
                Assert.Equal(0, h.ClaudeProbes);
                h.Provider.SelectedModel = "";
                Assert.Equal("ChatGPT (Codex, experimental) · Use the provider’s default model", panel.Subtitle);
                panel.Provider = null;
                h.Settings.AssistantModel = AssistantModel.Opus;
                Assert.Equal("Claude Code · Opus", panel.Subtitle);
            });
        }
        finally { await h.Ui.RunAsync(panel.Dispose); }
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task PanelNewConversationOrProviderSwitchDropsOldTranscriptAndLateEvents(bool switchProvider)
    {
        await using var h = await Harness.CreateAsync();
        var first = h.Provider;
        var second = h.NewProvider();
        var panel = await h.Ui.RunAsync(() => new AssistantPanelController(h.Settings, h.Locator,
            null, "unused-socket", h.Directory.Path, new Dictionary<string, string>(), time: h.Time, pending: h.Pending)
        { Provider = first });
        try
        {
            await h.Ui.RunAsync(() => Assert.True(panel.Submit("first question")));
            await first.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
            await h.Ui.RunAsync(() =>
            {
                first.Session.Emit(Delta("old answer"));
                Assert.Contains(panel.Items, item => item.Content is AnswerContent { Text: "old answer" });
                if (switchProvider) panel.Provider = second;
                else panel.NewConversation();
                first.Session.Emit(Delta("stale"), Result("stale answer"));
                first.Session.EmitExit("stale exit");
                Assert.Empty(panel.Items);
                Assert.Empty(panel.PinnedContexts);
                Assert.Equal(AssistantPanelController.Phase.Idle, panel.CurrentPhase);
                Assert.Null(panel.ProviderSession);
            });
            await h.IdleAsync();
            Assert.True(first.Session.Terminated);
            Assert.Equal(AssistantToolPolicy.Panel, first.Spec!.ToolPolicy);
            if (switchProvider)
            {
                await h.Ui.RunAsync(() => Assert.True(panel.Submit("second question")));
                await second.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
                await h.Ui.RunAsync(() =>
                {
                    first.Session.Emit(Delta("old secret"), Result("old secret"));
                    second.Session.Emit(Delta("fresh"), Result("fresh"));
                    Assert.Single(panel.Items, item => item.Content is AnswerContent);
                    Assert.Contains(panel.Items, item => item.Content is AnswerContent { Text: "fresh" });
                    Assert.DoesNotContain("first question", Assert.Single(second.Session.Inputs), StringComparison.Ordinal);
                });
            }
        }
        finally { await h.Ui.RunAsync(panel.Dispose); }
    }

    private static AssistantEvent Delta(string text) => new(AssistantEventKind.TextDelta) { Text = text };
    private static AssistantEvent Result(string text, byte[]? structured = null) => new(AssistantEventKind.Result)
    { Success = true, ResultText = text, Structured = structured };

    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<FakeProvider> providers = [];
        public TestUIContext Ui { get; } = new();
        public PendingWork Pending { get; } = new();
        public FakeTimeProvider Time { get; } = new();
        public TemporaryDirectory Directory { get; } = new();
        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);
        public AssistantRequest Request { get; private set; } = null!;
        public ClaudeCodeLocator Locator { get; private set; } = null!;
        public FakeProvider Provider { get; private set; } = null!;
        public int ClaudeProbes { get; private set; }
        public int ConsentAsked { get; private set; }
        public bool ConsentAnswer { get; set; } = true;
        public List<AssistantRequest.Outcome> Outcomes { get; } = [];
        public List<string> Texts { get; } = [];
        public List<int> CallbackThreads { get; } = [];
        public TaskCompletionSource Completed { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public static async Task<Harness> CreateAsync(bool consent = true, bool delayOpen = false)
        {
            var h = new Harness();
            await h.Ui.RunAsync(() =>
            {
                h.Provider = h.NewProvider(consent, delayOpen);
                h.Locator = new ClaudeCodeLocator(h.Settings, new Dictionary<string, string>(), usable: _ => { h.ClaudeProbes++; return false; });
                h.Request = new AssistantRequest(h.Settings, h.Locator, h.Directory.Path,
                    new Dictionary<string, string>(), time: h.Time, pending: h.Pending)
                {
                    Provider = h.Provider,
                    Consent = () => { h.ConsentAsked++; return Task.FromResult(h.ConsentAnswer); },
                };
            });
            return h;
        }

        public FakeProvider NewProvider(bool consent = true, bool delayOpen = false)
        {
            var next = new FakeProvider(consent, delayOpen);
            providers.Add(next);
            return next;
        }

        public void Start() => Request.Start("system instructions", "user input", outcome =>
        {
            CallbackThreads.Add(Environment.CurrentManagedThreadId);
            Outcomes.Add(outcome);
            Completed.TrySetResult();
        }, onText: text => { CallbackThreads.Add(Environment.CurrentManagedThreadId); Texts.Add(text); });

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                Request.Close();
                foreach (var item in providers) { item.ReleaseOpen(); item.Session.Terminate(); }
            });
            await IdleAsync();
            Ui.Dispose();
            Directory.Dispose();
        }
    }

    private sealed class FakeProvider(bool consent, bool delayOpen) : IAssistantProvider
    {
        private readonly TaskCompletionSource<IAssistantSession> opened = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public AssistantProviderID Id => AssistantProviderID.ChatGpt;
        public string SelectedModel { get; set; } = "test-model";
        public string Model => SelectedModel;
        public bool HasConsent { get; private set; } = consent;
        public int AcceptedConsent { get; private set; }
        public int OpenCalls { get; private set; }
        public AssistantSessionSpec? Spec { get; private set; }
        public CancellationToken OpenCancellation { get; private set; }
        public TaskCompletionSource OpenEntered { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public FakeSession Session { get; } = new();
        public void AcceptConsent() { AcceptedConsent++; HasConsent = true; }
        public Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken)
        {
            OpenCalls++;
            Spec = spec;
            OpenCancellation = cancellationToken;
            OpenEntered.TrySetResult();
            return delayOpen ? opened.Task : Task.FromResult<IAssistantSession>(Session);
        }
        public void ReleaseOpen() => opened.TrySetResult(Session);
    }

    private sealed class FakeSession : IAssistantSession
    {
        private readonly TaskCompletionSource completed = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public event EventHandler<IReadOnlyList<AssistantEvent>>? EventsReceived;
        public event EventHandler<AssistantSessionExit>? Exited;
        public Task Completion => completed.Task;
        public bool IsRunning => !Terminated;
        public bool Terminated { get; private set; }
        public List<string> Inputs { get; } = [];
        public TaskCompletionSource Submitted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public Task SubmitAsync(string input, CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            Inputs.Add(input);
            Submitted.TrySetResult();
            return Task.CompletedTask;
        }
        public void Emit(params AssistantEvent[] events) => EventsReceived?.Invoke(this, events);
        public void EmitExit(string description) => Exited?.Invoke(this, new AssistantSessionExit(1, description));
        public void Terminate()
        {
            if (Terminated) return;
            Terminated = true;
            Exited?.Invoke(this, new AssistantSessionExit(0, "terminated"));
            completed.TrySetResult();
        }
    }
}
