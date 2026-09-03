// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// ---------------------------------------------------------------------------
// Identifiers
// ---------------------------------------------------------------------------

// AccountID identifies a configured account. Opaque, stable across restarts.
type AccountID string

// FolderID identifies a folder within an account. Opaque; not the IMAP
// mailbox name, which may be renamed or contain a server-specific delimiter.
type FolderID string

// MessageID identifies a message in the local store. Opaque; not the RFC 5322
// Message-ID header, which is attacker-controlled and not unique.
type MessageID string

// ThreadID identifies a conversation thread within an account.
type ThreadID string

// DraftID identifies a locally stored draft.
type DraftID string

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// Page selects a slice of a list. Cursor is opaque and must be passed back
// unchanged; an empty cursor means "from the beginning".
type Page struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"` // 0 = server default; clamped to MaxPageLimit
}

// PageInfo is returned alongside every list. NextCursor is empty on the last
// page. Total is -1 when the backend cannot cheaply compute it.
type PageInfo struct {
	NextCursor string `json:"nextCursor,omitempty"`
	Total      int    `json:"total"`
}

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 500
)

// ---------------------------------------------------------------------------
// System
// ---------------------------------------------------------------------------

type SystemInfoParams struct{}

type SystemInfoResult struct {
	Version         string `json:"version"`         // daemon version string
	ProtocolVersion int    `json:"protocolVersion"` // see ProtocolVersion
	PID             int    `json:"pid"`
	StorePath       string `json:"storePath"`
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

// Security is the transport-security mode of an IMAP/SMTP connection.
type Security string

const (
	SecurityTLS      Security = "tls"      // implicit TLS (IMAPS 993 / SMTPS 465)
	SecuritySTARTTLS Security = "starttls" // upgrade on a plain port (143 / 587)
	SecurityNone     Security = "none"     // plaintext; allowed only for localhost/testing
)

// AuthMethod selects how the backend authenticates to a server.
type AuthMethod string

const (
	AuthPassword AuthMethod = "password" // PLAIN / LOGIN
	AuthOAuth2   AuthMethod = "oauth2"   // XOAUTH2 (Office 365 first; Gmail deferred)
)

// ServerConfig describes one endpoint (IMAP or SMTP).
type ServerConfig struct {
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	Security   Security   `json:"security"`
	Username   string     `json:"username"`
	AuthMethod AuthMethod `json:"authMethod"`
}

// OAuth2Config is present only when AuthMethod == AuthOAuth2.
type OAuth2Config struct {
	Provider string   `json:"provider"`           // "office365" | "custom"
	ClientID string   `json:"clientId,omitempty"` // bring-your-own-credentials
	TenantID string   `json:"tenantId,omitempty"`
	AuthURL  string   `json:"authUrl,omitempty"`
	TokenURL string   `json:"tokenUrl,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// AccountConfig is the non-secret part of an account. Secrets (passwords,
// refresh tokens) travel only in Credentials at add/test time and are stored
// in the system keyring. They are never returned by any method.
type AccountConfig struct {
	Name         string        `json:"name"`  // display name of the account
	Email        string        `json:"email"` // primary address
	DisplayName  string        `json:"displayName,omitempty"`
	IMAP         ServerConfig  `json:"imap"`
	SMTP         ServerConfig  `json:"smtp"`
	OAuth2       *OAuth2Config `json:"oauth2,omitempty"`
	SyncInterval int           `json:"syncIntervalSeconds,omitempty"` // 0 = default
}

// Credentials carries secrets for account.add / account.test. Exactly the
// fields relevant to the AuthMethod are set.
type Credentials struct {
	Password string `json:"password,omitempty"`
	// For OAuth2 the backend drives the flow itself and emits
	// notify.authRequired; nothing is passed here.
}

// Account is what account.list returns: config plus derived state.
type Account struct {
	ID      AccountID     `json:"id"`
	Config  AccountConfig `json:"config"`
	Enabled bool          `json:"enabled"`
	State   SyncState     `json:"state"`
}

type AccountListParams struct{}

type AccountListResult struct {
	Accounts []Account `json:"accounts"`
}

type AccountAddParams struct {
	Config      AccountConfig `json:"config"`
	Credentials Credentials   `json:"credentials"`
}

type AccountAddResult struct {
	AccountID AccountID `json:"accountId"`
}

type AccountRemoveParams struct {
	AccountID AccountID `json:"accountId"`
	// DeleteLocalData also purges the offline store for the account.
	DeleteLocalData bool `json:"deleteLocalData"`
}

type AccountRemoveResult struct{}

// AccountSetEnabledParams pauses (enabled=false) or resumes an account. A
// paused account keeps its configuration and local data, is never
// synchronised and reports SyncState.status "disabled".
type AccountSetEnabledParams struct {
	AccountID AccountID `json:"accountId"`
	Enabled   bool      `json:"enabled"`
}

type AccountSetEnabledResult struct{}

// AccountDiscoverParams asks for server settings matching an e-mail
// address. Only the domain leaves the machine for the ISPDB and DNS
// lookups; the provider's own autoconfig URL receives the address.
type AccountDiscoverParams struct {
	Email string `json:"email"`
}

// DiscoverSource says where a suggestion came from, from most to least
// trustworthy. When endpoints come from different sources the result
// reports the weakest.
type DiscoverSource string

const (
	DiscoverISPDB      DiscoverSource = "ispdb"      // Mozilla autoconfig database
	DiscoverAutoconfig DiscoverSource = "autoconfig" // the provider's own autoconfig document
	DiscoverSRV        DiscoverSource = "srv"        // RFC 6186 DNS SRV records
	DiscoverGuess      DiscoverSource = "guess"      // common host names verified by a TLS connection
	DiscoverNone       DiscoverSource = "none"       // nothing found; Config is omitted
)

// AccountDiscoverResult is a suggestion only: nothing is stored and
// nothing is authenticated. Config, when present, passes account.add
// validation with authMethod "password" and the username prefilled; the
// UI still asks for the password and should run account.test.
type AccountDiscoverResult struct {
	Config       *AccountConfig `json:"config,omitempty"`
	Source       DiscoverSource `json:"source"`
	ProviderName string         `json:"providerName,omitempty"` // display-only, untrusted text
}

// AccountTestParams tests connectivity without persisting anything.
type AccountTestParams struct {
	Config      AccountConfig `json:"config"`
	Credentials Credentials   `json:"credentials"`
}

// EndpointTestResult reports one endpoint. On failure Error is set.
type EndpointTestResult struct {
	OK           bool     `json:"ok"`
	Error        *Error   `json:"error,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	LatencyMS    int      `json:"latencyMs"`
}

type AccountTestResult struct {
	IMAP EndpointTestResult `json:"imap"`
	SMTP EndpointTestResult `json:"smtp"`
}

// ---------------------------------------------------------------------------
// Folders
// ---------------------------------------------------------------------------

// FolderRole is the special-use classification (RFC 6154 or heuristics).
type FolderRole string

const (
	RoleNone    FolderRole = "none"
	RoleInbox   FolderRole = "inbox"
	RoleSent    FolderRole = "sent"
	RoleDrafts  FolderRole = "drafts"
	RoleTrash   FolderRole = "trash"
	RoleJunk    FolderRole = "junk"
	RoleArchive FolderRole = "archive"
	RoleAll     FolderRole = "all"
	RoleOutbox  FolderRole = "outbox" // local-only queue of messages to send
)

type Folder struct {
	ID         FolderID   `json:"id"`
	AccountID  AccountID  `json:"accountId"`
	ParentID   FolderID   `json:"parentId,omitempty"`
	Name       string     `json:"name"` // leaf display name
	Path       string     `json:"path"` // full display path, "/"-separated
	Role       FolderRole `json:"role"`
	Subscribed bool       `json:"subscribed"`
	Selectable bool       `json:"selectable"` // false for \Noselect containers
	Unread     int        `json:"unread"`
	Total      int        `json:"total"`
}

type FolderListParams struct {
	AccountID AccountID `json:"accountId"`
	// IncludeUnsubscribed also returns folders the user has not subscribed to.
	IncludeUnsubscribed bool `json:"includeUnsubscribed,omitempty"`
}

type FolderListResult struct {
	Folders []Folder `json:"folders"`
}

type FolderSubscribeParams struct {
	AccountID  AccountID `json:"accountId"`
	FolderID   FolderID  `json:"folderId"`
	Subscribed bool      `json:"subscribed"`
}

type FolderSubscribeResult struct{}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// Flag is a message flag. Names are lower-case and stable; the backend maps
// them to IMAP system flags and keywords.
type Flag string

const (
	FlagSeen      Flag = "seen"
	FlagAnswered  Flag = "answered"
	FlagFlagged   Flag = "flagged"
	FlagDraft     Flag = "draft"
	FlagDeleted   Flag = "deleted"
	FlagJunk      Flag = "junk"
	FlagForwarded Flag = "forwarded"
)

// Address is a parsed RFC 5322 mailbox. Name may be empty. Both fields are
// attacker-controlled display data; UIs must not interpret them as markup.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// MessageSummary is the list-view projection of a message. It never contains
// body content beyond Snippet, which is plain text derived by the backend.
type MessageSummary struct {
	ID             MessageID `json:"id"`
	AccountID      AccountID `json:"accountId"`
	FolderID       FolderID  `json:"folderId"`
	ThreadID       ThreadID  `json:"threadId,omitempty"`
	From           []Address `json:"from"`
	To             []Address `json:"to,omitempty"`
	Subject        string    `json:"subject"`
	Date           time.Time `json:"date"` // RFC 3339; best-effort if the header is garbage
	Snippet        string    `json:"snippet"`
	Flags          []Flag    `json:"flags"`
	HasAttachments bool      `json:"hasAttachments"`
	Size           int64     `json:"size"`
}

// Attachment describes a MIME part the user can download. Content is fetched
// through a separate method in a later phase; only metadata crosses here now.
type Attachment struct {
	PartID      string `json:"partId"`
	Filename    string `json:"filename"` // sanitised: no path separators, no control chars
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Inline      bool   `json:"inline"` // referenced from the HTML body via cid:
	ContentID   string `json:"contentId,omitempty"`
}

// Message is the full header view of a message (message.get).
type Message struct {
	MessageSummary
	CC           []Address    `json:"cc,omitempty"`
	BCC          []Address    `json:"bcc,omitempty"`
	ReplyTo      []Address    `json:"replyTo,omitempty"`
	RFCMessageID string       `json:"rfcMessageId,omitempty"` // Message-ID header, display only
	InReplyTo    string       `json:"inReplyTo,omitempty"`
	References   []string     `json:"references,omitempty"`
	Attachments  []Attachment `json:"attachments"`
	// Headers is a curated subset of headers (List-Unsubscribe, Precedence,
	// Auto-Submitted, …) chosen by the backend. Never the raw header block.
	Headers map[string]string `json:"headers,omitempty"`
}

// SortOrder for message and thread lists.
type SortOrder string

const (
	SortDateDesc SortOrder = "dateDesc"
	SortDateAsc  SortOrder = "dateAsc"
)

type MessageListParams struct {
	AccountID AccountID `json:"accountId"`
	FolderID  FolderID  `json:"folderId"`
	Page      Page      `json:"page"`
	Sort      SortOrder `json:"sort,omitempty"`
	// UnreadOnly restricts the list to messages without the "seen" flag.
	UnreadOnly bool `json:"unreadOnly,omitempty"`
}

type MessageListResult struct {
	Messages []MessageSummary `json:"messages"`
	Page     PageInfo         `json:"page"`
}

type MessageGetParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
}

