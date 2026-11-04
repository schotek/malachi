// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// CRM accounts and saving mail to a CRM (docs/api.md §4.17; the design is
// docs/eway.md). The only CRM is eWay-CRM (AccountEway). Every string of
// these types that comes from the CRM (names, details) is untrusted
// display text: cleaned and capped by the daemon and shown as plain text.
// Every URL is built by the daemon from the account's stored service URL
// and an item GUID of fixed shape, never taken from the CRM.

// ---------------------------------------------------------------------------
// Limits (docs/eway.md D16). Exceeding an input limit is invalidArgument.
// The Windows and Swift mirrors check these values.
// ---------------------------------------------------------------------------

const (
	MaxCrmMessages        = 20       // CrmLookupParams.MessageIDs, CrmSaveParams.MessageIDs
	MaxCrmItems           = 50       // CrmSaveParams: len(Link) + len(Unlink) + Superior
	MaxCrmSuggestions     = 50       // CrmLookupResult.Suggestions
	MaxCrmSearchResults   = 50       // CrmSearchResult.Items
	MinCrmQueryBytes      = 2        // CrmSearchParams.Query, after trimming
	MaxCrmQueryBytes      = 200      // CrmSearchParams.Query
	MaxCrmLookupAddresses = 20       // addresses crm.lookup searches for
	MaxCrmNoteBytes       = 32 << 10 // the plain text sent with a saved message
	MaxCrmNameBytes       = 256      // CrmItem.Name
	MaxCrmDetailBytes     = 512      // CrmItem.Detail
)

// EwayConfig is present only when Kind == AccountEway.
type EwayConfig struct {
	// ServiceURL is the instance's web service address, normalised
	// (account.detectEway): https, no user info, query or fragment, no
	// trailing "/API.svc" and no trailing slash
	// ("https://hosting.eway-crm.com/company").
	ServiceURL string `json:"serviceUrl"`
	// Username is the eWay-CRM user name the password belongs to.
	Username string `json:"username"`
	// UserGUID is the eWay-CRM user's item GUID, set by the daemon after
	// the first sign-in; clients leave it as they got it.
	UserGUID string `json:"userGuid,omitempty"`
	// FollowThreads: later messages of a conversation the user saved are
	// saved by the daemon too, with the same links (docs/eway.md D22).
	// Always sent; clients send true for a new account. A configuration
	// without the field (an older client) is false: nothing automatic.
	FollowThreads bool `json:"followThreads"`
}

// CrmItemKind is the kind of a CRM item a saved message is linked to.
type CrmItemKind string

const (
	CrmContact CrmItemKind = "contact"
	CrmCompany CrmItemKind = "company"
	CrmDeal    CrmItemKind = "deal"    // eWay-CRM Leads
	CrmProject CrmItemKind = "project" // eWay-CRM Projects
)

// CrmReason says why crm.lookup or crm.search offers an item.
type CrmReason string

const (
	CrmReasonAddress CrmReason = "address" // an address of the messages is the item's e-mail
	CrmReasonDomain  CrmReason = "domain"  // a company tracking the address's domain
	CrmReasonContact CrmReason = "contact" // related to a found contact (its company, deal or project)
	CrmReasonCompany CrmReason = "company" // related to a found company (its deal or project)
	CrmReasonSearch  CrmReason = "search"  // a crm.search result
	CrmReasonSaved   CrmReason = "saved"   // already linked to the saved message
)

// CrmItem is one item of the CRM. Name and Detail are untrusted display
// text (Detail: a contact's e-mail and company, a deal's or project's
// state). URL opens the item in the CRM's web or desktop application.
type CrmItem struct {
	Kind   CrmItemKind `json:"kind"`
	GUID   string      `json:"guid"`
	Name   string      `json:"name"`
	Detail string      `json:"detail,omitempty"`
	Reason CrmReason   `json:"reason,omitempty"`
	// Superior: the item is the saved message's superior item (a deal or
	// project, eWay-CRM's OUTLOOKPROJECT relation); in suggestions, the
	// one deal or project the client pre-checks as superior.
	Superior bool   `json:"superior,omitempty"`
	URL      string `json:"url"`
}

// CrmItemRef names an item in crm.save.
type CrmItemRef struct {
	Kind CrmItemKind `json:"kind"`
	GUID string      `json:"guid"`
}

// CrmRecord is a message saved in a CRM account: the CRM's e-mail item
// and what it is linked to, as the daemon last read it from the server.
type CrmRecord struct {
	AccountID AccountID `json:"accountId"` // the eway account
	ItemGUID  string    `json:"itemGuid"`
	SavedAt   time.Time `json:"savedAt"`
	CheckedAt time.Time `json:"checkedAt"`
	Private   bool      `json:"private"`
	// Auto: the daemon saved the message by itself, following a saved
	// conversation (EwayConfig.FollowThreads).
	Auto  bool      `json:"auto,omitempty"`
	Items []CrmItem `json:"items"`
	URL   string    `json:"url"`
}

