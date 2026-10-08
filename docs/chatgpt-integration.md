# ChatGPT / Codex integration in Malachi Mail

Status: **Experimental implementation in GTK, macOS and Windows**,
updated on **2026-10-02**, the Board paragraphs on **2026-10-08**. The original proposal was checked against
`feat/board` at `e0da0cf`. The Windows implementation is described in §11 and GTK/macOS in §12;
the cross-platform release gates remain applicable. Native macOS/Windows
builds and live account authorization remain to be verified. This is not
a public release.

## 1. Decision and scope

Add an experimental **ChatGPT (Codex)** provider alongside Claude for the
in-app assistant. Keep the existing mail daemon and local `malachi-mcp`
bridge. Use a user-installed Codex executable as a child process, with
stdio transport and a separate Malachi Mail configuration.

The preferred account connection is **Sign in with ChatGPT (SIWC)** with
ChatGPT plan usage, followed by Codex App Server configured for that grant.
Eligible Plus and Pro users can authorize open-source apps without an API
key. Paid or remotely hosted products have a separate access process;
recheck eligibility before changing the distribution model.
[OpenAI SIWC quickstart](https://developers.openai.com/siwc/quickstart).

App Server is documented for embedding Codex, but its command is still
experimental and unsupported for production workloads. Ship this provider
behind an explicit experimental opt-in until the compatibility and safety
gates below pass. Do not integrate the removed `codex mcp-server` command.
[App Server status and migration](https://learn.chatgpt.com/docs/mcp-server).

This is a local agent, not local inference: selected mail and user text go
to OpenAI. SIWC does not import ChatGPT conversations, memory or account
context. API-key billing would be a separately labeled, optional future
mode; never switch to it silently when plan usage fails.
[SIWC overview](https://developers.openai.com/siwc/token-sharing-open-source).

The initial provider covers conversation, rewriting, search conversion,
suggested replies and board triage wherever the corresponding feature is
present. External hand-off to the ChatGPT desktop app, plugins published
in ChatGPT, and registration in the user's global Codex configuration are
separate work. The existing “Register with Claude” switch must not become
a prerequisite for using ChatGPT in the app.

## 2. Current implementation and core boundaries

The current Claude integration is a useful reference, not a protocol that
Codex can consume unchanged. See [mcp.md](mcp.md), especially the in-app
panel, board triage and suggested-reply sections.

| Responsibility | Current reference | Placement for the new provider |
|---|---|---|
| Mail, board, draft rules and authoritative limits | `backend/internal/core`, `backend/pkg/api`, `backend/cmd/malachi-mcp` | Reuse the platform-neutral Go daemon and bridge |
| Prompts, allowed operations, event/state rules | `ui/internal/assistant`, `ui/internal/assistantpanel`, `ui/internal/boardtriage` | Pure Go UI packages as reference, with Swift/C# core ports |
| macOS assistant/request/board control | `macos/Sources/MalachiCore/Assistant`, `Controllers` | `MalachiCore`, without AppKit |
| Windows assistant/request control | `windows/src/Malachi.Core/Assistants`, `Controllers` | `Malachi.Core`, without WinUI/P/Invoke |
| Executable/process services | `assistantpanel/locator.go`, `process.go`; Swift/C# `Platform/ClaudeCode*` | Injected platform services, with a Codex adapter |
| Widgets and preferences | `ui/internal/window`, `compose`, `settingspanel`; `MalachiMail`; `Malachi.App` | Thin native UI over core state |

“Shared core” follows the repository's existing pattern: one semantic
design, pure Go reference logic and matching `MalachiCore` / `Malachi.Core`
implementations and fixtures. It does not mean Swift or C# imports Go UI
implementation packages. The genuinely shared executable core remains
`malachid`, built from the same Go tree on all platforms.

Keep mail/domain decisions in that daemon. A provider adapter must not
reimplement recipients, quoting, threading, sanitization, triage conflict
checks, retention or draft persistence. It invokes the existing bridge.
Provider process orchestration and presentation state follow the current
Claude client architecture. No OpenAI token is a mail-account credential;
do not put one into daemon configuration, the mail database or the daemon's
keyring-helper protocol.

No backend/API change is required by this design. The board's `source` is
already a string; `malachi-mcp` derives annotation provenance from the MCP
client's `initialize.clientInfo.name`. Replace the UI's fixed
`assistant.TriageSource` with provider metadata and test the actual Codex
MCP name; do not relabel old annotations. If implementation discovers a
missing authoritative API operation, document that separately and change
`backend/pkg/api` and [api.md](api.md) together.

On this branch all three clients have board triage/suggested replies. The
Windows tree first had the assistant panel, rewrite and search, but no
matching Board controllers; that prerequisite was met by the Windows Board
port (§11), which a Codex adapter alone would not have been.

## 3. Provider contract in the client cores

Introduce provider-neutral contracts before wiring new widgets. The table
describes the cross-platform design; implemented Windows types are listed
in §11:

| Contract | Platform-independent responsibility |
|---|---|
| `ProviderID` | Stable `claude` / `chatgpt` identifiers, distinct from target/location |
| `ProviderCapabilities` | Conversation, tools, structured results, cancellation, ephemeral history; discovered/validated availability |
| `AssistantRequestSpec` | Use case, model, language, instructions, input, schema, tool policy, deadline |
| `AssistantSession` | Start, submit turn, interrupt, close; one active turn, ordered events |
| `AssistantEvent` | Ready, text delta, tool start/result, usage, completion, failure |
| `AssistantFailure` | Missing executable, incompatible version, not connected, permission denied, limit, network, tools unavailable, protocol, stopped |
| `ProviderConnection` | Account metadata, grant state, expiry/refresh state; tokens never exposed to widgets |
| `CredentialStore`, `ProcessRunner`, `BrowserLauncher` | Injected interfaces; their OS implementations own secrets, processes and browser launch |

Normalize Claude's stream-json and Codex's JSON-RPC into the same events.
Keep wire parsing, model IDs and authentication inside provider adapters.
Display tool names from a known catalog, and preserve tool errors separately
from successful results. Count triage progress only from accepted
`annotate_case` results, as the current implementation does.

Core logic must include provider availability, consent, use-case policy,
request lifecycle, stale-response suppression, model selection, retry
classification and deadlines. Reuse the existing text-only Markdown
renderer and draft-link extraction. These rules must not be duplicated in
GTK callbacks, AppKit views or WinUI code-behind.

Pure OAuth logic also belongs in the client cores: pending-attempt state,
identity validation through a vetted OIDC implementation, scope checks,
expiry calculations, refresh serialization and account-switch rules.
Network and loopback-listener operations are injected and independently
testable. Native credential stores and browser APIs stay in platform code.
Do not implement custom cryptography or copy a token-parsing shortcut from
an unverified example.

Start with one active ChatGPT connection and one refresh coordinator per
application. Keep a connection ID in the schema so multi-account support
can be added without merging distinct grants. Every request snapshots its
provider, connection, consent version, model and tool policy. Changing the
selection cancels pending requests and prevents late events from updating
another case, draft or conversation. Never transfer a transcript to another
provider without a new explicit user action and consent.

## 4. Account connection and credential lifecycle

For a new installation, persist an opaque host identifier and begin the
documented dynamic public-client registration. Request identity and plan
usage scopes, validate state/PKCE/nonce, verify the ID token, then check the
granted scopes. Save the issued client ID, not the registration entrypoint.
A successful identity login alone does not authorize inference.
[SIWC registration](https://developers.openai.com/siwc/token-sharing-open-source/sign-in).

Implementation requirements derived from that flow:

- Generate the host ID once per installation; preserve it across restart,
  reconnection and account switches. Define reinstall/migration behavior.
- Bind each pending attempt to its chosen account, callback URI and timeout.
  Bind the listener to loopback only; validate method/path and reject stale,
  repeated or mismatched callbacks. Close it on completion/cancellation.
  Use only redirect URI forms allowed by the current official guide.
- Request `openid profile email offline_access resource.invoke
  chatgpt.tokens.use.direct`, with resource `https://api.openai.com/v1`.
- Initial registration uses `client_id=dynamic_agent_client` and the app's
  consistent `agent_name_hint`; later exchanges use the issued client ID.
  Validate issuer, audience, signature, expiry and nonce; require the
  expected verified identity when reconnecting an existing connection.
- Store metadata separately from access, refresh and retained ID tokens.
  Commit a refreshed token set atomically. Serialize refresh across all
  requests and app instances sharing the grant.
- On sign-out, stop requests, attempt documented revocation and clear local
  tokens. Report unconfirmed remote revocation without retaining a usable
  local session. Retain non-secret registration/host metadata as specified
  by the connection policy.

Refresh/revocation behavior and rotating tokens follow
[SIWC accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions).
An `invalid_grant` or revoked session requires reconnection. A network
failure may get bounded backoff; it must not trigger repeated sign-in
windows. Keep credentials out of logs, diagnostics, registry/preferences,
command-line arguments and browser storage. Never read or copy
`~/.codex/auth.json` to establish the Malachi connection.

Use OS credential storage for the token set. A protected file is only an
explicitly selected fallback, with atomic writes, Unix `0600` or a Windows
owner-restricted DACL and strict owner/path checks. No silent plaintext
fallback when the system keyring is locked or unavailable. Token storage
format must handle values larger than a single Credential Manager blob.

## 5. Codex process and protocol adapter

Start a dedicated process for each conversation or one-shot request. This
matches current isolation and avoids reusing a triage-enabled process for
rewrites or suggested replies. OpenAI documents a custom Responses provider
using an access token supplied to the child environment, with stdio between
the app and Codex and HTTP streaming upstream.
[SIWC App Server configuration](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server).

The following is a **provider-configuration sketch**, not a safe complete
launcher. Construct argument arrays directly; apply §6 before inference:

```text
codex app-server --listen stdio://
  -c model_provider="openai_chatgpt_plan"
  -c model_providers.openai_chatgpt_plan.name="ChatGPT plan"
  -c model_providers.openai_chatgpt_plan.base_url="https://api.openai.com/v1"
  -c model_providers.openai_chatgpt_plan.env_key="MALACHI_CHATGPT_ACCESS_TOKEN"
  -c model_providers.openai_chatgpt_plan.wire_api="responses"
  -c model_providers.openai_chatgpt_plan.requires_openai_auth=false
  -c model_providers.openai_chatgpt_plan.supports_websockets=false
```

Use an application-controlled, isolated Codex home and an empty private
working directory, with per-request state directories. Do not alter the
parent process's `HOME`/`CODEX_HOME` or the user's Codex installation.
The child token variable contains only the access token; refresh and ID
tokens remain in the connection service. Scrub application-level API-key
and Codex authentication variables so they cannot select a different
account or billing mode. Process diagnostics must redact the child
environment and potentially sensitive stderr.

**Implemented Windows variation:** the child receives no OpenAI token.
`CodexInferenceGate` owns the fixed upstream HTTPS connection and inserts
the renewable access token there. Codex receives a random, per-session
loopback credential and endpoint instead. This also allows token refresh
between HTTP requests without restarting or replaying a turn. The direct
provider sketch above is a protocol reference, not the Windows launcher.

Generate schemas from a tested CLI version and record the version with
fixtures. During research, the local `0.159.0-alpha.12.1` CLI exposed App
Server, `ephemeral`, instructions and output-schema fields; this is evidence
of feasibility, not the minimum supported production version. Select that
version only after tests on each supported architecture. The implemented
clients enforce no minimum: the version string is read with `--version`
(only a well-formed `codex <x.y.z>` line is accepted) and **shown in
Preferences**; it is not a gate. No bundled SDK or
Node/Python runtime is necessary for a native stdio adapter.

Implement this protocol sequence:

1. `initialize` identifies Malachi Mail with stable `clientInfo.name`,
   title and version. Wait for its result before `initialized`.
2. Start a thread with the request's instructions, model, isolated working
   directory and verified configuration. Use an in-memory/ephemeral thread
   if the tested version supports the required privacy behavior.
   *Implemented (GTK `ui/internal/chatgpt/provider.go`, mirrored in Swift
   and C#):* `thread/start` sends `approvalPolicy: "never"`,
   `sandbox: "read-only"`, `ephemeral: true` and `environments: []`
   (`turn/start` repeats `environments: []`), the isolated `cwd` and the
   instructions, and offers the Malachi tools as `dynamicTools` run by the
   host through the gateway; the child does not start the bridge. A result
   that does not confirm an ephemeral thread, no instruction sources and
   the Malachi model provider fails with `codex_isolation_unverified`.
3. Inspect MCP startup/catalog before sending mail. Treat missing required
   tools as an error; initialization alone is not bridge readiness.
4. `turn/start` carries the user's input and, for search conversion, the
   result schema. Consume deltas, tool events and the terminal turn event.
5. Only a completed turn is success. Failed/interrupted turns, JSON-RPC
   errors and unexpected EOF are distinct failures. Interrupt via the
   protocol first, then terminate the owned process tree if necessary.

The adapter needs request-ID correlation, bounded framing/queues, UTF-8
handling and response validation. Unknown notifications may be ignored;
unknown server requests must receive a bounded explicit unsupported/denied
response rather than hang. Answer approval requests only under the
immutable request policy; never auto-approve an unexpected tool.
[App Server protocol](https://learn.chatgpt.com/docs/app-server).

Before a new turn, refresh credentials if needed. The documented
environment-token setup requires restarting the child to use a renewed
token. Preserve conversation context in application memory and rebuild an
ephemeral conversation from that context if necessary; do not depend on
`thread/resume` for a thread deliberately never persisted. Validate this
path in the prototype. Never replay old tool calls to reconstruct context.

Do not automatically resubmit a failed turn that may have created a draft
or annotation. First reconcile observed tool results with daemon state;
otherwise surface the partial result and let the user retry. A fresh bridge
can create another draft even when the previous process was limited to one.

## 6. Tools, prompt injection and privacy

The existing [security.md](security.md) and MCP tiers remain authoritative.
Select tools by use case, using bridge flags as well as a Codex allow list:

| Use case | Bridge configuration | Tool policy |
|---|---|---|
| Panel conversation | `--socket <path>` | Existing panel read tools and `create_draft` |
| Rewrite / search conversion | No bridge | No mail or external tools |
| Suggested reply | `--socket <path> --reply-only <messageId>` | Existing `SuggestReplyTools`; no triage/send/modify |
| Manual triage | `--socket <path> --allow-triage --triage-run <id> --triage-max <n>` | Existing manual `TriageToolsFor` |
| Automatic triage | Same run flags | Automatic policy, currently excluding `create_draft` |

Reuse `assistant.AllowedTools`, `SuggestReplyTools` and `TriageToolsFor` as
semantic policy; adapt Claude-prefixed wire names to the bare names Codex's
MCP allow list expects. Do not make string prefix rewriting the
authorization boundary. No `--allow-send` or `--allow-modify` is permitted.
The bridge's scoped flags continue to enforce draft/triage limits.

Codex documents `mcp_servers.<id>.enabled_tools`, optional per-tool approval
policy, shell toggles and web-search controls. Configure only the bundled
bridge, with `required=true` where tools are needed.
[MCP configuration](https://learn.chatgpt.com/docs/extend/mcp),
[configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference).

Disable all built-in file, command, web, image, browser, computer-use,
connector, plugin, skill and subagent capabilities that could expose or
act on mail. Disable user/project instructions, rules and hooks; do not
infer isolation from an empty working directory or a read-only sandbox.
Check effective configuration and actual tool exposure for the selected
CLI/model. The researched documentation does not establish a single flag
equivalent to Claude's `--tools ""`: complete removal of unwanted tools is
a **prototype/release gate**, not an assumption. Fail closed if the tested
profile cannot enforce it. Managed organizational restrictions must not be
bypassed to make this integration work.

The Windows implementation enforces this at the inference transport:
it removes all advertised tools except the `malachi` dynamic-tool namespace
and validates returned tool-call items before forwarding SSE events.
One-shot rewrite/search requests advertise no tools. The app handles
dynamic calls by forwarding them to its own clean-environment MCP sibling;
Codex never starts the bridge. Unexpected server requests are denied.

The access token supplied to Codex may be inherited by its MCP child.
An MCP `env` table is not proof that inherited variables were removed.
Require a verified clean child environment. If Codex cannot guarantee that,
provide a small GPL UI-owned bridge launcher per platform which discards
the environment, installs an allow list and directly execs/spawns the
absolute bundled `malachi-mcp` path. The launcher must not interpret model
arguments, invoke a shell or copy inference tokens into the bridge. Test
descendant processes, including attachment extraction. This is a launcher
concern, not a new backend credential feature.

Preserve the existing mail-as-data instructions and nonce fences. Render
answers as untrusted text through the existing Markdown subset; links use
the native confirmation flow. Never fetch raw HTML for the model. Validate
JSON results and draft IDs before using them; a successful tool envelope
does not turn arbitrary assistant text into an authorized action.

Keep conversations and tool results out of Codex rollout files, app logs,
telemetry and diagnostic uploads. `store:false` concerns upstream response
storage, not local logging or all provider-side data policies. Verify
ephemeral behavior with disk-canary tests, then remove per-request temporary
directories after the process tree exits; clean abandoned directories on
startup. Never delete the user's ordinary Codex data.

SIWC currently requires streaming requests without stored responses and
does not support hosted Responses MCP/tool search on this route. Local
Codex MCP calls are a different mechanism. Ensure its tool-discovery mode
does not emit unsupported upstream tool types.
[Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).

## 7. Native platform integration

### 7.1 Linux / GTK

Implement pure provider/policy/protocol state under proposed
`ui/internal/aiprovider` and `ui/internal/chatgpt`; retain shared prompts in
`assistant` where appropriate. Adapt `assistantpanel` and `boardtriage`
through injected sessions. Keep GTK wiring in `window`, `compose` and
`settingspanel`; RPC/process callbacks return through `glib.IdleAdd`.

The platform adapter resolves an explicitly chosen executable first, then
known user installations and PATH. Verify regular executable files and
the tested CLI capabilities; do not require a login shell. Store account
metadata under `$XDG_CONFIG_HOME/malachi/chatgpt` and request state under
`$XDG_CACHE_HOME/malachi/assistant/chatgpt` (standard home fallbacks), with
owner-only directories. Use Secret Service for tokens and the URI portal
for browser launch. Keyring failure leaves the account disconnected.

Toolbx development is a separate environment: executable, bridge, socket
and callback must be reachable from the process actually launched. Test
native RPM/DEB or host builds independently of Toolbx.

For Flatpak, the manifest deliberately denies home access and
`org.freedesktop.Flatpak` host execution. An installed host Codex cannot be
assumed accessible. Do not add a sandbox escape or expose the user's full
home. The release needs either a reviewed in-sandbox Codex distribution
with update/licensing/architecture coverage, or a separately designed
restricted host service. Until one is verified, show this provider as
unavailable in Flatpak with a specific explanation. Verify loopback OAuth,
Secret Service, sandboxed MCP/socket access and portals in the resulting
package, not only an unsandboxed build.

### 7.2 macOS / AppKit

Port the pure contracts, protocol and state rules into proposed
`MalachiCore/AIProviders` and `ChatGPT`. Update `AssistantController`,
`AssistantPanelController`, `AssistantRequest`, `ComposeRewriteController`,
`SearchConversion`, `BoardTriageController` and the Board suggested-reply
controller. Platform services return events on `@MainActor`; widgets remain
in `MalachiMail/Assistant`, `Board` and `Preferences/AIPaneViewController`.

Discover the executable without relying on Finder's PATH. Candidate
locations may include the user's chosen native binary and verified Homebrew
paths for Apple Silicon/Intel; existence is not version compatibility.
Invoke `Process` with an argument array and raw UTF-8 pipes. Own and reap
Codex and its bridge descendants; never signal unrelated user Codex sessions.

Store metadata beneath `~/Library/Application Support/Malachi Mail/ChatGPT`
and request state in the app's caches directory. Keep tokens in separate
login Keychain items under a stable Malachi Mail service/account identity.
Use the existing signed native app identity; do not reuse mail-account
Keychain entries. Open authorization with the system browser and test the
loopback callback from a signed/notarized build. Native Keychain code
belongs outside the AppKit-free semantic core. Verify arm64 and x86_64,
launch from Finder and Terminal, application relocation, signing and any
new helper entitlement requirements. Do not weaken hardened runtime as a
workaround for launching an external executable.

### 7.3 Windows / WinUI 3

Port pure contracts and fixtures into `Malachi.Core/AIProviders` and
`ChatGPT`, then connect the existing assistant/request controllers. Put
Keyring/DPAPI, private-directory/DACL, Job Object and browser services in
`Malachi.Platform.Windows`; preserve `Malachi.Core`'s WinUI/P/Invoke-free
tests. Views and preferences stay in `Malachi.App`.

Run a native `codex.exe` with `UseShellExecute=false`, `CreateNoWindow=true`
and `ArgumentList`. Do not run npm `.cmd`/PowerShell shims or translate a
Windows request into WSL: Linux paths, socket identity and credentials
would describe a different environment. Support user-selected paths with
spaces and Unicode. The current process/locator classes provide references
for cleared environments, UTF-8 byte streams, UI synchronization and the
spawn gate; improve descendant ownership with a Job Object where possible
and test crash cleanup, rather than relying only on parent-process kill.

Store metadata under `%LOCALAPPDATA%\Malachi Mail\ChatGPT` and request state
under the app's private assistant directory. Use Credential Manager with
validated chunking for large values, or a reviewed DPAPI CurrentUser store
with restrictive DACLs. Reuse platform storage primitives, not the daemon's
mail-password helper interface. Never store tokens in the settings registry.
Open the browser with the native launcher, bind OAuth only to loopback and
use the existing native AF_UNIX socket path for `malachi-mcp.exe`.

Validate native x64 and ARM64 executable availability; report unsupported
architecture instead of guessing or downloading a substitute. Test the
published app, not just Core tests: real paths, no console flash, DACLs,
browser callback, endpoint security restrictions and process-tree cleanup.
The Windows Board parity prerequisite in §2 is met (2026-10-06): the
Board's triage and suggested reply on Windows run with the provider chosen
in *Preferences → AI*. With ChatGPT the triage uses its own model
(`board-triage-chatgpt-model`, from the provider's catalog, apart from the
panel's `assistant-chatgpt-model`) and its own consent version
(`board-triage-chatgpt-consent-version`, a separate OpenAI Board disclosure,
`ChatGptBoardText`), reports `malachi-chatgpt` as the run's source and the
session's token usage as the run's; a provider switch stops a run and turns
automatic triage off. Written and driven through UI Automation over sample
data and a devmail account by the port's agents; a real Codex run of triage
and of a suggested reply is not yet verified by the owner.

## 8. Preferences, consent and migration

Add provider selection to the in-app AI preferences. Preserve existing
Claude choices and defaults; add proposed keys consistently to GSettings,
Swift Settings and C# SettingsKey/SettingsKeyInfo:

| Proposed preference | Meaning |
|---|---|
| `assistant-provider` | In-app default, `claude` initially |
| `assistant-codex-path` | Explicit native executable, empty means discovery |
| `assistant-chatgpt-model` | Model ID for the connected ChatGPT provider |
| `assistant-chatgpt-consent-version` | Version of mail/text disclosure accepted |
| `board-triage-provider` | Explicit board provider, initially preserving Claude |
| `board-triage-chatgpt-model` | Optional triage model choice |
| `board-triage-chatgpt-consent-version` | Separate automatic/board disclosure |

Connection metadata and tokens are not preferences. Do not reinterpret
Claude's `assistant-model`, `board-triage-model`, `assistant-consent` or
`board-triage-consent`. Approval to send mail to Anthropic never approves
OpenAI. Keep the existing `assistant-target` location concept distinct from
the model provider; external Claude targets still launch Claude.

Show executable/version, connection/grant state, selected model, experimental
status and disconnect controls. Follow SIWC's prescribed sign-in label and
branding; show plan usage and a “Manage usage” action.
[SIWC UI guidelines](https://developers.openai.com/siwc/ui-ux-guidelines).
Populate models from the configured provider but treat its catalog as
availability guidance, not proof of entitlement. Never hard-code the
research machine's model set into all installations.

Use GTK gettext as the msgid reference, with Swift/C# `L10n.T/N/C` ports.
Add new Go text sources to `po/POTFILES` and regenerate catalogs. Introduce
all three client settings and common functionality in the same work.
Automatic triage remains disabled for a disconnected or unconsented
provider and stops on limits; it must not continuously retry on a timer.

## 9. Verification and delivery

Use the repository's implementation order: pure Go reference and GTK,
then immediate Swift and C# ports of the same semantics and fixtures.
Do not merge a general provider refactor that regresses existing Claude.

1. **Feasibility gate:** pin a candidate native Codex version; prove SIWC
   sign-in, inference, MCP catalog filtering, schema output, interruption,
   ephemeral disk behavior and clean child environments on each OS. Start
   with a fake daemon and synthetic messages. No real mailbox in CI.
2. **Core extraction:** introduce provider contracts and wrap current
   Claude behavior without changing its UX, prompts, billing or tool tiers.
   Port contracts/tests to all three cores.
3. **ChatGPT provider:** implement account lifecycle, native storage,
   isolated process/protocol adapter and provider-specific errors. Wire
   panel, rewrite and search on all three clients.
4. **Board integration:** route triage/suggested replies through the same
   provider factory and immutable policies. Complete/coordinate the
   Windows Board prerequisite before claiming three-client board parity.
5. **Packaging and rollout:** validate each actual package/architecture;
   release experimental only for packaging combinations that passed.
   Update `mcp.md`, `security.md`, platform READMEs and release notes to
   describe implemented behavior, tested CLI versions and known limits.

Required automated fixtures/checks:

- OAuth cancellation, wrong state/nonce/audience/identity, missing plan
  scope, rotating refresh concurrency, restart, sign-out and redaction.
- Partial/malformed/oversized JSONL, unknown events/requests, MCP errors,
  failed/interrupted turns, late events and rapid provider switches.
- Mail containing hostile instructions attempting shell, file, web,
  connector, send, delete, recipient changes and unapproved draft creation.
  Assert denied tools and absence of side effects, not just refusal text.
- Canary access token present only in the native inference gateway's
  upstream request, absent from Codex, MCP/attachment and descendant environments;
  refresh/ID tokens remain exclusively in the credential service. Tokens and
  mail text are absent from logs, rollout files, temp leftovers and diagnostic
  output after success/crash.
- Accepted triage progress, reply-only target limits, no automatic drafts,
  no duplicate writes after refresh/retry and draft retention on cancellation.
- Provider-neutral render/search/rewrite behavior and existing Claude
  regression fixtures, including selection changes while a request runs.
- Native executable discovery, argument/path handling, permissions and
  descendant cleanup for Linux, macOS and Windows.

Run Go core/UI checks in Toolbx, Swift tests/builds on macOS, and C# Core
tests plus native WinUI/platform tests on Windows. A Linux-only validation
cannot approve the macOS Keychain or Windows Job Object paths. Record real
ARM64 validation separately from cross-compilation. With a user's explicit
test consent, finally compare both providers over representative Czech and
English mail for triage accuracy, reply quality, latency and plan usage.

## 10. Cross-platform release gates

- Select a supported Codex release and generate versioned protocol fixtures;
  the researched desktop-bundled alpha is not a release dependency.
- Verify every unwanted tool and instruction source can be disabled for
  the chosen CLI/model, including managed configuration interactions.
- Confirm SIWC account eligibility and the exact callbacks/branding in
  the current official guide; availability can change after this date.
- Prove access-token refresh works with ephemeral conversations and that
  tool execution cannot duplicate mutations during recovery.
- Validate the native token-store adapters and clean-environment bridge
  launchers on deployed systems: GTK Secret Service, macOS Keychain and
  Windows Credential Manager (§11–12).
- Resolve Flatpak deployment without changing the current sandbox policy
  casually; document unavailable combinations until solved.
- Complete the Windows Board dependency and all three native release gates.

All OpenAI links above were checked on 2026-10-02. Treat configuration
sketches and proposed interfaces as Malachi Mail design choices, and
recheck the linked official specifications when porting or releasing.

## 11. Windows implementation and validation hand-off

The Windows client now implements the experimental provider for the
assistant panel, compose rewriting and natural-language search conversion.
The Windows Board (docs/windows-port.md §11.8) uses the selected provider
as well, for triage and suggested replies, which §11 below describes. The
existing Claude provider remains the default.

To use it on Windows:

1. Install a native Windows Codex executable matching the application
   architecture. A `.cmd`/PowerShell/npm shell wrapper or WSL executable is
   not accepted; Preferences offers an executable picker and official
   installation link. Malachi Mail does not install or update Codex.
2. In **Preferences → AI**, select **ChatGPT (Codex, experimental)** as the
   in-app provider and select **In App (Experimental)** as the assistant
   target. External Claude hand-off remains separate.
3. Choose **Continue with ChatGPT** and finish authorization in the system
   browser. SIWC eligibility and plan limits apply. The model dropdown uses
   the installed runtime's catalog; an empty preference selects its default.
4. Accept the OpenAI disclosure before the first mail/text request. The
   Claude consent does not authorize OpenAI. Disconnecting cancels active
   requests and clears local credentials; an unconfirmed remote revocation
   is reported. **Manage usage** opens ChatGPT's usage settings.

Implementation boundaries:

- `Malachi.Core/Assistants/IAssistantProvider` and `IAssistantSession`
  normalize runtime events for the existing panel and one-shot controllers.
  Changing the provider, model, executable or connection cancels requests;
  stale callbacks are ignored. A null provider preserves the Claude path.
- `Malachi.Core/ChatGPT` contains PKCE/identity validation, serialized token
  refresh, protocol framing, the ephemeral Codex session and inference
  gateway. `Microsoft.IdentityModel.JsonWebTokens` validates signatures and
  issuer/audience/lifetime/nonce; version 8.23.0 is pinned in central NuGet
  metadata and lockfiles. This dependency replaces hand-written JWT crypto.
- `Malachi.Platform.Windows/ChatGPT` owns the system-browser loopback
  callback and Credential Manager storage. Renewable tokens are stored as
  checksummed generation chunks with an atomic head item; non-secret
  registration metadata is stored in an owner-private directory. A shared
  file lock serializes refresh across application instances. No plaintext
  credential fallback is provided.
- The loopback gateway authenticates each request with an opaque per-session
  credential, forwards only to the fixed OpenAI Responses endpoint, disables
  redirects and strips unauthorized tools before inference. It validates
  each SSE tool envelope before forwarding it. The sibling MCP process has
  no inference credential. The child uses a separate private `CODEX_HOME`
  and working directory, disables history/telemetry and is terminated with
  its process tree; owned session directories are cleaned up on normal exit.
  An exclusive lease protects active sessions from another app instance's
  startup cleanup. Cleanup skips links and unrelated paths. Hard-crash
  descendant ownership still needs native Windows validation; the current
  implementation uses `Process.Kill(entireProcessTree: true)`, not a Job Object.
- WinUI preferences, consent and event wiring remain in `Malachi.App`.
  The provider preferences are shared with the GTK schema and Go/Swift
  settings metadata; provider msgids live in the pure Go `assistant`
  package. GTK/macOS also expose the provider (§12). The two OpenAI
  Board keys (`board-triage-chatgpt-model`,
  `board-triage-chatgpt-consent-version`) are used by the Windows Board
  (above). The daemon, mail API and bridge implementation are unchanged.

Offline tests cover signed-identity attacks, refresh rotation/concurrency,
connection cancellation, revocation failure, inference tool filtering,
executable/version validation and provider availability independent of
Claude registration. Native storage tests exercise large credentials,
atomic rotation, private paths and cross-instance locking on Windows.

### Validation recorded on 2026-10-02

The implementation was developed on Linux ARM64. `Malachi.Core`, its test
project and `Malachi.Platform.Windows`/its test project compile with no
warnings or errors using .NET SDK 10.0.301. This local SDK was invoked
outside the repository's `global.json`; supported Windows builds still
use the pinned 10.0.4xx SDK.

The **94 targeted Core tests** for authentication, provider lifecycle,
settings, executable discovery, inference policy and availability pass,
along with **2 loopback callback tests**. The repository's
27 convention checks pass, as do Go assistant/settings tests, schema and
catalog checks. The lint command ran `go vet`; `golangci-lint` is not
installed in the development container.
The reserved Swift settings and their test expectations were updated;
Swift compilation/tests require a macOS machine and were not run here.

The optional compatibility canaries also pass with the actual Linux Codex
binary **0.159.0-alpha.12.1**, against synthetic HTTP responses and a fake
MCP process. They cover a tool-free request, a namespaced mail-tool call,
and failure after creating a draft without an automatic replay. They
check credentials do not enter the MCP environment and prompt, answer
and tool-result canaries do not persist in the active profile. This proves
the tested protocol path, not live SIWC entitlement or Windows execution.
No OpenAI inference or real mailbox was used by these tests.

A broader Core regression run had **12 failures and 84 skips out of 5,426
tests**. The failures concern existing API-count expectations, GTK editor
and icon parity, Windows path/fake-Claude assumptions on Linux, and an
asynchronous notification fixture. That broader suite is not green; these
results must not be presented as complete Windows validation.

The WinUI build on Linux stops at the Windows XAML compiler's task host.
XAML source parsing passes, but it does not verify the generated controls
or the application assembly. Before release, run the full Windows build,
Core/convention/platform tests and these manual checks on Windows 11:

1. Native x64 and ARM64 Codex discovery, executable paths with spaces and
   Unicode, published-app launch and no console flash.
2. Live **Continue with ChatGPT**, cancellation, reconnect, token rotation,
   restart, disconnect/revocation and **Manage usage** for an eligible account.
3. Credential Manager chunk rotation, DACLs and concurrent app instances;
   existing mail credentials and the user's own Codex profile stay separate.
4. Panel conversation/draft card, compose rewrite/undo and search conversion;
   switching provider/model/path, closing a view and quitting cancel work.
5. Usage limits and denied tools, network interruption after draft creation,
   abnormal app termination and cleanup of owned children/profile directories.

To run the optional native protocol canaries on Windows, build the fake
bridge and explicitly select both native executables from the repository
root (the bridge uses synthetic data only):

```powershell
go build -o "$env:TEMP\malachi-codex-canary-mcp.exe" ./windows/tests/Malachi.Core.Tests/testdata/codex/malachi-mcp.go
$env:MALACHI_TEST_MCP = "$env:TEMP\malachi-codex-canary-mcp.exe"
$env:MALACHI_TEST_CODEX = "C:\path\to\native\codex.exe"
# Include Assistants.CodexPolicyTests when running the Core test project.
```

These canaries skip when their executable fixtures are absent. A skip is
not a compatibility pass. Pin and verify a supported native Codex release
before distribution; the development machine's bundled alpha is not an
application dependency.
The optional `MALACHI_TEST_CODEX` canary runs an installed CLI against a
local fake inference response with synthetic input and a fake token; it
never calls OpenAI.

Before distribution, run `windows/build.ps1 test` and `app` on Windows x64
and ARM64, then manually verify browser sign-in, model availability, panel
multi-turn/draft behavior, rewrite acceptance/undo, search conversion,
provider switching and revocation with an eligible test account. Check
process descendants and session files after normal exit and application
crash. Linux cross-compilation cannot validate WinUI, Windows Credential
Manager/DACL behavior, browser launch or actual ChatGPT plan inference.


## 12. GTK and macOS implementation and validation hand-off

GTK and macOS implement the same experimental in-app provider for the
assistant panel, compose rewriting and natural-language search. They also
connect their existing Board triage, automatic triage and suggested replies;
Windows does the same since 2026-10-06 (§11). Claude remains the default,
and external Claude hand-off remains independent. The Board parts of all
three clients are written but not verified against a real ChatGPT account
(2026-10-08).

### Setup and platform storage

In **Preferences → AI**, select **ChatGPT (Codex, experimental)** and the
**In App (Experimental)** target, choose a native Codex executable if it is
not found automatically, then select **Continue with ChatGPT**. Complete
SIWC in the system browser. Accept the OpenAI disclosure before the first
request. The installed runtime supplies the model catalog; an empty model
setting keeps the runtime default and does not promise plan entitlement.

- GTK accepts native ELF executables for the running architecture, including
  the separately installed desktop runtime when found. Shell/npm wrappers
  are refused. Renewable grants live in the user's Secret Service, with
  distinct application/purpose attributes. Non-secret registration metadata
  and the refresh lock live under `$XDG_CONFIG_HOME/malachi/chatgpt/` (usually
  `~/.config/malachi/chatgpt/`). A missing or locked keyring produces an error;
  there is no plaintext credential fallback. Browser launching is marshalled
  onto the GTK main loop.
- The current Flatpak build displays ChatGPT as unavailable. It neither
  escapes the sandbox nor adds host-execution permissions. Use the native
  GTK build for this integration until a separate packaging design is
  validated.
- macOS accepts native Mach-O/universal executables and uses an application
  Keychain item independent of mail credentials. The browser callback and
  inference gateway use loopback-only Network.framework listeners; process
  groups are established before execution using `posix_spawn`. The native
  Keychain/browser/RSA signature adapters live in `MalachiMail`, while the
  provider, OAuth coordination, tool policy and controllers live in
  `MalachiCore`.

### Implementation boundaries and consent

`ui/internal/chatgpt` owns the GTK OAuth coordinator, native storage,
loopback gateway, Codex protocol client, executable discovery and temporary
profiles. `ui/internal/assistantpanel` exposes provider/session interfaces
and normalizes events for the existing controllers. GTK widgets stay in
`ui/internal/window`, compose and Blueprint. The new D-Bus dependency is
`github.com/godbus/dbus/v5 v5.1.0`, already used by the backend; it is used
only by the UI Secret Service adapter. Backend sources and the API remain
unchanged.

The macOS equivalents are `MalachiCore/ChatGPT` and `MalachiMail/ChatGPT`.
Both implementations retain the established Claude execution path when
Claude is selected. Selecting another provider/model/executable, replacing
a connection, disconnecting or withdrawing permission cancels affected
requests and rejects late callbacks. OAuth authorization, refresh and
credential rotation are serialized, including a lock between application
instances. HTTP 401 invalidates the current rejected token, while an old
401 cannot delete a newer token. Usage/permission failures do not authorize
silent replay of a mail mutation.

OpenAI consent keys are distinct from Claude consent:

| Setting | Purpose |
| --- | --- |
| `assistant-provider` | `claude` (default) or `chatgpt` |
| `assistant-codex-path` | Explicit native runtime path, empty for discovery |
| `assistant-chatgpt-model` | Foreground panel/rewrite/search model |
| `assistant-chatgpt-consent-version` | Versioned foreground OpenAI disclosure; current version 1 |
| `board-triage-chatgpt-model` | Board triage model; empty uses the runtime default independently of the panel model |
| `board-triage-chatgpt-consent-version` | Versioned Board OpenAI disclosure; current version 1 |

Board consent does not authorize foreground text requests; foreground consent
does not authorize background mail transfer. Changing the provider disables
automatic triage, requiring the user to enable it for the selected provider.
The existing interval, daily cap and Board preference controller remain in
use. The same exact tool tiers are enforced before inference and before
executing a tool: no tools for rewriting/search, read/draft tools for the
panel, reply-only tools scoped to the selected case, and triage tools scoped
to the current run. Automatic triage excludes reply creation. The existing
MCP bridge reports the source as `malachi-chatgpt`; no new daemon method is
needed. Actual validated token usage is reported to the Board's existing
usage accounting.

### Failures of a Codex run (2026-10-08)

The provider's reason codes map to the same failure classes as Claude's, in
the panel, the one-shot requests and the Board runs: `codex_not_found`
becomes *not found*; `chatgpt_not_connected`, `reconnect_required`,
`consent_required`, `permission_denied` and `identity_mismatch` become *not
signed in*; `chatgpt_usage_limit` becomes a new *limit* class ("the
assistant's usage limit was reached"; a Board run ends as failed with the
limit text and its automatic schedule backs off one step); anything else is
*stopped*. Where Claude Code would offer *Sign In…*, the panel offers
**Reconnect to ChatGPT** (runs the connection flow, then sends the last
question again). The failure of
the last turn is forgotten when a new turn is submitted. The gateway marks
a completed response's usage as final, so a Board run's token count is exact
unless the run was stopped, in which case it is a lower bound ("at least").
Changing the provider cancels a run and writes the Board preferences once
(automatic triage off, the Board's `assistant` preference repaired when the
selected provider's Board consent is missing); a change of a setting that
concerns only the inactive provider cancels nothing. The reason mapping and the
panel controller are tested in Go; the Reconnect offer is written in all
three clients (macOS and Windows still being finished when this was
written), not verified on any of them (2026-10-08).

### Validation performed on Linux ARM64

- GTK builds, the complete UI Go test suite and `make lint` pass. Lint uses
  `go vet` because `golangci-lint` is unavailable on this machine.
- OAuth tests exercise signed identity attacks, PKCE/state/nonce, refresh
  rotation and concurrency, late cancellation, stale-token rejection and
  disconnect/revocation failures. Gateway tests cover filtering, denied
  tools, SSE validation and credential separation; temporary-profile tests
  cover ownership, private permissions, symlinks and active leases.
- A live synthetic prompt without mail confirmed an upstream HTTP 200
  SSE body with the `Content-Type` header absent, both with and without
  `Accept: text/event-stream`. Gateways now send that Accept header and
  compare explicit SSE media types without case sensitivity, allowing
  parameters. An absent media type requires a complete JSON SSE data frame
  whose type starts with `response.` and whose output passes the existing
  tool policy before sending local HTTP 200 or any upstream bytes. Bounded
  blank/comment heartbeat frames are ignored during this check. Explicit
  non-SSE media types, HTML, malformed frames and early EOF produce a fixed
  sanitized HTTP 502. No JSON response fallback or broader tool authority
  is introduced. A native Codex end-to-end run through the corrected GTK
  gateway and a connected ChatGPT account completed a synthetic no-tool
  `Reply exactly OK` request with `gpt-6.1-sol` (3.29 seconds). No mail,
  response content or tokens were printed. A second live run completed one
  `list_accounts` call against the synthetic MCP fixture and reached
  `response.completed` (6.23 seconds). It used no daemon or real mailbox;
  actual mail reading through MCP remains unverified.
- Codex ResponsesLite `input` items of type `additional_tools` are validated,
  removed from history and merged with `tools` before the same strict
  Malachi namespace filtering. Built-in tools and other namespaces are
  discarded; missing or duplicate required tools still fail closed. Codex
  is configured with `features.code_mode.direct_only_tool_namespaces=["malachi"]`
  so code-mode models retain direct Malachi calls rather than wrapping them
  in `functions.exec`; the gateway does not permit that execution tool.
- Optional GTK native canaries ran against Codex
  `0.159.0-alpha.12.1` with synthetic local inference and a fake MCP bridge:
  fifteen cases: `gpt-6.1`, `gpt-6.1-sol`, `gpt-6-sol`, `gpt-5.6-sol`
  and the provider default, each with text, an allowed mail tool and failure
  after draft creation. All pass, including the focused gateway race run.
  They perform
  no real account authorization or OpenAI inference. They verify that the
  MCP child does not receive the inference credential, failed mutations are
  not replayed, and canary mail/tokens are absent from temporary profile
  files. Focused runtime/controller race tests and vet also pass.
- GTK model-cache notifications now update the dropdown on idle and coalesce
  repeated updates, preventing reentry while GTK is handling a selection.
  `TestPreferencesModelGTKSmoke` reproduced a SIGSEGV with the synchronous
  path; the corrected widget test passes three repeated runs.
- Extending the race run to GTK widget tests exposes an existing
  `checkptr` failure in the pinned gotk4/weak binding. The complete normal
  GTK suite passes; this is not evidence that GTK widget tests passed under
  race instrumentation. A parallel race run also hit the older Claude
  overlong-line fixture timeout; provider/controller race checks are run
  separately from GTK widgets.
- macOS localization tooling tests and string catalog checks run on Linux.
  Swift tests and the AppKit build cannot run here: Swift and Apple SDKs are
  unavailable. Added Swift offline tests must be run with `make test-macos`
  and the client built with `make macos` on a Mac. No live Keychain/SIWC or
  native Codex canary result is claimed for macOS.
- Windows Core builds without warnings. The expanded `CodexPolicyTests`
  pass all 35 cases with no skips, including missing-MIME first-frame
  loopback regressions and synthetic native Codex text
  and mail-tool canaries for `gpt-6.1`, `gpt-6-sol`, `gpt-5.6-sol` and the
  provider default, plus failure after draft creation for `gpt-6.1`.
  The provider controller suite also passes all 15 cases, including the
  selected-provider subtitle before the first question. These run on Linux
  ARM64 and do not establish native Windows UI behavior
  or real user-account inference. The native Windows limitations in §11
  still apply; macOS and Windows UI validation remains outstanding.

To repeat the optional GTK canaries from the repository root in the project
Toolbx, build the shared synthetic MCP fixture and select the native runtime:

```sh
go build -o /tmp/malachi-codex-canary-mcp ./windows/tests/Malachi.Core.Tests/testdata/codex/malachi-mcp.go
cd ui
MALACHI_TEST_CODEX=/absolute/path/to/codex MALACHI_TEST_MCP=/tmp/malachi-codex-canary-mcp go test -race -count=1 ./internal/chatgpt ./internal/assistantpanel
```

Missing native executables/fixtures cause skips; a skipped canary is not a
compatibility result.

Before releasing, run real sign-in, cancellation, refresh, disconnect,
keyring failure and reconnect tests on each native platform. Verify panel,
rewrite, own-words search and, on GTK/macOS, Board manual/automatic triage
and suggested replies against controlled test mail. Check that changing the
provider stops active work and requires the appropriate OpenAI consent.
Confirm no credential or mail text appears in logs, profiles or crash
artifacts. Normal termination owns child process groups and removes leased
profiles; hard-crash descendants require native validation on each platform.
The locally available desktop alpha is a tested protocol path, not a stable
release dependency or proof of live SIWC eligibility.