type MessageGetResult struct {
	Message Message `json:"message"`
}

// RemoteContentPolicy tells the backend how to treat references to remote
// resources (images, stylesheets, fonts) when producing the body.
type RemoteContentPolicy string

const (
	// RemoteBlock strips or neutralises every remote reference. Default.
	RemoteBlock RemoteContentPolicy = "block"
	// RemoteAllow keeps https: image references after the user explicitly
	// consented for this message. Scripts, forms, CSS and fonts are still
	// removed. There is no policy that disables sanitisation.
	RemoteAllow RemoteContentPolicy = "allow"
	// RemoteKnownSenders is valid only as the stored preference
	// (Preferences.RemoteContent): the backend resolves it per message to
	// RemoteAllow when every sender address is on the known-senders list
	// (sender.*), and to RemoteBlock otherwise. Decrypted content is always
	// RemoteBlock regardless of policy (docs/security.md §5).
	RemoteKnownSenders RemoteContentPolicy = "knownSenders"
)

type MessageBodyParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
	// RemoteContent overrides the stored preference for this call only.
	// Empty = use the stored policy (config.get); "block" and "allow" are the
	// only accepted overrides, "knownSenders" here is invalidArgument.
	RemoteContent RemoteContentPolicy `json:"remoteContent,omitempty"`
}

