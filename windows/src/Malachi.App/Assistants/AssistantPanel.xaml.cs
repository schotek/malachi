// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantPanelViewController.swift,
// AssistantTranscriptViews.swift and AssistantInputView.swift; GTK:
// ui/internal/window/assistant_panel.go (newAssistantPanel, updateState,
// contextIcon, updateSend, bindInput, submit, sendOrStop, applyChange,
// rebuild, the rows, bindScrolling, stickToEnd). The panel renders an
// AssistantPanelController and sends the clicks back; the host (the main
// window) gives the controller its hooks (consent, the context's members,
// Open Draft) and this panel the way a link of an answer is opened (after
// "Open This Link?") and the way a page of the application's own is (Get
// Claude Code…). Everything the model or mail wrote is TextBlock.Text or a
// Run set from code (AnswerRenderer), never markup.
//
// Windows differences: Return sends and Shift+Return starts a new line in a
// TextBox that accepts returns (GTK's text view the same); the user's
// question is a tinted card like GTK's .assistant-user bubble; the activity
// line's spinner is a ProgressRing.

using System;
using System.Collections.Generic;
using Malachi.App.Resources;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Assistants;

/// <summary>The assistant panel of the main window (assistant_panel.blp).</summary>
public sealed partial class AssistantPanel : UserControl
{
    // assistant_panel.go quickActions: the panel's buttons, in order.
    private static readonly AssistantAction[] QuickActions = [AssistantAction.Summarize, AssistantAction.DraftReply, AssistantAction.Tasks];

    // How close to its end the transcript counts as at its end (GTK 24 px).
    private const double EndSlack = 24;

    private readonly List<Button> actionButtons = [];
    private readonly List<Row> rows = [];
    private AssistantPanelController? controller;
    private Action<string>? openLink;
    private Action<string>? openPage;

    // The transcript was at its end and stays there as items arrive; a user
    // who scrolled up to read is left where they are.
    private bool followsEnd = true;

    /// <summary>The panel with its fixed texts; <see cref="Attach"/> gives it its controller.</summary>
    public AssistantPanel()
    {
        InitializeComponent();
        var texts = Assistant.Texts();
        var panel = Assistant.PanelTexts();
        TitleLabel.Text = texts.Assistant;
        AutomationProperties.SetName(this, texts.Assistant);
        SetTip(NewButton, panel.NewConversation);
        SetTip(ChipRemove, L10n.T("Remove"));
        foreach (var a in QuickActions)
        {
            var button = new Button { Content = Assistant.Label(a) };
            button.Click += (_, _) => controller?.Run(a);
            Actions.Children.Add(button);
            actionButtons.Add(button);
        }
        BarLabel.Text = panel.AnotherSelected;
        BarNew.Content = panel.NewConversation;
        BarAdd.Content = panel.AddToConversation;
        SetTip(PendingCancel, Mnemonic.Strip(L10n.T("_Cancel")));
        Footer.Text = panel.Footer;
        AutomationProperties.SetName(Input, panel.Placeholder);

        NewButton.Click += (_, _) => controller?.NewConversation();
        ChipRemove.Click += (_, _) => controller?.RemoveContext();
        BarNew.Click += (_, _) => controller?.NewConversation();
        BarAdd.Click += (_, _) => controller?.AddSelection();
        PendingCancel.Click += (_, _) => controller?.CancelPending();
        SendButton.Click += (_, _) => SendOrStop();
        Input.TextChanged += (_, _) => UpdateSend();
        Input.PreviewKeyDown += OnInputKeyDown;
        Scroller.ViewChanged += (_, _) => followsEnd = Scroller.ScrollableHeight - Scroller.VerticalOffset <= EndSlack;
        Transcript.SizeChanged += (_, _) => StickToEnd();
    }

