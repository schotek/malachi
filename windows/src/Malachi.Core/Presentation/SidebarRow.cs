// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the rows of ui/internal/window/folders.go (newHeaderRow,
// newTwisty, newFolderRow, folderRow.setUnread, rebuildFolderList's
// subtitle for a pinned folder) and favourites.go (newStar), with
// ui/internal/style's sidebar rules (the star waits for the pointer or the
// keyboard, a filled star stays in sight in the tree); macOS:
// Sidebar/FolderCellView.swift and FolderOutlineDataSource.swift, which keep
// this in AppKit and Windows in Core (docs/windows-port.md §7.4, §11.2).
//
// One row of the flat sidebar list, as a view model the WinUI ListView
// keeps across rebuilds: MailboxController publishes its entries as
// snapshots keyed by SidebarKey, and the view hands each new entry to the
// row of its key (KeyedListSync's view overload), which raises only what
// changed. Every text is plain: a folder's and an account's names are the
// server's and the user's.

using System.Globalization;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>One row of the folder sidebar.</summary>
public sealed partial class SidebarRow : ObservableObject
{
    /// <summary>folders.go <c>folderIndent</c>: the indent per tree level, in pixels.</summary>
    public const int IndentPerLevel = 12;

    /// <summary>folders.go <c>folderHeadingGap</c>: the space above a heading, in pixels.</summary>
    public const int HeadingGap = 3;

    /// <summary>A row for <paramref name="key"/>; <see cref="Update"/> fills it.</summary>
    public SidebarRow(SidebarKey key)
    {
        Key = key;
        Title = "";
        Subtitle = "";
        Tooltip = "";
        Icon = "";
        TwistyIcon = "";
        TwistyTooltip = "";
        Badge = "";
        StarIcon = "";
        StarTooltip = "";
    }

    /// <summary>The row's key across rebuilds.</summary>
    public SidebarKey Key { get; }

    /// <summary>A heading or a folder.</summary>
    [ObservableProperty]
    public partial SidebarRowKind Kind { get; private set; }

    /// <summary>A heading (the Favourites section's or an account's).</summary>
    public bool IsHeading => Kind != SidebarRowKind.Folder;

    /// <summary>The folder the row stands for; null on a heading.</summary>
    public FolderKey? Folder { get; private set; }

    /// <summary>The account of a folder or of its heading; null on the Favourites heading.</summary>
    public AccountId? Account { get; private set; }

    /// <summary>A heading's text (Favourites, the account's label) or the folder's display name.</summary>
    [ObservableProperty]
    public partial string Title { get; private set; }

    /// <summary>Under a pinned folder's name while two or more accounts are enabled: whose it is; "" otherwise.</summary>
    [ObservableProperty]
    public partial string Subtitle { get; private set; }

    /// <summary>The folder's path on the server (the row's tooltip); "" on a heading.</summary>
    [ObservableProperty]
    public partial string Tooltip { get; private set; }

    /// <summary>The indent of the tree level, in pixels.</summary>
    [ObservableProperty]
    public partial double Indent { get; private set; }

    /// <summary>The role icon (a GTK name, roleIcon); "" on a heading.</summary>
    [ObservableProperty]
    public partial string Icon { get; private set; }

    /// <summary>
    /// Whether the row has the fold arrow's column: every heading, and the
    /// folders of an account whose tree has a parent (<see cref="FolderEntry.Nested"/>),
    /// so that their titles line up.
    /// </summary>
    [ObservableProperty]
    public partial bool TwistyShown { get; private set; }

    /// <summary>Whether the arrow folds anything; an inactive arrow keeps its place, invisible and inert.</summary>
    [ObservableProperty]
    public partial bool TwistyActive { get; private set; }

    /// <summary>The arrow: pan-end while folded, pan-down while open (GTK names).</summary>
    [ObservableProperty]
    public partial string TwistyIcon { get; private set; }

    /// <summary>Expand or Collapse; "" on an inactive arrow.</summary>
    [ObservableProperty]
    public partial string TwistyTooltip { get; private set; }

    /// <summary>The folded state the arrow shows.</summary>
    [ObservableProperty]
    public partial bool Collapsed { get; private set; }

    /// <summary>The unread badge, "" at zero (setUnread).</summary>
    [ObservableProperty]
    public partial string Badge { get; private set; }

