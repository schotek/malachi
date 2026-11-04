// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The subset of Microsoft Graph's mail resources the engine reads.

type page[T any] struct {
	NextLink  string `json:"@odata.nextLink"`
	DeltaLink string `json:"@odata.deltaLink"`
	Value     []T    `json:"value"`
}

type mailFolder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ParentFolderID   string `json:"parentFolderId"`
	ChildFolderCount int    `json:"childFolderCount"`
	TotalItemCount   int    `json:"totalItemCount"`
	UnreadItemCount  int    `json:"unreadItemCount"`
	IsHidden         bool   `json:"isHidden"`
}

type emailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type recipient struct {
	EmailAddress emailAddress `json:"emailAddress"`
}

type followupFlag struct {
	FlagStatus string `json:"flagStatus"` // notFlagged | complete | flagged
}

type removed struct {
	Reason string `json:"reason"` // "deleted" (also for a move out of the folder)
}

// message is a delta/list entry; Removed is set for a tombstone.
type message struct {
	ID                string        `json:"id"`
	InternetMessageID string        `json:"internetMessageId"`
	Subject           string        `json:"subject"`
	From              *recipient    `json:"from"`
	ToRecipients      []recipient   `json:"toRecipients"`
	CCRecipients      []recipient   `json:"ccRecipients"`
	ReplyTo           []recipient   `json:"replyTo"`
	ReceivedDateTime  time.Time     `json:"receivedDateTime"`
	SentDateTime      time.Time     `json:"sentDateTime"`
	IsRead            bool          `json:"isRead"`
	IsDraft           bool          `json:"isDraft"`
	HasAttachments    bool          `json:"hasAttachments"`
	ConversationID    string        `json:"conversationId"`
	ParentFolderID    string        `json:"parentFolderId"`
	Flag              *followupFlag `json:"flag"`
	Removed           *removed      `json:"@removed"`
}

// messageSelect is the $select of delta and list requests: what the
// summary needs, never the body (the raw MIME is fetched separately).
const messageSelect = "id,internetMessageId,subject,from,toRecipients,ccRecipients,replyTo," +
	"receivedDateTime,sentDateTime,isRead,isDraft,hasAttachments,conversationId,parentFolderId,flag"

// flagsOf maps Graph's read/flag state onto IMAP-style flags.
func flagsOf(m message) []api.Flag {
	var flags []api.Flag
	if m.IsRead {
		flags = append(flags, api.FlagSeen)
	}
	if m.Flag != nil && m.Flag.FlagStatus == "flagged" {
		flags = append(flags, api.FlagFlagged)
	}
	if m.IsDraft {
		flags = append(flags, api.FlagDraft)
	}
	return flags
}

func addressesOf(rs []recipient) []api.Address {
	if len(rs) == 0 {
		return nil
	}
	out := make([]api.Address, 0, len(rs))
	for _, r := range rs {
		if r.EmailAddress.Address == "" && r.EmailAddress.Name == "" {
			continue
		}
		out = append(out, api.Address{Name: r.EmailAddress.Name, Address: r.EmailAddress.Address})
	}
	return out
}
