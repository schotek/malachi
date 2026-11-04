// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the selectors of macos/Sources/MalachiMail/App/Actions.swift and
// their validateUserInterfaceItem; GTK: a GSimpleAction with its enabled
// state (window/actions.go, ui/main.go addActions). One command of a
// window: a XamlUICommand that buttons and menu items bind to
// (Command="{x:Bind Commands.Reply.Command}"), and that the window's
// CommandRouter runs for its keys. What it does and whether it may run
// now are set by whoever owns the action (the shell for the application's
// commands, the screens for theirs); without a handler it is disabled, as
// a macOS menu item whose hook is not wired.

using System;
using Microsoft.UI.Xaml.Input;

namespace Malachi.App.Commands;

/// <summary>One command of a window.</summary>
public sealed class AppCommand
{
    private Action? handler;
    private Func<bool>? canExecute;
    private Func<bool>? gate;

    /// <summary>A command named <paramref name="name"/> (for logs), disabled until it has a handler.</summary>
    public AppCommand(string name)
    {
        Name = name;
        Command = new XamlUICommand();
        Command.ExecuteRequested += (_, _) => TryExecute();
        Command.CanExecuteRequested += (_, e) => e.CanExecute = IsEnabled;
    }

    /// <summary>The command's name.</summary>
    public string Name { get; }

    /// <summary>What buttons and menu items bind to.</summary>
    public XamlUICommand Command { get; }

    /// <summary>What the command does; null disables it.</summary>
    public Action? Handler
    {
        get => handler;
        set
        {
            handler = value;
            Refresh();
        }
    }

    /// <summary>Whether it may run now (an action's enabled state); null means always, once it has a handler.</summary>
    public Func<bool>? CanExecute
    {
        get => canExecute;
        set
        {
            canExecute = value;
            Refresh();
        }
    }

    /// <summary>
    /// What the window's mode allows (Board.Allows: in the Board mode the
    /// list and the reader are hidden, so no key may act on their message),
    /// checked before <see cref="CanExecute"/>; null means always. Set by the
    /// window, apart from the screens' own <see cref="CanExecute"/>.
    /// </summary>
    public Func<bool>? Gate
    {
        get => gate;
        set
        {
            gate = value;
            Refresh();
        }
    }

    /// <summary>Whether the command runs when invoked now.</summary>
    public bool IsEnabled => handler is not null && (gate?.Invoke() ?? true) && (canExecute?.Invoke() ?? true);

    /// <summary>Runs the command when it is enabled; true when it ran.</summary>
    public bool TryExecute()
    {
        if (!IsEnabled)
        {
            return false;
        }
        handler!.Invoke();
        return true;
    }

    /// <summary>Asks the bound controls to read <see cref="IsEnabled"/> again (macOS validateVisibleItems).</summary>
    public void Refresh() => Command.NotifyCanExecuteChanged();
}
