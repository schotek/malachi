// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantPanelControllerTests.swift
// (the assistant panel's state machine and its PanelHarness) and of
// ui/internal/assistantpanel/controller_test.go with harness_test.go (the
// same cases), against the stand-in claude.exe of Malachi.FakeClaude. No
// real Claude Code is ever run here.
//
// Windows differences: the kill grace and the resolve timeout run on a fake
// clock. A turn that Swift keeps open with "sleep 5" hangs here
// (FakeClaudeStep.Hang) and is killed by advancing the clock by the grace
// after Stop (Swift's SIGTERM ends the shell at once); members that the
// hook never gives are timed out by advancing it. The tests wait for a
// report (UiConditions) or until the panel's work and processes are done
// (IdleAsync, which counts a process until its end was reported), never
// for time. The private directory is shown made by the injected factory
// where Swift checks mode 0700, and the child's PATH starts with the
// directory of claude.exe where Swift's is "<dir>:/usr/bin:…". Swift
// rewrites the stand-in's script to sign it in; here auth status prints a
// file the test rewrites. Swift's sleep before the check that a declined
// consent started nothing is IdleAsync. Added: LocalDate, the count of a
// Context, and a closed panel.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
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
using ChangeKind = Malachi.Core.Controllers.AssistantPanelController.ChangeKind;
using Context = Malachi.Core.Controllers.AssistantPanelController.Context;
using Phase = Malachi.Core.Controllers.AssistantPanelController.Phase;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class AssistantPanelControllerTests
{
    // The bridge and the socket the tests name; nothing ever runs the bridge.
    private const string TestBridge = @"C:\Program Files\Malachi Mail\malachi-mcp.exe";
    private const string TestSocket = @"C:\Users\test\.cache\malachi\run\rpc.sock";

    // What the stand-in's auth status prints (the sign-in tests).
    private const string AuthFile = "auth-status";

    private const string SignedOut = "Claude Code is not signed in";
    private const string Waiting = "Waiting for the sign-in in your browser…";

    private static readonly TimeSpan Grace = TimeSpan.FromMilliseconds(300);

    private static readonly Context One = new(new AssistantSelection("a", ["m1"]));

    // The chip once a conversation keeps its context: the contexts in
    // pinned order, and the chip.
    private static readonly Dictionary<string, (Context?[] Contexts, string Want)> ChipCases = new()
    {
        ["one message"] = ([Message("m1", "Invoice 42")], "Conversation about: Invoice 42"),
        ["no subject"] = ([Message("m1")], "Selected message"),
        ["a blank subject"] = ([Message("m1", " \n\t")], "Selected message"),
        ["a subject on two lines"] = ([Message("m1", "Invoice\r\n42")], "Conversation about: Invoice 42"),
        ["all mail"] = ([null], "All mail"),
        ["a conversation"] = ([Folded(["m3", "m2", "m1"], 3, subject: "Trip")], "Conversation about: Trip"),
        ["a conversation without a subject"] = ([Folded(["m3", "m2", "m1"], 3)], "Conversation about 3 messages"),
        ["a folded conversation"] = ([Folded(["m3"], 3, partial: true, subject: "Trip")], "Conversation about: Trip"),
        ["two messages"] = ([Message("m1", "Invoice 42"), Message("m2", "Lunch")], "Conversation about 2 messages"),
        ["a conversation and a message"] = ([Folded(["m3", "m2"], 2), Message("m4")], "Conversation about 3 messages"),
        ["members not known yet"] = ([Folded(["m3"], 3, partial: true), Message("m9")], "Conversation about 4 messages"),
        ["a message counted once"] = ([Folded(["m2", "m1"], 2), Message("m1", "Invoice 42")], "Conversation about 2 messages"),
        ["all mail and a message"] = ([null, Message("m1", "Invoice 42")], "Conversation about: Invoice 42"),
        ["all mail and two messages"] = ([null, Message("m1", "Invoice 42"), Message("m2")], "Conversation about 2 messages"),
        ["the same id in two accounts"] = ([Message("m1", "Invoice 42"), new Context(new AssistantSelection("b", ["m1"]))], "Conversation about 2 messages"),
    };

    public static TheoryData<string> ChipNames => [.. ChipCases.Keys];

    private static void RequireWindows() => Assert.SkipUnless(OperatingSystem.IsWindows(), "the stand-in claude is a Windows program");

    private static FakeClaudeScript Fake(params IReadOnlyList<FakeClaudeStep>[] turns) => new(turns);

    // One message of account "a" as the panel's context.
    private static Context Message(string id, string subject = "", string thread = "") =>
        new(new AssistantSelection("a", [id]), Subject: subject, ThreadId: thread);

    // A conversation of account "a" as the panel's context: ids newest
    // first, only the newest when partial.
    private static Context Folded(string[] ids, int count, bool partial = false, string subject = "", string thread = "") =>
        new(new AssistantSelection("a", ids), count, partial, subject, thread);

    // A message action's prompt for the panel.
    private static string Prompt(AssistantAction a, AssistantSelection s) => Assistant.Prompt(AssistantTarget.App, a, s);

    // The pinned contexts, on the UI thread.
    private static List<Context?> Contexts(Harness h) => [.. h.Panel.PinnedContexts.Select(p => p.Context)];

    // Whether the model was told of each pinned context, on the UI thread.
    private static List<bool> Announced(Harness h) => [.. h.Panel.PinnedContexts.Select(p => p.Announced)];

    // A variable of the child's environment, its name compared without case.
    private static string Variable(IReadOnlyDictionary<string, string> env, string name) =>
        env.First(kv => string.Equals(kv.Key, name, StringComparison.OrdinalIgnoreCase)).Value;

    /// <summary>
    /// Summarize on the selected message: consent asked once and kept, the
    /// question, the tool line, the streamed answer replaced by the whole
    /// text; one process with the command line of Assistant.Args, the
    /// child's environment and the private directory.
    /// </summary>
    [Fact]
    public async Task SummarizeRunsATurn()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(
            Fake(CannedStreamJson.Turn(
                CannedStreamJson.Init,
                CannedStreamJson.ToolUse("t1", "read_message"),
                CannedStreamJson.ToolResult("t1", "From: someone"),
                CannedStreamJson.Delta("Hello **wor"),
                CannedStreamJson.Delta("ld**"),
                CannedStreamJson.Text("Hello **world**"),
                CannedStreamJson.Result())),
            consent: false);
        await h.On(() =>
        {
            h.Panel.SetContext(One);
            Assert.True(h.Panel.CanRunActions);
            h.Panel.Run(AssistantAction.Summarize);
            Assert.Equal(Phase.Preparing, h.Panel.CurrentPhase);
            Assert.False(h.Panel.CanRunActions);
        });
        await h.TurnAsync();
        Assert.Equal(1, h.ConsentAsked);
        Assert.True(h.Settings.AssistantConsent);
        Assert.Equal(
            [new UserContent("Summarize", ""), new ActivityContent("Reading a message…", true), new AnswerContent("Hello **world**", false)],
            await h.ContentsAsync());
        Assert.Equal(1, h.Starts);
        Assert.Equal([Prompt(AssistantAction.Summarize, One.Selection)], h.Prompts);
        Assert.Equal(
            Assistant.Args(new AssistantOptions
            {
                Bridge = TestBridge,
                Socket = TestSocket,
                Model = AssistantModel.Sonnet,
                SystemPrompt = Assistant.SystemPrompt("Czech", "2026-09-29"),
            }),
            FakeClaudeScript.Args(h.Dir.Path));
        var env = FakeClaudeScript.Env(h.Dir.Path);
        Assert.Equal("cs_CZ.UTF-8", Variable(env, "LANG"));
        Assert.DoesNotContain(env.Keys, k => k.StartsWith("ANTHROPIC", StringComparison.OrdinalIgnoreCase));
        Assert.StartsWith(h.Dir.Path + ";", Variable(env, "PATH"), StringComparison.OrdinalIgnoreCase);
        Assert.Equal(h.Work, FakeClaudeScript.Cwd(h.Dir.Path), ignoreCase: true);
        // Swift checks mode 0700: the directory was made private by the factory.
        Assert.Equal([h.Work], h.Directories.Ensured);

        // A follow-up goes to the same process as it is: Summarize's prompt
        // named the message already.
        Assert.True(await h.On(() => h.Panel.Submit("  And what is still open?  ")));
        await h.TurnAsync();
        Assert.Equal(1, h.Starts);
        var prompts = h.Prompts;
        Assert.Equal(2, prompts.Count);
        Assert.Equal("And what is still open?", prompts[1]);
        Assert.Equal(1, h.ConsentAsked);
        Assert.Equal(
            [new UserContent("", "And what is still open?"), new ActivityContent("Reading a message…", true), new AnswerContent("Hello **world**", false)],
            (await h.ContentsAsync()).TakeLast(3));
    }

    /// <summary>Without a context a question goes as it is; empty text is refused.</summary>
    [Fact]
    public async Task FreeQuestionWithoutContext()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("Two unread.")));
        await h.On(() =>
        {
            Assert.False(h.Panel.Submit("   "));
            Assert.Equal("All mail", h.Panel.ContextLabel);
            Assert.False(h.Panel.CanRunActions);
            h.Panel.Run(AssistantAction.Summarize); // needs a context
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
            Assert.True(h.Panel.Submit("How many unread?"));
            Assert.False(h.Panel.Submit("again")); // one at a time
        });
        await h.TurnAsync();
        Assert.Equal(["How many unread?"], h.Prompts);
        Assert.Equal(0, h.ConsentAsked);
        Assert.Equal(new AnswerContent("Two unread.", false), await h.LastAsync());
    }

    /// <summary>Consent declined: nothing is sent or started, the text goes back.</summary>
    [Fact]
    public async Task ConsentDeclined()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("x")), consent: false);
        h.ConsentAnswer = false;
        Assert.True(await h.On(() => h.Panel.Submit("Summarize my week")));
        await h.WhenAsync(() => h.ConsentAsked == 1 && h.Panel.CurrentPhase == Phase.Idle, "the declined consent");
        await h.On(() =>
        {
            Assert.Equal(["Summarize my week"], h.Restored);
            Assert.Empty(h.Panel.Items);
        });
        Assert.False(h.Settings.AssistantConsent);
        await h.IdleAsync();
        Assert.Equal(0, h.Starts);
    }

    /// <summary>Draft a Reply… waits for the words; the draft card opens through the application.</summary>
    [Fact]
    public async Task DraftReplyAndOpenDraft()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init,
            CannedStreamJson.ToolUse("t1", "read_message"),
            CannedStreamJson.ToolResult("t1", "…"),
            CannedStreamJson.ToolUse("t2", "create_draft"),
            CannedStreamJson.ToolResult("t2", @"draft d_9 (version 1) stored in account a; it is NOT sent.\n\nTo: x"),
            CannedStreamJson.Text("The draft is ready."),
            CannedStreamJson.Result())));
        await h.On(() =>
        {
            h.Panel.SetContext(One);
            Assert.Equal("Ask about your mail…", h.Panel.Placeholder);
            h.Panel.Run(AssistantAction.DraftReply);
            Assert.Equal(new PendingAction(AssistantAction.DraftReply), h.Panel.Pending);
            Assert.Equal("What should the reply say?", h.Panel.Placeholder);
            Assert.Equal("Draft a Reply…", h.Panel.PendingLabel);
            Assert.Equal(1, h.Focused);
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
            Assert.True(h.Panel.Submit("Yes, Thursday works."));
        });
        await h.TurnAsync();
        Assert.Null(await h.On(() => h.Panel.Pending));
        Assert.Equal([Prompt(AssistantAction.DraftReply, One.Selection) + "Yes, Thursday works."], h.Prompts);
        var draft = new DraftRef("a", "d_9", 1);
        Assert.Equal(
            [
                new UserContent("Draft a Reply…", "Yes, Thursday works."),
                new ActivityContent("Reading a message…", true),
                new ActivityContent("Saving a draft…", true),
                new DraftContent(draft),
                new AnswerContent("The draft is ready.", false),
            ],
            await h.ContentsAsync());
        await h.On(() =>
        {
            var card = Assert.Single(h.Panel.Items, i => i.Content == new DraftContent(draft));
            h.Panel.OpenDraftItem(card.Id);
        });
        Assert.Equal([draft], h.Opened);
    }

    /// <summary>A failed create_draft, or one whose line is not the bridge's, adds no card.</summary>
    [Fact]
    public async Task NoCardWithoutTheBridgeLine()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init,
            CannedStreamJson.ToolUse("t1", "create_draft"),
            CannedStreamJson.ToolResult("t1", "draft d1 (version 1) stored in account a; it is NOT sent.", error: true),
            CannedStreamJson.ToolUse("t2", "read_message"),
            CannedStreamJson.ToolResult("t2", "draft d2 (version 1) stored in account a; it is NOT sent."),
            CannedStreamJson.ToolUse("t3", "create_draft"),
            CannedStreamJson.ToolResult("t3", "I saved draft d3 for you"),
            CannedStreamJson.Result())));
        Assert.True(await h.On(() => h.Panel.Submit("Draft something")));
        await h.TurnAsync();
        Assert.DoesNotContain(await h.ContentsAsync(), c => c is DraftContent);
    }

    /// <summary>Ask About This Message…, an attachment and Summarize Unread; the removed context takes a waiting message action with it.</summary>
    [Fact]
    public async Task PendingActionsAndPrompts()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        await h.On(() =>
        {
            h.Panel.SetContext(One);
            h.Panel.Run(AssistantAction.Ask);
            Assert.Equal("What do you want to know?", h.Panel.Placeholder);
            Assert.Equal("Ask About This Message…", h.Panel.PendingLabel);
            Assert.False(h.Panel.Submit("  "), "a question needs words");
            h.Panel.RemoveContext();
            Assert.Null(h.Panel.Pending);
            Assert.Equal("All mail", h.Panel.ContextLabel);

            h.Panel.AskAttachment("a", "m1", "2");
            Assert.Equal("What do you want to know?", h.Panel.Placeholder);
            Assert.Equal("Ask the Assistant…", h.Panel.PendingLabel);
            h.Panel.CancelPending();
            Assert.Null(h.Panel.Pending);
            h.Panel.AskAttachment("a", "m1", "2");
            Assert.True(h.Panel.Submit("What is the total?"));
        });
        await h.TurnAsync();
        await h.On(() => h.Panel.SummarizeUnread("a", "in"));
        await h.TurnAsync();
        Assert.Equal(
            [Assistant.AttachmentPrompt("a", "m1", "2") + "What is the total?", Assistant.UnreadPrompt("a", "in")],
            h.Prompts);
        Assert.Equal(
            [new UserContent("Ask the Assistant…", "What is the total?"), new UserContent("Summarize Unread in This Folder", "")],
            (await h.ContentsAsync()).OfType<UserContent>());
    }

    /// <summary>The context follows the selection; a removed context comes back with the next selection; the chip's text.</summary>
    [Fact]
    public async Task ContextFollowsTheSelection()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        await h.On(() =>
        {
            var states = 0;
            h.Panel.StateChanged += (_, _) => states++;
            Assert.Equal("All mail", h.Panel.ContextLabel);
            h.Panel.SetContext(One);
            Assert.Equal("Selected message", h.Panel.ContextLabel);
            var conv = Folded(["m3"], 3, partial: true);
            h.Panel.SetContext(conv);
            Assert.Equal("Selected conversation (3 messages)", h.Panel.ContextLabel);
            h.Panel.RemoveContext();
            Assert.Null(h.Panel.EffectiveContext);
            Assert.Equal("All mail", h.Panel.ContextLabel);
            h.Panel.SetContext(conv);
            Assert.Equal(conv, h.Panel.EffectiveContext);
            h.Panel.SetContext(null);
            Assert.Equal("All mail", h.Panel.ContextLabel);
            Assert.Equal(5, states);
        });
    }

    /// <summary>A folded conversation's members are asked for when the question is sent, once; without an answer the newest message alone goes.</summary>
    [Fact]
    public async Task PartialContextIsResolved()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var conv = Folded(["m3"], 3, partial: true);
        var members = new AssistantSelection("a", ["m3", "m2", "m1"]);
        var asked = new List<Context>();
        await h.On(() =>
        {
            h.Panel.ResolveContext = (c, done) =>
            {
                asked.Add(c);
                h.Changed();
                done(members);
            };
            h.Panel.SetContext(conv);
            h.Panel.Run(AssistantAction.Tasks);
        });
        await h.TurnAsync();
        Assert.Equal([conv], asked);
        Assert.Equal([Prompt(AssistantAction.Tasks, members)], h.Prompts);
        // The pinned conversation keeps its members.
        await h.On(() =>
        {
            var pinned = h.Panel.PinnedContexts[0].Context;
            Assert.Equal(members, pinned?.Selection);
            Assert.False(pinned?.Partial);
            h.Panel.Run(AssistantAction.Summarize);
        });
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.Summarize, members), h.LastPrompt);
        Assert.Equal([conv], asked);

        await h.On(() =>
        {
            h.Panel.NewConversation();
            h.Panel.ResolveTimeout = TimeSpan.FromMilliseconds(100);
            h.Panel.ResolveContext = (c, _) =>
            {
                asked.Add(c);
                h.Changed();
            }; // never answers
            h.Panel.Run(AssistantAction.Summarize);
        });
        await h.WhenAsync(() => asked.Count == 2, "the question for the members");
        h.Time.Advance(TimeSpan.FromMilliseconds(100));
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.Summarize, conv.Selection), h.LastPrompt);
    }

    /// <summary>No Claude Code: an error with Get Claude Code… and Try Again, nothing started.</summary>
    [Fact]
    public async Task ClaudeNotFound()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        File.Delete(h.Claude);
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal(
            [new UserContent("", "Hello"), new ErrorContent("Claude Code was not found on this computer", true, ErrorOffer.Install)],
            await h.ContentsAsync());
        Assert.Equal(0, h.Starts);
    }

    // A stand-in that is signed out until a sign-in writes the file its auth
    // status prints (Go's signsIn).
    private static Func<string, FakeClaudeScript> SignedOutClaude(params IReadOnlyList<FakeClaudeStep>[] turns) => dir =>
    {
        var status = Path.Combine(dir, AuthFile);
        File.WriteAllText(status, "{\"loggedIn\": false}\n");
        return new FakeClaudeScript(turns, auth: [FakeClaudeStep.PrintFile(status)]);
    };

    // The stand-in's auth login signs in from now on.
    private static void SignsIn(Harness h) => FakeClaudeScript.SetLogin(
        h.Dir.Path, FakeClaudeStep.WriteFile(Path.Combine(h.Dir.Path, AuthFile), "{\"loggedIn\": true}\n"));

    /// <summary>
    /// controller_test.go TestNotSignedInSignsIn. Signed out: an error with
    /// Sign In…, which runs Claude Code's sign-in and then sends the same
    /// question, without a second bubble.
    /// </summary>
    [Fact]
    public async Task NotSignedInSignsIn()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(SignedOutClaude(CannedStreamJson.AnswerTurn("Hi there")));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal([new UserContent("", "Hello"), new ErrorContent(SignedOut, false, ErrorOffer.SignIn)], await h.ContentsAsync());
        Assert.Equal(0, h.Starts);
        Assert.Equal(0, h.Logins);

        // Try Again is not what the line offers.
        await h.On(() =>
        {
            h.Panel.Retry(h.Panel.Items[1].Id);
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
        });
        SignsIn(h);
        await h.On(() => h.Panel.SignIn(h.Panel.Items[1].Id));
        await h.TurnAsync();
        Assert.Equal(
            [
                new UserContent("", "Hello"),
                new ErrorContent(SignedOut, false),
                new ActivityContent(Waiting, true),
                new AnswerContent("Hi there", false),
            ],
            await h.ContentsAsync());
        Assert.Equal(1, h.Logins);
        Assert.Equal(1, h.Starts);
        Assert.Equal(["Hello"], h.Prompts);
        // The sign-in ran in the private directory with the child's
        // environment: nothing of a surrounding Claude or of Malachi Mail.
        var env = FakeClaudeScript.LoginEnv(h.Dir.Path);
        Assert.Equal(h.Dir.Path, Variable(env, "USERPROFILE"));
        Assert.DoesNotContain(env.Keys, k => k.StartsWith("ANTHROPIC", StringComparison.OrdinalIgnoreCase));
        Assert.DoesNotContain(env.Keys, k => k.StartsWith("MALACHI", StringComparison.OrdinalIgnoreCase));
        // A button used up does nothing more.
        await h.On(() =>
        {
            h.Panel.SignIn(h.Panel.Items[1].Id);
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
        });
        Assert.Equal(1, h.Logins);
    }

    /// <summary>controller_test.go TestSignInFails. A sign-in that ends badly: its reason, and Sign In… again.</summary>
    [Fact]
    public async Task SignInFails()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(SignedOutClaude(CannedStreamJson.AnswerTurn("Hi there")));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        FakeClaudeScript.SetLogin(
            h.Dir.Path, FakeClaudeStep.Stderr("Login failed: the browser said no\nmore\n"), FakeClaudeStep.Exit(1));
        await h.On(() => h.Panel.SignIn(h.Panel.Items[1].Id));
        await h.TurnAsync();
        Assert.Equal(
            [
                new UserContent("", "Hello"),
                new ErrorContent(SignedOut, false),
                new ActivityContent(Waiting, true),
                new ErrorContent("The sign-in failed: Login failed: the browser said no", false, ErrorOffer.SignIn),
            ],
            await h.ContentsAsync());
        Assert.Equal(0, h.Starts);

        // Once more, and it works.
        SignsIn(h);
        await h.On(() => h.Panel.SignIn(h.Panel.Items[3].Id));
        await h.TurnAsync();
        Assert.Equal(new AnswerContent("Hi there", false), await h.LastAsync());
        Assert.Equal(2, h.Logins);
    }

    /// <summary>controller_test.go TestSignInTimesOut. The browser brings no answer in time: the sign-in is ended.</summary>
    [Fact]
    public async Task SignInTimesOut()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(
            SignedOutClaude(CannedStreamJson.AnswerTurn("Hi there")), signInTimeout: TimeSpan.FromMilliseconds(500));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        FakeClaudeScript.SetLogin(h.Dir.Path, FakeClaudeStep.Hang());
        await h.On(() => h.Panel.SignIn(h.Panel.Items[1].Id));
        await h.TurnAsync();
        Assert.Equal(new ErrorContent("The sign-in took too long; try again", false, ErrorOffer.SignIn), await h.LastAsync());
        Assert.False(await h.On(() => h.Locator.SigningIn));
    }

    /// <summary>
    /// controller_test.go TestSignInStoppedAndReplaced. Stop ends a sign-in
    /// like any turn; one the settings start takes its place.
    /// </summary>
    [Fact]
    public async Task SignInStoppedAndReplaced()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(SignedOutClaude(CannedStreamJson.AnswerTurn("Hi there")));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        FakeClaudeScript.SetLogin(h.Dir.Path, FakeClaudeStep.Hang());
        await h.On(() => h.Panel.SignIn(h.Panel.Items[1].Id));
        await h.WhenAsync(() => h.Locator.SigningIn, "the sign-in");
        Assert.Equal(
            [new UserContent("", "Hello"), new ErrorContent(SignedOut, false), new ActivityContent(Waiting, false)],
            await h.ContentsAsync());
        await h.On(() =>
        {
            h.Panel.Stop();
            Assert.False(h.Locator.SigningIn);
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
        });
        await h.IdleAsync();
        Assert.Equal(
            [
                new UserContent("", "Hello"),
                new ErrorContent(SignedOut, false),
                new ActivityContent(Waiting, true),
                new NoteContent("The conversation was stopped"),
            ],
            await h.ContentsAsync());

        // The same question again: signed out, Sign In…, and the settings'
        // sign-in takes over while the browser is open.
        await h.On(h.Panel.NewConversation);
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        await h.On(() => h.Panel.SignIn(h.Panel.Items[1].Id));
        await h.WhenAsync(() => h.Locator.SigningIn, "the sign-in");
        SignsIn(h);
        var settings = await h.On(() => h.Locator.SignInAsync());
        await h.TurnAsync();
        Assert.Equal(new ClaudeCodeSignIn.Done(), await settings);
        Assert.Equal(
            [
                new UserContent("", "Hello"),
                new ErrorContent(SignedOut, false),
                new ActivityContent(Waiting, true),
                new ErrorContent(SignedOut, false, ErrorOffer.SignIn),
            ],
            await h.ContentsAsync());
    }

    /// <summary>
    /// controller_test.go TestRefusedSignInOffersSignIn. The API refuses the
    /// sign-in although auth status says loggedIn (expired, revoked): Claude
    /// Code's own message is no answer, the line offers Sign In…, and the
    /// question goes to a new process after it.
    /// </summary>
    [Fact]
    public async Task RefusedSignInOffersSignIn()
    {
        RequireWindows();
        const string refused = "Failed to authenticate. API Error: 401";
        await using var h = await Harness.CreateAsync(Fake(
            CannedStreamJson.Turn(
                CannedStreamJson.Init, CannedStreamJson.Failure("authentication_failed", refused), CannedStreamJson.Result(refused, success: false)),
            CannedStreamJson.AnswerTurn("Hi there")));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal([new UserContent("", "Hello"), new ErrorContent(SignedOut, false, ErrorOffer.SignIn)], await h.ContentsAsync());
        Assert.Null(await h.On(() => h.Panel.Process));
        await h.On(() => h.Panel.SignIn(h.Panel.Items[1].Id));
        await h.TurnAsync();
        Assert.Equal(
            [
                new UserContent("", "Hello"),
                new ErrorContent(SignedOut, false),
                new ActivityContent(Waiting, true),
                new AnswerContent("Hi there", false),
            ],
            await h.ContentsAsync());
        Assert.Equal(1, h.Logins);
        Assert.Equal(2, h.Starts);
        Assert.Equal(["Hello", "Hello"], h.Prompts);
    }

    /// <summary>controller_test.go TestRefusedTurnIsSaidOnce. Another refusal of the API is said once, by the result.</summary>
    [Fact]
    public async Task RefusedTurnIsSaidOnce()
    {
        RequireWindows();
        const string limit = "API Error: Rate limit reached";
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.Turn(
            CannedStreamJson.Init, CannedStreamJson.Failure("rate_limit", limit), CannedStreamJson.Result(limit, success: false))));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal(
            [new UserContent("", "Hello"), new ErrorContent("The assistant stopped: " + limit, true)],
            await h.ContentsAsync());
    }

    /// <summary>No bridge beside the application: the tools are missing.</summary>
    [Fact]
    public async Task NoBridge()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")), bridge: null);
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal(new ErrorContent("The Malachi Mail tools are not available to the assistant", false), await h.LastAsync());
        Assert.Equal(0, h.Starts);
    }

    /// <summary>The bridge not connected in Claude Code: the conversation ends, the next question starts a new process.</summary>
    [Fact]
    public async Task BridgeNotConnected()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            CannedStreamJson.Turn(CannedStreamJson.InitFailed, CannedStreamJson.Text("I cannot"), CannedStreamJson.Result()),
            CannedStreamJson.AnswerTurn("fine")));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal(
            [new UserContent("", "Hello"), new ErrorContent("The Malachi Mail tools are not available to the assistant", false)],
            await h.ContentsAsync());
        Assert.Null(await h.On(() => h.Panel.Process));
        Assert.Equal(1, h.Starts);
        Assert.True(await h.On(() => h.Panel.Submit("Again")));
        await h.TurnAsync();
        Assert.Equal(2, h.Starts);
    }

    /// <summary>Stop during a turn: the note, the streamed text kept and closed, the process gone; the next question starts a new one.</summary>
    [Fact]
    public async Task StopEndsTheTurn()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            [
                FakeClaudeStep.Lines(CannedStreamJson.Init, CannedStreamJson.ToolUse("t1", "search_messages"), CannedStreamJson.Delta("Looking")),
                FakeClaudeStep.Hang(),
            ],
            CannedStreamJson.AnswerTurn("Fresh start")));
        Assert.True(await h.On(() => h.Panel.Submit("Find the invoice")));
        await h.WhenAsync(() => h.Last() == new AnswerContent("Looking", true), "the streamed text");
        await h.On(() =>
        {
            Assert.Equal(Phase.Running, h.Panel.CurrentPhase);
            h.Panel.Stop();
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
            Assert.Null(h.Panel.Process);
        });
        // The stand-in outlives its input: the kill after the grace ends it.
        h.Time.Advance(Grace);
        Assert.Equal(
            [
                new UserContent("", "Find the invoice"),
                new ActivityContent("Searching mail…", true),
                new AnswerContent("Looking", false),
                new NoteContent("The conversation was stopped"),
            ],
            await h.ContentsAsync());
        Assert.True(await h.On(() => h.Panel.Submit("Once more")));
        await h.TurnAsync();
        Assert.Equal(2, h.Starts);
        Assert.Equal(new AnswerContent("Fresh start", false), await h.LastAsync());
    }

    /// <summary>New Conversation: the transcript empties, the process ends, the next question starts a new one.</summary>
    [Fact]
    public async Task NewConversation()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("One"), CannedStreamJson.AnswerTurn("Two")));
        Assert.True(await h.On(() => h.Panel.Submit("First")));
        await h.TurnAsync();
        var p = await h.On(() => h.Panel.Process);
        Assert.NotNull(p);
        await h.On(() =>
        {
            Assert.True(h.Panel.IsPinned);
            p.Exited += (_, _) => h.Changed();
            h.Panel.NewConversation();
            Assert.Empty(h.Panel.Items);
            Assert.False(h.Panel.IsPinned);
            Assert.Equal(ChangeKind.Reset, h.Changes[^1].Kind);
            Assert.Null(h.Panel.Process);
        });
        await h.WhenAsync(() => !p.Running, "the end of the first process");
        Assert.True(await h.On(() => h.Panel.Submit("Second")));
        await h.TurnAsync();
        Assert.Equal(2, h.Starts);
        Assert.Equal([new UserContent("", "Second"), new AnswerContent("Two", false)], await h.ContentsAsync());
    }

    /// <summary>The process ending during a turn: an error with its stderr, Try Again starts a new process.</summary>
    [Fact]
    public async Task ExitDuringATurn()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(new FakeClaudeScript(
            [CannedStreamJson.AnswerTurn("Recovered")],
            onStart: [FakeClaudeStep.IfStart(1, FakeClaudeStep.ReadLine(), FakeClaudeStep.Stderr("Error: boom\n"), FakeClaudeStep.Exit(1))]));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal(
            [new UserContent("", "Hello"), new ErrorContent("The assistant stopped: Error: boom", true)],
            await h.ContentsAsync());
        Assert.Null(await h.On(() => h.Panel.Process));
        await h.On(() => h.Panel.Retry(h.Panel.Items[1].Id));
        await h.TurnAsync();
        Assert.Equal(2, h.Starts);
        Assert.Equal(new AnswerContent("Recovered", false), await h.LastAsync());
    }

    /// <summary>A result that is not a success is said; the process stays for the next question.</summary>
    [Fact]
    public async Task FailedResult()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.Result("API Error: 529 Overloaded", success: false)),
            CannedStreamJson.AnswerTurn("Better")));
        Assert.True(await h.On(() => h.Panel.Submit("Hello")));
        await h.TurnAsync();
        Assert.Equal(new ErrorContent("The assistant stopped: API Error: 529 Overloaded", true), await h.LastAsync());
        Assert.True(await h.On(() => h.Panel.Process?.Running == true));
        Assert.True(await h.On(() => h.Panel.Submit("Hello again")));
        await h.TurnAsync();
        Assert.Equal(1, h.Starts);
        // The older error no longer offers Try Again.
        Assert.Equal(new ErrorContent("The assistant stopped: API Error: 529 Overloaded", false), (await h.ContentsAsync())[1]);
    }

    /// <summary>The model setting reaches the command line of the next conversation.</summary>
    [Fact]
    public async Task ModelSetting()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        await h.On(() =>
        {
            h.Settings.AssistantModel = AssistantModel.Opus;
            Assert.Equal("Claude Code · Opus", h.Panel.Subtitle);
            Assert.True(h.Panel.Submit("Hello"));
        });
        await h.TurnAsync();
        var args = FakeClaudeScript.Args(h.Dir.Path).ToList();
        var model = args.IndexOf("--model");
        Assert.True(model >= 0);
        Assert.Equal("opus", args[model + 1]);
    }

    // A conversation keeps its context

    /// <summary>
    /// The conversation's first question pins the chip's context, whatever
    /// kind of question it is: a quick action, a waiting action's words, a
    /// free question, a menu's action, Summarize Unread. Later selections
    /// change neither the chip nor what the conversation is about.
    /// </summary>
    [Fact]
    public async Task FirstQuestionPinsTheContext()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var m1 = Message("m1", "Invoice 42", "t1");
        var m2 = Message("m2", "Lunch", "t2");
        var sends = new (string Name, Action Send, string Prompt)[]
        {
            ("quick action", () => h.Panel.Run(AssistantAction.Summarize), Prompt(AssistantAction.Summarize, m1.Selection)),
            ("waiting action", () =>
            {
                h.Panel.Run(AssistantAction.DraftReply);
                _ = h.Panel.Submit("Yes.");
            }, Prompt(AssistantAction.DraftReply, m1.Selection) + "Yes."),
            ("free question", () => _ = h.Panel.Submit("What now?"), "Context: the user has selected message m1 in account a.\n\nWhat now?"),
            ("menu action", () => h.Panel.RunOn(AssistantAction.Tasks, m1), Prompt(AssistantAction.Tasks, m1.Selection)),
            ("unread", () => h.Panel.SummarizeUnread("a", "in"), Assistant.UnreadPrompt("a", "in")),
        };
        foreach (var (name, send, prompt) in sends)
        {
            await h.On(() =>
            {
                h.Panel.NewConversation();
                // The menu's action takes its own message, whatever the chip shows.
                h.Panel.SetContext(name == "menu action" ? m2 : m1);
                Assert.False(h.Panel.IsPinned, name);
                Assert.Equal("Selected message", h.Panel.ContextLabel);
                send();
            });
            await h.TurnAsync();
            Assert.Equal(prompt, h.LastPrompt);
            await h.On(() =>
            {
                Assert.Equal([m1], Contexts(h));
                Assert.Equal("Conversation about: Invoice 42", h.Panel.ContextLabel);
                Assert.False(h.Panel.AnotherSelected, name);
                h.Panel.SetContext(m2);
                Assert.Equal("Conversation about: Invoice 42", h.Panel.ContextLabel);
                Assert.True(h.Panel.AnotherSelected, name);
                Assert.Equal([m1], Contexts(h));
                h.Panel.RemoveContext(); // no remove button while pinned
                Assert.False(h.Panel.ContextRemoved, name);
            });
        }
    }

    /// <summary>The chip once a conversation keeps its context.</summary>
    [Theory]
    [MemberData(nameof(ChipNames))]
    public void PinnedChipLabels(string name)
    {
        var (contexts, want) = ChipCases[name];
        Assert.Equal(want, AssistantPanelController.PinnedLabel(contexts));
    }

    /// <summary>
    /// The bar "Another message is selected": only while the conversation
    /// keeps its context and the selection is part of none of it; a folded
    /// conversation is part of a pinned context when they share a message
    /// or, being a conversation, the thread.
    /// </summary>
    [Fact]
    public async Task AnotherSelectedBar()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var m1 = Message("m1", "Invoice", "t1");
        await h.On(() =>
        {
            h.Panel.SetContext(Message("m2", thread: "t2"));
            Assert.False(h.Panel.AnotherSelected, "nothing is pinned before the first question");
            h.Panel.SetContext(m1);
            Assert.True(h.Panel.Submit("Who sent it?"));
        });
        await h.TurnAsync();
        var cases = new (string Name, Context? Context, bool Want)[]
        {
            ("another message", Message("m2", thread: "t2"), true),
            ("the pinned message", m1, false),
            ("no selection", null, false),
            ("another message of the same thread", Message("m6", thread: "t1"), true),
            ("a folded conversation of the thread", Folded(["m5"], 3, partial: true, thread: "t1"), false),
            ("a conversation with the message", Folded(["m7", "m1"], 2), false),
            ("another folded conversation", Folded(["m8"], 2, partial: true, thread: "t3"), true),
            ("the same id in another account", new Context(new AssistantSelection("b", ["m1"])), true),
        };
        await h.On(() =>
        {
            var states = 0;
            h.Panel.StateChanged += (_, _) => states++;
            foreach (var (name, c, want) in cases)
            {
                h.Panel.SetContext(c);
                Assert.True(h.Panel.AnotherSelected == want, name);
                Assert.Equal("Conversation about: Invoice", h.Panel.ContextLabel);
            }
            Assert.Equal(cases.Length, states);
            h.Panel.NewConversation();
            Assert.False(h.Panel.AnotherSelected);
            Assert.Equal("Selected message", h.Panel.ContextLabel);
        });
    }

    /// <summary>Add to Conversation: the selection joins the conversation, the bar goes, and the next free question tells the model once.</summary>
    [Fact]
    public async Task AddToConversation()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var m1 = Message("m1", "Invoice", "t1");
        var m2 = Message("m2", "Lunch", "t2");
        await h.On(() =>
        {
            h.Panel.SetContext(m1);
            h.Panel.Run(AssistantAction.Summarize);
        });
        await h.TurnAsync();
        await h.On(() =>
        {
            h.Panel.AddSelection(); // nothing: the selection is what the conversation is about
            Assert.Single(h.Panel.PinnedContexts);
            h.Panel.SetContext(m2);
            Assert.True(h.Panel.AnotherSelected);
            h.Panel.AddSelection();
            Assert.False(h.Panel.AnotherSelected);
            Assert.Equal([m1, m2], Contexts(h));
            Assert.Equal([true, false], Announced(h));
            Assert.Equal("Conversation about 2 messages", h.Panel.ContextLabel);
            Assert.True(h.Panel.Submit("Which is older?"));
        });
        await h.TurnAsync();
        Assert.Equal(
            "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nWhich is older?",
            h.LastPrompt);
        Assert.Equal([true, true], await h.On(() => Announced(h)));
        Assert.True(await h.On(() => h.Panel.Submit("And the total?")));
        await h.TurnAsync();
        Assert.Equal("And the total?", h.LastPrompt);
        Assert.Equal(1, h.Starts);
        await h.On(() =>
        {
            h.Panel.SetContext(m1);
            Assert.False(h.Panel.AnotherSelected);
        });
    }

    /// <summary>An added folded conversation's members are asked for at once, and the model hears of all of them.</summary>
    [Fact]
    public async Task AddedConversationIsResolved()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var asked = new List<Context>();
        var conv = Folded(["m9"], 3, partial: true, subject: "Trip", thread: "t9");
        await h.On(() =>
        {
            h.Panel.ResolveContext = (c, done) =>
            {
                asked.Add(c);
                h.Changed();
                done(new AssistantSelection("a", ["m9", "m8", "m7"]));
            };
            h.Panel.SetContext(Message("m1", "Invoice"));
            Assert.True(h.Panel.Submit("Who sent it?"));
        });
        await h.TurnAsync();
        await h.On(() =>
        {
            h.Panel.SetContext(conv);
            Assert.True(h.Panel.AnotherSelected);
            h.Panel.AddSelection();
            Assert.Equal("Conversation about 4 messages", h.Panel.ContextLabel);
        });
        await h.WhenAsync(() => h.Panel.PinnedContexts[^1].Context?.Partial == false, "the members");
        Assert.Equal([conv], asked);
        await h.On(() =>
        {
            Assert.Equal("Conversation about 4 messages", h.Panel.ContextLabel);
            Assert.False(h.Panel.AnotherSelected);
            Assert.True(h.Panel.Submit("When do we leave?"));
        });
        await h.TurnAsync();
        Assert.Equal(
            "Context: the user has also selected a conversation with messages m9, m8, m7 (newest first) in account a; questions from now on may be about it too.\n\nWhen do we leave?",
            h.LastPrompt);
        Assert.Single(asked);
    }

    /// <summary>The bar's New Conversation: nothing is pinned any more, the chip follows the selection, and the next question starts over.</summary>
    [Fact]
    public async Task NewConversationFromTheBar()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var m2 = Message("m2", "Lunch");
        await h.On(() =>
        {
            h.Panel.SetContext(Message("m1", "Invoice"));
            Assert.True(h.Panel.Submit("First"));
        });
        await h.TurnAsync();
        await h.On(() =>
        {
            h.Panel.SetContext(m2);
            Assert.True(h.Panel.AnotherSelected);
            h.Panel.NewConversation();
            Assert.False(h.Panel.IsPinned);
            Assert.Empty(h.Panel.PinnedContexts);
            Assert.Empty(h.Panel.Items);
            Assert.False(h.Panel.AnotherSelected);
            Assert.Equal("Selected message", h.Panel.ContextLabel);
            Assert.True(h.Panel.CanRunActions);
            Assert.True(h.Panel.Submit("Second"));
        });
        await h.TurnAsync();
        Assert.Equal(2, h.Starts);
        Assert.Equal("Context: the user has selected message m2 in account a.\n\nSecond", h.LastPrompt);
        await h.On(() =>
        {
            Assert.Equal([m2], Contexts(h));
            Assert.Equal("Conversation about: Lunch", h.Panel.ContextLabel);
        });
    }

    /// <summary>
    /// A menu's action on a message that is part of no pinned context adds
    /// it and names it in its own prompt, so no line of context goes with
    /// the next question; on a pinned message it only runs. An attachment's
    /// question does the same for its message.
    /// </summary>
    [Fact]
    public async Task MenuActionOnAnotherMessageAddsIt()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var m1 = Message("m1", "Invoice", "t1");
        var m2 = Message("m2", "Lunch", "t2");
        var m4 = Message("m4", "Visit", "t4");
        await h.On(() =>
        {
            h.Panel.SetContext(m1);
            Assert.True(h.Panel.Submit("Who sent it?"));
        });
        await h.TurnAsync();
        Assert.Equal("Context: the user has selected message m1 in account a.\n\nWho sent it?", h.LastPrompt);

        await h.On(() =>
        {
            h.Panel.SetContext(m2);
            h.Panel.RunOn(AssistantAction.Summarize, m2);
        });
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.Summarize, m2.Selection), h.LastPrompt);
        await h.On(() =>
        {
            Assert.Equal([m1, m2], Contexts(h));
            Assert.Equal([true, true], Announced(h));
            Assert.False(h.Panel.AnotherSelected);
            Assert.True(h.Panel.Submit("Anything urgent?"));
        });
        await h.TurnAsync();
        Assert.Equal("Anything urgent?", h.LastPrompt);

        await h.On(() => h.Panel.RunOn(AssistantAction.Tasks, m1));
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.Tasks, m1.Selection), h.LastPrompt);
        Assert.Equal(2, await h.On(() => h.Panel.PinnedContexts.Count));

        await h.On(() =>
        {
            h.Panel.AskAttachment("a", "m3", "2", subject: "Scan", threadId: "t3");
            Assert.Equal(3, h.Panel.PinnedContexts.Count);
            Assert.Equal("Conversation about 3 messages", h.Panel.ContextLabel);
            Assert.True(h.Panel.Submit("What is it?"));
        });
        await h.TurnAsync();
        Assert.Equal(Assistant.AttachmentPrompt("a", "m3", "2") + "What is it?", h.LastPrompt);
        Assert.True(await h.On(() => h.Panel.Submit("And the date?")));
        await h.TurnAsync();
        Assert.Equal("And the date?", h.LastPrompt);

        // A waiting action stays on its message when the selection moves on.
        await h.On(() =>
        {
            h.Panel.SetContext(m4);
            h.Panel.RunOn(AssistantAction.DraftReply, m4);
            Assert.Equal(new PendingAction(AssistantAction.DraftReply), h.Panel.Pending);
            Assert.Equal(4, h.Panel.PinnedContexts.Count);
            Assert.False(h.Panel.AnotherSelected);
            h.Panel.SetContext(m1);
            h.Panel.SetContext(null);
            Assert.Equal(new PendingAction(AssistantAction.DraftReply), h.Panel.Pending);
            Assert.True(h.Panel.Submit("Fine"));
        });
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.DraftReply, m4.Selection) + "Fine", h.LastPrompt);
        Assert.Equal(1, h.Starts);
    }

    /// <summary>The quick actions act on the newest pinned context, not on the selection; with only all mail pinned there is nothing to act on.</summary>
    [Fact]
    public async Task QuickActionsUseTheNewestPinnedContext()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        var m1 = Message("m1", "Invoice", "t1");
        var m2 = Message("m2", "Lunch", "t2");
        await h.On(() =>
        {
            h.Panel.SetContext(m1);
            Assert.True(h.Panel.Submit("Who sent it?"));
        });
        await h.TurnAsync();
        await h.On(() =>
        {
            h.Panel.SetContext(m2);
            h.Panel.AddSelection();
            h.Panel.SetContext(Message("m3", thread: "t3"));
            Assert.True(h.Panel.AnotherSelected);
            Assert.True(h.Panel.CanRunActions);
            h.Panel.Run(AssistantAction.Tasks);
        });
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.Tasks, m2.Selection), h.LastPrompt);
        await h.On(() =>
        {
            h.Panel.Run(AssistantAction.DraftReply);
            h.Panel.SetContext(null);
            Assert.Equal(new PendingAction(AssistantAction.DraftReply), h.Panel.Pending);
            Assert.True(h.Panel.Submit("ok"));
        });
        await h.TurnAsync();
        Assert.Equal(Prompt(AssistantAction.DraftReply, m2.Selection) + "ok", h.LastPrompt);

        await h.On(() =>
        {
            h.Panel.NewConversation();
            Assert.True(h.Panel.Submit("How many unread?"));
        });
        await h.TurnAsync();
        Assert.Equal("How many unread?", h.LastPrompt);
        await h.On(() =>
        {
            Assert.Equal([null], Contexts(h));
            Assert.Equal("All mail", h.Panel.ContextLabel);
            Assert.False(h.Panel.CanRunActions);
            h.Panel.SetContext(m1);
            Assert.True(h.Panel.AnotherSelected);
            h.Panel.AddSelection();
            Assert.True(h.Panel.CanRunActions);
            Assert.Equal("Conversation about: Invoice", h.Panel.ContextLabel);
            Assert.True(h.Panel.Submit("From whom?"));
        });
        await h.TurnAsync();
        Assert.Equal(
            "Context: the user has also selected message m1 in account a; questions from now on may be about it too.\n\nFrom whom?",
            h.LastPrompt);
    }

    /// <summary>After Stop the next question starts a new Claude Code, which knows nothing of the conversation: its contexts are told again.</summary>
    [Fact]
    public async Task ANewProcessIsToldTheContextAgain()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            CannedStreamJson.AnswerTurn("Summary"),
            [FakeClaudeStep.Lines(CannedStreamJson.Init, CannedStreamJson.Delta("Thinking")), FakeClaudeStep.Hang()],
            CannedStreamJson.AnswerTurn("Again")));
        await h.On(() =>
        {
            h.Panel.SetContext(Message("m1", "Invoice"));
            h.Panel.Run(AssistantAction.Summarize);
        });
        await h.TurnAsync();
        await h.On(() =>
        {
            h.Panel.SetContext(Message("m2", "Lunch"));
            h.Panel.AddSelection();
            Assert.True(h.Panel.Submit("Which is older?"));
        });
        await h.WhenAsync(() => h.Last() == new AnswerContent("Thinking", true), "the streamed text");
        Assert.Equal(
            "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nWhich is older?",
            h.LastPrompt);
        await h.On(() =>
        {
            h.Panel.Stop();
            Assert.Equal(2, h.Panel.PinnedContexts.Count);
            Assert.Equal("Conversation about 2 messages", h.Panel.ContextLabel);
        });
        // The stand-in outlives its input: the kill after the grace ends it.
        h.Time.Advance(Grace);
        Assert.True(await h.On(() => h.Panel.Submit("Once more")));
        await h.TurnAsync();
        Assert.Equal(2, h.Starts);
        Assert.Equal(
            "Context: the user has selected message m1 in account a.\n"
            + "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nOnce more",
            h.LastPrompt);
    }

    // Windows additions

    /// <summary>LocalDate is the day on the given clock in its time zone, Gregorian.</summary>
    [Fact]
    public void LocalDateIsTheDayInTheUsersZone()
    {
        var time = new FakeTimeProvider(new DateTimeOffset(2026, 9, 29, 23, 30, 0, TimeSpan.Zero));
        time.SetLocalTimeZone(TimeZoneInfo.Utc);
        Assert.Equal("2026-09-29", AssistantPanelController.LocalDate(time));
        time.SetLocalTimeZone(TimeZoneInfo.CreateCustomTimeZone("UTC+02", TimeSpan.FromHours(2), "UTC+02", "UTC+02"));
        Assert.Equal("2026-09-30", AssistantPanelController.LocalDate(time));
    }

    /// <summary>A context counts at least its ids, as Swift's init does; more than one is a conversation.</summary>
    [Fact]
    public void ContextCountsItsIds()
    {
        var three = new AssistantSelection("a", ["m3", "m2", "m1"]);
        Assert.Equal(3, new Context(three).Count);
        Assert.Equal(5, new Context(three, 5).Count);
        Assert.True(new Context(three).Conversation);
        Assert.False(One.Conversation);
        Assert.Equal(1, new Context(new AssistantSelection("a", [])).Count);
    }

    /// <summary>A closed panel (the application quits) takes no question and starts nothing.</summary>
    [Fact]
    public async Task AClosedPanelDoesNothing()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(CannedStreamJson.AnswerTurn("ok")));
        await h.On(() =>
        {
            h.Panel.SetContext(One);
            h.Panel.Close();
            Assert.True(h.Panel.IsClosed);
            Assert.False(h.Panel.CanRunActions);
            h.Panel.Run(AssistantAction.Summarize);
            Assert.False(h.Panel.Submit("Hello"));
            Assert.Equal(Phase.Idle, h.Panel.CurrentPhase);
            Assert.Empty(h.Panel.Items);
        });
        await h.IdleAsync();
        Assert.Equal(0, h.Starts);
    }

    /// <summary>
    /// A panel over a stand-in claude in a directory of its own, on the
    /// test's UI thread (Swift's PanelHarness): the locator kept to the
    /// directory, the environment with what must never reach claude, the
    /// date and the language fixed, and every report recorded.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly UiConditions conditions = new();

        // A phase other than Idle was reported since the last turn ended.
        private bool busy;

        private Harness(Func<string, FakeClaudeScript> script)
        {
            Claude = script(Dir.Path).CreateIn(Dir.Path);
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

        public AssistantPanelController Panel { get; private set; } = null!;

        /// <summary>The panel's locator, the application's (the settings sign in through it too).</summary>
        public ClaudeCodeLocator Locator { get; private set; } = null!;

        /// <summary>The sign-ins the stand-in ran.</summary>
        public int Logins => FakeClaudeScript.Logins(Dir.Path);

        public int ConsentAsked { get; private set; }

        public bool ConsentAnswer { get; set; } = true;

        public List<string> Restored { get; } = [];

        public int Focused { get; private set; }

        public List<AssistantPanelController.Change> Changes { get; } = [];

        public List<DraftRef> Opened { get; } = [];

        /// <summary>The conversations the stand-in started.</summary>
        public int Starts => FakeClaudeScript.Starts(Dir.Path);

        /// <summary>The text of every turn written to the stand-in.</summary>
        public IReadOnlyList<string> Prompts => FakeClaudeScript.Prompts(Dir.Path);

        /// <summary>The text of the last turn written to the stand-in; "" before any.</summary>
        public string LastPrompt => Prompts is { Count: > 0 } p ? p[^1] : "";

        public static Task<Harness> CreateAsync(FakeClaudeScript script, bool consent = true, string? bridge = TestBridge) =>
            CreateAsync(_ => script, consent, bridge);

        /// <summary>A harness whose stand-in <paramref name="script"/> makes, given the directory.</summary>
        public static async Task<Harness> CreateAsync(
            Func<string, FakeClaudeScript> script, bool consent = true, string? bridge = TestBridge, TimeSpan? signInTimeout = null)
        {
            var h = new Harness(script);
            h.Panel = await h.Ui.RunAsync(() =>
            {
                h.Settings.AssistantClaudePath = h.Claude;
                h.Settings.AssistantConsent = consent;
                var prefix = h.Dir.Path + @"\";
                var locator = new ClaudeCodeLocator(
                    h.Settings,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path), ("ANTHROPIC_API_KEY", "sk-never"), ("MALACHI_SOCKET", TestSocket)),
                    timeout: TimeSpan.FromSeconds(60),
                    usable: p => p.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) && ClaudeCodeLocator.IsExecutableFile(p),
                    signInTimeout: signInTimeout ?? TimeSpan.FromSeconds(60));
                locator.SigningInChanged += (_, _) => h.Changed();
                h.Locator = locator;
                var panel = new AssistantPanelController(
                    h.Settings,
                    locator,
                    bridge,
                    TestSocket,
                    h.Work,
                    CannedStreamJson.Environment(("USERPROFILE", h.Dir.Path), ("LANG", "cs_CZ.UTF-8"), ("ANTHROPIC_API_KEY", "sk-never")),
                    killGrace: Grace,
                    time: h.Time,
                    pending: h.Pending,
                    directories: h.Directories)
                {
                    Today = () => "2026-09-29",
                    Language = () => "Czech",
                };
                panel.Consent = () =>
                {
                    h.ConsentAsked++;
                    h.Changed();
                    return Task.FromResult(h.ConsentAnswer);
                };
                panel.OpenDraft = draft =>
                {
                    h.Opened.Add(draft);
                    h.Changed();
                };
                panel.RestoreInputRequested += (_, text) =>
                {
                    h.Restored.Add(text);
                    h.Changed();
                };
                panel.FocusInputRequested += (_, _) =>
                {
                    h.Focused++;
                    h.Changed();
                };
                panel.Changed += (_, change) =>
                {
                    h.Changes.Add(change);
                    h.Changed();
                };
                panel.StateChanged += (_, _) =>
                {
                    h.busy |= panel.CurrentPhase != Phase.Idle;
                    h.Changed();
                };
                return panel;
            });
            return h;
        }

        /// <summary>Something the conditions look at changed (on the UI thread).</summary>
        public void Changed() => conditions.Changed();

        /// <summary>Runs <paramref name="f"/> on the UI thread.</summary>
        public Task<T> On<T>(Func<T> f) => Ui.RunAsync(f);

        /// <summary>Runs <paramref name="a"/> on the UI thread.</summary>
        public Task On(Action a) => Ui.RunAsync(a);

        public Task WhenAsync(Func<bool> condition, string what) => conditions.WhenAsync(Ui, condition, what);

        /// <summary>
        /// Waits until the turn under way ended (Swift's turn(): a phase
        /// other than Idle, then Idle).
        /// </summary>
        public Task TurnAsync() => WhenAsync(
            () =>
            {
                if (!busy || Panel.CurrentPhase != Phase.Idle)
                {
                    return false;
                }
                busy = false;
                return true;
            },
            "the end of the turn");

        /// <summary>The last item's content, on the UI thread; null for none.</summary>
        public AssistantPanelContent? Last() => Panel.Items.Count > 0 ? Panel.Items[^1].Content : null;

        public Task<AssistantPanelContent?> LastAsync() => Ui.RunAsync(Last);

        public Task<List<AssistantPanelContent>> ContentsAsync() => Ui.RunAsync(() => Panel.Items.Select(i => i.Content).ToList());

        // The panel's work and processes are done; a process ends by itself
        // or at the kill, after the grace on the fake clock.
        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, timeout: TimeSpan.FromSeconds(60));

        // Closes the panel, kills what is left and waits for it, so the
        // directory can go.
        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(Panel.Close);
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
