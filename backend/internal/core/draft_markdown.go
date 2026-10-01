// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/markdown"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Markdown answers draft.markdown (docs/api.md §4.5): text pasted into the
// compose editor, rendered as HTML when it reads as Markdown. The HTML
// goes through the sanitiser in compose mode, as the editor's HTML does
// on draft.save, so only its output crosses the API.
func (s *draftService) Markdown(_ context.Context, p api.DraftMarkdownParams) (*api.DraftMarkdownResult, error) {
	if len(p.Text) > api.MaxDraftBodyBytes {
		return nil, api.NewError(api.CodeInvalidArgument, "text is over %d bytes", api.MaxDraftBodyBytes)
	}
	if !utf8.ValidString(p.Text) {
		return nil, api.NewError(api.CodeInvalidArgument, "text is not valid UTF-8")
	}
	rendered, ok := markdown.Render(p.Text)
	if !ok {
		return &api.DraftMarkdownResult{}, nil
	}
	out, err := s.b.Sanitize(sanitize.Input{
		HTML:          rendered,
		Mode:          sanitize.ModeCompose,
		Policy:        api.RemoteBlock,
		MaxOutputSize: api.MaxDraftBodyBytes,
	})
	if err != nil || out.HTML == "" {
		// Too big or too deep once rendered: the editor pastes the text.
		return &api.DraftMarkdownResult{}, nil //nolint:nilerr
	}
	return &api.DraftMarkdownResult{Markdown: true, HTML: out.HTML}, nil
}
