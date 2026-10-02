// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition, ahead of a Swift port (APICodingTests.swift has no
// board yet): the board of docs/api.md §4.13 and its errors (§2:
// caseNotFound, quoteNotFound) through the typed layer, with invented data.
// The params encode with the JSON names of pkg/api and leave out what Go
// leaves out (omitempty), the results decode, values of a newer daemon
// decode as themselves, and every board method waits the default timeout.

using System;
using System.Linq;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Xunit;
using static Malachi.Core.Tests.Api.ApiJson;

namespace Malachi.Core.Tests.Api;

public sealed partial class ApiCodingTests
{
    internal const string BoardCaseJson = """
        {"id":"c_00112233445566778899aabbccddeeff","accountId":"acc_1","threadId":"t_9",
         "ruleState":"you","ruleReason":"you.addressed","userState":"them",
         "annotation":{"state":"hot","title":"Contract for Alice","summary":"Alice needs the signed contract.\nBy Friday.",
                       "why":"Alice asked you directly.","tasks":["Sign it","Send it back"],
                       "due":{"at":"2026-10-02T15:00:00Z","quote":"Please send it back by Friday 3 pm.","messageId":"m_5"},
                       "source":"claude-opus","at":"2026-09-30T08:30:00Z"},
         "visibility":"live","subject":"Contract","person":{"name":"Alice","address":"alice@example.org"},
         "date":"2026-09-30T08:00:00Z","snippet":"Could you sign","unread":true,"hasAttachments":true,"messageCount":3,
         "replyMessageId":"m_5","replyFolderId":"f_inbox","latestMessageId":"m_7","canArchive":true,
         "draft":{"draftId":"d_1","text":"Signed, attached.","updated":"2026-09-30T09:00:00Z"},
         "version":12}
        """;

    /// <summary>docs/api.md §4.13 <c>board.list</c>: cases, commitments and triage decode; null lists read as empty.</summary>
    [Fact]
    public void BoardListExample()
    {
        Assert.Empty(Keys(EncodeObject(new BoardListParams()))); // every enabled account
        Assert.Equal(["acc_1"], EncodeObject(new BoardListParams { AccountIds = ["acc_1"] }).GetProperty("accountIds").EnumerateArray().Select(e => e.GetString()));

        var r = Decode<BoardListResult>($$$"""
            {"cases":[{{{BoardCaseJson}}}],
             "commitments":[{"id":"k_1","caseId":"c_00112233445566778899aabbccddeeff","accountId":"acc_1","messageId":"m_4",
                             "text":"Send the report","quote":"I will send the report on Monday.","due":"2026-10-05T09:00:00Z",
                             "state":"open","at":"2026-09-30T08:31:00Z"}],
             "enabled":true,"assistant":true,
             "triage":{"lastRun":{"at":"2026-09-30T08:00:00Z","endedAt":"2026-09-30T08:02:00Z","trigger":"auto","source":"claude-opus","annotated":4},
                       "annotatedTodayAuto":12,"queue":3},
             "ready":true}
            """);
        var c = Assert.Single(r.Cases);
        Assert.Equal("c_00112233445566778899aabbccddeeff", c.Id);
        Assert.Equal("acc_1", c.AccountId);
        Assert.Equal("t_9", c.ThreadId);
        Assert.Equal(BoardState.You, c.RuleState.Value);
        Assert.Equal(BoardReason.YouAddressed, c.RuleReason.Value);
        Assert.Equal<BoardState?>(BoardState.Them, c.UserState);
        Assert.Equal(BoardVisibility.Live, c.Visibility.Value);
        Assert.True(c.DoneAt is null && c.RemindAt is null && c.Issue is null);
        Assert.Equal(new Address { Name = "Alice", Email = "alice@example.org" }, c.Person);
        Assert.Equal(Rfc3339.Parse("2026-09-30T08:00:00Z"), c.Date);
        Assert.True(c.Unread && c.HasAttachments && c.CanArchive);
        Assert.Equal(3, c.MessageCount);
        Assert.Equal("m_5", c.ReplyMessageId);
        Assert.Equal("f_inbox", c.ReplyFolderId);
        Assert.Equal("m_7", c.LatestMessageId);
        Assert.Equal(12L, c.Version);
        Assert.Equal("d_1", c.Draft!.DraftId);
        Assert.Equal("Signed, attached.", c.Draft.Text);
        var a = c.Annotation!;
        Assert.Equal<BoardState?>(BoardState.Hot, a.State);
        Assert.Equal("Alice needs the signed contract.\nBy Friday.", a.Summary);
        Assert.Equal(["Sign it", "Send it back"], a.Tasks);
        Assert.Equal("m_5", a.Due!.MessageId);
        Assert.Equal("Please send it back by Friday 3 pm.", a.Due.Quote);
        Assert.Equal(Rfc3339.Parse("2026-10-02T15:00:00Z"), a.Due.At);
        Assert.Equal("claude-opus", a.Source);
        Assert.Null(a.Stale);

        var k = Assert.Single(r.Commitments);
        Assert.Equal("k_1", k.Id);
        Assert.Equal(c.Id, k.CaseId);
        Assert.Equal(BoardCommitmentState.Open, k.State.Value);
        Assert.Null(k.ClosedReason);
        Assert.Equal(Rfc3339.Parse("2026-10-05T09:00:00Z"), k.Due);

        Assert.True(r.Enabled && r.Assistant && r.Ready);
        Assert.Null(r.Truncated);
        Assert.Equal(12, r.Triage.AnnotatedTodayAuto);
        Assert.Equal(3, r.Triage.Queue);
        var run = r.Triage.LastRun!;
        Assert.Equal(BoardTrigger.Auto, run.Trigger.Value);
        Assert.Equal(4, run.Annotated);
        Assert.Null(run.Error);

        // A board that is off: nothing listed, no run yet.
        var off = Decode<BoardListResult>("""
            {"cases":null,"commitments":null,"enabled":false,"assistant":false,"triage":{"annotatedTodayAuto":0,"queue":0},"ready":false}
            """);
        Assert.Empty(off.Cases);
        Assert.Empty(off.Commitments);
        Assert.Null(off.Triage.LastRun);
        Assert.Null(off.Triage.Usage24h); // no run in the last 24 hours reported usage
    }

