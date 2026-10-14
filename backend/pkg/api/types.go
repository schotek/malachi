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

type SystemStorageParams struct{}

// StorageConversion is the state of the background pass that brings stored
// mail to the current preferences (compressStore, attachmentOfflineDays).
type StorageConversion string

const (
	StorageConversionIdle    StorageConversion = "idle"    // nothing left to convert
	StorageConversionRunning StorageConversion = "running" // converting in the background
	StorageConversionNoSpace StorageConversion = "noSpace" // stopped: the disk is full; retried after a preference change or a restart
)

// SystemStorageResult is how much disk the mail store uses. Byte counts are
// file lengths, not allocated blocks.
type SystemStorageResult struct {
	TotalBytes               int64             `json:"totalBytes"`               // databaseBytes + messageBytes + attachmentBytes
	DatabaseBytes            int64             `json:"databaseBytes"`            // store.db with its -wal and -shm
	MessageBytes             int64             `json:"messageBytes"`             // stored raw messages, as stored (compressed or not)
	MessageUncompressedBytes int64             `json:"messageUncompressedBytes"` // their content; equals messageBytes without compression
	SavedBytes               int64             `json:"savedBytes"`               // what compression saves: messageUncompressedBytes - messageBytes
	AttachmentBytes          int64             `json:"attachmentBytes"`          // the compose-side attachment store (drafts, attachment.import)
	RemoteAttachmentBytes    int64             `json:"remoteAttachmentBytes"`    // decoded size of attachments kept on the server only
	Messages                 int               `json:"messages"`                 // messages with a stored raw file
	CompressedMessages       int               `json:"compressedMessages"`
	PartialMessages          int               `json:"partialMessages"` // messages with attachments on the server only
	Conversion               StorageConversion `json:"conversion"`
}

// SystemHelloParams opens the connection handshake (docs/api.md §1.4).
// The method name, this parameter and SystemHelloResult.ProtocolVersion
// never change, so that every client can tell every daemon's protocol.
type SystemHelloParams struct {
	ClientNonce string `json:"clientNonce"` // 64 lowercase hex digits (AuthNonce.Hex)
}

type SystemHelloResult struct {
	ProtocolVersion int    `json:"protocolVersion"` // see ProtocolVersion; compared before anything else
	DaemonNonce     string `json:"daemonNonce"`     // 64 lowercase hex digits
	DaemonProof     string `json:"daemonProof"`     // DaemonProof as 64 lowercase hex digits
}

type SystemAuthenticateParams struct {
	ClientProof string `json:"clientProof"` // ClientProof as 64 lowercase hex digits
}

// SystemAuthenticateResult is empty: the answer itself says that the
// connection is usable.
type SystemAuthenticateResult struct{}

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
	AuthOAuth2   AuthMethod = "oauth2"   // SASL XOAUTH2, OAUTHBEARER as fallback (Gmail through GNOME Online Accounts)
)

// ServerConfig describes one endpoint (IMAP or SMTP).
type ServerConfig struct {
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	Security   Security   `json:"security"`
	Username   string     `json:"username"`
	AuthMethod AuthMethod `json:"authMethod"`
	// CertificateSHA256 pins the server's certificate: 64 lowercase hex
	// digits of the SHA-256 of its DER encoding (NormalizeCertificateSHA256
	// accepts other spellings). When set, the endpoint accepts exactly this
	// certificate instead of verifying the chain and the host name (for a
	// server with its own certificate, e.g. a mail bridge reached over a
	// private network); a different certificate fails with tlsError reason
	// "pinMismatch". Not allowed with security "none" or authMethod "oauth2".
	CertificateSHA256 string `json:"certificateSha256,omitempty"`
}

// OAuth2Source says who holds the sign-in of an oauth2 endpoint.
type OAuth2Source string

const (
	// OAuth2SourceGOA: GNOME Online Accounts owns the sign-in, the refresh
	// token and the OAuth client id; the backend asks it for access tokens
	// and authenticates with SASL XOAUTH2. GOAAccountID is the GOA account
	// id ("account_…"). This is how Google accounts are used.
	OAuth2SourceGOA OAuth2Source = "goa"

	// OAuth2SourceDaemon: the backend runs the sign-in itself
	// (authorization code with PKCE, account.oauthStart) and keeps the
	// refresh token in the keyring. Provider is "google" for an IMAP
	// account (SASL XOAUTH2) or "office365" for a Graph account.
	// ClientID/TenantID optionally override the client the backend is
	// configured with; AuthURL, TokenURL and Scopes stay empty (the
	// backend's provider table owns them).
	OAuth2SourceDaemon OAuth2Source = "daemon"
)

// OAuth2 providers.
const (
	OAuth2ProviderGoogle    = "google"    // IMAP accounts, with OAuth2SourceGOA or OAuth2SourceDaemon
	OAuth2ProviderOffice365 = "office365" // Graph accounts with OAuth2SourceDaemon
	OAuth2ProviderCustom    = "custom"    // reserved: any other authorisation server; not implemented
)

// OAuth2Config is present only when an endpoint uses AuthOAuth2, or on a
// Graph account whose token comes from the backend's own flow. With Source
// "goa" only Provider and GOAAccountID are set; with Source "daemon"
// Provider and optionally ClientID/TenantID. An empty Source (Provider
// "custom" with AuthURL, TokenURL and Scopes) is reserved and not
// implemented.
type OAuth2Config struct {
	Source       OAuth2Source `json:"source,omitempty"`
	GOAAccountID string       `json:"goaAccountId,omitempty"`
	Provider     string       `json:"provider"`           // "google" | "office365" | "custom"
	ClientID     string       `json:"clientId,omitempty"` // bring-your-own-credentials
	TenantID     string       `json:"tenantId,omitempty"`
	AuthURL      string       `json:"authUrl,omitempty"`
	TokenURL     string       `json:"tokenUrl,omitempty"`
	Scopes       []string     `json:"scopes,omitempty"`
}

// AccountKind selects the protocol behind an account.
type AccountKind string

const (
	// AccountIMAP is a classic account: IMAP for the mailbox, SMTP for
	// sending. The default when Kind is empty.
	AccountIMAP AccountKind = "imap"
	// AccountGraph is a Microsoft 365 / Outlook.com account accessed through
	// the Microsoft Graph API; the token comes from GraphConfig.Source.
	AccountGraph AccountKind = "graph"
	// AccountJira is an issue tracker read like mail (Jira Cloud or Data
	// Center): the selected spaces and a few fixed views are its folders,
	// every issue a thread whose description, comments and status or
	// assignee changes are its messages. JiraConfig says where and how;
	// the token is Credentials.Password.
	AccountJira AccountKind = "jira"
)

// GraphSource says who holds the OAuth2 session of a Graph account.
type GraphSource string

const (
	// GraphSourceGOA: GNOME Online Accounts owns the sign-in and the refresh
	// token; the backend asks it for access tokens. GOAAccountID is the GOA
	// account id ("account_…").
	GraphSourceGOA GraphSource = "goa"

	// GraphSourceDaemon: the backend's own sign-in (account.oauthStart);
	// the account's OAuth2 block says {source: "daemon", provider:
	// "office365"} and the refresh token is in the keyring.
	GraphSourceDaemon GraphSource = "daemon"
)

// GraphConfig is present only when Kind == AccountGraph.
type GraphConfig struct {
	Source       GraphSource `json:"source"`
	GOAAccountID string      `json:"goaAccountId,omitempty"`
}

// JiraDeployment says which kind of Jira site a JiraConfig names.
type JiraDeployment string

const (
	JiraCloud      JiraDeployment = "cloud"      // REST v3, ADF, login + API token (Basic)
	JiraDataCenter JiraDeployment = "datacenter" // REST v2, wiki markup, personal access token (Bearer)
)

// VirtualFolder names one of the fixed views of an issue-tracker account
// (Folder.Virtual): folders the daemon fills with copies of the issues of
// the account's selected spaces.
type VirtualFolder string

const (
	VirtualAssignedToMe VirtualFolder = "assignedToMe" // issues assigned to the user
	VirtualWatching     VirtualFolder = "watching"     // issues the user watches
	VirtualOpen         VirtualFolder = "open"         // issues not in a closed status (JiraConfig.ClosedStatuses)
)

