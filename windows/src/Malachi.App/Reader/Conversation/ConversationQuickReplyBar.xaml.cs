// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/ConversationQuickReplyBar.swift
// (render, renderMode, modeLabel, modeIcon, setVisible with keyboardTarget,
// the actions and sendMessage:); GTK: ui/internal/window/quick_reply_bar.go
// (quickReplyBar: render, renderMode, buildMenu, the key controller and the
// input gate).
//
// The quick-reply bar of Mail's reading pane (docs/plans/chat-view.md §10–
// §12): under the page of a single message and under a conversation shown
// as chat, in the same place, the last row of the pane (MessageView in
// ReaderMode.Pane makes it; a message window has none). What
// ConversationQuickReplyBar.xaml lays out, filled from the controller.
// Everything it shows is QuickReplyController.State, read again on every
// Changed; the bar reports typing, the mode, Send, Open in a Window and
// Forward… to it and decides nothing. A hidden bar (State.Visible off) is
// collapsed and takes no room. A bar that hides while the keyboard is in it
// hands the keyboard to the view the pane shows (KeyboardTarget: the
// message's body, the conversation's layout), never leaving it on a
// collapsed control, where the window's single-key commands would act on
// the selection unseen.
//
// The mode button is a DropDownButton whose flyout has the reply modes the
// account offers as radio items and, after a separator, Forward…, which
// opens the compose window at once (GTK's menu model over qr.mode and
// qr.forward, macOS's pull-down). The flyout is built again only when the
// modes offered change (a rebuild would close it while open); the radio
// marks follow the mode on every render. On an issue the mode button gives
// way to a plain label, Comment with the Comment icon, and no flyout. Its
// tooltip names the sender of the message the reply goes to
// (Conversation.QuickReplyTargetTooltip); none when that is empty. In a
// narrow pane (ConversationLayout.QuickReplyIconOnly of the pane's width,
// which the pane passes on every change of it, SetPaneWidth, also while the
// bar is collapsed, so that it shows right the first time) the mode button
// shows its icon alone.
//
// Keys, in the field's PreviewKeyDown: Return is a new line (the TextBox
// accepts returns); Ctrl+Enter sends (the controller sends only while Send
// is enabled; the key is used either way, so it never adds a line); Escape
// gives the keyboard to the message list while the list is on screen, and
// is left alone otherwise. The window's single-key commands are off while
// the field has the keyboard, since it is a TextBox (KeyboardRouting), and
// Space types.
//
// While a Send is on its way the buttons are disabled, and the field keeps
// the keyboard and stays enabled — a disabled TextBox would let the focus go
// and with it the typing gate, so that the single keys would act on the
// list — but refuses every edit: BeforeTextChanging is cancelled for
// anything but the bar's own render (IsReadOnly would keep the focus as
// well, but a read-only TextBox drops its caret and its look says
// "disabled", which the brief state does not warrant), and Ctrl+Z and
// Ctrl+Y are swallowed, so that no undo step is left over from a refused
// edit. The buttons take no focus on a click (GTK SetFocusOnClick(false)):
// a click on Send would otherwise move the keyboard to a button that then
// disables itself.
//
// Rule 3: the only text from mail here is the target's sender in the mode
// button's tooltip (ConversationItem.Sender, cleaned to one line by the
// model), a plain string; every label is TextBlock.Text. What the user types
// becomes HTML in Core (Conversation.QuickReplyHtml, escaped), never here.
//
// The field reports its text through TextChanging, synchronously with each
// change (never for the bar's own render); Send, Open in a Window and
// Forward… hand the field's text to the controller once more first. The
// TextBox keeps line ends as CR; QuickReplyHtml normalises them.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Resources;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using L = Malachi.Core.Model.ConversationLayout;
using Rules = Malachi.Core.Model.Conversation;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Reader.Conversation;

