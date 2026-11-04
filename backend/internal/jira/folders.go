// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The folders of an issue-tracker account come from its configuration,
// not from the site: first the fixed views (Folder.Virtual; role none, an
// English name the clients replace by their own for the code) that
// JiraConfig.DisabledFolders leaves, then one folder per selected space,
// named as the site names the space. Mailboxes are stable codes —
// "view:<code>" and "space:<space id>" — so a renamed space or a
// re-enabled view keeps (or gets back) the same folder rows, and
// UpsertFolders drops the folder of a deselected space or a disabled view
// with its rows.

// viewOrder is the order of the views; viewNames their English names.
var (
	viewOrder = []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualWatching, api.VirtualOpen}
	viewNames = map[api.VirtualFolder]string{
		api.VirtualAssignedToMe: "Assigned to Me",
		api.VirtualWatching:     "Watching",
		api.VirtualOpen:         "Open",
	}
)

const (
	viewMailboxPrefix  = "view:"
	spaceMailboxPrefix = "space:"
	// windowPrefix + days is a space folder's delta_link once a pass
	// enumerated the account's window for it.
	windowPrefix = "window:"
)

// enabledViews are the views the configuration shows, in viewOrder.
func enabledViews(cfg api.JiraConfig) []api.VirtualFolder {
	off := map[api.VirtualFolder]bool{}
	for _, v := range cfg.DisabledFolders {
		off[v] = true
	}
	var out []api.VirtualFolder
	for _, v := range viewOrder {
		if !off[v] {
			out = append(out, v)
		}
	}
	return out
}

// folderBatch is the account's folder list: the enabled views, then the
// configured spaces by name. listed are the site's spaces by id (their
// current names); a space the site did not list keeps the name the
// configuration has, else its key.
func folderBatch(cfg api.JiraConfig, listed map[string]Space) []store.Folder {
	var out []store.Folder
	for _, v := range enabledViews(cfg) {
		name := viewNames[v]
		out = append(out, store.Folder{
			Mailbox: viewMailboxPrefix + string(v), Name: name, Path: name,
			Role: api.RoleNone, Subscribed: true, Selectable: true, Virtual: v,
		})
	}
	type named struct {
		ref  api.SpaceRef
		name string
	}
	var spaces []named
	seen := map[string]bool{}
	for _, sp := range cfg.Spaces {
		if sp.ID == "" || seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		name := sp.Name
		if l, ok := listed[sp.ID]; ok && l.Name != "" {
			name = l.Name
		}
		name = cleanText(name, maxNameBytes)
		if name == "" {
			name = sp.Key
		}
		if name == "" {
			name = sp.ID
		}
		spaces = append(spaces, named{sp, name})
	}
	sort.SliceStable(spaces, func(i, j int) bool {
		a, b := strings.ToLower(spaces[i].name), strings.ToLower(spaces[j].name)
		if a != b {
			return a < b
		}
		return spaces[i].ref.Key < spaces[j].ref.Key
	})
	for _, sp := range spaces {
		out = append(out, store.Folder{
			Mailbox: spaceMailboxPrefix + sp.ref.ID, Name: sp.name, Path: sp.name,
			Role: api.RoleNone, Subscribed: true, Selectable: true,
		})
	}
	return out
}

// windowOf reads the window a space folder was enumerated for; 0 when
// none was.
func windowOf(f store.Folder) int {
	s, ok := strings.CutPrefix(f.DeltaLink, windowPrefix)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// upsertFolders stores the account's folders and sorts them into the
// pass: the space folders by space id, the view folders by code.
func (s *Syncer) upsertFolders(ctx context.Context, p *pass) error {
	stored, removed, err := s.deps.Store.UpsertFolders(ctx, s.account.ID, folderBatch(p.cfg, p.listed))
	if err != nil {
		return storageError(err)
	}
	if len(removed) > 0 {
		s.log.Info("jira folders removed", "count", len(removed))
	}
	p.spaceFolders = map[string]store.Folder{}
	p.allSpaceFolders = map[string]store.Folder{}
	p.views = map[api.VirtualFolder]store.Folder{}
	for _, f := range stored {
		switch {
		case f.Virtual != "":
			p.views[f.Virtual] = f
		case strings.HasPrefix(f.Mailbox, spaceMailboxPrefix):
			id := strings.TrimPrefix(f.Mailbox, spaceMailboxPrefix)
			p.allSpaceFolders[id] = f
			if _, visible := p.listed[id]; visible {
				p.spaceFolders[id] = f
			}
		}
	}
	return nil
}
