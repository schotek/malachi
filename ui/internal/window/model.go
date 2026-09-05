// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"sort"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/widget"
)

// This file is the window's view model: plain Go, no GTK. It mirrors what
// the backend returned (accounts, folders, the current message page) so the
// widgets can be rebuilt from it and optimistic updates have one place to
// live. Nothing here decides anything about mail; it only caches API data.

// folderKey identifies a folder across accounts.
type folderKey struct {
	Account api.AccountID
	Folder  api.FolderID
}

// folderEntry is one row of the sidebar: an account header (Header set,
// Folder zero), the Favourites heading (Header and Favourite set, Account
// zero too) or a folder at the given tree depth.
type folderEntry struct {
	Header  bool
	Account api.Account
	Folder  api.Folder
	Depth   int

	// Favourite is set on the rows of the Favourites section at the top of
	// the sidebar: its heading and one row per pinned folder, each at depth
	// 0 without children. The same folder has a second row in its account's
	// tree, so a folder key alone does not name a row (rowKey does).
	Favourite bool
	// Starred means the folder is pinned: its star is filled, on the row in
	// the section and on the one in the tree alike.
	Starred bool

	// HasChildren means the row can be folded; Collapsed means it currently
	// is, and its descendants are left out of the list entirely.
	HasChildren bool
	Collapsed   bool
	// Nested is set on every row of an account whose tree has at least one
	// parent, so childless rows can reserve the width of the arrow and their
	// titles line up. An account with a flat folder list looks as before.
	Nested bool
	// Badge is the number the row shows: the folder's own unread count, or
	// that plus every hidden descendant's while it is collapsed. Unread mail
	// folded away is still visible on the ancestor.
	Badge int
}

// mailModel holds what the main window currently shows.
//
// The generation counters guard asynchronous replies: a caller bumps the
// counter before starting a request and ignores the reply when the counter
// moved on (folder changed, connection dropped, …).
type mailModel struct {
	accounts  []api.Account
	folders   map[api.AccountID][]api.Folder
	folderErr map[api.AccountID]error
	entries   []folderEntry
	selected  folderKey
	// selectedFav says which of the selected folder's rows carries the
	// highlight: the one in the Favourites section or the one in the tree,
	// whichever the user clicked last. Display only; selected is the folder.
	selectedFav bool
	// collapsed is which sidebar nodes are folded away (collapse.go);
	// favourites which folders are pinned to the top (favourites.go).
	collapsed  collapseState
	favourites favouriteState

	// listFolder is the folder messages belong to (or are being loaded
	// for); it lags selected between selectFolder and loadMessages.
	listFolder folderKey
	messages   []api.MessageSummary
	index      map[api.MessageID]int
	nextCursor string
	total      int // -1 when the backend could not compute it
	listErr    error

	listGen, bodyGen, foldersGen uint64
	loading, loadingMore         bool
}

// clearMessages empties the list (folder switch, disconnect) without
// touching the generation counters.
func (m *mailModel) clearMessages() {
	m.setMessages(nil, api.PageInfo{Total: -1})
}

// bumpAll invalidates every in-flight reply and clears the loading flags
// (connection lost).
func (m *mailModel) bumpAll() {
	m.bumpList()
	m.bumpBody()
	m.bumpFolders()
	m.loading = false
	m.loadingMore = false
}

// accountLabel is the sidebar header text for an account: its configured
// name, falling back to the address. Both are user-entered, shown as plain
// text.
func accountLabel(a api.Account) string {
	if name := strings.TrimSpace(a.Config.Name); name != "" {
		return name
	}
	return strings.TrimSpace(a.Config.Email)
}

// maxFolderDepth bounds the sidebar tree; deeper (or cyclic) chains fall
// back to a depth derived from the display path.
const maxFolderDepth = 32

// rebuildEntries recomputes the sidebar rows from accounts and folders.
func (m *mailModel) rebuildEntries() {
	m.entries = sortFolders(m.accounts, m.folders, m.collapsed, m.favourites)
}

// refreshBadges recomputes the number every visible row shows, after an
// unread count moved. A collapsed row sums its hidden descendants, so one
// changed folder can move an ancestor's badge and a single-row update is not
// enough.
func (m *mailModel) refreshBadges() {
	for i := range m.entries {
		e := &m.entries[i]
		if e.Header {
			continue
		}
		e.Badge = badgeFor(m.folders[e.Account.ID], e.Folder, e.Collapsed)
	}
}

