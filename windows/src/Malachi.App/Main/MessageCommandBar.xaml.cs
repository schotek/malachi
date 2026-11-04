// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the message page's header bar of ui/internal/window/window.go
// (the reply buttons, the star's clicked handler) and actions.go
// (setMessageActionsSensitive, setStar, trashTooltip); macOS:
// MainWindow/MainToolbar.swift and MainWindowController's validation
// (starTitle, trashTitle), and its More Actions menu with the actions the
// toolbar does not show (toolbar.MissingActions and the overflow, M6, T8).
// See MessageCommandBar.xaml. The buttons run the main window's commands,
// which are enabled from the selection's ActionFlags; the star and the
// trash follow the flags too, and so do the actions an account does not
// offer (hidden) and Reply's label (ActionPresentation). Which buttons
// show and in what order is the user's toolbar-mail layout, its message
// section (ApplyToolbar, docs/plans/toolbar-customization.md W.4).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Commands;
using Malachi.App.Resources;
using Malachi.App.Toolbars;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Main;

/// <summary>The message pane's header bar.</summary>
public sealed partial class MessageCommandBar : UserControl
{
    // The items More Actions already has of its own.
    private static readonly HashSet<string> InMenu = new(StringComparer.Ordinal) { Toolbar.Junk, Toolbar.MarkRead, Toolbar.LoadPictures };

    // The items of More Actions the bar put in on opening.
    private readonly List<MenuFlyoutItemBase> added = [];

    private WindowCommands? commands;

