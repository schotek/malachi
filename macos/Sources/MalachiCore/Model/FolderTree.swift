// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The sidebar layout, the pure functions of ui/internal/window/model.go
// (`sortFolders` and what it calls) and the folder naming of folders.go.

/// Bounds the sidebar tree; deeper (or cyclic) chains fall back to a depth
/// derived from the display path (model.go `maxFolderDepth`).
public let maxFolderDepth = 32

/// The sidebar header text for an account: its configured name, falling
/// back to the address. Both are user-entered, shown as plain text
/// (model.go `accountLabel`).
public func accountLabel(_ a: Account) -> String {
    let name = a.config.name.trimmingCharacters(in: .whitespacesAndNewlines)
    if !name.isEmpty {
        return name
    }
    return a.config.email.trimmingCharacters(in: .whitespacesAndNewlines)
}

/// Whether `f` is in `flags` (model.go `hasFlag`).
public func hasFlag(_ flags: [Flag], _ f: Flag) -> Bool {
    flags.contains(f)
}

/// Whether `s` belongs in a list shown under `f` (model.go
/// `matchesFilter`). It mirrors what the backend selects, and is used for
/// the one message the UI adds without asking (notify.newMessage). A row
/// already on screen is left alone when a flag change makes it stop
/// matching; the next load of the list applies the filter again.
public func matchesFilter(_ s: MessageSummary, _ f: MessageFilter) -> Bool {
    switch f {
    case .unread:
        return !hasFlag(s.flags, .seen)
    case .flagged:
        return hasFlag(s.flags, .flagged)
    default:
        return true
    }
}

/// The enabled accounts, keeping order (model.go `enabledAccounts`).
public func enabledAccounts(_ accounts: [Account]) -> [Account] {
    accounts.filter(\.enabled)
}

/// The folders the sidebar lists: an empty outbox is dropped (the folder
/// exists from the first send on; it only earns a row while something is
/// in it). The model keeps the full list so `folderByRole` still finds it
/// (model.go `visibleFolders`).
public func visibleFolders(_ list: [Folder]) -> [Folder] {
    list.filter { !($0.role == .outbox && $0.total == 0) }
}

/// The account's own address, for Reply All exclusion (model.go
/// `selfAddress`).
public func selfAddress(_ a: Account) -> Address {
    Address(name: a.config.displayName, address: a.config.email)
}

/// Lays the sidebar out (model.go `sortFolders`): the Favourites section
/// when anything is pinned (`favouriteSection`), then the enabled accounts
/// in list order, each preceded by a header row when there are at least
/// two of them or a Favourites section above them, then the account's
/// folders as a tree. Roots are ordered by (roleRank, path); the children
/// of a folder follow it, ordered the same way. Depth comes from `parentId`;
/// folders whose parent chain is cyclic or deeper than `maxFolderDepth` are
/// appended after the tree with a depth counted from their display path.
/// Non-selectable containers are kept so their children have somewhere to
/// hang.
public func sortFolders(
    _ accounts: [Account], _ folders: [AccountID: [Folder]], _ collapsed: CollapseState, _ favourites: FavouriteState
) -> [FolderEntry] {
    let enabled = enabledAccounts(accounts)
    var out = favouriteSection(enabled, folders, favourites)
    // A single account needs no heading of its own, unless a Favourites
    // section sits above its folders and the two would run into each other.
    let headers = enabled.count >= 2 || !out.isEmpty
    for a in enabled {
        // Only a header can fold a whole account away, and headers only
        // exist from two accounts on.
        let folded = headers && collapsed.accountCollapsed(a.id)
        if headers {
            out.append(FolderEntry(header: true, account: a, hasChildren: true, collapsed: folded))
        }
        if folded {
            continue
        }
        out.append(contentsOf: folderTree(a, folders[a.id] ?? [], collapsed, favourites))
    }
    return out
}