// setMessages replaces the list with a first page. Duplicate IDs (which a
// well-behaved backend never sends) keep their first occurrence.
func (m *mailModel) setMessages(list []api.MessageSummary, page api.PageInfo) {
	m.messages = m.messages[:0]
	m.index = make(map[api.MessageID]int, len(list))
	for _, s := range list {
		if _, dup := m.index[s.ID]; dup {
			continue
		}
		m.index[s.ID] = len(m.messages)
		m.messages = append(m.messages, s)
	}
	m.nextCursor = page.NextCursor
	m.total = page.Total
	m.listErr = nil
}

// appendMessages adds a further page, skipping messages already listed
// (a message inserted from notify.newMessage may show up again in a page).
// It returns how many rows were added.
func (m *mailModel) appendMessages(list []api.MessageSummary, page api.PageInfo) (added int) {
	if m.index == nil {
		m.index = make(map[api.MessageID]int, len(list))
	}
	for _, s := range list {
		if _, dup := m.index[s.ID]; dup {
			continue
		}
		m.index[s.ID] = len(m.messages)
		m.messages = append(m.messages, s)
		added++
	}
	m.nextCursor = page.NextCursor
	m.total = page.Total
	return added
}

// insertMessage places s at idx (clamped to the list bounds) and reports
// whether it was inserted; an ID already in the list is left alone.
func (m *mailModel) insertMessage(idx int, s api.MessageSummary) bool {
	if m.index == nil {
		m.index = make(map[api.MessageID]int)
	}
	if _, dup := m.index[s.ID]; dup {
		return false
	}
	if idx < 0 {
		idx = 0
	}
	if idx > len(m.messages) {
		idx = len(m.messages)
	}
	m.messages = append(m.messages, api.MessageSummary{})
	copy(m.messages[idx+1:], m.messages[idx:])
	m.messages[idx] = s
	m.reindex(idx)
	if m.total >= 0 {
		m.total++
	}
	return true
}

// removeMessage drops id from the list and returns the removed summary and
// the index it had (the natural place to select a neighbour afterwards).
func (m *mailModel) removeMessage(id api.MessageID) (s api.MessageSummary, idx int, ok bool) {
	idx, ok = m.index[id]
	if !ok {
		return api.MessageSummary{}, -1, false
	}
	s = m.messages[idx]
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
	delete(m.index, id)
	m.reindex(idx)
	if m.total > 0 {
		m.total--
	}
	return s, idx, true
}

// reindex refreshes index for the rows from idx onwards.
func (m *mailModel) reindex(idx int) {
	for i := idx; i < len(m.messages); i++ {
		m.index[m.messages[i].ID] = i
	}
}

// messageAt returns the summary at list position idx.
func (m *mailModel) messageAt(idx int) (api.MessageSummary, bool) {
	if idx < 0 || idx >= len(m.messages) {
		return api.MessageSummary{}, false
	}
	return m.messages[idx], true
}

// message returns the summary with the given ID and its list position.
func (m *mailModel) message(id api.MessageID) (s api.MessageSummary, idx int, ok bool) {
	idx, ok = m.index[id]
	if !ok {
		return api.MessageSummary{}, -1, false
	}
	return m.messages[idx], idx, true
}

// updateFlags applies a flag change to the cached summary (optimistic
// update or server notification) and reports whether anything changed.
// A flag in both set and clear ends up set.
func (m *mailModel) updateFlags(id api.MessageID, set, clear []api.Flag) bool {
	idx, ok := m.index[id]
	if !ok {
		return false
	}
	msg := &m.messages[idx]
	changed := false
	out := make([]api.Flag, 0, len(msg.Flags)+len(set))
	for _, f := range msg.Flags {
		if hasFlag(clear, f) && !hasFlag(set, f) {
			changed = true
			continue
		}
		out = append(out, f)
	}
	for _, f := range set {
		if !hasFlag(out, f) {
			out = append(out, f)
			changed = true
		}
	}
	if changed {
		msg.Flags = out
	}
	return changed
}

// folder looks a folder up by key.
func (m *mailModel) folder(k folderKey) (api.Folder, bool) {
	for _, f := range m.folders[k.Account] {
		if f.ID == k.Folder {
			return f, true
		}
	}
	return api.Folder{}, false
}

