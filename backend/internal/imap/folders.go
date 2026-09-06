// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// statusOptions is what change detection compares between passes.
var statusOptions = imap.StatusOptions{NumMessages: true, UIDNext: true, UIDValidity: true, NumUnseen: true}

// discovered is the result of one LIST: the folder batch for
// store.UpsertFolders and, with LIST-STATUS, the status per mailbox.
type discovered struct {
	folders []store.Folder
	status  map[string]*imap.StatusData
}

// discoverFolders lists every mailbox of the account and maps it to the
// store's folder model: hierarchy from the delimiter (missing parents get a
// non-selectable placeholder), roles from SPECIAL-USE attributes or name
// heuristics, INBOX first and the rest by path. Subscription is honoured
// only when LIST-EXTENDED reports it; otherwise everything counts as
// subscribed (there is no LSUB in the client).
func discoverFolders(ctx context.Context, sess *session) (discovered, error) {
	opts := &imap.ListOptions{}
	if sess.caps.Has(imap.CapSpecialUse) {
		opts.ReturnSpecialUse = true
	}
	if sess.caps.Has(imap.CapListStatus) {
		so := statusOptions
		opts.ReturnStatus = &so
	}
	extended := sess.caps.Has(imap.CapListExtended)
	if extended {
		opts.ReturnSubscribed = true
	}

	var list []*imap.ListData
	err := sess.do(ctx, commandTimeout, func() error {
		var err error
		list, err = sess.List("", "*", opts).Collect()
		return err
	})
	if err != nil {
		return discovered{}, err
	}

	d := discovered{status: map[string]*imap.StatusData{}}
	byMailbox := map[string]*store.Folder{}
	var folders []*store.Folder
	add := func(f *store.Folder) {
		byMailbox[f.Mailbox] = f
		folders = append(folders, f)
	}
	for i, ld := range list {
		if i >= maxFolders {
			break
		}
		if ld == nil || ld.Mailbox == "" || hasAttr(ld.Attrs, imap.MailboxAttrNonExistent) {
			continue
		}
		mailbox := cleanField(ld.Mailbox, maxFieldBytes)
		if mailbox == "" || mailbox != ld.Mailbox || byMailbox[mailbox] != nil {
			// A name with control characters cannot be addressed safely;
			// a duplicate would make the upsert fail.
			continue
		}
		if virtualView(ld.Attrs) {
			// Gmail's Important and Starred: views of messages the other
			// folders already hold, and the store has no cross-folder
			// identity to fold them into.
			continue
		}
		delim := ""
		if ld.Delim != 0 {
			delim = string(ld.Delim)
		}
		f := &store.Folder{
			Mailbox:    mailbox,
			Delimiter:  delim,
			Selectable: !hasAttr(ld.Attrs, imap.MailboxAttrNoSelect),
			Subscribed: !extended || hasAttr(ld.Attrs, imap.MailboxAttrSubscribed),
			Role:       roleFromAttrs(ld.Attrs),
			// An \All folder holds every message the others already hold:
			// listed, a move target, never downloaded.
			Unsynced: hasAttr(ld.Attrs, imap.MailboxAttrAll),
		}
		add(f)
		if ld.Status != nil {
			st := *ld.Status
			d.status[mailbox] = &st
		}
	}

	// Hierarchy: parent from the delimiter; placeholders for missing parents.
	for i := 0; i < len(folders); i++ {
		f := folders[i]
		f.Name, f.ParentMailbox = splitMailbox(f.Mailbox, f.Delimiter)
		if f.ParentMailbox != "" && byMailbox[f.ParentMailbox] == nil && len(folders) < maxFolders {
			add(&store.Folder{Mailbox: f.ParentMailbox, Delimiter: f.Delimiter, Selectable: false, Subscribed: true, Role: api.RoleNone})
		}
	}
	for _, f := range folders {
		f.Path = displayPath(f.Mailbox, f.Delimiter)
		if strings.EqualFold(f.Mailbox, "INBOX") {
			f.Role = api.RoleInbox
		}
	}
	assignRoles(folders, byMailbox)
	if sess.caps.Has(capGmail) {
		gmailArchive(folders)
	}

	sort.SliceStable(folders, func(i, j int) bool {
		a, b := folders[i], folders[j]
		if (a.Role == api.RoleInbox) != (b.Role == api.RoleInbox) {
			return a.Role == api.RoleInbox
		}
		if la, lb := strings.ToLower(a.Path), strings.ToLower(b.Path); la != lb {
			return la < lb
		}
		return a.Path < b.Path
	})
	d.folders = make([]store.Folder, len(folders))
	for i, f := range folders {
		d.folders[i] = *f
	}
	return d, nil
}