// AccountDetectEwayParams asks about an eWay-CRM instance before an
// account exists. ServiceURL is what the user typed or pasted. Nothing is
// stored and nothing is authenticated.
type AccountDetectEwayParams struct {
	ServiceURL string `json:"serviceUrl"`
}

// AccountDetectEwayResult describes the instance.
type AccountDetectEwayResult struct {
	ServiceURL string `json:"serviceUrl"` // normalised, as EwayConfig.ServiceURL
	// MicrosoftSignIn: the instance signs in through Microsoft Entra,
	// which this version cannot do; account.add would answer
	// crmSignInUnsupported.
	MicrosoftSignIn bool   `json:"microsoftSignIn"`
	Version         string `json:"version,omitempty"` // untrusted display text
}

// CrmSignInReason says why an instance cannot be signed in to
// (CodeCrmSignInUnsupported, CrmSignInUnsupportedData).
type CrmSignInReason string

const (
	CrmSignInMicrosoft CrmSignInReason = "microsoft" // Microsoft Entra sign-in
	CrmSignInPlan      CrmSignInReason = "plan"      // the plan has no API access
)

// CrmSignInUnsupportedData is Error.Data of CodeCrmSignInUnsupported.
type CrmSignInUnsupportedData struct {
	Reason CrmSignInReason `json:"reason"`
}

// MessagesNotDownloadedData is Error.Data of CodePartNotDownloaded from
// crm.save: the messages not whole on this device; message.download
// fetches each, then the client calls crm.save again.
type MessagesNotDownloadedData struct {
	MessageIDs []MessageID `json:"messageIds"`
}

// CrmLookupParams asks what the messages are linked to in the CRM and
// what they could be linked to. AccountID empty = the first enabled eway
// account in account.list order.
type CrmLookupParams struct {
	AccountID        AccountID   `json:"accountId,omitempty"`
	MessageAccountID AccountID   `json:"messageAccountId"`
	MessageIDs       []MessageID `json:"messageIds"` // 1..MaxCrmMessages
}

// CrmSavedMessage is whether one message of the lookup is saved; Record
// null = not saved in that account.
type CrmSavedMessage struct {
	MessageID MessageID  `json:"messageId"`
	Record    *CrmRecord `json:"record"`
}

// CrmLookupResult: Saved has one entry per requested message, in order.
// Suggestions are the items the addresses of the messages lead to,
// ranked, at most MaxCrmSuggestions; Partial: the CRM did not answer
// every lookup within the daemon's budget.
type CrmLookupResult struct {
	AccountID   AccountID         `json:"accountId"`
	Saved       []CrmSavedMessage `json:"saved"`
	Suggestions []CrmItem         `json:"suggestions"`
	Partial     bool              `json:"partial,omitempty"`
}

// CrmSearchParams searches the CRM's items by name. Kinds empty = all.
type CrmSearchParams struct {
	AccountID AccountID     `json:"accountId,omitempty"`
	Query     string        `json:"query"`
	Kinds     []CrmItemKind `json:"kinds,omitempty"`
}

// CrmSearchResult: at most MaxCrmSearchResults items, Truncated when
// there were more.
type CrmSearchResult struct {
	Items     []CrmItem `json:"items"`
	Truncated bool      `json:"truncated,omitempty"`
}

// CrmSaveParams saves messages of one mail account to the CRM, or changes
// the links of a saved one. Link items are added, Unlink items removed
// (only with one message, already saved); Superior is the deal or
// project linked as the superior item (nil: none added). Private makes
// the saved item visible to the user only.
type CrmSaveParams struct {
	AccountID        AccountID    `json:"accountId,omitempty"`
	MessageAccountID AccountID    `json:"messageAccountId"`
	MessageIDs       []MessageID  `json:"messageIds"` // 1..MaxCrmMessages
	Link             []CrmItemRef `json:"link,omitempty"`
	Unlink           []CrmItemRef `json:"unlink,omitempty"`
	Superior         *CrmItemRef  `json:"superior,omitempty"`
	Private          bool         `json:"private,omitempty"`
}

// CrmSaveOutcome is the result for one message: Record on success (with
// Created when this call created the CRM item), Error otherwise.
type CrmSaveOutcome struct {
	MessageID MessageID  `json:"messageId"`
	Record    *CrmRecord `json:"record,omitempty"`
	Created   bool       `json:"created,omitempty"`
	Error     *Error     `json:"error,omitempty"`
}

// CrmSaveResult has one outcome per requested message, in order.
type CrmSaveResult struct {
	Results []CrmSaveOutcome `json:"results"`
}

// CrmRemoveParams deletes a saved message's item from the CRM (for every
// user of the CRM) and the local record.
type CrmRemoveParams struct {
	AccountID        AccountID `json:"accountId,omitempty"`
	MessageAccountID AccountID `json:"messageAccountId"`
	MessageID        MessageID `json:"messageId"`
}

type CrmRemoveResult struct{}
