// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// boardBackend serves a board whose setState echoes the decoded params;
// every other board method is the stub's.
type boardBackend struct {
	StubBackend
}

func (b *boardBackend) Board() api.BoardService { return echoBoard{} }

type echoBoard struct{ stubBoard }

func (echoBoard) SetState(_ context.Context, p api.BoardSetStateParams) (*api.BoardSetStateResult, error) {
	c := api.BoardCase{ID: p.CaseID, RuleState: api.BoardInfo, UserState: p.State}
	return &api.BoardSetStateResult{Case: c}, nil
}

// Archive answers with one moved message and a reminded case, so that
// the wire form of the 2026-10-08 fields can be checked.
func (echoBoard) Archive(_ context.Context, p api.BoardArchiveParams) (*api.BoardArchiveResult, error) {
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	return &api.BoardArchiveResult{Archived: 1, Case: api.BoardCase{ID: p.CaseID, Visibility: api.BoardLive, RemindedAt: &at},
		Moved: []api.BoardMoved{{MessageID: "m_1", FromFolderID: "f_inbox"}}}, nil
}

func boardMethods() []string {
	var out []string
	for _, m := range api.AllMethods {
		if strings.HasPrefix(m, "board.") {
			out = append(out, m)
		}
	}
	return out
}

// Every board method reaches the backend's board service: the stub answers
// notImplemented, and params that do not decode answer invalidParams
// before anything reaches it.
func TestBoardMethodsDispatch(t *testing.T) {
	if n := len(boardMethods()); n != 17 {
		t.Fatalf("%d board methods in api.AllMethods, want 17", n)
	}
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)
	for _, m := range boardMethods() {
		resp := call(t, c, r, m, map[string]any{})
		if resp.Error == nil || resp.Error.Code != api.CodeNotImplemented {
			t.Errorf("%s: want notImplemented from the stub, got %+v", m, resp)
		}
	}
	resp := call(t, c, r, api.MethodBoardSetDone, map[string]any{"caseId": 7})
	if resp.Error == nil || resp.Error.Code != api.CodeInvalidParams {
		t.Errorf("a number as caseId: want invalidParams, got %+v", resp)
	}
	resp = call(t, c, r, api.MethodBoardSetDraft, map[string]any{"caseId": "c_1", "draftId": []int{1}})
	if resp.Error == nil || resp.Error.Code != api.CodeInvalidParams {
		t.Errorf("an array as draftId: want invalidParams, got %+v", resp)
	}
}

func TestBoardSetStateDecodes(t *testing.T) {
	ts := startServer(t, &boardBackend{}, nil, nil)
	c, r := dialAuthed(t, ts.sock)

	resp := call(t, c, r, api.MethodBoardSetState, api.BoardSetStateParams{CaseID: "c_1", State: api.Ptr(api.BoardThem)})
	if resp.Error != nil {
		t.Fatalf("board.setState: %+v", resp.Error)
	}
	var res api.BoardSetStateResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.Case.ID != "c_1" || res.Case.UserState == nil || *res.Case.UserState != api.BoardThem {
		t.Errorf("echoed case: %+v", res.Case)
	}

	resp = call(t, c, r, api.MethodBoardSetState, json.RawMessage(`{"caseId":"c_2","state":null}`))
	if resp.Error != nil {
		t.Fatalf("board.setState null: %+v", resp.Error)
	}
	var cleared api.BoardSetStateResult
	if err := json.Unmarshal(resp.Result, &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Case.ID != "c_2" || cleared.Case.UserState != nil {
		t.Errorf("null state echoed as %+v", cleared.Case)
	}

	// The other board methods are still the stub's.
	resp = call(t, c, r, api.MethodBoardList, api.BoardListParams{})
	if resp.Error == nil || resp.Error.Code != api.CodeNotImplemented {
		t.Errorf("board.list: want notImplemented, got %+v", resp)
	}
}

// board.archive's moved messages and a case's remindedAt reach the client
// under their documented names.
func TestBoardArchiveMovedOnTheWire(t *testing.T) {
	ts := startServer(t, &boardBackend{}, nil, nil)
	c, r := dialAuthed(t, ts.sock)
	resp := call(t, c, r, api.MethodBoardArchive, api.BoardArchiveParams{CaseID: "c_1"})
	if resp.Error != nil {
		t.Fatalf("board.archive: %+v", resp.Error)
	}
	raw := string(resp.Result)
	for _, want := range []string{`"moved":[{"messageId":"m_1","fromFolderId":"f_inbox"}]`, `"remindedAt":"2026-10-08T09:00:00Z"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("result %s lacks %s", raw, want)
		}
	}
}

func TestBoardChangedNotification(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)

	var n api.BoardNotifier = ts.Server
	n.BoardChanged(api.BoardChangedNotification{AccountIDs: []api.AccountID{"acc_1"}})
	line, err := readLine(c, r, waitLimit)
	if err != nil {
		t.Fatalf("no notification: %v", err)
	}
	var got api.Notification
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != api.NotifyBoardChanged || string(got.Params) != `{"accountIds":["acc_1"]}` {
		t.Errorf("notification: %s", line)
	}
}