// NotificationMailMode says what the daemon does with a notification
// e-mail of the site that arrives in one of the user's mail accounts
// (JiraConfig.NotificationMail).
type NotificationMailMode string

const (
	NotificationMailSync   NotificationMailMode = "sync"   // default ("" too): a notification mail syncs its issue at once
	NotificationMailHide   NotificationMailMode = "hide"   // sync + hide the mail in the mail account (display filter)
	NotificationMailIgnore NotificationMailMode = "ignore" // nothing
)

// Limits of JiraConfig (docs/api.md §4.1). Exceeding one is
// invalidArgument.
const (
	MaxJiraSpaces          = 200 // JiraConfig.Spaces
	MaxJiraStatuses        = 64  // JiraConfig.ClosedStatuses
	MaxJiraListEntries     = 32  // NotificationSenders, BotNames, MetadataFilters, AuthorPrefixes, each
	MaxJiraPatternBytes    = 512 // one entry of those lists
	MaxJiraOfflineDays     = 365 // JiraConfig.OfflineDays
	DefaultJiraOfflineDays = 30  // what JiraConfig.OfflineDays 0 means
)

// JiraConfig is present only when Kind == AccountJira. The zero value of
// every field is its default.
type JiraConfig struct {
	// SiteURL is the site, normalised: https (http only for a loopback
	// host, or when the user typed http for a Data Center site), no user
	// info, query or fragment, no trailing slash; a Data Center site may
	// carry a context path ("https://jira.example.org/jira").
	SiteURL    string         `json:"siteUrl"`
	Deployment JiraDeployment `json:"deployment"`
	// CloudID is the cloud site's id (a UUID from /_edge/tenant_info);
	// cloud only. It enables the route through the Atlassian API gateway
	// that scoped API tokens need.
	CloudID string `json:"cloudId,omitempty"`
	// Login is the Atlassian account e-mail the API token belongs to
	// (cloud: required, sent with the token as Basic authentication);
	// empty for datacenter, whose personal access token stands alone.
	Login string `json:"login,omitempty"`
	// Spaces are the spaces (Jira projects) synchronised, 1..MaxJiraSpaces.
	Spaces []SpaceRef `json:"spaces"`
	// OfflineDays is the account's own retention window: issues updated
	// within it are kept, and open issues assigned to the user whatever
	// their age. 0 = DefaultJiraOfflineDays; 1..MaxJiraOfflineDays.
	OfflineDays int `json:"offlineDays,omitempty"`
	// OnlyMine keeps only the issues the user reports, is assigned, watches
	// or updated recently, instead of every issue of the spaces.
	OnlyMine bool `json:"onlyMine,omitempty"`
	// HideEvents leaves status and assignee changes out of the threads.
	HideEvents bool `json:"hideEvents,omitempty"`
	// DisabledFolders are the virtual folders not shown; empty = all three.
	DisabledFolders []VirtualFolder `json:"disabledFolders,omitempty"`
	// ClosedStatuses decide the "open" folder: an issue in one of them is
	// closed. Empty = the statuses of the category done.
	ClosedStatuses []StatusRef `json:"closedStatuses,omitempty"`
	// NotificationMail is what a notification e-mail of the site in a mail
	// account does ("" = NotificationMailSync).
	NotificationMail NotificationMailMode `json:"notificationMail,omitempty"`
	// NotificationSenders recognise those e-mails by sender: "addr@host"
	// or "@host". Empty = "@<site host>" for cloud, none for datacenter.
	NotificationSenders []string `json:"notificationSenders,omitempty"`
	// BotNames are the display names of integrations that post comments on
	// someone else's behalf; such a comment is attributed to the person
	// named in its header. Empty = no re-attribution.
	BotNames []string `json:"botNames,omitempty"`
	// MetadataFilters are RE2 patterns, each matched against a whole
	// trimmed line of any comment; matching lines are removed, unless that
	// would leave the comment empty.
	MetadataFilters []string `json:"metadataFilters,omitempty"`
	// AuthorPrefixes are words stripped from the start of a re-attributed
	// author's name (such as an organisation name the bot puts in front of
	// it), matched as whole words, ignoring case.
	AuthorPrefixes []string `json:"authorPrefixes,omitempty"`
}

// SpaceRef names a space (a Jira project) of the site. Name is display
// text, untrusted.
type SpaceRef struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

// StatusRef names a workflow status of the site. Name is display text,
// untrusted.
type StatusRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// AccountConfig is the non-secret part of an account. Secrets (passwords,
// refresh tokens, API tokens) travel only in Credentials at add/test time
// and are stored in the system keyring. They are never returned by any
// method.
//
// IMAP and SMTP are set for AccountIMAP only, Graph for AccountGraph only,
// Jira for AccountJira only.
type AccountConfig struct {
	Name         string        `json:"name"`  // display name of the account
	Email        string        `json:"email"` // primary address
	DisplayName  string        `json:"displayName,omitempty"`
	Kind         AccountKind   `json:"kind,omitempty"` // "" = AccountIMAP
	IMAP         *ServerConfig `json:"imap,omitempty"`
	SMTP         *ServerConfig `json:"smtp,omitempty"`
	OAuth2       *OAuth2Config `json:"oauth2,omitempty"`
	Graph        *GraphConfig  `json:"graph,omitempty"`
	Jira         *JiraConfig   `json:"jira,omitempty"`
	SyncInterval int           `json:"syncIntervalSeconds,omitempty"` // 0 = default
}

// Protocol returns Kind with the empty default resolved.
func (c AccountConfig) Protocol() AccountKind {
	if c.Kind == "" {
		return AccountIMAP
	}
	return c.Kind
}

// Credentials carries secrets for account.add / account.update /
// account.test (and account.listSpaces). Exactly the fields relevant to
// the account are set: a password for password endpoints, and as Password
// the API token (cloud) or personal access token (datacenter) of a jira
// account; OAuthSession for an account whose OAuth2/Graph source is
// "daemon"; nothing for a GNOME Online Accounts account (the token source
// holds it).
type Credentials struct {
	Password string `json:"password,omitempty"`
	// OAuthSession is the id of a complete account.oauthStart session: the
	// tokens it obtained stay in the backend. account.add and
	// account.update consume it (the refresh token goes to the keyring);
	// account.test only reads it. Its verified mailbox, provider and
	// client must be the account's; a re-sign-in session (oauthStart with
	// accountId) serves only that account's update and test.
	OAuthSession string `json:"oauthSession,omitempty"`
}

// AccountCapability is something a client may offer for an account's
// messages beyond reading them and setting flags (Account.Capabilities).
type AccountCapability string

const (
	CapabilityCompose  AccountCapability = "compose"  // can be the From of a new message and the target of a forward
	CapabilityReply    AccountCapability = "reply"    // its messages can be answered by e-mail
	CapabilityReplyAll AccountCapability = "replyAll" // ... to all recipients
	CapabilityForward  AccountCapability = "forward"  // its messages can be forwarded (for jira: into a compose account, DraftCreateParams.MessageAccountID)
	CapabilityComment  AccountCapability = "comment"  // reply creates a comment draft (Draft.Comment); a client labels Reply "Comment"
	CapabilityMove     AccountCapability = "move"     // message.move
	CapabilityDelete   AccountCapability = "delete"   // message.delete
	// CapabilityTransition: the status of an issue can be changed through
	// issue.transitions and issue.transition.
	CapabilityTransition AccountCapability = "transition"
)

// MailCapabilities is what a mail account (imap, graph) can do, and what a
// client assumes of an account whose Capabilities is nil (a daemon from
// before capabilities). Read-only: copy before changing.
var MailCapabilities = []AccountCapability{
	CapabilityCompose, CapabilityReply, CapabilityReplyAll, CapabilityForward, CapabilityMove, CapabilityDelete,
}

// Account is what account.list returns: config plus derived state.
type Account struct {
	ID      AccountID     `json:"id"`
	Config  AccountConfig `json:"config"`
	Enabled bool          `json:"enabled"`
	State   SyncState     `json:"state"`
	// Capabilities lists what a client may offer for the account's
	// messages. This daemon always sends it (an empty list: nothing beyond
	// reading and flags); nil, from an older daemon, means
	// MailCapabilities. Use Can.
	Capabilities []AccountCapability `json:"capabilities"`
}

