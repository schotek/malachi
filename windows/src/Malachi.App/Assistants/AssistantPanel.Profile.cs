// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantProposalView.swift and
// the profile button of AssistantPanelViewController.swift / AssistantPanelHost.swift;
// GTK: ui/internal/window/assistant_panel_profile.go. A note the assistant
// proposed for the user's profile (ProposalContent, docs/plans/
// assistant-profile.md P16) is a card: "The assistant suggests remembering:",
// the note as plain text read from the ProfileStore (never from the model's
// answer), the account it is for, and Save, Edit… and Dismiss. Saved, it
// says "Saved to your profile" without buttons; dismissed, it goes; a
// proposal the store no longer has (resolved in the preferences or another
// window) keeps the card without buttons. The header's profile button opens
// Preferences → AI with the number of waiting proposals in its tooltip; it
// is collapsed while the daemon has no profile.
//
// Windows differences: the panel has no menu, so the button stands in the
// header like macOS's; a refused resolve only gives the buttons back (no
// beep helper exists here, and no msgid for the error).

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.App.Resources;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Assistants;

/// <summary>The panel's proposal cards and its profile button.</summary>
public sealed partial class AssistantPanel
{
    private readonly List<ProposalRow> proposalRows = [];
    private ProfileStore? profileStore;
    private Func<string, Task<string?>>? editProposal;
    private Action? openProfile;

    /// <summary>
    /// The application's profile, for the proposal cards and the header's
    /// button; <paramref name="edit"/> asks for a proposal's changed text,
    /// <paramref name="open"/> opens Preferences → AI. Before
    /// <see cref="Attach"/>.
    /// </summary>
    internal void AttachProfile(ProfileStore store, Func<string, Task<string?>> edit, Action open)
    {
        ArgumentNullException.ThrowIfNull(store);
        profileStore = store;
        editProposal = edit;
        openProfile = open;
        store.Changed += OnProfileChanged;
        ProfileButton.Click += (_, _) => openProfile?.Invoke();
        UpdateProfileButton();
    }

    /// <summary>Stops following the profile (the panel goes).</summary>
    internal void DetachProfile()
    {
        if (profileStore is { } s)
        {
            s.Changed -= OnProfileChanged;
        }
        profileStore = null;
    }

    private void OnProfileChanged(object? sender, EventArgs e)
    {
        UpdateProfileButton();
        foreach (var row in proposalRows)
        {
            row.Refresh();
        }
    }

    private void UpdateProfileButton()
    {
        var s = profileStore;
        ProfileButton.Visibility = s is { Supported: true } ? Visibility.Visible : Visibility.Collapsed;
        var tip = Assistant.ProfileProposalCount(s?.Proposals.Count ?? 0);
        SetTip(ProfileButton, tip);
    }

    private ProposalRow MakeProposalRow()
    {
        var row = new ProposalRow(this);
        proposalRows.Add(row);
        return row;
    }

    // A note the assistant proposed: see the file's header.
    private sealed class ProposalRow : Row
    {
        private enum Outcome
        {
            Pending,
            Saved,
            Dismissed,
        }

        private readonly AssistantPanel panel;
        private readonly Border card;
        private readonly TextBlock title = PlainLabel("DimCaptionLabelStyle");
        private readonly TextBlock text = PlainLabel("BodyLabelStyle");
        private readonly TextBlock detail = PlainLabel("DimCaptionLabelStyle");
        private readonly StackPanel buttons = new() { Orientation = Orientation.Horizontal, Spacing = 6 };
        private readonly Button save = new();
        private readonly Button edit = new();
        private readonly Button dismiss = new();
        private string id = "";
        private Outcome outcome;
        private bool busy;

        public ProposalRow(AssistantPanel panel)
        {
            this.panel = panel;
            var t = Assistant.ProfileTexts();
            title.Text = t.ProposalTitle;
            save.Content = t.Save;
            edit.Content = t.Edit;
            dismiss.Content = t.Dismiss;
            AutomationProperties.SetAutomationId(save, "AssistantProposalSave");
            AutomationProperties.SetAutomationId(edit, "AssistantProposalEdit");
            AutomationProperties.SetAutomationId(dismiss, "AssistantProposalDismiss");
            save.Click += (_, _) => _ = ResolveAsync(accept: true, null);
            dismiss.Click += (_, _) => _ = ResolveAsync(accept: false, null);
            edit.Click += (_, _) => _ = EditAsync();
            buttons.Children.Add(save);
            buttons.Children.Add(edit);
            buttons.Children.Add(dismiss);
            var head = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
            head.Children.Add(new FontIcon { Glyph = Icons.Glyph("avatar-default-symbolic"), FontSize = 16, VerticalAlignment = VerticalAlignment.Center });
            head.Children.Add(title);
            var column = new StackPanel { Spacing = 6 };
            column.Children.Add(head);
            column.Children.Add(text);
            column.Children.Add(detail);
            column.Children.Add(buttons);
            card = new Border
            {
                Child = column,
                Padding = new Thickness(12, 8, 12, 8),
                CornerRadius = new CornerRadius(8),
                BorderThickness = new Thickness(1),
                Background = (Brush)Application.Current.Resources["CardBackgroundFillColorDefaultBrush"],
                BorderBrush = (Brush)Application.Current.Resources["CardStrokeColorDefaultBrush"],
            };
            AutomationProperties.SetAutomationId(card, "AssistantProposal");
        }

        public override UIElement Root => card;

        public override void Update(AssistantPanelContent content)
        {
            if (content is not ProposalContent c || c.Proposal == id)
            {
                return;
            }
            id = c.Proposal;
            outcome = Outcome.Pending;
            Refresh();
        }

        public void Refresh()
        {
            var t = Assistant.ProfileTexts();
            card.Visibility = outcome == Outcome.Dismissed ? Visibility.Collapsed : Visibility.Visible;
            var store = panel.profileStore;
            if (outcome != Outcome.Pending || store?.Proposal(id) is not { } q)
            {
                buttons.Visibility = Visibility.Collapsed;
                detail.Text = outcome == Outcome.Saved ? t.ProposalOK : "";
                detail.Visibility = outcome == Outcome.Saved ? Visibility.Visible : Visibility.Collapsed;
                return;
            }
            text.Text = q.Text;
            detail.Text = ProfileDialogs.AccountLine(store.Profile, q.AccountId?.Value);
            detail.Visibility = detail.Text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
            buttons.Visibility = Visibility.Visible;
            save.IsEnabled = edit.IsEnabled = dismiss.IsEnabled = !busy;
        }

        private async Task EditAsync()
        {
            if (busy || panel.profileStore?.Proposal(id) is not { } q || panel.editProposal is not { } ask)
            {
                return;
            }
            var edited = await ask(q.Text);
            if (edited is not null)
            {
                await ResolveAsync(accept: true, edited);
            }
        }

        private async Task ResolveAsync(bool accept, string? edited)
        {
            if (busy || panel.profileStore is not { } store)
            {
                return;
            }
            busy = true;
            Refresh();
            var error = await store.ResolveAsync(id, accept, edited);
            busy = false;
            if (error is null)
            {
                outcome = accept ? Outcome.Saved : Outcome.Dismissed;
            }
            Refresh();
        }
    }
}
