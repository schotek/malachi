# Publishing the Windows client in the Microsoft Store

Status: research and plan, 2026-11-04. Nothing in this document is
implemented: no code, workflow or manifest was changed for it. It
continues the MSIX trial of 2026-11-04
(`.github/workflows/windows-msix.yml`, `windows/msix/build-msix.ps1`,
`windows/msix/AppxManifest.xml`, [windows-port.md §17](../windows-port.md#17-before-a-public-release)).

## 0. Why the Store, and what it does not change

- The owner wants the Windows client **free**. Paid features come later
  (advanced sync, server-side protection) through Malachi Mail's own
  payment system for every platform, **never through Store payments**.
  The Store listing is therefore *Free*, without add-ons or in-app
  purchases.
- The Store **re-signs an MSIX package for free**; an EXE or MSI it lists
  has to be signed by the publisher. Code signing is open
  ([windows-port.md §17](../windows-port.md#17-before-a-public-release)):
  Azure Artifact Signing takes individuals only in the USA and Canada, the
  owner is in the Czech Republic. The Store is so far the only route to a
  signed Windows client without a certificate.
- The Inno Setup installer of the release (`docs/releasing.md` §7) stays.
  The Store is a second channel, not a replacement, at least until the
  Store package is proven.

### How to read the sources

`learn.microsoft.com` is blocked by this session's network policy, so no
Microsoft page was read directly. Everything marked **[search]** comes
from web search summaries of Microsoft Learn, Microsoft blogs, Microsoft
Q&A and third-party reports; **[forum]** is a developer report, not
policy; **[repo]** is checked in this repository; **[unverified]** is the
author's understanding that no source in this session confirmed. Before
acting on a [search] item, the owner (or an agent with access to
learn.microsoft.com) should open the cited page.

## 1. Account, name and package identity

### 1.1 Developer account

- Since 2025-09-10, registration as an **individual** developer is free
  in over 200 markets, without a credit card; it needs a **personal
  Microsoft account** and identity verification by a photo of a
  government ID and a selfie, best done on a phone
  ([Windows blog, 2025-09-10](https://blogs.windows.com/windowsdeveloper/2025/09/10/free-developer-registration-for-individual-developers-on-microsoft-store/),
  [Learn: what's new for individual developers](https://learn.microsoft.com/en-my/windows/apps/publish/whats-new-individual-developer),
  [Learn: opening a developer account](https://learn.microsoft.com/windows/apps/publish/partner-center/opening-a-developer-account))
  **[search]**. The entry point is storedeveloper.microsoft.com.
- A Microsoft post of May 2026 reports company accounts free as well
  ([Neowin](https://www.neowin.net/news/microsoft-makes-company-developer-accounts-free-for-the-microsoft-store/))
  **[search]**. An individual account shows the owner's own name as the
  publisher; a company account needs a registered company (and its
  verification). For now: **individual**.
- Whether an individual account can later be converted into a company
  account, keeping the product and its identity, is **[unverified]**.
  If Malachi Mail is to be sold by a company later, the owner should
  find out before the first submission, because the package's Publisher
  is tied to the account (§1.3).

### 1.2 Reserving the name

In Partner Center (*Apps and games → New product → MSIX or PWA app*) the
owner reserves the product name **Malachi Mail** (the user-facing name
of `CLAUDE.md`). A reservation is held for a limited time before a
submission has to follow **[unverified: three months is the commonly
quoted period]**. Whether the name is still free is **[unverified]**; if
it is taken, the owner decides the alternative (never shorten or rename
anything in code for it).

### 1.3 The identity Partner Center assigns

After the reservation, *Product management → Product identity* shows the
three values the package must carry exactly **[unverified: from common
practice; not re-read at the source]**:

| Manifest | Partner Center | Example form | Today in the trial |
|---|---|---|---|
| `Identity/@Name` | Package/Identity/Name | `12345VladislavJanecek.MalachiMail` | `io.github.schotek.Malachi` |
| `Identity/@Publisher` | Package/Identity/Publisher | `CN=<GUID>` | `CN=Malachi Mail Test` |
| `Properties/PublisherDisplayName` | Package/Properties/PublisherDisplayName | `Vladislav Janeček` | `Malachi Mail (test)` |

Notes:

- The **package identity name is not the app ID**. `io.github.schotek.Malachi`
  stays everywhere it is today (`CLAUDE.md`: never shortened or changed):
  the HKCU settings key, the gschema keys, the AUMID of the unpackaged
  notifications, the macOS bundle ids. The package name is a Store
  artefact; its *package family name* (name + publisher hash) is what
  Windows uses for the packaged app's AUMID
  (`<PackageFamilyName>!MalachiMail`). Any code that compares AUMIDs or
  looks itself up by name has to accept both. **[repo]** a grep for
  `PackageFamilyName`/`GetCurrentPackage` finds only
  `ClaudeDesktopApp.cs` (Claude's own package), so the app does not look
  itself up today.
- The workflow takes `identity` and `publisher` as inputs and passes them
  to `build-msix.ps1` (`-IdentityName`, `-Publisher`) **[repo]**. The
  script also has `-PublisherDisplayName`, but **the workflow has no input
  for it**, so a Store package would carry `Malachi Mail (test)`, which
  does not match Partner Center and the upload is refused **[repo; the
  refusal is unverified]**. Fix when implementing: a `publisher_name`
  input, or, better, the three values as repository *variables* (not
  secrets: they are public in every package) read by a Store job.
- The values never change once a product is published; a different
  Publisher means a different product.

## 2. The package

### 2.1 Version

The rules **[search]**
([Learn: package version numbering](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/package-version-numbering),
[Microsoft Q&A](https://learn.microsoft.com/en-us/answers/questions/144791/problem-trying-to-submit-a-package-to-the-store-pa),
[electron-builder #2979](https://github.com/electron-userland/electron-builder/issues/2979)):

- Four parts, each 0–65535; the **fourth part is reserved for the Store
  and must be 0** in the package.
- **The first part cannot be 0.** This is the finding that matters:
  Malachi Mail is `0.2.0` today, and `build-msix.ps1` maps it to
  `0.2.0.<commits>` **[repo]**, which the Store refuses twice (major 0,
  revision ≠ 0 except on a tag).
- Every submission's version must be higher than the published one for
  the same architecture; x64 and arm64 may share a version.

Options for the owner (a decision; the mapping is permanent once a
package is published, because versions may only grow):

| Option | 0.2.0 becomes | Later 1.0.0 becomes | Cost |
|---|---|---|---|
| A. First Store release only from 1.0.0 | — | `1.0.0.0` | Store waits for 1.0 |
| B. Major + 1, forever | `1.2.0.0` | `2.0.0.0` | Store version ≠ app version; About and `system.info` keep the real one |
| C. `1.<minor>.<patch>.0` while major is 0, then real | `1.2.0.0` | collides with 0.0.x mapping unless 1.0.0 is skipped to ≥ 1.<last minor+1> | fragile |

Recommendation: **A** if 1.0 is near, otherwise **B** (simple and
monotonic; the Store shows its version on the listing page, which then
differs from the app's About, a known oddity). Either way the Store job
builds only from a clean `vX.Y.Z` tag, so the fourth part is 0, and the
`git describe` commit count stays a feature of test packages.

### 2.2 Architectures and the bundle

- One package per architecture with identical identity except
  `ProcessorArchitecture`, combined with `makeappx bundle` into a
  **`.msixbundle`** (or a `.msixupload` around it), uploaded as one
  package. Once a bundle is published, later submissions must keep a
  bundle **[search]**
  ([Learn: app package requirements](https://learn.microsoft.com/windows/apps/publish/publish-your-app/msix/app-package-requirements),
  [Q&A: one package per architecture](https://learn.microsoft.com/en-sg/answers/questions/73656/publish-1-package-per-architecture)).
- Today the workflow builds one architecture per run **[repo]**. A Store
  job would build both app folders (the ARM64 one cross-built, as
  `windows.yml` does), pack two `.msix`, bundle them, and upload the
  unsigned bundle as the artifact.
- ARM64 still has not run on real ARM64 hardware as a package
  ([windows-port.md §17](../windows-port.md#17-before-a-public-release));
  the bundle can start with x64 only, but then it must stay a bundle when
  ARM64 joins.

### 2.3 Logos, `resources.pri` and qualifiers

Today `build-msix.ps1` renders three PNGs without qualifiers
(`StoreLogo` 50, `Square44x44Logo` 44, `Square150x150Logo` 150) and does
**not** generate a `resources.pri` **[repo]**. For the Store:

- **Package logos** with scale variants (`.scale-100/125/150/200/400`) and
  for `Square44x44Logo` also `targetsize-16/24/32/48/256` and their
  `_altform-unplated` variants (taskbar, Start, Settings → Apps), plus
  `Wide310x150Logo` and `Square310x310Logo` if the tile sizes are
  declared. Without them Windows scales the one bitmap and WACK warns
  **[unverified: from the MSIX asset guidance, not re-read]**.
- Qualified file names only work through a **package resource index**:
  `makepri new` over the stage (with a `priconfig.xml`), producing
  `resources.pri` at the package root **[search, secondary]**.
- **WinUI's own resources**: the app folder carries `MalachiMail.pri`
  (MRT Core resources of the WinUI app). Unpackaged, MRT Core reads
  `<exe>.pri`; packaged, it reads the package's `resources.pri`
  **[unverified]**. This is the first item of the §17 trial ("MRT Core and
  `MalachiMail.pri`"): if the packaged app cannot find its XAML
  resources, the fix is to merge `MalachiMail.pri` into the generated
  `resources.pri` (`makepri new` with a resource pack of the existing PRI,
  or building the app with `WindowsPackageType=MSIX`, which makes the SDK
  generate one PRI). To test before anything else.
- **Store listing images** are separate (§4.4).

### 2.4 Target versions

`<TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.22000.0"
MaxVersionTested="10.0.26100.0" />` **[repo]**. MinVersion 22000 limits
the listing to Windows 11, as the client is today (windows/README.md).
`MaxVersionTested` should follow the newest Windows the owner tests on
(Windows 11 26100 x64 and 26300 ARM64 per `CLAUDE.md` and §17): raising
it to the tested build is cheap and avoids compatibility shims
**[unverified: that the Store warns about an old MaxVersionTested]**.

### 2.5 Windows App Certification Kit

The Store runs its own certification, which includes WACK-like checks;
running WACK locally first saves round trips. On a Windows machine with
the Windows SDK: `appcert.exe reset`, then `appcert.exe test
-appxpackagepath <bundle> -reportoutputpath report.xml` **[unverified:
command line from memory]**. Checks likely to flag Malachi Mail: the
binary analyzer on the Go binaries (ASLR and DEP flags; Go sets them by
default on Windows **[unverified]**), blocked executables (the app starts
`malachid.exe`, `malachi-credentials.exe` and `malachi-mcp.exe` from its
folder, which is allowed for full-trust apps **[unverified]**), and the
default-image and scale checks of §2.3. CI cannot run WACK (it needs an
interactive session **[unverified]**), so it is a step for the Windows
agent or the owner.

## 3. Restricted capabilities

### 3.1 `runFullTrust`

Required for every packaged Win32 desktop app (`EntryPoint=
"Windows.FullTrustApplication"`). Partner Center detects it and asks for
a short justification; it is routinely granted for desktop apps, and a
note that the app is a packaged desktop (Win32/WinUI 3) app is usually
enough **[search, forum]**
([AppConsult blog](https://learn.microsoft.com/en-us/archive/blogs/appconsult/a-simpler-and-faster-way-to-publish-your-desktop-bridge-applications-on-the-microsoft-store),
[StackOverflow archive](https://kiwix.lema.org/stackoverflow.com_en_all_2023-11/questions/51684258/windows-store-doesn-t-let-to-submit-app-with-runfulltrust)).
Risk: low.

Text for the submission notes (draft):

> Malachi Mail is a WinUI 3 desktop mail client packaged as MSIX. It
> needs runFullTrust because it runs as a desktop app and starts its own
> background mail engine (malachid.exe) and credential helper from the
> package, which talk over a local socket.

### 3.2 `unvirtualizedResources`

Rarely granted. Microsoft staff in 2020 said it can be requested during
submission or beforehand through developer support (*Dashboard issue →
App submissions → Other*), with a concrete explanation, taking five
business days or more; incomplete requests are denied. Advanced
Installer writes that a Store submission with it will "chances are" be
rejected; Python's Store package is quoted as a precedent **[forum,
search]**
([Tech Community thread](https://techcommunity.microsoft.com/discussions/msix-discussions/write-to-non-virtualized-appdata-location-from-msix-installed-binary/1512850/replies/1514789),
[Advanced Installer](https://www.advancedinstaller.com/msix-disable-registry-file-redirection.html)).
Risk: **high**, and it delays the first submission.

Why the trial declares it ([windows-port.md](../windows-port.md),
Packaging): the unpackaged app keeps `config.toml`, `store.db` and the
WebView2 data in `%LOCALAPPDATA%\Malachi Mail\` and its settings in HKCU,
and MSIX virtualisation would hide them from anything outside the
package, notably the Claude registration.

### 3.3 The alternative: keep virtualisation, declare what Windows must see

What virtualisation actually changes, as this author understands
Microsoft's "how packaged desktop apps run" page **[unverified: page not
reachable here, to be re-read]**:

- Writes to **new** files under the user's AppData go to the package's
  private location (`%LOCALAPPDATA%\Packages\<family>\LocalCache\…`);
  **existing** files outside are modified in place; reads see both.
- HKCU writes go to a private per-package hive; reads see the real HKCU
  merged.
- Nothing outside AppData is virtualised.

Checked against Malachi Mail **[repo, except where noted]**:

| What | Where | Under virtualisation |
|---|---|---|
| Socket and its key | `%USERPROFILE%\.cache\malachi\run\` | outside AppData: real, visible to `malachi-mcp` everywhere (chosen so on purpose, windows-port.md "RPC socket") |
| Mail data, `config.toml`, WebView2 data | `%LOCALAPPDATA%\Malachi Mail\` | the app and its daemon (same package) see one private copy; an existing installation's data is still read **[unverified]**, new files go private; uninstalling the package deletes the private copy |
| Settings | `HKCU\Software\io.github.schotek.Malachi` | private hive: works for the app, gone on uninstall |
| `mailto:` registration, Run value, notification COM class | HKCU `Classes`, `Run` | **invisible to Windows** → must be manifest declarations |
| Claude Code registration (`~/.claude.json`) | profile root | real |
| Claude Desktop registration | `%APPDATA%\Claude\…` (unpackaged Claude) or Claude's own package folder (MSIX Claude) | an existing file is modified in place **[unverified]**; a new one would land in our private copy and Claude would not see it. To test with both Claude Desktops |
| Stored passwords | Credential Manager | real |

So a Store package without `unvirtualizedResources` is plausible if:

1. **`windows.protocol`** `mailto` (`uap:Extension Category="windows.protocol"`)
   replaces the app's own HKCU registration, and the app then skips its
   own registration when it runs packaged (Settings → Default apps still
   needs the user's choice).
2. **`windows.startupTask`** (`desktop:Extension`, with a `TaskId`)
   replaces the Run value; the app's "start at sign-in" switch then uses
   `StartupTask.GetAsync/RequestEnableAsync` when packaged. The user can
   turn it off in Task Manager; the app reads the state rather than
   assuming it.
3. **Notifications** are already declared (activator CLSID and COM
   server) **[repo]**; the unpackaged registration code must not run in
   the package.
4. **`malachi-mcp`** gets an execution alias (§3.4).
5. The data path stays the same; whether the app should instead use
   `ApplicationData.Current.LocalFolder` is not needed, because the
   virtualised path already lands there.

Each of these is a code path "when packaged", which is Windows client
code in `windows/` (allowed by rule 4: platform code belongs to its
client's tree); the daemon does not change.

**Recommendation:** first try the trial package **without**
`unvirtualizedResources` and with the declarations above on a Windows
machine. If the Claude Desktop registration is the only thing that fails,
prefer fixing that in the registration (`malachi-mcp install` writing
through the alias, or telling the user to restart Claude) over a
capability that may block the listing. Request `unvirtualizedResources`
only if a must-have breaks, and then before the first submission through
developer support, with this justification (draft):

> Malachi Mail runs a local mail engine (malachid.exe) that other desktop
> programs reach: AI assistants such as Claude Desktop and Claude Code
> start the app's MCP bridge (malachi-mcp.exe), which must read the same
> configuration and the user's existing mail store that the app writes.
> With AppData virtualisation those files are private to the package and
> the bridge started by another app cannot find them, and users moving
> from the installer version would lose access to their existing store.

### 3.4 `malachi-mcp` inside a package

- The install folder (`C:\Program Files\WindowsApps\<name>_<version>_…`)
  **changes with every update** **[search]**
  ([Tech Community](https://techcommunity.microsoft.com/discussions/msix-discussions/msix-update-always-installs-in-different-location-forcing-the-firewall-for-new-r/3676462)).
  `malachi-mcp install` writes the bridge's absolute path into Claude's
  configuration (Preferences → AI → MCP, `McpRegistrationController`
  passing `--command`) **[repo, windows-port.md]**, so each Store update
  would break the registration.
- Fix: a **`uap3:AppExecutionAlias`** `malachi-mcp.exe` for the bridge
  (windows-port.md §17 already lists it as a later track). Windows puts a
  stub in `%LOCALAPPDATA%\Microsoft\WindowsApps\`, a stable path; the
  registration then passes that path (or just `malachi-mcp.exe` if that
  folder is on `PATH`) as `--command` **[search]**
  ([Learn: shortcut via alias](https://learn.microsoft.com/en-us/windows/msix/psf/create-shortcut-with-script-package-support-framework)).
  Alias registration reportedly fails for names with spaces; the
  executable names have none **[forum]**.
- Whether Claude Desktop (itself MSIX) can start an alias of another
  package is **[unverified]**: the alias stub runs the target with *our*
  package identity, so the bridge would see our virtualised view. That
  is a reason the bridge reads the socket and key outside AppData.
- `.mcp.json` in the repository points at `build/malachi-mcp` (developer
  setup); unaffected.

## 4. Store policies and the listing

### 4.1 Privacy policy (required)

- Partner Center asks whether the app accesses, collects or transmits
  personal information, sets the answer to *yes* itself when the
  capabilities imply it, and then requires a **privacy policy URL**;
  policy 10.5.1 requires a privacy policy for packaged desktop apps
  ("Desktop Bridge products") in any case **[search]**
  ([Learn: support info](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/support-info),
  [policy change history](https://learn.microsoft.com/windows/apps/publish/store-policies-change-history)).
- **[repo]** `web/` has no privacy page yet; `docs/plans/feedback.md` §7
  step 1 already plans `web/privacy.html` in cs and en. That page is a
  prerequisite of the Store submission too. It should say, for the Store
  build: mail, contacts and calendars stay on the device and go only to
  the user's own servers (IMAP/SMTP, Microsoft Graph, Google, CalDAV/
  CardDAV, Jira, eWay-CRM); sign-in with Microsoft or Google goes through
  their sites; no telemetry; the Store build makes no update check (§5);
  the AI assistant runs the user's own Claude Code or Codex, which send
  the chosen mail to Anthropic or OpenAI under the user's account with
  them, only after consent; remote images only on request; the feedback
  form when it exists. The owner writes or approves the text (it is a
  legal statement).

### 4.2 Age rating (IARC)

A questionnaire in the submission, answered once and again only when the
answers change **[search]**
([Felix Wong, 2025](https://felixwong.com/2025/03/developers-how-to-update-age-ratings-for-your-microsoft-app/),
[Windows blog 2016](https://blogs.windows.com/buildingapps/2016/01/06/now-available-single-age-rating-system-to-simplify-app-submissions)).
Answers for an email client that matter: users communicate with each
other and see unfiltered content from the internet (yes), shares
location (no), digital purchases (no), and any question on generative AI
content (the assistant), which the owner answers as it stands at
submission. Expect a rating like "Users Interact" / 12+ or similar
**[unverified]**. The owner fills it in personally.

### 4.3 Description, category, languages

- Category: **Productivity** **[unverified: the subcategory list]**.
- Listing languages: **English (en-us)** and **Czech (cs-cz)**, matching
  the manifest's `<Resources>` **[repo]**. Partner Center expects a
  listing for each language the package declares **[unverified]**. Texts
  can start from `web/` and the AppStream metainfo; the owner approves
  them.
- The description should name the Corresponding Source (the GitHub
  repository and the tag), which also serves GPLv3 §6(d) (§4.5).
- Fields that may stay empty: trailers, search terms (up to seven,
  optional), "What's new" (fill from `NEWS`).
- Support info: website `https://malachimail.app` (must be the
  publisher's own site, not the Store listing **[search]**), support
  contact (the report link `https://malachimail.app/#report` or an
  address the owner chooses), privacy URL (§4.1).

### 4.4 Screenshots and images

- At least one screenshot is required, four or more recommended; PNG, at
  least **1366 × 768** for desktop, up to 3840 × 2160, ≤ 50 MB **[search,
  older page]**
  ([Learn: screenshots and images](https://learn.microsoft.com/en-us/windows/uwp/publish/app-screenshots-and-images),
  [submissions FAQ](https://learn.microsoft.com/windows/apps/publish/faq/submit-your-app)).
- A 1:1 store logo of **300 × 300** PNG **[search]**; optional poster art
  and hero image.
- Per language: Czech screenshots of the Czech UI.
- **Never real mail.** Screenshots come from an isolated instance with
  sample data (`MALACHI_BOARD_SAMPLES=1`, `MALACHI_CALENDAR_SAMPLES=1`,
  `MALACHI_CONTACTS_SAMPLES=1`, `MALACHI_RULES_SAMPLES=1`) or the
  `malachi-devmail` accounts, with `MALACHI_DATA_DIR`, a short
  `MALACHI_SOCKET` and `MALACHI_SETTINGS_KEY` as `CLAUDE.md` prescribes
  for agents on Windows. The UI smoke harness (`Malachi.App.UiTests`)
  could take them at a fixed window size; a Windows agent can make
  drafts, the owner chooses.

### 4.5 Licence

- The Store's **standard application licence terms** forbid copying and
  redistribution, which are further restrictions that GPLv3 §10 forbids.
  The Store agreement lets a publisher supply its own licence terms, and
  FOSS terms may conflict with the standard terms' limitations as far as
  the open source licence requires **[search; old agreement text]**
  ([PCWorld](https://www.pcworld.com/article/472689/windows_store_makes_a_special_allowance_for_open_source.html),
  [J-B Kempf on the Windows Store and the GPL](https://jbkempf.com/blog/Windows-Store-and-the-GPL),
  [QGIS developers, 2026-08](https://lists.osgeo.org/pipermail/qgis-developer/2026-August/068497.html)).
  In Partner Center: *Properties* / the listing's **license terms** field
  with the owner's terms (the GPL-3.0-or-later text or a link to
  `LICENSE`, plus `LICENSING.md`) instead of the standard terms. The
  exact field name and whether a URL suffices is **[unverified]**; the
  owner checks the current Microsoft Store App Developer Agreement before
  submitting.
- The **GPLv3 §7 additional permission** in `LICENSING.md` (2026-10-28)
  covers conveying the client with the Windows App SDK, WebView2 SDK and
  VC++ runtime **[repo]**; the MSIX contains the same app folder, so it
  is covered. The `Microsoft-notices\` folder and `THIRD-PARTY-NOTICES.md`
  are inside the app folder and therefore in the package **[repo, for the
  installer; for the stage, copied unchanged]**.
- Store re-signing does not restrict the user's rights: the source and
  the unsigned build path stay public. The **Corresponding Source** is
  the tag on GitHub; the listing links it.
- Whether the owner's dual licensing of `backend/` (AGPL + commercial,
  `LICENSING.md`) needs any mention in the listing: no, the package is
  distributed under AGPL/GPL **[author's view]**.

### 4.6 Other policies to keep in mind

- **Self-update**: a Store app should not install a different version of
  itself outside the Store **[search, unclear wording]**
  ([Q&A](https://learn.microsoft.com/en-us/answers/a/12618642)); §5 keeps
  the daemon's update offer out of the Store build.
- **Executables**: starting the bundled daemon and helpers is normal for
  full-trust apps; never downloading and running code (the app never
  does).
- **Personal data and AI**: the assistant hand-off and in-app panel run
  the user's own Claude Code/Codex; disclose it in the privacy policy and
  the description (§4.1).

## 5. Updates and the daemon's update check

**[repo]** The channel comes from the build: `build.ps1` sets
`-X main.updateChannel=windows-setup-<arch>` **only for the `installer`
target**; `build.ps1 app` builds the daemon without a channel, and a
daemon without a channel never checks (`backend/cmd/malachid/main.go`,
`docs/services.md` §2.4, `docs/security.md` §7.4, which already names "a
store" among the builds that never check). `build-msix.ps1` packs the
folder of `build.ps1 app`, so **the MSIX already has the update check
off**.

Proposal:

- Keep it that way **explicitly**: the Store job documents that it packs
  `build.ps1 app` on purpose and must never pack the installer's folder;
  a check in `build-msix.ps1` (or the Store job) could refuse a folder
  whose `malachid.exe` reports a channel (`malachid --version` printing
  it, if it does) **[to decide when implementing]**.
- No `windows-store-*` channel in the manifest service: the Store
  updates the package itself, and offering the Inno installer to a Store
  user would mix two installations (§6) and break policy (§4.6).
- If the owner later wants security **notices** in Store builds (a text
  pointing to the Store, never a download), that is a new channel such
  as `windows-store-<arch>` whose packages carry no `url`; the manifest
  schema would need that (a new rule in `docs/services.md` §2), so it is
  out of scope now.
- The UI: with `update.status {enabled: false}` the update rows in
  Preferences → General and About should already be hidden or say
  updates come from elsewhere **[unverified: to check on the Store build;
  a sentence "Updates come from the Microsoft Store" would be one new
  msgid and is the owner's call]**.

## 6. Coexistence with the installer version

- Both can be installed at once; they share the socket path
  (`%USERPROFILE%\.cache\malachi\run\rpc.sock`) and, with
  `unvirtualizedResources`, the data. The daemon's store lock
  (`store.Lock`) refuses a second daemon over the same store, and the
  second app attaches to the running daemon **[repo, CLAUDE.md]**,
  possibly of the other version (protocol check). Without
  `unvirtualizedResources` the Store copy has its own data but the same
  socket path, so whichever starts first serves both apps.
- Recommendation: the Store build sets its own socket path
  (`MALACHI_SOCKET` set by the packaged app, e.g. under
  `%USERPROFILE%\.cache\malachi\run\store\`) **[to decide; affects
  `malachi-mcp`, which then needs the same through its alias or its
  command line]**, or the documentation says to uninstall one. The
  owner decides; the simplest is "one or the other" in the listing and on
  `web/download.html`.
- Migration from the installer to the Store: without virtualisation the
  data is shared; with it, the Store copy reads the existing data
  **[unverified]** and writes new files privately, which may split the
  store (SQLite WAL and new message files). That is a reason to test the
  migration explicitly before choosing §3.3.

## 7. Automating the submission (not to implement now)

- **Microsoft Store Developer CLI** (`msstore`, preview) in GitHub
  Actions: `microsoft/microsoft-store-apppublisher` action, `msstore
  reconfigure` with four values, then `msstore publish <bundle> -id
  <productId>` (or `msstore submission update` + `submission publish`)
  **[search]**
  ([Learn: GitHub Actions](https://learn.microsoft.com/en-my/windows/apps/publish/msstore-dev-cli/github-actions),
  [Learn: commands](https://learn.microsoft.com/en-us/windows/apps/publish/msstore-dev-cli/commands-exe)).
- Secrets it needs (names free; Microsoft's guide uses these):
  `AZURE_AD_TENANT_ID`, `AZURE_AD_APPLICATION_CLIENT_ID`,
  `AZURE_AD_APPLICATION_SECRET` (or a certificate), `SELLER_ID`; the
  product id (public, a variable). They require an **Entra ID app
  registration** added to the Partner Center account with a role that
  can submit. Whether an **individual** account with a personal
  Microsoft account can associate an Entra tenant and add such an app
  is **[unverified]**; the owner checks in Partner Center (*Account
  settings → User management → Microsoft Entra applications*).
- The alternative is the **Submission API** (REST, same credentials);
  `msstore` wraps it.
- The **first submission is manual** in Partner Center (the product, the
  listing, the age rating and the capability justifications have to
  exist first).
- Shape if automated later, within the owner's rules (`CLAUDE.md`: CI only
  on release tags and by hand, no extra checks, artifacts only for tags):
  a job in `windows.yml` on `vX.Y.Z` that builds the bundle, keeps it as
  the tag's artifact, and creates a **draft** submission with the new
  package, which the owner reviews and submits in Partner Center; or a
  separate hand-run workflow. Publishing without the owner's click is not
  proposed. A client secret in a public repository's secrets is usable
  only by workflows of the repository; the same care as the `MACOS_*`
  secrets (`docs/releasing.md` §8).

## 8. Order of work, and who does what

| # | Step | Who | Depends on |
|---|---|---|---|
| 1 | Decide version mapping (§2.1 A/B), individual vs company (§1.1), coexistence (§6) | **owner** | — |
| 2 | Create the Partner Center account, verify identity (ID + selfie) | **owner personally** | — |
| 3 | Reserve "Malachi Mail", read the three identity values (§1.3) | **owner** | 2 |
| 4 | Write and publish `web/privacy.html` (cs, en) | agent drafts, **owner approves** | — |
| 5 | Run the existing trial on Windows (`windows-msix.yml`, signed): the §17 list — starts packaged, MRT/PRI, WebView2, data and settings, notifications and clicks, `mailto:`, Run, Claude registration | Windows agent, then owner | — |
| 6 | Same package **without** `unvirtualizedResources` (§3.3), compare | Windows agent | 5 |
| 7 | Choose virtualisation path; if needed, ask developer support for `unvirtualizedResources` | **owner** | 6 |
| 8 | Implement: manifest declarations (protocol, startup task, alias), packaged code paths in the client, logos + `resources.pri`, version mapping, `publisher_name`/identity variables, both architectures + bundle, Store job | agent (only `windows/` and the workflow; a new msgid or UI change outside Windows brings in rule 6) | 1, 3, 7 |
| 9 | WACK on the bundle (§2.5) | Windows agent or owner | 8 |
| 10 | Listing texts en/cs, screenshots from sample data, 300 × 300 logo | agent drafts, **owner chooses** | 8 |
| 11 | Age rating questionnaire, category, licence terms, privacy URL, capability justifications, first submission | **owner personally** | 3, 4, 9, 10 |
| 12 | Certification feedback, fixes | agent + owner | 11 |
| 13 | Update `docs/releasing.md` §7, `docs/windows-port.md` §17, `web/download.html` | agent | 12 |
| 14 | Optional: automation (§7) | agent, secrets by **owner** | 12 |

The owner must do personally: the account and identity verification,
the name reservation, every legal statement (privacy policy, licence
terms, age rating answers), the capability justifications as the
publisher, approving texts and screenshots, pressing *Submit*, and any
secret. An agent can do everything in the repository, the trial runs on
a Windows machine, drafts, and the documentation.

## 9. Open risks

1. **`unvirtualizedResources` refused or delayed** (§3.2). Mitigation:
   the §3.3 path, tested first.
2. **Major version 0** refused (§2.1); a permanent mapping decision.
3. **MRT Core / PRI** in a package built from an unpackaged WinUI folder
   (§2.3); the trial has not run yet.
4. **The Claude registration** with a versioned install path and
   virtualised AppData (§3.4); needs the alias and a test with both
   Claude Desktop builds.
5. **Licence terms field**: that Partner Center still accepts custom
   terms replacing the standard licence for GPL software (§4.5).
6. **Entra app for automation** on an individual account (§7); only
   matters for step 14.
7. **Two installations** sharing a socket (§6).
8. **Account type**: moving from individual to company later may mean a
   new product and identity (§1.1).
9. Everything marked [search] or [unverified] was not read at the
   source in this session (learn.microsoft.com blocked).

## Sources

- [Free developer registration for individual developers on Microsoft Store (Windows blog)](https://blogs.windows.com/windowsdeveloper/2025/09/10/free-developer-registration-for-individual-developers-on-microsoft-store/)
- [What's new for individual developers (Learn)](https://learn.microsoft.com/en-my/windows/apps/publish/whats-new-individual-developer)
- [Opening a developer account (Learn)](https://learn.microsoft.com/windows/apps/publish/partner-center/opening-a-developer-account)
- [Company developer accounts free (Neowin)](https://www.neowin.net/news/microsoft-makes-company-developer-accounts-free-for-the-microsoft-store/)
- [Package version numbering (Learn)](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/package-version-numbering)
- [Package acceptance validation error (Q&A)](https://learn.microsoft.com/en-us/answers/questions/144791/problem-trying-to-submit-a-package-to-the-store-pa)
- [electron-builder #2979](https://github.com/electron-userland/electron-builder/issues/2979)
- [App package requirements for MSIX apps (Learn)](https://learn.microsoft.com/windows/apps/publish/publish-your-app/msix/app-package-requirements)
- [Publish 1 package per architecture (Q&A)](https://learn.microsoft.com/en-sg/answers/questions/73656/publish-1-package-per-architecture)
- [Write to non-virtualized AppData from MSIX (Tech Community)](https://techcommunity.microsoft.com/discussions/msix-discussions/write-to-non-virtualized-appdata-location-from-msix-installed-binary/1512850/replies/1514789)
- [How to disable registry and file redirections for MSIX (Advanced Installer)](https://www.advancedinstaller.com/msix-disable-registry-file-redirection.html)
- [A simpler way to publish Desktop Bridge apps (AppConsult)](https://learn.microsoft.com/en-us/archive/blogs/appconsult/a-simpler-and-faster-way-to-publish-your-desktop-bridge-applications-on-the-microsoft-store)
- [Store doesn't let submit app with runFullTrust (StackOverflow archive)](https://kiwix.lema.org/stackoverflow.com_en_all_2023-11/questions/51684258/windows-store-doesn-t-let-to-submit-app-with-runfulltrust)
- [MSIX update installs in a different location (Tech Community)](https://techcommunity.microsoft.com/discussions/msix-discussions/msix-update-always-installs-in-different-location-forcing-the-firewall-for-new-r/3676462)
- [Create a shortcut through an execution alias (Learn)](https://learn.microsoft.com/en-us/windows/msix/psf/create-shortcut-with-script-package-support-framework)
- [Support info for MSIX app (Learn)](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/support-info)
- [Change history for Microsoft Store Policies (Learn)](https://learn.microsoft.com/windows/apps/publish/store-policies-change-history)
- [App screenshots, images, and trailers (Learn)](https://learn.microsoft.com/en-us/windows/uwp/publish/app-screenshots-and-images)
- [Submit your app FAQ (Learn)](https://learn.microsoft.com/windows/apps/publish/faq/submit-your-app)
- [Updating age ratings for your Microsoft app (Felix Wong)](https://felixwong.com/2025/03/developers-how-to-update-age-ratings-for-your-microsoft-app/)
- [Single age rating system (Windows blog, 2016)](https://blogs.windows.com/buildingapps/2016/01/06/now-available-single-age-rating-system-to-simplify-app-submissions)
- [Windows Store makes a special allowance for open source (PCWorld)](https://www.pcworld.com/article/472689/windows_store_makes_a_special_allowance_for_open_source.html)
- [Windows Store and the GPL (J-B Kempf)](https://jbkempf.com/blog/Windows-Store-and-the-GPL)
- [Microsoft Store distribution for QGIS (qgis-developer)](https://lists.osgeo.org/pipermail/qgis-developer/2026-August/068497.html)
- [Store updates for apps (Q&A)](https://learn.microsoft.com/en-us/answers/a/12618642)
- [msstore CLI in GitHub Actions (Learn)](https://learn.microsoft.com/en-my/windows/apps/publish/msstore-dev-cli/github-actions)
- [msstore CLI commands (Learn)](https://learn.microsoft.com/en-us/windows/apps/publish/msstore-dev-cli/commands-exe)