// Can reports whether the account has the capability; nil Capabilities
// means MailCapabilities.
func (a Account) Can(c AccountCapability) bool {
	caps := a.Capabilities
	if caps == nil {
		caps = MailCapabilities
	}
	for _, have := range caps {
		if have == c {
			return true
		}
	}
	return false
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

// AccountReorderParams sets the display order of account.list. The listed
// accounts take the head in the given order; accounts left out keep their
// relative order behind them, so a client that has not seen a just-added
// account does not move it.
type AccountReorderParams struct {
	AccountIDs []AccountID `json:"accountIds"`
}

type AccountReorderResult struct{}

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
	DiscoverGOA        DiscoverSource = "goa"        // the address is signed in through GNOME Online Accounts (Graph for Microsoft 365, IMAP with oauth2 for Google)
	DiscoverISPDB      DiscoverSource = "ispdb"      // Mozilla autoconfig database
	DiscoverAutoconfig DiscoverSource = "autoconfig" // the provider's own autoconfig document
	DiscoverSRV        DiscoverSource = "srv"        // RFC 6186 DNS SRV records
	DiscoverProvider   DiscoverSource = "provider"   // a known provider recognised by DNS or host names; may need a sign-in first
	DiscoverGuess      DiscoverSource = "guess"      // common host names verified by a TLS connection
	DiscoverNone       DiscoverSource = "none"       // nothing found; Config is omitted
)

// AccountDiscoverResult is a suggestion only: nothing is stored and
// nothing is authenticated. A password IMAP Config passes account.add
// validation with the username prefilled; the UI still asks for the
// password and should run account.test. With source "goa" the Config is
// complete (a Graph account for Microsoft 365, an oauth2 IMAP account for
// Google, goaAccountId set) and needs no password. With source "provider"
// it tells the UI the address belongs to a provider that signs in through
// GNOME Online Accounts (ProviderName says which) and must be added there
// first.
//
// Alternatives lists further ways to add the same address, in the
// backend's order of preference, each a config that passes account.add
// validation: for a Google or Microsoft 365 address not signed in through
// GNOME Online Accounts, the backend's own sign-in (source "daemon"; the
// primary Config when GNOME Online Accounts is not running) and, for
// Google, IMAP/SMTP with an app password.
type AccountDiscoverResult struct {
	Config       *AccountConfig  `json:"config,omitempty"`
	Source       DiscoverSource  `json:"source"`
	ProviderName string          `json:"providerName,omitempty"` // display-only, untrusted text
	Alternatives []AccountConfig `json:"alternatives,omitempty"`
}

// LinkedAccount is an account another desktop service is signed in to and
// that Malachi can use: the Microsoft 365 and Google accounts of GNOME
// Online Accounts with mail enabled. Configured says whether a Malachi
// account with that address already exists.
type LinkedAccount struct {
	Provider        string `json:"provider"` // "microsoft365" | "google"
	Email           string `json:"email"`
	Name            string `json:"name,omitempty"` // display name, untrusted text
	GOAAccountID    string `json:"goaAccountId"`
	Configured      bool   `json:"configured"`
	AttentionNeeded bool   `json:"attentionNeeded"` // the service wants the user to sign in again
	// Config is the account to add, built from what the service knows
	// (servers, user names, the token source); it passes account.add as
	// is, without credentials.
	Config *AccountConfig `json:"config,omitempty"`
}

type AccountLinkedParams struct{}

type AccountLinkedResult struct {
	Accounts []LinkedAccount `json:"accounts"`
}

// OAuthBrowserPage holds the texts of the page the browser shows after the
// provider redirects back to the backend. They come from the UI, which
// knows the user's language; plain text, at most 200 characters each,
// escaped by the backend. Omitted texts leave a page without sentences.
type OAuthBrowserPage struct {
	SuccessTitle string `json:"successTitle,omitempty"`
	SuccessText  string `json:"successText,omitempty"`
	FailureTitle string `json:"failureTitle,omitempty"`
	FailureText  string `json:"failureText,omitempty"`
}

// AccountOAuthStartParams begins the backend's own sign-in: either for a
// new account (Config, source "daemon", e.g. from account.discover) or to
// sign an existing "daemon" account in again (AccountID). Exactly one is
// set.
type AccountOAuthStartParams struct {
	AccountID   AccountID         `json:"accountId,omitempty"`
	Config      *AccountConfig    `json:"config,omitempty"`
	BrowserPage *OAuthBrowserPage `json:"browserPage,omitempty"`
}

// AccountOAuthStartResult: the UI opens AuthURL in the user's browser; the
// backend listens for the redirect on 127.0.0.1 until ExpiresAt.
type AccountOAuthStartResult struct {
	SessionID string    `json:"sessionId"`
	AuthURL   string    `json:"authUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// OAuthSessionStatus is the state account.oauthWait reports.
type OAuthSessionStatus string

const (
	OAuthSessionPending  OAuthSessionStatus = "pending"  // the browser has not come back yet; call again
	OAuthSessionComplete OAuthSessionStatus = "complete" // signed in; Config is set
)

type AccountOAuthWaitParams struct {
	SessionID string `json:"sessionId"`
}

// AccountOAuthWaitResult: with Status "complete" Config is the account to
// pass to account.test / account.add together with
// credentials.oauthSession (for a re-sign-in of an existing account the
// backend has already stored the token and Config is the account's).
type AccountOAuthWaitResult struct {
	Status OAuthSessionStatus `json:"status"`
	Config *AccountConfig     `json:"config,omitempty"`
}

// AccountOAuthCancelParams: a pending session ends with cancelled, a
// completed one is discarded (its tokens leave the backend's memory); an
// unknown one is ignored.
type AccountOAuthCancelParams struct {
	SessionID string `json:"sessionId"`
}

type AccountOAuthCancelResult struct{}

// AccountUpdateParams replaces the configuration of an existing account.
// The password is optional: empty keeps the stored one. Enabled is not
// touched (see account.setEnabled).
type AccountUpdateParams struct {
	AccountID   AccountID     `json:"accountId"`
	Config      AccountConfig `json:"config"`
	Credentials Credentials   `json:"credentials"`
}

type AccountUpdateResult struct{}

// AccountTestParams tests connectivity without persisting anything. With
// AccountID set and no password given, the stored password of that account
// is used, so an existing account can be re-tested without retyping it.
type AccountTestParams struct {
	AccountID   AccountID     `json:"accountId,omitempty"`
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

// AccountTestResult carries one entry per endpoint of the account kind:
// imap and smtp for an IMAP account, graph for a Graph account, jira for
// a Jira account (its Capabilities carry "cloud" or "datacenter", and
// "gateway" when the Atlassian API gateway route was used).
type AccountTestResult struct {
	IMAP  *EndpointTestResult `json:"imap,omitempty"`
	SMTP  *EndpointTestResult `json:"smtp,omitempty"`
	Graph *EndpointTestResult `json:"graph,omitempty"`
	Jira  *EndpointTestResult `json:"jira,omitempty"`
}

// AccountDetectSiteParams asks what kind of issue-tracker site a URL
// names, before an account exists. URL is what the user typed: a host
// ("acme.atlassian.net") or a full URL; https is assumed. Nothing is
// stored and nothing is authenticated.
type AccountDetectSiteParams struct {
	URL string `json:"url"`
}

// AccountDetectSiteResult describes the site. Title and Version are
// untrusted display text from the site.
type AccountDetectSiteResult struct {
	Kind       AccountKind    `json:"kind"`    // AccountJira
	SiteURL    string         `json:"siteUrl"` // normalised, as JiraConfig.SiteURL
	Deployment JiraDeployment `json:"deployment"`
	CloudID    string         `json:"cloudId,omitempty"` // cloud only
	Title      string         `json:"title,omitempty"`
	Version    string         `json:"version,omitempty"`
}

// AccountListSpacesParams signs in to the site of a jira Config and lists
// what the account settings choose from. Config's connection fields are
// validated as for account.add; its spaces may be empty. With AccountID
// set (editing an account) and no Credentials.Password, the stored token
// of that account is used.
type AccountListSpacesParams struct {
	AccountID   AccountID     `json:"accountId,omitempty"`
	Config      AccountConfig `json:"config"`
	Credentials Credentials   `json:"credentials"`
	// Counts asks for an estimate of each space's issues updated within
	// Config.Jira.OfflineDays (Space.Issues).
	Counts bool `json:"counts,omitempty"`
}

// Space is a space (a Jira project) of the site. Key and Name are
// untrusted display text.
type Space struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	ServiceDesk bool   `json:"serviceDesk,omitempty"` // a service-desk space (internal comments exist)
	Issues      int    `json:"issues"`                // estimated issues in the window; -1 = not counted
}

// IssueStatus is a workflow status of the site. Name is untrusted display
// text.
type IssueStatus struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Category IssueStatusCategory `json:"category"`
}

// SiteUser is the user the token signs in as. Untrusted display text.
type SiteUser struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"` // empty when the site does not reveal it
}

