// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// ErrGone reports that the site no longer has the item a message shows
// (the issue, the comment or the changelog entry is gone, or the user may
// no longer see it). The next pass removes the local copies.
var ErrGone = errors.New("jira: item gone")

// FetchMessage rebuilds the message of one stored item from the site for
// message.download: the issue, its comments (the description needs them
// too, to know which files are its own) or its changelog are read again
// and the item built exactly as the syncer builds it (synth.go), so the
// result has the stored Message-ID and, when the site still says the
// same, the stored bytes. The size is the message's length.
//
// Errors: ErrGone (wrapped) when the item is gone; otherwise *api.Error
// (ToAPIError of the site's or the network's failure, invalidArgument for
// an account without a Jira configuration).
func (sv *Supervisor) FetchMessage(ctx context.Context, acc store.Account, msg store.Message) (io.ReadCloser, int64, error) {
	cfg := acc.Config.Jira
	if cfg == nil {
		return nil, 0, api.NewError(api.CodeInvalidArgument, "jira: the account has no jira configuration")
	}
	issueID, ok := store.IssueIDOfThread(msg.ThreadID)
	kind, _, _ := strings.Cut(msg.RemoteID, ":")
	if !ok || (kind != "i" && kind != "c" && kind != "h") {
		return nil, 0, fmt.Errorf("%w: not a message of an issue", ErrGone)
	}
	ac, err := sv.clientFor(acc)
	if err != nil {
		return nil, 0, ToAPIError(err)
	}
	got, err := ac.remote.BulkIssues(ctx, []string{issueID}, IssueOptions{Fields: FieldsAll, Rendered: true})
	if err != nil {
		return nil, 0, ToAPIError(err)
	}
	if len(got) == 0 {
		return nil, 0, fmt.Errorf("%w: the site no longer shows the issue", ErrGone)
	}
	is := got[0]
	var comments []Comment
	if kind == "i" || kind == "c" {
		cl, err := ac.remote.Comments(ctx, issueID, 0)
		switch {
		case IsNotFound(err):
			return nil, 0, fmt.Errorf("%w: the site no longer shows the issue", ErrGone)
		case err != nil:
			return nil, 0, ToAPIError(err)
		}
		comments = cl.Comments
	}
	var hist []History
	if kind == "h" {
		hs, err := ac.remote.Changelogs(ctx, []string{issueID})
		if err != nil {
			return nil, 0, ToAPIError(err)
		}
		hist = hs[issueID]
	}
	log := sv.deps.Log.With("component", "jira", "account", acc.ID)
	y, err := newSynth(*cfg, compileRules(*cfg, log), ac.remote, ac.client, User{}, log)
	if err != nil {
		return nil, 0, ToAPIError(err)
	}
	for _, it := range y.items(is, comments, hist, false, false) {
		if it.remoteID != msg.RemoteID {
			continue
		}
		raw, err := y.build(ctx, is, it)
		if err != nil {
			return nil, 0, ToAPIError(err)
		}
		return io.NopCloser(bytes.NewReader(raw)), int64(len(raw)), nil
	}
	return nil, 0, fmt.Errorf("%w: the site no longer has the item", ErrGone)
}