// folderStatus issues STATUS for one mailbox.
func folderStatus(ctx context.Context, sess *session, mailbox string) (*imap.StatusData, error) {
	var st *imap.StatusData
	err := sess.do(ctx, commandTimeout, func() error {
		var err error
		st, err = sess.Status(mailbox, &statusOptions).Wait()
		return err
	})
	return st, err
}

func hasAttr(attrs []imap.MailboxAttr, want imap.MailboxAttr) bool {
	for _, a := range attrs {
		if strings.EqualFold(string(a), string(want)) {
			return true
		}
	}
	return false
}

// capGmail is the capability Gmail's IMAP announces its extensions with.
// Nothing of X-GM-EXT-1 itself is used; it says which server this is.
const capGmail = imap.Cap("X-GM-EXT-1")

// virtualView reports a folder that only shows messages held elsewhere
// (\Important, \Flagged): Gmail's Important and Starred.
func virtualView(attrs []imap.MailboxAttr) bool {
	return hasAttr(attrs, imap.MailboxAttrImportant) || hasAttr(attrs, imap.MailboxAttrFlagged)
}

// gmailArchive makes Gmail's All Mail the archive target: a message moved
// there loses its INBOX label, which is what Gmail calls archiving. A
// folder the server marks \Archive itself keeps precedence, and the
// folder stays unsynchronised whatever its role.
func gmailArchive(folders []*store.Folder) {
	for _, f := range folders {
		if f.Role == api.RoleArchive {
			return
		}
	}
	for _, f := range folders {
		if f.Role == api.RoleAll && f.Unsynced {
			f.Role = api.RoleArchive
			return
		}
	}
}

// roleFromAttrs maps RFC 6154 attributes to roles.
func roleFromAttrs(attrs []imap.MailboxAttr) api.FolderRole {
	for _, a := range attrs {
		switch strings.ToLower(string(a)) {
		case `\all`:
			return api.RoleAll
		case `\archive`:
			return api.RoleArchive
		case `\drafts`:
			return api.RoleDrafts
		case `\junk`:
			return api.RoleJunk
		case `\sent`:
			return api.RoleSent
		case `\trash`:
			return api.RoleTrash
		}
	}
	return api.RoleNone
}