type AccountListSpacesResult struct {
	User     SiteUser      `json:"user"`
	Spaces   []Space       `json:"spaces"` // by name, at most 1000
	Statuses []IssueStatus `json:"statuses"`
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
	// Synced is false for a folder the daemon lists and accepts moves
	// into but never downloads: Gmail's All Mail, the archive target. A
	// message moved there leaves the local store.
	Synced bool `json:"synced"`
	Unread int  `json:"unread"`
	Total  int  `json:"total"`
	// Virtual is set on a fixed view of an issue-tracker account: its
	// messages are copies of messages of the account's space folders. Role
	// stays "none" and Name is an English fallback; clients name the
	// folder by this code.
	Virtual VirtualFolder `json:"virtual,omitempty"`
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
	ID        MessageID `json:"id"`
	AccountID AccountID `json:"accountId"`
	FolderID  FolderID  `json:"folderId"`
	// ThreadID names the conversation (§4.4): opaque, per account, the
	// same across folders. Empty only for a message an older daemon
	// stored that has not been linked yet.
	ThreadID       ThreadID  `json:"threadId,omitempty"`
	From           []Address `json:"from"`
	To             []Address `json:"to,omitempty"`
	Subject        string    `json:"subject"`
	Date           time.Time `json:"date"` // RFC 3339; best-effort if the header is garbage
	Snippet        string    `json:"snippet"`
	Flags          []Flag    `json:"flags"`
	HasAttachments bool      `json:"hasAttachments"`
	Size           int64     `json:"size"`
	// Outbox is present only for a message in the account's outbox folder
	// (role "outbox"): its delivery state.
	Outbox *OutboxInfo `json:"outbox,omitempty"`
	// Issue is present only for a message of an issue-tracker account:
	// the issue it belongs to and what part of it the message is.
	Issue *MessageIssue `json:"issue,omitempty"`
	// Bulk is present only for a message the daemon classified as bulk
	// mail from its headers (newsletter, mailing list, automated); absent
	// for personal mail, issue-tracker items and rows not classified yet.
	Bulk *BulkInfo `json:"bulk,omitempty"`
}

// BulkKind is what kind of bulk mail a message is.
type BulkKind string

const (
	// BulkNewsletter: marketing or newsletter mail (List-Unsubscribe
	// without a discussion list's List-Post).
	BulkNewsletter BulkKind = "newsletter"
	// BulkList: a discussion mailing list (List-Id with a List-Post
	// address).
	BulkList BulkKind = "list"
	// BulkAutomated: machine-sent mail without an unsubscribe offer
	// (Auto-Submitted, Precedence bulk/junk/list, or a bulk-sending
	// service's fields such as Feedback-ID or X-SG-EID): receipts,
	// tickets, notifications.
	BulkAutomated BulkKind = "automated"
)

// BulkInfo classifies a bulk message. Every string is cleaned by the
// daemon (no control or bidi characters) but still comes from the mail.
type BulkInfo struct {
	Kind BulkKind `json:"kind"`
	// ListID is the List-Id identifier (e.g. "golang-nuts.googlegroups.com"),
	// lower case, without the angle brackets and the phrase; set for
	// BulkList and, when the header exists, for BulkNewsletter.
	ListID string `json:"listId,omitempty"`
	// Domain is the lower-case domain of the From address, for display.
	Domain string `json:"domain,omitempty"`
}

// OutboxState is the delivery state of a queued message.
type OutboxState string

const (
	OutboxQueued  OutboxState = "queued"  // waiting for the next attempt
	OutboxSending OutboxState = "sending" // an SMTP session is running
	OutboxSent    OutboxState = "sent"    // delivered; the Sent copy is pending
	OutboxFailed  OutboxState = "failed"  // permanent failure; outbox.retry re-queues
)

// OutboxInfo describes a message in the outbox.
type OutboxInfo struct {
	State    OutboxState `json:"state"`
	Attempts int         `json:"attempts"`
	// NextAttemptAt is set while queued after a transient failure.
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	// Error is the last failure; absent before the first attempt and after
	// a success.
	Error *Error `json:"error,omitempty"`
}

// MaxOutgoingMessageBytes caps the built RFC 5322 message: attachments are
// base64-encoded, so 25 MiB of files become roughly 34 MiB on the wire.
const MaxOutgoingMessageBytes = 36 << 20

// Attachment describes a MIME part of a message. message.part fetches its
// content; only metadata crosses here.
type Attachment struct {
	PartID      string `json:"partId"`
	Filename    string `json:"filename"` // sanitised: no path separators, no control chars
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Inline      bool   `json:"inline"` // referenced from the HTML body via cid:
	ContentID   string `json:"contentId,omitempty"`
	// Remote: the part's data is not stored on this device, only on the
	// mail server (Preferences.AttachmentOfflineDays,
	// Preferences.NeverStoreAttachments); message.download fetches it —
	// into the store, or under NeverStoreAttachments into the daemon's
	// memory, where the part stays Remote. Set only once the body is
	// fetched; name, type and size are those of the original part.
	Remote bool `json:"remote,omitempty"`
}

// LargeAttachmentMinBytes is the decoded size from which an attachment may
// be kept on the server only under Preferences.AttachmentOfflineDays.
// Smaller parts, the text and HTML bodies and the pictures the HTML shows
// are stored. Under Preferences.NeverStoreAttachments no attachment is
// stored, and a picture the HTML shows only when it is smaller than this.
const LargeAttachmentMinBytes = 100 << 10

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
	// Unsubscribe is present only when the message's List-Unsubscribe
	// header offers a method the daemon can use (see message.unsubscribe),
	// and never for a message in the junk folder.
	Unsubscribe *UnsubscribeOffer `json:"unsubscribe,omitempty"`
}

// UnsubscribeMethod is how message.unsubscribe would act.
type UnsubscribeMethod string

const (
	// UnsubscribeOneClick: RFC 8058 one-click POST by the daemon to an
	// https URL, after a DKIM check of the message.
	UnsubscribeOneClick UnsubscribeMethod = "oneClick"
	// UnsubscribeMailto: the daemon queues an unsubscribe message in the
	// outbox of the account the message arrived in.
	UnsubscribeMailto UnsubscribeMethod = "mailto"
	// UnsubscribeURL: only a web page; the client opens it in a browser
	// after asking the user. The daemon never fetches it.
	UnsubscribeURL UnsubscribeMethod = "url"
)

// UnsubscribeOffer describes the best unsubscribe method of a message.
type UnsubscribeOffer struct {
	Method UnsubscribeMethod `json:"method"`
	// Target is what the confirmation shows: the host of the URL for
	// oneClick and url, the address for mailto.
	Target string `json:"target"`
	// URL is the https page to open; only for UnsubscribeURL.
	URL string `json:"url,omitempty"`
	// UnsubscribedAt is set when the user already unsubscribed from this
	// list or sender through message.unsubscribe (oneClick or mailto).
	UnsubscribedAt *time.Time `json:"unsubscribedAt,omitempty"`
}

// SortOrder for message and thread lists.
type SortOrder string

const (
	SortDateDesc SortOrder = "dateDesc"
	SortDateAsc  SortOrder = "dateAsc"
)

// MessageFilter narrows a message listing to a subset of the folder. The
// counts in PageInfo are taken after the filter. Empty means FilterAll.
type MessageFilter string