    /// <summary>A bar whose buttons are disabled until <see cref="Attach"/>.</summary>
    public MessageCommandBar()
    {
        InitializeComponent();
        StarButton.IsEnabled = false;
        MarkReadButton.IsEnabled = false;
        StarGlyph.Glyph = Icons.Glyph("non-starred-symbolic");
        ReplyGlyph.Glyph = Icons.Glyph("mail-reply-sender-symbolic");
        MarkReadGlyph.Glyph = Icons.Glyph("mail-read-symbolic");
        StarButton.Click += OnStarClick;
        MarkReadButton.Click += OnMarkReadClick;
        var loadImages = Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.LoadPictures);
        AutomationProperties.SetName(LoadPicturesButton, loadImages);
        ToolTipService.SetToolTip(LoadPicturesButton, loadImages);
        Strip.Bar = Toolbar.Bar.Mail;
        Strip.OverflowChanged += (_, _) => OverflowChanged?.Invoke(this, EventArgs.Empty);
        // The Windows default until the window applies the user's layout.
        ApplyToolbar(Toolbar.PresentFor(ToolbarVariant.Windows, Toolbar.Bar.Mail, Toolbar.DefaultFor(ToolbarVariant.Windows, Toolbar.Bar.Mail), Toolbar.Features.None)
            .SectionRun(Toolbar.Bar.Mail, Toolbar.Section.Message));
        Labels.Register(ReplyButton, () => commands is { } c ? ActionPresentation.ReplyLabel(c.Flags) : L10n.T("Reply"));
        Labels.Register(ReplyAllButton, () => L10n.T("Reply All"));
        Labels.Register(ForwardButton, () => L10n.T("Forward"));
        Labels.Register(ArchiveButton, () => L10n.T("Archive"));
        Labels.Register(TrashButton, () => Outbox.TrashTooltip(commands?.Flags.Outbox ?? false));
        Labels.Register(StarButton, () => commands?.Flags.Flagged == true ? L10n.T("Unstar") : L10n.T("Star"));
        Labels.Register(AssistantButton, () => Toolbar.ItemLabel(Toolbar.Bar.Mail, Toolbar.Assistant));
        Labels.Register(MoreButton, () => L10n.T("More Actions"));
        Labels.Register(MarkReadButton, MarkReadLabel);
        Labels.Register(JunkButton, () => L10n.T("Mark as Junk"));
        Labels.Register(MoveToButton, () => L10n.T("Move To"));
        Labels.Register(UndoButton, () => L10n.T("Undo"));
        Labels.Register(LoadPicturesButton, () => loadImages);
    }

    /// <summary>Which items gave way for the lack of room changed (More Actions has them).</summary>
    public event EventHandler? OverflowChanged;

    /// <summary>Save to eWay-CRM… was chosen in More Actions while the bar does not show its button.</summary>
    public event EventHandler? CrmSaveRequested;

    /// <summary>The display mode of the bar's icon buttons (the window applies toolbar-display-mode).</summary>
    internal ToolbarLabels Labels { get; } = new();

    /// <summary>The bar's strip (a right click on an empty spot offers Customize Toolbar…).</summary>
    internal FrameworkElement Bar => BarRoot;

    /// <summary>The actions More Actions adds because the layout does not show them (Toolbar.MissingActionsFor; the window's).</summary>
    public Func<IReadOnlyList<string>>? MissingActions { get; set; }

    /// <summary>Fills Move To's menu with the selection's folders (the list's context menu does the same).</summary>
    public Action<IList<MenuFlyoutItemBase>>? FillMoveTargets { get; set; }

    /// <summary>The Assistant's ✦ button (window.blp assistant_button); the main window gives it its menu.</summary>
    public Button Assistant => AssistantButton;

    /// <summary>The eWay-CRM button (crm_button); the main window binds it to its CrmController.</summary>
    internal Crm.CrmToolbarButton CrmButton => CrmCommand;

    /// <summary>The assistant panel's toggle (window.blp assistant_panel_button).</summary>
    public ToggleButton AssistantPanel => AssistantPanelToggle;

    /// <summary>
    /// The width the bar needs to show every item of its layout: the strip's
    /// items, those that gave way included, and the padding (the minimum
    /// width GTK's header bar would ask its pane for).
    /// </summary>
    public double MinimumWidth => BarRoot.Padding.Left + BarRoot.Padding.Right + Strip.NaturalWidth;

    /// <summary>Binds the buttons to the main window's commands and shows their flags.</summary>
    public void Attach(WindowCommands windowCommands)
    {
        ArgumentNullException.ThrowIfNull(windowCommands);
        commands = windowCommands;
        CommandBinding.Bind(ReplyButton, windowCommands.Reply);
        CommandBinding.Bind(ReplyAllButton, windowCommands.ReplyAll);
        CommandBinding.Bind(ForwardButton, windowCommands.Forward);
        CommandBinding.Bind(TrashButton, windowCommands.Trash);
        CommandBinding.Bind(ArchiveButton, windowCommands.Archive);
        CommandBinding.Bind(JunkButton, windowCommands.Junk);
        CommandBinding.Bind(UndoButton, windowCommands.Undo);
        CommandBinding.Bind(LoadPicturesButton, windowCommands.LoadImages);
        CommandBinding.Bind(MenuJunk, windowCommands.Junk);
        CommandBinding.Bind(MenuMarkUnread, windowCommands.MarkUnread);
        CommandBinding.Bind(MenuMarkRead, windowCommands.MarkRead);
        CommandBinding.Bind(MenuLoadImages, windowCommands.LoadImages);
        CommandBinding.Bind(MenuTrustSender, windowCommands.TrustSender);
        CommandBinding.Bind(MenuPrint, windowCommands.Print);
        CommandBinding.Bind(MenuChangeStatus, windowCommands.ChangeStatus);
        foreach (var c in new[] { windowCommands.ChangeStatus, windowCommands.ToggleFlag, windowCommands.Trash, windowCommands.MarkRead, windowCommands.MarkUnread })
        {
            c.Command.CanExecuteChanged += (_, _) => ShowFlags();
        }
        ShowFlags();
    }

    /// <summary>Shows <paramref name="run"/>, the message section of Toolbar.PresentFor (the user's toolbar-mail layout).</summary>
    public void ApplyToolbar(IReadOnlyList<Toolbar.Shown> run) => Strip.Apply(run);

    /// <summary>
    /// actions.go setStar and trashTooltip for the current flags: the
    /// star's state, icon and tooltip (Star or Unstar), and Cancel Sending
    /// for an outbox message's trash.
    /// </summary>
    public void ShowFlags()
    {
        if (commands is not { } c)
        {
            return;
        }
        var flags = c.Flags;
        StarButton.IsEnabled = c.ToggleFlag.IsEnabled;
        StarButton.IsChecked = flags.Flagged;
        StarGlyph.Glyph = Icons.Glyph(flags.Flagged ? "starred-symbolic" : "non-starred-symbolic");
        var star = flags.Flagged ? L10n.T("Unstar") : L10n.T("Star");
        ToolTipService.SetToolTip(StarButton, star);
        AutomationProperties.SetName(StarButton, star);
        var trash = Outbox.TrashTooltip(flags.Outbox);
        ToolTipService.SetToolTip(TrashButton, trash);
        AutomationProperties.SetName(TrashButton, trash);
        // actions.go presentReply and the capabilities' visibility.
        var reply = ActionPresentation.ReplyLabel(flags);
        ReplyGlyph.Glyph = ActionPresentation.ReplyGlyph(flags);
        ToolTipService.SetToolTip(ReplyButton, reply);
        AutomationProperties.SetName(ReplyButton, reply);
        ReplyButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Reply);
        ReplyAllButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.ReplyAll);
        ForwardButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Forward);
        TrashButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Trash);
        ArchiveButton.Visibility = ActionPresentation.Shown(flags, MessageActionKind.Archive);
        var status = c.ChangeStatus.IsEnabled ? Visibility.Visible : Visibility.Collapsed;
        MenuChangeStatus.Visibility = status;
        MenuChangeStatusSeparator.Visibility = status;
        // markRead (O3): Mark as Read while a selected row is unread, else Mark as Unread.
        MarkReadButton.IsEnabled = c.MarkRead.IsEnabled || c.MarkUnread.IsEnabled;
        MarkReadGlyph.Glyph = Icons.Glyph(c.MarkRead.IsEnabled || !c.MarkUnread.IsEnabled ? "mail-read-symbolic" : "mail-unread-symbolic");
        var markRead = MarkReadLabel();
        ToolTipService.SetToolTip(MarkReadButton, markRead);
        AutomationProperties.SetName(MarkReadButton, markRead);
        MoveToButton.IsEnabled = flags.Move;
        Labels.Refresh();
    }

    // The toggle's label: what a click does now.
    private string MarkReadLabel() =>
        commands is { } c && !c.MarkRead.IsEnabled && c.MarkUnread.IsEnabled
            ? Toolbar.StripMnemonic(L10n.T("Mark as _Unread"))
            : Toolbar.StripMnemonic(L10n.T("Mark as _Read"));

    // window.go: the star acts on the selection (every member of a
    // conversation row, flagTarget); the button then shows the model.
    private void OnStarClick(object sender, RoutedEventArgs e)
    {
        commands?.ToggleFlag.TryExecute();
        ShowFlags();
    }

    private void OnMarkReadClick(object sender, RoutedEventArgs e)
    {
        if (commands is not { } c)
        {
            return;
        }
        if (c.MarkRead.IsEnabled)
        {
            c.MarkRead.TryExecute();
        }
        else
        {
            c.MarkUnread.TryExecute();
        }
        ShowFlags();
    }

    private void OnMoveToOpening(object? sender, object e)
    {
        MoveToMenu.Items.Clear();
        FillMoveTargets?.Invoke(MoveToMenu.Items);
    }

    // More Actions (M6, T8): first the actions the layout does not show
    // (Toolbar.MissingActionsFor), then those that gave way for the lack of
    // room, then a separator and the menu's own items.
    private void OnMoreOpening(object? sender, object e)
    {
        foreach (var item in added)
        {
            MoreMenu.Items.Remove(item);
        }
        added.Clear();
        if (commands is not { } c)
        {
            return;
        }
        var ids = new List<string>();
        foreach (var id in (MissingActions?.Invoke() ?? []).Concat(Strip.Overflow))
        {
            if (!ids.Contains(id, StringComparer.Ordinal) && !InMenu.Contains(id))
            {
                ids.Add(id);
            }
        }
        foreach (var id in ids)
        {
            if (MoreItem(c, id) is { } item)
            {
                AutomationProperties.SetAutomationId(item, "MoreActions." + id);
                added.Add(item);
            }
        }
        if (added.Count == 0)
        {
            return;
        }
        added.Add(new MenuFlyoutSeparator());
        for (var i = 0; i < added.Count; i++)
        {
            MoreMenu.Items.Insert(i, added[i]);
        }
    }

    // An item of More Actions for a toolbar item, with the button's command.
    private MenuFlyoutItemBase? MoreItem(WindowCommands c, string id)
    {
        switch (id)
        {
            case Toolbar.MoveTo:
                {
                    var sub = new MenuFlyoutSubItem { Text = L10n.T("Move To") };
                    if (c.Flags.Move)
                    {
                        FillMoveTargets?.Invoke(sub.Items);
                    }
                    sub.IsEnabled = sub.Items.Count > 0;
                    return sub;
                }
            case Toolbar.Crm:
                {
                    var crm = new MenuFlyoutItem { Text = L10n.T("Save to eWay-CRM…"), IsEnabled = CrmCommand.Visibility == Visibility.Visible };
                    crm.Click += (_, _) => CrmSaveRequested?.Invoke(this, EventArgs.Empty);
                    return crm;
                }
        }
        var (command, label) = id switch
        {
            Toolbar.Reply => (c.Reply, ActionPresentation.ReplyLabel(c.Flags)),
            Toolbar.ReplyAll => (c.ReplyAll, L10n.T("Reply All")),
            Toolbar.Forward => (c.Forward, L10n.T("Forward")),
            Toolbar.Archive => (c.Archive, L10n.T("Archive")),
            Toolbar.Trash => (c.Trash, Outbox.TrashTooltip(c.Flags.Outbox)),
            Toolbar.Star => (c.ToggleFlag, c.Flags.Flagged ? L10n.T("Unstar") : L10n.T("Star")),
            Toolbar.Undo => (c.Undo, L10n.T("Undo")),
            _ => ((AppCommand?)null, ""),
        };
        if (command is null)
        {
            return null;
        }
        var item = CommandBinding.Item(command);
        item.Text = label;
        return item;
    }
}
