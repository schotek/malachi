# eWay-CRM

The design of eWay-CRM support in Malachi Mail: saving a message to the
user's eWay-CRM with its relations to contacts, companies, deals and
projects, seeing and opening what is saved, and adding the eWay-CRM
account on Preferences → Integrations. It follows the shape of
[calendar.md](calendar.md): scope, decisions, architecture per layer,
security, tasks with gates, open questions.

Status (2026-10-19): branch `feat/eway`. T0 is done ("As verified (T0)"
after §1); the daemon side T1–T4 is built (§6 "As built"); the Go
reference T5, the macOS client T6 (owner-verified, Gate B) and the GTK
client T7 (owner-verified, Gate C, 2026-10-20) are built, the Windows
client T8 is written and awaits the owner's build (Gate D; §2.7 "As
built"). The
decisions of §1 stand as answered in §5.
The UI decisions of 2026-10-17 (wireframes approved: the toggle button on
the message, the relations dialog, the pill and the bar, the account on
Integrations) are taken and not reopened here; §1 only makes them precise.

Contents:

0. Purpose and scope
1. Decisions (D1–D21)
2. Architecture
3. Security
4. Tasks and gates
5. Open questions for the owner
6. As built (T1–T4)

The contract in §1 D13–D14 and §2 is written to be frozen in task T1
(`backend/pkg/api/crm.go` and `docs/api.md` §4.17 in one commit, rule 5);
later tasks change it only by a decision recorded here.

---

## 0. Purpose and scope

### 0.1 Version 1

- **Save a message to eWay-CRM**, like the Outlook add-in's "Uložit do
  eWay-CRM": the raw message (`.eml`) and its metadata become an eWay
  e-mail item, linked to the contacts and companies its addresses belong
  to and, when the user picks one, to a deal or project as its superior
  item. One message or a multi-selection of one mail account.
- **See what is saved**: a toggle button on the message that reads
  "V eWay-CRM" once saved, a pill in the message list, a bar under the
  header with the linked items, and **open** the item in eWay-CRM.
- **Edit the links** of a saved message and **remove** it from eWay-CRM
  (with confirmation; §5 Q3 asks whether removal is in v1).
- **The eWay-CRM account** (`kind: eway`) is added, edited, paused and
  removed on Preferences → Integrations (web service URL, user name,
  password). It never appears in the Mail sidebar, like a CalDAV account.
- Daemon with a fake eWay server for tests; GTK reference, macOS and
  Windows ports in the same piece of work (rule 6).

### 0.2 Later (listed, not designed here)

- Save attachments as eWay Documents (`SaveDocument` + binary upload;
  the menu item "Uložit přílohy jako dokumenty…" exists as a disabled
  placeholder only if the owner wants it visible, §5 Q7).
- Create a task, journal entry or contact from a message.
- Automatic tracking rules (save every message from/to a tracked company
  or contact), and saving sent mail on send.
- OAuth for instances that sign in through Microsoft Entra (needs a
  client id and secret issued by eWay per application, D3).
- MCP tools: `lookup_crm` (read-only) and `save_to_crm` behind
  `--allow-modify` (D19), planned as v1.5.
- Switching non-ASCII text parts of Malachi's outgoing mail from
  quoted-printable to base64 (D17).

### 0.3 Facts this design rests on

Verified against eWay's public trial (WCF 10.0.0.84) on 2026-10-17
(research notes, not in the repo):

- API: `POST <ws>/API.svc/<Method>`, JSON in and out; answers carry
  `ReturnCode` (`rcSuccess` or another `rc…`), `Description`, `Data`.
  `InsecureAPI.svc` exists for http; we never use it.
- `LogIn {userName, passwordHash, appVersion, clientMachineIdentifier,
  clientMachineName}`: `passwordHash` = MD5 hex of the UTF-8 password,
  `appVersion` = letters followed by a number. Returns `SessionId` (sent in
  every later body) and `UserItemGuid`. Session: 20 minutes idle, 24 hours
  at most, bound to the client's IP address; a dead one answers
  `rcBadSession`.
- Cloudflare in front of the cloud instances refuses a non-browser-like
  User-Agent (403, error code 1010); `MalachiMail-research/1.0
  (+https://github.com/schotek/malachi)` passed.