const (
	FilterAll     MessageFilter = "all"
	FilterUnread  MessageFilter = "unread"  // without the "seen" flag
	FilterFlagged MessageFilter = "flagged" // with the "flagged" flag
)

type MessageListParams struct {
	AccountID AccountID     `json:"accountId"`
	FolderID  FolderID      `json:"folderId"`
	Page      Page          `json:"page"`
	Sort      SortOrder     `json:"sort,omitempty"`
	Filter    MessageFilter `json:"filter,omitempty"`
	// UnreadOnly restricts the list to messages without the "seen" flag.
	//
	// Deprecated: use Filter. Honoured only while Filter is empty, so that
	// a client written against the older contract keeps working.
	UnreadOnly bool `json:"unreadOnly,omitempty"`
}

// EffectiveFilter resolves Filter, falling back to the deprecated UnreadOnly
// when no filter was given. An unknown Filter is returned unchanged: the
// backend rejects it with invalidArgument.
func (p MessageListParams) EffectiveFilter() MessageFilter {
	if p.Filter != "" {
		return p.Filter
	}
	if p.UnreadOnly {
		return FilterUnread
	}
	return FilterAll
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
	// TrimQuoted asks for the body without the quoted history of a reply
	// (Gmail's quote, a cited blockquote, Outlook's header block and what
	// follows it, a "-----Original Message-----" separator, ">" lines after
	// "On … wrote:"); MessageBodyResult.QuotedTrimmed says whether anything
	// was cut. The client asks again without it to show the whole message.
	// false (the default) returns the body exactly as before.
	TrimQuoted bool `json:"trimQuoted,omitempty"`
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

// BodyState says whether the daemon holds the message content.
type BodyState string

const (
	BodyFetched BodyState = "fetched" // text and html available
	BodyPending BodyState = "pending" // the sync engine has not downloaded the body yet
	BodyTooBig  BodyState = "tooBig"  // over the daemon's raw-message cap; never downloaded
	BodyFailed  BodyState = "failed"  // downloaded but unparsable; nothing shown
)

// MessageBodyResult carries the renderable content of a message.
//
// HTML is ALWAYS the sanitiser's output: no scripts, no event handlers, no
// javascript:/data: URLs, no external CSS, no forms, no frames, and remote
// references removed or, under RemoteAllow, fetched by the daemon and
// inlined. The UI renders it in a JavaScript-disabled webview with a strict
// CSP. Text is the plain text alternative, or a text rendering derived from
// HTML when the message has no text part.
type MessageBodyResult struct {
	MessageID MessageID `json:"messageId"`
	BodyState BodyState `json:"bodyState"`
	HasHTML   bool      `json:"hasHtml"`
	// HTML is the sanitised body: a fragment for the webview's <body>, with
	// cid: images rewritten to malachi-cid:<accountId>/<messageId>/<partId>
	// (served by message.part). Empty when HasHTML is false or HTMLWithheld.
	HTML string `json:"html,omitempty"`
	// HTMLWithheld is set when the message has an HTML part that could not
	// be shown safely: the sanitiser refused it (a cap breach), or the raw
	// message could not be read again. Text is still the plain-text form.
	HTMLWithheld bool           `json:"htmlWithheld,omitempty"`
	Text         string         `json:"text"`
	Blocked      BlockedContent `json:"blocked"`
	Links        []Link         `json:"links"`
	// InlineParts maps the Content-IDs whose cid: references survived in
	// HTML to their attachment PartIDs.
	InlineParts map[string]string `json:"inlineParts,omitempty"`
	// RemotePictures counts the pictures of InlineParts kept on the mail
	// server only (Preferences.NeverStoreAttachments leaves the ones of
	// LargeAttachmentMinBytes and more there) and not available on this
	// device now: message.part answers partNotDownloaded for them until
	// message.download has fetched the message, after which a client asks
	// for the body again. 0 when every picture can be shown.
	RemotePictures int `json:"remotePictures,omitempty"`
	// RemoteContent is the policy that was applied, after the stored
	// preference, the per-call override and the known-senders list were
	// resolved: "block" or "allow", never "knownSenders". A client offers
	// to load the images only under "block"; under "allow" everything
	// loadable was loaded already and what Blocked still counts (CSS
	// url(), srcset, background attributes, plain http:, a fetch that
	// failed) cannot be loaded by asking again.
	RemoteContent RemoteContentPolicy `json:"remoteContent"`
	// SanitizerVersion identifies the sanitiser ruleset; bump on any rule change.
	SanitizerVersion string `json:"sanitizerVersion"`
	// QuotedTrimmed is set only when MessageBodyParams.TrimQuoted cut a
	// quoted history off: HTML, Text, Blocked, Links, InlineParts and
	// RemotePictures then all describe the trimmed body (Text is the text
	// alternative cut by the same rules, or the text rendering of the
	// trimmed HTML when they find nothing to cut in it). A message without
	// HTML, or whose HTML is withheld, has its Text trimmed. false: the
	// whole body, nothing was found to cut or cutting would have left
	// nothing to show.
	QuotedTrimmed bool `json:"quotedTrimmed,omitempty"`
}

// MessagePartParams names one MIME part of a received message, by the
// PartID Attachment carries and malachi-cid: URLs end with.
type MessagePartParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
	PartID    string    `json:"partId"`
}

// MessagePartResult is the decoded content of the part. Data is capped by
// MaxAttachmentDataBytes; a larger part fails with attachmentTooBig.
type MessagePartResult struct {
	PartID      string `json:"partId"`
	ContentType string `json:"contentType"`
	Filename    string `json:"filename"` // sanitised, as in Attachment
	Size        int64  `json:"size"`
	Data        []byte `json:"data"` // base64 on the wire
}

// MessageEmbeddedParams names an attached message (a message/rfc822 part,
// or a part named *.eml) of a stored message, by the PartID Attachment
// carries, to be shown as a message of its own.
type MessageEmbeddedParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
	PartID    string    `json:"partId"`
	// RemoteContent overrides the stored preference for this call only, as
	// in MessageBodyParams. The policy is resolved for the senders of the
	// containing message, not the attached one.
	RemoteContent RemoteContentPolicy `json:"remoteContent,omitempty"`
}

// MessageEmbeddedResult is the attached message rendered read-only: its
// headers as message.get would report them and its body as message.body
// would, produced from the part's bytes on demand and never stored.
//
// Message.ID, AccountID and FolderID are those of the containing message
// (the attached one has no id of its own); Flags is empty. Its cid:
// pictures are inlined into Body.HTML as data: URIs, since nothing can
// serve its parts by URL, so Body.InlineParts is empty and the pictures
// shown are left out of Message.Attachments. The attachments listed have
// no PartID: message.part serves the containing message's parts only, so
// they cannot be fetched.
type MessageEmbeddedResult struct {
	PartID  string            `json:"partId"`
	Message Message           `json:"message"`
	Body    MessageBodyResult `json:"body"`
}

// MessageDownloadParams names a stored message whose missing content
// (attachments kept on the server, or a body not downloaded yet) the daemon
// should fetch from the mail server now.
type MessageDownloadParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
}

// MessageDownloadResult is the message as message.get reports it after the
// download: no attachment is Remote any more, except under
// Preferences.NeverStoreAttachments, where the parts stay Remote and are
// served from the daemon's memory while the message is held there. Part ids
// may differ from before for Microsoft 365 accounts, whose server rebuilds
// the MIME.
type MessageDownloadResult struct {
	Message Message `json:"message"`
}

// MessageUnsubscribeParams names the message whose unsubscribe offer to
// use. The daemon re-reads the headers from the stored message; the client
// sends no URL or address.
type MessageUnsubscribeParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
	// Method is empty for the offer's own method, or UnsubscribeMailto
	// for the message's mailto: alternative after an UnsubscribeUnverified
	// answer (the user confirmed it); any other value is invalidArgument.
	Method UnsubscribeMethod `json:"method,omitempty"`
}

// UnsubscribeOutcome is what message.unsubscribe did.
type UnsubscribeOutcome string

