// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardViews.swift (the state bar)
// and BoardNavView.swift (DotView), with BoardPalette.accent of
// BoardAppearance.swift; GTK: boardStateDotClass (window/board_list.go).
// The state picks the visual state (BoardStateSwatch.xaml).

using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A patch of a board state's accent: a row's bar, a filter's dot.</summary>
public sealed partial class BoardStateSwatch : UserControl
{
    private Board.State state = Board.State.Info;

    /// <summary>A grey patch (For Your Information).</summary>
    public BoardStateSwatch()
    {
        InitializeComponent();
        Loaded += (_, _) => Apply();
    }

    /// <summary>The state whose accent the patch shows.</summary>
    public Board.State State
    {
        get => state;
        set
        {
            state = value;
            Apply();
        }
    }

    /// <summary>The patch's corners (a dot is round, a bar barely).</summary>
    public CornerRadius Corners
    {
        get => Fill.CornerRadius;
        set => Fill.CornerRadius = value;
    }

    private void Apply() => VisualStateManager.GoToState(this, state.ToString(), false);
}