    /// <summary>board.get and the user's decisions: setState, setDone, remind, archive, discardDraft, setDraft.</summary>
    [Fact]
    public void BoardCaseMethods()
    {
        AssertSameJson("""{"caseId":"c_1"}""", EncodeObject(new BoardGetParams { CaseId = "c_1" }).GetRawText());
        var got = Decode<BoardGetResult>($$$"""
            {"case":{{{BoardCaseJson}}},
             "messages":[{"id":"m_5","folderId":"f_inbox","from":{"address":"alice@example.org"},"date":"2026-09-30T08:00:00Z","mine":false,"text":"Could you sign?","trimmed":true},
                         {"id":"m_6","folderId":"f_sent","from":{"address":"me@example.org"},"date":"2026-09-30T08:10:00Z","mine":true,"text":"Will do."}]}
            """);
        Assert.Equal(2, got.Messages.Count);
        Assert.Equal("m_5", got.Messages[0].Id);
        Assert.True(got.Messages[0].Trimmed);
        Assert.True(got.Messages[1].Mine);
        Assert.Null(got.Messages[1].Trimmed);
        Assert.Empty(Decode<BoardGetResult>($$$"""{"case":{{{BoardCaseJson}}},"messages":null}""").Messages);

        AssertSameJson("""{"caseId":"c_1","state":"hot"}""", EncodeObject(new BoardSetStateParams { CaseId = "c_1", State = BoardState.Hot }).GetRawText());
        // Back to automatic: the null state is left out, which the daemon reads the same.
        AssertSameJson("""{"caseId":"c_1"}""", EncodeObject(new BoardSetStateParams { CaseId = "c_1" }).GetRawText());
        AssertSameJson("""{"caseId":"c_1","done":false}""", EncodeObject(new BoardSetDoneParams { CaseId = "c_1", Done = false }).GetRawText());
        var until = DateTimeOffset.FromUnixTimeSeconds(1_788_343_200);
        AssertSameJson("""{"caseId":"c_1","until":"2026-09-02T10:00:00Z"}""", EncodeObject(new BoardRemindParams { CaseId = "c_1", Until = until }).GetRawText());
        AssertSameJson("""{"caseId":"c_1"}""", EncodeObject(new BoardRemindParams { CaseId = "c_1" }).GetRawText());
        AssertSameJson("""{"caseId":"c_1"}""", EncodeObject(new BoardArchiveParams { CaseId = "c_1" }).GetRawText());
        AssertSameJson("""{"caseId":"c_1"}""", EncodeObject(new BoardDiscardDraftParams { CaseId = "c_1" }).GetRawText());
        AssertSameJson("""{"caseId":"c_1","draftId":"d_42"}""", EncodeObject(new BoardSetDraftParams { CaseId = "c_1", DraftId = "d_42" }).GetRawText());
        Assert.Equal("c_00112233445566778899aabbccddeeff", Decode<BoardSetDraftResult>($$$"""{"case":{{{BoardCaseJson}}}}""").Case.Id);

        var archived = Decode<BoardArchiveResult>($$$"""{"archived":2,"case":{{{BoardCaseJson}}}}""");
        Assert.Equal(2, archived.Archived);
        Assert.Null(archived.NoArchive);
        var onlyDone = Decode<BoardArchiveResult>($$$"""{"archived":0,"noArchive":true,"case":{{{BoardCaseJson}}}}""");
        Assert.True(onlyDone.NoArchive);

        // board.unflag: nothing to clear is no error, 0 is sent.
        AssertSameJson("""{"caseId":"c_1"}""", EncodeObject(new BoardUnflagParams { CaseId = "c_1" }).GetRawText());
        var unflagged = Decode<BoardUnflagResult>($$$"""{"case":{{{BoardCaseJson}}},"unflagged":2}""");
        Assert.Equal(2, unflagged.Unflagged);
        Assert.Equal("c_00112233445566778899aabbccddeeff", unflagged.Case.Id);
        Assert.Equal(0, Decode<BoardUnflagResult>($$$"""{"case":{{{BoardCaseJson}}},"unflagged":0}""").Unflagged);

        // draft.get, and Draft.local: left out when false, as Go's omitempty.
        AssertSameJson("""{"accountId":"acc_1","draftId":"d_1"}""", EncodeObject(new DraftGetParams { AccountId = "acc_1", DraftId = "d_1" }).GetRawText());
        var draftResult = Decode<DraftGetResult>("""{"draft":{"id":"d_1","accountId":"acc_1","version":2,"to":[],"subject":"Re: Lunch","textBody":"Yes.","local":true,"updatedAt":"2026-10-02T07:00:00Z"}}""");
        Assert.True(draftResult.Draft.Local);
        Assert.Equal(new DraftId("d_1"), draftResult.Draft.Id);
        var plain = new Draft { AccountId = "acc_1", Subject = "s", TextBody = "t" };
        Assert.False(EncodeObject(plain).TryGetProperty("local", out _));
        Assert.True(EncodeObject(plain with { Local = true }).GetProperty("local").GetBoolean());
        Assert.Equal("c_00112233445566778899aabbccddeeff", Decode<BoardSetStateResult>($$$"""{"case":{{{BoardCaseJson}}}}""").Case.Id);

        // A snoozed case of a jira account.
        var snoozed = Decode<BoardCase>(BoardCaseJson
            .Replace("\"visibility\":\"live\"", "\"visibility\":\"snoozed\",\"remindAt\":\"2026-10-02T07:00:00Z\"", StringComparison.Ordinal)
            .Replace("\"canArchive\":true", "\"canArchive\":false,\"issue\":{\"key\":\"ITSD-42\",\"status\":\"In Progress\"}", StringComparison.Ordinal));
        Assert.Equal(BoardVisibility.Snoozed, snoozed.Visibility.Value);
        Assert.Equal(Rfc3339.Parse("2026-10-02T07:00:00Z"), snoozed.RemindAt);
        Assert.Equal(new BoardIssue { Key = "ITSD-42", Status = "In Progress" }, snoozed.Issue);
        Assert.Null(snoozed.Issue!.StatusCategory);
    }