const (
	// UnsubscribeDone: the one-click POST was accepted.
	UnsubscribeDone UnsubscribeOutcome = "unsubscribed"
	// UnsubscribeQueued: the unsubscribe message is in the outbox.
	UnsubscribeQueued UnsubscribeOutcome = "queued"
	// UnsubscribeOpenURL: nothing was sent; the offer is a web page
	// (method url) the client opens in a browser.
	UnsubscribeOpenURL UnsubscribeOutcome = "openUrl"
	// UnsubscribeUnverified: nothing was sent; the one-click request could
	// not be verified. Mailto names the message's mailto: alternative, if
	// any, which the client may offer (Method UnsubscribeMailto). A
	// one-click URL is never handed out: it need not answer a browser.
	UnsubscribeUnverified UnsubscribeOutcome = "unverified"
)

type MessageUnsubscribeResult struct {
	Outcome UnsubscribeOutcome `json:"outcome"`
	// URL is the https page for UnsubscribeOpenURL.
	URL string `json:"url,omitempty"`
	// Mailto is the address of the mailto: alternative for
	// UnsubscribeUnverified; empty when the message offers none.
	Mailto string `json:"mailto,omitempty"`
	// UnsubscribedAt is set for UnsubscribeDone and UnsubscribeQueued.
	UnsubscribedAt *time.Time `json:"unsubscribedAt,omitempty"`
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

// Thread listings are per folder: a ThreadSummary returned for a folder
// describes the members in that folder (docs/api.md §4.4), except
// FolderIDs, which is account-wide.
const (
	// MaxThreadMessages is the most members thread.get returns (the
	// newest); ThreadSummary.MessageCount still counts them all.
	MaxThreadMessages = 500
	// MaxThreadParticipants caps ThreadSummary.Participants.
	MaxThreadParticipants = 8
)

type ThreadSummary struct {
	ID           ThreadID  `json:"id"`
	AccountID    AccountID `json:"accountId"`
	Subject      string    `json:"subject"`      // the newest member's, Re:/Fwd: stripped
	Participants []Address `json:"participants"` // distinct senders, newest first, ≤ MaxThreadParticipants
	MessageCount int       `json:"messageCount"`
	UnreadCount  int       `json:"unreadCount"`
	LatestDate   time.Time `json:"latestDate"`
	// Latest is the newest member in scope: what LatestDate, Snippet and
	// Subject come from, in full, so a client shows it without another
	// call.
	Latest         MessageSummary `json:"latest"`
	Snippet        string         `json:"snippet"` // from the latest message
	Flags          []Flag         `json:"flags"`   // union of member flags
	HasAttachments bool           `json:"hasAttachments"`
	// FolderIDs lists every folder of the account that contains at least
	// one member, whatever the scope.
	FolderIDs []FolderID `json:"folderIds"`
	// Issue is present only for a thread of an issue-tracker account,
	// which is one issue.
	Issue *IssueInfo `json:"issue,omitempty"`
	// SentCount, in a folder's scope, is how many members of the thread
	// in the account's folders of role "sent" are not in that folder (a
	// copy with the same Message-ID header there counts as in it): the
	// user's own replies the folder lacks. It is not part of MessageCount
	// or any other aggregate. 0 when the folder is itself a sent folder or
	// the outbox, for an issue-tracker account (it has no sent folder),
	// and in the account-wide scope of thread.get (the sent members are
	// members there).
	SentCount int `json:"sentCount"`
}

type ThreadListParams struct {
	AccountID AccountID `json:"accountId"`
	FolderID  FolderID  `json:"folderId"`
	Page      Page      `json:"page"`
	Sort      SortOrder `json:"sort,omitempty"`
	// Filter narrows the listing like message.list's: a thread is unread
	// or flagged when any member in the folder is. Empty = all.
	Filter MessageFilter `json:"filter,omitempty"`
}

type ThreadListResult struct {
	Threads []ThreadSummary `json:"threads"`
	Page    PageInfo        `json:"page"`
}

type ThreadGetParams struct {
	AccountID AccountID `json:"accountId"`
	ThreadID  ThreadID  `json:"threadId"`
	// FolderID restricts the members (and the summary) to one folder;
	// empty = every member of the account.
	FolderID FolderID `json:"folderId,omitempty"`
	// WithSent, with FolderID, also returns the members the summary's
	// SentCount counts (ThreadGetResult.Sent). Ignored without FolderID.
	WithSent bool `json:"withSent,omitempty"`
}

type ThreadGetResult struct {
	Thread   ThreadSummary    `json:"thread"`
	Messages []MessageSummary `json:"messages"` // oldest first, at most MaxThreadMessages (the newest)
	// Sent are the members of the thread in the account's sent folders
	// that are not among the folder's members, compared by Message-ID
	// header as well as by id, one per Message-ID (SentCount counts them):
	// oldest first, at most MaxThreadMessages (the newest). Only with
	// WithSent and FolderID; absent otherwise and when there are none.
	// They are not part of Thread's aggregates and are never the folder's
	// members (actions on the conversation are not theirs).
	Sent []MessageSummary `json:"sent,omitempty"`
}

// ---------------------------------------------------------------------------
// Issues (the messages of an issue-tracker account)
// ---------------------------------------------------------------------------

// IssueStatusCategory is the category of an issue's status. An open enum:
// "" is unknown, and a client treats an unknown value as "".
type IssueStatusCategory string

const (
	StatusCategoryTodo       IssueStatusCategory = "todo"
	StatusCategoryInProgress IssueStatusCategory = "inProgress"
	StatusCategoryDone       IssueStatusCategory = "done"
)

// IssueItemKind says what part of an issue a message is.
type IssueItemKind string

const (
	IssueItemDescription IssueItemKind = "description" // the issue itself: its summary and description; the thread's first message
	IssueItemComment     IssueItemKind = "comment"     // a comment
	IssueItemEvent       IssueItemKind = "event"       // changes of watched fields (IssueChange), stored read and never notified
)

// CommentVisibility says who can read a comment of a service-desk issue.
type CommentVisibility string

const (
	CommentPublic   CommentVisibility = "public"   // the customer too
	CommentInternal CommentVisibility = "internal" // the service-desk team only
)

// IssueField names an issue field whose changes become event messages. An
// open enum: a client skips a change of a field it does not know.
type IssueField string

const (
	IssueFieldStatus   IssueField = "status"
	IssueFieldAssignee IssueField = "assignee"
)

// IssueInfo describes an issue as the daemon last synchronised it. Every
// string but Key and URL is untrusted display text from the site.
type IssueInfo struct {
	Key            string              `json:"key"` // "ITSD-42"
	URL            string              `json:"url"` // <siteUrl>/browse/<key>, http(s) only
	Summary        string              `json:"summary"`
	Status         string              `json:"status"`
	StatusCategory IssueStatusCategory `json:"statusCategory,omitempty"`
	Type           string              `json:"type,omitempty"`
	Priority       string              `json:"priority,omitempty"`
	Assignee       string              `json:"assignee,omitempty"` // display name; "" = unassigned
	Reporter       string              `json:"reporter,omitempty"`
	AssignedToMe   bool                `json:"assignedToMe,omitempty"`
	Watching       bool                `json:"watching,omitempty"`
	// CommentVisibilities lists the visibilities a new comment may have:
	// ["public","internal"] on a service-desk issue, empty elsewhere (a
	// comment is then public).
	CommentVisibilities []CommentVisibility `json:"commentVisibilities,omitempty"`
}

// IssueChange is one change an event message stands for. From and To are
// display values ("" = none: unassigned, or no earlier value).
type IssueChange struct {
	Field IssueField `json:"field"`
	From  string     `json:"from,omitempty"`
	To    string     `json:"to,omitempty"`
}

// MessageIssue is MessageSummary.Issue: the issue (its fields flattened
// into the same JSON object) and what the message is of it.
type MessageIssue struct {
	IssueInfo
	Item       IssueItemKind     `json:"item"`
	Visibility CommentVisibility `json:"visibility,omitempty"` // a comment of a service-desk issue
	Changes    []IssueChange     `json:"changes,omitempty"`    // item "event"
	// Via is the display name of the integration that posted a comment on
	// someone else's behalf (JiraConfig.BotNames); the message's From is
	// then the person named in the comment.
	Via    string `json:"via,omitempty"`
	Edited bool   `json:"edited,omitempty"` // the comment was edited after it was posted
	// Mine: the account's own user wrote the item on the site (never set
	// with Via: a relayed comment is someone else's).
	Mine bool `json:"mine,omitempty"`
}

// Issue status transitions (issue.transitions, issue.transition; an
// account with CapabilityTransition). Both name the issue by any message
// of it: the message's thread is the issue.

type IssueTransitionsParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"` // any message of the issue
}