    /// <summary>
    /// Renders <paramref name="panelController"/>; a link of an answer goes to
    /// <paramref name="linkOpener"/> (which asks before it opens anything), a
    /// page the application itself names (Get Claude Code…) to
    /// <paramref name="pageOpener"/>.
    /// </summary>
    public void Attach(AssistantPanelController panelController, Action<string> linkOpener, Action<string> pageOpener)
    {
        ArgumentNullException.ThrowIfNull(panelController);
        ArgumentNullException.ThrowIfNull(linkOpener);
        ArgumentNullException.ThrowIfNull(pageOpener);
        controller = panelController;
        openLink = linkOpener;
        openPage = pageOpener;
        panelController.Changed += (_, change) => ApplyChange(change);
        panelController.StateChanged += (_, _) => UpdateState();
        panelController.FocusInputRequested += (_, _) =>
            // After the panel has unfolded (a message action opened it).
            DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, FocusInput);
        panelController.RestoreInputRequested += (_, text) =>
        {
            if (Input.Text.Length == 0)
            {
                Input.Text = text;
            }
        };
        Rebuild();
        UpdateState();
    }

    /// <summary>The question field takes the keyboard.</summary>
    public void FocusInput() => Input.Focus(FocusState.Programmatic);

    private static void SetTip(Button button, string text)
    {
        ToolTipService.SetToolTip(button, text);
        AutomationProperties.SetName(button, text);
    }

    // State (assistant_panel.go updateState)

    private void UpdateState()
    {
        if (controller is not { } c)
        {
            return;
        }
        SubtitleLabel.Text = c.Subtitle;
        ChipLabel.Text = c.ContextLabel;
        ToolTipService.SetToolTip(ChipLabel, c.ContextLabel);
        if (c.IsPinned)
        {
            var pinned = c.PinnedContexts;
            ChipIcon.Glyph = Icons.Glyph(pinned.Count == 1 ? ContextIcon(pinned[0].Context) : "view-list-symbolic");
            ChipRemove.Visibility = Visibility.Collapsed;
        }
        else
        {
            ChipIcon.Glyph = Icons.Glyph(ContextIcon(c.EffectiveContext));
            ChipRemove.Visibility = c.EffectiveContext is null ? Visibility.Collapsed : Visibility.Visible;
        }
        Bar.Visibility = c.AnotherSelected ? Visibility.Visible : Visibility.Collapsed;
        foreach (var b in actionButtons)
        {
            b.IsEnabled = c.CanRunActions;
        }
        var label = c.PendingLabel;
        PendingLabel.Text = label;
        PendingRow.Visibility = label.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        Input.PlaceholderText = c.Placeholder;
        NewButton.IsEnabled = !c.IsClosed && (c.Items.Count > 0 || c.IsRunning || c.Pending is not null || c.IsPinned);
        UpdateSend();
    }

    // The chip's icon of one context: all mail, a conversation, a message.
    private static string ContextIcon(AssistantPanelController.Context? context) => context switch
    {
        null => "mail-inbox-symbolic",
        { Conversation: true } => "mail-message-new-symbolic",
        _ => "mail-unread-symbolic",
    };

    // Send, or Stop while an answer comes.
    private void UpdateSend()
    {
        if (controller is not { } c)
        {
            SendButton.IsEnabled = false;
            return;
        }
        if (c.IsRunning)
        {
            SendButton.Content = Assistant.PanelTexts().Stop;
            SendButton.Style = (Style)Application.Current.Resources["DefaultButtonStyle"];
            SendButton.IsEnabled = !c.IsClosed;
            return;
        }
        SendButton.Content = Mnemonic.Strip(L10n.T("_Send"));
        SendButton.Style = (Style)Application.Current.Resources["AccentButtonStyle"];
        var draft = c.Pending is PendingAction { Action: AssistantAction.DraftReply };
        var enabled = !c.IsClosed && (Input.Text.Trim().Length > 0 || draft);
        // Stop clicked becomes a disabled Send: the keyboard stays in the
        // panel, in the question field, rather than going to the window's
        // first control.
        if (!enabled && SendButton.FocusState != FocusState.Unfocused)
        {
            Input.Focus(FocusState.Programmatic);
        }
        SendButton.IsEnabled = enabled;
    }

    // The question field (assistant_panel.go bindInput)

    // Return sends, Shift+Return starts a new line, Escape drops a waiting
    // message action.
    private void OnInputKeyDown(object sender, KeyRoutedEventArgs e)
    {
        switch (e.Key)
        {
            case WinUIKey.Enter:
                var shift = Microsoft.UI.Input.InputKeyboardSource.GetKeyStateForCurrentThread(WinUIKey.Shift)
                    .HasFlag(Windows.UI.Core.CoreVirtualKeyStates.Down);
                var alt = Microsoft.UI.Input.InputKeyboardSource.GetKeyStateForCurrentThread(WinUIKey.Menu)
                    .HasFlag(Windows.UI.Core.CoreVirtualKeyStates.Down);
                if (shift || alt)
                {
                    return;
                }
                e.Handled = true;
                Submit();
                break;
            case WinUIKey.Escape when controller?.Pending is not null:
                e.Handled = true;
                controller.CancelPending();
                break;
        }
    }

    // The controller takes the text (the field empties) or leaves it (a
    // question under way, nothing typed).
    private void Submit()
    {
        if (controller?.Submit(Input.Text) == true)
        {
            Input.Text = "";
        }
    }

    private void SendOrStop()
    {
        if (controller is not { } c)
        {
            return;
        }
        if (c.IsRunning)
        {
            c.Stop();
            return;
        }
        Submit();
    }

    // The transcript (assistant_panel.go applyChange)

    private void ApplyChange(AssistantPanelController.Change change)
    {
        if (controller is not { } c)
        {
            return;
        }
        var items = c.Items;
        switch (change.Kind)
        {
            case AssistantPanelController.ChangeKind.Reset:
                Rebuild();
                followsEnd = true;
                break;
            case AssistantPanelController.ChangeKind.Appended:
                if (change.Index == rows.Count && change.Index < items.Count)
                {
                    AppendRow(items[change.Index]);
                }
                else
                {
                    Rebuild();
                }
                break;
            case AssistantPanelController.ChangeKind.Updated:
                if (change.Index >= items.Count || rows.Count != items.Count)
                {
                    Rebuild();
                    break;
                }
                var item = items[change.Index];
                var row = rows[change.Index];
                if (row.Kind == item.Content.GetType())
                {
                    row.Update(item.Content);
                }
                else
                {
                    ReplaceRow(change.Index, item);
                }
                break;
        }
        UpdateState();
        StickToEnd();
    }

    private void Rebuild()
    {
        Transcript.Children.Clear();
        rows.Clear();
        if (controller is { } c)
        {
            foreach (var item in c.Items)
            {
                AppendRow(item);
            }
        }
    }

    private void AppendRow(AssistantPanelController.Item item)
    {
        var row = MakeRow(item);
        Transcript.Children.Add(row.Root);
        rows.Add(row);
    }

    private void ReplaceRow(int index, AssistantPanelController.Item item)
    {
        var row = MakeRow(item);
        Transcript.Children[index] = row.Root;
        rows[index] = row;
    }

    // Scrolls to the end after the layout, while following it.
    private void StickToEnd()
    {
        if (!followsEnd)
        {
            return;
        }
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, () =>
        {
            if (followsEnd)
            {
                Scroller.ChangeView(null, Scroller.ScrollableHeight, null, disableAnimation: true);
            }
        });
    }

    private Row MakeRow(AssistantPanelController.Item item)
    {
        var id = item.Id;
        Row row = item.Content switch
        {
            UserContent => new UserRow(),
            AnswerContent => new AnswerRow(href => openLink?.Invoke(href)),
            ActivityContent => new ActivityRow(),
            DraftContent => new DraftRow(() => controller?.OpenDraftItem(id)),
            _ => new LineRow(() => controller?.Retry(id), offer => Offered(id, offer)),
        };
        row.Kind = item.Content.GetType();
        row.Update(item.Content);
        return row;
    }

    /// <summary>The button of what an error line, or the settings' Claude Code row, offers; "" for nothing.</summary>
    internal static string OfferLabel(ErrorOffer offer) => offer switch
    {
        ErrorOffer.SignIn => Assistant.SignInTexts().SignIn,
        ErrorOffer.Install => Assistant.SignInTexts().GetClaudeCode,
        _ => "",
    };

    // The other button of an error line: Sign In… runs Claude Code's own
    // sign-in and sends the question again (the controller's), Get Claude
    // Code… opens Anthropic's page with the installers.
    private void Offered(int id, ErrorOffer offer)
    {
        switch (offer)
        {
            case ErrorOffer.SignIn:
                controller?.SignIn(id);
                break;
            case ErrorOffer.Install:
                openPage?.Invoke(Assistant.InstallUrl);
                break;
            default:
                break;
        }
    }

    // A plain-text label for model or mail text: never markup.
    private static TextBlock PlainLabel(string style) => new()
    {
        Style = (Style)Application.Current.Resources[style],
        TextWrapping = TextWrapping.WrapWholeWords,
    };

    // One item's widget and how it follows its content.
    private abstract class Row
    {
        public Type Kind { get; set; } = typeof(object);

        public abstract UIElement Root { get; }

        public abstract void Update(AssistantPanelContent content);
    }

    // The user's question: a tinted card with the action's label over the
    // typed text, indented from the leading edge.
    private sealed class UserRow : Row
    {
        private readonly Border card;
        private readonly TextBlock label = PlainLabel("DimCaptionLabelStyle");
        private readonly TextBlock text = PlainLabel("BodyLabelStyle");

        public UserRow()
        {
            text.IsTextSelectionEnabled = true;
            var column = new StackPanel { Spacing = 2 };
            column.Children.Add(label);
            column.Children.Add(text);
            card = new Border
            {
                Child = column,
                Margin = new Thickness(28, 0, 0, 0),
                Padding = new Thickness(10, 6, 10, 6),
                CornerRadius = new CornerRadius(8),
                Background = (Brush)Application.Current.Resources["SubtleFillColorSecondaryBrush"],
            };
        }

        public override UIElement Root => card;

        public override void Update(AssistantPanelContent content)
        {
            if (content is not UserContent c)
            {
                return;
            }
            label.Text = c.Label;
            label.Visibility = c.Label.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
            text.Text = c.Text;
            text.Visibility = c.Text.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        }
    }

    // An answer: a read-only rich text block that draws its Markdown; a
    // click on a link goes through the confirmation.
    private sealed class AnswerRow(Action<string> openLink) : Row
    {
        private readonly RichTextBlock view = new() { IsTextSelectionEnabled = true, TextWrapping = TextWrapping.Wrap };
        private string? shown;

        public override UIElement Root => view;

        public override void Update(AssistantPanelContent content)
        {
            if (content is not AnswerContent c || c.Text == shown)
            {
                return;
            }
            shown = c.Text;
            AnswerRenderer.Render(view, c.Text, openLink);
        }
    }

    // A tool at work: a spinner, then a check mark, beside its label.
    private sealed class ActivityRow : Row
    {
        private readonly StackPanel box = new() { Orientation = Orientation.Horizontal, Spacing = 6 };
        private readonly ProgressRing spinner = new() { Width = 14, Height = 14, IsActive = true };
        private readonly FontIcon check = new() { Glyph = Icons.Glyph("object-select-symbolic"), FontSize = 12 };
        private readonly TextBlock label = PlainLabel("DimCaptionLabelStyle");

        public ActivityRow()
        {
            check.Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"];
            box.Children.Add(spinner);
            box.Children.Add(check);
            box.Children.Add(label);
        }

        public override UIElement Root => box;

        public override void Update(AssistantPanelContent content)
        {
            if (content is not ActivityContent c)
            {
                return;
            }
            label.Text = c.Label;
            spinner.IsActive = !c.Done;
            spinner.Visibility = c.Done ? Visibility.Collapsed : Visibility.Visible;
            check.Visibility = c.Done ? Visibility.Visible : Visibility.Collapsed;
        }
    }

    // A draft the bridge saved: "A draft is ready" with Open Draft.
    private sealed class DraftRow : Row
    {
        private readonly Border card;

        public DraftRow(Action open)
        {
            var t = Assistant.PanelTexts();
            var grid = new Grid { ColumnSpacing = 8 };
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            var icon = new FontIcon { Glyph = Icons.Glyph("document-edit-symbolic"), FontSize = 16, VerticalAlignment = VerticalAlignment.Center };
            var label = PlainLabel("BodyLabelStyle");
            label.Text = t.DraftReady;
            label.VerticalAlignment = VerticalAlignment.Center;
            var button = new Button { Content = t.OpenDraft, VerticalAlignment = VerticalAlignment.Center };
            button.Click += (_, _) => open();
            Grid.SetColumn(label, 1);
            Grid.SetColumn(button, 2);
            grid.Children.Add(icon);
            grid.Children.Add(label);
            grid.Children.Add(button);
            card = new Border
            {
                Child = grid,
                Padding = new Thickness(12, 8, 12, 8),
                CornerRadius = new CornerRadius(8),
                BorderThickness = new Thickness(1),
                Background = (Brush)Application.Current.Resources["CardBackgroundFillColorDefaultBrush"],
                BorderBrush = (Brush)Application.Current.Resources["CardStrokeColorDefaultBrush"],
            };
        }

        public override UIElement Root => card;

        public override void Update(AssistantPanelContent content)
        {
        }
    }

    // An error (with Try Again when the question can be sent once more, and
    // the button of what it offers: Sign In… or Get Claude Code…) or a note.
    private sealed class LineRow : Row
    {
        private readonly Grid grid = new() { ColumnSpacing = 6 };
        private readonly FontIcon icon = new() { Glyph = Icons.Glyph("dialog-warning-symbolic"), FontSize = 14, VerticalAlignment = VerticalAlignment.Top };
        private readonly TextBlock label = PlainLabel("BodyLabelStyle");
        private readonly Button button = new() { Content = L10n.T("Try Again") };
        private readonly Button other = new();
        private readonly StackPanel buttons = new() { Orientation = Orientation.Horizontal, Spacing = 6, HorizontalAlignment = HorizontalAlignment.Left };
        private ErrorOffer offered;

        public LineRow(Action retry, Action<ErrorOffer> offer)
        {
            icon.Foreground = (Brush)Application.Current.Resources["SystemFillColorCriticalBrush"];
            button.Click += (_, _) => retry();
            other.Click += (_, _) => offer(offered);
            buttons.Children.Add(other);
            buttons.Children.Add(button);
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var column = new StackPanel { Spacing = 4 };
            column.Children.Add(label);
            column.Children.Add(buttons);
            Grid.SetColumn(column, 1);
            grid.Children.Add(icon);
            grid.Children.Add(column);
        }

        public override UIElement Root => grid;

        public override void Update(AssistantPanelContent content)
        {
            switch (content)
            {
                case ErrorContent e:
                    label.Text = e.Text;
                    label.Foreground = (Brush)Application.Current.Resources["SystemFillColorCriticalBrush"];
                    icon.Visibility = Visibility.Visible;
                    offered = e.Offer;
                    other.Content = OfferLabel(e.Offer);
                    other.Visibility = e.Offer == ErrorOffer.None ? Visibility.Collapsed : Visibility.Visible;
                    button.Visibility = e.Retry ? Visibility.Visible : Visibility.Collapsed;
                    buttons.Visibility = e.Retry || e.Offer != ErrorOffer.None ? Visibility.Visible : Visibility.Collapsed;
                    break;
                case NoteContent n:
                    label.Text = n.Text;
                    label.Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"];
                    icon.Visibility = Visibility.Collapsed;
                    buttons.Visibility = Visibility.Collapsed;
                    break;
            }
        }
    }
}
