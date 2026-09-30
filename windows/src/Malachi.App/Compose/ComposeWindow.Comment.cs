// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeWindowController+Comment.swift
// and CommentHeaderView.swift; GTK: ui/internal/compose/comment.go
// (isComment, visibility, applyCommentMode) and compose.blp (comment_header).
// The comment mode of a compose window (Jira.CommentCompose): a window opened
// for a comment draft (ComposeParams.Comment, draft.create reply on an
// account that comments) writes a comment on an issue. The header fields
// give way to a card with the issue (its key and summary, plain text from the
// site) and, on a service-desk request, the choice between a reply to the
// customer and an internal note; the title names the issue; the formatting
// bar keeps Jira.CommentFormats (CommentMode.RestrictedToolbar); nothing
// attaches (no Attach button, Attach Files or Insert Image, files dropped on
// the editor are refused). There is no Save Draft either: no Drafts folder
// keeps a comment, its autosave is only against a crash and the saved copy
// goes with the window unless it was sent (ComposeDraftController). The
// window is pinned to the issue's account, which writes no mail and so is
// not in the From list (ComposeController.CommentAccount).

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Compose;

/// <summary>The comment mode of a compose window.</summary>
public sealed partial class ComposeWindow
{
    // The choices of who reads the comment (Jira.VisibilityOptions); none on
    // an issue without the choice and in an e-mail window.
    private IReadOnlyList<JiraVisibilityOption> commentOptions = [];

    // The header is showing the draft's choice: not an edit.
    private bool settingVisibility;

    /// <summary>comment.go <c>isComment</c>: the window writes a comment on an issue.</summary>
    public bool IsComment => parameters.Comment is not null;

    /// <summary>
    /// comment.go <c>visibility</c>: the visibility chosen in the header, the
    /// draft's at first (Jira.SelectedVisibility); public without a choice
    /// and for an e-mail.
    /// </summary>
    CommentVisibility IComposeForm.CommentVisibility =>
        commentOptions.Count == 0
            ? CommentVisibility.Public
            : CommentMode.ChosenVisibility(commentOptions, CommentVisibilityBar.SelectedItem?.Tag as string);

    // applyCommentMode: sets the window up for a comment; nothing for an
    // e-mail. Called once from the constructor, after the commands and the
    // bar are wired, so that it only takes away.
    private void ApplyCommentMode()
    {
        if (parameters.Comment is not { } c)
        {
            return;
        }
        Header.Visibility = Visibility.Collapsed;
        CommentHeader.Visibility = Visibility.Visible;
        CommentTitle.Text = Jira.CommentTitle(c.Issue.Key);
        var summary = Jira.Clean(c.Issue.Summary);
        CommentSummary.Text = summary;
        CommentSummary.Visibility = summary.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

        commentOptions = Jira.VisibilityOptions(c.Issue);
        var chosen = Jira.SelectedVisibility(c);
        settingVisibility = true;
        try
        {
            foreach (var o in commentOptions)
            {
                var item = new SelectorBarItem { Text = o.Label, Tag = o.Visibility.Value };
                ToolTipService.SetToolTip(item, o.Label);
                CommentVisibilityBar.Items.Add(item);
                if (o.Visibility == chosen)
                {
                    CommentVisibilityBar.SelectedItem = item;
                }
            }
        }
        finally
        {
            settingVisibility = false;
        }
        CommentVisibilityBar.Visibility = commentOptions.Count > 0 ? Visibility.Visible : Visibility.Collapsed;

        // Nothing attaches, nothing is kept as a draft: the draft menu keeps
        // Discard (compose.blp hidden-when of the disabled actions).
        AttachButton.Visibility = Visibility.Collapsed;
        AttachFilesItem.Visibility = Visibility.Collapsed;
        SaveDraftItem.Visibility = Visibility.Collapsed;
        InsertImageItem.Visibility = Jira.CommentAllows(JiraFormat.Image) ? Visibility.Visible : Visibility.Collapsed;
        DiscardSeparator.Visibility = InsertImageItem.Visibility;
        // A dropped file is refused (no copy cursor), not imported.
        EditorSlot.AllowDrop = false;
        FormatBar.RestrictToComment();
    }

    // Only the user's choice counts as an edit.
    private void OnCommentVisibilityChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (settingVisibility || closing || !IsComment)
        {
            return;
        }
        draft.MarkDirty();
    }
}
