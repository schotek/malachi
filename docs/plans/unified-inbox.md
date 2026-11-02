# Společná doručená pošta (Unified Inbox)

## Context

Uživatel s více poštovními účty dnes musí procházet Doručenou poštu každého účtu zvlášť;
jediné místo, kde seznam míchá účty, je hledání v rozsahu „všechny účty“ (vždy ploché).
Cíl: položka **„Všechny doručené“** nahoře v postranním panelu, která ukáže Doručenou
poštu všech povolených poštovních účtů (`kind` `imap` a `graph`; nikdy `jira`, `caldav`,
`eway`) v jednom seznamu — plochém i seskupeném podle konverzací — ve všech třech
klientech (GTK, macOS, Windows) současně (pravidlo 6 CLAUDE.md).

Rozhodnutí vlastníka (AskUserQuestion 2026-11-02):
- **U1** seskupení podle konverzací ano (respektuje `group-by-conversation`).
- **U2** výběr více zpráv **přes účty**; akce se rozdělí na volání po účtech.
  Přeposlat více zpráv a Přesunout do ▸ (výběr cílové složky) jen v rámci jednoho účtu
  (jinak vypnuté).
- **U3** položka nahoře nad Oblíbenými, jen při ≥ 2 povolených poštovních účtech,
  přepínač v Předvolbách pro skrytí (gschema `unified-inbox`, bool, výchozí zapnuto).
- **U4** jen Doručená; API obecně přes roli složky, aby šlo později přidat Odeslané/Koncepty
  bez změny kontraktu (dnes přijímá jen `inbox`, jinak `invalidArgument`).

Práce v branchi `claude/exciting-ritchie-dy3qqt` (určená session; „samostatný branch“
= tato větev, ne `main`). Ověření na reálných systémech provede vlastník až potom.

## 1. Jádro (démon) — agent A (Opus)

Kompatibilní rozšíření protokolu 2, `docs/api.md` ve stejném commitu (pravidlo 5).

**API (`backend/pkg/api/types.go`)**
- `MessageListParams.Unified FolderRole \`json:"unified,omitempty"\`` a totéž
  v `ThreadListParams`. S `unified` musí být `accountId` i `folderId` prázdné, jinak
  `invalidArgument`; jediná přijatá hodnota `inbox`.
- Rozsah: složky role `inbox`, `synced`, ne `virtual`, účtů `enabled` s `Protocol()` ∈
  {imap, graph} (nový helper `isMailAccount` v `core` vedle `hasNoMail`/`isIssueAccount`).
- Výsledky beze změny tvaru: `MessageSummary` nese `accountId`/`folderId`;
  `ThreadSummary` nese `accountId`, `folderIds` a `latest.folderId` (klient z něj vezme
  složku pro `thread.get {folderId}`).
- Bez nové notifikace a bez nové chyby: klient pozná relevantní `notify.newMessage`
  podle role složky, počty sečte z `folder.list` (už je má).
- Konstanta `api.UnifiedRoles = []FolderRole{RoleInbox}` (klienti nic nehádají).

**Store (`backend/internal/store/`)**
- `ListMessages` / `ListThreads` zobecnit na sadu složek: nová `ListMessagesIn(ctx,
  folders []FolderRef, …)` / `ListThreadsIn` se stejným keyset kurzorem
  (`date`,`id` resp. `latest`, `thread_id`+`folder_id`), nové prefixy kurzoru
  (`"u"/"U"` a `"v"/"V"`), aby kurzor jedné složky nešel použít v unified a naopak.
  Dotaz `folder_id IN (…)` s existujícím indexem `messages_by_date (folder_id, date, id)`
  (SQLite udělá merge přes `ORDER BY` + LIMIT; pro pár Doručených stačí). Vlákna se
  seskupí `GROUP BY folder_id, thread_id` (vlákno patří vždy jednomu účtu → jedné Doručené).
- Původní funkce pro jednu složku zůstanou a interně volají obecnou (žádná změna chování).
- **Bez migrace.**

**Core (`backend/internal/core/messages.go`, `threads.go`)**
- Větev `unified` v `messageService.List` a `threadService.List`: sesbírat složky
  (`unifiedFolders(ctx, role)`), zavolat store, pak `decorateRows`/`decorateThreads`
  **po účtech** (seskupit výsledky podle `AccountID`, zachovat pořadí). `attachOutboxInfo`
  se přeskočí (role inbox). `SentCount`/`DraftCount` vláken počítat vůči Doručené účtu
  daného vlákna (stávající helpery s `folderID` = Doručená toho účtu).
- `mailOnlyMessages` (`core/dispatch.go`) už prázdný `accountId` propouští — ověřit test.

**Most MCP**: beze změny (volitelně později). **Dokumentace**: `docs/api.md` §4.3 a §4.4
(odstavec „Unified listing“), `docs/architecture.md` §7 (rozhodnutí U1–U4).

