// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A minimal case sends every required field and none of the optional ones.
func TestBoardCaseMinimalJSON(t *testing.T) {
	date := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	c := BoardCase{
		ID: "c_0123456789abcdef0123456789abcdef", AccountID: "acc_1", ThreadID: "t_9",
		RuleState: BoardYou, RuleReason: BoardReasonYouAddressed, Visibility: BoardLive,
		Subject: "Lunch", Person: Address{Name: "Alice", Address: "alice@example.org"}, Date: date,
		Snippet: "Shall we?", MessageCount: 1, ReplyMessageID: "m_1", ReplyFolderID: "f_inbox",
		LatestMessageID: "m_1", Version: 7,
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"c_0123456789abcdef0123456789abcdef","accountId":"acc_1","threadId":"t_9",` +
		`"ruleState":"you","ruleReason":"you.addressed","visibility":"live","subject":"Lunch",` +
		`"person":{"name":"Alice","address":"alice@example.org"},"date":"2026-10-01T09:30:00Z",` +
		`"snippet":"Shall we?","unread":false,"hasAttachments":false,"messageCount":1,` +
		`"replyMessageId":"m_1","replyFolderId":"f_inbox","latestMessageId":"m_1","canArchive":false,"version":7}`
	if string(raw) != want {
		t.Fatalf("minimal case:\n got %s\nwant %s", raw, want)
	}
	var back BoardCase
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, c) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, c)
	}
}

func TestBoardCaseFullJSON(t *testing.T) {
	date := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	done, due := date.Add(time.Hour), date.Add(72*time.Hour)
	c := BoardCase{
		ID: "c_1", AccountID: "acc_2", ThreadID: "jira:10042",
		RuleState: BoardInfo, RuleReason: BoardReasonJiraWatching, UserState: Ptr(BoardHot),
		Annotation: &BoardAnnotation{
			State: Ptr(BoardThem), Title: "Printer", Summary: "Line one\nLine two", Why: "Waiting for IT",
			Tasks: []string{"Ask again"}, Source: "claude", At: date, Stale: true,
			Due: &BoardDue{At: due, Quote: "by Friday at the latest", MessageID: "m_7"},
		},
		Visibility: BoardDone, DoneAt: &done, Subject: "ITSD-42: Printer",
		Person: Address{Address: "u-1@users.jira.invalid"}, Date: date, Unread: true, HasAttachments: true,
		MessageCount: 3, ReplyMessageID: "m_5", ReplyFolderID: "space:1", LatestMessageID: "m_7",
		Issue:      &BoardIssue{Key: "ITSD-42", Status: "In Progress", StatusCategory: StatusCategoryInProgress},
		CanArchive: true, Draft: &BoardDraft{DraftID: "d_1", Text: "Thanks", Updated: date}, Version: 1,
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"userState":"hot"`, `"annotation":{"state":"them","title":"Printer"`,
		`"tasks":["Ask again"]`, `"due":{"at":"2026-10-04T09:30:00Z","quote":"by Friday at the latest","messageId":"m_7"}`,
		`"stale":true`, `"doneAt":"2026-10-01T10:30:00Z"`, `"issue":{"key":"ITSD-42","status":"In Progress","statusCategory":"inProgress"}`,
		`"draft":{"draftId":"d_1","text":"Thanks","updated":"2026-10-01T09:30:00Z"}`, `"canArchive":true`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("full case lacks %s: %s", key, raw)
		}
	}
	if strings.Contains(string(raw), "remindAt") {
		t.Errorf("absent remindAt sent: %s", raw)
	}
	var back BoardCase
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, c) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, c)
	}
}

