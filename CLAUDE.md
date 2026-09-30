# CLAUDE.md

Instrukce pro AI asistenty pracující na tomto repozitáři.

## Co je tento projekt

**Malachi Mail** — desktopový emailový klient: jedno jádro v Go s veškerou
logikou (démon `malachid` v `backend/`) a nativní UI pro každou platformu:
GTK4 pro Linux (`ui/`), Swift/AppKit pro macOS (`macos/`) a C#/WinUI 3 pro
Windows (`windows/`). UI a démon jsou dva samostatné procesy komunikující
přes JSON-RPC na unix socketu (na Windows AF_UNIX).

### Názvosloví — dodržuj důsledně

- V textech pro uživatele (README, metainfo, UI) piš `Malachi Mail`
- V kódu, cestách, balíčcích a identifikátorech `malachi`, daemon `malachid`
- App ID `io.github.schotek.Malachi` — nikdy nezkracuj ani neměň velikost písmen
- Nepřejmenovávej nic z toho bez explicitního zadání
- GitHub uživatel je `schotek`: Go module path `github.com/schotek/malachi/…`,
  repozitář `https://github.com/schotek/malachi`

## Nepřekročitelná pravidla

### 1. Hranice backend ↔ UI je posvátná
Veškerá logika patří do backendu. UI pouze zobrazuje a posílá příkazy.
Pokud přidáváš funkcionalitu a zdá se ti přirozené dát ji do UI,
je to skoro jistě chyba. Cílem je vyměnitelné UI.
UI smí z `backend/` importovat pouze `pkg/api`.

### 2. Sanitizace HTML nikdy neopouští backend
`message.body` vrací sanitizované HTML. Surové HTML se přes API neposílá
za žádných okolností — ani pod flagem, ani pro debugging, ani v testech
běžících proti reálné schránce. Stub v `internal/sanitize` selhává
„zavřeně" (vrací chybu a prázdné tělo, nikdy vstup).

### 3. Email je nepřátelský vstup
Každý parser MIME, každý renderer, každý handler odkazu vychází z předpokladu,
že vstup je záměrně poškozený. Testy pro nový parsovací kód musí obsahovat
patologické případy (do `backend/testdata/mime`), ne jen šťastnou cestu.
V UI: `SetUseMarkup(false)` na všem, co zobrazuje data ze serveru (na macOS
`stringValue`, na Windows `TextBlock.Text`/`TextBox.Text`; nikdy
`NSAttributedString(html:)`, XAML ani RTF nad čímkoli ze zprávy, HTML jen
v uzamčených webových pohledech).