// BlockedContent summarises what the sanitiser removed or neutralised so the
// UI can show an honest "N remote images blocked" banner.
type BlockedContent struct {
	RemoteImages   int `json:"remoteImages"`
	RemoteStyles   int `json:"remoteStyles"`
	RemoteFonts    int `json:"remoteFonts"`
	Scripts        int `json:"scripts"`
	Forms          int `json:"forms"`
	EventHandlers  int `json:"eventHandlers"`
	DangerousURLs  int `json:"dangerousUrls"` // javascript:, data:text/html, vbscript:, …
	EmbeddedFrames int `json:"embeddedFrames"`
	TrackingPixels int `json:"trackingPixels"` // heuristically detected 1×1 remote images
}

// Link is an extracted hyperlink so the UI can display the real destination
// rather than the link text.
type Link struct {
	Text string `json:"text"`
	Href string `json:"href"` // normalised absolute URL; only http(s) and mailto survive
}

// MessageBodyResult carries the renderable content of a message.
//
// HTML is ALWAYS the sanitiser's output: no scripts, no event handlers, no
// javascript:/data: URLs, no external CSS, no forms, no frames, and remote
// references removed or rewritten according to RemoteContent. The UI renders
// it in a JavaScript-disabled webview with a strict CSP. Text is the plain
// text alternative, or a text rendering derived from HTML when the message
// has no text part.
type MessageBodyResult struct {
	MessageID MessageID      `json:"messageId"`
	HasHTML   bool           `json:"hasHtml"`
	HTML      string         `json:"html,omitempty"` // sanitised; empty when HasHTML is false
	Text      string         `json:"text"`
	Blocked   BlockedContent `json:"blocked"`
	Links     []Link         `json:"links"`
	// InlineParts maps cid: references present in HTML to attachment PartIDs.
	InlineParts map[string]string `json:"inlineParts,omitempty"`
	// SanitizerVersion identifies the sanitiser ruleset; bump on any rule change.
	SanitizerVersion string `json:"sanitizerVersion"`
}