// An annotation without a state or deadline leaves them off the wire, but
// always sends its tasks (never null) and its text fields.
func TestBoardAnnotationJSON(t *testing.T) {
	raw, err := json.Marshal(BoardAnnotation{Tasks: []string{}, Source: "s", At: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"title":"","summary":"","why":"","tasks":[],"source":"s","at":"2026-10-01T00:00:00Z"}`; string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
}

// board.setState and board.remind take null for "back to automatic" and
// "no remind"; absent means the same.
func TestBoardNullParams(t *testing.T) {
	for _, in := range []string{`{"caseId":"c_1","state":null}`, `{"caseId":"c_1"}`} {
		var p BoardSetStateParams
		if err := json.Unmarshal([]byte(in), &p); err != nil {
			t.Fatal(err)
		}
		if p.CaseID != "c_1" || p.State != nil {
			t.Errorf("%s decoded as %+v", in, p)
		}
	}
	var p BoardSetStateParams
	if err := json.Unmarshal([]byte(`{"caseId":"c_1","state":"them"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.State == nil || *p.State != BoardThem {
		t.Errorf("state decoded as %v", p.State)
	}
	raw, _ := json.Marshal(BoardSetStateParams{CaseID: "c_1"})
	if string(raw) != `{"caseId":"c_1","state":null}` {
		t.Errorf("clearing a state: %s", raw)
	}

	var r BoardRemindParams
	if err := json.Unmarshal([]byte(`{"caseId":"c_1","until":null}`), &r); err != nil || r.Until != nil {
		t.Errorf("null until: %+v, %v", r, err)
	}
	if err := json.Unmarshal([]byte(`{"caseId":"c_1","until":"2026-10-02T07:00:00Z"}`), &r); err != nil ||
		r.Until == nil || !r.Until.Equal(time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("until: %+v, %v", r, err)
	}
}

func TestBoardListResultJSON(t *testing.T) {
	at := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	res := BoardListResult{
		Cases: []BoardCase{}, Commitments: []BoardCommitment{{
			ID: "k_1", CaseID: "c_1", AccountID: "acc_1", MessageID: "m_2", Text: "Send the slides",
			Quote: "I will send the slides tomorrow", State: CommitmentOpen, At: at,
		}},
		Enabled: true, Assistant: true, Ready: true,
		Triage: BoardTriage{
			LastRun:            &BoardRun{At: at, Trigger: TriggerAuto, Source: "claude", Annotated: 4, Error: RunTimeout},
			AnnotatedTodayAuto: 4, Queue: 2,
		},
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"cases":[],"commitments":[{"id":"k_1","caseId":"c_1","accountId":"acc_1","messageId":"m_2",` +
		`"text":"Send the slides","quote":"I will send the slides tomorrow","state":"open","at":"2026-10-01T08:00:00Z"}],` +
		`"enabled":true,"assistant":true,"triage":{"lastRun":{"at":"2026-10-01T08:00:00Z","trigger":"auto",` +
		`"source":"claude","annotated":4,"error":"timeout"},"annotatedTodayAuto":4,"queue":2},"ready":true}`
	if string(raw) != want {
		t.Fatalf("list result:\n got %s\nwant %s", raw, want)
	}
	var back BoardListResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, res) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, res)
	}
}

func TestBoardQueueJSON(t *testing.T) {
	date := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	res := BoardQueueResult{Items: []BoardQueueItem{{
		CaseID: "c_1", AccountID: "acc_1", InputKey: "00ff", RuleState: BoardYou,
		RuleReason: BoardReasonYouRepliedToYou, Subject: "Offer", ReplyMessageID: "m_3",
		Own: []string{"me@example.org"},
		Messages: []BoardQueueMessage{{
			MessageID: "m_3", From: Address{Address: "bob@example.org"}, To: []Address{{Address: "me@example.org"}},
			Date: date, Text: "Any news?", Truncated: true,
		}},
	}}, Remaining: 4}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"cc"`) || strings.Contains(string(raw), `"userState"`) || strings.Contains(string(raw), `"issue"`) {
		t.Errorf("empty optional fields sent: %s", raw)
	}
	var back BoardQueueResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, res) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, res)
	}
}

func TestBoardAnnotateAndCommitParamsJSON(t *testing.T) {
	due := time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC)
	a := BoardAnnotateParams{CaseID: "c_1", InputKey: "k", Source: "claude"}
	raw, _ := json.Marshal(a)
	if want := `{"caseId":"c_1","inputKey":"k","source":"claude"}`; string(raw) != want {
		t.Errorf("minimal annotate: %s, want %s", raw, want)
	}
	a = BoardAnnotateParams{CaseID: "c_1", InputKey: "k", RunID: "r_1", State: Ptr(BoardHot), Title: "T",
		Summary: "S", Why: "W", Tasks: []string{"a", "b"}, Due: &BoardDue{At: due, Quote: "until next Friday", MessageID: "m_1"},
		DraftID: "d_1", Source: "claude"}
	raw, _ = json.Marshal(a)
	var backA BoardAnnotateParams
	if err := json.Unmarshal(raw, &backA); err != nil || !reflect.DeepEqual(backA, a) {
		t.Fatalf("annotate round trip: %+v, %v", backA, err)
	}

	c := BoardCommitParams{CaseID: "c_1", InputKey: "k", MessageID: "m_2", Text: "Call Bob", Quote: "I will call Bob on Monday", Due: &due, Source: "claude"}
	raw, _ = json.Marshal(c)
	if strings.Contains(string(raw), "runId") {
		t.Errorf("empty runId sent: %s", raw)
	}
	var backC BoardCommitParams
	if err := json.Unmarshal(raw, &backC); err != nil || !reflect.DeepEqual(backC, c) {
		t.Fatalf("commit round trip: %+v, %v", backC, err)
	}
}