- Saving: **first** `SaveBinaryAttachment?sessionId=…&itemGuid=…&fileName=…`
  with the raw `.eml` as `application/octet-stream` under a GUID the
  client chose, **then** `SaveEmail {transmitObject}` with the same
  `ItemGUID`. Required: `FileAs`, `SenderEmailAddress`, `To`, `SentOn`
  (all four or none: the server's hash needs them), `AttachmentsCount`
  (NOT NULL), `EmailFileExtension: ".eml"` (default `.msg`). ISO 8601
  `SentOn` was accepted. The server computes `Hash` and refuses a
  duplicate with `rcDuplicateEmail`.
- `SearchEmails {transmitObject: {MessageId}}` finds a saved message
  exactly; `GetEmailsByItemGuids {itemGuids, includeRelations}` returns
  its relations; `GetEmailMessage` parses the stored file (sender,
  To/Cc, Message-ID, References, `HtmlBody`, attachments);
  `GetBinaryAttachment` returns it byte-identical; `DeleteEmail
  {itemGuid}` removes it.
- `SaveRelation {ItemGUID1, FolderName1, ItemGUID2, FolderName2,
  RelationType}`: `GENERAL` links Contacts or Companies with Emails;
  `OUTLOOKPROJECT` is the "superior item" relation to Leads, Projects,
  Companies or Marketing; the e-mail also has the foreign keys
  `Leads_OutlookProjectGuid` and `Projects_OutlookProjectGuid`.
- `SearchContacts` is exact and case-insensitive; with
  `binaryLogicalOperator: "OR"` one call matches `Email1Address`,
  `Email2Address` and `Email3Address`. `SearchCompanies` matches `Email`;
  companies also have tracked domains (`TrackedDomains`, table
  `CompaniesDomains`).
- Typed query objects of the WCF JSON need `__type` as their first key.
- Instances with Microsoft Entra sign-in need OAuth with a client id and
  secret that eWay support issues per application (PKCE is supported,
  the secret is still sent). The API needs the Standard plan.
- eWay's parser: quoted-printable soft line breaks inside a multibyte
  UTF-8 sequence come out garbled (base64 and 8bit are fine); with an
  HTML part only `HtmlBody` is kept (`TextBody` null); attachment names
  come from `Content-Type name=`, not `Content-Disposition filename=`.
- Open-item link: `https://open.eway-crm.com/?ws=<base64url ws>&l=<base64url
  eway://Emails/<guid>>`.

Not verified (each is marked **verify** where it matters): upload size
limit, several `OUTLOOKPROJECT` relations on one e-mail,
`GetServiceAuthSettings` (name, verb, `IsAzureAuth`), how `LogIn` answers
a plan without API access, `DeleteRelation`, searching companies by
tracked domain, wildcard search by name, the `Note` and `FileAs` column
limits, base64url padding and whether `ws` includes `/API.svc` in the
open link, the Leads/Projects fields that say "open", whether
`rcDuplicateEmail` names the existing item, whether an orphan binary is
cleaned up after a failed `SaveEmail`, `SaveDocument`.

---

## 1. Decisions

Each decision: the proposal, the alternatives, the consequences, and its
status. All are `proposed`; the owner answers them (or §5) before T1.

### D1 · Account kind `eway`, capability `crm` — proposed

- **Proposal.** New `api.AccountKind` `AccountEway = "eway"` (pkg/api
  `types.go`, next to `AccountCalDAV`) with `AccountConfig.Eway
  *EwayConfig` (json `eway`), and a new `AccountCapability`
  `CapabilityCrm = "crm"`. `capabilitiesFor` (core/accounts_jira.go)
  returns exactly `["crm"]` for it. The account has no folders, no mail
  and no outbox: `folder.list` answers no folders and every message method
  naming it answers `invalidArgument`, as a `caldav` account does
  (`mailOnlyMessages.refuse` in core/dispatch.go today tests for caldav
  only; it becomes "kind without mail": caldav or eway). `kindOrder` gains
  `AccountEway` after `AccountCalDAV`; `validateAccountConfig`'s default
  message names the new kind.
- **Clients.** `capabilities.InMail` (ui/internal/capabilities) excludes
  caldav only today; it becomes "kind with mail" (not caldav, not eway),
  and so do macOS `mailSidebarAccounts` / `inMail` and Windows
  `FolderTree.InMail`/`MailSidebarAccounts`. `integrations.ShowsInIntegrations`
  already catches the kind (not imap/graph); `Catalog` and `RowFor` get an
  eWay entry (D20, §2.7).
- **Alternatives.** (a) A CRM block on a mail account (no own account):
  breaks the one-credential-per-account rule and the Integrations page.
  (b) A generic `kind: crm` with a provider field: no second CRM is
  planned; a kind per service matches `jira` and `caldav`.
- **Consequences.** Older clients see an account of an unknown kind with
  capabilities `["crm"]`: they must not show it in Mail. GTK, macOS and
  Windows ship in one release (rule 6), so this only matters for an old
  client against a new daemon, which `ProtocolVersion` 2 tolerates (an
  unknown kind is already a no-op supervisor and an empty capability set
  in older daemons).

### D2 · Realm and the account's address — proposed

- **Proposal.** `store.RealmOf` gains the eway case: `siteRealm` of
  `EwayConfig.ServiceURL` (lower-cased host[:port] + path, no trailing
  slash), so an eWay account may share an e-mail address with the user's
  mail accounts (uniqueness is (email, realm), store/accounts.go).
  `accountRealm` refuses an eway account without a service URL.
  `AccountConfig.Email` is the eWay user's e-mail when `LogIn` → user item
  gives one (`Email1Address` of `GetUsersByItemGuids`, T0 2), else the
  user name when it is an address, else **empty**: an eway account is the
  only kind that needs no address (`validateAccountConfig`; found at Gate
  B 2026-10-17 — the owner's user name `vjanecek` is not an address and
  his CRM user has no e-mail, so the add failed with "empty address").
  `Name` defaults to the instance's path segment ("satomar") and is
  editable.
- **ServiceURL normalisation** (`eway.NormaliseServiceURL`): https only;
  no user info, query or fragment; a trailing `/API.svc`, `/API.svc/…` or
  `/InsecureAPI.svc` the user pasted is cut; no trailing slash. `http://`
  is refused with `invalidArgument` and a reason the wizard shows (D3).
- **Alternatives.** Realm = host only: two instances on
  `hosting.eway-crm.com` (`/satomar`, `/other`) would collide.

### D3 · Credentials and sign-in — proposed

- **Proposal.** User name in `EwayConfig.Username`; the password travels
  as `credentials.password` at `account.add`/`update`/`test` and is kept
  in the keyring under `auth.KeyPassword`, like a Jira token or a CalDAV
  password. The daemon computes the MD5 hex at each `LogIn`; neither the
  password nor its hash is ever logged, stored elsewhere, or part of an
  error. Only https. Instances that sign in through Microsoft Entra are
  refused with a clear error: before the first `LogIn` the wizard calls
  `account.detectEway`, which asks `GET <ws>/API.svc/GetServiceAuthSettings`
  anonymously and reads `IsAzureAuth` (**verify** name, verb and field on
  the trial and on the owner's instance); true → error 1205
  `crmSignInUnsupported` with `data.reason = "microsoft"`. A `LogIn`
  refused because the plan has no API access → the same code with
  `reason = "plan"` (**verify** what `LogIn` answers).
- **Alternatives.** (a) Keep only the MD5 hex in the keyring: the hash is
  password-equivalent for eWay but does not reveal a password the user
  reuses elsewhere; it breaks the uniform `credentials.password` rule
  and "replace password" works the same. Worth it? (§5 Q9). (b) OAuth for
  Entra instances now: needs a client secret from eWay per application,
  which an open-source client cannot keep secret (the same reason Google
  has no built-in client, `docs/architecture.md` §7).
- **Consequences.** An Entra instance cannot be added in v1; the wizard
  says so before asking for a password.

### D4 · Session — proposed

- **Proposal.** One session per account, held in memory by the account's
  `eway.Client` (never on disk). Logged in lazily on the first call that
  needs it; `rcBadSession` (idle timeout, 24 h limit, a new IP address
  after the laptop moved) → log in again **once** and repeat the call;
  a second `rcBadSession` is `serverError`. A refused `LogIn` (wrong
  password: `rcBadLogin`, **verify** the code) is `authFailed`, marks the
  account's state `authFailed` (banner + "Edit account…" as for Jira) and
  stops further logins until the account is edited, so a wrong password
  cannot lock the user out. `LogOut` (**verify** it exists) on account
  removal, pause and daemon shutdown, best effort with a 2 s limit.
- **Alternatives.** Login per call: 2× the requests and a login entry per
  save in eWay's audit log.

### D5 · Client identity sent to eWay — proposed

- `appVersion` = `MalachiMail1` (constant; the number changes only if
  eWay's behaviour towards us must change). `clientMachineIdentifier` = a
  random 128-bit id (hex) created on first use and kept in `meta`
  `eway.clientId` (one per daemon store, not per account).
  `clientMachineName` = the constant `Malachi Mail`, **not** the host
  name (the host name tells the CRM's administrators and anyone with the
  audit log the user's machine name; it is personal data we do not need
  to send). `User-Agent` = `MalachiMail/<version>
  (+https://github.com/schotek/malachi)`, the form that passed
  Cloudflare's check; a 403 with error code 1010 is reported as
  `serverError` with class `blockedUserAgent` in the log.

### D6 · Which messages can be saved — proposed

- Only messages of mail accounts (`imap`, `graph`): never Jira messages
  (synthesised, not mail anyone sent), never the Outbox or a Drafts folder
  (role `outbox`/`drafts`) and never a local draft. The pure UI rule
  (`crm.Available`, §2.7) and the daemon (`invalidArgument`) both refuse.
  Junk is allowed (the user decides).
- The message must be **whole** on this device: the copy
  `message.download` holds in the memory cache, else the stored file when
  it is not a skeleton (`storedWhole` in core/forward_many.go: no
  `RawPartial`, no `RemoteParts`). Otherwise `crm.save` answers 1504
  `partNotDownloaded` with `data = {"messageIds": [...]}` and the client
  runs `message.download` for those and retries, as the attachment chips
  already do before opening a remote attachment. Under
  `neverStoreAttachments` the downloaded copy lives only in the memory
  cache, which is enough.
- Size: at most `ingest.MaxMessageBytes` (25 MiB) → else 1502
  `attachmentTooBig` with `{limit, size}`; eWay's own upload limit is
  **verify** and may lower it.

### D7 · What is sent — proposed

The raw message exactly as the server delivered it (`<guid>.eml`, the
file name `fileName=<subject>.eml` through `safename.Filename`), then
`SaveEmail` with:

| eWay field | Value |
|---|---|
| `ItemGUID` | the GUID of the upload (UUID v4 from `crypto/rand`) |
| `FileAs`, `Subject` | the stored subject, cleaned; empty subject → the sender address (the backend has no localised "(no subject)") |
| `SenderEmailAddress` | the address of `From` of the stored summary (the display name is in the uploaded file; `EmailTracked` has no name field) |
| `To`, `Cc` | addresses only, joined `"a@x; b@y"` (the trial's form); names left out; capped at 4 KiB each, cut at an address boundary |
| `SentOn` | the `Date` header, else the internal date; ISO 8601 with offset |
| `ReceivedTime` | the internal date (the column verified on the trial; `ReceivedOn` does not exist) |
| `MessageId` | the `Message-ID` header, verbatim (with angle brackets as the trial stored it, **verify**) |
| `AttachmentsCount` | attachments of the stored summary (not inline parts) |
| `EmailFileExtension` | `".eml"` |
| `Note` | the stored plain text of the message (the search document's text), cleaned, ≤ 32 KiB (`MaxCrmNoteBytes`; **verify** the column limit) |
| `IsPrivate` | the dialog's "Soukromý" check box |

Nothing else about the user or the device goes with it. Bcc is not in the
stored message (it never is, `docs/security.md` §7.1) and is not sent.

### D8 · Duplicates — proposed

1. With a `Message-ID`: `SearchEmails {MessageId}` first. Found → that
   item is the saved message (no upload, no `SaveEmail`); the daemon only
   adds the relations it lacks (`GetEmailsByItemGuids
   includeRelations`). Several found (an old double save) → the oldest.
2. Not found → upload, `SaveEmail`. `rcDuplicateEmail` (the server's
   hash matched another item, possibly saved by a colleague or without a
   Message-ID) → search by `MessageId` again; found → step 1; not found
   (no Message-ID, or the other item is someone's private e-mail) → error
   1307 `crmDuplicate` "eWay-CRM already has this message", and the
   uploaded binary is orphaned (**verify** whether eWay cleans it; if not,
   say so in §3 and try `DeleteEmail` on the GUID).
3. A second `crm.save` of the same message while one runs is `conflict`
   (per message key, in memory), like `message.unsubscribe`.

### D9 · Suggestions — proposed

`crm.lookup` builds the dialog's prefilled check boxes from the
message's addresses, never from its text:

- **Addresses**: `From`, `Reply-To`, `To`, `Cc` of every selected
  message (Sent mail: the recipients), lower-cased, deduplicated, minus
  the user's own addresses of every account (the identity set the board
  uses, `Identity.WithSelf`), at most 20 (`MaxCrmLookupAddresses`, From
  first).
- **Contacts**: one `SearchContacts {Email1Address, Email2Address,
  Email3Address} OR` per address (exact match).
- **Companies**: `SearchCompanies {Email}` per address; the companies of
  the found contacts (**verify** the contact's company foreign key,
  probably `Companies_CompanyGuid`); and companies whose tracked domains
  contain the address's domain, through the typed query API with
  `__type` first (**verify** on the trial; if it does not work, v1 drops
  the domain match and §5 says so). Domains of freemail providers are not
  matched: a fixed list in `internal/eway` (gmail.com, seznam.cz,
  outlook.com, …).
- **Deals and projects**: through the found contacts and companies —
  their relations to `Leads` and `Projects` (`Get…ByItemGuids
  includeRelations`, `ForeignFolderName`), then the items themselves for
  name and state; only open ones (**verify** the state fields) and at most
  10 of each, newest first.
- **Prefill**: found contacts and companies checked; a deal or project is
  checked as the superior item only when exactly one open one was found
  (§5 Q2); everything else listed unchecked. A message already saved shows
  its current relations checked instead.
- **Budget**: concurrency 4 per account (the Jira client's
  `maxConcurrent`), 15 s for the whole lookup; what is not done by then
  is left out and the result says `partial: true` (the dialog shows a
  hint, search still works). Cached in memory per (eWay account, address)
  for 10 minutes.
- **Search field**: `crm.search` by name in contacts, companies, deals
  and projects (`FileAs`; wildcard support **verify**, else the query API
  with a "contains" condition), at most 50 results, 2–200 bytes of query.

### D10 · Relation types — decided (owner, §5 Q1)

- Contacts and companies: `SaveRelation GENERAL` (`FolderName1` =
  `Contacts`/`Companies`, `FolderName2` = `Emails`).
- The chosen deal or project: `OUTLOOKPROJECT` (the superior item, which
  is what the Outlook add-in sets, **verify** direction of the pair) plus
  the e-mail's foreign key `Leads_OutlookProjectGuid` or
  `Projects_OutlookProjectGuid` in `SaveEmail`. v1 allows **one**
  superior item (the foreign keys hold one each); further deals/projects
  the user checks get `GENERAL` (§5 Q1; **verify** on the trial whether
  several `OUTLOOKPROJECT` relations are accepted and shown).
- Edit links: unchecking a relation removes it (`DeleteRelation`,
  **verify**; if it does not exist, v1's dialog for a saved message can
  only add, and §5 says so).

### D11 · Several messages, one eWay account — decided (owner, §5 Q12)

- Multi-selection: 1–20 messages of one mail account (`MaxCrmMessages`;
  the selection is already one account, `selection.Restrict`). One
  dialog: suggestions merged over all, the chosen relations and privacy
  apply to each; `crm.save` answers per message (some may fail).
- One eWay account per installation in v1 (§5 Q12, *owner*). The daemon
  does not forbid a second one (realms differ), but every client uses the
  first **enabled** eWay account in `account.list` order: the dialog has
  no account popup, `crm.lookup`/`crm.save` without `accountId` mean that
  account, and the button reads "V eWay-CRM" when the message is saved in
  it. An account popup with a remembered `crm-last-account` key is
  deferred until someone needs two accounts.

### D12 · Local record (migration 0021) — proposed

The next free migration number is **0021** (the last is
`0020_calendar.sql`). `0021_crm_links.sql`, tables and indexes only, no
trigger (0017's triggers stay the only ones on `messages`):

```sql
CREATE TABLE crm_links (
    crm_account_id  TEXT    NOT NULL,              -- the eway account
    mail_account_id TEXT    NOT NULL,
    msg_key         TEXT    NOT NULL,              -- rfc_message_id, else 'id:' || messages.id
    item_guid       TEXT    NOT NULL,              -- the eWay e-mail item
    saved_at        INTEGER NOT NULL,              -- unix seconds
    checked_at      INTEGER NOT NULL,
    private         INTEGER NOT NULL DEFAULT 0,
    relations       TEXT    NOT NULL DEFAULT '[]', -- JSON [{kind, guid, name, superior}]
    PRIMARY KEY (crm_account_id, mail_account_id, msg_key)
);
CREATE INDEX crm_links_by_message ON crm_links (mail_account_id, msg_key);
```

- Keyed by Message-ID, so every copy of a message (Inbox and Archive)
  shows as saved. A display-only cache: **the server is the truth**.
  Rows go with either account (explicit delete in the removal path, as
  `dropCalendarCache` does; there are no FKs to `accounts` in the store).
- Staleness: a row older than 24 h since `checked_at` is checked again in
  the background when `message.get` returns it (`GetEmailsByItemGuids`);
  gone → row deleted and `notify.messagesChanged`. `crm.lookup` always
  checks the server.
- Alternatives: no local record (list pill needs one call per row — no);
  a column on `messages` (copies, and 0017's rule about touching
  `messages`).

### D13 · API surface — proposed

Compatible extension of protocol 2 (`ProtocolVersion` stays 2), new
`docs/api.md` **§4.17 crm** (the last section today is §4.16 invitation),
methods in `pkg/api/methods.go`, a `CrmService` in `service.go`, types and
limits in `pkg/api/crm.go`.

```jsonc
EwayConfig { "serviceUrl": "https://hosting.eway-crm.com/satomar",
             "username": "jan", "userGuid": "…" (opt, set by the daemon) }

CrmItemKind "contact" | "company" | "deal" | "project"
CrmItem { "kind", "guid", "name", "detail" (opt),   // e-mail, company of a contact, state
          "reason": "address|domain|contact|company|search|saved" (opt),
          "superior": bool (opt), "url": "https://open.eway-crm.com/…" }
CrmRecord { "accountId", "itemGuid", "savedAt", "checkedAt", "private": bool,
            "items": [CrmItem], "url" }
CrmItemRef { "kind", "guid" }
```

- `account.detectEway {serviceUrl}` → `{serviceUrl (normalised),
  microsoftSignIn: bool, version (opt)}`; anonymous, 15 s, like
  `account.detectSite`. `account.add`/`update`/`test` take an
  `eway` config + `credentials.password`; `test` logs in.
- `crm.lookup {accountId (opt: the only eway account),
  messageAccountId, messageIds [1..20]}` → `{accountId, saved:
  [{messageId, record: CrmRecord|null}], suggestions: [CrmItem],
  partial: bool}`.
- `crm.search {accountId, query, kinds (opt)}` → `{items: [CrmItem],
  truncated: bool}`.
- `crm.save {accountId, messageAccountId, messageIds [1..20], link:
  [CrmItemRef], unlink: [CrmItemRef] (one saved message only), superior:
  CrmItemRef|null, private: bool}` → `{results: [{messageId, record
  (opt), created: bool, error: Error (opt)}]}`.
- `crm.remove {accountId, messageAccountId, messageId}` → `{}`:
  `DeleteEmail` of the item, then the local row; the client confirms
  first (D18).
- `MessageSummary.crmSaved: true` (opt; a row exists for the summary's
  account and key) and `Message.crm: [CrmRecord]` (opt, one per eWay
  account) for `message.get`.
- Notifications: after `crm.save` and `crm.remove`, and when a stale
  check removes a row, `notify.messagesChanged {accountId: <mail
  account>, folderIds}` (the folders of the affected rows); account state
  changes go through `notify.accountsChanged` as for other kinds. No new
  notification.

### D14 · Error codes — proposed

Free codes found in `docs/api.md` §2 and `pkg/api/errors.go`: **1008**,
**1109**, **1205**, **1307**, **1509** (the next in each range; codes are
only added, never renumbered). Proposed:

| Code | Name | Meaning |
|---|---|---|
| 1109 | crmItemNotFound | a GUID named in `crm.save` (`link`, `superior`) is not an item of that eWay account, or the saved message is gone for `crm.remove` |
| 1205 | crmSignInUnsupported | the eWay instance cannot be signed in to by this version: `data` = `{ "reason": "microsoft" \| "plan" }` |
| 1307 | crmDuplicate | eWay refused the message as a duplicate of an item the daemon cannot find (D8) |

Reused: `authFailed` (wrong password), `networkError`, `serverTimeout`,
`tlsError`, `serverError` (any other `rc…`, its class in the log only),
`partNotDownloaded` (D6, with `messageIds`), `attachmentTooBig`,
`conflict`, `invalidArgument` (wrong kinds, Outbox/Drafts/Jira, limits),
`messageNotFound`, `accountNotFound`. 1008 and 1509 stay free.
`TestDocsCoverAllMethods` keeps the table and §4.17 in step.

### D15 · The open-item link — proposed

Built by the daemon from the stored `serviceUrl` and a GUID checked for
shape (`^[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}$`), never from
a URL or string the server returned: `https://open.eway-crm.com/?ws=` +
base64url(serviceUrl) + `&l=` + base64url(`eway://<Folder>/<guid>`) with
`<Folder>` ∈ `Emails`, `Contacts`, `Companies`, `Leads`, `Projects`
(**verify** padding, whether `ws` includes `/API.svc`, and the folder
names in `l`). Clients open it in the browser without the "Open this
link?" question, because nothing in it comes from mail or from the
server; they never build it themselves.

### D16 · Limits — proposed

In `pkg/api/crm.go`: `MaxCrmMessages = 20`, `MaxCrmItems = 50` (link +
unlink + superior), `MaxCrmSuggestions = 50`, `MaxCrmSearchResults = 50`,
`MaxCrmQueryBytes = 200`, `MaxCrmLookupAddresses = 20`,
`MaxCrmNoteBytes = 32 << 10`. In `internal/eway`: JSON response ≤ 8 MiB,
error body ≤ 64 KiB (Jira's `maxErrorBody`), request timeout 30 s, upload
timeout 2 min, `LogIn` 15 s, 4 requests in flight per account,
`Retry-After` on 429/503 honoured up to 60 s twice (Jira's rule), at most
5 redirects and none across origins (D3, §3). Display strings cleaned and
capped (names 256 bytes, details 512).

### D17 · eWay's quoted-printable bug — proposed

Malachi's SMTP builder writes text parts quoted-printable
(`backend/internal/smtp/build.go`, `writeText`), so the user's own sent
Czech mail saved to eWay may render with garbled characters at soft line
breaks. v1 sends the message as delivered and changes nothing in the
SMTP builder (the saved file must stay byte-identical; rewriting it
would break signatures and the dedup hash). **Separate follow-up, not
v1**: write non-ASCII text parts as base64 (or 8bit where the server
announces `8BITMIME`) in the builder, which fixes it for future sent
mail (§5 Q6). Mail from others is what it is; we report the bug to eWay.

### D18 · Remove and edit links — decided (owner, §5 Q3)

- "Odebrat z eWay-CRM" asks first (title "Remove from eWay-CRM?", the
  subject in the body, destructive button) and then calls `crm.remove`,
  which deletes the eWay e-mail item (`DeleteEmail`) with its relations
  — for every user of the CRM, not only this one (the body says so).
- "Upravit vazby…" opens the same dialog over the saved message:
  checked = its current relations, the result goes as `link`/`unlink`.
  Privacy can be changed (`SaveEmail {ItemGUID, IsPrivate}`, which the
  trial showed updates without touching the binary).

### D19 · MCP — proposed (v1.5)

`lookup_crm` (read-only: whether a message is saved, its links,
suggestions) always; `save_to_crm` only behind `--allow-modify`, with
the bridge's usual rules (no HTML, every string through `clean()` and
the nonce fence). No removal tool. Not part of v1 tasks (T11 later).

### D20 · Texts — proposed

Brand names (`eWay-CRM`, the pill's `eWay`) are not msgids. New msgids
(English, Czech), in `ui/internal/crm/text.go` (into `po/POTFILES`):

| Where | msgid | cs |
|---|---|---|
| button, not saved | Save to eWay-CRM | Uložit do eWay-CRM |
| button, saved | In eWay-CRM | V eWay-CRM |
| button, busy (tooltip) | Saving to eWay-CRM… | Ukládá se do eWay-CRM… |
| context menu | Save to eWay-CRM… | Uložit do eWay-CRM… |
| context menu, several (plural) | Save %d Messages to eWay-CRM… | Uložit %d zprávu / zprávy / zpráv do eWay-CRM… |
| menu, bar button | Open in eWay-CRM | Otevřít v eWay-CRM |
| menu | Edit Links… | Upravit vazby… |
| menu | Remove from eWay-CRM | Odebrat z eWay-CRM |
| dialog title | Save to eWay-CRM / Edit Links | Uložit do eWay-CRM / Upravit vazby |
| dialog groups | Contacts · Companies · Deals · Projects | Kontakty · Společnosti · Obchody · Projekty |
| dialog | Superior Item | Nadřazená položka |
| dialog search | Search eWay-CRM | Hledat v eWay-CRM |
| dialog | Private | Soukromý |
| dialog hint | Only you will see this message in eWay-CRM. | Tuto zprávu uvidíte v eWay-CRM jen vy. |
| dialog empty | Nothing in eWay-CRM matches these addresses. | K těmto adresám v eWay-CRM nic není. |
| dialog partial | eWay-CRM did not answer in time; some suggestions may be missing. | eWay-CRM neodpověděl včas, některé návrhy mohou chybět. |
| dialog button | _Save | _Uložit |
| toast | Saved to eWay-CRM | Uloženo do eWay-CRM |
| toast (plural) | %d messages saved to eWay-CRM | %d zpráva / zprávy / zpráv uloženo do eWay-CRM |
| toast | Removed from eWay-CRM | Odebráno z eWay-CRM |
| pill tooltip, bar | Saved in eWay-CRM | Uloženo v eWay-CRM |
| Integrations page description (replaces the 2026-10-17 msgid in all three clients) | Services beside your mail: issue trackers, calendars and CRM | Služby vedle pošty: sledování úkolů, kalendáře a CRM |
| remove question | Remove from eWay-CRM? | Odebrat z eWay-CRM? |
| remove body | “%s” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays. | Zpráva „%s“ bude z eWay-CRM smazána i s vazbami, pro všechny jeho uživatele. Zpráva v Malachi Mail zůstane. |
| wizard title | Add eWay-CRM Account | Přidat účet eWay-CRM |
| wizard fields | Web Service Address · User Name · Password | Adresa webové služby · Uživatelské jméno · Heslo |
| wizard hint (owner, 2026-10-17: says where to find it) | The address of your eWay-CRM, such as https://hosting.eway-crm.com/company. In the web application, point at your company's name in your account panel to see it; in Outlook it is under eWay-CRM → Settings → Connection. | Adresa vašeho eWay-CRM, například https://hosting.eway-crm.com/firma. Ve webové aplikaci ji zobrazíte najetím na název firmy v panelu svého účtu, v Outlooku je v eWay-CRM → Nastavení → Připojení. |
| error 1205 microsoft | This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet. | Tento eWay-CRM se přihlašuje účtem Microsoft, což Malachi Mail zatím neumí. |
| error 1205 plan | This eWay-CRM does not allow access through its API. | Tento eWay-CRM nedovoluje přístup přes své API. |
| error http | Malachi Mail connects to eWay-CRM over https only. | Malachi Mail se k eWay-CRM připojuje jen přes https. |
| error 1307 | eWay-CRM already has this message. | eWay-CRM už tuto zprávu má. |
| error 1504 | Downloading the message first… (status) | Nejdřív se stahuje zpráva… |
| catalogue | Save messages to your CRM with their contacts, companies and deals | Ukládání zpráv do CRM s kontakty, společnostmi a obchody |
| account remove body | %s will be removed from Malachi Mail. Messages saved in eWay-CRM stay there. | Účet %s bude z aplikace Malachi Mail odebrán. Zprávy uložené v eWay-CRM tam zůstanou. |

"Save Attachments as Documents…" has no msgid until the feature exists
(§5 Q7). Every msgid is used by all three clients (Windows
`StringsCheckTests`); the replaced page description leaves the template
with `make po`, so no `parity-exclusions.txt` line is needed.

### D22 · Following a saved conversation — decided (owner, 2026-10-17)

- **Owner's decision.** After the user saves a message, the daemon saves
  the later messages of the same conversation by itself — the user's
  replies and the other side's answers — with the same links and the same
  superior item, so a saved thread stays whole in the CRM. eWay-CRM's
  Outlook add-in reaches a similar result only through its automatic
  tracking by contacts; without it every message is saved by hand. This is
  the only automatic save of v1; tracking by contacts and companies
  (§0.2) stays for later.
- **Switch.** `EwayConfig.followThreads` (json `followThreads`), per eWay
  account, default **on** for a new account; the assistant's sign-in page
  shows it as a check box, and the account's edit (Integrations → Edit)
  too. Off: nothing automatic happens; already saved messages stay.
- **Followed thread.** A conversation of a mail account is followed by an
  eWay account when any of its members (same `messages.thread_id`, same
  mail account) has a `crm_links` row for that eWay account. The
  **template** is the followed thread's most recently saved member
  (`saved_at`): its links, superior item and privacy are copied.
- **Which messages.** A member stored **after** the thread's first save
  (the row's stored time; `internal_date` when that is older than the
  first `saved_at`, so a late copy of an older message is not pulled in),
  in a folder of role inbox, sent, archive or none — never drafts, outbox,
  junk or trash, never a local draft, never a message of another kind of
  account — not saved yet (`crm_links`), and **whole** on this device
  (`storedWhole`/memory cache, as `crm.save`). A member with parts still
  on the server is skipped and logged once; the user can save it by hand
  (the button shows it as not saved). The usual dedup applies (Message-ID
  with sender and date), so a reply the other side saved from Outlook is
  linked, not duplicated.
- **When.** (1) The `Stored` hook after a message's body is stored (the
  same hook the invitations and the Jira notification mail use), after
  thread linking — the Sent copy of the user's own reply arrives through
  the same path on IMAP and Graph, so outgoing mail needs no second hook;
  the hook only enqueues `(mail account, message id)`. (2) A worker per
  eWay account drains the queue serially (one `crm.save`-equivalent call
  per message, the account's client, its concurrency and session rules),
  and (3) an hourly sweep (`core.Maintain`) catches up after offline time:
  for every enabled eWay account with the switch on, every followed thread
  of the last 90 days, at most 50 saves per sweep. Failures are logged by
  class and retried by the sweep; nothing blocks the sync.
- **Result.** The automatic save writes the usual `crm_links` row with
  `auto = 1` — a new forward-only migration `0022_crm_links_auto.sql`
  adds the column `auto INTEGER NOT NULL DEFAULT 0` (0021 stays as it is:
  the owner's test copy already ran it) — and `CrmRecord.auto: bool`
  tells the clients; `notify.messagesChanged` as for a manual save. No
  toast, no notification (Outlook is silent too).
- **Security (§3 / docs/security.md §7.3).** The only case in which mail
  leaves the daemon without a per-message action by the user: bounded to
  conversations the user saved from that mail account into that CRM,
  copying only what the user chose for the first message; the switch is
  per account and visible in the Integrations row's edit; the daemon
  never follows a thread into another mail account or another CRM.
- **UI.** Besides the check box nothing new: the pill, the bar and the
  button show the automatically saved message as saved. Texts (D20):
  check box `Also save later messages of saved conversations`
  (cs `Ukládat i další zprávy uložených konverzací`), its hint `Replies
  and new messages in a conversation you saved are saved to eWay-CRM with
  the same links.` (cs `Odpovědi a nové zprávy v konverzaci, kterou jste
  uložili, se do eWay-CRM uloží se stejnými vazbami.`).

### D21 · No syncer — proposed

An eway account synchronises nothing. Its entry in the kind table
(core/backend.go `newKindSupervisor`) is a tiny `eway.Supervisor`: `Start`
creates the account's client (no network), `State` reports the last
known login state (`ok`, `authFailed`, `offline`, `error` with class),
`Trigger` is a no-op that answers false, `Stop` logs out. No outbox entry
(the outbox table maps the kind to the no-op worker). Pausing the account
(`account.setEnabled false`) hides the button in every client and makes
`crm.*` answer `unavailable`.

### As verified (T0, 2026-10-17)

Against eWay's public trial (WCF 10.0.0.84, OutlookClientVersion
10.0.0.82); scripts and raw logs in the agent's scratchpad, not in the
repo; everything created was deleted and every session logged out. The
answers to the **verify** items of §0.3 and §1:

| # | Question | Answer |
|---|---|---|
| 1 | `GetServiceAuthSettings` | **GET** `<ws>/API.svc/GetServiceAuthSettings` without a session: `{ReturnCode, Description, IsActiveDirectory, IsAzureAuth}`. POST is 405; an unknown method is HTTP 404 with an HTML WCF page, so "no such method" is not a JSON answer. |
| 2 | `LogIn` failures | `rcLoginFailed` for a wrong password and an unknown user alike (no user enumeration; the Description echoes the user name). `appVersion` without a number and an empty `clientMachineIdentifier` are not refused by the trial (we keep `MalachiMail1` and the random id). Success: `SessionId`, `UserItemGuid`, `IsAdmin`, `WcfVersion`, `OutlookClientVersion`, `Debug`. The user's address is `Email1Address` of `GetUsersByItemGuids` (may be empty); the user record also has `Server_Password`. A plan without API access: not testable. |
| 3 | `LogOut` | Exists (`rcSuccess`); the old session then answers `rcBadSession`, whose Description **contains the session id**. |
| 4 | `OUTLOOKPROJECT` | Only with the e-mail as `ItemGUID1` (`Emails` → `Leads`/`Projects`); the other direction fails in a trigger ("requires Direction to be set"). **One** per e-mail (`rcDuplicateRelation` for a second). Setting `Leads_OutlookProjectGuid` / `Projects_OutlookProjectGuid` with `SaveEmail` creates the relation; setting the other key **replaces** it. `GENERAL` works in both directions. |
| 5 | `DeleteRelation` | Flat `{relationDataGuid}` = the relation's `RelationDataGUID` from `Get…ByItemGuids includeRelations`; removes both sides. Other shapes: `rcParameterError`. |
| 6 | Tracked domains, contact's company | `TrackedDomains` is a writable string; `SearchCompanies {TrackedDomains}` is exact; the typed `Query` with `LikeFilterExpressionPredicate` on `TrackedDomains` works (`Domains` is unknown). `SearchContacts` results carry no company GUID and ignore `includeForeignKeys`; `GetContactsByItemGuids {includeForeignKeys: true}` gives `Companies_CompanyGuid`. |
| 7 | Search by name | `Search*` on `FileAs` is exact (no prefix, substring or `%`). "Contains" needs `Query` + LIKE (Leads, Projects, Companies, Contacts; also `Email1Address`); it answers `Data: [{the requested columns}]`. A contact's `FileAs` is "Last, First". |
| 8 | Open deals and projects | `IsCompleted`/`CompletedDate` and `IsLost`/`LostDate`; open = neither. `StateEn` is a workflow enum value without a closed flag (null for an item saved by the API). |
| 9 | Upload limit | None found up to a 42.5 MB `.eml` (2/10/42 MB in 1.1/4.2/13.9 s). |
| 10 | Message-ID | Stored as sent, with angle brackets; `SearchEmails {MessageId}` is exact, case-insensitive, and **needs the brackets**; `GetEmailMessage` returns it without. |
| 11 | `IsPrivate` | Flips both ways with `SaveEmail {ItemGUID, IsPrivate}`; the binary stays. |
| 12 | Orphaned upload | An upload whose GUID never gets `SaveEmail` cannot be fetched or listed; `DeleteEmail` of that GUID answers success anyway; there is no `DeleteBinaryAttachment`. Server-side clean-up is unknowable. |

Not answered by T0: the `Note` and `FileAs` column limits, the open
link's padding and `ws` form (D15), the code of `LogIn` on a plan without
API access, a second user's view of a private item. They wait for
Gate A.

---

## 2. Architecture

### 2.1 Daemon packages

- `internal/eway` (new, AGPL like the rest of `backend/`, no new
  dependency):
  - `client.go` — `Client` for one instance and one user: `Options
    {ServiceURL, Username, Password func(ctx) (string, error), HTTP,
    UserAgent, ClientID, Log, Now}`; `call(ctx, method, body, out)` POSTs
    JSON to `<ws>/API.svc/<method>`, bounded by a semaphore of 4, reads
    at most `maxJSONBytes`, maps transport failures and HTTP statuses to
    `*api.Error` (`classifyTransport` as in `internal/jira`), follows
    redirects itself only within the service URL's origin and refuses an
    https → http hop; `upload(ctx, guid, name, raw)` for
    `SaveBinaryAttachment`.
  - `session.go` — lazy `LogIn`, the session id behind a mutex, one
    re-login on `rcBadSession`, the `authFailed` latch (D4), `LogOut`.
  - `types.go` — the wire structs (`transmitObject`, `ReturnCode`,
    `Description`, `Data`), `rc…` → error class, typed query objects with
    `__type` first (a custom `MarshalJSON`, because `encoding/json` orders
    struct fields as declared and maps by key).
  - `clean.go` — `cleanText`/`truncate`/`isInvisible` with Jira's rules
    (control and bidi characters, zero-width, caps), `cleanGUID`,
    `cleanEmail`.
  - `query.go` — `SearchContacts`, `SearchCompanies`, tracked domains,
    relations of contacts/companies, Leads/Projects by GUID, search by
    name; the freemail list.
  - `save.go` — `SaveMessage(ctx, raw, meta, links, superior, private)`
    implementing D8 and D10; `Remove`, `Relations`.
  - `link.go` — the open-item URL (D15).
  - `ewaytest/` — a fake eWay server over `httptest.NewTLSServer`: sessions
    with idle expiry and an IP binding switch, `rcBadSession`,
    `rcBadLogin`, the Entra and plan answers, Cloudflare's 403/1010 for a
    bad User-Agent, the trial's order rule (a `SaveEmail` whose GUID has
    no uploaded binary fails), the four-field hash with
    `rcDuplicateEmail`, `SearchEmails {MessageId}`, contacts with three
    e-mail fields (exact, case-insensitive), companies with tracked
    domains, Leads/Projects and relations, `DeleteEmail`,
    `DeleteRelation`, 429 with `Retry-After`, oversized and malformed
    answers. Fixtures in `backend/testdata/eway` (hostile names: bidi,
    control characters, 1 MiB names, wrong GUID shapes, wrong JSON types).
  - `NormaliseServiceURL`, `Supervisor` (D21).
- `internal/core`: `accounts_eway.go` (validation as
  `validateCalDAV`: no imap/smtp/oauth2/graph/jira/caldav/calendar blocks;
  `account.detectEway`; the eway part of `account.test`; client
  construction with the keyring password, the password going only to
  `LogIn` of the account's own origin), `crm_service.go` (the four
  methods, the per-message `conflict` guard, availability rules of D6),
  `crm_lookup.go` (D9 with its budget and cache), `crm_save.go` (whole
  message via `storedWhole`, metadata, `notify.messagesChanged`),
  `crm_maint.go` (stale re-check, removal with the account).
- `internal/store`: `crm_links.go` (`PutCrmLink`, `CrmLinksFor(mail
  account, keys)`, `DeleteCrmLink`, `DropCrmLinks(account)`), the
  `crmSaved` flag in the listing queries (an `EXISTS` on
  `crm_links_by_message`, skipped when the table is empty),
  `RealmOf` eway case, `migrations/0021_crm_links.sql`.
- `pkg/api`: `crm.go` (types, limits), `types.go` (kind, capability,
  `EwayConfig` in `AccountConfig`, `MessageSummary.CrmSaved`,
  `Message.Crm`), `errors.go` (D14), `methods.go`, `service.go`.

### 2.2 Save, in order

1. Client: `crm.lookup` (dialog opens with a spinner) → the user checks
   items, maybe searches (`crm.search`), presses Save → `crm.save`.
2. Daemon checks the eWay account (kind, enabled, not `authFailed`), the
   mail account and each message (D6); any not whole →
   `partNotDownloaded {messageIds}` before anything goes out.
3. Client downloads those (`message.download`, its usual progress) and
   calls `crm.save` again.
4. Per message, in turn: `SearchEmails {MessageId}` → found: relations to
   add/remove; else new GUID → `SaveBinaryAttachment` → `SaveEmail` →
   `rcDuplicateEmail` handling (D8) → `SaveRelation` for each new link,
   `DeleteRelation` for each unlink → `GetEmailsByItemGuids
   includeRelations` to record what the server now has.
5. `crm_links` row written; `notify.messagesChanged` for the mail account
   once at the end; result per message.

### 2.3 Lookup, in order

Addresses of the messages (D9) → own addresses removed → per address,
four at a time within 15 s: contact search, company search, tracked
domain → companies of contacts → relations to Leads/Projects → the items
→ merged, ranked (address > domain > via contact > via company), capped →
with `saved` per message from the server (`SearchEmails {MessageId}` and
its relations), refreshing `crm_links`.

### 2.4 Account add and test

Wizard: service URL → `account.detectEway` (normalise, Entra check) →
user name + password → `account.test` (one `LogIn`, user item for the
e-mail) → `account.add`. Edit: name, user name, replace password (as the
Jira token), remove. The settings dialog is opened from the Integrations
row (`integrations.EditorEway`).

### 2.5 UI: the pure package `ui/internal/crm`

No gotk4, tested, texts behind `Translator` (= `jira.Translator`), the
model the GTK widgets use and the ports copy (`MalachiCore/CRM`,
`Malachi.Core/Crm`):

- `Accounts(accounts) []api.Account` — enabled eway accounts in list
  order; `Available(sel Selection, accounts) bool` — at least one, the
  messages' account a mail account, no Outbox/Drafts/local draft, 1–20
  rows (D6, D11).
- `Button(state) ButtonState` — `Hidden`, `Save` (label "Save to
  eWay-CRM"), `Saved` (label "In eWay-CRM", opens the menu), `Busy`;
  from `MessageSummary.CrmSaved`/`Message.Crm` and a running save.
- `Menu(saved) []Item` — Show in eWay-CRM, Edit Links…, (later) Save
  Attachments as Documents…, Remove from eWay-CRM.
- `Dialog` — model over `crm.lookup`: groups by kind, check state,
  one superior item (radio among deals/projects), search results merged
  into the groups without losing checks, Private, the Save button's
  enabled rule, the `link`/`unlink` diff for a saved message, `partial`
  hint.
- `Bar(m *api.Message) Bar` — visible when saved: text, the linked items
  (kind icon + name, each opening its own `url`), "Open in eWay".
- `Pill(s api.MessageSummary)` — `eWay` + tooltip.
- `RemoveQuestion(subject)`, `ErrorText(err)`, toasts; `WizardFlow`
  (detect → credentials → test → add, like `accountwizard/jira_flow.go`).
- `contextmenu`: a new `ItemID` `SaveToCrm` in the group after
  Forward (label from `crm`, plural for several), shown only when
  `MessageState.Crm` is true; `selection.Flags` gains `CrmSaved` so the
  multi-selection label and the menu's Show/Remove (single only) follow.

#### As built (T5, 2026-10-17)

`ui/internal/crm` is pure (imports `pkg/api` and `ui/internal/jira` only),
every msgid is in `text.go` (in `po/POTFILES` after `integrations.go`),
`po_test.go` keeps the template in step. Where it differs from the sketch
above: one rule `OfferFor` serves the button, the context menu and the
multi-selection (the sketch's `Available(sel, accounts)`); `Available`
picks the account; the menu item is **Open in eWay-CRM** (§5 Q11); the
wizard's texts and failure rules live here, its flow in
`accountwizard/eway_flow.go` (unexported `ewayFlow`, like `jiraFlow`).

Exported API:

```go
type Translator = jira.Translator
const Brand = "eWay-CRM"; const PillLabel = "eWay"; const MaxMessages = api.MaxCrmMessages
const Icon = "x-office-address-book-symbolic"

// accounts and rules (crm.go)
func Accounts(accounts []api.Account) []api.Account            // enabled eway, list order
func Available(accounts []api.Account) (*api.Account, bool)     // the first of them
func IsMailAccount(a api.Account) bool                          // imap, "", graph
func CanSave(messageAccount api.Account, folderRole api.FolderRole, localDraft bool) bool
type Row struct { Account api.Account; Role api.FolderRole; LocalDraft, Saved bool }
type Offer struct { Shown, Enabled bool; Count int; Saved bool }
func OfferFor(accounts []api.Account, rows []Row) Offer
func RecordOf(m *api.Message, crm api.AccountID) *api.CrmRecord
func IsSaved(s api.MessageSummary, m *api.Message, crm api.AccountID) bool
type Key struct { Account api.AccountID; Message api.MessageID }
type InFlight struct{ … }   // Begin/End(account, ids), Has(account, id), Any(account, ids)
type ButtonState int        // ButtonHidden, ButtonNotSaved, ButtonSaving, ButtonSaved
func ButtonStateFor(accounts []api.Account, r Row, busy bool) ButtonState
type MenuID string          // MenuOpen "open", MenuEditLinks "editLinks", MenuRemove "remove"
type MenuItem struct { ID MenuID; Label string; Destructive bool }
func NotDownloaded(err error) ([]api.MessageID, bool)            // partNotDownloaded data
func SignInReason(err error) (api.CrmSignInReason, bool)         // 1205 data
func OpenURL(u string) string                                    // https only, else ""
func DefaultName(serviceURL string) string                       // last path segment, else host
func ServiceHost(a api.Account) string                           // "host/path", Integrations subtitle

// dialog (dialog.go)
var Kinds = []api.CrmItemKind{contact, company, deal, project}
func CanBeSuperior(k api.CrmItemKind) bool
type DialogEntry struct { Item api.CrmItem; Checked, Superior bool } // Ref(), CanBeSuperior()
type DialogGroup struct { Kind api.CrmItemKind; Entries []DialogEntry }
func NewDialog(res api.CrmLookupResult) *Dialog
func (d *Dialog) AccountID() api.AccountID
func (d *Dialog) Messages() []api.MessageID
func (d *Dialog) Editing() bool; Partial() bool; IsEmpty() bool
func (d *Dialog) Private() bool; SetPrivate(on bool)
func (d *Dialog) Superior() (api.CrmItemRef, bool)
func (d *Dialog) Checked(ref api.CrmItemRef) bool
func (d *Dialog) Groups() []DialogGroup
func (d *Dialog) Toggle(ref api.CrmItemRef, on bool) bool
func (d *Dialog) SetSuperior(ref api.CrmItemRef) bool
func (d *Dialog) Merge(res api.CrmSearchResult) int
func (d *Dialog) Changed() bool; CanSave() bool
func (d *Dialog) SaveParams(messageAccountID api.AccountID, messageIDs []api.MessageID) (api.CrmSaveParams, bool)
func (d *Dialog) SearchParams(raw string) (api.CrmSearchParams, bool)

// texts (text.go)
type Button struct { State ButtonState; Label, Tooltip string; Enabled, Menu bool }
func ButtonFor(state ButtonState, tr Translator) Button
func MenuLabel(id MenuID, tr Translator) string
func SavedMenu(tr Translator) []MenuItem
func SaveMenuLabel(count int, tr Translator) string
type DialogStrings struct { Title, Search, SuperiorItem, Private, PrivateHint, Empty, Partial, Save string }
func DialogTexts(editing bool, tr Translator) DialogStrings
func GroupTitle(k api.CrmItemKind, tr Translator) string
type BarItem struct { Kind api.CrmItemKind; Name string; Superior bool; URL string }
type Bar struct { Title string; Items []BarItem; Names, URL, OpenLabel, Note string }
func BarFor(rec *api.CrmRecord, tr Translator) (Bar, bool)
type Pill struct { Label, Tooltip string }
func PillFor(s api.MessageSummary, tr Translator) (Pill, bool)
type SaveSummary struct { Saved, Failed int; Err *api.Error }
func Summarize(res api.CrmSaveResult) SaveSummary
func SaveToast(s SaveSummary, tr Translator) (text string, err *api.Error) // "" + err: client's general sentence
func RemovedToast(tr Translator) string
type Question struct { Heading, Body, Confirm string }
func RemoveQuestion(subject string, tr Translator) Question
func ErrorText(err error, tr Translator) string                  // 1205, 1307, 1109; else ""
type Action int  // ActionSave, ActionLookup, ActionSearch, ActionRemove, ActionDownload
func ActionText(a Action, tr Translator) string                  // the "%s" of "%s failed: …"
func DownloadingText(n int, tr Translator) string
type WizardPage int   // PageService, PageCredentials
type WizardStrings struct { Title, EditTitle, CredentialsTitle, ServiceLabel, ServiceHint, NameLabel,
    UserLabel, PasswordLabel, NewPasswordLabel, PasswordPrompt, Next, Add, Save,
    Contacting, SigningIn, Adding, Saving string }
func WizardTexts(tr Translator) WizardStrings
func CheckServiceInput(raw string, tr Translator) (ok bool, problem string) // http refused here
type WizardStep int   // StepDetect, StepTest, StepSave
type WizardField int  // FieldNone, FieldService, FieldUser, FieldPassword
type Failure struct { Page WizardPage; Banner, What string; Field WizardField }
func FailureOf(step WizardStep, err error, editing bool, tr Translator) Failure
```

Rules worth knowing for the ports:

- `OfferFor`: one row is shown and enabled exactly when `CanSave`; several
  are shown when their account is a mail account and enabled when every row
  `CanSave` and there are at most 20; several rows are always "Save N
  Messages", never the saved-state items.
- `IsSaved`: the loaded `Message.Crm` wins over `MessageSummary.CrmSaved`
  when the message is the row's; `InFlight` makes the button `Saving`.
- `Dialog`: items with an unknown kind or a GUID not of GUID shape are left
  out, GUIDs are lower-cased, names and details cleaned (`jira.Clean`) and
  capped again (256/512 bytes), a non-https `url` dropped. Prefill as D9
  (contacts/companies with reason `address`/`domain` checked, the
  `Superior`-flagged deal/project checked as superior); editing = one
  message with a record (its items checked, superior as recorded,
  suggestions unchecked). At most one superior: the first deal/project
  checked while none is superior takes it, `SetSuperior` moves it (checking
  an unchecked one), unchecking the superior leaves none (nothing is
  promoted). A check that would make the save name more than 50 items is
  refused. The diff: `link` = checked, not recorded, not the new superior;
  `unlink` = recorded, unchecked (editing only); `superior` only when it
  differs from the recorded one, and then the replaced superior goes into
  `link` when it stays checked. (A daemon gap found here — `ApplyLinks`
  skipped a `link` that was related already while the CRM dropped the old
  superior on the key change — is fixed: `ApplyLinks` re-reads the
  relations after the key changed, §6 "Review fixes"; the client needed
  no change.)
- `SaveToast`: one success "Saved to eWay-CRM", several the plural, one
  failure `ErrorText` or the client's general sentence, several with
  failures the plural "N messages were not saved".
- `CheckServiceInput` refuses `http://` with the D20 text before
  `account.detectEway`; `FailureOf` maps 1205 `microsoft` to the address
  page (field address; on the sign-in page when editing), `plan` to the
  sign-in page, `authFailed` to the password field with the wizard's
  existing "The server rejected the user name or password",
  `authRequired` to "Enter the password for this account".
- `ewayFlow` (accountwizard): address → `account.detectEway` (15 s;
  `microsoftSignIn` stops there with the 1205 text, no password page) →
  name (default `DefaultName`), user name, password → `account.test`
  (50 s; the `eway` endpoint's error is the step's error) →
  `account.add` (60 s). Edit opens on the sign-in page, the address
  fixed; an empty password keeps the stored one (test with `accountId`)
  unless the user name changed, which also clears `userGuid`; saves with
  `account.update`.

Also in T5: `contextmenu` gains `CrmSave` "crmSave" (its own group after
the answers; label `crm.SaveMenuLabel(Labels.Count)`) and, for one saved
message, `CrmOpen` "crmOpen", `CrmEditLinks` "crmEditLinks", `CrmRemove`
"crmRemove"; `MessageState.OfferCrm/Crm/CrmSaved`, filled by
`SetCrm(crm.Offer)`. `selection.Flags` gains `Crm` and `CrmSaved`
(`Combine`: several = every row `Crm`, ≤ 20, never `CrmSaved`, off in the
outbox). `capabilities.InMail` is false for `eway`. `integrations`:
`Catalog` third entry eWay-CRM (`EwayIcon` = `crm.Icon`), `RowFor` eway
(switch, `EditorEway`, "Edit Account", subtitle `crm.ServiceHost`), the
page description of §5 Q8 (the literal swapped in macOS
`Model/Integrations.swift` and Windows `Model/Integrations.cs` too). GTK
compiles: Add… and Edit of an eWay row are no-ops with `TODO(T7)`.

Swift / C# naming suggested: `MalachiCore/CRM/` enum namespace `CRM`
(`CRM.available`, `CRM.canSave`, `CRM.offer(for:rows:)`, `CRM.Offer`,
`CRM.Row`, `CRM.ButtonState` cases `.hidden/.notSaved/.saving/.saved`,
`CRM.InFlight` struct, `CRM.MenuID` raw strings as Go, `CRM.Dialog` final
class, `CRM.Bar`, `CRM.Pill`, `CRM.Text` for the text functions,
`CRM.Wizard` for pages/steps/fields/`failure(of:)`), the flow
`EWayWizardController` (`@MainActor`, `MalachiCore/Controllers`); tests
`CRMTests`, `CRMDialogTests`, `CRMTextTests`, `EWayWizardControllerTests`
(1:1 from `crm_test.go`, `dialog_test.go`, `text_test.go`,
`eway_flow_test.go`). Windows: `Malachi.Core.Crm` namespace with a static
`Crm` class (as `Cal`), `Crm.Dialog`, `EWayWizardController`.

Msgids (cs; plurals in the three Czech forms):

| msgid | cs |
|---|---|
| Save to eWay-CRM | Uložit do eWay-CRM |
| Saving to eWay-CRM… | Ukládá se do eWay-CRM… |
| In eWay-CRM | V eWay-CRM |
| Saved in eWay-CRM | Uloženo v eWay-CRM |
| Open in eWay-CRM | Otevřít v eWay-CRM |
| Edit Links… | Upravit vazby… |
| Remove from eWay-CRM | Odebrat z eWay-CRM |
| Save to eWay-CRM… | Uložit do eWay-CRM… |
| Save %d Message(s) to eWay-CRM… | Uložit %d zprávu / zprávy / zpráv do eWay-CRM… |
| Search eWay-CRM | Hledat v eWay-CRM |
| Superior Item | Nadřazená položka |
| Private (ctx crm) | Soukromý |
| Only you will see this message in eWay-CRM. | Tuto zprávu uvidíte v eWay-CRM jen vy. |
| Nothing in eWay-CRM matches these addresses. | K těmto adresám v eWay-CRM nic není. |
| eWay-CRM did not answer in time; some suggestions may be missing. | eWay-CRM neodpověděl včas, některé návrhy mohou chybět. |
| Edit Links | Upravit vazby |
| Contacts · Companies · Deals · Projects (ctx crm) | Kontakty · Společnosti · Obchody · Projekty |
| Saved to eWay-CRM | Uloženo do eWay-CRM |
| %d message(s) saved to eWay-CRM | %d zpráva uložena / zprávy uloženy / zpráv uloženo do eWay-CRM |
| %d message was / messages were not saved to eWay-CRM. *(new)* | %d zprávu / zprávy / zpráv se nepodařilo uložit do eWay-CRM. |
| Removed from eWay-CRM | Odebráno z eWay-CRM |
| Remove from eWay-CRM? | Odebrat z eWay-CRM? |
| “%s” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays. | Zpráva „%s“ bude z eWay-CRM smazána i s vazbami, pro všechny jeho uživatele. Zpráva v Malachi Mail zůstane. |
| eWay-CRM already has this message. | eWay-CRM už tuto zprávu má. |
| eWay-CRM no longer has this item. *(new, 1109)* | Tato položka už v eWay-CRM není. |
| This eWay-CRM does not allow access through its API. | Tento eWay-CRM nedovoluje přístup přes své API. |
| This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet. | Tento eWay-CRM se přihlašuje účtem Microsoft, což Malachi Mail zatím neumí. |
| Saving to eWay-CRM *(new, action)* | Uložení do eWay-CRM |
| Contacting eWay-CRM *(new, action/progress)* | Spojení s eWay-CRM |
| Searching eWay-CRM *(new, action)* | Hledání v eWay-CRM |
| Removing from eWay-CRM *(new, action)* | Odebrání z eWay-CRM |
| Downloading the message *(new, action)* | Stažení zprávy |
| Downloading the message first… | Nejdřív se stahuje zpráva… |
| Downloading %d message(s) first… *(new plural)* | Nejdřív se stahuje %d zpráva / stahují %d zprávy / stahuje %d zpráv… |
| Add eWay-CRM Account | Přidat účet eWay-CRM |
| Web Service Address | Adresa webové služby |
| The address of your eWay-CRM, such as https://hosting.eway-crm.com/company | Adresa vašeho eWay-CRM, například https://hosting.eway-crm.com/firma |
| Malachi Mail connects to eWay-CRM over https only. | Malachi Mail se k eWay-CRM připojuje jen přes https. |
| Save messages to your CRM with their contacts, companies and deals *(integrations)* | Ukládání zpráv do CRM s kontakty, společnostmi a obchody |
| %s will be removed from Malachi Mail. Messages saved in eWay-CRM stay there. *(integrations)* | Účet %s bude z aplikace Malachi Mail odebrán. Zprávy uložené v eWay-CRM tam zůstanou. |
| Services beside your mail: issue trackers, calendars and CRM *(integrations, replaces the old one)* | Služby vedle pošty: sledování úkolů, kalendáře a CRM |

Reused, not new: "_Save", "_Remove", "(No subject)", "Edit Account",
"Sign In", "Account Name", "User Name", "Password", "New Password (leave
empty to keep)", "Enter the password for this account", "The server
rejected the user name or password", "This is not a web address", "_Next",
"_Add Account", "Signing in", "Adding the account", "Saving the account".
The new msgids beyond D20 are the action forms the clients' general error
sentence needs ("%s failed: …"), the 1109 text, the partial-failure toast
and the plural of the download status.

### 2.6 GTK

- Action bar (`ui/data/ui/window.blp` and `message_window.blp`, next to
  `message_menu` / `assistant_button`): `crm_button` (a `Gtk.Button` in
  state Save, a `Gtk.MenuButton` with `crm_menu` in state Saved — two
  widgets, one visible), actions `win.crm-save`, `win.crm-show`,
  `win.crm-edit`, `win.crm-remove` (and `msg.*`); it moves into the More
  actions menu below the narrow breakpoint as the other action buttons
  do. `window/crm_actions.go`, `window/crm_dialog.go` + `crm_dialog.blp`
  (`Adw.Dialog` with a search entry, `Gtk.ListBox` groups,
  `Gtk.CheckButton`s, a `Gtk.Switch` row for Private), `window/crm_bar.go`
  (the bar under the header, built like `window/bulk.go`'s strip, plain
  labels only, `SetUseMarkup(false)`), the list pill in
  `widget/message_row.go` + `message_row.blp` (`crm_pill` beside
  `bulk_pill`, `widget/pill.go` style), context menu in
  `window/context_menus.go`.
- Wizard: `accountwizard/eway_flow.go` (pure flow, tests against a fake
  daemon) + `accountwizard/eway.go` + `ui/data/ui/eway_wizard.blp`;
  settings `ui/internal/ewayaccount` (`Controller` + `Dialog` +
  `eway_account.blp`), like `jiraaccount`.
- Integrations: `integrations.Catalog` gains eWay-CRM (icon
  `x-office-address-book-symbolic`, **verify** in the Fedora 42 theme),
  `RowFor` an eway case (`EditorEway`, remove body of D20, switch
  offered), `window/integrations_page.go` opens the wizard and the
  settings dialog.

### 2.7 macOS and Windows

| Piece | GTK (reference) | macOS | Windows |
|---|---|---|---|
| pure model | `ui/internal/crm` | `MalachiCore/CRM/*.swift` | `Malachi.Core/Crm/*.cs` |
| API types | `pkg/api/crm.go` | `MalachiCore/API/CrmAPI.swift` | `Malachi.Core/Api/Crm.cs` (+ `GoContractTests`) |
| button + menu | `window/crm_actions.go` | `MessageView/CrmButton.swift`, toolbar item in the message window | `Reader/CrmButton` (`SplitButton`/`DropDownButton`) |
| dialog | `crm_dialog.blp` | `MalachiMail/CRM/CrmDialogController.swift` (sheet) | `Malachi.App/Crm/CrmDialog.xaml` (`ContentDialog`) |
| bar | `window/crm_bar.go` | `MessageView/CrmBarView.swift` (`stringValue` only) | `Reader/CrmBarView` (`TextBlock.Text`) |
| pill | `crm_pill` | `MessageCellView` | `Main/MessageRowView` |
| context menu | `context_menus.go` | `MessageContextMenu.swift` | `ContextMenus` + `MessageListPane` |
| wizard | `accountwizard/eway*.go` | `AccountWizard/EWay/EWaySheetController.swift` | `Wizard/EWayWizardWindow` |
| settings | `ewayaccount` | `Preferences/EWayAccount/` | `Preferences/EWayAccountWindow` |
| Integrations | `integrations.Catalog` | `Model/Integrations.swift` | `Model/Integrations.cs` |

Ports copy the Go tests 1:1; every string with the GTK msgid as key.

#### As built (T6, macOS, 2026-10-17)

Where the macOS client differs from the table above (details in
`docs/macos-port.md` "eWay-CRM", the deviations in `macos/README.md`):

- Files: `MalachiCore/API/CRMAPI.swift`, `MalachiCore/CRM/CRM.swift`,
  `CRMDialog.swift`, `CRMText.swift` (enum `CRM`, Go names in lower
  camel case; `PillLabel` is `pillBrand`; `SaveToast` returns
  `(text: String?, error: RPCError?)`), `Controllers/CRMController.swift`
  (`CRMState`, `CRMDialogSession`, `CRMController`),
  `Controllers/EWayWizardController.swift`; AppKit `MalachiMail/CRM/`
  (`CRMToolbarItem`, `CRMDialogController`, `CRMBarView`),
  `AccountWizard/EWay/EWayWizardWindowController.swift`,
  `App/Integration+CRM.swift`. There is no `Preferences/EWayAccount/`:
  the Integrations row's Edit opens the assistant in its edit mode
  (`Integrations.Editor.eway`, as the Go `EditorEway` says).
- The button is a toolbar item before ✦ Assistant in the main window and
  the message windows (not in the message view); the same items are in
  the Message menu after Forward and in the list's context menu.
- One message per selected row: a folded conversation row saves its
  shown message (the newest member in the folder), as Reply takes it,
  so `Offer.Count` is the number of rows.
- The dialog opens at once with a spinner while crm.lookup runs (a
  failed lookup shows its sentence with only Cancel); Save closes it and
  crm.save runs in the background with the messages busy in every
  window; the partNotDownloaded round downloads and asks again at most
  twice, with `DownloadingText` as a toast.
- `CRMState` keeps what a finished crm.save or crm.remove reported for
  10 s, so the button and the bar turn before the daemon's
  notify.messagesChanged refreshes the list and the message.
- Timeouts (client side): `account.detectEway` 15 s, `crm.lookup` and
  `crm.search` 30 s, `crm.save` 15 min (20 uploads of up to 2 min each,
  the daemon has no budget for the whole call), `crm.remove` 30 s; the
  assistant's budgets are eway_flow.go's (15, 50, 60 s).
- Checked here: `swift build`, the filtered `swift test` (CRM, dialog,
  texts, controller, wizard, API, context menu, selection, Integrations,
  folder tree), `check-strings.py` (no msgid missing); the dialog and
  the assistant measured once with the debug binary over sample data
  (`MALACHI_START` `crm-dialog`, `eway-wizard`). Not checked: anything
  against a real daemon or eWay-CRM (Gate B), the toolbar button's
  states on screen.

#### As built (T7, GTK, 2026-10-19)

Where the GTK client differs from §2.6 (written before the macOS client;
the port follows macOS as built, the deviation rows are in
`macos/README.md`):

- Files: `window/crm.go` (the application's state — the eWay account of
  `account.list`, the messages a save or removal runs for, a finished
  call's own answer kept 10 s — and `refreshCRM`, the one function that
  redraws every control), `window/crm_actions.go` (`crmControls` per
  window: the button, the actions `win.crm-save`, `win.crm-open`,
  `win.crm-edit-links`, `win.crm-remove` and `msg.*`, the More Actions
  section, save with download-and-retry at most twice, remove after the
  question), `window/crm_dialog.go` + `ui/data/ui/crm_dialog.blp`,
  `window/crm_bar.go`, `accountwizard/eway.go` + `eway_wizard.blp`; the
  smoke tests `window/crm_gtk_smoke_test.go`,
  `accountwizard/eway_gtk_smoke_test.go`, `widget` `TestCrmPillRow`.
  There is no `ui/internal/ewayaccount`: as on macOS, the Integrations
  row's Edit, the eWay catalogue row's Add… and the account's sign-in
  and certificate banners open the assistant (`NewEway`, `NewEwayEdit`).
- The button is `crm_slot` in the header bar of the reading pane and the
  message window between Trash and the Star, a group of its own with
  12 px margins on both sides (`[end]` children of an `Adw.HeaderBar`
  pack right to left, so the slot is declared after `star_button`);
  icon-only with the label as tooltip like every header-bar action, and
  `has-frame: false` on both widgets (inside the slot's Box the bar's CSS
  for its direct children does not flatten them; seen in Gate C); the
  saved state shows the CRM icon with a check mark and opens the menu;
  while saving a spinner replaces the icon and the button is off. Below
  the 600 sp breakpoint (the main window's narrowest, the message window
  got one of its own) the slot hides; More Actions always lists the items
  (Save with the count of several rows, or Open / Edit Links… / Remove).
- One message per selected row as on macOS; a folded conversation row
  offers nothing (`crmSubject.conv` → `crm.Row.Conversation`), so the
  main window's subjects are the shown messages of `selectedRows()`.
- The dialog: Cancel and Save in the header, Enter in the search field
  saves as the Mac's default button does, Superior Item is a radio suffix
  on each deal or project row (one group across both), Private is an
  `Adw.SwitchRow` with its hint as subtitle, the search delay is
  `GtkSearchEntry`'s own 300 ms; the groups are rebuilt only when the
  listed items change, so the keyboard focus stays while check boxes and
  radios are set.
- The bar is `crm_bar` under the header (`toolbar remote-bar crm-bar`
  classes) in both Blueprints and made on first need on a conversation
  card; Open in eWay-CRM launches the daemon's URL at once (the comment in
  `crm_bar.go` says why: D15, `crm.OpenURL` https only).
- Refresh: `refreshCRM` runs on every selection change
  (`setMessageActionsSensitive`), every message load (`settleLoaded`),
  the accounts loaded (`loadAccounts`) and `notify.messagesChanged`
  (`crmMessagesChanged`: the cache let go of the account's messages, so
  every open message window of it, and the pane when it does not reload
  itself, asks `message.get` again — the body stays as shown — so the
  saved state does not flip back once the 10 s override expires).
- The pill is `crm_pill` after `bulk_pill` (teal, `label.issue-pill.crm-pill`),
  only on message rows (`SetThread` clears it).
- The assistant is an `Adw.Dialog` of the Jira assistant's shape
  (520 × 560): Web Service Address with the hint and the problem line
  under the field → Sign In with the address read only, name, user,
  password (`Adw.PasswordEntryRow`) and the follow-conversations check box
  as a prefix of an `Adw.ActionRow` with the hint as subtitle; the
  header's Back, Escape and a swipe are the flow's Back.
- Timeouts as on macOS: `crm.lookup` and `crm.search` 30 s, `crm.save`
  15 min, `crm.remove` 30 s; the assistant's are `eway_flow.go`'s.
- Dev steps `MALACHI_GTK_START=crm-dialog` (invented suggestions, no
  daemon) and `eway-wizard`. No new msgid; `po/` changed in `#:` lines
  only; nothing added to `po/POTFILES`.
- Checked in the Apple container (2026-10-19): `gtk.sh make build`,
  `make lint`, `make po` (no new msgid), the plain tests of `window`,
  `widget`, `style`, `crm`, `integrations`, `contextmenu`, `selection`,
  `accountwizard` and the smoke tests (`MALACHI_GTK_SMOKE=1`) of `window`,
  `widget` and `accountwizard`; one headless run of `crm-dialog;eway-wizard`.
- **Gate C passed** (owner, 2026-10-20): `make run-gtk-live` over a copy of
  his data against his instance, in XQuartz. His two findings were in the
  container image, not the client: the CRM icon drew as image-missing
  because the image had no `librsvg2` (gdk-pixbuf's SVG loader; GTK's own
  bundled icons are PNG, which hid it for every other icon), and the
  button in its Box was not flattened by the header bar's CSS
  (`has-frame: false` now). The image also gained Adwaita Sans and the
  emoji fonts, and `gtk.sh x11` writes a GNOME-like `gtk-4.0/settings.ini`.

#### As built (T8, Windows, 2026-10-19)

Written on a Mac without .NET as the port of the Go reference and the
macOS client; nothing compiled or run yet (Gate D on the owner's Windows
machine). Details in `docs/windows-port.md` §11.11, the deviation rows in
`windows/README.md`:

- API: `Malachi.Core/Api/Crm.cs` (Go's `GUID` members are `ItemGuid`),
  `AccountKind.Eway`, `Capability.Crm`, `AccountConfig.Eway`,
  `AccountTestResult.Eway`, `MessageSummary.CrmSaved`, `Message.Crm`, the
  codes 1109/1205/1307, the ten limits, five methods (101 in all);
  `GoContractTests` reads crm.go both ways, `ApiRoundTripTests` has a
  sample of every record.
- Core: the package `crm` is three static classes in the namespace
  `Malachi.Core.Crm` — `CrmRules` (crm.go), `CrmDialog` (dialog.go),
  `CrmText` (text.go) — because a class `Crm` inside a namespace `Crm`
  would be hidden from the other namespaces (as Jira in `IssueTrackers`);
  types carry a `Crm` prefix (`CrmRow`, `CrmOffer`, `CrmInFlight`, …).
  `Controllers/CrmController.cs` (`CrmState`, `CrmDialogSession`,
  `CrmController`) and `EwayWizardController.cs` (`ewayFlow` with the Jira
  wizard's event names). `ContextMenu` + `MessageState.WithCrm`,
  `Selection.Combine` and `ActionFlags.Crm`/`CrmSaved` (filled by Combine,
  not yet fed per row; the context menu reads the controller's offer
  directly), `Integrations` (catalogue entry, `Editor.Eway`, the row),
  `FolderTree.InMail` excludes eway, `AccountHeaderBadge` "EWAY",
  `MessageRow.CrmPillText`/`CrmPillTooltip`.
- App: `Crm/CrmToolbarButton` (Save / Saving with a `ProgressRing` / In
  eWay-CRM as a `DropDownButton`), `Crm/CrmSaveDialog` (a `ContentDialog`
  built in code, Save primary on the left, queued through
  `IAlerts.ShowDialogAsync`), `Crm/CrmBarView`, `Crm/CrmWindowSupport`,
  `MainWindow.Crm.cs` (the button between Trash and the Star, the context
  menu through `MessageListPane.Crm`), the message window's own
  controller, the bar last in the reader's header and after the bulk strip
  on a conversation card, `IssuePill.IsCrm` (the attention fill: Windows
  has no teal), `Wizard/EwayWizardWindow` + `EwayServicePage` +
  `EwayCredentialsPage` (modal over Preferences), Integrations Add… and
  Edit, glyph ContactInfo (U+E779) for `x-office-address-book`.
- Deviations from macOS: More Actions has no CRM items (the button is
  beside it and the command bar does not fold); a save or removal releases
  its busy mark even when its window closed first (the Swift port had
  skipped that — fixed on macOS the same day, `CRMController.runSave`/
  `runRemove` capture the state, test
  `endsTheBusyMarkWhenTheWindowCloses`); no UI smoke tests (no sample-data
  hookup for the dialog; `CrmDialogSession.Sample()` exists unused).
- msgids: every msgid the new code uses is in `po/malachi.pot` and every
  eWay msgid of the template is used (checked by script both ways);
  `windows/parity-exclusions.txt` unchanged.
- Tests written: `Crm/CrmRulesTests`, `CrmDialogTests`, `CrmTextTests`,
  `Controllers/CrmControllerTests`, `EwayWizardControllerTests`, and the
  updated `ApiCodingTests` (101 methods, 48 codes), `GoContractTests` (72
  limits), `ApiRoundTripTests`, `ContextMenuTests`, `SelectionTests`,
  `IntegrationsTests`, `FolderTreeTests`, `JiraSidebarTests`.
- Gate D (owner, from `windows\`): `build.ps1 app` (expect a few compile
  fixes: collection expressions in tuple test cases, `default` passed to
  `Assert.Equal`, controls built in code, the `Crm.` prefixes from
  `Malachi.App.Reader`), `build.ps1 test` (the classes above;
  `IconGlyphsTests.EveryIconNameOfTheGtkUiHasAGlyph` was failing before
  this work on the calendar icons), `build.ps1 lint` (SPDX,
  `StringsCheckTests`), the UI smoke tests (`CalendarPreferencesSmokeTests`:
  the Integrations page has a third card; `SmokeTests`), then a manual walk
  over a copy of the data against the owner's instance: wizard (http
  refused, a Microsoft instance stopped on the address page, default
  name, follow-conversations, wrong password, Add, Edit with an empty
  password and a changed user), button states (hidden, Save, spinner,
  drop-down, count, conversation row), dialog (spinner, groups, superior
  radio, search, hints, Private, Save), bar and pill, Edit Links, Remove,
  context menu, thread following.

### 2.8 MCP (later, T11)

`cmd/malachi-mcp`: `lookup_crm` and `save_to_crm` over `crm.lookup` and
`crm.save`; `docs/mcp.md` gets a "CRM" section. Not in v1.

---

## 3. Security

To go into `docs/security.md` as **§7.3 Saving mail to a CRM** (the
next free subsection of §7 Transport, after §7.2 Unsubscribing), with a
line in §6 Credentials and a row in the §12 review checklist.

- **A new outbound flow.** Until now a whole received message left the
  device only as a forward the user sent. `crm.save` sends the message as
  received — every header, the body, every attachment, every recipient
  visible in it — to the user's CRM, where everyone with access to that
  CRM (subject to `IsPrivate`) can read it. It happens **only on an
  explicit action** of the user on the named messages: no rule, no
  background save, no MCP tool in v1, and the dialog shows the eWay
  account it goes to. Removing it deletes it for every CRM user, after a
  question.
- **https only**, to the account's service URL; `InsecureAPI.svc` and
  http URLs are refused at validation and at every hop. No certificate
  pinning in v1 (system trust store; Jira has none either).
- **Credentials only to the origin.** The password lives in the keyring
  and is used only to compute the `LogIn` hash sent to the service URL's
  origin; the session id goes only in request bodies and the upload's
  query to the same origin. Redirects are followed by the client itself,
  only within that origin, never with a body that carries the session to
  another host; a cross-origin redirect is an error. Neither the
  password, its hash nor the session id is logged or part of an error.
- **The server is untrusted input.** Every string from eWay (names,
  e-mails, states, descriptions, `Description` of an error) is cleaned
  and capped as Jira's (`internal/eway/clean.go`), GUIDs checked for
  shape, a wrongly shaped answer is an error not an empty list; clients
  show them as plain text only (rule 3). eWay's `GetEmailMessage`
  `HtmlBody` is never requested for display — what Malachi shows is its
  own stored copy.
- **Suggestions read addresses only**, never the body; the user's own
  addresses are not searched; lookups go only to the user's eWay.
- **The open link is never taken from server data** (D15): built from the
  stored service URL and a GUID of fixed shape; it goes to eWay's
  `open.eway-crm.com`, which learns the service URL and the item GUID
  (that is its purpose).
- **What eWay learns about the device**: a random client id, the constant
  machine name "Malachi Mail", the app version (D5); not the host name.
- **Logging**: only the method, the error class (`rc…`, HTTP status,
  transport class) and counts; never subjects, addresses, names, GUIDs of
  contacts, or bodies.

---

## 4. Tasks and gates

Announce the tests each task runs (owner rule); no full suites, fuzz or
`-race` on the owner's Mac without asking. No commit without the owner's
word (CLAUDE.md).

- **T0 · Trial checks** (agent, only with the owner's permission: it
  writes to eWay's shared trial). Answer every **verify** of §0.3 with a
  script in the scratchpad (never in the repo), clean up what it creates
  (`DeleteEmail`, `DeleteContact`). Output: a short table appended to §1
  "As verified". Gate: the owner reads it and answers §5.
- **T1 · Contract.** `pkg/api/crm.go`, `types.go` (kind, capability,
  `EwayConfig`, `CrmSaved`, `Crm`), `errors.go` (1109, 1205, 1307),
  `methods.go`, `service.go`; `docs/api.md` §2 rows, §4.1
  (`account.detectEway`, eway in add/update/test), §4.17, §5 note, §7
  changelog. Tests: `TestDocsCoverAllMethods`, JSON round trips.
- **T2 · `internal/eway` + `ewaytest`.** Client, session, types, clean,
  query, save, link, `NormaliseServiceURL`, supervisor. Tests against the
  fake: upload-before-save order, the four-field hash and
  `rcDuplicateEmail`, re-login on `rcBadSession` exactly once, the
  `authFailed` latch, Cloudflare 403/1010, Retry-After, redirect rules
  (cross-origin, https → http), caps, hostile fixtures in
  `backend/testdata/eway`, a fuzz target for the answer decoder (not run
  on the Mac without asking). Optional live test behind
  `MALACHI_TEST_EWAY=<ws>` + `MALACHI_TEST_EWAY_USER` +
  `MALACHI_TEST_EWAY_PASSWORD` (skipped otherwise; saves one synthetic
  message, links nothing, deletes it).
- **T3 · Store.** Migration `0021_crm_links.sql`, `crm_links.go`,
  `crmSaved` in listings, `RealmOf`. Tests: copies share the row,
  account removal drops rows, listing cost with an empty table.
- **T4 · Core.** `accounts_eway.go` (validation, detect, test, kind table,
  capabilities, `mailOnlyMessages`), `crm_*.go`. Tests with `ewaytest`:
  partNotDownloaded then success after download, Outbox/Drafts/Jira
  refused, conflict, dedup paths, notifications, stale re-check.
  **Gate A (owner):** a native daemon over a copy of his data
  (`MALACHI_DATA_DIR` + own `MALACHI_SOCKET`) and a throw-away script or
  `malachi-rpc` against his instance `hosting.eway-crm.com/satomar`: he
  adds the account with his own credentials (the daemon holds them,
  never the agent), saves one message of his choice, sees it in eWay,
  removes it.
**Client order (owner, 2026-10-17):** the macOS client comes first and
the GTK and Windows ports follow only after the owner has verified and
approved it — an exception to rule 6 of `CLAUDE.md` of the same kind as
the board and the calendar. The pure Go package stays the reference for
rules and msgids (the `.pot` is generated from it), so it precedes the
macOS client; the GTK widgets wait.

- **T5 · Go UI reference and msgids.** `ui/internal/crm` (+ `po/POTFILES`),
  `contextmenu`, `selection`, `capabilities.InMail`, `integrations`
  (Catalog, RowFor, `EditorEway`, the new page description), tests;
  then `make po` in the Apple container with the Czech strings of D20 in
  `po/cs.po` (so `check-strings.py` on macOS and the Windows coverage test
  see every msgid), `make lint`, `gtk.sh stop`. The GTK client still
  compiles (the changed packages are pure), its new widgets come in T7.
- **T6 · macOS.** The client of §2.7 with tests; `swift build`, filtered
  `swift test --filter 'Crm|Integrations|ContextMenu|Selection'`,
  `check-strings.py`; debug binary over sample data at most 3 times.
  **Gate B (owner):** his own build over a copy of his data and his
  instance: wizard, Integrations row, button states, dialog,
  multi-selection, bar, pill, Edit Links, Remove. **T7 and T8 start only
  after his approval.**
- **T7 · GTK** (after Gate B). Widgets of §2.6 in the Apple container:
  `gtk.sh make build`, `go test ./internal/crm ./internal/integrations
  ./internal/window -run 'Crm|Integr|Context'`, `make po` (no new
  msgids expected), `make lint`; optional `MALACHI_GTK_SMOKE=1` smoke over
  sample data; `gtk.sh stop`. **Gate C (owner):** `make run-gtk-live`
  over a copy: button states, dialog, multi-selection, bar, pill, narrow
  layout, wizard, Integrations row.
- **T8 · Windows** (after Gate B). Port with tests (`Malachi.Core.Tests/Crm`,
  API samples, `GoContractTests`), UI smoke over sample data; on the
  owner's Windows machine `build.ps1 app`/`test`/`lint`. **Gate D (owner).**
  Until T7/T8 land, the GTK and Windows clients show an eWay account on
  Integrations only as "a kind this client does not know" (EditorNone):
  no crash, no mail sidebar entry (`InMail` from T5), no save button.
- **T9 · Docs.** `docs/security.md` §7.3 (+ §6 line, §12 row),
  `docs/architecture.md` §3.9 "CRM accounts (`kind: eway`)" and a §7
  entry, `CLAUDE.md` state paragraph (orchestrator), README deviation
  tables if a client deviates, "As built" notes back in this file.
- **T10 · SMTP base64 follow-up** (only if §5 Q6 says yes; its own
  branch).
- **T11 · MCP** (v1.5): `lookup_crm`, `save_to_crm`, `docs/mcp.md`.

---

## 5. Questions and their answers (2026-10-17)

Answered by the owner (AskUserQuestion) where marked *owner*; the rest
are the orchestrator's defaults, open to change before the task that
uses them.

1. **Several deals/projects** — *owner*: one superior item
   (`OUTLOOKPROJECT`, the first deal or project the user checks) and the
   rest as plain links (`GENERAL`), as in D10.
2. **Prefill of deals/projects** — default: the superior item is
   pre-checked only when exactly one open deal or project was found (the
   Outlook add-in's rule); otherwise all are listed unchecked.
3. **Remove from eWay-CRM in v1** — *owner*: yes, with the confirmation of
   D18 saying the item disappears for every CRM user.
4. **"Private" default** — default: off, not remembered.
5. **Button label once saved** — default: "V eWay-CRM" (wireframe).
6. **SMTP base64 for non-ASCII text parts** (D17) — default: a separate
   follow-up on its own branch, proposed to the owner after v1.
7. **"Uložit přílohy jako dokumenty…"** — default: not shown until it
   exists (no disabled menu items); its msgid waits with it.
8. **Integrations page description** — default: change it to
   `Services beside your mail: issue trackers, calendars and CRM`
   (cs `Služby vedle pošty: sledování úkolů, kalendáře a CRM`); the old
   msgid goes away in all three clients in the same step.
9. **Keyring** — default: the plain password under `credentials.password`
   (uniform with Jira and CalDAV; D3).
10. **Pill in the message list** — default: shown (wireframe).
11. **Bar and menu wording** — default: unified as `Open in eWay-CRM`
    (cs `Otevřít v eWay-CRM`) for the bar's button and the menu item;
    `Open in eWay` and `Show in eWay-CRM` are dropped from D20.
12. **Several eWay accounts** — *owner*: one per installation in v1. The
    daemon does not forbid a second account (realms differ), but the
    clients use the first enabled eWay account in list order, the dialog
    has no account popup and there is no `crm-last-account` key; D11's
    popup is deferred.
13. **T0 on the trial** — *owner*: allowed; the agent cleans up after
    itself and records the answers in §0.3 / §1 ("As verified").

---

## 6. As built (T1–T4, daemon, 2026-10-17)

Where the build follows T0 or deviates from §1–§3:

- **Contract (T1).** As D13 with these additions: `MinCrmQueryBytes` (2),
  `MaxCrmNameBytes` (256) and `MaxCrmDetailBytes` (512) are named
  constants in `pkg/api/crm.go`; `AccountTestResult.eway` is the eway
  probe; `crm.save`'s `partNotDownloaded` data is the type
  `MessagesNotDownloadedData {messageIds}`; `CrmLookupResult.saved[].record`
  is always sent (`null` when not saved). `AccountEway` and
  `CapabilityCrm` are in `types.go` with their siblings. `Backend.Crm()`
  is a new service (the stub answers `notImplemented`).
- **Detection (D3, T0 1).** `GET` only. An instance whose service does
  not know the method (404 HTML) is `microsoftSignIn: false` and is not an
  error; an answer that is not eWay-CRM's JSON is `serverError`.
  `account.add` runs the detection itself and refuses an Entra instance
  with 1205 `microsoft` before the password is sent.
- **Sign-in (D4, T0 2–3).** `rcLoginFailed` (not `rcBadLogin`) latches
  `authFailed` until the account is edited (`account.update` restarts the
  client). The `plan` reason is mapped from any `LogIn` return code
  containing "licen" — **unverified** (the trial cannot show it). No
  `Description` of any answer is logged or put in an error (T0 3);
  errors carry the method and the return code only. The user record is
  read for `Email1Address` only. `account.add` signs in once (verifies the
  password, sets `userGuid`, fills an empty `email` from the user record,
  else from the user name when it is an address); `account.update` keeps
  the stored `userGuid` when the instance and user stay.
- **Superior item (D10, T0 4).** Through the e-mail's foreign key in
  `SaveEmail` (on creation in the same call, later as an update), never
  `SaveRelation OUTLOOKPROJECT`; a new superior replaces the old one (the
  old deal or project is then not linked at all — the CRM keeps one).
  Further deals and projects: `GENERAL`. A `GENERAL` that already exists
  (`rcDuplicateRelation`) counts as done.
- **Unlink (D10, D18, T0 5).** `DeleteRelation {relationDataGuid}` for
  every relation of the item, the superior one included (which also
  clears the foreign key in the fake; **verify** at Gate A that the real
  service clears it).
- **Duplicates (D8, T0 10, 12).** The Message-ID is sent and searched
  with angle brackets (the store keeps it without). After
  `rcDuplicateEmail`, or any other refusal of `SaveEmail`, the daemon
  calls `DeleteEmail` on its own GUID (best effort, the service answers
  success regardless) and documents the orphan risk in security.md §7.3.
  "Several found → the oldest" orders by `ItemCreated` when the answer
  has it (the field name is assumed, not verified; without it the first
  is taken).
- **Privacy (D18, T0 11).** Changed only for an item this call created
  (sent in `SaveEmail`) or one the local record already knew (an edit);
  an item found by Message-ID that someone else saved keeps its flag.
- **Suggestions (D9, T0 6–8).** Contacts' companies through
  `GetContactsByItemGuids {includeForeignKeys}`; tracked domains through
  the typed `Query` LIKE `%domain%` on `TrackedDomains`, then only
  companies whose list names the domain exactly (a `LIKE` alone would
  match `xbeta.example` for `beta.example`); open = `!IsCompleted &&
  !IsLost`; "newest first" by `ItemChanged`. The freemail list is in
  `internal/eway/clean.go`. The address cache lives in the account's
  client (cleared when the client is rebuilt).
- **Search (D9, T0 7).** Typed `Query` LIKE `%query%` on `FileAs` per
  kind, contacts also on `Email1Address`, companies on `Email`; the
  user's `%`, `_`, `[`, `]` are replaced by spaces (matched as written).
- **Size (D6, T0 9).** The cap stays `ingest.MaxMessageBytes` (25 MiB),
  per message as `attachmentTooBig` in its outcome; no lower limit of the
  service was found.
- **Open link (D15).** Base64url **without** padding, `ws` = the service
  URL without `/API.svc`, `l` = `eway://<Folder>/<guid lower case>` —
  the design's assumption, **not verified** by T0: Gate A checks that the
  link opens the item.
- **Call-level vs per-message errors of `crm.save`.** A sign-in, keyring,
  network or TLS failure before the first message is saved fails the
  call; after one was saved, the remaining messages carry it.
  `crmItemNotFound`, `crmDuplicate`, `attachmentTooBig` and other CRM
  refusals are per message.
- **Notifications (D13).** `notify.messagesChanged` of the mail account
  with the folders of all copies (`store.CrmFoldersOf`), coalesced with
  the issue-mail notifications (`Backend.messagesChanged`); also when
  `crm.lookup` finds a message newly saved or no longer saved.
- **Store (D12).** `crm_links` as designed, plus `CHECK (private IN
  (0,1))` and an index on `mail_account_id` alone; `crmSaved` counts only
  enabled CRM accounts (a JOIN on `accounts.enabled`) and rides on the
  listings' per-page invitation pass (`attachInvitationsTo`), skipped
  with one `EXISTS` while the table is empty.
- **Clients' next step (T5+).** `crm.lookup` without `accountId` returns
  the account it used; `crmSaved` reaches the list through
  `message.list`/`thread.list`/`search.query` and changes through
  `notify.messagesChanged`; on `partNotDownloaded` the client runs
  `message.download` for each id in `data.messageIds` and calls
  `crm.save` again; every `url` is safe to open without asking.

### Review fixes (after T4, 2026-10-17)

An independent read-only review of the daemon found eight defects; all
are fixed with tests (`go vet` clean; `pkg/api`, `rpc`, `eway` (26),
`store` (295) and the whole `core` package pass):

- **Forged Message-ID (D8, the serious one).** A message with a local
  record only ever reads, relinks or deletes *that* recorded item; the
  GUID is never swapped to another item, and a recorded item gone from
  the server deletes the local row (the next save creates a new item).
  Without a record, an item found by Message-ID counts as this message
  only when its `SenderEmailAddress` equals the From address (case
  insensitive) and its `SentOn` matches the Date to the minute (a date
  without a zone is accepted against UTC or the message's own wall-clock
  time); otherwise the message is not saved, `crm.remove` answers
  `crmItemNotFound` and a save creates a new item. Applies to
  `crm.lookup`, `crm.remove`, the background re-check and the reuse in
  `SaveMessage`.
- **Unknown items** named in `link`, `unlink` or `superior` are checked
  (`Client.CheckItems`, once per kind) before anything is uploaded; the
  whole call fails, as docs/api.md §4.17 promises.
- **Memory.** Wholeness and size come from the memory cache, `RawState`,
  `RemoteParts` and the raw file's size without reading it; each raw
  message is read right before its upload and dropped afterwards.
- **Stale re-check** claims the message's busy key (skipped while a save or
  remove holds it), backs off an hour after a failure (`MarkCrmChecked`)
  and runs under the client's context, which `Close` cancels.
- **Partial lookup** no longer records a failure in the account's state
  once the caller's budget context has ended (the account is not marked
  offline by a slow lookup).
- **Orphans.** After a transport failure of the upload or of `SaveEmail`
  the daemon deletes the GUID best effort under its own 10 s context
  (docs/security.md §7.3 says so).
- **Superior unlink** sends `SaveEmail` with the deal or project key as an
  explicit JSON `null` after `DeleteRelation`; a superior that is
  replaced while it stays checked becomes a `GENERAL` link (`ApplyLinks`
  re-reads the relations after the key changed; one extra read).
- **Lifecycle.** `Client.Close` sets a stopped flag (a later sign-in
  answers `unavailable`), cancels the context and logs out, also a
  sign-in that finishes after Close; `attachCrmSavedTo` requires the CRM
  account's kind to be `eway`; `ForgetCache` runs after a save with at
  least one success.

Gate A additions for the owner: the `SentOn` format `GetEmailsByItemGuids`
returns on his instance; unlinking the superior clears its key; saving
the same message twice finds the existing item; switching the superior
from a deal to a project while the deal stays checked leaves the deal
linked.

### Gate B findings (macOS, 2026-10-17)

- **Add failed with "empty address".** The owner's eWay user name is not an
  address and his CRM user has no e-mail, so `AccountConfig.Email` stayed
  empty and the generic validation refused it. Fixed in the daemon
  (`validateAccountConfig`, `store.AddAccount`/`UpdateAccount`): an eway
  account is the only kind that may have no address (D2, `TestEwayAccountWithoutAddress`).
- **The wizard's banner wrapped into a narrow column.** `WizardBannerView`
  now feeds its label the width it got (`preferredMaxLayoutWidth` in
  `layout()`); the Jira wizard shared the latent bug.
- **The address hint** under the field now says where to find the address
  (D20 wizard hint; new msgid, Czech in `po/cs.po`).
- **The Integrations row said IMAP.** `accountHeaderBadge` (Go
  `window/model.go`, Swift `FolderTree.swift`) fell through to the mail
  default for an eway account; now `crm.KindBadge` / `CRM.kindBadge` =
  "EWAY" (tests in `jira_list_test.go` and `FolderTreeTests`). Windows
  `FolderTree.AccountHeaderBadge` follows in T8.
- **No eWay button or menu items in the main window.** Two causes. The
  toolbar item looked its `CRMController` up through its view's window
  (`stack.window?.windowController`), but a hidden `NSToolbarItem` has no
  view in the window, so the button hidden at start (nothing selected
  yet) could never find the controller again and stayed hidden; it now
  gets the controller from the toolbar that made it
  (`MainToolbar.crmController`, set by both window controllers after
  `super.init`), the view's window only as a fallback. And on macOS 15 a
  hidden item is left out of `validateVisibleItems`, so `wireCRM` now
  refreshes every CRM control on the list's selection changes (single and
  several rows), on every message the cache loads and once the accounts
  are loaded, chained after the handlers installed before it
  (`Integration+CRM.swift`); a message window refreshes once its
  controller is installed.
- **A conversation's row offered Save to eWay-CRM** and would have saved
  its newest member. Owner's decision: such a row (folded or unfolded, the
  one standing for the thread) offers nothing; the user unfolds it and
  saves the members, one or several. `crm.Row.Conversation` in the Go
  reference (`OfferFor` → nothing), `CRM.Row.conversation` and
  `CRMController.conversations` in Swift; the main window and the context
  menu pass the rows with `ListRow.thread`.
- **The button sat in the Star/✦ capsule.** Owner's decision: a capsule
  of its own between Archive/Trash and Star/✦ (`.space` on both sides in
  `MainToolbar`'s item lists).
- **Which mail accounts may save (owner, 2026-10-17).** Every mail
  account, as eWay-CRM's Outlook add-in does for its manual *Save to
  eWay-CRM* (the add-in limits only *automatic tracking* to chosen
  mailboxes, with limits per data-file type: Exchange cached mode, POP3
  and shared mailboxes cannot be tracked). A per-account selection comes
  with automatic tracking (§0.2), not before; D6 stands.
- **The dialog's empty space under two groups** left the owner wondering
  what was missing. A quiet line `Deals and projects are added by
  searching above` (cs `Obchody a projekty přidáte hledáním nahoře`) now
  sits under the groups while something is listed but no deal or project
  (`Dialog.OffersSearchHint` in Go, `offersSearchHint` in Swift, the
  label in `CRMDialogController`); the empty dialog keeps its own text.
  The sheet's fixed height stays (owner's choice).

### As built (D22, 2026-10-17)

- `EwayConfig.followThreads` is a plain bool, always sent by the wizards
  (true for a new account); absent in a config = false. `CrmRecord.auto`
  is omitted when false. Migration `0022_crm_links_auto.sql` adds the
  column; 0021 untouched.
- Store: `CrmFollowedThreadTemplate` (the most recently saved member and
  the first save's time), `CrmFollowable`/`CrmUnsavedMembers` (stored
  after the first save, role inbox/sent/archive/none, not hidden, not
  saved; a copy of one message once), `CrmFollowedBy` (the hook's one
  query), `CrmFollowedThreads` (the sweep).
- Core `crm_follow.go`: the `Stored` hook (`afterMailStored` →
  `noteCrmFollow`, following accounts cached 30 s, cleared on account
  changes) only enqueues; one worker goroutine per eWay account, queue cap
  1000, serial saves through `crm.save`'s code path, dropped on pause,
  removal, switch-off or client close; a message not whole is skipped and
  logged once; a save colliding with a running one is left to the sweep;
  template items gone from the CRM are left out; `auto = 1`, then
  `notify.messagesChanged`. Sweep hourly and at start (`Maintain`):
  threads followed in the last 90 days, ≤ 50 saves per pass, cancelled
  with the client. Bulk-classified members are never saved automatically
  (newsletters forging `References` into a saved thread), pending ones
  wait for the sweep.
- Accepted risk (docs/security.md §7.3): a mail with forged
  `In-Reply-To`/`References` joins the thread locally and, unless bulk,
  is saved with the conversation; the user chose to share that thread and
  can remove the item.
- UI: the sign-in page's check box (Go `eway_flow.go`, Swift
  `EWayWizardController` + `EWayWizardWindowController`), sent on
  `account.test`/`add`/`update`; two msgids with Czech.
- Gate A/B additions: save a message, reply from Malachi, confirm the
  Sent copy appears in eWay with the same links and `auto`; answer from
  Outlook with the add-in's tracking and confirm one item; switch off,
  reply again, nothing saved.
