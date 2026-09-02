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

// Draft is a message being composed. The compose body is plain text in this
// phase; HTML composition is a later decision and will be a separate field
// with its own sanitisation on the way *in*.
type Draft struct {
	ID        DraftID   `json:"id,omitempty"` // empty on first save
	AccountID AccountID `json:"accountId"`
	// Version implements optimistic concurrency: draft.save fails with
	// CodeConflict when the stored version differs from the one supplied.
	Version     int               `json:"version"`
	To          []Address         `json:"to"`
	CC          []Address         `json:"cc,omitempty"`
	BCC         []Address         `json:"bcc,omitempty"`
	Subject     string            `json:"subject"`
	TextBody    string            `json:"textBody"`
	InReplyTo   MessageID         `json:"inReplyTo,omitempty"` // local ID; backend resolves headers
	Forwarding  MessageID         `json:"forwarding,omitempty"`
	Attachments []DraftAttachment `json:"attachments,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// DraftAttachment references a file already imported into the backend's
// attachment store (import method to be added with the compose phase).
type DraftAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

type DraftSaveParams struct {
	Draft Draft `json:"draft"`
}

type DraftSaveResult struct {
	DraftID DraftID `json:"draftId"`
	Version int     `json:"version"`
}

type DraftListParams struct {
	AccountID AccountID `json:"accountId"`
	Page      Page      `json:"page"`
}

type DraftListResult struct {
	Drafts []Draft  `json:"drafts"`
	Page   PageInfo `json:"page"`
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