    /// <summary>Triage (the MCP bridge's methods): board.queue, board.annotate, board.commit, board.setCommitment.</summary>
    [Fact]
    public void BoardTriageMethods()
    {
        Assert.Empty(Keys(EncodeObject(new BoardQueueParams()))); // every triage account, the daemon's limit
        var queueParams = EncodeObject(new BoardQueueParams { CaseIds = ["c_1"], Limit = 5 });
        Assert.Equal(["caseIds", "limit"], Keys(queueParams));
        var q = Decode<BoardQueueResult>("""
            {"items":[{"caseId":"c_1","accountId":"acc_1","inputKey":"0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f","ruleState":"you","ruleReason":"you.repliedToYou",
                       "subject":"Contract","replyMessageId":"m_5","own":["me@example.org"],
                       "messages":[{"messageId":"m_5","from":{"address":"alice@example.org"},"date":"2026-09-30T08:00:00Z","mine":false,"text":"Could you sign?"}]}],
             "remaining":7}
            """);
        var item = Assert.Single(q.Items);
        Assert.Equal(BoardReason.YouRepliedToYou, item.RuleReason.Value);
        Assert.Null(item.UserState);
        Assert.Null(item.Issue);
        Assert.Equal(["me@example.org"], item.Own);
        var message = Assert.Single(item.Messages);
        Assert.True(message.To is null && message.Cc is null && message.Truncated is null);
        Assert.Equal(7, q.Remaining);
        Assert.Null(item.HasDraft);
        Assert.True(Decode<BoardQueueItem>("""
            {"caseId":"c_1","accountId":"acc_1","inputKey":"ik","ruleState":"you","ruleReason":"you.addressed","subject":"s","replyMessageId":"m_5","hasDraft":true}
            """).HasDraft);
        Assert.Null(Decode<BoardAnnotateResult>($$$"""{"case":{{{BoardCaseJson}}}}""").DraftNotLinked);
        Assert.True(Decode<BoardAnnotateResult>($$$"""{"case":{{{BoardCaseJson}}},"draftNotLinked":true}""").DraftNotLinked);

        var annotate = EncodeObject(new BoardAnnotateParams { CaseId = "c_1", InputKey = "ik", Source = "claude-opus" });
        Assert.Equal(["caseId", "inputKey", "source"], Keys(annotate)); // what is left out is empty
        var full = EncodeObject(new BoardAnnotateParams
        {
            CaseId = "c_1",
            InputKey = "ik",
            RunId = "r_1",
            State = BoardState.Them,
            Title = "Contract",
            Summary = "Alice waits.",
            Why = "You promised it.",
            Tasks = ["Send it"],
            Due = new BoardDue { At = DateTimeOffset.FromUnixTimeSeconds(1_788_343_200), Quote = "by Friday, please", MessageId = "m_5" },
            DraftId = "d_1",
            Source = "claude-opus",
        });
        Assert.Equal(["caseId", "draftId", "due", "inputKey", "runId", "source", "state", "summary", "tasks", "title", "why"], Keys(full));
        Assert.Equal("them", full.GetProperty("state").GetString());
        Assert.Equal("2026-09-02T10:00:00Z", full.GetProperty("due").GetProperty("at").GetString());
        Assert.Equal("r_1", full.GetProperty("runId").GetString());

        var commit = EncodeObject(new BoardCommitParams
        {
            CaseId = "c_1",
            InputKey = "ik",
            MessageId = "m_4",
            Text = "Send the report",
            Quote = "I will send the report on Monday.",
            Source = "claude-opus",
        });
        Assert.Equal(["caseId", "inputKey", "messageId", "quote", "source", "text"], Keys(commit));
        var committed = Decode<BoardCommitResult>("""
            {"commitment":{"id":"k_1","caseId":"c_1","accountId":"acc_1","messageId":"m_4","text":"Send the report",
                           "quote":"I will send the report on Monday.","state":"closed","closedReason":"replied","at":"2026-09-30T08:31:00Z"}}
            """).Commitment;
        Assert.Equal(BoardCommitmentState.Closed, committed.State.Value);
        Assert.Equal("replied", committed.ClosedReason);
        Assert.Null(committed.Due);
        AssertSameJson("""{"commitmentId":"k_1","done":true}""", EncodeObject(new BoardSetCommitmentParams { CommitmentId = "k_1", Done = true }).GetRawText());
    }