/// The block at the top of the sidebar (model.go `favouriteSection`): a
/// heading and one row per pinned folder, in tree order (accounts in list
/// order, then roles, then names), each at depth 0 and without its
/// children. A pin that does not resolve (the folder gone or renamed on the
/// server, its account switched off, a container that cannot be opened, an
/// outbox with nothing in it) is left out silently; when none resolves
/// there is no section at all. Account folds do not apply here: the section
/// is what stays in view while the tree is folded away.
public func favouriteSection(
    _ enabled: [Account], _ folders: [AccountID: [Folder]], _ favourites: FavouriteState
) -> [FolderEntry] {
    var rows: [FolderEntry] = []
    for a in enabled {
        let pinned = visibleFolders(folders[a.id] ?? []).filter { f in
            f.selectable && favourites.has(FolderKey(account: a.id, folder: f.id))
        }
        for f in sortSiblings(pinned) {
            rows.append(FolderEntry(account: a, folder: f, favourite: true, starred: true, badge: f.unread))
        }
    }
    if rows.isEmpty {
        return []
    }
    return [FolderEntry(header: true, favourite: true)] + rows
}

/// Orders one account's folders (model.go `folderTree`; see `sortFolders`),
/// leaving out what `visibleFolders` hides and everything below a collapsed
/// folder. Pinned folders are marked starred.
public func folderTree(
    _ a: Account, _ folders: [Folder], _ collapsed: CollapseState, _ favourites: FavouriteState
) -> [FolderEntry] {
    let list = visibleFolders(folders)
    let byID = Set(list.map(\.id))
    var children: [FolderID: [Folder]] = [:]
    var roots: [Folder] = []
    for f in list {
        guard let parent = f.parentId, !parent.rawValue.isEmpty, parent != f.id, byID.contains(parent) else {
            roots.append(f)
            continue
        }
        children[parent, default: []].append(f)
    }
    roots = sortSiblings(roots)
    for id in children.keys {
        children[id] = sortSiblings(children[id] ?? [])
    }
    // The rows of an account either all reserve the arrow's width or none
    // do, so a flat mailbox keeps the layout it had before folding existed.
    let nested = !children.isEmpty

    var out: [FolderEntry] = []
    out.reserveCapacity(list.count)
    var visited = Set<FolderID>()

    func markSubtree(_ f: Folder) {
        // Records f and everything below it as visited, so a collapsed
        // branch is not mistaken for an unreachable one.
        if visited.contains(f.id) {
            return
        }
        visited.insert(f.id)
        for c in children[f.id] ?? [] {
            markSubtree(c)
        }
    }

    func walk(_ f: Folder, _ depth: Int) {
        if visited.contains(f.id) || depth > maxFolderDepth {
            return
        }
        visited.insert(f.id)
        let kids = children[f.id] ?? []
        let key = FolderKey(account: a.id, folder: f.id)
        let fold = !kids.isEmpty && collapsed.folderCollapsed(key)
        out.append(FolderEntry(
            account: a, folder: f, depth: depth,
            starred: favourites.has(key),
            hasChildren: !kids.isEmpty, collapsed: fold, nested: nested,
            badge: badgeFor(list, f, collapsed: fold)
        ))
        if fold {
            // Mark the whole subtree seen, or the orphan sweep below would
            // list every hidden descendant flat.
            for c in kids {
                markSubtree(c)
            }
            return
        }
        for c in kids {
            walk(c, depth + 1)
        }
    }
    for r in roots {
        walk(r, 0)
    }

    // Whatever the walk did not reach sits in a parent cycle or below the
    // depth cap: list it flat, ordered like roots, indented by its path.
    let orphans = sortSiblings(list.filter { !visited.contains($0.id) })
    for f in orphans {
        out.append(FolderEntry(
            account: a, folder: f, depth: pathDepth(f.path),
            starred: favourites.has(FolderKey(account: a.id, folder: f.id)),
            nested: nested, badge: f.unread
        ))
    }
    return out
}

