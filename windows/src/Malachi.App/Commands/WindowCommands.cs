// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Actions.swift (the MalachiActions
// a window implements) and of the validation of MainWindowController and
// MessageWindowController (flags → enabled items); GTK: the app.* actions
// of ui/main.go, the win.* of window/actions.go and message_window.go's
// msg.*. Every window has one set: the application's commands (New
// Message, Preferences, Add Account, About, Quit), the per-message ones
// (enabled from the ActionFlags of what the window shows,
// actions.go setMessageActionsSensitive), the main window's (Check for
// New Mail, Search, the primary menu's F10), Close for a secondary
// window, Send and Save Draft of a compose window, the Preferences'
// reordering. The screens bind their buttons and menu items to these and
// set the handlers of their own; the CommandRouter runs the ones with keys
// (docs/windows-port.md §11.5).

using System.Collections.Generic;
using Malachi.Core.Model;
using Malachi.Core.Presentation;

namespace Malachi.App.Commands;

/// <summary>The commands of one window, by name.</summary>
public sealed class WindowCommands
{
    private readonly Dictionary<ShortcutCommand, AppCommand> byShortcut;
    private ActionFlags flags;

    /// <summary>A set whose commands are all disabled until their handlers are set.</summary>
    public WindowCommands()
    {
        byShortcut = new()
        {
            [ShortcutCommand.NewMessage] = NewMessage,
            [ShortcutCommand.Preferences] = Preferences,
            [ShortcutCommand.Quit] = Quit,
            [ShortcutCommand.CheckForNewMail] = CheckForNewMail,
            [ShortcutCommand.Search] = Search,
            [ShortcutCommand.MainMenu] = MainMenu,
            [ShortcutCommand.Reply] = Reply,
            [ShortcutCommand.ReplyAll] = ReplyAll,
            [ShortcutCommand.Forward] = Forward,
            [ShortcutCommand.Trash] = Trash,
            [ShortcutCommand.Archive] = Archive,
            [ShortcutCommand.Junk] = Junk,
            [ShortcutCommand.MarkUnread] = MarkUnread,
            [ShortcutCommand.ToggleFlag] = ToggleFlag,
            [ShortcutCommand.CloseWindow] = CloseWindow,
            [ShortcutCommand.Send] = Send,
            [ShortcutCommand.SaveDraft] = SaveDraft,
            [ShortcutCommand.MoveUp] = MoveUp,
            [ShortcutCommand.MoveDown] = MoveDown,
        };
        // The per-message commands follow Flags unless a screen says otherwise.
        Reply.CanExecute = () => flags.Reply;
        ReplyAll.CanExecute = () => flags.ReplyAll;
        Forward.CanExecute = () => flags.Forward;
        Trash.CanExecute = () => flags.Trash;
        Archive.CanExecute = () => flags.Archive;
        Junk.CanExecute = () => flags.Junk;
        MarkRead.CanExecute = () => flags.MarkRead;
        MarkUnread.CanExecute = () => flags.MarkUnread;
        ToggleFlag.CanExecute = () => flags.ToggleFlag;
        LoadImages.CanExecute = () => flags.LoadImages;
        TrustSender.CanExecute = () => flags.TrustSender;
        ChangeStatus.CanExecute = () => flags.ChangeStatus;
    }

    /// <summary>app.compose (Ctrl+N).</summary>
    public AppCommand NewMessage { get; } = new(nameof(NewMessage));

    /// <summary>app.preferences (Ctrl+,).</summary>
    public AppCommand Preferences { get; } = new(nameof(Preferences));

    /// <summary>app.add-account.</summary>
    public AppCommand AddAccount { get; } = new(nameof(AddAccount));

    /// <summary>app.add-jira-account: the Jira account assistant.</summary>
    public AppCommand AddJiraAccount { get; } = new(nameof(AddJiraAccount));

    /// <summary>app.about.</summary>
    public AppCommand About { get; } = new(nameof(About));

    /// <summary>app.quit (Ctrl+Q).</summary>
    public AppCommand Quit { get; } = new(nameof(Quit));

