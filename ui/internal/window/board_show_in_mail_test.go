// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

func TestDecideReveal(t *testing.T) {
	inbox := folderKey{Account: "acc_1", Folder: "f_inbox"}
	other := folderKey{Account: "acc_1", Folder: "f_other"}

	cases := []struct {
		name                         string
		curFolder                    folderKey
		loading, searching, timedOut bool
		want                         revealOutcome
	}{
		{"still loading the right folder", inbox, true, false, false, revealWaiting},
		{"finished loading the right folder", inbox, false, false, false, revealReady},
		{"a search started", inbox, true, true, false, revealDropped},
		{"another folder selected", other, true, false, false, revealDropped},
		{"another folder, not even loading", other, false, false, false, revealDropped},
		{"deadline passed while still loading", inbox, true, false, true, revealReady},
		{"finished and past the deadline", inbox, false, false, true, revealReady},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideReveal(inbox, c.curFolder, c.loading, c.searching, c.timedOut)
			if got != c.want {
				t.Errorf("decideReveal() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRevealRowKeys(t *testing.T) {
	cases := []struct {
		name   string
		msg    api.MessageID
		thread api.ThreadID
		want   []listKey
	}{
		{
			name: "flat mode: just the message",
			msg:  "m_1", thread: "",
			want: []listKey{{Message: "m_1"}},
		},
		{
			name: "grouped: the member row, then the conversation's",
			msg:  "m_1", thread: "t_1",
			want: []listKey{{Thread: "t_1", Message: "m_1"}, {Thread: "t_1"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := revealRowKeys(c.msg, c.thread)
			if len(got) != len(c.want) {
				t.Fatalf("revealRowKeys() = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("revealRowKeys()[%d] = %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestRevealGone(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found", &api.Error{Code: api.CodeMessageNotFound}, true},
		{"gone", &api.Error{Code: api.CodeMessageGone}, true},
		{"some other api error", &api.Error{Code: api.CodeInvalidArgument}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := revealGone(c.err); got != c.want {
				t.Errorf("revealGone(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// Show in Mail looks for the reply target, else the newest message
// (macOS reply ?? latestMessage).
func TestBoardRevealTarget(t *testing.T) {
	cases := []struct {
		name   string
		d      board.Detail
		msg    api.MessageID
		folder api.FolderID
		ok     bool
	}{
		{"the reply target with its folder", board.Detail{Reply: &board.ReplyTarget{Message: "m_1", Folder: "f_1"}, LatestMessage: "m_2"}, "m_1", "f_1", true},
		{"no reply target: the newest, folder from message.get", board.Detail{LatestMessage: "m_2"}, "m_2", "", true},
		{"an empty reply target falls back too", board.Detail{Reply: &board.ReplyTarget{}, LatestMessage: "m_2"}, "m_2", "", true},
		{"neither", board.Detail{}, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, folder, ok := boardRevealTarget(c.d)
			if msg != c.msg || folder != c.folder || ok != c.ok {
				t.Errorf("boardRevealTarget() = %q, %q, %v; want %q, %q, %v", msg, folder, ok, c.msg, c.folder, c.ok)
			}
		})
	}
}