// IssueTransition is one status change the site offers the user on the
// issue. Name and To are untrusted display text from the site.
type IssueTransition struct {
	ID   string `json:"id"`
	Name string `json:"name"` // the transition's name, as the site's own status menu shows it
	To   string `json:"to"`   // the name of the status it leads to
	// ToCategory is the category of that status ("" when the site does
	// not say).
	ToCategory IssueStatusCategory `json:"toCategory,omitempty"`
	// NeedsInput: the transition opens a screen on the site or has fields
	// that must be filled; it cannot be performed here (issue.transition
	// refuses it with invalidArgument). A client lists it disabled.
	NeedsInput bool `json:"needsInput,omitempty"`
}

type IssueTransitionsResult struct {
	Issue       IssueInfo         `json:"issue"`       // the issue as the daemon last synchronised it
	Transitions []IssueTransition `json:"transitions"` // never null; in the site's order, at most MaxIssueTransitions
}

// MaxIssueTransitions bounds the transitions issue.transitions returns.
const MaxIssueTransitions = 100

type IssueTransitionParams struct {
	AccountID    AccountID `json:"accountId"`
	MessageID    MessageID `json:"messageId"`    // any message of the issue
	TransitionID string    `json:"transitionId"` // IssueTransition.ID of a transition without NeedsInput
}

type IssueTransitionResult struct {
	// Issue is the issue after the daemon refreshed it from the site (or
	// as last synchronised when the refresh did not finish in time: the
	// transition was performed all the same).
	Issue IssueInfo `json:"issue"`
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
	// Replaces names a message of the account's Drafts folder that this
	// draft takes over: draft.save links the draft to it, and the upload of
	// the draft replaces that message on the server. Set by draft.open when
	// it built the draft from a message without loss; clients send it back
	// unchanged. Never returned by draft.save or draft.list.
	Replaces MessageID `json:"replaces,omitempty"`
	// Comment is set on a comment draft of an issue-tracker account
	// (draft.create reply on an account with CapabilityComment): sending it
	// posts a comment to the issue instead of an e-mail. In draft.save only
	// Comment.Visibility is read.
	Comment *DraftComment `json:"comment,omitempty"`
	// Local keeps the draft on this device: it is not uploaded to the
	// account's Drafts folder (a board case's suggested reply, §4.13). It
	// reaches the server when it is sent, or as an ordinary draft when the
	// user edited it and it lost its case (docs/api.md §4.5); an untouched
	// suggestion that loses its case is deleted. In draft.save it is read
	// on the first save only (no id) and refused with Replaces; later saves
	// keep the stored value, which a client can neither set nor clear.
	// Linking a draft to a case (board.setDraft, board.annotate draftId)
	// makes it local. In results it is true for a local draft and for
	// every draft of an issue-tracker account, whose drafts never reach a
	// server folder.
	Local     bool      `json:"local,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"` // server-set; ignored in params
}

// DraftComment says where a comment draft goes and who may read it.
type DraftComment struct {
	Issue IssueInfo `json:"issue"`
	// Visibility "" is public; "internal" only when
	// Issue.CommentVisibilities allows it.
	Visibility CommentVisibility `json:"visibility"`
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

// DraftGetParams names one stored draft (draft.get).
type DraftGetParams struct {
	AccountID AccountID `json:"accountId"`
	DraftID   DraftID   `json:"draftId"`
}

// DraftGetResult is the draft as one item of draft.list returns it.
type DraftGetResult struct {
	Draft Draft `json:"draft"`
}

// ComposeMode selects how draft.create pre-fills a draft.
type ComposeMode string

const (
	ComposeNew      ComposeMode = "new"
	ComposeReply    ComposeMode = "reply"
	ComposeReplyAll ComposeMode = "replyAll"
	ComposeForward  ComposeMode = "forward"
)

// Limits of draft.create's attribution (docs/api.md §4.5).
const (
	MaxDraftAttributionBytes = 2048
	MaxDraftAttributionLines = 16
)

// DraftCreateParams asks the backend for an unsaved template: recipients
// computed from the original (Reply-To/From/To/CC minus the account's own
// addresses), a Re:/Fwd: subject, the quoted sanitised body in both forms,
// the original's inline pictures (and, forwarding, its attachments)
// imported into the store, or a parsed mailto: URI. Reply and forward
// logic lives here, not in the UI (CLAUDE.md rule 1).
type DraftCreateParams struct {
	AccountID AccountID   `json:"accountId"`
	Mode      ComposeMode `json:"mode"`
	MessageID MessageID   `json:"messageId,omitempty"` // required unless Mode is ComposeNew
	Mailto    string      `json:"mailto,omitempty"`    // ComposeNew only
	// Attribution is the line the client wants above the quote ("On <date>,
	// <sender> wrote:", or the header block of a forwarded message), in the
	// user's language: the backend has none. Plain text, lines separated by
	// LF, at most MaxDraftAttributionBytes and MaxDraftAttributionLines; the
	// backend escapes it. Empty = no line. Ignored for ComposeNew.
	Attribution string `json:"attribution,omitempty"`
	// MessageAccountID is the account of MessageID when it differs from
	// AccountID (ComposeForward only): forwarding a message of another of
	// the user's accounts, such as an issue tracker's from a mail account.
	// Empty = AccountID.
	MessageAccountID AccountID `json:"messageAccountId,omitempty"`
}

// QuoteForm says how much of the original a draft.create result quotes.
type QuoteForm string

const (
	QuoteHTML QuoteForm = "html" // the sanitised HTML of the original, with its pictures
	QuoteText QuoteForm = "text" // the original's text (the message has no HTML, or it could not be used)
	QuoteNone QuoteForm = "none" // nothing: the body is not downloaded, or Mode is ComposeNew
)

// DraftCreateResult.Draft has an empty ID and version 0; nothing is
// persisted until the first draft.save. Draft.Attachments lists what was
// imported for it (unbound until then; the client sends the ids back in
// draft.save).
type DraftCreateResult struct {
	Draft  Draft     `json:"draft"`
	Quoted QuoteForm `json:"quoted"`
	// Blocked counts what the sanitiser removed from the quoted original
	// (remote images above all); empty unless Quoted is QuoteHTML.
	Blocked BlockedContent `json:"blocked"`
	// Skipped lists the parts of the original that were not imported
	// (over a cap, unreadable, or of a kind the store does not take).
	Skipped []Attachment `json:"skipped,omitempty"`
}

// DraftOpenParams asks for a message of the account's Drafts folder as a
// draft to edit.
type DraftOpenParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
}

// DraftOpenResult.Draft is either the saved draft whose server copy the
// message is (ID and Version set, nothing imported), or a draft built from
// the message: unsaved, its attachments imported but unbound as with
// draft.create, Replaces set when nothing was lost on the way. A draft
// built from a newer copy of a saved draft carries that draft's ID and
// Version, so its first draft.save goes through the version check.
// Nothing is persisted by draft.open.
type DraftOpenResult struct {
	Draft Draft `json:"draft"`
	// Blocked counts what the sanitiser removed from the message's HTML.
	Blocked BlockedContent `json:"blocked"`
	// Skipped lists the parts of the message that were not imported.
	Skipped []Attachment `json:"skipped,omitempty"`
}

// DraftMarkdownParams carries text pasted into the compose editor
// (draft.markdown): plain text, at most MaxDraftBodyBytes, valid UTF-8.
type DraftMarkdownParams struct {
	Text string `json:"text"`
}

// DraftMarkdownResult says whether Text reads as Markdown and, when it
// does, carries it rendered as compose-mode sanitised HTML for the editor
// to insert. Markdown false: the editor pastes the text as it is.
type DraftMarkdownResult struct {
	Markdown bool   `json:"markdown"`
	HTML     string `json:"html,omitempty"`
}