/// <summary>The quick-reply bar of the reading pane: under a single message and under the chat layout of a conversation.</summary>
internal sealed partial class ConversationQuickReplyBar : UserControl
{
    // The radio group of the mode flyout's reply items.
    private const string ModeGroup = "QuickReplyMode";

    private readonly QuickReplyController controller;
    private readonly EventHandler onChanged;

    // The mode button shows its icon alone (a narrow pane).
    private bool iconOnly;

    // The modes the flyout was last built for (null before the first time).
    private string? menuModes;

    // A Send is on its way: the field refuses edits.
    private bool sending;

    // The bar sets the field's text itself (the gate lets it through).
    private bool rendering;

    private bool closed;

    /// <summary>The bar over <paramref name="controller"/>, which it renders and reports to.</summary>
    public ConversationQuickReplyBar(QuickReplyController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        InitializeComponent();

        CommentGlyph.Glyph = Icons.Glyph("chat-message-new");
        AssistantGlyph.Glyph = Icons.Glyph("malachi-assistant");
        OpenGlyph.Glyph = Icons.Glyph("window-new");
        SendGlyph.Glyph = Icons.Glyph("mail-send");

        // The toolbar's ✦: its tooltip and accessible name.
        var assistant = Malachi.Core.Assistants.Assistant.Texts().Assistant;
        ToolTipService.SetToolTip(AssistantButton, assistant);
        AutomationProperties.SetName(AssistantButton, assistant);
        var open = Rules.QuickReplyOpenWindowLabel();
        ToolTipService.SetToolTip(OpenButton, open);
        AutomationProperties.SetName(OpenButton, open);
        ToolTipService.SetToolTip(SendButton, L10n.T("Send (Ctrl+Enter)"));
        AutomationProperties.SetName(SendButton, Mnemonic.Strip(L10n.T("_Send")));

        // GTK SetFocusOnClick(false): a click leaves the keyboard where it was.
        ModeButton.AllowFocusOnInteraction = false;
        AssistantButton.AllowFocusOnInteraction = false;
        OpenButton.AllowFocusOnInteraction = false;
        SendButton.AllowFocusOnInteraction = false;

        // TextChanging, not TextChanged: it runs synchronously with the
        // change, so a render that sets the field (another conversation's
        // text) is never followed by a late report of the text before it.
        Field.TextChanging += (_, _) =>
        {
            if (!closed && !rendering)
            {
                controller.SetText(Field.Text);
            }
        };
        Field.BeforeTextChanging += (_, e) =>
        {
            if (sending && !rendering)
            {
                e.Cancel = true;
            }
        };
        Field.PreviewKeyDown += OnFieldKeyDown;
        AssistantButton.Click += (_, _) => OpenAssistant?.Invoke(AssistantButton);
        OpenButton.Click += (_, _) =>
        {
            Report();
            controller.OpenWindow();
        };
        SendButton.Click += (_, _) => Send();

        onChanged = (_, _) => Render();
        controller.Changed += onChanged;
        Render();
    }

    /// <summary>Escape in the field: the message list takes the keyboard (nothing when it is not on screen).</summary>
    public Action? FocusList { get; set; }

    /// <summary>Whether the message list is on screen, so that Escape can give it the keyboard.</summary>
    public Func<bool>? ListOnScreen { get; set; }

    /// <summary>
    /// The ✦ button, with itself to show a menu over: what the toolbar's ✦
    /// does (the main window's <c>ShowAssistantFrom</c>).
    /// </summary>
    public Action<FrameworkElement>? OpenAssistant { get; set; }

    /// <summary>
    /// The bar hid while the keyboard was in it: the pane gives the keyboard
    /// to the view it shows (macOS <c>keyboardTarget</c>).
    /// </summary>
    public Action? KeyboardTarget { get; set; }