### 4. Jeden kód na platformu, žádné větvení
Jádro (`backend/`) je platformně neutrální Go: týž strom se beze změny
staví pro Linux i do aplikací pro macOS a Windows, linuxové služby přes
D-Bus jsou jen volitelné za běhu. GTK UI je linuxový kód. Do žádného
z nich nepřidávej build tagy, podmíněnou kompilaci ani abstrakce „pro
jistotu“ pro Windows a macOS; co se jinde liší, řeší démon neutrálním
bodem rozšíření voleným za běhu (jako `MALACHI_KEYRING=helper`) nebo
parametrem, který mu předá klient (jako `malachi-mcp --claude-desktop-config`),
ne platformním kódem ve stromu. Ostatní platformy mají vlastní nativní UI
(Swift/AppKit v `macos/`, C#/WinUI 3 ve `windows/`) jako samostatné
klienty nad API démona, přičemž GTK UI je mustr, který zrcadlí; nikdy
větvení tohoto kódu. Platformní kód patří jen do stromu svého klienta.
Přenositelnost je zajištěná hranicí na API, ne podmíněnou kompilací.

### 5. Neměň API kontrakt bez aktualizace docs/api.md
Kontrakt (`backend/pkg/api/`) a dokumentace (`docs/api.md`) se mění současně,
v jednom commitu. Test `TestDocsCoverAllMethods` hlídá, že každá metoda a
notifikace je v dokumentu zmíněna a každý chybový kód má řádek v tabulce §2
(`| kód | jméno |`). Chybové kódy se nikdy nepřečíslovávají, jen přidávají.
Nekompatibilní změna = bump `ProtocolVersion`.

## Konvence

- Go: standardní formátování, `golangci-lint`, errors wrapované s kontextem
- Struktura balíčků: `internal/` pro implementaci, `pkg/api/` pro veřejný kontrakt
- MCP most (`backend/cmd/malachi-mcp`) je klient démona jako UI: z `backend/`
  importuje jen `pkg/api`, žádné volání nepošle před dokončeným handshakem
  (`api.ClientHandshake`), nikdy nevrací HTML, každý řetězec z pošty prochází
  `clean()` a ohradou s nonce, mutující nástroje jen za přepínačem (`docs/mcp.md`)
- Dva Go moduly (`backend/`, `ui/`) + `go.work` v kořeni; `ui/go.mod` má
  `replace` na `../backend`, aby offline build fungoval i bez workspace
- UI: Blueprint (`.blp`), ne ručně psané GtkBuilder XML; `.ui` jsou generované
  a ignorované gitem; Go se na widgety odkazuje přes ID z builderu
- Callbacky z RPC klienta běží mimo hlavní smyčku → vždy `glib.IdleAdd`
- Commity: conventional commits (`feat:`, `fix:`, `docs:`, `refactor:`)
- Migrace databáze (`internal/store/migrations/NNNN_name.sql`) jsou dopředné
  a číslované, nikdy se needitují zpětně
- Závislosti nad rámec zadání jen se zdůvodněním v commitu
- Jazyk: **backend je jazykově neutrální** — vrací jen kódy, enumy a anglické technické
  `error.message` (nestabilní, UI je nezobrazuje doslova); gettext se do `backend/` nikdy
  nezavádí. Veškeré texty pro uživatele řeší desktopová aplikace: v Blueprintu `_("…")`,
  v Go `i18n.T/N/C` (`ui/internal/i18n`, doména `malachi`). Věty nikdy neskládej
  konkatenací, používej `fmt.Sprintf(i18n.T("… %s …"), x)`; plurály přes `i18n.N`;
  nad nejednoznačné msgid dej `// TRANSLATORS:`; formáty data jsou strftime msgid.
  Nový Go soubor s texty přidej do `po/POTFILES`; `make po` aktualizuje šablonu i `.po`,
  `make lint` hlídá, že `po/malachi.pot` odpovídá zdrojům. Prefixy `Re:`/`Fwd:` se
  nepřekládají.
- macOS klient: texty přes `L10n.T/N/C` s klíčem = GTK msgid (nic do
  `po/POTFILES`, katalogy vznikají z `po/` při buildu); řetězce jen pro macOS
  zůstávají anglicky s komentářem `// macOS-only string`; `.blp` a Go UI jsou
  reference parity, `docs/screenshots` nikdy.
- Windows klient: texty přes `L10n.T/N/C` (C#) a `{l:T Msgid=…}` (XAML)
  s klíčem = GTK msgid (nic do `po/POTFILES`, `po/*.po` se čtou za běhu
  z `locale\` vedle exe); řetězce jen pro Windows zůstávají anglicky
  s komentářem `// Windows-only string` (v XAML `<!-- Windows-only string -->`);
  msgid šablony, který klient nepoužívá, patří s důvodem do
  `windows/parity-exclusions.txt`; `.blp` a Go UI jsou reference parity,
  macOS klient zdroj portu. Konvence C# kódu jsou v `docs/windows-port.md`
  §3.1 (hlavička souboru jmenuje portovaný Swift a Go soubor).
- Licence: `backend/` je AGPL-3.0-only (duálně licencované jádro, viz
  `LICENSING.md`), vše ostatní GPL-3.0-or-later. Každý nový zdrojový soubor
  (`.go`, `.swift`, `.py`, `.blp`, `.sql`, `.sh`, ve `windows/` také `.cs`,
  `.xaml`, `.csproj`/`.props`/`.targets`/`.slnx`, `.manifest`, `.config`,
  `.ps1`, `.js`, `.css`, `.html`, `.xml`, `.resw`) začíná hlavičkou `SPDX-FileCopyrightText`
  a `SPDX-License-Identifier` podle toho, ve které části leží; XML typy ji
  mají jako komentář za XML deklarací, JSON a Markdown žádnou. Ve `windows/`
  ji u C# hlídá kompilátor (IDE0073 podle `windows/.editorconfig`), u
  ostatních typů `Malachi.Conventions.Tests`. Do `backend/`
  nepřidávej závislosti pod copyleftem silnějším než MPL/LGPL, jinak by
  komerční licence jádra nebyla udělitelná

## Prostředí

Vývoj probíhá v Fedora 42 Toolbx kontejneru (`toolbox enter malachi`).
Kompilace a testy uvnitř, `flatpak-builder` a testování výsledného balíčku
na hostiteli.

Ověřené verze (2026-09-02): Go 1.25, GTK 4.18, libadwaita 1.7, GLib 2.84,
WebKitGTK 2.52 (API 6.0), Blueprint 0.16, SQLite 3.47, gsound 1.0.3
(`gsound-devel`; bez něj Makefile staví UI s `-tags nosound`).

WebKitGTK pro GTK4 je API verze 6.0 (`webkitgtk6.0-devel`), ne 4.x.

gotk4 je připnutý na snapshot `v0.3.2-0.20250703063411` a gotk4-adwaita na
commit `e94555b846b6` (2025-07-03, generováno pro libadwaita 1.7), protože
gotk4 v0.4.x vyžaduje GLib ≥ 2.86 a novější gotk4-adwaita libadwaita 1.9;
Fedora 42 má GLib 2.84 a libadwaita 1.7. Při povýšení runtime (GNOME 50+)
povyš obojí najednou. Čistá kompilace gotk4 trvá ~15 min; `CC="ccache gcc"`
by při další plné rekompilaci většinu času ušetřil.

WebKitGTK 6.0 binding je `gotk4-webkitgtk/pkg` `v0.0.0-20240108031600-dee1973cf440`
(balíček `webkit/v6`, generováno pro WebKit 2.42, repozitář od té doby stojí).
Vyžaduje jen gotk4 v0.1.0, takže náš pin zůstává. Binding **nemá žádné asynchronní
funkce** (`evaluate_javascript` apod.); `ui/internal/editor/evaluate.go` má na to
malý cgo shim. První kompilace `webkit/v6` trvá několik minut.
DMA-BUF renderer WebKitu je v UI **vypnutý** (`ui/internal/editor/renderer.go`
nastaví `WEBKIT_DISABLE_DMABUF_RENDERER=1` při startu): na Asahi grafice ukazoval
první snímek view jako červený záblesk a skrytí widgetu nepomáhá, buffer obchází
GSK. `MALACHI_WEBKIT_DMABUF=1` ho pro test zase zapne. Ladění sandboxu:
`WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1` jen pokud bwrap selže (v tomto
Toolbxu není potřeba).

Windows klient se staví a testuje přímo na Windows 11 (ověřeno 2026-09-28 na
Windows 11 Pro 26100 x64): .NET SDK 10.0.4xx (`windows/global.json`,
`rollForward: latestFeature`), Go 1.25+ (`C:\Program Files\Go\bin\go.exe`
se najde i mimo `PATH`, tady go1.27), Git for Windows (`git describe` a jeho
`sh.exe` pro recepty kořenového Makefilu), GNU make 4.4 z winget
`ezwinports.make` (volitelný, bez něj `windows\build.ps1`), Windows
PowerShell 5.1. Visual Studio 2026 je volitelné (otevře `windows\Malachi.slnx`),
ale `malachi-credentials.exe` je NativeAOT a linkuje se MSVC nástroji
(Visual Studio nebo jeho Build Tools, „Desktop development with C++“, pro
ARM64 i ARM64 build tools, které tu nejsou). Backend se na Windows staví
beze změn a jeho `go vet` / `go test ./...` jsou zelené. Klon z doby před
`.gitattributes` (LF pro všechno, `testdata` beze změn) je potřeba jednou
znovu checkoutnout na čistém stromu: `git rm -r --cached -q . && git reset
--hard`; nikdy `git add --renormalize` na starém CRLF worktree (zapsal by
CRLF do LF fixtur).

## Stav a priority

Aktuální fáze: IMAP čtení i odesílání fungují. Účty (registr, keyring přes
Secret Service, průvodce s autodetekcí, editace), sync engine (`internal/imap`:
jeden syncer na účet, IDLE + polling, operační log pro příznaky/přesuny/
mazání, okno retence `offlineDays`, APPEND odeslaných kopií do Sent), MIME
parser (`internal/mime`), odesílání (`internal/smtp` builder + SMTP doručení,
`internal/outbox` worker na účet s backoffem, lokální složka role `outbox`,
`message.send` / `outbox.retry`) a UI se skutečnými složkami, zprávami a
oknem Nová zpráva. Microsoft 365 / Outlook.com jde přes Microsoft Graph
(`kind: graph`, `internal/graph`: delta dotazy, immutable ID, polling
inboxu po minutě, `sendMail`), token z GNOME Online Accounts
(`internal/auth/goa`, D-Bus) nebo z vlastního přihlášení démona (viz
níže); core rozděluje účty podle `kind` na IMAP a Graph supervisor
(`internal/core/dispatch.go`); průvodce nabízí účty z GOA (`account.linked`)
a pro nepřihlášené adresy GOA stránku s volbou „Použít místo toho prohlížeč“.
HTML pošta: sanitizér (`internal/sanitize`, vlastní nad `x/net/html`,
verze rulesetu `"1"`) sanitizuje na vyžádání ze surového souboru;
`message.body` vrací `html`, `blocked`, `links`, `inlineParts`, případně
`htmlWithheld`; vzdálené obrázky pod politikou `allow` stahuje démon
(`internal/remoteimg`) a vkládá jako `data:`; `message.part` servíruje
části zprávy pro schéma `malachi-cid:`; `message.embedded` vykreslí
přiloženou zprávu (`message/rfc822`, `.eml`) jen pro čtení a jen na
vyžádání, obrázky vloží jako `data:`, parser nerekurzuje, nic se neukládá
(UI ji otevře z chipu přílohy v samostatném okně); klik na jinou přílohu
ji ukáže v náhledu (GNOME Sushi přes `ui/internal/preview`, bez Sushi
výchozí aplikace; na macOS Quick Look, na Windows vlastní náhled ve
WebView2), Otevřít a Uložit jako jsou v menu chipu. UI je vykresluje ve WebKitGTK 6.0
bez JavaScriptu (`ui/internal/htmlview`, CSP, síť odříznutá), lišta nabízí
načtení obrázků a důvěru odesílateli. Compose posílá formátovaný text
(`richText = true`), odchozí zprávy jsou `multipart/alternative`
(+ `related` pro vložené obrázky, + `mixed` pro přílohy). Odpověď a
přeposlání připravuje backend (`draft.create`, `internal/core/quote.go`):
adresáti, `Re:`/`Fwd:`, originál citovaný jako sanitizované HTML v compose
režimu (první `draft.save` je identita), jeho `cid:` obrázky zkopírované do
úložiště příloh pod novými id (`attachment.get` je vrací editoru); UI dodá
jen lokalizovanou hlavičku citace (`attribution`), `compose.Prefill` je
fallback bez démona. Koncepty na serveru: uložený koncept po 30 s klidu
nahraje syncer do složky Koncepty (IMAP `APPEND` s `\Draft`, Graph
`POST me/messages`), každá verze s novým Message-ID, předchozí kopie jde
přes `OpDelete` (`store/draft_sync.go`, `core/draft_sync.go`, migrace 0012);
odeslání, `draft.delete` a koš/přesun kopie mažou druhou stranu;
`draft.open` otevře zprávu ze složky Koncepty jako koncept (vlastní, nebo
převzatý od jiného klienta přes `replaces` jen bez ztráty); všechna tři UI ji
otevírají dvojklikem a pruhem „Upravit“. Trvalé smazání na Gmailu jde přes
Koš. Doplňování příjemců: `contact.search` slévá
sebrané adresy (`collected_addresses`, plní outbox worker po doručení a
jednorázový backfill ze složek Odeslané, nikdy z příchozího `From`)
s knihami EDS účtu odesílatele (`internal/contacts/eds`, D-Bus `Sources5`
+ `AddressBook10`, jen čtení, bez EDS tiše prázdné). Gmail / Google
Workspace: IMAP+SMTP s XOAUTH2 tokenem z GOA (`core/goa_accounts.go`,
`credentialFor`), All Mail jako nesynchronizovaný cíl archivace.
Threading: hotovo v backendu. `internal/thread` = pravidla sjednocení
(union, ne JWZ strom) a capy, `store/threads.go` = linkování v transakci
zápisu + `ListThreads`/`GetThread`/`ThreadMessages`, migrace 0011 = indexy +
`message_refs`, `thread_id` nikdy prázdné, Graph drží serverové
`conversationId`, backfill starých řádků v `core.Maintain`, IMAP stahuje
`References` už s obálkou; `thread.list`/`thread.get` (`core/threads.go`)
počítají vlákna per účet a zobrazují per složku (agregáty jen přes členy
složky, `latest` = nejnovější člen v plném souhrnu). UI: přepínač
Předvolby → Seznam zpráv → Seskupovat podle konverzací (GSettings
`group-by-conversation`, výchozí vypnuto) přepne seznam na `thread.list`
(`ui/internal/window/thread_model.go` čistý model, `threads.go` zrcadlení
do ListBoxu podle klíčů), rozbalení volá `thread.get {folderId}`, akce na
sbaleném řádku jdou na všechny členy ve složce, Outbox se neseskupuje.
Vyhledávání: `search.query` jen nad lokálním úložištěm (okno
`offlineDays`), contentless FTS5 `messages_fts` s prefixy 2 a 3 a
`search_docs` (migrace 0013, triggery na `messages`, backfill v
`core.Maintain` pod `search.indexed`), `internal/search` = čistý parser
syntaxe, FTS5 výraz s každou hodnotou v uvozovkách a výřez se zvýrazněním;
rozsahy složka / účet / všechny povolené účty, koš a nevyžádaná jen jako
vybraná složka nebo přes `in:`, řazení podle data, `total` do 1000.
GTK: lišta hledání nad seznamem (Ctrl+F, `window/search.go`,
`search_model.go`, GSettings `search-scope`), hledá při psaní od 2 znaků,
výsledky ploše se složkou/účtem a tučnými shodami, jednopísmenné zkratky
se při psaní do pole vypínají; MCP nástroj `search_messages`; macOS:
hledací pole v toolbaru (⌘F), pruh rozsahu nad seznamem jako v Mailu,
logika v `MalachiCore` (`SearchModel.swift`,
`MailboxController+Search.swift`); Windows: hledací pole uprostřed titulkové
lišty (Ctrl+F, Ctrl+E), pruh rozsahu nad seznamem, logika v `Malachi.Core`
(`SearchModel.cs`, `MailboxController.Search.cs`). MCP most pro AI agenty
(`backend/cmd/malachi-mcp`, stdio server, klient socketu importující jen
`pkg/api`; `.mcp.json` v kořeni ho registruje pro Claude Code; výchozí jen
čtení + koncepty (nové, odpověď, odpověď všem, přeposlání přes
`draft.create`), `--allow-modify` / `--allow-send` přes
`MALACHI_MCP_ALLOW_MODIFY` / `MALACHI_MCP_ALLOW_SEND`; nikdy nevrací HTML,
obsah pošty v ohradě s nonce; podpříkazy `status`/`install`/`uninstall
--json` zapisují registraci do konfigurace Claude Desktop a Claude Code a
Předvolby → AI → MCP je ve všech třech UI jen přepínač nad nimi (GTK
`ui/internal/mcpsetup`, macOS `MCPRegistrationController`, Windows
`McpRegistrationController`, který předá `--command` a u MSIX Claude Desktop
`--claude-desktop-config`); viz `docs/mcp.md`). macOS klient (`macos/`, Swift/AppKit, SwiftPM tools 6.0, macOS 14+,
GPL-3.0-or-later): plné zrcadlo GTK UI — průvodce účtem, sidebar,
seznam (plochý i vlákna), čtení s uzamčeným WKWebView (JS vypnutý,
stejná CSP, scheme handler `malachi-cid:`, síť odříznutá proxy i content
rule listem), přílohy, akce, compose s contenteditable editorem a
bridge skriptem, koncepty, `draft.create`, `mailto:`, Settings,
notifikace, login item, čeština. Tři targety: `MalachiCore` (bez
AppKit: API typy přepsané z `docs/api.md`, transport, supervisor,
1:1 porty čisté logiky Go UI i jejích testů, `@MainActor` kontrolery,
`UserDefaults` s klíči GSettings + `command-r`, gettext shim s klíči =
GTK msgid; `scripts/po2strings.py` generuje `.lproj` z `po/` při
buildu), `MalachiMail` (AppKit), `MalachiKeychain` (`malachi-keychain`,
helper keyringu démona nad login keychain). Démon pro něj dostal dvě
rozšíření volená za běhu, obě platformně neutrální:
`MALACHI_KEYRING=helper` + `MALACHI_KEYRING_HELPER` (`internal/auth/helper`,
styl git-credential) a výchozí hodnoty preferencí úložiště
`MALACHI_DEFAULT_*` (komprese zapnutá, přílohy 30 dní; viz níže);
app spouští `malachid` z bundlu s `--config`/`--store` v
`~/Library/Application Support/Malachi Mail/`, socket na výchozí cestě
démona, `malachi-mcp` je v bundlu. Gmail a Microsoft 365 jdou přes
vlastní přihlášení démona v prohlížeči (client ID v `config.toml`),
doplňování příjemců jen ze sebraných adres. Odchylky od GTK jen z tabulky
v `macos/README.md` (unified toolbar, skládání panelů bez navigace zpět,
stavový pruh přes spodek okna místo patičky sidebaru, bez tlačítka
hlavní nabídky (je v menu baru), bannery jako karty se symbolem, seznam se stránkuje sám, filtr v toolbaru jako v Mailu, hledací pole v toolbaru s pruhem rozsahu, Settings bez hledání, ⌥⌘↑/↓, volba ⌘R, pořadí tlačítek NSAlert,
quarantine na přílohách, zvuk Glass); `.blp` jsou reference, nová
funkce jde nejdřív do backendu a GTK, pak sem. Ad-hoc podpis: po každém
rebuildu se Keychain jednou zeptá (`make macos SIGN='…'` to řeší).
Kontributorský popis `docs/macos-port.md`. `make macos` / `run-macos` /
`test-macos` jsou jen na Darwinu.

Windows klient (`windows/`, C#/.NET 10, WinUI 3 na Windows App SDK 2.5
z komponentových balíčků, ne z metabalíčku, Windows 11, x64 a ARM64,
nebalený a self-contained, GPL-3.0-or-later): plné zrcadlo GTK UI, port
macOS klienta — průvodce účtem, sidebar s oblíbenými, seznam (plochý
i vlákna, stránkuje se sám), hledání, čtení v uzamčeném WebView2 (skript
vypnutý, stejná CSP, síť odříznutá pravidlem resolveru `MAP * ~NOTFOUND`
a mrtvou proxy, každý požadavek odpovídá brána `WebResourceRequested`,
schéma `malachi-cid:` a dokument přes `malachi-doc:`), okna zpráv
a přiložených zpráv, vlastní náhled příloh (obrázky, PDF a text ve
WebView2, programy nikdy), Mark of the Web na přílohách, akce
s kontextovými menu, compose s contenteditable editorem a bridge skriptem,
koncepty, `draft.create`, `mailto:` a registrace pro Výchozí aplikace,
Předvolby, notifikace se systémovým zvukem pošty, ikona v oznamovací
oblasti při běhu na pozadí, spuštění po přihlášení (klíč Run), čeština.
Tři projekty a helper: `Malachi.Core` (net10.0 bez WinUI a P/Invoke,
testovatelný na jakémkoli OS: API typy přepsané z `docs/api.md`, transport
s handshakem, supervisor démona, 1:1 porty čisté logiky Go UI a Swiftu
i jejich testů, kontrolery na UI vlákně, prezentační třídy, které macOS
drží netestované v AppKitu, nastavení s klíči gschema + `ctrl-r`
v `HKCU\Software\io.github.schotek.Malachi`, `L10n` nad `po/*.po` čtenými
za běhu), `Malachi.Platform.Windows` (služby Windows přes CsWin32: proces
démona a konzole, politika souboru s klíčem, registr, Mark of the Web,
launcher, Run, `mailto:`, tray), `Malachi.App` (WinUI 3, `MalachiMail.exe`,
tenké: okna, XAML, vrstva WebView2) a `Malachi.Credentials`
(`malachi-credentials.exe`, NativeAOT helper keyringu démona nad Credential
Managerem, hodnota nad 2560 B po kusech ověřených SHA-256). Testy: xUnit v3
na Microsoft.Testing.Platform, ~4 000 (Core s FakeDaemon a MailFixture,
služby Windows včetně skutečného `malachid.exe`, helper, konvence: SPDX
hlavičky, gschema, kontrola řetězců a pokrytí msgid) a síťový kanárek, který
pouští skutečné pohledy WebView2 proti nepřátelským dokumentům a surovému
korpusu `testdata/mime` a z NetLogu Chromia ověřuje, že neodešel žádný
požadavek; UI smoke testy (`Malachi.App.UiTests`, UI Automation nad
publikovanou aplikací s dočasnými daty a falešným keyringem, volitelně
proti lokálnímu IMAP/SMTP serveru `MALACHI_DEVMAIL`) a ruční průchod celým
UI. CI `.github/workflows/windows.yml` (Go testy démona na Windows, build,
testy, lint a zipy x64 a ARM64). Build: `make windows` / `run-windows` / `test-windows` (jen na
Windows, z PowerShellu i Git Bashe) delegují na `windows/build.ps1` (`app`,
`test`, `lint`, `package`, …); výstup `build\windows\<arch>\Malachi Mail\`
s `malachid.exe`, `malachi-mcp.exe` a `malachi-credentials.exe`. App spouští
démona sama (`--config`/`--store` v `%LOCALAPPDATA%\Malachi Mail\`,
`MALACHI_DATA_DIR` přebíjí, socket na výchozí cestě démona
`%USERPROFILE%\.cache\malachi\run\rpc.sock` v adresáři s DACL jen pro
uživatele a SYSTEM, `MALACHI_KEYRING=helper`), zastavuje ho
`CTRL_BREAK_EVENT` (Go ho bere jako přerušení), po 15 s kill; C# `RpcClient`
kontroluje u souboru s klíčem navíc vlastníka a DACL. Démon dostal jen
platformně neutrální opravy (`exec.LookPath` pro helper, zavírání surových
souborů před smazáním, `malachi-mcp --claude-desktop-config/--command`,
`.gitattributes` a přenositelné Go testy). Odchylky od GTK jen z tabulky
ve `windows/README.md` (hledání v titulkové liště, stavový pruh přes spodek
okna, skládání panelů při 900/600 px s tlačítky v titulkové liště, menu `…`
s Přidat účet a Konec, klávesy Windows s volbou `ctrl-r`, přístupové
klávesy místo mnemonik, pořadí tlačítek ContentDialog, bannery InfoBar,
kontextová menu, vlastní náhled příloh, Mark of the Web, Uložit vše bez
programů a zástupců, potvrzení nevypsaných odkazů, obnova WebView2 jednou na dokument, uložení konceptů
při Konci, tray, zvuk `MailBeep`, průvodce jako modální okno, Předvolby
jako okno s navigací a bez hledání, Výchozí aplikace, klíč Run, vlastník
a DACL souboru s klíčem); `.blp` jsou reference, nová funkce jde nejdřív
do backendu a GTK, pak do macOS a Windows. Před veřejným vydáním zbývá
(`docs/windows-port.md` §17): licenční výjimka GPLv3 §7 pro komponenty
Microsoftu (rozhodnutí vlastníka), podpis kódu, instalátor (Velopack,
winget), běh ARM64 na skutečném hardwaru, UI smoke testy na runneru CI
a ruční ověření kliknutí na notifikaci. Kontributorský popis `docs/windows-port.md`.

Pořadí prací:
1. ~~IMAP — čtení, synchronizace, offline store~~ hotovo
2. ~~SMTP a odesílání~~ hotovo (přílohy, outbox, kopie do Sent)
3. ~~Microsoft 365 přes Graph + GNOME Online Accounts~~ hotovo (místo
   XOAUTH2/IMAP; zdůvodnění v `docs/architecture.md` §7)
4. ~~Sanitizér HTML (compose i view) a renderování s webview~~ hotovo
   (vlastní sanitizér, `htmlWithheld`, `message.part`, stahování obrázků
   démonem, multipart/alternative)
5. ~~Threading~~ hotovo (backend i seskupený seznam v UI)
6. ~~Vyhledávání~~ hotovo (backend, GTK, MCP, macOS, Windows)
7. ~~Klient pro Windows~~ hotovo (WinUI 3, `windows/`; zbývá distribuce,
   `docs/windows-port.md` §17)
8. Účty Jira (`kind: jira`) — backend, macOS a GTK hotovo (čtení,
   komentáře, změna stavu, notifikační maily, zobrazení konverzace);
   Windows zbývá
   (reference `ui/internal/jira`, `ui/internal/capabilities`,
   `ui/internal/conversation`)

Gmail a Microsoft 365 mají dvě cesty. Na GNOME přednostně GNOME Online
Accounts (token i registrované klient ID drží GOA, proto žádný CASA audit;
`OAuth2Config{source: goa}` / `GraphConfig{source: goa}`). Jinak vlastní
přihlášení démona (`source: daemon`, `internal/auth/oauth2flow`:
authorization code + PKCE, jednorázový listener na `127.0.0.1`, prohlížeč
otevírá UI, refresh token jen v keyringu pod `oauth2.refresh_token`,
Gmail přes IMAP/SMTP XOAUTH2 připnutý na `imap.gmail.com`/`smtp.gmail.com`,
Microsoft přes Graph); metody `account.oauthStart/oauthWait/oauthCancel`,
`credentials.oauthSession`, `account.discover` `alternatives`, chyba
`oauthClientMissing`. Klienti: per-účet `clientId` > `config.toml`
`[oauth2.google] client_id/client_secret`, `[oauth2.microsoft]
client_id/tenant` > `oauth2flow.builtinClients`. Vestavěný je jen
Microsoft (registrace projektu v Entra, multitenant + osobní účty, veřejný
klient, bez publisher verification — firmy s omezeným souhlasem ho
schvalují přes správce); Google žádný (restricted scope = verification +
roční CASA), Gmail mimo GOA přes app password nebo vlastního klienta. Gmail s app password je nouzová cesta
(discover ji nabízí jako alternativu). Poskytovatel `custom` zůstává
`notImplemented`.

Certifikáty: IMAP/SMTP endpoint může připnout SHA-256 otisk listového
certifikátu (`ServerConfig.certificateSha256`, TOML `certificate_sha256`;
zakázáno se `security: none` a `authMethod: oauth2`, Graph ani HTTP
klienti pin nikdy nemají). S pinem `transport.EndpointTLSConfig` přijme
právě ten certifikát (jediné `InsecureSkipVerify` v backendu, porovnání
ve `VerifyConnection`). `tlsError` nese `error.data` (`api.TLSErrorData`:
důvod, vyčištěné údaje certifikátu, `expectedSha256` u `pinMismatch`).
Průvodce ve všech třech UI nabízí „Důvěřovat certifikátu…“ až po výslovném
potvrzení s otiskem (pravidla v `ui/internal/certtrust`, zrcadla
`MalachiCore/Wizard/CertTrust.swift` a `Malachi.Core/Wizard/CertTrust.cs`), změna hosta nebo portu pin zahodí,
stránka Servery ho ukáže se Zapomenout, účet s odmítnutým nebo změněným
certifikátem má stav a banner s „Upravit účet…“. Žádné obecné
„ignorovat certifikát“ (`docs/security.md` §7).

Spojení s démonem (protokol 2, `docs/api.md` §1.4): démon při každém startu
vygeneruje náhodný klíč a zapíše ho vedle socketu do `<socket>.key`
(`api.KeyPath`, výchozí `rpc.sock.key`, 0600; při čistém ukončení ho smaže,
po pádu zůstane do dalšího startu). Každé spojení začíná `system.hello`
(klient porovná `protocolVersion`, teprve pak přečte klíč a ověří důkaz
démona, HMAC-SHA256 přes dvě nonce) a `system.authenticate` s důkazem
klienta; do té doby démon nespustí kód backendu, nepošle notifikaci a na
jiný požadavek odpoví 1005 `unauthenticated` a spojení zavře (4 KiB, 10 s,
nejvýš 32 takových spojení). Go klienti (GTK UI, MCP most) volají
`api.ClientHandshake`, Swift `RPCClient` dělá totéž a navíc kontroluje
vlastníka a práva souboru s klíčem, C# `RpcClient` také a na Windows místo
práv DACL (`WindowsKeyFilePolicy`). Co to chrání a co ne:
`docs/security.md` §8.

Úložiště pošty (migrace 0014, `docs/architecture.md` §3.1, §3.2, §7):
surová zpráva je `messages/<účet>/<id>` (jak přišla) nebo `<id>.zst` (jeden
zstd rámec přes `klauspost/compress` s velikostí a checksumem, ověřený před
přejmenováním); jak se soubor čte, určuje jméno, nikdy obsah. Soubory jen
přes `store.OpenMessageRaw`/`PutMessageRaw`/`WithMessageRaw` (zámek na
zprávu), účetnictví v `message_files`, příjem přes `staging/`, outbox vždy
prostý a fsyncnutý. Preference `compressStore` a `attachmentOfflineDays`
(0 vše, N dní, -1 jen malé; v `config.set` chybějící = beze změny, bez klíče
v `config.toml`), výchozí za běhu `MALACHI_DEFAULT_COMPRESS_STORE` /
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS` (nastavuje jen macOS supervisor,
1 a 30; `StartSync` je při prvním použití uloží). `internal/ingest`
rozhodne každou zprávu hned při stažení (stage → parse → `Decide` →
`mime.Skeleton` → `VerifySkeleton` → `CommitMessageRaw`), takže první
synchronizace disk nezaplní: podle `attachmentOfflineDays` (stáří podle
`internal_date`) zůstanou na serveru přílohy ≥ 100 KiB, které HTML
neukazuje přes `cid:`, nikdy však u Konceptů, Outboxu, zpráv bez kopie na
serveru, podepsaných či šifrovaných a 7 dní po stažení na vyžádání; při
pochybnosti celá zpráva. `Attachment.remote` se jen odvozuje z
`remote_parts`, `message.part`/`message.embedded` vrací 1504
`partNotDownloaded`, `message.download` stáhne celou zprávu samostatným
spojením jen pro čtení (IMAP EXAMINE + `BODY.PEEK[]`, Graph `$value`; 1305
`messageGone`). Údržba v `core.Maintain` (`core/raw_maintenance.go`):
hodinový úklid souborů, kroky `codec` (převod oběma směry) a `attachments`
(ořez stárnoucí pošty bez sítě) s kurzory v `meta` `raw.step.*`; uvolnění
nastavení nic zpětně nestahuje; `system.storage` hlásí obsazené místo a
stav převodu. Všechna tři UI: Předvolby → Obecné → Pošta (*Keep Attachments
Offline For*, *Compress Stored Mail*, *Disk Space Used*), čip vzdálené
přílohy stáhne zprávu před otevřením, uložením i přeposláním; MCP
`get_attachment` a přeposlání v `create_draft` stahují z vlastního serveru
uživatele (2 min, 256 MiB na proces). Preference `neverStoreAttachments`
(`attachments.never_store`, výchozí vypnuto, bez výchozí hodnoty
z prostředí) přebíjí `attachmentOfflineDays` a neuloží žádnou přílohu
ani obrázek z HTML od 100 KiB (menší obrázky přes `cid:` zůstávají;
`message.body` `remotePictures` → pruh *Download Pictures*; výjimky výše
zůstávají celé, `strippable_bytes` -2 = nikdy neořezávat, denní krok
`3:never:<datum>` (verze pravidla `ingest.NeverStoreRule`) ořízne i dříve
stažené), příjem staguje v paměti (`store.StageMemory`) a
`message.download` drží celou zprávu jen v paměťové cache démona
(`core/memcache.go`: LRU 256 MiB, 30 min nečinnosti, zahozená při
ukončení, vypnutí režimu a pozastavení či odebrání účtu), ze které
`message.part`/`message.embedded`/`draft.create`/`draft.open` obslouží
vzdálené části, takže stažená zpráva na disk nejde; všechna tři UI mají
přepínač *Never Store Attachments* a adresář pro otevření a náhled mažou
při každém startu i ukončení. Na Windows nový úložný kód přejmenování
přes otevřený soubor a mazání otevřeného souboru řeší přenositelně:
zavřít před náhradou či smazáním, `fsretry` pod zámkem jmen zprávy
a soubor držený čtenářem déle je `store.ErrBusy` (převod a ořez se k němu
vrátí), žádný platformní kód (`docs/windows-port.md` §14).

Účty Jira (`kind: jira`, migrace 0015, `docs/architecture.md` §3.6 a §7,
`docs/api.md` §4.1): issue tracker čtený jako pošta — Jira Cloud (REST v3,
ADF) i Data Center (REST v2, wiki markup), `internal/jira` vedle
`internal/imap` a `internal/graph`, v `core/dispatch.go` tabulka
supervisorů podle `kind`. Vzdálená vrstva (`client.go`, `remote.go`,
`cloud.go`, `datacenter.go`, `types.go`: rozhraní `Remote`, nejvýš 4
souběžné požadavky na účet, Retry-After, redirecty jen na https a nikdy
s tokenem na jiný host, JSON do 32 MiB, každý řetězec ze site vyčištěný
a oříznutý); syncer (`supervisor.go`, `sync.go`: jeden syncer na účet,
průchod každou minutu, inkrementální JQL `updated >= "-Nm"` s rezervou
5 min, hodinová rekonciliace id ve scope pro smazané, přesunuté a
sledované issues, `reconcile.go`, retence podle okna účtu). Každá položka
issue — popis, komentář, změna stavu či řešitele (`events.go`) — je jedna
syntetizovaná zpráva RFC 5322 (`synth.go`: deterministicky, při každém
sestavení tytéž bajty; adresy `<id>@users.jira.invalid`, Message-ID
`issue.<id>@<host>.malachi.invalid`, předmět `KEY: Summary` na každém
řádku; obrázky ze site jako `cid:` části, relativní odkazy absolutní,
`images.go`) uložená přes `internal/ingest` jako pošta; `thread_id =
jira:<issueId>`, `remote_id` `i:`/`c:`/`h:`; tabulky `issues`,
`issue_items`, `issue_spaces` (`store/issues.go`). Složky = vybrané spaces
(`space:<id>`) a pevné pohledy `assignedToMe`/`watching`/`open`
(`Folder.virtual`, kopie řádků se stejným `remote_id` a Message-ID,
`folders.go`; `open` podle `closedStatuses`, jinak kategorie done);
příznaky jen lokální a na všechny kopie (`ops.go`); move/delete účet nemá
(`Account.capabilities`: jira `["comment","forward","transition"]`, mail
`api.MailCapabilities`, nil od staršího démona = mail; `message.move`/
`delete` bez capability = `invalidArgument`). Bot cleaner
`internal/jira/botclean`: komentáře, které přeposílá integrace jako „Issue
Sync – Synchronization for Jira“, dostanou autora a čas z hlavičky `KEY
Autor added comment - datum` (`via`), řádky podle `metadataFilters` (RE2)
se odstraní; je to čistič, ne bezpečnostní hranice — sanitizér běží při
zobrazení. `render_key` (pravidla botů + `hideEvents` + `synthVersion`)
přestaví uložené řádky na místě pod týmiž id a průchod pak pošle
`notify.messagesChanged`. `message.download` zprávu sestaví ze site znovu
(`fetch.go`, `ErrGone` → `messageGone`). Fake site pro testy
`internal/jira/jiratest` (Cloud i DC, žádná síť), patologická data
`backend/testdata/jira`. Přihlášení: Cloud e-mail + API token (Basic; při
401 a známém `cloudId` přes bránu `api.atlassian.com/ex/jira/<cloudId>`,
kterou vyžadují scoped tokeny), DC personal access token (Bearer); token
je `credentials.password` v keyringu (`auth.KeyPassword`) a jde jen na
site, pro který byl uložen; žádné OAuth (Atlassian nedovoluje client
secret v open source, PKCE nepodporuje a všichni uživatelé jedné aplikace
sdílejí její limit; §7). Průvodce: `account.detectSite` (anonymně
serverInfo + tenant_info, klient 15 s) a `account.listSpaces` (přihlášení,
spaces, statusy, uživatel, odhad počtu issues, 45 s; s `accountId` uložený
token). Per-účet `JiraConfig.offlineDays` (0 = 30, max 365; preference
`offlineDays` neplatí), otevřené issues přiřazené mně bez ohledu na stáří
(do 500), `onlyMine`, `hideEvents`, `disabledFolders`; první backfill:
položky mladší 3 dnů nepřečtené, starší přečtené; události vždy přečtené
a nikdy nenotifikované. Unikátnost účtu je (email, realm): mail účty realm
"", jira normalizovaný host[:port]+path site (`store.RealmOf`), takže jira
účet smí mít adresu schránky. Komentáře (`core/comments.go`):
`draft.create reply` na jira účtu = koncept komentáře (`Draft.comment`,
`visibility` `public`/`internal`, interní jen u service-desk issue),
lokální (žádná složka Koncepty, `draft_sync` se neozbrojí); `message.send`
ho zařadí do outboxu jako MIME a `jira.Supervisor.Deliver` pošle v ADF
(Cloud, `adf.go`) nebo wiki (DC, `wiki.go`) ze sanitizovaného HTML
(`comment.go`, limit 32 767 znaků) s entity property
`io.github.schotek.malachi.outbox` = id outbox zprávy (idempotence po
ztracené odpovědi) a `sd.public.comment` u interních, pak refresh issue
(čeká ≤ 30 s). Přeposlání jira zprávy = obyčejný e-mail z mail účtu
(`DraftCreateParams.messageAccountId`, kopie částí do úložiště příloh mail
účtu). Notifikační maily site v mail účtech (`core/issue_mail.go`,
`jira/notification.go`, `docs/security.md` §4.1): `JiraConfig.notificationMail`
`sync` (výchozí: hook `Stored` po uložení těla → `MatchNotification` podle
From (`notificationSenders`, výchozí `@<host site>` na Cloudu, na DC nic) a
klíče v předmětu → link v `issue_mail_links` + refresh issue, u čerstvé
zprávy počká ≤ 5 s před `notify.newMessage`), `hide` (navíc
`messages.hidden`, jen když issue je v účtu uložené; display filtr, na
serveru nic; zpět při změně nastavení, pozastavení, odebrání; hodinové
přehodnocení v `core.Maintain`, po `account.update` prohlédne i starší
poštu), `ignore`; každá změna skrytí = `notify.messagesChanged` (koalescence
250 ms). MCP most: `list_accounts` vrací `capabilities`, `list_messages`/
`search_messages`/`read_message` `issue` (key, status, item), `create_draft`
`mode: reply` na jira účtu = koncept komentáře (`visibility`), ostatní
režimy odmítá, přeposlání z mail účtu přes `messageAccountId`,
`list_transitions` a za `--allow-modify` `transition_issue`
(`docs/mcp.md`). Změna stavu (`docs/api.md` §4.12, `core/issue_transitions.go`,
`jira/transitions.go`): `issue.transitions` vypíše přechody, které site
uživateli na issue dovolí (`GET …/transitions?expand=transitions.fields`,
nejvýš 100), `issue.transition` jeden provede a issue hned obnoví (čeká
≤ 30 s), takže řádek události a nový stav dorazí obvyklou cestou; přechod
s obrazovkou nebo povinným polem je `needsInput` a démon ho odmítne ještě
před POSTem (`invalidArgument`), 401 = `authFailed`, 404 = `messageGone`,
odmítnutí site = `serverError` s její vyčištěnou zprávou; řešitel se
nemění. UI: macOS první (`macos/`): průvodce jako sheet
(`AccountWizard/Jira`, `JiraWizardController`), sidebar s kapslí JIRA
a pohledy nad spaces (`FolderTree.swift`), seznam jira složky vždy
seskupený (`MailModel+Jira.swift`) s pilulkou stavu a řádky událostí,
karta issue nad hlavičkou (`IssueCardView`, `IssueReading.swift`; událost
bez těla), akce podle capabilities (`Model/Capabilities.swift`,
`ActionRules.swift`, `MainWindow/ActionPresentation.swift`: Reply →
Comment, Forward přes mail účet), okno komentáře (`Compose/CommentHeaderView`,
`ComposeWindowController+Comment`), pilulka stavu v kartě issue jako
nabídka přechodů a „Změnit stav“ v menu Zpráva a Další akce
(`Shared/IssueStatusPill`, `IssueTransitionMenu`, `App/ChangeStatusMenus`,
`IssueActionsController`; přechody `needsInput` neaktivní s vysvětlením,
přechod do stavu, který issue už má, se nenabízí —
`ui/internal/jira/transitions.go`), nastavení účtu jako sheet
(`Preferences/JiraAccount`, `JiraAccountController`: spaces, okno, pohledy
a uzavřené stavy, notifikační maily, boti s nabídkou „Issue Sync“;
`JiraPattern.swift` kontroluje RE2 podle `regexp/syntax`),
`notify.messagesChanged` → `MailboxController.handleMessagesChanged`.
Čistá logika nejdřív jako reference v Go (`ui/internal/jira` — texty,
karta, události, průvodce, compose, nastavení; `ui/internal/capabilities`;
testované na Macu), portovaná 1:1 do `MalachiCore/Jira`; Windows UI
zbývá (`windows/parity-exclusions.txt` „Jira account: macOS first“;
funkce označené „Swift-first“ a kde je GTK zrcadlí: tabulka
v `macos/README.md`). GTK (2026-09-30) používá referenční balíčky tak,
jak jsou, přes `i18n.Tr` (adaptér `jira.Translator` nad `i18n.T/N/C`)
a nepřidalo žádný msgid: sidebar (`model.go` `accountLabel`,
`sortSiblings` s `VirtualRank`, `folderIcon`, `accountHeaderBadge`;
`folders.go` kapsle druhu za jménem každého účtu — JIRA, u pošty
poskytovatel `GOOGLE`/`M365` podle `signin.Provider`, jinak `IMAP`; GTK
první, macOS a Windows ukazují zatím jen JIRA —, názvy pohledů), seznam
(`groupedListing` = nastavení nebo `alwaysGrouped`, `groupingChanged`
nechá jira složku být, `countsUnread`, `widget/message_row.go` klíč,
pilulky `widget/pill.go` a řádky událostí; CSS pilulek v
`internal/style`), karta issue nad hlavičkou v panelu i okně zprávy
(`issue_reading.go`, `issue_card.go`: klíč otevře jen URL vlastního
site, pilulka stavu je `MenuButton` s popoverem přechodů načteným při
otevření; `issue_actions.go` = port `IssueActionsController` s testy;
„Změnit stav“ v menu Další akce `win.change-status` / `msg.change-status`,
skryté, když účet stavy nemění), akce podle capabilities
(`action_rules.go` + `actions.go`: nepodporované akce zmizí z lišty,
Odpovědět → Komentovat s ikonou `chat-message-new-symbolic`; `compose_open.go`
`openComment` bez náhradního předvyplnění, přeposlání přes
`capabilities.ForwardFrom` s `messageAccountId`), režim komentáře okna
Nová zpráva (`compose/comment.go`, větve v `draft.go`, `Manager.Accounts`
= `ComposeAccounts`, `CanComposeNew` řídí `app.compose` v `main.go`),
průvodce (`accountwizard/jira_flow.go` čistý tok s testy proti falešnému
démonovi, `jira.go` + `jira_wizard.blp`; *Přidat Jira účet…* v nabídce
„+“ Předvoleb → Účty a na prázdném okně `app.add-jira-account`),
nastavení účtu (`ui/internal/jiraaccount`: `Controller` s testy,
`Dialog` + `jira_account.blp`, `TokenReplaced` po novém tokenu), cesty
„Upravit účet“ (`accounts_page.go` `accountEditor`/`jiraEditor`,
`jira_editors.go`: banner přihlášení s důvodem → průvodce na tokenu,
jinak nastavení; nikdy poštovní průvodce), `notify.messagesChanged`
(`notify.go` `handleMessagesChanged`, `evictAccount`, `refreshShown`,
`forgetMembers`), texty notifikací (`issueNotificationLine`) a banneru
(`accountAuthBannerTitle`). Zobrazení konverzace (macOS a GTK, reference
`ui/internal/conversation`, pro Windows „Conversation view: macOS
first“): výběr sbaleného řádku vlákna (≥ 2 členů ve složce; jira složky
vždy) ukáže v panelu čtení celé vlákno jako nativní karty od nejstarší
s časovou osou v levém okraji, u jira kartou issue nahoře a událostmi jako
kompaktní řádky, přečtený se označí jen nejnovější člen, který není
událost; každá HTML karta má vlastní uzamčený WKWebView v režimu `sized`
(výšku hlásí skript aplikace ve vlastním světě, JS obsahu vypnutý, strop
4000 pt, nejvýš 8 živých pohledů; `ConversationLayout.swift`,
`ConversationViewController.swift`), nikdy jeden složený dokument — CSS
jedné zprávy by přepsalo hlavičky ostatních. GTK: `window/conversation_*.go`
(jako issue v Jiře: nahoře karta issue, pod ní úvodní zpráva — popis
issue, u pošty nejstarší neořezaná zpráva — sbalená na hlavičku
a náhled, dokud následuje jiná zpráva, pak ostatní od nejnovější,
`convDisplayOrder`, otevřené nahoře — rozhodnutí uživatele 2026-09-30,
model i macOS zůstávají od nejstarší; řádek starších zpráv dole; controller
a layout čisté a testované, stránka `conversation_view.blp`
přidaná do `message_stack` při prvním použití, karty znovu používají
čipy, adresy a lišty panelu přes vlastní `messageView`, mezerník
a Shift+mezerník v seznamu listují), karta `htmlview/card.go`
+ `html_card.blp`: WebKitGTK neumí vypnout jen JS obsahu, proto
`enable-javascript` zapnutý se `enable-javascript-markup` vypnutým,
jediný skript aplikace ve světě `malachi-size` s handlerem `size`
registrovaným jen tam (`size.go`, Go bere jen ověřené číslo), strop
4000 px a zmrazení výšky (`webHeightGovernor`); samostatný pohled zprávy
má JS dál vypnutý úplně (`docs/security.md` §3.2). `MALACHI_DATA_DIR` přebíjí
datový adresář i na macOS (`Daemon/Paths.swift`, jako na Windows).

Rozhodnutí i otevřené otázky: viz `docs/architecture.md` §7 (mimo jiné
jazyk UI, sanitizační knihovna, definice účtů, uložení těl zpráv včetně
komprese a příloh na vyžádání, Microsoft účty).

## Čeho si být vědom

- `gotk4` je generovaný binding; v některých částech API se vyskytují
  memory leaky a pády. Při podivném chování zvaž, že chyba nemusí být v našem kódu.
- První kompilace `gotk4` trvá desítky minut. Není to zamrznutí.
- Toolbx sdílí domovský adresář s hostitelem (včetně `~/go` a build cache).
- `XDG_RUNTIME_DIR` nemusí být v kontejneru nastavený; backend i UI pak
  používají `~/.cache/malachi/run/rpc.sock`. `MALACHI_SOCKET` přebíjí obojí.
  Ve Flatpaku (`FLATPAK_ID`) leží socket v `$XDG_RUNTIME_DIR/app/<app-id>/`,
  jediné části runtime dir sdílené mezi instancemi sandboxu (`api.SocketBase`).
- Vedle socketu leží klíč spojení `<socket>.key` (`rpc.sock.key`, 0600): démon
  ho při každém startu zapíše nový a při čistém ukončení smaže (po pádu zůstane
  do dalšího startu), klienti ho čtou při každém připojení znovu, až po odpovědi
  na `system.hello`; nikdy se neloguje. Se starým démonem (protokol 1) ukáže UI
  stálé „Protocol mismatch: UI 2, backend 1“ (ukonči ho, UI pak spustí nový).
  Ruční komunikace se socketem (`socat`, skript) vyžaduje handshake
  z `docs/api.md` §1.4, jinak přijde 1005 `unauthenticated` a zavřené spojení;
  pouhé připojení a zavření je v pořádku.
- Démon drží výhradní zámek storu `<store>.daemon.lock` (`store.Lock`: EXCLUSIVE
  transakce SQLite, kterou systém uvolní s procesem i po pádu); druhý démon
  nad stejným storem skončí chybou „another malachid is using the store“ ještě
  dřív, než sáhne na socket (třeba `make run-backend` vedle démona z UI).
  Na Linuxu a macOS tenhle soubor nic jiného v démonu nesmí otevřít: zavření
  jakéhokoli deskriptoru souboru pustí zámky procesu, proto ho
  `attachment.import` odmítá (`store.IsLockFile`).
- Démona nespouští nic na desktopu: UI si ho spustí samo (`ui/internal/daemon`,
  hledá `malachid` vedle vlastní binárky, `MALACHI_DAEMON=none` vypne) a při
  ukončení aplikace ho zastaví. Běžícího démona (`make run-backend`) použije
  a nechá být.
- `make build` musí proběhnout před `scripts/dev-run.sh`; skript binárky nestaví.
  `make run-dev` / `run-backend` / `run-frontend` build zajistí samy.
- Úprava Go souboru z `po/POTFILES`, která posune řádky s texty, posune i odkazy
  `#: soubor:řádek` v `po/malachi.pot` a `make lint` selže („po/malachi.pot is
  out of date“), dokud neproběhne `make po` (v Toolbxu). Kde to nejde, nech
  řádky s texty na stejných číslech (nový kód pod poslední text nebo do nového
  souboru bez textů).
- Nový msgid v `po/malachi.pot` (práce na GTK a `make po`) musí Windows klient
  použít, nebo ho zapsat s důvodem do `windows/parity-exclusions.txt`: test
  pokrytí (`StringsCheckTests.EveryTemplateMsgidIsUsedOrExcluded`) ho jinak
  hlásí jako chybu (`CoverageEnforced` je zapnuté). Zrušený msgid, který v exclusions zůstal, a msgid z exclusions, který
  klient začal používat, shodí `build.ps1 lint` vždy. msgid použitý ve
  `windows/src` musí v šabloně být (s kontextem i plurálem).
- Msgidy Jira účtů a zobrazení konverzace (`ui/internal/jira/*.go`,
  `ui/internal/conversation/conversation.go`, v `po/POTFILES` za
  `ui/internal/compose/suggest.go`) vznikly s macOS klientem před GTK
  widgety; od GTK portu je `make po` přečísloval a GTK je používá přes
  `i18n.Tr`. Windows je má v `windows/parity-exclusions.txt` („Jira
  account: macOS first“, „Conversation view: macOS first“); s portem se
  odtud mažou.
- Jira testuj proti kopii, ne nad ostrým storem: migrace 0015 přestaví
  tabulku `accounts` a je jako každá migrace nevratná, takže by ostrý
  store změnila dřív, než je větev v `main`. Na macOS
  `MALACHI_DATA_DIR=<kopie adresáře Application Support> MALACHI_SOCKET=<vlastní
  rpc.sock>` pro app i démona (`Daemon/Paths.swift`, stejně jako Windows
  agent v `%TEMP%`), `malachi-mcp` čte totéž `MALACHI_SOCKET`; vlastní
  socket je nutný, jinak app převezme démona nad ostrým storem. Na
  Linuxu kopie `~/.local/share/malachi` (store i `messages/`, démon
  zastavený) a `config.toml`, démon ručně `./build/malachid --config
  <kopie>/config.toml --store <kopie>/store.db --socket
  $XDG_RUNTIME_DIR/malachi-test.sock` a UI s `MALACHI_SOCKET` na týž
  socket a `MALACHI_DAEMON=none` (`make run-frontend`); XDG proměnné
  UI neměň, přesměrovaly by i dconf s předvolbami. Komentáře
  na produkční Jiře jen do issue, které uživatel sám určí; Data Center
  není k dispozici a ověřuje se jen fakem `internal/jira/jiratest`.
- Windows: XAML kompilátor je nástroj .NET Frameworku bez podpory dlouhých
  cest a na cestě přes 260 znaků padá (`MSB3073`, `XamlCompiler.exe`,
  `MSB3106`), i se zapnutými dlouhými cestami ve Windows. Klon drž na krátké
  cestě (`D:\src\malachi`) a NuGet cache ve výchozím `%USERPROFILE%\.nuget`.
- Windows: Claude Desktop je balíček MSIX a každý proces, který spustí (i agent
  a to, co agent spustí), vidí virtualizovaný AppData i HKCU: nové soubory
  pod `%APPDATA%`/`%LOCALAPPDATA%` a zápisy do HKCU (nastavení, Run,
  `mailto:`, registrace notifikací) skončí v úložišti balíčku Claude, jinde
  neviditelné. Agent proto pouští app s `MALACHI_DATA_DIR` a krátkým
  `MALACHI_SOCKET` v `%TEMP%` (taková kopie nechá uživatelovy registrace
  `mailto:` a Run být) a s vlastním `MALACHI_SETTINGS_KEY`
  (`io.github.schotek.Malachi.<přípona>` pod `HKCU\Software`, po běhu
  smazat), aby nesahal na uživatelovy předvolby; co musí dojít do
  skutečného registru, spouští mimo strom Claude (WMI
  `Win32_Process.Create`) a po sobě uklidí.
- Windows: cesta AF_UNIX socketu má nejvýš 107 bajtů UTF-8 (macOS 103); app ji
  ověří při startu a zprávou jmenuje `MALACHI_SOCKET`. Soubor s klíčem na
  Windows dědí ACL adresáře: `MALACHI_SOCKET` v adresáři, kam smějí jiní
  (`D:\…` mimo profil), skončí „Backend unavailable“ s důvodem v logu.
- Windows: každý `dotnet` příkaz běží z `windows\` (tam `global.json` vybírá
  SDK a Microsoft.Testing.Platform; jinde `dotnet test` spadne na VSTest).
  Varování jsou chyby, `build.ps1 lint` je `dotnet format --verify-no-changes`
  plus konvenční testy.