// MessageSendParams queues a saved draft for delivery. Delivery is
// asynchronous: the result only confirms enqueueing. The queued message
// lives in the account's outbox folder; SyncState.PendingOutbox counts it
// (SyncState.FailedOutbox once delivery gave up) and MessageSummary.Outbox
// carries its state and last error.
type MessageSendParams struct {
	AccountID AccountID `json:"accountId"`
	DraftID   DraftID   `json:"draftId"`
	Version   int       `json:"version"`
}

type MessageSendResult struct {
	OutboxID MessageID `json:"outboxId"`
}

// OutboxRetryParams re-queues a queued or failed outbox message for an
// immediate attempt.
type OutboxRetryParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
}

type OutboxRetryResult struct{}

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

// AttachmentGetParams reads an attachment of the store back: what an
// editor shows for a "cid:" reference the backend minted (a quoted
// picture draft.create imported).
type AttachmentGetParams struct {
	AccountID    AccountID `json:"accountId"`
	AttachmentID string    `json:"attachmentId"`
}

// AttachmentGetResult carries the whole file; one over
// MaxAttachmentDataBytes is attachmentTooBig, as with message.part.
type AttachmentGetResult struct {
	AttachmentID string `json:"attachmentId"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType"`
	Size         int64  `json:"size"`
	Data         []byte `json:"data"` // base64 on the wire
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// Search limits (docs/api.md, search.query): a longer query or one with
// more terms and filters is invalidArgument, and page.total is the exact
// number of matches only up to MaxSearchTotal, -1 beyond.
const (
	MaxSearchQueryBytes = 1024
	MaxSearchTerms      = 32
	MaxSearchTotal      = 1000
)

// SearchQueryParams scopes a search: a folder (FolderID, which needs
// AccountID), an account (AccountID alone) or every enabled account
// (neither). Query is what the user typed, in the syntax of docs/api.md.
type SearchQueryParams struct {
	AccountID AccountID `json:"accountId,omitempty"`
	FolderID  FolderID  `json:"folderId,omitempty"`
	Query     string    `json:"query"`
	Page      Page      `json:"page"`
}

type SearchResult struct {
	Message MessageSummary `json:"message"`
	// Snippet is a plain-text excerpt with match ranges; never HTML.
	Snippet string       `json:"snippet"`
	Ranges  []MatchRange `json:"ranges,omitempty"`
	// Score is reserved: results are ordered by date and it is always 0.
	Score float64 `json:"score"`
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
	// PendingOutbox counts outbox messages in state queued or sending.
	PendingOutbox int `json:"pendingOutbox"`
	// FailedOutbox counts outbox messages in state failed: delivery gave
	// up and waits for outbox.retry or a delete.
	FailedOutbox int `json:"failedOutbox"`
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

// OfflineDaysMax bounds Preferences.OfflineDays (0 = keep everything).
const OfflineDaysMax = 3650

// AttachmentOfflineDaysMax bounds Preferences.AttachmentOfflineDays;
// AttachmentOfflineNone keeps no large attachment locally.
const (
	AttachmentOfflineDaysMax = 3650
	AttachmentOfflineNone    = -1
)

// MaxMessageIDsPerCall bounds messageIds in message.flag, message.move and
// message.delete.
const MaxMessageIDsPerCall = 1000

// Preferences are the user-settable daemon options. They affect mail
// handling and therefore live in the backend, not in the UI's own settings.
// Precedence: value set through config.set, then config.toml, then the
// built-in default.
//
// The pointer fields were added later: in config.set an absent (nil) one
// means "unchanged", because an older client drops fields it does not
// know; config.get and the result of config.set always set them. Their
// defaults come from the environment of the process that starts the
// daemon (MALACHI_DEFAULT_COMPRESS_STORE, MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS;
// the macOS app sets them), else off and 0; a default is stored as the
// preference the first time it applies.
type Preferences struct {
	// SyncIntervalSeconds is the periodic sync interval; 0 = manual only.
	SyncIntervalSeconds int `json:"syncIntervalSeconds"`
	// RemoteContent is the default policy for message.body when the call
	// does not override it: block (default), knownSenders or allow.
	RemoteContent RemoteContentPolicy `json:"remoteContent"`
	// OfflineDays bounds the local mail cache: headers and bodies of messages
	// newer than this many days are kept; older ones are not stored at all.
	// 0 keeps everything.
	OfflineDays int `json:"offlineDays"`
	// CompressStore stores raw messages zstd-compressed; stored mail is
	// converted in the background when it changes.
	CompressStore *bool `json:"compressStore,omitempty"`
	// AttachmentOfflineDays: 0 keeps every attachment locally; 1..3650 keeps
	// the large ones (LargeAttachmentMinBytes and up) of messages received
	// in the last N days, the older ones stay on the server
	// (Attachment.Remote); AttachmentOfflineNone (-1) keeps no large one.
	AttachmentOfflineDays *int `json:"attachmentOfflineDays,omitempty"`
	// NeverStoreAttachments stores no attachment of any size, and of the
	// pictures the HTML shows only those smaller than
	// LargeAttachmentMinBytes (MessageBodyResult.RemotePictures counts the
	// others); message.download then keeps the downloaded message in the
	// daemon's memory only, until it quits. It overrides
	// AttachmentOfflineDays. Default false, from no environment variable.
	NeverStoreAttachments *bool `json:"neverStoreAttachments,omitempty"`
}

// Ptr returns a pointer to v, for the optional Preferences fields.
func Ptr[T any](v T) *T { return &v }

type ConfigGetParams struct{}

type ConfigGetResult struct {
	Preferences Preferences `json:"preferences"`
}

// ConfigSetParams replaces the preference set (read-modify-write); an
// absent pointer field is left unchanged.
type ConfigSetParams struct {
	Preferences Preferences `json:"preferences"`
}

// ConfigSetResult is the effective values after validation, every field
// set.
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

// ContactSource says where a recipient suggestion came from.
type ContactSource string

const (
	// ContactSourceSent: a recipient of mail the user sent. Never fed from
	// incoming From headers, which are attacker-controlled.
	ContactSourceSent ContactSource = "sent"
	// ContactSourceAddressBook: a system address book (Evolution Data
	// Server), read only.
	ContactSourceAddressBook ContactSource = "addressBook"
)

// Contact is one recipient suggestion. Name and Book are untrusted display
// text; UIs must not interpret them as markup.
type Contact struct {
	Name    string        `json:"name,omitempty"`
	Address string        `json:"address"` // normalised, syntactically valid
	Source  ContactSource `json:"source"`
	// Book is the display name of the address book the contact came from;
	// empty for a collected address.
	Book string `json:"book,omitempty"`
}

const (
	DefaultContactLimit  = 10
	MaxContactLimit      = 50
	MaxContactQueryBytes = 256
)

type ContactSearchParams struct {
	AccountID AccountID `json:"accountId"`
	Query     string    `json:"query"`
	// Limit caps the result; 0 means DefaultContactLimit, more than
	// MaxContactLimit is clamped.
	Limit int `json:"limit,omitempty"`
}

type ContactSearchResult struct {
	Contacts []Contact `json:"contacts"` // never null; ranked best first
}

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
	// AuthURL is set for the backend's own OAuth2 sign-in (never with
	// reason keyringError): the UI opens it in the user's browser (GTK
	// through gtk.URILauncher, i.e. the OpenURI portal inside Flatpak and
	// the desktop's default handler otherwise; macOS through NSWorkspace).
	// The backend completes the flow on its loopback redirect listener.
	AuthURL string `json:"authUrl,omitempty"`
}

// AccountsChangedNotification is emitted after account.add, account.remove
// and account.setEnabled, to every client including the caller. It carries
// no payload: clients re-run account.list.
type AccountsChangedNotification struct{}

// MessagesChangedNotification says that messages of the account's folders
// changed without arriving or being deleted: hidden or shown again (a
// notification mail of an issue-tracker site hidden in a mail account), or
// rebuilt in place under their ids (a jira account's items rendered with
// other settings, a comment edited or re-attributed, an issue renamed), or
// classified as bulk mail by the background pass over existing mail.
// Clients showing those folders drop what they cached of their messages
// and list them again. FolderIDs empty = any folder of the account.
type MessagesChangedNotification struct {
	AccountID AccountID  `json:"accountId"`
	FolderIDs []FolderID `json:"folderIds,omitempty"`
}
