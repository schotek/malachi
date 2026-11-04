// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardAppearance.swift
// (BoardPalette.colours) as the detail's and the rows' state pill paints it;
// GTK: boardStatePillClass (window/board_actions.go). The state picks the
// visual state (BoardStatePill.xaml); the text is the state's name, plain.

using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The pill of a board state: its name in its colours.</summary>
public sealed partial class BoardStatePill : UserControl
{
    private Board.State state = Board.State.Info;

    /// <summary>An empty grey pill.</summary>
    public BoardStatePill()
    {
        InitializeComponent();
        Loaded += (_, _) => Apply();
    }

    /// <summary>The state whose colours the pill has.</summary>
    public Board.State State
    {
        get => state;
        set
        {
            state = value;
            Apply();
        }
    }

    /// <summary>The pill's text (the state's name), plain.</summary>
    public string Text
    {
        get => Label.Text;
        set => Label.Text = value ?? "";
    }

    private void Apply() => VisualStateManager.GoToState(this, state.ToString(), false);
}
