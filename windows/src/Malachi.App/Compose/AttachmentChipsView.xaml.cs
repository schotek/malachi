// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/AttachmentChipsView.swift (add,
// remove, removeAll, onRemove, onPreview, ComposeTileView); GTK:
// ui/internal/compose/compose.go (addChip, the chips map,
// attBox.SetVisible). The tiles are Core's chips
// (ComposeAttachmentsController.Chips), applied by id so that the tiles
// that stay keep their place. A click on a tile asks for its preview while
// someone listens to PreviewRequested (the compose window); without, the
// tiles are inert (the event editor).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Attachments;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Compose;

/// <summary>The attachment tiles of a compose window.</summary>
public sealed partial class AttachmentChipsView : UserControl
{
    private readonly Dictionary<string, (ComposeAttachmentChip Chip, AttachmentTile Tile)> tiles = new(StringComparer.Ordinal);

    /// <summary>No tiles, hidden.</summary>
    public AttachmentChipsView()
    {
        InitializeComponent();
        Visibility = Visibility.Collapsed;
    }

    /// <summary>The Remove of a tile, with the attachment's id (removeAttachment).</summary>
    public event EventHandler<string>? RemoveRequested;

    /// <summary>A click on a tile, with the attachment's id and the tile; the tiles are inert while no one listens.</summary>
    public event EventHandler<(string Id, FrameworkElement Tile)>? PreviewRequested;

    /// <summary>Shows <paramref name="list"/>; hidden while it is empty.</summary>
    public void Show(IReadOnlyList<ComposeAttachmentChip> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        var batch = ChipIcons.Shared.StartBatch();
        var kept = new Dictionary<string, (ComposeAttachmentChip, AttachmentTile)>(StringComparer.Ordinal);
        foreach (var chip in list)
        {
            kept[chip.Id] = tiles.TryGetValue(chip.Id, out var had) && had.Chip == chip ? had : (chip, Tile(chip, batch));
        }
        tiles.Clear();
        foreach (var (id, entry) in kept)
        {
            tiles[id] = entry;
        }
        var wanted = list.Select(c => (UIElement)tiles[c.Id].Tile).ToList();
        if (!wanted.SequenceEqual(Tiles.Children))
        {
            Tiles.Children.Clear();
            foreach (var tile in wanted)
            {
                Tiles.Children.Add(tile);
            }
        }
        Visibility = list.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    private AttachmentTile Tile(ComposeAttachmentChip chip, IconLookups<ImageSource>.Batch batch)
    {
        var tile = new AttachmentTile();
        var close = AttachmentTile.AccessoryButton("window-close", L10n.T("Remove"));
        close.Click += (_, _) => RemoveRequested?.Invoke(this, chip.Id);
        tile.Set(ChipIcons.For(batch, chip.Name, AttachmentTile.IconSize), chip.Name, chip.Size, close);
        if (PreviewRequested is not null)
        {
            tile.Clicked += (_, _) => PreviewRequested?.Invoke(this, (chip.Id, tile));
        }
        return tile;
    }
}