    /// <summary>board.preferences, board.setPreferences, board.runStart and board.runEnd.</summary>
    [Fact]
    public void BoardPreferencesAndRuns()
    {
        var prefs = Decode<BoardPreferencesResult>("""
            {"preferences":{"enabled":true,"assistant":false,"windows":{"hot":90,"you":30,"them":30,"info":14},
                            "triageAccounts":[],"autoTriage":false,"autoTriageMinutes":30,"autoTriageDailyCases":60}}
            """).Preferences;
        Assert.Equal(new BoardWindows { Hot = 90, You = 30, Them = 30, Info = 14 }, prefs.Windows);
        Assert.Empty(prefs.TriageAccounts);
        Assert.Equal(30, prefs.AutoTriageMinutes);
        Assert.Equal(60, prefs.AutoTriageDailyCases);
        var set = EncodeObject(new BoardSetPreferencesParams { Preferences = prefs with { Assistant = true } }).GetProperty("preferences");
        Assert.True(set.GetProperty("assistant").GetBoolean());
        Assert.False(set.GetProperty("autoTriage").GetBoolean()); // every field is sent, false ones too
        Assert.Equal(0, set.GetProperty("triageAccounts").GetArrayLength()); // empty = every mail account, never null
        Assert.Equal(14, set.GetProperty("windows").GetProperty("info").GetInt32());

        AssertSameJson("""{"trigger":"manual","source":"claude-opus"}""", EncodeObject(new BoardRunStartParams { Trigger = BoardTrigger.Manual, Source = "claude-opus" }).GetRawText());
        Assert.Equal("r_1", Decode<BoardRunStartResult>("""{"runId":"r_1"}""").RunId);
        AssertSameJson("""{"runId":"r_1"}""", EncodeObject(new BoardRunEndParams { RunId = "r_1" }).GetRawText()); // a success
        AssertSameJson("""{"runId":"r_1","error":"signedOut"}""", EncodeObject(new BoardRunEndParams { RunId = "r_1", Error = BoardRunError.SignedOut }).GetRawText());
        AssertSameJson(
            """{"runId":"r_1","usage":{"inputTokens":1200,"outputTokens":340,"cacheCreationInputTokens":0,"cacheReadInputTokens":9000}}""",
            EncodeObject(new BoardRunEndParams
            {
                RunId = "r_1",
                Usage = new BoardUsage { InputTokens = 1200, OutputTokens = 340, CacheCreationInputTokens = 0, CacheReadInputTokens = 9000 },
            }).GetRawText());
        var usage = Decode<BoardTriage>("""
            {"annotatedTodayAuto":0,"queue":0,"usage24h":{"inputTokens":1200,"outputTokens":340,"cacheCreationInputTokens":0,"cacheReadInputTokens":9000,"runs":2}}
            """).Usage24h!;
        Assert.Equal(1200L, usage.InputTokens);
        Assert.Equal(9000L, usage.CacheReadInputTokens);
        Assert.Equal(2, usage.Runs);
        Assert.Equal(typeof(EmptyParams), API.BoardPreferences.ParamsInfo.Type);
        Assert.Equal(typeof(EmptyResult), API.BoardRunEnd.ResultInfo.Type);
    }

