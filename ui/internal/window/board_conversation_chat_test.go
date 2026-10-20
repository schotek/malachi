// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"fmt"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// A bubble whose message.get failed says gone only for a message the
// daemon no longer has, as Show in Mail does.
func TestBoardChatMissingText(t *testing.T) {
	gone := board.ShowInMailGone(i18n.Tr)
	failed := board.ShowInMailFailed(i18n.Tr)
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, failed},
		{errors.New("connection lost"), failed},
		{&api.Error{Code: api.CodeMessageNotFound}, gone},
		{fmt.Errorf("wrapped: %w", &api.Error{Code: api.CodeMessageGone}), gone},
		{&api.Error{Code: api.CodeInvalidArgument}, failed},
	} {
		if got := boardChatMissingText(tc.err); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.err, got, tc.want)
		}
	}
}
