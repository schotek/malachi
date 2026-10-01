// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The two variants of a cached body (loadedMessage.showQuoted, store) and
// the button's offer for an entry, ported with the macOS client's
// QuotedTextTests.swift (offer, variantsTradePlaces).

// quotedBody is the body of id: trimmed (cut says whether anything was)
// or whole, under policy.
func quotedBody(id string, whole, cut bool, policy api.RemoteContentPolicy) *api.MessageBodyResult {
	b := &api.MessageBodyResult{MessageID: api.MessageID(id), BodyState: api.BodyFetched, HTML: "<p>new</p>", Text: "new", RemoteContent: policy}
	if whole {
		b.HTML, b.Text = "<p>new</p><blockquote>old</blockquote>", "new\n> old"
	} else {
		b.QuotedTrimmed = cut
	}
	return b
}

func TestQuotedOfferForAnEntry(t *testing.T) {
	if got := quotedOffer(nil); got != conversation.QuotedNone {
		t.Errorf("nil: %v", got)
	}
	lm := &loadedMessage{}
	if got := quotedOffer(lm); got != conversation.QuotedNone {
		t.Errorf("nothing yet: %v", got)
	}
	lm.body = quotedBody("a", false, false, api.RemoteBlock)
	if got := quotedOffer(lm); got != conversation.QuotedNone {
		t.Errorf("nothing was cut: %v", got)
	}
	lm.body = quotedBody("a", false, true, api.RemoteBlock)
	if got := quotedOffer(lm); got != conversation.QuotedShow {
		t.Errorf("trimmed: %v", got)
	}
	lm.err = api.NewError(api.CodeInternalError, "x")
	if got := quotedOffer(lm); got != conversation.QuotedNone {
		t.Errorf("a failed body: %v", got)
	}
	lm.err = nil
	lm.showQuoted(true)
	if lm.body != nil || quotedOffer(lm) != conversation.QuotedHide {
		t.Errorf("the whole body on its way: %+v", lm)
	}
	lm.err = api.NewError(api.CodeInternalError, "x")
	if got := quotedOffer(lm); got != conversation.QuotedHide {
		t.Errorf("failed: the way back stays: %v", got)
	}
}

func TestLoadedMessageVariantsTradePlaces(t *testing.T) {
	trimmed := quotedBody("a", false, true, api.RemoteBlock)
	whole := quotedBody("a", true, false, api.RemoteBlock)
	lm := &loadedMessage{body: trimmed}
	small := lm.size()
	if lm.showQuoted(false) {
		t.Error("already trimmed")
	}
	if !lm.showQuoted(true) || !lm.quotedShown || lm.body != nil || lm.otherBody != trimmed {
		t.Fatalf("switched: %+v", lm)
	}
	if lm.switchPolicy != "" {
		t.Errorf("no images loaded: %q", lm.switchPolicy)
	}
	lm.store(whole, true, false)
	if lm.body != whole || lm.size() <= small {
		t.Errorf("both variants count: %d <= %d", lm.size(), small)
	}
	lm.showQuoted(false)
	if lm.body != trimmed || lm.otherBody != whole {
		t.Errorf("back without asking: %+v", lm)
	}
	// An answer for the variant left goes aside.
	allowed := quotedBody("a", true, false, api.RemoteAllow)
	lm.store(allowed, true, false)
	if lm.body != trimmed || lm.otherBody != allowed {
		t.Errorf("aside: %+v", lm)
	}
	// Under another policy the variant aside goes.
	trimmedAllowed := quotedBody("a", false, true, api.RemoteAllow)
	lm.store(trimmedAllowed, false, true)
	if lm.body != trimmedAllowed || lm.otherBody != nil {
		t.Errorf("replaced: %+v", lm)
	}
	lm.showQuoted(true)
	if lm.switchPolicy != api.RemoteAllow {
		t.Errorf("the images stay loaded in the whole body: %q", lm.switchPolicy)
	}
	// The request in flight follows its variant.
	lm.fetching = true
	lm.showQuoted(false)
	if lm.fetching || !lm.fetchingOther {
		t.Errorf("in flight: %v %v", lm.fetching, lm.fetchingOther)
	}
	if lm.bodyAnswered(true) || lm.fetchingOther {
		t.Error("the answer for the variant left")
	}
}
