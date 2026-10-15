// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_draft.go (convDraftCard,
// newConvDraftCard, update, onHeader); macOS:
// MalachiMail/MessageView/ConversationDraftCardView.swift. A draft card of the conversation view
// (Core's ConversationDraftCard): the user's unsent reply to the
// conversation, on an issue the unsent comment, right under what opened the
// conversation (Conversation.DraftsFirst). A native card, never a web view:
// the header has the sender (the user), the red Draft badge (and the
// Internal one of an internal comment), when it was saved, Edit and
// Discard; the body is the draft's plain text. A double click on the
// header, the padding above it included, is Edit. Edit opens the draft as
// draft.get has it now in a compose window (a comment window for a comment
// draft), or raises the window editing it; Discard asks and deletes it
// (draft.delete), or raises that window instead (ActionsController
// EditThreadDraft, DiscardThreadDraft). The draft's avatar in the timeline
// is the user's (ConversationRow). Every text is set as plain text.

using System;
using System.Linq;
using Malachi.App.Controls;
using Malachi.App.Localization;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>One draft card of the conversation view.</summary>
internal sealed partial class ConversationDraftView : UserControl
{
    private readonly ConversationView pane;
    private readonly Grid header;
    private readonly StackPanel buttons;
    private readonly TextBlock senderLabel;
    private readonly IssuePill internalPill;
    private readonly TextBlock saved;
    private readonly TextBlock text;

    /// <summary>newConvDraftCard: the card of <paramref name="card"/> in <paramref name="pane"/>.</summary>
    public ConversationDraftView(ConversationView pane, ConversationDraftCard card, Func<string, Style> look)
    {
        this.pane = pane;
        Card = card;
        AutomationProperties.SetAutomationId(this, "ConversationDraft");

        // The header: the sender, the badges and the time on a line that
        // wraps, Edit and Discard at its trailing end.
        senderLabel = new TextBlock { Style = look("ConversationSenderStyle") };
        var badge = new IssuePill { IsDraft = true, Text = Core.Model.Conversation.DraftBadge() };
        internalPill = new IssuePill { IsInternal = true };
        saved = new TextBlock
        {
            Style = look("ConversationCaptionStyle"),
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
        };
        var flow = new WrapBox { ChildSpacing = 8, LineSpacing = 4 };
        flow.Children.Add(senderLabel);
        flow.Children.Add(badge);
        flow.Children.Add(internalPill);
        flow.Children.Add(saved);

        var edit = new Button { Style = look("ConversationCardButtonStyle"), Content = Core.Model.Conversation.DraftEditLabel() };
        AutomationProperties.SetAutomationId(edit, "ConversationDraftEdit");
        edit.Click += (_, _) => pane.Services.Router.EditThreadDraft(Card.Draft);
        var discard = new Button { Style = look("ConversationCardButtonStyle") };
        MnemonicLabel.SetText(discard, Core.Model.Conversation.DraftDiscardLabel());
        AutomationProperties.SetAutomationId(discard, "ConversationDraftDiscard");
        discard.Click += (_, _) => pane.Services.Router.DiscardThreadDraft(Card.Draft, pane.HostWindow);
        buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4, VerticalAlignment = VerticalAlignment.Top };
        buttons.Children.Add(edit);
        buttons.Children.Add(discard);

        header = new Grid { ColumnSpacing = 8, Margin = new Thickness(L.CardPaddingH - 1, L.CardPaddingV - 1, L.CardPaddingH - 1, 6) };
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(buttons, 1);
        header.Children.Add(flow);
        header.Children.Add(buttons);

        // The body: the draft's plain text, selectable.
        text = new TextBlock
        {
            Margin = new Thickness(L.CardPaddingH - 1, 2, L.CardPaddingH - 1, L.CardPaddingV - 1),
            IsTextSelectionEnabled = true,
            TextWrapping = TextWrapping.Wrap,
        };

        var root = new StackPanel();
        root.Children.Add(header);
        root.Children.Add(text);
        // Border is sealed in WinUI, so the card hosts its frame.
        Content = new Border { Style = look("ConversationDraftCardStyle"), Child = root };
        DoubleTapped += OnDoubleTapped;
        ApplyBodyFont();
        Update(card);
    }

    /// <summary>The card as last shown.</summary>
    public ConversationDraftCard Card { get; private set; }

    /// <summary>update: shows <paramref name="card"/> (a newer version of the draft, or the same).</summary>
    public void Update(ConversationDraftCard card)
    {
        Card = card;
        senderLabel.Text = card.Sender;
        internalPill.Text = card.Internal ? card.InternalLabel : "";
        if (card.Draft.UpdatedAt.IsGoZero)
        {
            saved.Text = "";
            saved.Visibility = Visibility.Collapsed;
        }
        else
        {
            saved.Text = Core.Model.Conversation.DraftSaved(Format.FormatTime(card.Draft.UpdatedAt));
            saved.Visibility = Visibility.Visible;
        }
        text.Text = card.Text;
        text.Visibility = card.Text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        // The card is read as whose draft it is, the badge and when.
        AutomationProperties.SetName(this, string.Join(", ", new[] { card.Sender, Core.Model.Conversation.DraftBadge(), saved.Text }.Where(t => t.Length > 0)));
    }

    /// <summary>The plain-text body's font follows the text-zoom and monospace settings, as a card's does.</summary>
    public void ApplyBodyFont()
    {
        text.FontSize = pane.BodyFontSize;
        if (pane.BodyFontFamily is { } mono)
        {
            text.FontFamily = mono;
        }
        else
        {
            text.ClearValue(TextBlock.FontFamilyProperty);
        }
    }

    // A double click on the header is Edit (convDraftCard's click gesture).
    private void OnDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        if (!OnHeader(e))
        {
            return;
        }
        e.Handled = true;
        pane.Services.Router.EditThreadDraft(Card.Draft);
    }

    // onHeader: from the card's top edge to the bottom of the header's line,
    // off its buttons (whose clicks are their own).
    private bool OnHeader(DoubleTappedRoutedEventArgs e)
    {
        if (e.GetPosition(header).Y >= header.ActualHeight)
        {
            return false;
        }
        for (var at = e.OriginalSource as DependencyObject; at is not null && !ReferenceEquals(at, this); at = VisualTreeHelper.GetParent(at))
        {
            if (at is ButtonBase || ReferenceEquals(at, buttons))
            {
                return false;
            }
        }
        return true;
    }
}
