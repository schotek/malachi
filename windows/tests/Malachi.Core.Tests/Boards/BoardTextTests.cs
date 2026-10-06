// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/board/text_test.go (TestCounts, TestStates,
// TestStylesAndGroups, TestSourceAndStatusLine, TestReasons, TestPhases,
// TestFailed, TestInlineReply without the consent texts, which are the
// triage's) and the texts check of BoardModelTests.swift
// (BoardInlineReplyDetailTests.texts). The catalogue is English, so the
// texts are the msgids; the Czech plural forms are read from po/cs.po.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;
using Malachi.Core.Tests.I18n;
using Malachi.Core.Tests.Text;
using Xunit;
using static Malachi.Core.Boards.Board;
using BText = Malachi.Core.Boards.Board.Text;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardTextTests
{
    // The rule codes of docs/api.md §4.13.
    private static readonly string[] ReasonCodes =
    [
        "hot.important", "hot.flagged", "you.addressed", "you.repliedToYou", "them.replied", "them.asked",
        "info.ccOnly", "info.notAddressed", "info.yourNote", "info.unknownSender", "jira.yourComment",
        "jira.assigned", "jira.reporter", "jira.commented", "jira.watching", "kept",
    ];

    [Fact]
    public void Counts()
    {
        (string Got, string Want)[] cases =
        [
            (BText.CaseCount(1), "1 case"),
            (BText.CaseCount(23), "23 cases"),
            (BText.CaseCount(0), "0 cases"),
            (BText.MessageCount(1), "1 message"),
            (BText.MessageCount(3), "3 messages"),
            (BText.Conversation(3), "Conversation · 3 messages"),
            (BText.TodoPhrase(0), "Nothing needs you today."),
            (BText.TodoPhrase(-1), "Nothing needs you today."),
            (BText.TodoPhrase(1), "1 thing needs you today."),
            (BText.TodoPhrase(4), "4 things need you today."),
            (BText.AndMore(3), "and 3 more"),
            (BText.PromiseCount(1), "1 promise"),
            (BText.PromiseCount(3), "3 promises"),
            (BText.Archived(1, false), "Archived 1 message."),
            (BText.Archived(3, false), "Archived 3 messages."),
            (BText.Archived(0, false), "Marked as done. No message was in the inbox."),
            (BText.Archived(5, true), "Marked as done. This account has no archive."),
        ];
        foreach (var (got, want) in cases)
        {
            Assert.Equal(want, got);
        }
    }

    /// <summary>
    /// Go's TestPluralForms with the real Czech catalogue: every counted
    /// text asks for the form of its own number, and a context entry wins.
    /// </summary>
    [Fact]
    public void CzechPluralFormsAndContexts()
    {
        var cs = Catalogue.Load(RepositoryPo.Directory, ["cs"]);
        string Form(int n) => cs.Plural("%d case", "%d cases", n);
        Assert.NotEqual(Form(1), Form(3));
        Assert.NotEqual(Form(3), Form(12));
        Assert.NotEqual("All Accounts", cs.Context("account filter", "All Accounts"));
    }

    [Fact]
    public void States()
    {
        string[] want = ["Hot", "Waiting for You", "Waiting for Them", "For Your Information"];
        for (var i = 0; i < Board.States.Count; i++)
        {
            Assert.Equal(want[i], BText.StateName(Board.States[i]));
            Assert.Equal(want[i], BText.FilterTitle(Filter.Of(Board.States[i])));
        }
        Assert.Equal("Overview", BText.FilterTitle(Filter.All));
        Assert.Equal("Done", BText.FilterTitle(Filter.Done));
        Assert.Equal("Nothing burning.", BText.ColumnEmpty(State.Hot));
        Assert.Equal("Empty.", BText.ColumnEmpty(State.Info));
        Assert.Equal("", BText.StateName((State)9)); // an unknown state has no name
    }

    [Fact]
    public void StylesAndGroups()
    {
        (string Got, string Want)[] cases =
        [
            (BText.StyleTitle(BoardStyle.List), "List"),
            (BText.StyleTitle(BoardStyle.Columns), "Columns"),
            (BText.StyleTitle(BoardStyle.Today), "Today"),
            (BText.StyleMenuTitle(BoardStyle.List), "As List"),
            (BText.StyleMenuTitle(BoardStyle.Columns), "As Columns"),
            (BText.StyleMenuTitle(BoardStyle.Today), "Today"),
            (BText.DefaultStyleSetting, "Default View"),
            (BText.DueGroupTitle(DueGroupKind.Overdue), "Overdue"),
            (BText.DueGroupTitle(DueGroupKind.Today), "Today"),
            (BText.DueGroupTitle(DueGroupKind.Tomorrow), "Tomorrow"),
            (BText.DueGroupTitle(DueGroupKind.ThisWeek), "Next 7 Days"),
            (BText.DueGroupTitle(DueGroupKind.Later), "Later"),
            (BText.RemindPreset(RemindPresetKind.LaterToday), "Later Today"),
            (BText.RemindPreset(RemindPresetKind.Tomorrow), "Tomorrow"),
            (BText.RemindPreset(RemindPresetKind.NextWeek), "Next Week"),
            (BText.Close, "_Close"),
            (BText.Discard, "_Discard"),
            (BText.Quoted("Friday at noon"), "“Friday at noon”"),
            (BText.SnoozedUntil("Tomorrow 09:00"), "Back on the board Tomorrow 09:00"),
            (BText.SpokenRemind("Tomorrow 09:00"), "Back on the board Tomorrow 09:00"),
            (BText.SpokenDue("Tomorrow"), "Due Tomorrow"),
            (BText.SpokenAssistant("Lunch on Friday"), "Assistant: Lunch on Friday"),
        ];
        foreach (var (got, want) in cases)
        {
            Assert.Equal(want, got);
        }
    }

    [Fact]
    public void SourceAndStatusLine()
    {
        static Run Model(string m, string note = "") => new() { Model = m, Date = DateTimeOffset.UnixEpoch, Note = note };
        (string Got, string Want)[] cases =
        [
            (BText.SourceText(StateSource.Rules, null), "The daemon’s rules set the state. The assistant has not looked at this case yet."),
            (BText.SourceText(StateSource.AssistantKept, null), "The daemon’s rules set the state and the assistant kept it."),
            (BText.SourceText(StateSource.AssistantKept, Model("Sonnet")), "The daemon’s rules set the state and the assistant (Sonnet) kept it."),
            (BText.SourceText(StateSource.AssistantChanged(State.You), Model("")), "The assistant refined the state. The rules suggested: Waiting for You."),
            (BText.SourceText(StateSource.AssistantChanged(State.Hot), Model("Sonnet")), "The assistant (Sonnet) refined the state. The rules suggested: Hot."),
            (BText.SourceText(StateSource.User, null), "You moved this case yourself."),
            (BText.SourceText(StateSource.AssistantOff, Model("x")), "The daemon’s rules set the state; the assistant is off."),
            (BText.StatusLine(false, Model("m", "n")), "Sorted by the daemon’s rules · assistant off"),
            (BText.StatusLine(true, null), "Sorted by rules · refined by the assistant"),
            (BText.StatusLine(true, Model("Sonnet")), "Sorted by rules · refined by the assistant (Sonnet)"),
            (BText.StatusLine(true, Model("", "2 errors")), "Sorted by rules · refined by the assistant · 2 errors"),
            // The run's strings are cleaned here.
            (BText.StatusLine(true, Model(" Son\nnet ", "\u202E")), "Sorted by rules · refined by the assistant (Son net)"),
        ];
        foreach (var (got, want) in cases)
        {
            Assert.Equal(want, got);
        }
    }

    [Fact]
    public void Reasons()
    {
        var seen = new HashSet<string>();
        foreach (var c in ReasonCodes)
        {
            var r = BText.Reason(new BoardReason(c));
            Assert.True(r != BText.ReasonUnknown && seen.Add(r), $"Reason({c}) = {r} is not its own");
        }
        Assert.Equal(ReasonCodes.Order(StringComparer.Ordinal), KnownReasons.Select(r => r.Value).Order(StringComparer.Ordinal));
        foreach (var c in new[] { "", "hot", "kept.", "HOT.IMPORTANT" })
        {
            Assert.Equal("The daemon’s rules put the case here.", BText.Reason(new BoardReason(c)));
        }
        Assert.Equal(BText.ReasonUnknown, BText.Reason(default));
    }

    [Fact]
    public void Phases()
    {
        Assert.Equal(BText.EmptyTitle, BText.EmptyTitleOf(Phase.Ready));
        Assert.Equal(BText.EmptyBody, BText.EmptyBodyOf(Phase.Ready));
        Assert.Equal("", BText.EmptyBodyOf(Phase.Loading));
        foreach (var p in new[] { Phase.Loading, Phase.Ready, Phase.Off })
        {
            Assert.Equal("", BText.Notice(p, false));
            Assert.Equal("Only the newest 1,000 cases are on the board.", BText.Notice(p, true));
        }
        Assert.Equal(BText.EmptyBodyOf(Phase.Preparing), BText.Notice(Phase.Preparing, true));
        Assert.Equal(BText.EmptyBodyOf(Phase.Unsupported), BText.Notice(Phase.Unsupported, false));
        foreach (var p in new[] { Phase.Unavailable, Phase.Failed, Phase.Unsupported })
        {
            Assert.Equal("Board Unavailable", BText.EmptyTitleOf(p));
            Assert.True(p.IsFailure);
        }
        foreach (var p in new[] { Phase.Loading, Phase.Preparing, Phase.Ready, Phase.Off })
        {
            Assert.False(p.IsFailure);
        }
    }

    [Fact]
    public void Failed()
    {
        (BText.Action, Exception?, string)[] cases =
        [
            (BText.Action.Move, null, "Moving the case failed."),
            (BText.Action.Move, RpcErrorTextFailureException.NotConnected(), "Moving the case failed: the mail backend is not running."),
            (BText.Action.Move, RpcErrorTextFailureException.Disconnected(), "Moving the case failed: the mail backend is not running."),
            (BText.Action.Remind, RpcErrorTextFailureException.Timeout("board.remind"), "Setting the reminder failed: the mail backend did not answer in time."),
            (BText.Action.Remind, new OperationCanceledException(), "Setting the reminder failed: the mail backend did not answer in time."),
            (BText.Action.Archive, new RpcErrorTextFailureException("transport"), "Archiving failed."),
            (BText.Action.Commitment, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.CaseNotFound)), "Changing the promise failed: the promise no longer exists."),
            (BText.Action.Done, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.CaseNotFound)), "Marking the case done failed: the case is no longer on the board."),
            (BText.Action.Move, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.InvalidArgument)), "Moving the case failed: the board did not accept it."),
            (BText.Action.Preferences, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.MethodNotFound)), "Changing the board’s settings failed: this mail backend has no board."),
            (BText.Action.Preferences, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.NotImplemented)), "Changing the board’s settings failed: this mail backend has no board."),
            (BText.Action.Reopen, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.StorageError)), "Moving the case back to the board failed: the mail backend could not save it."),
            (BText.Action.DiscardDraft, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.DraftNotFound)), "Discarding the draft failed: the draft no longer exists."),
            (BText.Action.Unflag, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.CaseNotFound)), "Removing the star failed: the case is no longer on the board."),
            (BText.Action.Unflag, RpcErrorTextFailureException.Daemon(new ErrorCode(ErrorCode.Conflict), "the daemon's words"), "Removing the star failed."),
        ];
        foreach (var (action, error, want) in cases)
        {
            Assert.Equal(want, BText.Failed(action, error));
        }
        var seen = new HashSet<string>();
        foreach (var a in Enum.GetValues<BText.Action>())
        {
            var s = BText.Failed(a, null);
            Assert.True(seen.Add(s) && s != " failed.", $"Failed({a}) = {s} is not its own");
        }
    }

    [Fact]
    public void InlineReply()
    {
        (string Got, string Want)[] cases =
        [
            (BText.DraftNote, "Only here on the board until you send it"),
            (BText.ReplyLoading, "Loading the suggested reply…"),
            (BText.ReplyLoadFailed, "The suggested reply could not be opened."),
            (BText.ReplyRemoved, "The suggested reply was removed elsewhere."),
            (BText.ReplyNotSaved, "This reply could not be saved yet; Malachi Mail keeps trying."),
            (BText.ReplyNotSent("Re: Offer"), "Your reply “Re: Offer” was not sent; it is still on the board."),
            (BText.QuitUnsavedHeading, "Quit without saving a reply?"),
            (BText.QuitUnsavedBody, "A reply on the board could not be saved or sent yet. If you quit now, what you typed in it may be lost."),
            (BText.QuitAnyway, "_Quit Anyway"),
            (BText.Unstar, "Unstar"),
            (BText.Failed(BText.Action.Unflag, null), "Removing the star failed."),
        ];
        foreach (var (got, want) in cases)
        {
            Assert.Equal(want, got);
        }
        // The subject in the toast is cleaned and capped at 80 bytes.
        var sent = BText.ReplyNotSent(new string('x', 200) + "\n");
        Assert.Contains(new string('x', 80) + "”", sent, StringComparison.Ordinal);
        Assert.DoesNotContain(new string('x', 81), sent, StringComparison.Ordinal);
    }

    [Fact]
    public void BoardsOwnLiterals()
    {
        Assert.Equal("✦", BText.AssistantMark);
        Assert.Equal("Board", BText.BoardName);
        Assert.Equal("You", BText.You);
        Assert.Equal("All Accounts", BText.AllAccounts);
        Assert.Equal("Unread", BText.SpokenUnread);
    }
}