type MessageFlagParams struct {
	AccountID  AccountID   `json:"accountId"`
	MessageIDs []MessageID `json:"messageIds"`
	Set        []Flag      `json:"set,omitempty"`
	Clear      []Flag      `json:"clear,omitempty"`
}

type MessageFlagResult struct{}

type MessageMoveParams struct {
	AccountID      AccountID   `json:"accountId"`
	MessageIDs     []MessageID `json:"messageIds"`
	TargetFolderID FolderID    `json:"targetFolderId"`
}

type MessageMoveResult struct{}

type MessageDeleteParams struct {
	AccountID  AccountID   `json:"accountId"`
	MessageIDs []MessageID `json:"messageIds"`
	// Permanent bypasses the Trash folder (expunge). Default: move to Trash.
	Permanent bool `json:"permanent,omitempty"`
}

type MessageDeleteResult struct{}

// ---------------------------------------------------------------------------
// Threads
// ---------------------------------------------------------------------------

type ThreadSummary struct {
	ID             ThreadID  `json:"id"`
	AccountID      AccountID `json:"accountId"`
	Subject        string    `json:"subject"` // normalised (Re:/Fwd: stripped)
	Participants   []Address `json:"participants"`
	MessageCount   int       `json:"messageCount"`
	UnreadCount    int       `json:"unreadCount"`
	LatestDate     time.Time `json:"latestDate"`
	Snippet        string    `json:"snippet"` // from the latest message
	Flags          []Flag    `json:"flags"`   // union of member flags
	HasAttachments bool      `json:"hasAttachments"`
	// FolderIDs lists every folder that contains at least one member.
	FolderIDs []FolderID `json:"folderIds"`
}

type ThreadListParams struct {
	AccountID AccountID `json:"accountId"`
	FolderID  FolderID  `json:"folderId"`
	Page      Page      `json:"page"`
	Sort      SortOrder `json:"sort,omitempty"`
}

type ThreadListResult struct {
	Threads []ThreadSummary `json:"threads"`
	Page    PageInfo        `json:"page"`
}

type ThreadGetParams struct {
	AccountID AccountID `json:"accountId"`
	ThreadID  ThreadID  `json:"threadId"`
}