**Testy**: store (`TestListMessagesIn*`: dvě Doručené, řazení, kurzor přes hranici
účtů, filtr unread/flagged, skryté řádky, vypnutý účet, jira/caldav/eway mimo, cizí
kurzor = `ErrBadCursor`), core (`TestUnifiedList*`, `TestUnifiedThreads*`,
`invalidArgument` pro `unified` s `accountId` nebo jinou rolí), `api` round-trip,
`TestDocsCoverAllMethods` beze změny metod.

## 2. Reference v Go — agent A (po API), čisté balíčky `ui/internal/…`

Nový balíček `ui/internal/unified` (bez gotk4, testovaný), mustr pro Swift a C#:
- `Accounts(accounts []api.Account) []api.AccountID` — povolené imap/graph v pořadí sidebaru.
- `Shown(accounts, setting bool) bool` — ≥ 2 takové účty && přepínač.
- `Inboxes(folders map[AccountID][]Folder, accs) []FolderKey`, `Unread(...)`, `Total(...)`
  (součet `Unread` Doručených; badge a titulek „%d nepřečtených z %d“ z existujících msgid).
- `Covers(accountID, folderID, role)` — zda `notify.newMessage`/`messagesChanged` patří
  sjednocenému seznamu.
- `Params(filter, cursor, grouped)` → `MessageListParams`/`ThreadListParams` s `Unified`.
- Texty (`text.go`): „All Inboxes“ (název položky a titulek seznamu), řádek Předvoleb
  „Show All Inboxes“ + podtitulek. Nové msgid do `po/POTFILES`, česky v `po/cs.po`
  („Všechny doručené“, „Zobrazovat Všechny doručené“).
- `ui/internal/selection`: nová `RestrictUnified` / parametr `crossAccount` — v unified
  výběru `Restrict` neomezuje; `Flags`/`Combine` dostanou `SingleAccount bool`, které
  vypne Přeposlat (více) a Přesunout do; `GroupByAccount(rows)` pro rozdělení akcí.
- `ui/internal/contextmenu`: `MessageState.SingleAccount`.
- Klíč gschema `unified-inbox` (`data/io.github.schotek.Malachi.gschema.xml`),
  `settings.KeyUnifiedInbox` v `ui/internal/settings/store.go`.
- Klíč výběru: zástupný `FolderKey{Account: "", Folder: "@unified:inbox"}` s helperem
  `IsUnified()` (všechny tři klienti mají `FolderKey` jako dvojici; prázdný účet jinde
  nevzniká). Nikdy do `favourite-folders` ani `collapsed-*`, nedá se připnout.
- Klíčová změna ve všech klientech: místo `k == listFolder` všude
  `listCovers(listFolder, k, folders)` (= rovnost, nebo unified a `k` je Doručená
  pokrytého účtu) — `onNewMessage`/`applyNewMessage`, `handleMessagesChanged`,
  `onSyncFinished`, `withdrawViewedNotifications`, `afterUndo`; `fetchMembers`
  (`thread.get`) bere účet z řádku a složku z `latest.folderId`.
- Původ řádku: v unified seznamu `rowMessage`/`searchOrigin` ukáže jen jméno účtu
  (`accountLabel`), stejným `widget.Message.Origin` jako hledání.

## 3. GTK — agent B (Sonnet), po A

Soubory: `ui/internal/window/model.go` (`sortFolders`: řádek „Všechny doručené“ první,
`folderEntry.Unified`, `badgeFor`, `initialFolder` beze změny), `folders.go`
(`rebuildFolderList`, `newFolderRow` s ikonou `mail-unread-symbolic`, `selectFolder`,
`onNewMessage`: vložit, když `unified.Covers`, `onSyncFinished`, `adjustCounts` →
přepočet unified badge), `messages.go` (`loadMessages`/`loadMore` s `Unified`),
`threads.go` (`loadThreadPage`, `fetchMembers`: účet a složka z řádku, ne z `listFolder`),
`notify.go` (`handleMessagesChanged`: reload při změně kteréhokoli pokrytého účtu),
`search_model.go` (`searchOrigin`: v unified zobrazit kapsli účtu na řádku — přes
`widget.Message.Origin` jen `accountLabel`), `date_groups.go` (`usesDateGroups` i pro
unified), `selection.go` (bez `restrictSelection` v unified), `actions.go`
(trash/archive/junk/move po účtech: `list[0].AccountID` → smyčka nad `GroupByAccount`),
`action_rules.go`, `context_menus.go`, `undo.go` (`afterUndo` reload i v unified),
`window.go` (`refreshListTitle`), `preferences.blp`/`preferences.go` (řádek
v Předvolby → Seznam zpráv nebo Obecné → Postranní panel), `notify.go`
`withdrawViewedNotifications` (stáhnout notifikace všech Doručených).
Hledání v rozsahu „složka“ při unified = všechny Doručené (search s `FolderID` prázdným
by bylo jiné): v první verzi rozsah „Složka“ v unified → „Všechny účty“ s `in:inbox`.
Testy `ui/internal/window/*_test.go` nad falešným oknem + smoke `MALACHI_GTK_SMOKE`.