    /// <summary>The ✦ button shows: the Assistant is shown (<see cref="AssistantController.Shown"/>, as the toolbar's ✦).</summary>
    public bool AssistantShown
    {
        get => AssistantButton.Visibility == Visibility.Visible;
        set => AssistantButton.Visibility = value ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>
    /// A UserControl has no automation peer of its own: the bar is a group
    /// named QuickReplyBar for UI Automation, as the chat's layouts are panes
    /// (ConversationGroupPeer).
    /// </summary>
    protected override AutomationPeer OnCreateAutomationPeer() =>
        new ConversationGroupPeer(this, nameof(ConversationQuickReplyBar), AutomationControlType.Group);

    /// <summary>
    /// The reading pane is <paramref name="width"/> wide (its SizeChanged):
    /// the mode button shows its icon alone in a narrow pane
    /// (<see cref="Malachi.Core.Model.ConversationLayout.QuickReplyIconOnly"/>).
    /// </summary>
    public void SetPaneWidth(double width)
    {
        var narrow = L.QuickReplyIconOnly(width);
        if (narrow == iconOnly)
        {
            return;
        }
        iconOnly = narrow;
        Render();
    }

    /// <summary>The bar stops following the controller (the window closes).</summary>
    public void Close()
    {
        closed = true;
        controller.Changed -= onChanged;
    }

    // Shows the controller's state.
    private void Render()
    {
        if (closed)
        {
            return;
        }
        var s = controller.State;
        // Hiding with the keyboard in it: the pane's view takes it once the
        // bar is drawn (below).
        var handOff = !s.Visible && Visibility == Visibility.Visible && OwnsFocus();
        Visibility = s.Visible ? Visibility.Visible : Visibility.Collapsed;
        // The gate first: the text of a Send that went through is cleared
        // in the same change that ends it.
        sending = s.Sending;
        if (!string.Equals(Field.Text, s.Text, StringComparison.Ordinal))
        {
            rendering = true;
            try
            {
                Field.Text = s.Text;
                Field.SelectionStart = Field.Text.Length;
            }
            finally
            {
                rendering = false;
            }
        }
        var placeholder = Rules.QuickReplyPlaceholder(s.Comment);
        Field.PlaceholderText = placeholder;
        AutomationProperties.SetName(Field, placeholder);
        ModeButton.IsEnabled = !s.Sending;
        OpenButton.IsEnabled = !s.Sending;
        SendButton.IsEnabled = !s.Blank && !s.Sending;
        RenderMode(s);
        if (handOff)
        {
            KeyboardTarget?.Invoke();
        }
    }

    // Whether the keyboard is in the bar (its field or a button).
    private bool OwnsFocus()
    {
        if (XamlRoot is null || FocusManager.GetFocusedElement(XamlRoot) is not DependencyObject focused)
        {
            return false;
        }
        for (var e = focused; e is not null; e = VisualTreeHelper.GetParent(e))
        {
            if (ReferenceEquals(e, this))
            {
                return true;
            }
        }
        return false;
    }

    // The mode button, or on an issue the Comment label.
    private void RenderMode(QuickReplyState s)
    {
        var tooltip = Rules.QuickReplyTargetTooltip(s.Mode, s.Target?.Sender ?? "");
        object? tip = tooltip.Length == 0 ? null : tooltip;
        ModeButton.Visibility = s.Comment ? Visibility.Collapsed : Visibility.Visible;
        CommentBox.Visibility = s.Comment ? Visibility.Visible : Visibility.Collapsed;
        if (s.Comment)
        {
            var commentLabel = Jira.ReplyLabel(comment: true);
            CommentText.Text = commentLabel;
            CommentText.Visibility = iconOnly ? Visibility.Collapsed : Visibility.Visible;
            ToolTipService.SetToolTip(CommentBox, tip);
            AutomationProperties.SetName(CommentBox, commentLabel);
            AutomationProperties.SetHelpText(CommentBox, tooltip);
            return;
        }
        var label = ModeLabel(s.Mode);
        ModeGlyph.Glyph = ModeGlyphOf(s.Mode);
        ModeText.Text = label;
        ModeText.Visibility = iconOnly ? Visibility.Collapsed : Visibility.Visible;
        ToolTipService.SetToolTip(ModeButton, tip);
        AutomationProperties.SetName(ModeButton, label);
        AutomationProperties.SetHelpText(ModeButton, tooltip);
        var modes = string.Join(",", s.Modes);
        if (!string.Equals(modes, menuModes, StringComparison.Ordinal))
        {
            menuModes = modes;
            BuildMenu(s.Modes);
        }
        foreach (var item in ModeMenu.Items.OfType<RadioMenuFlyoutItem>())
        {
            item.IsChecked = item.Tag is QuickReplyMode m && m == s.Mode;
        }
    }

    // The flyout: the reply modes as radio items, then Forward….
    private void BuildMenu(IReadOnlyList<QuickReplyMode> modes)
    {
        ModeMenu.Items.Clear();
        var replies = modes.Where(m => m != QuickReplyMode.Forward).ToList();
        foreach (var mode in replies)
        {
            var item = new RadioMenuFlyoutItem
            {
                Text = ModeLabel(mode),
                GroupName = ModeGroup,
                Tag = mode,
                Icon = new FontIcon { Glyph = ModeGlyphOf(mode) },
            };
            AutomationProperties.SetAutomationId(item, mode == QuickReplyMode.ReplyAll ? "QuickReplyModeReplyAll" : "QuickReplyModeReply");
            item.Click += (_, _) => controller.SetMode(mode);
            ModeMenu.Items.Add(item);
        }
        if (modes.Contains(QuickReplyMode.Forward))
        {
            if (replies.Count > 0)
            {
                ModeMenu.Items.Add(new MenuFlyoutSeparator());
            }
            var forward = new MenuFlyoutItem
            {
                Text = Rules.QuickReplyForwardLabel(),
                Icon = new FontIcon { Glyph = ModeGlyphOf(QuickReplyMode.Forward) },
            };
            AutomationProperties.SetAutomationId(forward, "QuickReplyForward");
            forward.Click += (_, _) =>
            {
                Report();
                controller.Forward();
            };
            ModeMenu.Items.Add(forward);
        }
    }

    // The field's keys: Ctrl+Enter sends, Escape goes to the list, and no
    // undo while a Send is on its way.
    private void OnFieldKeyDown(object sender, KeyRoutedEventArgs e)
    {
        switch (e.Key)
        {
            case WinUIKey.Enter when IsDown(WinUIKey.Control):
                e.Handled = true;
                Send();
                break;
            case WinUIKey.Z or WinUIKey.Y when sending && IsDown(WinUIKey.Control):
                e.Handled = true;
                break;
            case WinUIKey.Escape when ListOnScreen?.Invoke() == true:
                e.Handled = true;
                FocusList?.Invoke();
                break;
        }
    }

    // Send: the controller sends only with something to send and nothing on
    // its way.
    private void Send()
    {
        Report();
        controller.Send();
    }

    // The field's text as it is now, once more before an action.
    private void Report()
    {
        if (!sending)
        {
            controller.SetText(Field.Text);
        }
    }

    private static bool IsDown(WinUIKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(Windows.UI.Core.CoreVirtualKeyStates.Down);

    private static string ModeLabel(QuickReplyMode mode) => mode switch
    {
        QuickReplyMode.ReplyAll => L10n.T("Reply All"),
        QuickReplyMode.Forward => Rules.QuickReplyForwardLabel(),
        _ => L10n.T("Reply"),
    };

    private static string ModeGlyphOf(QuickReplyMode mode) => mode switch
    {
        QuickReplyMode.ReplyAll => Icons.Glyph("mail-reply-all"),
        QuickReplyMode.Forward => Icons.Glyph("mail-forward"),
        _ => Icons.Glyph("mail-reply-sender"),
    };
}