// roleNames is the name heuristic for servers without SPECIAL-USE:
// normalised leaf names in English, Czech, German and French.
var roleNames = map[string]api.FolderRole{
	// sent
	"sent": api.RoleSent, "sent items": api.RoleSent, "sent mail": api.RoleSent, "sent messages": api.RoleSent,
	"odeslané": api.RoleSent, "odeslane": api.RoleSent, "odeslaná pošta": api.RoleSent, "odeslana posta": api.RoleSent,
	"odeslané zprávy": api.RoleSent, "gesendet": api.RoleSent, "gesendete elemente": api.RoleSent,
	"gesendete objekte": api.RoleSent, "gesendete nachrichten": api.RoleSent,
	"envoyés": api.RoleSent, "envoyes": api.RoleSent, "éléments envoyés": api.RoleSent,
	"elements envoyes": api.RoleSent, "messages envoyés": api.RoleSent,
	// drafts
	"drafts": api.RoleDrafts, "draft": api.RoleDrafts, "koncepty": api.RoleDrafts, "rozepsané": api.RoleDrafts,
	"rozepsane": api.RoleDrafts, "entwürfe": api.RoleDrafts, "entwurfe": api.RoleDrafts, "brouillons": api.RoleDrafts,
	// trash
	"trash": api.RoleTrash, "deleted": api.RoleTrash, "deleted items": api.RoleTrash, "deleted messages": api.RoleTrash,
	"bin": api.RoleTrash, "koš": api.RoleTrash, "kos": api.RoleTrash, "odstraněná pošta": api.RoleTrash,
	"odstranena posta": api.RoleTrash, "smazané": api.RoleTrash, "smazane": api.RoleTrash,
	"papierkorb": api.RoleTrash, "gelöschte elemente": api.RoleTrash, "geloschte elemente": api.RoleTrash,
	"gelöschte objekte": api.RoleTrash, "corbeille": api.RoleTrash, "éléments supprimés": api.RoleTrash,
	"elements supprimes": api.RoleTrash,
	// junk
	"junk": api.RoleJunk, "junk e-mail": api.RoleJunk, "junk email": api.RoleJunk, "junk mail": api.RoleJunk,
	"spam": api.RoleJunk, "bulk mail": api.RoleJunk, "nevyžádaná pošta": api.RoleJunk, "nevyzadana posta": api.RoleJunk,
	"nevyžádané": api.RoleJunk, "unerwünscht": api.RoleJunk, "unerwuenscht": api.RoleJunk,
	"courrier indésirable": api.RoleJunk, "indésirables": api.RoleJunk, "pourriel": api.RoleJunk,
	// archive
	"archive": api.RoleArchive, "archives": api.RoleArchive, "archiv": api.RoleArchive, "archív": api.RoleArchive,
	"archivováno": api.RoleArchive, "archivovane": api.RoleArchive, "archivované": api.RoleArchive,
}

// roleForName applies the heuristic to a leaf name.
func roleForName(name string) api.FolderRole {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.Join(strings.Fields(n), " ")
	return roleNames[n]
}

// assignRoles keeps at most one folder per role: SPECIAL-USE wins, then the
// name heuristic on top-level folders and children of INBOX, in list order.
func assignRoles(folders []*store.Folder, byMailbox map[string]*store.Folder) {
	taken := map[api.FolderRole]bool{}
	for _, f := range folders {
		if f.Role != "" && f.Role != api.RoleNone {
			if taken[f.Role] {
				f.Role = api.RoleNone
				continue
			}
			taken[f.Role] = true
		}
	}
	for _, f := range folders {
		if f.Role != "" && f.Role != api.RoleNone {
			continue
		}
		f.Role = api.RoleNone
		if f.ParentMailbox != "" {
			p := byMailbox[f.ParentMailbox]
			if p == nil || !strings.EqualFold(p.Mailbox, "INBOX") {
				continue
			}
		}
		if r := roleForName(f.Name); r != "" && !taken[r] {
			taken[r] = true
			f.Role = r
		}
	}
}

// splitMailbox returns the leaf name and the parent mailbox.
func splitMailbox(mailbox, delim string) (name, parent string) {
	if delim == "" {
		return mailbox, ""
	}
	trimmed := strings.TrimRight(mailbox, delim)
	if trimmed == "" {
		return mailbox, ""
	}
	i := strings.LastIndex(trimmed, delim)
	if i < 0 {
		return trimmed, ""
	}
	return trimmed[i+len(delim):], trimmed[:i]
}

// displayPath is the "/"-separated form of a mailbox name.
func displayPath(mailbox, delim string) string {
	if delim == "" || delim == "/" {
		return mailbox
	}
	return strings.ReplaceAll(mailbox, delim, "/")
}
