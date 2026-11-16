// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// metaHTMLText records the upgrade pass that reads the HTML markup some
// senders put into the text/plain part as HTML (mime.LooksLikeHTML) for
// the messages stored before the parser did: absent = not started,
// "<version>:<last message id>" = in progress, "<version>:done" =
// finished. Another version starts the pass over.
const (
	metaHTMLText         = "mime.htmlText"
	htmlTextVersion      = "1"
	htmlTextBackfillRows = 50
	// htmlTextBackfillPause lets the sync writers in between two batches.
	htmlTextBackfillPause = 20 * time.Millisecond
)

// backfillHTMLText rewrites, in the background and in batches, the stored
// plain-text body and snippet of every message whose text is HTML markup,
// from the stored text itself (so it needs no raw file). The search index
// follows text_body through its triggers; the accounts with a changed row
// are told to list again. The cursor is saved after every batch; an
// interrupted pass resumes where it stopped.
func (b *Backend) backfillHTMLText(ctx context.Context) error {
	cursor, found, err := b.store.GetMeta(ctx, metaHTMLText)
	done := htmlTextVersion + ":done"
	if err != nil || cursor == done {
		return err
	}
	after := ""
	if found && strings.HasPrefix(cursor, htmlTextVersion+":") {
		after = strings.TrimPrefix(cursor, htmlTextVersion+":")
	}
	limits := mime.DefaultLimits()
	changed := map[string]bool{}
	fixed := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := b.store.ListAngleTexts(ctx, after, htmlTextBackfillRows)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		account := make(map[string]string, len(batch))
		var fixes []store.TextFix
		for _, c := range batch {
			if !mime.LooksLikeHTML(c.Text) {
				continue
			}
			text := mime.PlainText(c.Text, limits.MaxTextBytes)
			account[c.ID] = c.AccountID
			fixes = append(fixes, store.TextFix{ID: c.ID, Old: c.Text, Text: text, Snippet: mime.Snippet(text, limits.MaxSnippetRunes)})
		}
		if len(fixes) > 0 {
			var ids []string
			if err := b.store.Paced(ctx, func() error {
				var err error
				ids, err = b.store.SetTexts(ctx, fixes)
				return err
			}); err != nil {
				return err
			}
			for _, id := range ids {
				changed[account[id]] = true
			}
			fixed += len(ids)
		}
		after = batch[len(batch)-1].ID
		if err := b.store.SetMeta(ctx, metaHTMLText, htmlTextVersion+":"+after); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(htmlTextBackfillPause):
		}
	}
	if err := b.store.SetMeta(ctx, metaHTMLText, done); err != nil {
		return err
	}
	if fixed > 0 {
		b.log.Info("html markup in text parts read as html", "messages", fixed)
	}
	accounts := make([]string, 0, len(changed))
	for acc := range changed {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	for _, acc := range accounts {
		b.syncNotifier.MessagesChanged(api.MessagesChangedNotification{AccountID: api.AccountID(acc)})
	}
	return nil
}