/// The number of separators in a display path (`strings.Count(path, "/")`),
/// counted over bytes so that a combining mark after a slash cannot hide it.
private func pathDepth(_ path: String) -> Int {
    path.utf8.reduce(0) { $0 + ($1 == UInt8(ascii: "/") ? 1 : 0) }
}

/// The unread count a row shows (model.go `badgeFor`): the folder's own,
/// plus every descendant's while the folder is collapsed and they are out
/// of sight. The walk is over the account's whole list, which is why it
/// tolerates parent cycles by visiting each folder at most once.
public func badgeFor(_ list: [Folder], _ f: Folder, collapsed: Bool) -> Int {
    if !collapsed {
        return f.unread
    }
    var children: [FolderID: [Folder]] = [:]
    for c in list {
        if let parent = c.parentId, !parent.rawValue.isEmpty, parent != c.id {
            children[parent, default: []].append(c)
        }
    }
    var total = f.unread
    var seen: Set<FolderID> = [f.id]
    func sum(_ id: FolderID) {
        for c in children[id] ?? [] {
            if seen.contains(c.id) {
                continue
            }
            seen.insert(c.id)
            total += c.unread
            sum(c.id)
        }
    }
    sum(f.id)
    return total
}

/// Orders folders at one tree level (model.go `sortSiblings`): special-use
/// roles first (Inbox, Drafts, Sent, …), then alphabetically by path,
/// case-insensitively. Stable, so equal keys keep the server's order.
public func sortSiblings(_ list: [Folder]) -> [Folder] {
    list.sorted { a, b in
        let ra = roleRank(a.role), rb = roleRank(b.role)
        if ra != rb {
            return ra < rb
        }
        return a.path.lowercased() < b.path.lowercased()
    }
}

/// The sidebar order of special-use folders; ordinary folders sort last
/// (model.go `roleRank`).
public func roleRank(_ r: FolderRole) -> Int {
    switch r {
    case .inbox: return 0
    case .drafts: return 1
    case .sent: return 2
    case .archive: return 3
    case .junk: return 4
    case .trash: return 5
    case .outbox: return 6
    case .all: return 7
    default: return 100
    }
}

/// The GTK symbolic icon name for a folder role (model.go `roleIcon`). The
/// AppKit layer maps these names to SF Symbols; the names are kept so the
/// mapping table has one key per GTK icon.
public func roleIcon(_ r: FolderRole) -> String {
    switch r {
    case .inbox: return "mail-unread-symbolic"
    case .drafts: return "document-edit-symbolic"
    case .sent, .outbox: return "mail-send-symbolic"
    case .trash: return "user-trash-symbolic"
    case .junk: return "mail-mark-junk-symbolic"
    case .archive: return "folder-download-symbolic"
    default: return "folder-symbolic"
    }
}

/// The display name of a folder (folders.go `folderTitle`): the localised
/// name for a role folder (whatever the server calls it), the server's name
/// otherwise. Plain text either way.
public func folderTitle(_ f: Folder) -> String {
    switch f.role {
    case .inbox: return L10n.C("folder", "Inbox")
    case .drafts: return L10n.C("folder", "Drafts")
    case .sent: return L10n.C("folder", "Sent")
    case .trash: return L10n.C("folder", "Trash")
    case .junk: return L10n.C("folder", "Junk")
    case .archive: return L10n.C("folder", "Archive")
    case .all: return L10n.C("folder", "All Mail")
    case .outbox: return L10n.C("folder", "Outbox")
    default: return f.name
    }
}

/// `initialFolder` over the rows of one section (model.go `firstFolder`):
/// the Favourites rows when `favourite` is set, the tree rows otherwise. The
/// first Inbox wins, else the first selectable folder.
public func firstFolder(_ entries: [FolderEntry], favourite: Bool) -> FolderKey? {
    var first: FolderKey?
    for e in entries {
        guard !e.header, e.favourite == favourite, let folder = e.folder, folder.selectable, let k = e.key else {
            continue
        }
        if folder.role == .inbox {
            return k
        }
        if first == nil {
            first = k
        }
    }
    return first
}
