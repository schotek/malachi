// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/AttachmentChipsView.swift (add,
// remove, removeAll, the chip's remove button); GTK:
// ui/internal/compose/compose.go (addChip, the chips map,
// attBox.SetVisible). The chips are Core's (ComposeAttachmentsController
// .Chips), applied by key so that the chips that stay keep their place.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Compose;

/// <summary>The attachment chips of a compose window.</summary>
public sealed partial class AttachmentChipsView : UserControl
{
    private readonly ObservableCollection<ComposeAttachmentChip> chips = [];

    /// <summary>No chips, hidden.</summary>
    public AttachmentChipsView()
    {
        InitializeComponent();
        ChipsList.ItemsSource = chips;
        Visibility = Visibility.Collapsed;
    }

    /// <summary>The Remove of a chip, with the attachment's id (removeAttachment).</summary>
    public event EventHandler<string>? RemoveRequested;

    /// <summary>Shows <paramref name="list"/>; hidden while it is empty.</summary>
    public void Show(IReadOnlyList<ComposeAttachmentChip> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        KeyedListSync.Apply(chips, list, c => c.Id);
        Visibility = chips.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnRemoveClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string id)
        {
            RemoveRequested?.Invoke(this, id);
        }
    }
}
