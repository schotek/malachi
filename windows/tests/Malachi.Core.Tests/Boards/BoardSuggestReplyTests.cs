// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the board's half of macos/Tests/MalachiCoreTests/
// BoardSuggestReplyTests.swift (offered, offeredByAccount, view,
// failureTexts); Go: ui/internal/board/suggest_reply_test.go. The request's
// tests (commandLine, message, instructionCleaning, systemPrompt) test
// Assistant's suggestReply* and come with the port of
// AssistantSuggestReply.swift.

using System;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.IssueTrackers;
using Xunit;
using static Malachi.Core.Boards.Board;
using BoardText = Malachi.Core.Boards.Board.Text;
using IssueInfo = Malachi.Core.Boards.Board.IssueInfo;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardSuggestReplyTests
{
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_848_800);
    private static readonly BoardCaseId Id1 = new("c_1");
    private static readonly BoardCaseId Id2 = new("c_2");

    private static Case MailCase(State state = State.You) => new()
    {
        Id = Id1,
        Account = "acc_1",
        Person = "P",
        Date = T0,
        Subject = "S",
        RuleState = state,
        Reply = new ReplyTarget("m_1", "f_1"),
    };

    [Fact]
    public void Offered()
    {
        var mail = new AccountInfo("acc_1", "Work", "IMAP");
        var s = new Snapshot { Accounts = [mail] };
        Assert.True(SuggestReplyOffered(MailCase(), s, samples: false));
        Assert.False(SuggestReplyOffered(MailCase(), s, samples: true), "never with the samples");
        foreach (var st in new[] { State.Hot, State.Them })
        {
            Assert.True(SuggestReplyOffered(MailCase(st), s, samples: false));
        }
        Assert.False(SuggestReplyOffered(MailCase(State.Info), s, samples: false));
        // The state in effect counts: the user's choice, the annotation's.
        Assert.True(SuggestReplyOffered(MailCase(State.Info) with { UserState = State.You }, s, samples: false));
        Assert.False(SuggestReplyOffered(MailCase(State.You) with { UserState = State.Info }, s, samples: false));
        var annotated = MailCase(State.You) with { Annotation = new Annotation { State = State.Info, Title = "t" } };
        Assert.False(SuggestReplyOffered(annotated, new Snapshot { Accounts = [mail], Annotated = true }, samples: false));
        Assert.True(SuggestReplyOffered(annotated, s, samples: false), "annotations off");
        Assert.False(SuggestReplyOffered(MailCase() with { Reply = null }, s, samples: false));
        Assert.False(SuggestReplyOffered(MailCase() with { Draft = new DraftLink("d_1", "x") }, s, samples: false));
        Assert.False(SuggestReplyOffered(MailCase().WithDone(true), s, samples: false));
    }

    /// <summary>
    /// Jira: the reply is a comment draft, offered when the account can
    /// comment; an account not listed counts as mail unless the case is an
    /// issue.
    /// </summary>
    [Fact]
    public void OfferedByAccount()
    {
        var issue = MailCase() with { Issue = new IssueInfo("K-1", "Open", JiraStatusStyle.Plain) };
        var commenting = new AccountInfo("acc_1", "Jira", "JIRA", CanReply: true);
        var mute = new AccountInfo("acc_1", "Jira", "JIRA", CanReply: false);
        Assert.True(SuggestReplyOffered(issue, new Snapshot { Accounts = [commenting] }, samples: false));
        Assert.False(SuggestReplyOffered(issue, new Snapshot { Accounts = [mute] }, samples: false));
        Assert.False(SuggestReplyOffered(issue, new Snapshot(), samples: false));
        Assert.True(SuggestReplyOffered(MailCase(), new Snapshot(), samples: false));
        Assert.False(SuggestReplyOffered(MailCase(), new Snapshot { Accounts = [mute] }, samples: false));
    }

    private static SuggestReplyInputs Inputs(
        bool offered = true, bool available = true, bool found = true, bool? signedIn = true, SuggestReplyState? state = null) => new()
        {
            Offered = offered,
            Available = available,
            ClaudeFound = found,
            SignedIn = signedIn,
            State = state ?? new SuggestReplyState.Idle(),
            CaseId = Id1,
        };

    [Fact]
    public void View()
    {
        Assert.Equal(SuggestReplyView.Hidden, SuggestReplyViewOf(Inputs(offered: false)));
        Assert.Equal(SuggestReplyView.Hidden, SuggestReplyViewOf(Inputs(available: false)));
        var idle = SuggestReplyViewOf(Inputs());
        Assert.True(idle.Shown && idle.Enabled && !idle.Running && idle.Note.Length == 0 && !idle.NoteIsFailure);
        Assert.True(idle.Title == "✦ Suggest Reply" && idle.Placeholder == "What should the reply say? (optional)");
        Assert.True(idle.Progress == "Writing a suggested reply…" && idle.Stop == "Stop");
        Assert.True(SuggestReplyViewOf(Inputs(signedIn: null)).Enabled, "not known counts as signed in");

        var running = SuggestReplyViewOf(Inputs(state: new SuggestReplyState.Running(Id1)));
        Assert.True(running.Running && !running.Enabled && running.Note.Length == 0);
        var elsewhere = SuggestReplyViewOf(Inputs(state: new SuggestReplyState.Running(Id2)));
        Assert.True(!elsewhere.Running && !elsewhere.Enabled);
        Assert.Equal("The assistant is writing a reply for another conversation", elsewhere.Note);

        var missing = SuggestReplyViewOf(Inputs(found: false));
        Assert.True(missing.Shown && !missing.Enabled && missing.Note == Assistant.PanelTexts().NotFound);
        var signedOut = SuggestReplyViewOf(Inputs(signedIn: false));
        Assert.True(signedOut.Shown && !signedOut.Enabled && signedOut.Note == Assistant.SignInTexts().Hint && !signedOut.NoteIsFailure);

        var failed = SuggestReplyViewOf(Inputs(state: new SuggestReplyState.Failed(Id1, SuggestReplyFailure.Timeout)));
        Assert.True(failed.Enabled && failed.NoteIsFailure && failed.Note == "The suggested reply failed: it took too long.");
        var failedThere = SuggestReplyViewOf(Inputs(state: new SuggestReplyState.Failed(Id2, SuggestReplyFailure.Timeout)));
        Assert.True(failedThere.Enabled && failedThere.Note.Length == 0);
        // Signed out says so rather than the failure.
        Assert.Equal(
            Assistant.SignInTexts().Hint,
            SuggestReplyViewOf(Inputs(signedIn: false, state: new SuggestReplyState.Failed(Id1, SuggestReplyFailure.NotSignedIn))).Note);
        // Go: only a running request is running.
        Assert.True(!new SuggestReplyState.Idle().IsRunning && new SuggestReplyState.Running(Id1).IsRunning);
    }

    [Fact]
    public void FailureTexts()
    {
        (SuggestReplyFailure F, string Want)[] want =
        [
            (SuggestReplyFailure.NotFound, "Claude Code was not found"),
            (SuggestReplyFailure.NotSignedIn, "Claude Code is not signed in"),
            (SuggestReplyFailure.ToolsMissing, "the Malachi Mail tools are not available to the assistant"),
            (SuggestReplyFailure.Timeout, "it took too long"),
            (SuggestReplyFailure.Cancelled, "it was stopped"),
            (SuggestReplyFailure.Stopped, "the assistant stopped"),
            (SuggestReplyFailure.Backend, "the mail backend did not answer"),
            (SuggestReplyFailure.NoDraft, "the assistant wrote no reply"),
        ];
        Assert.Equal(SuggestReplyFailures.Length, want.Length);
        foreach (var (f, text) in want)
        {
            Assert.Equal(text, BoardText.SuggestReplyFailureText(f));
            Assert.Equal($"The suggested reply failed: {text}.", BoardText.SuggestReplyFailed(f));
        }
    }

    /// <summary>Windows addition: the Codex provider's words where Swift has them.</summary>
    [Fact]
    public void ChatGptProvider()
    {
        var chatGpt = Inputs(found: false) with { Provider = AssistantProviderID.ChatGpt };
        Assert.Equal("Codex was not found. Choose a native Codex executable.", SuggestReplyViewOf(chatGpt).Note);
        Assert.Equal("Reconnect to ChatGPT", SuggestReplyViewOf(Inputs(signedIn: false) with { Provider = AssistantProviderID.ChatGpt }).Note);
        var failed = Inputs(state: new SuggestReplyState.Failed(Id1, SuggestReplyFailure.NotSignedIn)) with
        {
            Provider = AssistantProviderID.ChatGpt,
        };
        Assert.Equal("The suggested reply failed: Reconnect to ChatGPT.", SuggestReplyViewOf(failed).Note);
    }
}
