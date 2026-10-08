// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the panel's Reconnect to ChatGPT tests of
// ui/internal/assistantpanel (ReconnectProvider, reconnectFirst,
// providerFailure) and macos AssistantPanelControllerTests; in-memory
// provider, no Codex and no Claude Code.

using System;
using System.Collections.Generic;
using System.Linq;
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

public sealed class AssistantPanelReconnectTests
{
    [Theory]
    [InlineData("chatgpt_reconnect_required")]
    [InlineData("chatgpt_not_connected")]
    [InlineData("chatgpt_consent_required")]
    [InlineData("chatgpt_permission_denied")]
    [InlineData("chatgpt_identity_mismatch")]
    public async Task NotSignedInFailureOffersReconnect(string code)
    {
        await using var h = await Harness.CreateAsync(openFails: code);
        await h.Ui.RunAsync(() => Assert.True(h.Panel.Submit("Hello")));
        await h.IdleAsync();
        var error = await h.Ui.RunAsync(() => Assert.IsType<ErrorContent>(h.Panel.Items[^1].Content));
        Assert.Equal(ErrorOffer.Reconnect, error.Offer);
        Assert.False(error.Retry);
        Assert.DoesNotContain("chatgpt_", error.Text, StringComparison.Ordinal);
    }

    [Fact]
    public async Task WithoutTheHookThereIsNoOffer()
    {
        await using var h = await Harness.CreateAsync(openFails: "chatgpt_reconnect_required", hook: false);
        await h.Ui.RunAsync(() => Assert.True(h.Panel.Submit("Hello")));
        await h.IdleAsync();
        Assert.Equal(ErrorOffer.None, await h.Ui.RunAsync(() => Assert.IsType<ErrorContent>(h.Panel.Items[^1].Content).Offer));
    }

    [Fact]
    public async Task UsageLimitIsASentenceNotACode()
    {
        await using var h = await Harness.CreateAsync(openFails: "chatgpt_usage_limit");
        await h.Ui.RunAsync(() => Assert.True(h.Panel.Submit("Hello")));
        await h.IdleAsync();
        var error = await h.Ui.RunAsync(() => Assert.IsType<ErrorContent>(h.Panel.Items[^1].Content));
        Assert.DoesNotContain("chatgpt_usage_limit", error.Text, StringComparison.Ordinal);
        Assert.Equal(ErrorOffer.None, error.Offer);
        Assert.True(error.Retry);
    }

    [Fact]
    public async Task ReconnectRunsTheHookOnceAndAsksAgain()
    {
        await using var h = await Harness.CreateAsync(openFails: "chatgpt_reconnect_required");
        await h.Ui.RunAsync(() => Assert.True(h.Panel.Submit("Hello")));
        await h.IdleAsync();
        h.Provider.OpenFails = null;
        await h.Ui.RunAsync(() => h.Panel.Reconnect(h.Panel.Items[^1].Id));
        await h.Session.Submitted.Task.WaitAsync(TimeSpan.FromSeconds(10), TestContext.Current.CancellationToken);
        Assert.Equal(1, h.Hooks);
        Assert.Equal(2, h.Provider.OpenCalls);
        Assert.Single(h.Session.Inputs);
        Assert.Contains("Hello", h.Session.Inputs[0], StringComparison.Ordinal);
    }

    [Fact]
    public async Task FailedReconnectOffersItAgain()
    {
        await using var h = await Harness.CreateAsync(openFails: "chatgpt_reconnect_required", hookResult: false);
        await h.Ui.RunAsync(() => Assert.True(h.Panel.Submit("Hello")));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => h.Panel.Reconnect(h.Panel.Items[^1].Id));
        await h.IdleAsync();
        var error = await h.Ui.RunAsync(() => Assert.IsType<ErrorContent>(h.Panel.Items[^1].Content));
        Assert.Equal("Could not connect to ChatGPT.", error.Text);
        Assert.Equal(ErrorOffer.Reconnect, error.Offer);
        Assert.Equal(1, h.Hooks);
        Assert.Equal(1, h.Provider.OpenCalls);
    }

    private sealed class Harness : IAsyncDisposable
    {
        public TestUIContext Ui { get; } = new();
        public PendingWork Pending { get; } = new();
        public TemporaryDirectory Dir { get; } = new();
        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);
        public AssistantPanelController Panel { get; private set; } = null!;
        public FakeProvider Provider { get; } = new();
        public FakeSession Session { get; } = new();
        public int Hooks { get; private set; }

        public static async Task<Harness> CreateAsync(string? openFails = null, bool hook = true, bool hookResult = true)
        {
            var h = new Harness();
            h.Provider.OpenFails = openFails;
            h.Provider.Session = h.Session;
            await h.Ui.RunAsync(() =>
            {
                var locator = new ClaudeCodeLocator(h.Settings, new Dictionary<string, string>(), usable: _ => false);
                h.Panel = new AssistantPanelController(
                    h.Settings, locator, null, @"C:\Users\test\.cache\malachi\run\rpc.sock", h.Dir.Path,
                    new Dictionary<string, string>(), time: new FakeTimeProvider(), pending: h.Pending,
                    directories: new FakePrivateDirectories())
                {
                    Today = () => "2026-10-08",
                    Language = () => "English",
                    Provider = h.Provider,
                };
                if (hook)
                {
                    h.Panel.ReconnectProvider = () =>
                    {
                        h.Hooks++;
                        return Task.FromResult(hookResult);
                    };
                }
            });
            return h;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() => { Panel.Close(); Session.Terminate(); });
            await IdleAsync();
            Ui.Dispose();
            Dir.Dispose();
        }
    }

    private sealed class FakeProvider : IAssistantProvider
    {
        public AssistantProviderID Id => AssistantProviderID.ChatGpt;
        public string Model => "test-model";
        public bool Available => true;
        public bool Connected => true;
        public bool HasConsent => true;
        public bool HasBoardConsent => true;
        public string? OpenFails { get; set; }
        public int OpenCalls { get; private set; }
        public FakeSession Session { get; set; } = null!;
        public void AcceptConsent() { }
        public void AcceptBoardConsent() { }
        public Task<IAssistantSession> OpenAsync(AssistantSessionSpec spec, CancellationToken cancellationToken)
        {
            OpenCalls++;
            if (OpenFails is { } code) { throw new AssistantProviderException(code); }
            return Task.FromResult<IAssistantSession>(Session);
        }
    }

    private sealed class FakeSession : IAssistantSession
    {
        private readonly TaskCompletionSource completed = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public event EventHandler<IReadOnlyList<AssistantEvent>>? EventsReceived;
        public event EventHandler<AssistantSessionExit>? Exited;
        public Task Completion => completed.Task;
        public bool IsRunning => !completed.Task.IsCompleted;
        public List<string> Inputs { get; } = [];
        public TaskCompletionSource Submitted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public void Emit(params AssistantEvent[] events) => EventsReceived?.Invoke(this, events);
        public Task SubmitAsync(string input, CancellationToken cancellationToken)
        {
            Inputs.Add(input);
            Submitted.TrySetResult();
            return Task.CompletedTask;
        }
        public void Terminate()
        {
            if (completed.TrySetResult()) { Exited?.Invoke(this, new AssistantSessionExit(0, "terminated")); }
        }
    }
}