    /// <summary>win.refresh (F5; Ctrl+R with ctrl-r = refresh).</summary>
    public AppCommand CheckForNewMail { get; } = new(nameof(CheckForNewMail));

    /// <summary>win.search (Ctrl+F, Ctrl+E): the search box.</summary>
    public AppCommand Search { get; } = new(nameof(Search));

    /// <summary>The main window's primary menu (F10), while its button is shown.</summary>
    public AppCommand MainMenu { get; } = new(nameof(MainMenu));

    /// <summary>Reply (Ctrl+R).</summary>
    public AppCommand Reply { get; } = new(nameof(Reply));

    /// <summary>Reply All (Ctrl+Shift+R).</summary>
    public AppCommand ReplyAll { get; } = new(nameof(ReplyAll));

    /// <summary>Forward (Ctrl+Shift+F).</summary>
    public AppCommand Forward { get; } = new(nameof(Forward));

    /// <summary>win.trash (Delete): Move to Trash, or Cancel Sending in the outbox.</summary>
    public AppCommand Trash { get; } = new(nameof(Trash));

    /// <summary>win.archive (A).</summary>
    public AppCommand Archive { get; } = new(nameof(Archive));

    /// <summary>win.junk (J).</summary>
    public AppCommand Junk { get; } = new(nameof(Junk));

    /// <summary>win.mark-read (menu only).</summary>
    public AppCommand MarkRead { get; } = new(nameof(MarkRead));

    /// <summary>win.mark-unread (U).</summary>
    public AppCommand MarkUnread { get; } = new(nameof(MarkUnread));

    /// <summary>win.toggle-flag (S).</summary>
    public AppCommand ToggleFlag { get; } = new(nameof(ToggleFlag));

    /// <summary>win.load-images (menu).</summary>
    public AppCommand LoadImages { get; } = new(nameof(LoadImages));

    /// <summary>win.trust-sender (menu).</summary>
    public AppCommand TrustSender { get; } = new(nameof(TrustSender));

    /// <summary>
    /// win.change-status (menu, hidden while disabled): pops up the Change
    /// Status menu of the issue card on display, where the row's account
    /// changes the statuses of its issues.
    /// </summary>
    public AppCommand ChangeStatus { get; } = new(nameof(ChangeStatus));

    /// <summary>Closes a secondary window (Escape, Ctrl+W); the window may veto it with CanExecute.</summary>
    public AppCommand CloseWindow { get; } = new(nameof(CloseWindow));

    /// <summary>compose.send (Ctrl+Enter).</summary>
    public AppCommand Send { get; } = new(nameof(Send));

    /// <summary>compose.save (Ctrl+S).</summary>
    public AppCommand SaveDraft { get; } = new(nameof(SaveDraft));

    /// <summary>Moves the selected account up (Ctrl+Up).</summary>
    public AppCommand MoveUp { get; } = new(nameof(MoveUp));

    /// <summary>Moves the selected account down (Ctrl+Down).</summary>
    public AppCommand MoveDown { get; } = new(nameof(MoveDown));

    /// <summary>
    /// What the window's current message allows (the list's selection in
    /// the main window, the message of a message window); the per-message
    /// commands are re-validated when it changes.
    /// </summary>
    public ActionFlags Flags
    {
        get => flags;
        set
        {
            if (flags == value)
            {
                return;
            }
            flags = value;
            foreach (var c in (AppCommand[])[Reply, ReplyAll, Forward, Trash, Archive, Junk, MarkRead, MarkUnread, ToggleFlag, LoadImages, TrustSender, ChangeStatus])
            {
                c.Refresh();
            }
        }
    }

    /// <summary>The command of a keyboard shortcut.</summary>
    public AppCommand For(ShortcutCommand command) => byShortcut[command];

    /// <summary>Every command, for re-validating them all (a hook was wired).</summary>
    public IEnumerable<AppCommand> All =>
    [
        .. byShortcut.Values, AddAccount, AddJiraAccount, About, MarkRead, LoadImages, TrustSender, ChangeStatus,
    ];

    /// <summary>Re-validates every command.</summary>
    public void RefreshAll()
    {
        foreach (var c in All)
        {
            c.Refresh();
        }
    }
}
