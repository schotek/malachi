// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// QuitSequence (Core/Presentation): AppDelegate.swift's
// applicationShouldTerminate (stop the daemon, sweep, terminate) after the
// Windows addition of docs/windows-port.md §0 (the dirty drafts saved, and
// the close question asked only of the windows whose save failed). The
// steps are recorded in order; a question's answer is held open with a
// TaskCompletionSource to see what arrives meanwhile.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class QuitSequenceTests
{
    [Fact]
    public async Task AUsersQuitSavesStopsReleasesAndExitsInOrder()
    {
        var h = new Harness();
        Assert.True(await h.Quit.QuitAsync());
        Assert.Equal(["save", "begin", "stop", "release", "exit"], h.Log);
        Assert.True(h.Quit.IsStopping);
    }

    [Fact]
    public async Task OnlyTheWindowsWhoseSaveFailedAreAsked()
    {
        var h = new Harness();
        var a = new Window("a");
        var b = new Window("b");
        h.Failed = [a, b];
        Assert.True(await h.Quit.QuitAsync());
        Assert.Equal(["save", "ask a", "ask b", "begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task ACancelAbandonsTheQuitAndTheNextQuitStartsAfresh()
    {
        var h = new Harness();
        var a = new Window("a") { Answer = false };
        var b = new Window("b");
        h.Failed = [a, b];
        Assert.False(await h.Quit.QuitAsync());
        Assert.Equal(["save", "ask a"], h.Log);
        Assert.False(h.Quit.IsQuitting);

        a.Answer = true;
        h.Log.Clear();
        Assert.True(await h.Quit.QuitAsync());
        Assert.Equal(["save", "ask a", "ask b", "begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task AQuitWhileAskingIsTheSameQuit()
    {
        var h = new Harness();
        var answer = new TaskCompletionSource<bool>();
        var a = new Window("a") { Held = answer };
        h.Failed = [a];
        var first = h.Quit.QuitAsync();
        var second = h.Quit.QuitAsync();
        Assert.Same(first, second);
        Assert.True(h.Quit.IsQuitting);
        Assert.False(h.Quit.IsStopping);
        answer.SetResult(true);
        Assert.True(await first);
        Assert.Equal(["save", "ask a", "begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task ASessionEndSavesNothingAndAsksNothing()
    {
        var h = new Harness { Failed = [new Window("a")] };
        Assert.True(await h.Quit.QuitAsync(QuitReason.SessionEnd));
        Assert.Equal(["begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task ASessionEndWhileAQuestionIsUpStopsAtOnce()
    {
        var h = new Harness();
        var answer = new TaskCompletionSource<bool>();
        h.Failed = [new Window("a") { Held = answer }, new Window("b")];
        var user = h.Quit.QuitAsync();
        Assert.True(await h.Quit.QuitAsync(QuitReason.SessionEnd));
        Assert.Equal(["save", "ask a", "begin", "stop", "release", "exit"], h.Log);
        // The question's late answer changes nothing, whatever it is.
        answer.SetResult(false);
        Assert.True(await user);
        Assert.Equal(["save", "ask a", "begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task AQuitAfterTheStopIsTheStop()
    {
        var h = new Harness();
        Assert.True(await h.Quit.QuitAsync());
        Assert.True(await h.Quit.QuitAsync());
        Assert.True(await h.Quit.QuitAsync(QuitReason.SessionEnd));
        Assert.Equal(["save", "begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task AFailedSaveAbandonsTheQuit()
    {
        var h = new Harness { SaveThrows = true };
        Assert.False(await h.Quit.QuitAsync());
        Assert.Equal(["save"], h.Log);
        Assert.False(h.Quit.IsQuitting);
    }

    [Fact]
    public async Task AThrowingStepDoesNotKeepTheAppFromExiting()
    {
        var h = new Harness { StepsThrow = true };
        Assert.True(await h.Quit.QuitAsync());
        Assert.Equal(["save", "begin", "stop", "release", "exit"], h.Log);
    }

    [Fact]
    public async Task TheDefaultQuestionIsTheWindowsOwn()
    {
        var asked = new List<string>();
        var quit = new QuitSequence(new QuitSteps
        {
            SaveDrafts = () => Task.FromResult<IReadOnlyList<IComposeWindowHandle>>([new Window("w", asked)]),
        });
        Assert.True(await quit.QuitAsync());
        Assert.Equal(["close w"], asked);
    }

    [Fact]
    public async Task NoStepsStillQuit() => Assert.True(await new QuitSequence(new QuitSteps()).QuitAsync());

    private sealed class Harness
    {
        public Harness()
        {
            Quit = new QuitSequence(new QuitSteps
            {
                SaveDrafts = () =>
                {
                    Log.Add("save");
                    return SaveThrows
                        ? Task.FromException<IReadOnlyList<IComposeWindowHandle>>(new InvalidOperationException("save"))
                        : Task.FromResult<IReadOnlyList<IComposeWindowHandle>>(Failed);
                },
                AskClose = w =>
                {
                    var window = (Window)w;
                    Log.Add("ask " + window.Name);
                    return window.Held?.Task ?? Task.FromResult(window.Answer);
                },
                BeginStopping = () => Step("begin"),
                StopDaemon = () =>
                {
                    Step("stop");
                    return Task.CompletedTask;
                },
                Release = () => Step("release"),
                Exit = () => Step("exit"),
            });
        }

        public QuitSequence Quit { get; }

        public List<string> Log { get; } = [];

        public IReadOnlyList<IComposeWindowHandle> Failed { get; set; } = [];

        public bool SaveThrows { get; init; }

        public bool StepsThrow { get; init; }

        private void Step(string name)
        {
            Log.Add(name);
            if (StepsThrow)
            {
                throw new InvalidOperationException(name);
            }
        }
    }

    private sealed class Window(string name, List<string>? closed = null) : IComposeWindowHandle
    {
        public string Name { get; } = name;

        public bool Answer { get; set; } = true;

        public TaskCompletionSource<bool>? Held { get; init; }

        public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder)
        {
        }

        public void Toast(string text)
        {
        }

        public Task<bool> CloseForQuitAsync()
        {
            closed?.Add("close " + Name);
            return Task.FromResult(true);
        }
    }
}