    /// <summary>The board's error codes, quoteNotFound's data, values of a newer daemon, and the timeouts.</summary>
    [Fact]
    public void BoardErrorsValuesAndTimeouts()
    {
        var quote = Decode<RpcError>("""{"code":1506,"message":"quote not found","data":{"field":"commitment"}}""");
        Assert.Equal(ErrorCode.QuoteNotFound, quote.Code);
        Assert.Equal("quoteNotFound", quote.Code.Name);
        Assert.Equal(QuoteField.Commitment, quote.Data!.Value.Deserialize(ApiJsonContext.Wire.QuoteNotFoundData)!.Field.Value);
        var missing = Decode<RpcError>("""{"code":1106,"message":"no such case"}""");
        Assert.Equal(ErrorCode.CaseNotFound, missing.Code);
        Assert.Equal("caseNotFound", missing.Code.Name);

        // An open enumeration: a reason, a visibility or a run error of a newer daemon decodes as itself.
        var newer = Decode<BoardCase>(BoardCaseJson
            .Replace("\"you.addressed\"", "\"you.mentioned\"", StringComparison.Ordinal)
            .Replace("\"visibility\":\"live\"", "\"visibility\":\"pinned\"", StringComparison.Ordinal));
        Assert.Equal("you.mentioned", newer.RuleReason.Value);
        Assert.Equal("pinned", newer.Visibility.Value);
        var run = Decode<BoardRun>("""{"at":"2026-09-30T08:00:00Z","trigger":"external","source":"s","annotated":0,"error":"quota"}""");
        Assert.Equal(new BoardRunError("quota"), run.Error);
        Assert.Null(run.EndedAt);

        var board = API.Methods.Where(m => m.Name.StartsWith("board.", StringComparison.Ordinal)).ToArray();
        Assert.Equal(15, board.Length);
        Assert.All(board, m => Assert.Equal(RpcTimeouts.Default, m.Timeout));
        Assert.Same(API.BoardList, board[0]);
        Assert.Same(API.BoardRunEnd, board[^1]);
    }
}