// folderListed reports whether the sidebar still lists k, whether or not a
// fold currently hides its row. It is the test for keeping a selection:
// folding a parent must not move it, but an account switched off, a folder
// gone from the server or an outbox that has just drained must.
func (m *mailModel) folderListed(k folderKey) bool {
	if k == (folderKey{}) {
		return false
	}
	a, ok := m.account(k.Account)
	if !ok || !a.Enabled {
		return false
	}
	for _, f := range visibleFolders(m.folders[k.Account]) {
		if f.ID == k.Folder {
			return true
		}
	}
	return false
}

// folderByRole returns the account's folder with the given special-use
// role (the first one when a server reports several).
func (m *mailModel) folderByRole(acc api.AccountID, role api.FolderRole) (api.Folder, bool) {
	for _, f := range m.folders[acc] {
		if f.Role == role {
			return f, true
		}
	}
	return api.Folder{}, false
}

// folderRole is the special-use role of folder k, RoleNone when the folder
// is unknown or plain.
func (m *mailModel) folderRole(k folderKey) api.FolderRole {
	if f, ok := m.folder(k); ok {
		return f.Role
	}
	return api.RoleNone
}

// inOutbox reports whether s is a queued outgoing message: the backend
// marks it with Outbox, and its folder is the account's outbox.
func (m *mailModel) inOutbox(s api.MessageSummary) bool {
	return s.Outbox != nil || m.folderRole(folderKey{Account: s.AccountID, Folder: s.FolderID}) == api.RoleOutbox
}

// visibleFolders drops the folders the sidebar does not list: an empty
// outbox (the folder exists from the first send on; it only earns a row
// while something is in it). m.folders keeps the full list so folderByRole
// still finds it.
func visibleFolders(list []api.Folder) []api.Folder {
	out := make([]api.Folder, 0, len(list))
	for _, f := range list {
		if f.Role == api.RoleOutbox && f.Total == 0 {
			continue
		}
		out = append(out, f)
	}
	return out
}

// adjustUnread changes the cached unread count of a folder by delta, never
// below zero. Both the folders map and the sidebar entries are updated so
// a later rebuild does not undo the change.
func (m *mailModel) adjustUnread(k folderKey, delta int) {
	list := m.folders[k.Account]
	for i := range list {
		if list[i].ID != k.Folder {
			continue
		}
		list[i].Unread += delta
		if list[i].Unread < 0 {
			list[i].Unread = 0
		}
		for j := range m.entries {
			e := &m.entries[j]
			if !e.Header && e.Account.ID == k.Account && e.Folder.ID == k.Folder {
				e.Folder.Unread = list[i].Unread
			}
		}
		// The folder may be hidden under a collapsed ancestor whose badge
		// counts it, so every badge is recomputed, not just this row's.
		m.refreshBadges()
		return
	}
}

// account looks an account up by ID.
func (m *mailModel) account(id api.AccountID) (api.Account, bool) {
	for _, a := range m.accounts {
		if a.ID == id {
			return a, true
		}
	}
	return api.Account{}, false
}

// enabledAccounts returns the accounts the sidebar shows, in list order.
func (m *mailModel) enabledAccounts() []api.Account {
	return enabledAccounts(m.accounts)
}

// initialFolder is the folder to select when nothing is selected yet: the
// first Inbox, otherwise the first selectable folder. The tree is searched
// before the Favourites section, which repeats folders of the tree: a pinned
// Inbox of a later account must not win over the first account's. The
// section only decides when the tree gave nothing (every account folded).
func (m *mailModel) initialFolder() (folderKey, bool) {
	if k, ok := firstFolder(m.entries, false); ok {
		return k, true
	}
	return firstFolder(m.entries, true)
}

// firstFolder is initialFolder over the rows of one section: the Favourites
// rows when favourite is set, the tree rows otherwise.
func firstFolder(entries []folderEntry, favourite bool) (folderKey, bool) {
	var first folderKey
	found := false
	for _, e := range entries {
		if e.Header || e.Favourite != favourite || !e.Folder.Selectable {
			continue
		}
		k := folderKey{Account: e.Account.ID, Folder: e.Folder.ID}
		if e.Folder.Role == api.RoleInbox {
			return k, true
		}
		if !found {
			first, found = k, true
		}
	}
	return first, found
}

// bumpList invalidates in-flight message.list replies.
func (m *mailModel) bumpList() uint64 { m.listGen++; return m.listGen }

// bumpBody invalidates in-flight message.get / message.body replies.
func (m *mailModel) bumpBody() uint64 { m.bodyGen++; return m.bodyGen }

// bumpFolders invalidates in-flight account.list / folder.list replies.
func (m *mailModel) bumpFolders() uint64 { m.foldersGen++; return m.foldersGen }