## 4. macOS — agent C (Sonnet), po A

Vstupy: `Model/FolderKey.swift`, `FolderTree.swift` (`sortFolders`, `mailSidebarAccounts`),
`MailModel.swift`, `MailboxController.swift` (`select`, `handleNewMessage`,
`handleMessagesChanged`, `onSyncFinished`), `MailboxController+List.swift`
(`loadMessages`, `loadThreadPage`, `loadMore`, `restrict`, `applyNewMessage`),
`ActionsController.swift` (`msgs[0].accountId` → po účtech), `API/Messages.swift`
a `API/Threads.swift` (`accountId`/`folderId` jako optional nebo `unified`),
`Settings.swift` (+klíč, `SettingsTests` 44 → 45), AppKit
`Sidebar/FolderOutlineDataSource.swift`, `FolderSidebarViewController.swift`,
`MessageListViewController.swift`.
Port 1:1 z `ui/internal/unified` do `MalachiCore/Model/Unified.swift`, `Selection`,
`ContextMenu`, `Settings` (klíč `unified-inbox`), `FolderTree.swift` (položka nahoře),
`MailboxController`/`ListController` (načítání s `unified`, notifikace, počty),
`ActionsController` (akce po účtech), `MalachiMail` sidebar (`FolderSidebarViewController`),
Předvolby (`AppearancePaneViewController` nebo General). API zrcadlo
`MalachiCore/API` (+`unified`). Testy `swift test` (`UnifiedTests`, rozšířené
`SelectionTests`, `MailboxController*Tests`). Na Linuxu bez překladu → v předávce.

## 5. Windows — agent D (Sonnet), po A

Vstupy: `Model/FolderKey.cs`, `Controllers/SidebarKey.cs`, `Model/FolderTree.cs`,
`Presentation/SidebarRowKind.cs` (+kind nebo příznak), `MailboxController.cs`
(`Select`, `HandleNewMessage`, `HandleMessagesChanged`, `OnSyncFinished`),
`MailboxController.List.cs`, `ActionsController.cs` (`msgs[0].AccountId` → po účtech),
`Settings/SettingsKey.cs` + `SettingsStore` (`SettingsTests` 48 → 49,
`GschemaCoverageTests`), App `Main/SidebarPane.xaml(.cs)`, `MessageListPane.xaml.cs`.
Port do `Malachi.Core` (`Model/Unified.cs`, `Selections`, `ContextMenus`,
`SettingsStore.UnifiedInbox` — počet klíčů gschema v testech +1, `Api` +`Unified`
a vzorky round-trip, `GoContractTests`), `MailboxController`, `ActionsController`,
App `SidebarPane`, Předvolby. Nové msgid nesmí skončit v `windows/parity-exclusions.txt`
(použít je). Testy xUnit v `windows/tests/Malachi.Core.Tests`. Bez `dotnet` → předávka.

## Orchestrace

1. Já: vytvořit/checkout větev, krátký `docs/plans/unified-inbox.md` (tento plán česky
   + rozhodnutí U1–U4 + kontrakt).
2. **Agent A (Opus)**: jádro + `docs/api.md` + reference `ui/internal/unified`,
   `selection`, `contextmenu`, `settings`, gschema, `po/` (texty ručně do `.pot`/`cs.po`,
   `make po` není k dispozici — poznamenat). Spustí `go vet`, `go test ./...` v `backend/`
   a čisté balíčky v `ui/`. Commit.
3. Po A paralelně **B (GTK, Sonnet)**, **C (macOS, Sonnet)**, **D (Windows, Sonnet)**,
   každý jen ve svém stromu (B: `ui/internal/window`, `ui/data/ui`; C: `macos/`;
   D: `windows/`), commit každý zvlášť. Konflikty v `po/` řeší jen A.
4. Já: revize diffu, `go test` znovu, aktualizace CLAUDE.md (odstavec Společná doručená
   pošta + bod v pořadí prací), commit, push `git push -u origin claude/exciting-ritchie-dy3qqt`.

## Ověření

Zde (Linux kontejner): `cd backend && go vet ./... && go test ./...`;
`cd ui && go test ./internal/unified/... ./internal/selection/... ./internal/contextmenu/...
./internal/settings/...`; `gofmt -l`; GTK okno jen `go vet` pokud gotk4 cgo jde
přeložit, jinak `gopls check`/typová kontrola.

