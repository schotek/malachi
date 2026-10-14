// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The comment mode of a compose pane (ui/internal/jira compose.go,
// jira.CommentCompose): a pane opened for a comment draft
// (Params.Comment, draft.create reply on an account that comments) writes
// a comment on an issue. The header rows give way to a card with the
// issue (its key and summary, plain text from the site) and, on a
// service-desk request, the choice between a reply to the customer and an
// internal note; the pane's title (ReplyTitle, OnTitle) names the issue;
// the formatting toolbar keeps jira.CommentFormats; nothing attaches (no
// Attach button, Attach Files or Insert Image, files dropped on the
// editor are refused). There is no Save Draft either: no Drafts folder
// keeps a comment, its autosave is only against a crash and the saved
// copy goes with the pane unless it was sent (draft.go). The pane is
// pinned to the issue's account, which writes no mail and so is not in
// the From list (Manager.commentAccount).
// macOS: ComposeWindowController+Comment.swift, CommentHeaderView.swift.

// applyCommentMode sets the pane up for a comment; nothing for an e-mail.
// Called once from NewPane, after the actions and the toolbar are wired,
// so that it only takes away.
func (p *Pane) applyCommentMode() {
	c := p.params.Comment
	if c == nil {
		return
	}
	cw, _ := jira.CommentCompose(api.Draft{AccountID: p.params.AccountID, Comment: c}, i18n.Tr)
	p.headerRows.SetVisible(false)
	p.commentHeader.SetVisible(true)
	p.commentTitle.SetUseMarkup(false)
	p.commentTitle.SetLabel(cw.Title)
	summary := jira.Clean(c.Issue.Summary)
	p.commentSummary.SetUseMarkup(false)
	p.commentSummary.SetLabel(summary)
	p.commentSummary.SetVisible(summary != "")

	p.commentOptions = cw.Visibilities
	for _, o := range cw.Visibilities {
		t := adw.NewToggle()
		t.SetName(string(o.Visibility))
		t.SetLabel(o.Label)
		p.visibilityGroup.Add(t)
	}
	if len(cw.Visibilities) > 0 {
		p.visibilityGroup.SetActiveName(string(cw.Visibility))
		p.visibilityGroup.SetVisible(true)
		// Connected after the draft's choice is shown: only the user's
		// counts as an edit.
		p.visibilityGroup.NotifyProperty("active-name", p.dc.markDirty)
	}

	// Nothing attaches, nothing is kept as a draft; the menu items of
	// disabled actions hide (compose.blp hidden-when).
	if p.footerAttach != nil {
		p.footerAttach.SetVisible(false)
	}
	p.actions["attach"].SetEnabled(false)
	p.actions["save"].SetEnabled(false)
	p.actions["insert-image"].SetEnabled(jira.CommentAllows(jira.FormatImage))
	p.blockAction.SetEnabled(jira.CommentAllows(jira.FormatHeading))
	p.alignAction.SetEnabled(jira.CommentAllows(jira.FormatAlignment))
	// A dropped file is refused (editor.New), not imported.
	p.editor.OnDropFiles = nil
	p.restrictToolbar()
}

// toolbarFormats maps the controls of the formatting toolbar, by their ids
// in compose_pane.blp, to the format each applies.
var toolbarFormats = map[string]jira.Format{
	"bold_button":      jira.FormatBold,
	"italic_button":    jira.FormatItalic,
	"underline_button": jira.FormatUnderline,
	"block_button":     jira.FormatHeading,
	"align_button":     jira.FormatAlignment,
	"ul_button":        jira.FormatBulletList,
	"ol_button":        jira.FormatNumberedList,
	"quote_button":     jira.FormatQuote,
	"link_button":      jira.FormatLink,
	"color_button":     jira.FormatColour,
	"image_button":     jira.FormatImage,
	"clear_button":     jira.FormatClear,
}

// restrictToolbar keeps only the toolbar controls of jira.CommentFormats
// and the separators between groups that kept one (restrictedToolbar).
func (p *Pane) restrictToolbar() {
	var children []gtk.Widgetter
	var items []toolItem
	for c := p.toolbar.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		_, sep := c.(*gtk.Separator)
		children = append(children, c)
		items = append(items, toolItem{separator: sep, format: toolbarFormats[gtk.BaseWidget(c).BuildableID()]})
	}
	for i, shown := range restrictedToolbar(items) {
		gtk.BaseWidget(children[i]).SetVisible(shown)
	}
}

// toolItem is a child of the formatting toolbar: a separator, or a control
// of format (none: a control the comment mode leaves alone).
type toolItem struct {
	separator bool
	format    jira.Format
}

// restrictedToolbar says which of the toolbar's items show in comment
// mode: a control when a comment keeps its format (jira.CommentAllows), a
// separator only between two groups that kept a control. Formats without
// a control (code) change nothing. (macOS FormatToolbar.restrict.)
func restrictedToolbar(items []toolItem) []bool {
	shown := make([]bool, len(items))
	last := -1 // the last item shown
	for i, it := range items {
		switch {
		case it.separator:
			if last >= 0 && !items[last].separator {
				shown[i] = true
				last = i
			}
		case it.format == "" || jira.CommentAllows(it.format):
			shown[i] = true
			last = i
		}
	}
	if last >= 0 && items[last].separator {
		shown[last] = false
	}
	return shown
}

// chosenVisibility is the visibility of the option named active (the
// toggle group's active name); public when no option has that name, as
// without a choice.
func chosenVisibility(options []jira.VisibilityOption, active string) api.CommentVisibility {
	for _, o := range options {
		if string(o.Visibility) == active {
			return o.Visibility
		}
	}
	return api.CommentPublic
}

// wireComment is the comment draft.save is sent: the issue as it came and
// the visibility chosen (of a comment draft.save reads only the
// visibility); nil for an e-mail. Called by draftController.build.
func wireComment(c *api.DraftComment, v api.CommentVisibility) *api.DraftComment {
	if c == nil {
		return nil
	}
	return &api.DraftComment{Issue: c.Issue, Visibility: v}
}
