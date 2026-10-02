// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