// hasFlag reports whether f is in flags.
func hasFlag(flags []api.Flag, f api.Flag) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// summaryMessage projects a list summary onto what a row displays.
func summaryMessage(s api.MessageSummary) widget.Message {
	return widget.Message{
		From:           s.From,
		Subject:        s.Subject,
		Snippet:        s.Snippet,
		Date:           s.Date,
		Unread:         !hasFlag(s.Flags, api.FlagSeen),
		Flagged:        hasFlag(s.Flags, api.FlagFlagged),
		HasAttachments: s.HasAttachments,
	}
}

// enabledAccounts filters accounts to the enabled ones, keeping order.
func enabledAccounts(accounts []api.Account) []api.Account {
	out := make([]api.Account, 0, len(accounts))
	for _, a := range accounts {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out
}

// sortFolders lays the sidebar out: the Favourites section when anything is
// pinned (favouriteSection), then the enabled accounts in list order, each
// preceded by a header row when there are at least two of them or a
// Favourites section above them, then the account's folders as a tree.
// Roots are ordered by (roleRank, path); the children of a folder follow
// it, ordered the same way. Depth comes from ParentID; folders whose parent
// chain is cyclic or deeper than maxFolderDepth are appended after the tree
// with a depth counted from their display path. Non-selectable containers
// are kept so their children have somewhere to hang.
func sortFolders(accounts []api.Account, folders map[api.AccountID][]api.Folder, collapsed collapseState, favourites favouriteState) []folderEntry {
	enabled := enabledAccounts(accounts)
	out := favouriteSection(enabled, folders, favourites)
	// A single account needs no heading of its own, unless a Favourites
	// section sits above its folders and the two would run into each other.
	headers := len(enabled) >= 2 || len(out) > 0
	for _, a := range enabled {
		// Only a header can fold a whole account away, and headers only
		// exist from two accounts on.
		folded := headers && collapsed.accountCollapsed(a.ID)
		if headers {
			out = append(out, folderEntry{
				Header: true, Account: a,
				HasChildren: true, Collapsed: folded,
			})
		}
		if folded {
			continue
		}
		out = append(out, folderTree(a, folders[a.ID], collapsed, favourites)...)
	}
	return out
}

// favouriteSection is the block at the top of the sidebar: a heading and
// one row per pinned folder, in tree order (accounts in list order, then
// roles, then names), each at depth 0 and without its children. A pin that
// does not resolve — the folder gone or renamed on the server, its account
// switched off, a container that cannot be opened, an outbox with nothing
// in it — is left out silently; when none resolves there is no section at
// all. Account folds do not apply here: the section is what stays in view
// while the tree is folded away.
func favouriteSection(enabled []api.Account, folders map[api.AccountID][]api.Folder, favourites favouriteState) []folderEntry {
	var rows []folderEntry
	for _, a := range enabled {
		var pinned []api.Folder
		for _, f := range visibleFolders(folders[a.ID]) {
			if f.Selectable && favourites.has(folderKey{Account: a.ID, Folder: f.ID}) {
				pinned = append(pinned, f)
			}
		}
		sortSiblings(pinned)
		for _, f := range pinned {
			rows = append(rows, folderEntry{Favourite: true, Starred: true, Account: a, Folder: f, Badge: f.Unread})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]folderEntry{{Header: true, Favourite: true}}, rows...)
}

// folderTree orders one account's folders (see sortFolders), leaving out
// what visibleFolders hides and everything below a collapsed folder. Pinned
// folders are marked Starred.
func folderTree(a api.Account, list []api.Folder, collapsed collapseState, favourites favouriteState) []folderEntry {
	list = visibleFolders(list)
	byID := make(map[api.FolderID]bool, len(list))
	for _, f := range list {
		byID[f.ID] = true
	}
	children := make(map[api.FolderID][]api.Folder)
	var roots []api.Folder
	for _, f := range list {
		if f.ParentID == "" || f.ParentID == f.ID || !byID[f.ParentID] {
			roots = append(roots, f)
			continue
		}
		children[f.ParentID] = append(children[f.ParentID], f)
	}
	sortSiblings(roots)
	for id := range children {
		sortSiblings(children[id])
	}
	// One row of an account either all reserve the arrow's width or none do,
	// so a flat mailbox keeps the layout it had before folding existed.
	nested := len(children) > 0

	out := make([]folderEntry, 0, len(list))
	visited := make(map[api.FolderID]bool, len(list))
	var walk func(f api.Folder, depth int)
	walk = func(f api.Folder, depth int) {
		if visited[f.ID] || depth > maxFolderDepth {
			return
		}
		visited[f.ID] = true
		kids := children[f.ID]
		fold := len(kids) > 0 && collapsed.folderCollapsed(folderKey{Account: a.ID, Folder: f.ID})
		out = append(out, folderEntry{
			Account: a, Folder: f, Depth: depth,
			HasChildren: len(kids) > 0,
			Collapsed:   fold,
			Nested:      nested,
			Badge:       badgeFor(list, f, fold),
			Starred:     favourites.has(folderKey{Account: a.ID, Folder: f.ID}),
		})
		if fold {
			// Mark the whole subtree seen, or the orphan sweep below would
			// list every hidden descendant flat.
			for _, c := range kids {
				markSubtree(children, c, visited)
			}
			return
		}
		for _, c := range kids {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}

	// Whatever the walk did not reach sits in a parent cycle or below the
	// depth cap: list it flat, ordered like roots, indented by its path.
	var orphans []api.Folder
	for _, f := range list {
		if !visited[f.ID] {
			orphans = append(orphans, f)
		}
	}
	sortSiblings(orphans)
	for _, f := range orphans {
		out = append(out, folderEntry{
			Account: a, Folder: f, Depth: strings.Count(f.Path, "/"),
			Nested: nested, Badge: f.Unread,
			Starred: favourites.has(folderKey{Account: a.ID, Folder: f.ID}),
		})
	}
	return out
}

// markSubtree records f and everything below it as visited, so a collapsed
// branch is not mistaken for an unreachable one.
func markSubtree(children map[api.FolderID][]api.Folder, f api.Folder, visited map[api.FolderID]bool) {
	if visited[f.ID] {
		return
	}
	visited[f.ID] = true
	for _, c := range children[f.ID] {
		markSubtree(children, c, visited)
	}
}

// badgeFor is the unread count a row shows: the folder's own, plus every
// descendant's while the folder is collapsed and they are out of sight. The
// walk is over the account's whole list, which is why it tolerates parent
// cycles by visiting each folder at most once.
func badgeFor(list []api.Folder, f api.Folder, collapsed bool) int {
	if !collapsed {
		return f.Unread
	}
	children := make(map[api.FolderID][]api.Folder, len(list))
	for _, c := range list {
		if c.ParentID != "" && c.ParentID != c.ID {
			children[c.ParentID] = append(children[c.ParentID], c)
		}
	}
	total := f.Unread
	seen := map[api.FolderID]bool{f.ID: true}
	var sum func(id api.FolderID)
	sum = func(id api.FolderID) {
		for _, c := range children[id] {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			total += c.Unread
			sum(c.ID)
		}
	}
	sum(f.ID)
	return total
}

// sortSiblings orders folders at one tree level: special-use roles first
// (Inbox, Drafts, Sent, …), then alphabetically by path.
func sortSiblings(list []api.Folder) {
	sort.SliceStable(list, func(i, j int) bool {
		ri, rj := roleRank(list[i].Role), roleRank(list[j].Role)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(list[i].Path) < strings.ToLower(list[j].Path)
	})
}

// roleRank is the sidebar order of special-use folders; ordinary folders
// sort last.
func roleRank(r api.FolderRole) int {
	switch r {
	case api.RoleInbox:
		return 0
	case api.RoleDrafts:
		return 1
	case api.RoleSent:
		return 2
	case api.RoleArchive:
		return 3
	case api.RoleJunk:
		return 4
	case api.RoleTrash:
		return 5
	case api.RoleOutbox:
		return 6
	case api.RoleAll:
		return 7
	}
	return 100
}

// roleIcon is the symbolic icon for a folder role. Adwaita ships no
// mail-inbox-symbolic or mail-archive-symbolic, hence mail-unread and
// folder-download for those two.
func roleIcon(r api.FolderRole) string {
	switch r {
	case api.RoleInbox:
		return "mail-unread-symbolic"
	case api.RoleDrafts:
		return "document-edit-symbolic"
	case api.RoleSent, api.RoleOutbox:
		return "mail-send-symbolic"
	case api.RoleTrash:
		return "user-trash-symbolic"
	case api.RoleJunk:
		return "mail-mark-junk-symbolic"
	case api.RoleArchive:
		return "folder-download-symbolic"
	}
	return "folder-symbolic"
}

// selfAddress is the account's own address, for Reply All exclusion.
func selfAddress(a api.Account) api.Address {
	return api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
}