type ThreadGetResult struct {
	Thread   ThreadSummary    `json:"thread"`
	Messages []MessageSummary `json:"messages"` // chronological
}

// ---------------------------------------------------------------------------
// Drafts and sending
// ---------------------------------------------------------------------------

// Limits enforced by draft.save and attachment.*. Exceeding one is
// invalidArgument unless noted. Exported so clients can pre-check.
const (
	MaxDraftBodyBytes       = 1 << 20  // textBody and htmlBody, each
	MaxDraftSubjectBytes    = 1024     // bytes
	MaxDraftRecipients      = 500      // to + cc + bcc
	MaxDraftAttachments     = 100      // per draft
	MaxAttachmentBytes      = 25 << 20 // one file (attachment.import) → attachmentTooBig
	MaxDraftAttachmentBytes = 25 << 20 // sum over a draft (draft.save) → attachmentTooBig
	MaxAttachmentDataBytes  = 16 << 20 // inline base64 payloads; the transport line cap is 32 MiB
)

// Draft is a message being composed. The backend is the authority for every
// derived field: it sanitises HTMLBody on the way in, derives TextBody from
// it, assigns attachment metadata and sets UpdatedAt.
type Draft struct {
	ID        DraftID   `json:"id,omitempty"` // empty on first save
	AccountID AccountID `json:"accountId"`
	// Version implements optimistic concurrency: draft.save fails with
	// CodeConflict when the stored version differs from the one supplied.
	// Ignored on the first save, which returns version 1.
	Version int       `json:"version"`
	To      []Address `json:"to"`
	CC      []Address `json:"cc,omitempty"`
	BCC     []Address `json:"bcc,omitempty"`
	Subject string    `json:"subject"`
	// TextBody is the message body as typed when HTMLBody is empty. When
	// HTMLBody is set the value sent to draft.save is ignored: the stored and
	// returned TextBody is the plain-text alternative the backend derives
	// from the sanitised HTML.
	TextBody string `json:"textBody"`
	// HTMLBody is the rich-text body. In draft.save params it is the
	// editor's HTML, treated as hostile (pasted web content) and run through
	// internal/sanitize in compose mode: scripts, forms, event handlers,
	// remote references and data: URLs are removed; <img src> survives only
	// as "cid:<contentId>" of one of this draft's inline attachments. Only
	// the sanitiser's output is stored, listed and sent. Empty = plain text.
	HTMLBody    string            `json:"htmlBody,omitempty"`
	InReplyTo   MessageID         `json:"inReplyTo,omitempty"`  // local ID; backend resolves headers
	Forwarding  MessageID         `json:"forwarding,omitempty"` // mutually exclusive with InReplyTo
	Attachments []DraftAttachment `json:"attachments,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"` // server-set; ignored in params
}