Vlastník na reálných systémech (předávka v `docs/plans/unified-inbox.md`):
- GTK v kontejneru: `gtk.sh make build`, `gtk.sh test-ui`, smoke, `make po` (beze změny
  msgid), `make lint`, `make run-gtk-live` nad kopií dat.
- macOS: `swift build`, `swift test`, `check-strings.py`, debug binárka nad kopií dat.
- Windows: `build.ps1 app`/`test`/`lint`, UI smoke, ruční průchod.
- Ručně ve všech třech: položka jen při ≥ 2 účtech, přepínač, badge, řazení, plochý
  i seskupený seznam, nová pošta z obou účtů, výběr přes účty + Koš/Archivovat/Přečtené/
  hvězdička + Zpět, Přesunout do vypnuté u smíšeného výběru, odpověď z účtu řádku,
  hledání z unified.

## As built (2026-11-02) a předávka k ověření

Commity na `claude/exciting-ritchie-dy3qqt`: `c067f24` démon + `docs/api.md`,
`6bd7543` Go reference (`ui/internal/unified`, `selection`, `contextmenu`, `settings`,
gschema, `po/`), `e1c1b7d`/`1754379`/`e38cd99` GTK, `b1c6237` macOS, `964f91e` Windows.

Navíc proti plánu (ve všech třech klientech):
- Vlákna v seskupeném sjednoceném seznamu jsou klíčovaná dvojicí (účet, vlákno):
  Graph ukládá `conversationId` Exchange jako `thread_id` a dvě schránky jednoho
  tenantu ho sdílejí. Do `thread.get` jde vždy surové id.
- Skupiny Zpět (`undo.Entry.Group`, `Stack.TakeGroup`; Swift-first, Go reference
  a C# port): Koš / Archivovat / Nevyžádaná přes víc účtů je jedna akce, toast i Ctrl+Z
  ji vrátí celou (toast „Vráceno“ se ukáže za každý účet, vidět je poslední).
- Kontextové menu položky jen se „Zkontrolovat poštu“ (všechny účty).
- Uložit do eWay-CRM je u výběru z více účtů vypnuté (`crm.save` bere jeden účet).
- Hledání spuštěné z položky má rozsah „Všechny účty“ (Složka vypnutá).

Ověřeno zde (Linux): `backend` `go vet ./...`, testy `pkg/api`, `store`, `core`, `rpc`;
`ui` testy `unified`, `selection`, `contextmenu`, `undo`; `gofmt`. **Nic z GTK okna,
macOS ani Windows se tu nepřeložilo** (chybí GTK, Swift, dotnet).

Čeká na reálných systémech:
- GTK (kontejner): `gtk.sh make build`, `go test ./internal/window/...`
  (`unified_test.go`), `ui/internal/settings`, `make po` (posunuté `#:` odkazy,
  ručně přidané msgid mají vyjít beze změny), `make lint`.
- macOS: `swift build`, `swift test` (`UnifiedTests`, `MailboxControllerUnifiedTests`,
  `SettingsTests` 45, `SelectionTests`, `ContextMenuTests`, `ActionsControllerTests`),
  `check-strings.py`; řádek bez šipky zarovnaný s hlavičkami účtů.
- Windows: `build.ps1 app`/`test`/`lint` (`UnifiedTests`, `SettingsTests` 49,
  `GschemaCoverageTests`, `ApiRoundTripTests`, `GoContractTests`, `CrmRulesTests`);
  pozor na collection expressions v podmínkách a `with` na `readonly record struct`.
  `MailFixture` `unified` zatím nepodporuje (macOS ano).
- Ručně ve všech třech nad kopií dat se dvěma účty (ideálně dva M365 v jednom tenantu):
  položka jen při ≥ 2 účtech a se zapnutým přepínačem, skrytí při výběru, badge, plochý
  i seskupený seznam a stránkování, kapsle účtu na řádcích, nová pošta do obou účtů,
  výběr přes účty + Přečtené/hvězdička/Koš/Archivovat/Nevyžádaná a jedno Zpět,
  Přeposlat/Přesunout do/eWay vypnuté u smíšeného výběru, odpověď z účtu řádku,
  hledání z položky, Zkontrolovat poštu z kontextového menu.

Nevyžádaná, Archivovat (rozhodnutí vlastníka 2026-11-02): zprávy účtů bez složky
dané role se ve všech třech klientech přeskočí, otázka počítá jen přesouvané a toast
„Tento účet nemá složku nevyžádané pošty“ přijde, jen když složku nemá žádný účet.

Zbývající rozdíly k rozhodnutí: prázdný seznam
říká „Tato složka je prázdná“; odpověď odeslaná do Odeslaných neobnoví počet
odeslaných u konverzace ve sjednoceném seznamu (`applyNewMessageElsewhere`) v GTK.
