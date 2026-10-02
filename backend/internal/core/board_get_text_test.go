// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// board.get gives an ordinary mail whole (several paragraphs, past the
// old 4000 bytes), cuts a longer one at api.MaxBoardMessageTextBytes and
// marks it trimmed; board.queue keeps its own, smaller cap.
func TestBoardGetGivesAWholeMail(t *testing.T) {
	para := strings.TrimSpace("Could you check the figures before the meeting? " + strings.Repeat("The totals per region are in the second table. ", 10))
	whole := strings.TrimSpace(strings.Repeat(para+"\n\n", 11)) // about 6 kB
	if n := len(whole); n <= 4000 || n >= api.MaxBoardMessageTextBytes {
		t.Fatalf("fixture is %d bytes", n)
	}
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_whole", rfc: "w1", from: boardAlice, to: []api.Address{boardMe}, text: whole})
	x.put(bmail{folder: x.inbox, thread: "t_long", rfc: "l1", from: boardBob, to: []api.Address{boardMe}, at: 1,
		text: strings.Repeat("Is the long report ready? ", 1000)})
	x.drain()

	got, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: x.caseOf("t_whole").ID})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("board.get: %+v %v", got, err)
	}
	if m := got.Messages[0]; m.Text != whole || m.Trimmed {
		t.Fatalf("whole mail: %d bytes, trimmed %v", len(m.Text), m.Trimmed)
	}

	got, err = x.svc.Get(x.ctx, api.BoardGetParams{CaseID: x.caseOf("t_long").ID})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("board.get long: %+v %v", got, err)
	}
	if m := got.Messages[0]; len(m.Text) > api.MaxBoardMessageTextBytes || len(m.Text) < api.MaxBoardMessageTextBytes-30 || !m.Trimmed {
		t.Fatalf("long mail: %d bytes, trimmed %v", len(m.Text), m.Trimmed)
	}

	x.assistantOn()
	q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Items) == 0 {
		t.Fatal("board.queue: no items")
	}
	for _, it := range q.Items {
		for _, m := range it.Messages {
			if len(m.Text) > api.MaxBoardQueueMessageBytes {
				t.Fatalf("queue message %d bytes", len(m.Text))
			}
		}
	}
}