// DraftAttachment is a file in the backend's attachment store, created by
// attachment.import. In draft.save params only ID is read; every other
// field is backend-assigned.
type DraftAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`    // sanitised; never the raw client name
	ContentType string `json:"contentType"` // detected from content, not taken from the client
	Size        int64  `json:"size"`
	// Inline marks an image referenced from HTMLBody as "cid:<contentId>";
	// it is sent as Content-Disposition: inline inside multipart/related.
	// Only image/* may be inline.
	Inline    bool   `json:"inline"`
	ContentID string `json:"contentId,omitempty"` // backend-assigned, without angle brackets
}

type DraftSaveParams struct {
	Draft Draft `json:"draft"`
}

// DraftSaveResult echoes what the backend stored, which is what will be
// sent: the derived text, the sanitised HTML, what the sanitiser removed and
// the attachment list after reconciliation.
type DraftSaveResult struct {
	DraftID     DraftID           `json:"draftId"`
	Version     int               `json:"version"`
	TextBody    string            `json:"textBody"`
	HTMLBody    string            `json:"htmlBody,omitempty"`
	Blocked     BlockedContent    `json:"blocked"`
	Attachments []DraftAttachment `json:"attachments,omitempty"`
}

type DraftListParams struct {
	AccountID AccountID `json:"accountId"`
	Page      Page      `json:"page"`
}

type DraftListResult struct {
	Drafts []Draft  `json:"drafts"` // newest updatedAt first, full bodies
	Page   PageInfo `json:"page"`
}

type DraftDeleteParams struct {
	AccountID AccountID `json:"accountId"`
	DraftID   DraftID   `json:"draftId"`
}

type DraftDeleteResult struct{}

// ComposeMode selects how draft.create pre-fills a draft.
type ComposeMode string

const (
	ComposeNew      ComposeMode = "new"
	ComposeReply    ComposeMode = "reply"
	ComposeReplyAll ComposeMode = "replyAll"
	ComposeForward  ComposeMode = "forward"
)

// DraftCreateParams asks the backend for an unsaved template: recipients
// computed from the original (Reply-To/From/To/CC minus the account's own
// addresses), a Re:/Fwd: subject, the quoted sanitised body in both forms,
// forwarded attachments imported into the store, or a parsed mailto: URI.
// Reply and forward logic lives here, not in the UI (CLAUDE.md rule 1).
type DraftCreateParams struct {
	AccountID AccountID   `json:"accountId"`
	Mode      ComposeMode `json:"mode"`
	MessageID MessageID   `json:"messageId,omitempty"` // required unless Mode is ComposeNew
	Mailto    string      `json:"mailto,omitempty"`    // ComposeNew only
}

// DraftCreateResult.Draft has an empty ID and version 0; nothing is
// persisted until the first draft.save.
type DraftCreateResult struct {
	Draft Draft `json:"draft"`
}

// MessageSendParams queues a saved draft for delivery. Delivery is
// asynchronous: the result only confirms enqueueing; progress arrives via
// notify.syncState for the outbox folder.
type MessageSendParams struct {
	AccountID AccountID `json:"accountId"`
	DraftID   DraftID   `json:"draftId"`
	Version   int       `json:"version"`
}

type MessageSendResult struct {
	OutboxID MessageID `json:"outboxId"`
}

// ---------------------------------------------------------------------------
// Attachments (compose-side store)
// ---------------------------------------------------------------------------

// AttachmentImportParams copies a file into the backend's attachment store.
// Exactly one of Path and Data is set. Path must be absolute and name a
// regular file (the UI obtains it from the FileChooser portal; both
// processes share the sandbox). Data carries pasted or dragged content and
// is capped by MaxAttachmentDataBytes; larger content must go through Path.
type AttachmentImportParams struct {
	AccountID AccountID `json:"accountId"`
	Path      string    `json:"path,omitempty"`
	Data      []byte    `json:"data,omitempty"`     // base64 on the wire
	Filename  string    `json:"filename,omitempty"` // required with Data; overrides the basename of Path
	Inline    bool      `json:"inline,omitempty"`   // image to be referenced as cid:<contentId>
}

type AttachmentImportResult struct {
	Attachment DraftAttachment `json:"attachment"`
}

type AttachmentRemoveParams struct {
	AccountID    AccountID `json:"accountId"`
	AttachmentID string    `json:"attachmentId"`
}

type AttachmentRemoveResult struct{}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

type SearchQueryParams struct {
	// AccountID empty = search all accounts.
	AccountID AccountID `json:"accountId,omitempty"`
	FolderID  FolderID  `json:"folderId,omitempty"`
	// Query is the user-typed string. Syntax (FTS5 subset, field prefixes such
	// as from:, subject:, has:attachment) is defined in docs/api.md.
	Query string `json:"query"`
	Page  Page   `json:"page"`
}

type SearchResult struct {
	Message MessageSummary `json:"message"`
	// Snippet is a plain-text excerpt with match ranges; never HTML.
	Snippet string       `json:"snippet"`
	Ranges  []MatchRange `json:"ranges,omitempty"`
	Score   float64      `json:"score"`
}

// MatchRange is a byte range within SearchResult.Snippet.
type MatchRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type SearchQueryResult struct {
	Results []SearchResult `json:"results"`
	Page    PageInfo       `json:"page"`
}

// ---------------------------------------------------------------------------
// Sync
// ---------------------------------------------------------------------------

// SyncStatus is the coarse state of one account.
type SyncStatus string

const (
	SyncIdle         SyncStatus = "idle"
	SyncSyncing      SyncStatus = "syncing"
	SyncOffline      SyncStatus = "offline"
	SyncAuthRequired SyncStatus = "authRequired"
	SyncError        SyncStatus = "error"
	SyncDisabled     SyncStatus = "disabled"
)

type SyncState struct {
	AccountID AccountID  `json:"accountId"`
	Status    SyncStatus `json:"status"`
	// FolderID is set while a specific folder is being synchronised.
	FolderID FolderID `json:"folderId,omitempty"`
	// Progress is 0–100, or -1 when unknown.
	Progress int        `json:"progress"`
	LastSync *time.Time `json:"lastSync,omitempty"`
	Error    *Error     `json:"error,omitempty"`
	// PendingOutbox counts messages waiting to be sent.
	PendingOutbox int `json:"pendingOutbox"`
}

type SyncStatusParams struct {
	AccountID AccountID `json:"accountId,omitempty"` // empty = all
}

type SyncStatusResult struct {
	Accounts []SyncState `json:"accounts"`
}

type SyncTriggerParams struct {
	AccountID AccountID `json:"accountId,omitempty"` // empty = all
	FolderID  FolderID  `json:"folderId,omitempty"`  // empty = whole account
	// Full forces a complete resynchronisation instead of an incremental one.
	Full bool `json:"full,omitempty"`
}

type SyncTriggerResult struct{}

// ---------------------------------------------------------------------------
// Config (daemon-owned preferences)
// ---------------------------------------------------------------------------

// SyncIntervalMin is the smallest non-zero Preferences.SyncIntervalSeconds
// the backend accepts; 0 disables periodic sync (manual sync.trigger only).
const SyncIntervalMin = 60

// Preferences are the user-settable daemon options. They affect mail
// handling and therefore live in the backend, not in the UI's own settings.
// Precedence: value set through config.set, then config.toml, then the
// built-in default.
type Preferences struct {
	// SyncIntervalSeconds is the periodic sync interval; 0 = manual only.
	SyncIntervalSeconds int `json:"syncIntervalSeconds"`
	// RemoteContent is the default policy for message.body when the call
	// does not override it: block (default), knownSenders or allow.
	RemoteContent RemoteContentPolicy `json:"remoteContent"`
}

type ConfigGetParams struct{}

type ConfigGetResult struct {
	Preferences Preferences `json:"preferences"`
}

// ConfigSetParams replaces the whole preference set (read-modify-write).
type ConfigSetParams struct {
	Preferences Preferences `json:"preferences"`
}

// ConfigSetResult echoes the effective values after validation.
type ConfigSetResult struct {
	Preferences Preferences `json:"preferences"`
}

// KnownSender is an address the user trusts enough to load remote images
// from. Entries come from mail the user sent ("sent") or from an explicit
// decision ("user"). The list is keyed on the recipient addresses of
// outgoing mail and on explicit user actions, never on incoming From
// headers, which are attacker-controlled.
type KnownSender struct {
	Address string    `json:"address"`
	Source  string    `json:"source"` // "sent" | "user"
	AddedAt time.Time `json:"addedAt"`
}

const (
	KnownSenderSourceSent = "sent"
	KnownSenderSourceUser = "user"
)

type SenderListParams struct{}

type SenderListResult struct {
	Senders []KnownSender `json:"senders"`
}

type SenderAddParams struct {
	Address string `json:"address"` // bare address or "Name <address>"
}

type SenderAddResult struct{}

type SenderRemoveParams struct {
	Address string `json:"address"`
}

type SenderRemoveResult struct{}

// ---------------------------------------------------------------------------
// Notifications (backend → client)
// ---------------------------------------------------------------------------

type NewMessageNotification struct {
	AccountID AccountID      `json:"accountId"`
	FolderID  FolderID       `json:"folderId"`
	Message   MessageSummary `json:"message"`
}

type SyncStateNotification struct {
	State SyncState `json:"state"`
}

// AuthRequiredNotification is emitted when the backend cannot proceed without
// user interaction: expired OAuth2 token, wrong password, missing keyring.
type AuthRequiredNotification struct {
	AccountID AccountID `json:"accountId"`
	Reason    ErrorCode `json:"reason"` // CodeAuthRequired, CodeAuthFailed, CodeKeyringError
	Message   string    `json:"message"`
	// AuthURL is set for OAuth2: the UI must open it through the OpenURI
	// portal. The backend completes the flow on its loopback redirect listener.
	AuthURL string `json:"authUrl,omitempty"`
}

// AccountsChangedNotification is emitted after account.add, account.remove
// and account.setEnabled, to every client including the caller. It carries
// no payload: clients re-run account.list.
type AccountsChangedNotification struct{}
