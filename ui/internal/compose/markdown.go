// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import "github.com/schotek/malachi/backend/pkg/api"

// pasteMarkdown is the editor's OnPaste: the daemon renders pasted text
// that reads as Markdown (draft.markdown) and answer gets its sanitised
// HTML; anything else (not Markdown, an error, an older daemon without
// the method) answers "", so the editor pastes the text as it is.
func (p *Pane) pasteMarkdown(text string, answer func(html string)) {
	p.rpc(func() (any, error) {
		var res api.DraftMarkdownResult
		err := p.m.client.Call(p.ctx(), api.MethodDraftMarkdown, api.DraftMarkdownParams{Text: text}, &res)
		return res, err
	}, func(v any, err error) {
		res, _ := v.(api.DraftMarkdownResult)
		if err != nil {
			p.m.log.Debug("markdown paste", "err", err)
		}
		if err != nil || !res.Markdown {
			answer("")
			return
		}
		answer(res.HTML)
	})
}