// quoteNotFound names the field in error.data.
func TestQuoteNotFoundJSON(t *testing.T) {
	e := &Error{Code: CodeQuoteNotFound, Message: "quote not found", Data: QuoteNotFoundData{Field: QuoteFieldDue}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"code":1506,"message":"quote not found","data":{"field":"due"}}`; string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
	if CodeCaseNotFound.String() != "caseNotFound" || CodeQuoteNotFound.String() != "quoteNotFound" {
		t.Errorf("names: %s %s", CodeCaseNotFound, CodeQuoteNotFound)
	}
}

func TestBoardPreferencesJSON(t *testing.T) {
	raw, err := json.Marshal(DefaultBoardPreferences())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"enabled":true,"assistant":false,"windows":{"hot":90,"you":30,"them":30,"info":14},` +
		`"triageAccounts":[],"autoTriage":false,"autoTriageMinutes":30,"autoTriageDailyCases":60}`
	if string(raw) != want {
		t.Fatalf("defaults:\n got %s\nwant %s", raw, want)
	}
	var p BoardSetPreferencesParams
	if err := json.Unmarshal([]byte(`{"preferences":`+want+`}`), &p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Preferences, DefaultBoardPreferences()) {
		t.Fatalf("round trip: %+v", p.Preferences)
	}
}

func TestBoardRunAndNotificationJSON(t *testing.T) {
	raw, _ := json.Marshal(BoardRunEndParams{RunID: "r_1"})
	if string(raw) != `{"runId":"r_1"}` {
		t.Errorf("a successful run's end: %s", raw)
	}
	raw, _ = json.Marshal(BoardRunStartParams{Trigger: TriggerManual, Source: "claude"})
	if string(raw) != `{"trigger":"manual","source":"claude"}` {
		t.Errorf("run start: %s", raw)
	}
	if raw, _ = json.Marshal(BoardChangedNotification{}); string(raw) != `{}` {
		t.Errorf("notification for any account: %s", raw)
	}
	if raw, _ = json.Marshal(BoardChangedNotification{AccountIDs: []AccountID{"acc_1"}}); string(raw) != `{"accountIds":["acc_1"]}` {
		t.Errorf("notification: %s", raw)
	}
}

func TestBoardUsageJSON(t *testing.T) {
	u := BoardUsage{InputTokens: 1200, OutputTokens: 340, CacheCreationInputTokens: 5, CacheReadInputTokens: 9000}
	end := BoardRunEndParams{RunID: "r_1", Error: RunCancelled, Usage: &u}
	raw, _ := json.Marshal(end)
	want := `{"runId":"r_1","error":"cancelled","usage":{"inputTokens":1200,"outputTokens":340,` +
		`"cacheCreationInputTokens":5,"cacheReadInputTokens":9000}}`
	if string(raw) != want {
		t.Fatalf("run end:\n got %s\nwant %s", raw, want)
	}
	var back BoardRunEndParams
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, end) {
		t.Fatalf("run end round trip: %+v %v", back, err)
	}

	tr := BoardTriage{Queue: 1, Usage24h: &BoardUsageTotal{BoardUsage: u, Runs: 2}}
	raw, _ = json.Marshal(tr)
	want = `{"annotatedTodayAuto":0,"queue":1,"usage24h":{"inputTokens":1200,"outputTokens":340,` +
		`"cacheCreationInputTokens":5,"cacheReadInputTokens":9000,"runs":2}}`
	if string(raw) != want {
		t.Fatalf("triage:\n got %s\nwant %s", raw, want)
	}
	var trBack BoardTriage
	if err := json.Unmarshal(raw, &trBack); err != nil || !reflect.DeepEqual(trBack, tr) {
		t.Fatalf("triage round trip: %+v %v", trBack, err)
	}

	// Non-integers do not decode (the dispatcher's invalidParams).
	for _, bad := range []string{`{"runId":"r","usage":{"inputTokens":1.5}}`, `{"runId":"r","usage":{"inputTokens":"7"}}`,
		`{"runId":"r","usage":{"inputTokens":1e30}}`} {
		if err := json.Unmarshal([]byte(bad), &back); err == nil {
			t.Errorf("%s decoded", bad)
		}
	}
	if !u.Valid() || (BoardUsage{OutputTokens: -1}).Valid() {
		t.Error("Valid")
	}
	if c := (BoardUsage{InputTokens: -3, OutputTokens: 2 * MaxBoardUsageTokens, CacheReadInputTokens: 7}).Clamped(); c !=
		(BoardUsage{OutputTokens: MaxBoardUsageTokens, CacheReadInputTokens: 7}) {
		t.Errorf("Clamped: %+v", c)
	}
}

func TestBoardStateValid(t *testing.T) {
	for _, s := range BoardStates {
		if !s.Valid() {
			t.Errorf("%q is not valid", s)
		}
	}
	for _, s := range []BoardState{"", "HOT", "done", "live"} {
		if s.Valid() {
			t.Errorf("%q is valid", s)
		}
	}
}