    /// <summary>Whether the row has the pin star: a folder that can be opened (a container cannot be pinned).</summary>
    [ObservableProperty]
    public partial bool HasStar { get; private set; }

    /// <summary>The folder is pinned.</summary>
    [ObservableProperty]
    public partial bool Starred { get; private set; }

    /// <summary>starred or non-starred (GTK names).</summary>
    [ObservableProperty]
    public partial string StarIcon { get; private set; }

    /// <summary>Add to Favourites, or Remove from Favourites.</summary>
    [ObservableProperty]
    public partial string StarTooltip { get; private set; }

    /// <summary>
    /// The star stays in sight: a filled star in the tree says the folder is
    /// pinned. Otherwise it waits for the pointer, the selection or the
    /// keyboard (style.go <c>.folder-star</c>); in the Favourites section every
    /// row is pinned, so it waits there too.
    /// </summary>
    [ObservableProperty]
    public partial bool StarPinned { get; private set; }

    /// <summary>A folder that can be selected; headings and containers cannot.</summary>
    [ObservableProperty]
    public partial bool Selectable { get; private set; }

    /// <summary>A container that cannot be opened is dimmed (dim-label).</summary>
    [ObservableProperty]
    public partial bool Dimmed { get; private set; }

    /// <summary>
    /// Shows <paramref name="e"/>; <paramref name="several"/> says whether
    /// two or more accounts are enabled (a pinned Inbox then says whose it
    /// is). Only what changed is raised.
    /// </summary>
    public void Update(FolderEntry e, bool several)
    {
        System.ArgumentNullException.ThrowIfNull(e);
        Account = e.Account?.Id;
        Folder = e.Key;
        if (e.Header)
        {
            Kind = e.Favourite ? SidebarRowKind.FavouritesHeading : SidebarRowKind.AccountHeading;
            Title = e.Favourite ? L10n.T("Favourites") : e.Account is { } a ? FolderTree.AccountLabel(a) : "";
            Subtitle = "";
            Tooltip = "";
            Indent = 0;
            Icon = "";
            // The Favourites section does not fold, but keeps the arrow's
            // place so that the headings line up (folders.go newHeaderRow).
            SetTwisty(shown: true, active: !e.Favourite, collapsed: !e.Favourite && e.Collapsed);
            Badge = "";
            HasStar = false;
            Starred = false;
            StarIcon = "";
            StarTooltip = "";
            StarPinned = false;
            Selectable = false;
            Dimmed = false;
            return;
        }
        var f = e.Folder!;
        Kind = SidebarRowKind.Folder;
        Title = FolderTree.FolderTitle(f);
        Subtitle = e.Favourite && several && e.Account is { } account ? FolderTree.AccountLabel(account) : "";
        Tooltip = f.Path;
        Indent = IndentPerLevel * e.Depth;
        Icon = FolderTree.RoleIcon(f.Role);
        // Accounts without any nesting get no arrow column at all.
        SetTwisty(shown: e.Nested, active: e.Nested && e.HasChildren, collapsed: e.Collapsed);
        Badge = e.Badge > 0 ? e.Badge.ToString(CultureInfo.InvariantCulture) : "";
        HasStar = f.Selectable;
        Starred = f.Selectable && e.Starred;
        StarIcon = Starred ? "starred-symbolic" : "non-starred-symbolic";
        StarTooltip = Starred ? L10n.T("Remove from Favourites") : L10n.T("Add to Favourites");
        StarPinned = Starred && !e.Favourite;
        Selectable = f.Selectable;
        Dimmed = !f.Selectable;
    }

    /// <summary>What a screen reader names the row: its text, and the unread count when there is one.</summary>
    public override string ToString()
    {
        var name = Subtitle.Length > 0 ? Title + ", " + Subtitle : Title;
        return Badge.Length > 0 ? name + ", " + Badge : name;
    }

    partial void OnKindChanged(SidebarRowKind value) => OnPropertyChanged(nameof(IsHeading));

    // folders.go newTwisty: pan-down open, pan-end folded; an inactive
    // arrow has no tooltip.
    private void SetTwisty(bool shown, bool active, bool collapsed)
    {
        TwistyShown = shown;
        TwistyActive = active;
        Collapsed = collapsed;
        TwistyIcon = collapsed ? "pan-end-symbolic" : "pan-down-symbolic";
        TwistyTooltip = !active ? "" : collapsed ? L10n.T("Expand") : L10n.T("Collapse");
    }
}
