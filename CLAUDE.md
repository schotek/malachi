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
patologické případy (do `backend/testdata/mime`, dokumenty příloh do
`backend/testdata/documents`), ne jen šťastnou cestu.
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

### 6. Změna UI jde do všech tří klientů současně
Každá změna nebo oprava v UI (nová funkce, oprava chování, úprava vzhledu)
se dělá v jedné práci v GTK, macOS i Windows klientovi, ne jen v jednom
z nich s tím, že porty přijdou „někdy“. Pořadí zůstává: nejdřív čistá
logika v Go (`ui/internal/…`) a GTK jako reference, hned potom port do
`MalachiCore`/`MalachiMail` a `Malachi.Core`/`Malachi.App` i s testy.
Klient, který na tomto stroji nejde sestavit (Swift a C# na Linuxu,
GTK na Windows), se napíše podle okolního kódu a v předávce se uvede, co
zbývá sestavit a ověřit. Když zadání neříká, kterých klientů se změna
týká, zeptej se uživatele (v Claude Code přes `AskUserQuestion`), jestli
ji dělat jen v jednom, nebo ve všech třech. Výjimky jsou jen odchylky
z tabulek v `macos/README.md` a `windows/README.md` a věci, které na
platformě nedávají smysl (třeba Claude Desktop na Linuxu). Příklad:
kolečko čekání v panelu asistenta (2026-09-30) přišlo do všech tří
klientů naráz (`Controller.Waiting` → `waiting` → `IsWaiting`).

## Konvence

- Go: standardní formátování, `golangci-lint`, errors wrapované s kontextem
- Struktura balíčků: `internal/` pro implementaci, `pkg/api/` pro veřejný kontrakt
- MCP most (`backend/cmd/malachi-mcp`) je klient démona jako UI: z `backend/`
  importuje jen `pkg/api` a vlastní `cmd/malachi-mcp/internal/extract`, který
  čte text PDF, DOCX a XLSX jen v podřízeném procesu sebe sama (`__extract`),
  žádné volání nepošle před dokončeným handshakem
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
  komerční licence jádra nebyla udělitelná. `THIRD-PARTY-NOTICES.md` nese
  oznámení třetích stran `malachi-mcp` (jeho Go moduly a komponenty
  `pdfium.wasm`) a jde do každého balíčku; nový modul v mostu nebo povýšení
  go-pdfium = aktualizovat ho (`docs/releasing.md` §9). Na macOS má jen
  `malachi-mcp` oprávnění `allow-unsigned-executable-memory` (kompilátor
  wazero), aplikace ani ostatní binárky ne

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
(+ `related` pro vložené obrázky, + `mixed` pro přílohy). Vložený prostý
text, který vypadá jako Markdown (bez bohatého HTML ve schránce), převede
démon (`draft.markdown`, `internal/markdown` nad goldmarkem, výstup
sanitizovaný v compose režimu) a editor ho vloží jako HTML, ve všech
třech UI ve zprávě i v komentáři Jira (bridge `paste`/`pasted`). Odpověď a
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
obsah pošty v ohradě s nonce; `get_attachment` vrací i text PDF, DOCX
a XLSX, čtený v podřízeném procesu mostu (PDF přes PDFium ve WebAssembly),
s cache v paměti (`docs/mcp.md`, Documents); podpříkazy `status`/`install`/`uninstall
--json` zapisují registraci do konfigurace Claude Desktop a Claude Code a
Předvolby → AI → MCP je ve všech třech UI jen přepínač nad nimi (GTK
`ui/internal/mcpsetup`, macOS `MCPRegistrationController`, Windows
`McpRegistrationController`, který předá `--command` a u MSIX Claude Desktop
`--claude-desktop-config`); viz `docs/mcp.md`). Menu Asistent (macOS,
GTK `ui/internal/window/assistant.go` a Windows; čistá logika a texty
v `ui/internal/assistant`, portovaná do `MalachiCore` a `Malachi.Core`,
klíče gschema `assistant-menu` a `assistant-target`) předá vybranou poštu do Claude Desktop
nebo Claude Code odkazem `claude://` / `claude-cli://` s předvyplněným,
neodeslaným dotazem, který nese jen ID; existuje jen se zapnutým
přepínačem Registrovat v Claude (`assistant.Shown`); poštu Claude čte přes most,
přílohu dostane jako soubor (Cowork, pracovní adresář Claude Code); třetí
cíl „V aplikaci (experimentální)“ spouští v panelu hlavního okna uživatelův
`claude -p` (stream-json, proces na rozhovor) jen s nástroji mostu pro
čtení a koncepty, bez jeho nastavení, pluginů a ukládání relací, se
souhlasem při prvním použití (klíče `assistant-model`,
`assistant-claude-path`, `assistant-consent`); démon
ani most se kvůli tomu nemění (`docs/mcp.md`, Hand-off). macOS klient (`macos/`, Swift/AppKit, SwiftPM tools 6.0, macOS 14+,
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
hlavní nabídky (je v menu baru), bannery jako karty se symbolem, seznam se stránkuje sám, filtr v toolbaru jako v Mailu, hledací pole v toolbaru s pruhem rozsahu, bez hledání sbalené na lupu, Settings bez hledání, ⌥⌘↑/↓, volba ⌘R, pořadí tlačítek NSAlert,
quarantine na přílohách, zvuk Glass); `.blp` jsou reference, nová
funkce jde nejdřív do backendu a GTK, pak sem. Ad-hoc podpis: po každém
rebuildu, který změní binárku, se Keychain zeptá jednou za každou položku
(`make macos SIGN='…'` to řeší); binárky mají pevný identifikátor
`io.github.schotek.Malachi.<binárka>`. Distribuce: `make macos-dmg`
(universal arm64 + x86_64, Go spojené `lipo`, `ARCHS=` jen tento Mac)
a `make macos-notarize`; s Developer ID hardened runtime, timestamp
a `Resources/MalachiMail.entitlements` (Apple events kvůli restartu
Claude Desktop); CI `.github/workflows/macos.yml` na `macos-26` (Xcode 26
kvůli SDK pro Liquid Glass) testuje, staví DMG, s pěti secrets
`MACOS_*` podepisuje a notarizuje, k release tagu připojí jen
notarizovaný DMG (`docs/releasing.md` §8; členství v Apple Developer
Program a secrets zatím chybí, do té doby ad hoc).
Kontributorský popis `docs/macos-port.md`. `make macos` / `macos-dmg` /
`macos-notarize` / `run-macos` / `test-macos` jsou jen na Darwinu.

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
oblasti při běhu na pozadí, spuštění po přihlášení (klíč Run), účty Jira
a zobrazení konverzace (od 2026-09-30, viz odstavec o Jira níže), čeština.
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
na Microsoft.Testing.Platform, ~6 000 (Core s FakeDaemon a MailFixture,
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
8. ~~Asistent (Claude)~~ hotovo v macOS, GTK (jen Claude Code, v terminálu
   nebo v aplikaci) i Windows: úroveň A, panel B1 a B2 (viz níže); cíl
   „V aplikaci“ experimentální do potvrzení podmínek Anthropicu
9. Účty Jira (`kind: jira`) — backend, macOS, GTK i Windows hotovo
   (čtení, komentáře, změna stavu, notifikační maily, zobrazení
   konverzace; Windows na `feat/jira-windows`, zbývá průchod vlastníka
   proti skutečnému Jira Cloud)
10. Nástěnka (případy, pravidla, triage asistentem) — backend, API, most
    MCP, macOS a GTK klient napsané na `feat/board`; Go model je v
    `ui/internal/board`, běh triage v `boardtriage` a návrhy odpovědí v
    `boardreply`. Zbývá Windows a ruční ověření GTK včetně skutečného
    běhu triage a návrhu odpovědi s Claude Code

Asistent (stav 2026-09-30, sloučeno do `main`; uživatel potvrdil, že
funguje ve všech třech klientech). Na macOS je hotové a uživatelem otestované:
úroveň A (menu ✦ Asistent v toolbaru a menu Zpráva, položka „Zeptat se
asistenta…“ na čipu přílohy, předání do Claude Desktop `claude://` a
Claude Code `claude-cli://`, skupina Asistent v Předvolbách → AI,
existuje jen se zapnutým „Registrovat v Claude“, bez náhradního cíle,
nabídka restartu Claude Desktop, který za běhu přepisuje svou
konfiguraci) a B1 (třetí cíl „V aplikaci (experimentální)“: panel vpravo
v hlavním okně nad `claude -p` se stream-json, souhlas při prvním dotazu,
✦ v liště hlavního okna je s tímto cílem bez nabídky a panel jen otevře
a dá fokus poli dotazu (`assistant.ButtonOpensPanel`; okno zprávy nabídku
nechává, platí ve všech třech klientech), rychlé akce
(`assistant.PanelActions`, včetně Shrnout nepřečtené ve složce vybrané
v postranním panelu), odpověď jako podmnožina Markdownu bez HTML, karta
„Otevřít koncept“, rozhovor drží kontext s lištou „Vybrali jste jinou
zprávu“) a B2 (jen s cílem „V aplikaci“: tlačítko ✦ v okně Nová zpráva
upraví výběr, jinak vlastní text nad hlavičkou citace — Zdvořileji,
Stručněji, Opravit chyby, Přeložit do angličtiny, vlastní pokyn; náhled
a Nahradit / Vložit pod / Zahodit jedním krokem zpět; „Hledat vlastními
slovy“ v nabídce lupy pole hledání a ⌥↩ převede napsaná slova na dotaz
v syntaxi hledání přes `--json-schema`; obě žádosti jsou jednorázové
a bez nástrojů). Referencí pro port je čistý Go balíček `ui/internal/assistant`
(texty, dotazy, příkazová řádka, události, Markdown, pravidla dostupnosti;
testovaný), chování UI popisuje `docs/mcp.md` (Hand-off, The panel in the
app) a macOS: `MalachiCore/Assistant/`, `Controllers/AssistantController`,
`ClaudeDesktopController`, `AssistantPanelController`,
`Platform/ClaudeCodeLocator`, `ClaudeCodeProcess`, `Controllers/AssistantRequest`,
`ComposeRewriteController`, `SearchConversion`, `MalachiMail/Assistant/`,
`Preferences/AIPaneViewController`; editor bridge má dva doplňky
(`rewriteTarget`, `rewriteApply`) a okno Nová zpráva si pamatuje svou
hlavičku citace (`ComposeParams.attribution`). GTK: úroveň A a panel B1
jsou hotové a uživatelem otestované (`ui/internal/window/assistant.go`: stav `Assistant` pro celou aplikaci
nad `mcpsetup` a výchozím handlerem schématu z GIO, `MenuButton`
`assistant_button` vedle `message_menu` v `window.blp` i
`message_window.blp` s akcemi `win.assistant`/`msg.assistant`,
`win.assistant-unread`, `app.assistant-target`/`-setup`/`-problem`,
položka `att.ask` v menu čipu přílohy, skupina Asistent na `ai_page`,
ikona `malachi-assistant-symbolic` v `ui/data/icons`) i panel B1
(`ui/internal/assistantpanel`: `Controller` jako port
`AssistantPanelController`, `Process`, `Locator`, testy proti falešnému
`claude` portované ze Swiftu; `window/assistant_panel.go` +
`assistant_panel.blp` v `Adw.OverlaySplitView` `assistant_split` na
konci hlavního okna s přepínačem `assistant_panel_button`, odpovědi jako
`GtkTextView` se značkami z `assistant.Markdown`, odkazy vždy přes
„Otevřít tento odkaz?“; Předvolby → AI řádky Claude Code a Model; pracovní
adresář `~/.cache/malachi/assistant`). Na Linuxu jen Claude Code:
v terminálu, který vybere jeho handler `claude-cli://` (`$TERMINAL`,
`x-terminal-emulator`, běžné emulátory), nebo v panelu; Claude Desktop pro
Linux je preview, které nepodporujeme: menu i Předvolby ho ukazují
zašedlé (`supportedTarget`), uložené `desktop` se čte jako Claude Code,
výchozí hodnota gschema zůstává referenční (`desktop`), proto žádná
nabídka restartu Claude Desktop (který i na Linuxu za běhu přepisuje
konfiguraci, „Config file written“ v `~/.config/Claude/logs/main.log`);
stránka AI opakuje neúspěšný dotaz na stav po 1, 2 a 4 s. B2 je napsané:
`assistantpanel` `Request` (jednorázový požadavek bez mostu), `Rewriter`
a `Searcher` jako porty `AssistantRequest`, `ComposeRewriteController`
a `SearchConversion` i s testy; okno Nová zpráva má tlačítko ✦
`rewrite_button` s `rewrite_popover` (`compose/rewrite.go`, rozhraní
`compose.Assistant` nad stavem `window.Assistant`, `CanRunInApp`), most
editoru `rewriteTarget`/`rewriteApply` (pasáž posílá zprávou „rewrite“,
`editor.RewriteTarget`, `ApplyRewrite`), `compose.Params.Attribution`;
hledání má tlačítko ✦ `search_own_words` vedle pole a Alt+Enter
(`window/search_ownwords.go`). Handler Claude Code
z vývojového běhu v Toolbxu běží uvnitř kontejneru; předání do terminálu
zkoušet s aplikací nainstalovanou na hostiteli. Windows: A, B1 i B2 jsou
napsané jako port macOS (`Malachi.Core/Assistants/` = čistý balíček
`ui/internal/assistant` včetně sémantiky bajtů UTF-8 a čtení JSON jako Go;
kontrolery `AssistantController`, `ClaudeDesktopController`,
`AssistantPanelController`, `AssistantRequest`, `ComposeRewriteController`,
`SearchConversion`, `Platform/ClaudeCodeLocator`, `ClaudeCodeProcess`, testy
proti falešnému `claude.exe` z `tests/Malachi.FakeClaude`; UI
`Malachi.App/Assistants/`, `MainWindow.Assistant.cs`, `MainWindow.OwnWords.cs`,
`Compose/ComposeWindow.Rewrite.cs`, `Preferences/AiPage`), msgid asistenta
jsou z exclusions pryč. Odlišnosti: Claude Code jen jako `claude.exe`
(nativní instalátor `%USERPROFILE%\.local\bin`, pak `PATH`; npm
`claude.cmd` ne, `cmd.exe` by JSON argumenty nepřenesl bezpečně), konec
procesu zavřením stdin a zabitím stromu procesů po 2 s (žádný SIGTERM),
pracovní adresář `%LOCALAPPDATA%\Malachi Mail\assistant`; Claude Desktop je
MSIX `Claude_pzs8sxrjxfjjc`, restart ho požádá o ukončení Restart
Managerem jako při odhlášení (zavření okna ho jen schová do oznamovací
oblasti), čeká 45 s a spustí ho podle AUMID (`ClaudeDesktopApp`). Ověřeno
automaticky a v UI proti falešnému `claude.exe` a devmailu a uživatelem se
skutečným Claude Desktop a Claude Code (2026-09-30). Cíl „V aplikaci“ zůstává
experimentální, dokud Anthropic nepotvrdí podmínky pro spouštění Claude
Code z aplikace.

Přihlášení Claude Code z aplikace (2026-09-30, ve všech třech klientech;
běžný uživatel nic nespouští v terminálu). Claude Code má vlastní
přihlášení, oddělené od Claude Desktop (`~/.claude/.credentials.json`, na
macOS Keychain); aplikace přihlašovací údaje dál nikdy nevidí. Když
`claude auth status` řekne nepřihlášeno, řádek panelu „Claude Code není
přihlášený“ a řádek Claude Code v Předvolbách → AI nabídnou *Přihlásit
se…*: locator spustí vlastní `claude auth login` Claude Code
(`assistant.SignInArgs`, `SignInEnv` = `ChildEnv` + proměnné desktopové
session pro otevření prohlížeče na Linuxu; na Windows jen
`ChildEnvironment`), čeká nejdéle 10 minut na konec procesu (stav 0 =
přihlášeno; jeho výstup se neukazuje ani neloguje), jedno přihlášení pro
celou aplikaci (nové nahradí běžící, to skončí jako zrušené), panel ho
ukazuje jako řádek aktivity „Čeká se na přihlášení v prohlížeči…“, Stop ho
ukončí a po úspěchu se sama znovu pošle poslední otázka. Zprávu, kterou
Claude Code napíše sám, když API tah odmítne (`assistant` s polem
`error`, událost `EventFailure`), panel neukazuje jako odpověď (výsledek
ji opakuje); `authentication_failed` (vypršelý či odvolaný token, ať
`auth status` tvrdí cokoli) skončí stejným řádkem s *Přihlásit se…* a
ukončí proces. Bez Claude Code nabízí panel i Předvolby *Získat Claude
Code…* (`assistant.InstallURL`, stránka Anthropic v prohlížeči; aplikace
nic nestahuje ani nespouští). Přepis v okně Nová zpráva a hledání
vlastními slovy tlačítko nemají a odkazují na Předvolby → AI
(`SignInTexts().Hint`). Referencí je Go: `ui/internal/assistant`
(`SignInTexts`, `SignInFailedText`, `events.go`) a
`ui/internal/assistantpanel` (`Locator.SignIn`, `Controller.SignIn`,
`Offer`), testy proti falešnému `claude` (na Windows se pouští
křížově přeložené ve WSL). Stav: Windows hotový a ověřený testy i
průchodem UI proti falešnému `claude.exe`; okno GTK
(`window/assistant_panel.go`, `preferences.go`) a macOS klient jsou
napsané na Windows bez překladu, takže je čeká sestavení a test
v Toolbxu a na Macu; `po/malachi.pot` a `po/cs.po` jsou upravené ručně
(xgettext na Windows není), `make po` v Toolbxu je srovná. Skutečné
přihlášení (souhlas v prohlížeči) ověřuje vlastník. Na Macu zkontrolovat
hlavně: `Content.error(_, retry:, offer: = .none)` (výchozí hodnota
asociované hodnoty a `.none` u `Offer`), `ClaudeCodeLocator.startSignIn`
(`SignInRun`, `Task.detached` a `AsyncStream` pod Swift 6,
`nonisolated static runSignIn` s typy vnořenými v `@MainActor` třídě),
řádek chyby s tlačítky ve `FlowView` (mezera u poznámek a chyb bez
tlačítek) a testy závislé na čase (`signInStoppedAndReplaced`,
`ClaudeCodeLocatorTests.signIn`).

Doplněk k přihlášení (2026-09-30, všechny tři klienty). Když Claude
Code nemůže obnovit token, protože zámek obnovy
`~/.claude/.oauth_refresh.lock` drží jiný Claude Code (nebo ho po sobě
nechal proces ukončený uprostřed obnovy), odmítne tah s `error:
server_error`, ne `authentication_failed`, a slovy „Failed to refresh
OAuth token: …“ (změřeno s 2.1.284 v dočasném `HOME`: zámek starší než
asi minutu si Claude Code převezme sám, `auth status` token neobnovuje).
`EventFailure` teď nese text zprávy (`Event.Text`), `Event.RefreshFailed()`
ho pozná podle začátku a panel pak ukončí proces a řádek „Asistent
skončil: …“ nabídne *Zkusit znovu* i *Přihlásit se…*. `maxReason`
(`StoppedText`, `SearchFailedText`, `SignInFailedText`) a `reasonLimit`
procesu mají 400 bajtů místo 200 (ta věta Claude Code má 215 a rada je na
jejím konci). `Locator.run` (`--version`, `auth status`) po vypršení
limitu posílá SIGTERM a SIGKILL až po `DefaultKillGrace`, stejně jako
`Process` a přihlášení; macOS to tak dělal už dřív (`BridgeRunner`),
Windows dál ukončuje strom procesů. Porty: Swift `Assistant.refreshFailed`
a `Event.refreshFailed`, C# `Assistant.RefreshFailedPrefix` a
`AssistantEvent.RefreshFailed`, v obou controllerech `refreshFailed`,
`maxReason`/`MaxReason` a `reasonLimit`/`ReasonLimit` 400.

Kolečko čekání v panelu (2026-09-30, všechny tři klienty): dokud běží
dotaz a nic v přepisu neukazuje práci (nestreamuje odpověď, nepracuje
nástroj ani přihlášení), je pod přepisem otáčející se kolečko; stav počítá
controller z položek (Go `Controller.Waiting`, Swift `waiting`, C#
`IsWaiting`), pohledy jen přepínají (GTK `assistant_waiting` v
`assistant_panel.blp`, macOS `AssistantTranscriptView.setWaiting`, Windows
`WaitingRing` v `AssistantPanel.xaml`).

Obojí je v Go a GTK sestavené a otestované; Swift a C# jsou napsané na
Linuxu bez překladu, takže je čeká sestavení a testy na Macu a na
Windows: macOS `waitsWhereNothingShowsTheWork`,
`refreshFailedOffersRetryAndSignIn`, `signInFailures`, `stoppedText`,
`signInFailedText`, `searchFailedText`, `stderrIsBounded` a kontrola
řádků proti `events_test.go`; Windows `WaitsWhereNothingShowsTheWork`,
`RefreshFailedOffersRetryAndSignIn`, `SignInFailures`, `StoppedTextCases`,
`SignInFailedText`, `StderrIsBounded` a `LinesAreGos`. Na pohled: kolečko
pod přepisem (macOS řádek v `FillStackView`, Windows `ProgressRing` pod
`Transcript`) a řádek chyby s oběma tlačítky.

Tabulky v odpovědích (2026-09-30, všechny tři klienty): model je posílá
i přes zákaz v systémovém promptu (Úkoly a termíny: „co, kdo a do kdy“),
proto je parser Markdownu (`assistant.Markdown`, Swift
`Assistant.markdown`, C# `Assistant.Markdown`) čte jako GitHub tabulku
(řádek buněk v odstavci, řádek oddělovačů se stejným počtem buněk, pak
řádky) a z každého řádku udělá odrážku: první buňka tučně, další neprázdné
buňky na vlastních řádcích jako „záhlaví: buňka“. Pohledy se nemění; jen
GTK spojuje řádky jednoho bloku mimo kód znakem U+2028 jako macOS
(`lineSeparator`), aby pokračovací řádek odrážky začínal pod textem.
Popisky záhlaví smějí stát nejvýš tolik bajtů jako řádky samotné (jinak
by obří záhlaví opakované u každého řádku znásobilo výstup). Swift a C#
napsané bez překladu: testy `blocks` a `isLinear` (macOS) a `BlockCases`
a `LinearInputs` (Windows).

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
testované na Macu), portovaná 1:1 do `MalachiCore/Jira`; Windows viz
níže (funkce označené „Swift-first“ a kde je GTK zrcadlí: tabulka
v `macos/README.md`). GTK (2026-09-30) používá referenční balíčky tak,
jak jsou, přes `i18n.Tr` (adaptér `jira.Translator` nad `i18n.T/N/C`)
a nepřidalo žádný msgid: sidebar (`model.go` `accountLabel`,
`sortSiblings` s `VirtualRank`, `folderIcon`, `accountHeaderBadge`;
`folders.go` kapsle druhu za jménem každého účtu — JIRA, u pošty
poskytovatel `GOOGLE`/`M365` podle `signin.Provider`, jinak `IMAP`; GTK
první, macOS od 2026-09-30 (`FolderTree.swift` `accountHeaderBadge`),
Windows od téhož dne —, názvy pohledů), seznam
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
(`accountAuthBannerTitle`). Zobrazení konverzace (všechny tři klienty,
reference `ui/internal/conversation`): výběr sbaleného řádku vlákna (≥ 2 členů ve složce; jira složky
vždy) ukáže v panelu čtení celé vlákno jako nativní karty s časovou osou
v levém okraji, řazené jako issue v Jiře (viz GTK níže; macOS stejně od
2026-09-30, `ConversationLayout.displayOrder`), u jira kartou issue nahoře
a událostmi jako kompaktní řádky, přečtený se označí jen nejnovější člen,
který není událost; každá HTML karta má vlastní uzamčený WKWebView v režimu `sized`
(výšku hlásí skript aplikace ve vlastním světě, JS obsahu vypnutý, strop
4000 pt, nejvýš 8 živých pohledů; `ConversationLayout.swift`,
`ConversationViewController.swift`), nikdy jeden složený dokument — CSS
jedné zprávy by přepsalo hlavičky ostatních. GTK: `window/conversation_*.go`
(jako issue v Jiře: nahoře karta issue, pod ní úvodní zpráva — popis
issue, u pošty nejstarší neořezaná zpráva — sbalená na hlavičku
a náhled, dokud následuje jiná zpráva, pak ostatní od nejnovější,
`convDisplayOrder`, otevřené nahoře — rozhodnutí uživatele 2026-09-30,
model zůstává od nejstarší; řádek starších zpráv dole; controller
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
macOS dorovnaný na GTK port (2026-09-30; Windows port bere toto chování
jako výchozí): řazení konverzace se sbalenou úvodní kartou (šipka
`CardFoldButton`, náhled ze `snippet`, volba uživatele platí do výběru
jiné konverzace), kapsle druhu u všech účtů, *Přidat účet Jira…* i na
prázdném okně, stránka Účty se po výměně tokenu načte znovu, uložení
nastavení přeskočí zašedlé pole odesílatelů, „Změnit stav“ v Dalších
akcích je mimo issue skryté (v menu baru zašedlé jako ostatní položky),
komentář se otevře bez `message.download`, `issue.transitions` čeká 45 s
jako GTK, klíč issue, který nejde otevřít, je text k označení, pilulka
stavu se ořízne na 24 znaků (`PillLabel.statusCharacters`) a celý stav má
tooltip, selhané `message.get` se v konverzaci neopakuje (`noGet`)
a model postavený po selhaném `thread.get` ze seznamu se po příchodu členů
postaví znovu (`listing`), čipy příloh v kartách mají „Zeptat se
asistenta…“. Ikony poskytovatelů v Předvolbách → Účty macOS nemá (SF
Symbols je neobsahuje, řádek pošty má obálku; tabulka v `macos/README.md`).

Windows (2026-09-30, větev `feat/jira-windows`, `docs/windows-port.md`
§11.7 a §6.7): port Swiftu, chování podle GTK. Core: `IssueTrackers/`
(= `ui/internal/jira` a `ui/internal/capabilities`), `Model/Conversation*`,
`ConversationLayout*` a `WebHeightGovernor`, controllery
`JiraWizardController`, `JiraAccountController`, `IssueActionsController`,
`ConversationController`, režim komentáře `ComposeController`
(`CommentAccount`), vše s porty testů Go a Swiftu. App: průvodce
`Wizard/JiraWizardWindow` (stránky site, přihlášení, spaces; vlastní modální
okno přes `ModalDialog` jako poštovní průvodce), nastavení
`Preferences/JiraAccountWindow` (+ `JiraListEditor`, `JiraStatusPicker`;
Uložit a Zrušit dole), *Přidat účet Jira…* v nabídce `…`, v „+“ Předvoleb
→ Účty a na prázdném okně, karta issue `Reader/IssueCardView` (pilulka
jako `DropDownButton` s přechody), okno komentáře
`Compose/ComposeWindow.Comment.cs` (viditelnost jako `SelectorBar`),
zobrazení konverzace `Reader/Conversation/` (`ConversationView`,
`ConversationCard`, řádky časové osy a událostí; čipy sdílené s panelem
přes `MessageChips`; místo čtení drží kotvení `ScrollViewer`; mezerník
a Shift+mezerník v seznamu). HTML karta je `WebViews/CardWebView`: skript
stránky vypnutý jako v prohlížeči zprávy, výšku měří hostitel skriptem
`CardSize` přes `ExecuteScriptAsync` po načtení, obrázku a změně šířky,
výšky či zvětšení (WebView2 nemá izolovaný svět a bez skriptu stránky
žádný posluchač nepoběží), strop a zmrazení jako GTK; kolečko nad kartou,
jejíž dokument se vejde, posouvá sloupec; kanárek pouští i kartu.
Odchylky v tabulce `windows/README.md`. Prošlé ručně proti falešnému
Jira DC (kopie `jiratest` na 127.0.0.1 ve výzkumné složce agenta, token
fixtury) a devmailu; průchod našel dvě chyby, obě opravené: porovnání
karty issue přes JSON kontext API (`JiraCard` v něm není, konverzace
přestala sledovat členy) a `sameShape`, který při novém načtení nechal
členy vlákna, jehož issue změnilo jen stav či řešitele (s vypnutými
událostmi) — oprava ve všech třech klientech, Go a Swift napsané na
Windows bez překladu: `go test ./internal/window` v Toolbxu a
`swift test` na Macu (`setThreadsIssueMoved`) čekají. Zbývá průchod
vlastníka proti skutečnému Jira Cloud (token zadá sám, komentář jen do
issue, které určí).

Odeslané odpovědi v konverzaci a skrytá citovaná historie (2026-10-01,
všechny tři klienty; macOS otestovaný vlastníkem, GTK a Windows napsané
na Macu bez překladu — čeká `make build`, `go test ./internal/window/...`
a `make lint` v Toolbxu a `build.ps1 app`/`test`/`lint` na Windows).
Kompatibilní rozšíření protokolu 2 (`docs/api.md` §4.4 a `message.body`):
`ThreadSummary.sentCount` (členové vlákna ve složkách role `sent`, které
vypsaná složka nemá, podle Message-ID; 0 v Odeslaných, Outboxu
a virtuálních složkách) a `thread.get` `withSent` → `sent` (jen se
`folderId`, nikdy členové složky ani agregáty). Řádek je konverzace při
`messageCount + sentCount ≥ 2` (`conversation.IsConversationRow`);
odeslané karty (`Item.Sent`) jsou v časové ose podle data, startují
sbalené, nikdy nepřečtené ani označované, akce nad vláknem a počty
nepřečtených je nezahrnují, karta má Odpovědět/Všem/Přeposlat. Klient
duplicity nevidí (Message-ID zná jen démon), proto se znovu ptá
`thread.get`: příchod do Odeslaných vypsaného účtu, příchod do vypsané
složky ve vláknu s odpověďmi a `notify.messagesChanged` jmenující
Odeslané. Sbalit jde každá karta (`ui/internal/conversation/fold.go`:
`DefaultFolds`, `Folds` platné do výběru jiné konverzace, `FoldAllOffer`
→ tlačítko Sbalit vše / Rozbalit vše nad konverzací). Citovaná historie:
`message.body` `trimQuoted` → `quotedTrimmed`; démon ořízne strom před
sanitizací (`internal/sanitize/quote.go`, `quote_text.go`: Gmail,
cite blockquote, Outlook web i desktop s lokalizovanými hlavičkami,
Původní zpráva, `>` za „napsal(a):“), jen když citace jde do konce
a nad ní je viditelný text, při pochybnosti nic; MCP most a
`draft.create` dostávají celé tělo. Klienti se ptají s `trimQuoted`,
drží obě varianty v cache zprávy, tlačítko „•••“ (Zobrazit / Skrýt
citovaný text) pod tělem v kartách konverzace, panelu čtení i okně
zprávy (přiložené zprávy ne); odhalení platí do jiné konverzace či
zprávy, okno ho drží, dokud je otevřené (`QuotedReveal`, Swift první,
port v `ui/internal/conversation/quoted.go` a `Malachi.Core`).

Dvojklik na záhlaví karty konverzace (2026-10-02, všechny tři klienty;
necommitnuté; Go a GTK sestavené a otestované v Toolbxu, Swift a C#
napsané na Linuxu bez překladu — čeká `swift test` na Macu
(`doubleClickOpensAWindowOrTheDraft`) a `build.ps1 app`/`test`/`lint` na
Windows (`DoubleClickOpensAWindowOrTheDraft`)). Dvojklik na záhlaví karty
v zobrazení konverzace (od horního okraje karty po spodek řádku záhlaví,
mimo jeho tlačítka: šipka sbalení, rozbalení příjemců, Odpovědět/Všem/
Přeposlat) otevře zprávu stejně jako dvojklik na její řádek v seznamu:
v samostatném okně, zprávu ze složky Koncepty v okně Nová zpráva. Pravidlo
je jedno pro seznam i kartu: GTK `Window.openMessage` (`message_view.go`,
volá ho i row-activated), Swift `ActionsController.openMessage` přes
`MessageActionDelegate.openMessage`, C# `ActionsController.OpenMessage`
přes `MessageActionRouter.OpenMessage`. Pohledy jen poznají místo: GTK
`convCard.openOnDoubleClick`/`onHeader` (gesto na kartě, meze přes
`ComputeBounds`), macOS `NSClickGestureRecognizer` karty se dvěma kliky,
který čte jen kliky na záhlaví (`shouldAttemptToRecognizeWith`, `onHeader`,
`ConversationCardHeader.isButton`) a nezdržuje kliky pohledům pod sebou
(`delaysPrimaryMouseButtonEvents = false`), Windows `DoubleTapped` na
rámečku karty (`OnFrameDoubleTapped`, `OnHeader`). Na Macu ověřit hlavně,
že jednoduchý klik na šipku sbalení, rozbalení příjemců a tlačítka při
najetí reaguje hned a že dvojklik v těle (výběr slova) okno neotevírá.

Příjemci jako badge (2026-10-01, všechny tři klienty; macOS otestovaný
vlastníkem, GTK a Windows napsané na Macu bez překladu — čeká `make build`,
`go test ./internal/compose/... ./internal/recipients/...` a `make lint`
v Toolbxu a `build.ps1 app`/`test`/`lint` na Windows). V okně Nová zpráva
se dokončená adresa v polích Komu, Kopie a Skrytá zavře do kapsle
s křížkem, vzhledem stejné jako kapsle adresy v hlavičce přijaté zprávy;
neplatný záznam je červená kapsle. Všechna pravidla drží čistý model
`ui/internal/recipients` (`Tokens`: tokeny + rozepsaný text; bez gotk4,
proto vlastní balíček; port `MalachiCore/Compose/RecipientTokens.swift`
a `Malachi.Core/Compose/RecipientTokens.cs` i s testy): zavírá čárka,
středník, Enter, Tab, ztráta fokusu a výběr z našeptávače, mezera jen za
samotnou platnou adresou; vložení dělí i po řádcích (nejvýš 64 KiB);
strop 1000 tokenů jen zastaví automatické dělení, zbytek zůstane
rozepsaný, nic se nezahodí; popisek a tooltip jsou bez znaků řízení směru
textu. Odeslání i uložení konceptu čtou `Resolved()` modelu, nikdy znovu
parsovaný text pole (ten je ztrátový: dvě neplatné kapsle se mohou spojit
v jednu platnou adresu, jméno s dvojtečkou se zpět nepřečte). Pohledy
(`RecipientTokenField.swift`, `compose/recipient_field.go` nad
`Adw.WrapBox`, `RecipientTokenBox.xaml`) pravidla neobsahují: Backspace
v prázdném editoru označí poslední kapsli a další ji smaže, šipky kapslemi
procházejí, dvojklik vrátí kapsli do textu, pole má nejvýš 4 řádky a pak
se posouvá, změna se hlásí jen při skutečné změně textu. Zbývá:
`compose/address.go` opakuje dělení a formátování z `recipients`;
`AddressList.format` nedává jméno s dvojtečkou do uvozovek (starší chyba,
týká se předvyplnění odpovědi).

Hromadná pošta a odhlášení (2026-10-01, všechny tři klienty; necommitnuté,
macOS sestavený a cílené testy zelené, GTK jen `gopls` + Blueprint v Dockeru,
Windows napsané bez překladu — čeká `make build`/`go test ./internal/window/...`/
`make lint` v Toolbxu a `build.ps1 app`/`test`/`lint` na Windows). Démon
klasifikuje zprávy z hlaviček (`internal/bulk`, pravidla verze 1:
`newsletter`, `list` = List-Id + List-Post, `automated` (od verze 2 i podle
stop rozesílacích služeb, `bulk.SenderFingerprints`, např. Kickstarter přes
SendGrid bez `List-Unsubscribe`); migrace 0016
`messages.bulk`/`list_id` + `unsubscriptions`, IMAP bere hlavičky už
s obálkou, backfill pod `bulk.classified`, jira nikdy), `MessageSummary.bulk`,
`Message.unsubscribe` (oneClick / mailto / url, `unsubscribedAt`, v nevyžádané
nikdy) a `message.unsubscribe` (`core/unsubscribe.go`): oneClick jen po DKIM
podpisu organizace odesílatele přes `List-Unsubscribe` i `-Post`
(`go-msgauth`; u účtů Graph, kde Exchange vydává přestavěné MIME a tělo
podpisu nesedí, horní `Authentication-Results` od Exchange s `dkim=pass`
téže organizace + `h=` podpisu té domény, `bulk.ExchangeVerified`), POST
z `internal/oneclick` (bez přesměrování, bez neveřejných adres i s proxy,
do logu jen třída chyby), jinak výsledek `unverified` s adresou `mailto`
alternativy (klient ji po potvrzení pošle s `method: mailto`; URL pro jeden
klik se nikdy neotevírá v prohlížeči, POST endpointy na GET neodpovídají);
mailto jako prostý mail přes outbox bez konceptu; url jen vrací stránku;
opakování do 60 s vrátí uložený výsledek, souběh `conflict`; chyba 1505
`unsubscribeFailed` (`docs/security.md` §7.2). MCP: `bulk` v souhrnech,
`unsubscribe` za `--allow-modify`, mailto navíc jen s `--allow-send`. UI:
reference `ui/internal/bulkmail` (štítek v seznamu, pruh nad zprávou,
potvrzení, `Fallback` po neověřeném odesílateli, `OpenableURL` jen https),
port `MalachiCore/Bulk` a `Malachi.Core/Bulk`; pruh v panelu, okně zprávy
a kartách konverzace, ne v přiložené zprávě.

Nástěnka (stav 2026-10-02, větev `feat/board`; nejdřív macOS a potom
GTK z výslovného pokynu vlastníka, výjimka z pravidla 6: Windows
ji stále dluží). Hlavní okno má dva režimy, Pošta (vše dosavadní)
a Nástěnka. Přepíná dvousegmentový přepínač (`envelope` / `square.grid.2x2`)
na začátku každého toolbaru a položky Pošta a Nástěnka na vrcholu menu
Zobrazení, bez klávesových zkratek; režim se neukládá (`Board.initialMode`
je Pošta). Styl nástěnky při prvním zobrazení po spuštění určuje klíč
`board-default-style` (`list`/`columns`/`today`, výchozí `list`, neznámá
hodnota = Seznam; Předvolby → Obecné → Nástěnka → Výchozí zobrazení),
pak platí poslední zvolený až do ukončení a změna klíče po prvním zobrazení
se projeví až po dalším spuštění (`BoardController.boardWillShow`,
`Board.styleOnShow`). V Nástěnce je obsah okna (sidebar, seznam, čtení, panel
asistenta) nahrazený stránkou nástěnky, stavový pruh zůstává. Pohled split
view pošty se z okna **vyjme** (`MainContentViewController.setMode`;
kontroler i stav pošty — složka, výběr, posun, hledání, přepis asistenta —
žijí dál a po návratu se pohled vrátí): skrytý split by v unified toolbaru
dál držel místo pro svou sekci sidebaru. Okno proto podle režimu a stylu
mění toolbary a po každé instalaci toolbaru musí odebrat a znovu vložit
`.sidebarTrackingSeparator`, jinak se po návratu pohledu do okna znovu
nenaváže na dělicí čáru (změřeno, ne zdokumentováno). Akce nad poštou jsou
v Nástěnce vypnuté (`Board.Command.allows`), Zkontrolovat poštu a Nová
zpráva zůstávají; Outbox ze stavového pruhu a panel asistenta vrátí Poštu;
okno s Nástěnkou se pro notifikace nepočítá jako pohled na složku
(`viewsMail`).

Model a pravidla (démon, `docs/architecture.md` §3.7, `docs/api.md` §4.13,
rozhodnutí v §7). **Případ** je jedno vlákno účtu (u účtu jira issue) v
jednom ze čtyř stavů `hot` / `you` / `them` / `info` (Hot, Čeká na vás,
Čeká na ně, K informaci). Stav dávají pravidla démona
(`internal/board`, čistý balíček bez storu a hodin, `RulesVersion` "4";
každá změna pravidel nebo toho, co jim store podává, = nové číslo, démon
pak přepočítá všechny případy i uloženou poštu v nejdelším okně). Čtou jen
hlavičky, strukturu, role složek, příznaky a klasifikaci hromadné pošty,
nikdy slova zprávy, s jedinou výjimkou: otazník ve **vlastním textu**
uživatele (`board.OwnText`: u HTML pošty text HTML části po ořezu citace
jako `trimQuoted`, jinak prostý text; pryč `>` řádky, atribuce s
neoznačenou citací pod ní, podpis; selhává „zavřeně“, při pochybnosti
méně textu). Počítají se členové mimo koš, nevyžádanou a koncepty, ne
skryté, ne hromadné (vlastní vždy), u jira ne události; „moje“ je jen
řádek ve složce role `sent`/`outbox`, nikdy podle `From`; kopie jednoho
Message-ID jsou jedna zpráva a za cizí zprávu mluví kopie uložená první
(pozdější dvojče nic nezmění). Rozhodnutí vlastníka: `them` jen
`them.replied` (moje poslední zpráva odpovídá někomu, kdo ve vlákně psal)
nebo `them.asked` (žádná příchozí, otazník ve vlastním textu jedné z
posledních 10 mých zpráv, která není přeposlání); moje zpráva ve tvaru
přeposlání (`Fwd:`/`FW:`/…, příloha `message/rfc822`, začíná citací,
nebo nic neodpovídá a nad citací má méně než 300 B) případ nikdy nedělá.
Příchozí poslední člen: pořadí `hot.flagged`, `info.yourNote`,
`hot.important`, `you.repliedToYou`, `you.addressed`, `info.unknownSender`,
`info.ccOnly`, `info.notAddressed`. **Známý odesílatel** = jeho `From`
nebo `Reply-To` je v `To`/`Cc` některé zprávy ve složkách `sent`/`outbox`
kteréhokoli povoleného poštovního účtu (nejnovější první, nejvýš 20 000,
čte se hodinově a při změně účtů) nebo mé zprávy ve vlákně; zpráva mně od
neznámého je `info.unknownSender` a její `Importance` se nepočítá
(`you.repliedToYou` a vlastní vlajka `hot.flagged` platí pro kohokoli).
Jira: bez známého jira uživatele (`issues.me.`) žádné případy, issue
v kategorii done nebo v `closedStatuses` není případ, události
nerozhodují, poslední položka moje → `them`, cizí → `you` (jsem řešitel,
reportér nebo jsem dřív psal), `info` (jen sleduji). Okna (preference
`windows`): 90/30/30/14 dní pro hot/you/them/info, od `date` případu podle
stavu v platnosti. Dokud příchozí člen čeká na klasifikaci hromadné pošty,
verdikt je `Pending` a případ zůstává, jak byl. **Rozhodnutí uživatele
vyhrává**: stav uživatele > anotace asistenta (jen se zapnutou preferencí
`assistant` a ne `stale`) > pravidla; stav uživatele, remind (i prošlý,
dokud nepřijde done nebo nový), budoucí termín a otevřený závazek drží
případ, který by pravidla zahodila (`kept`); done znovu otevře jen
příchozí zpráva, kterou démon uložil po done a která přišla nejdřív den
před ním, a ne kopie zprávy, kterou případ měl už při done (`done_seen`;
tedy ne zfalšované `Date`, backfill ani přesun jiným klientem); done ruší
remind a zavírá otevřené závazky, remind ruší done; hotový případ je v
`board.list` ještě 30 dní.

Vrstvy. Migrace `0017_board.sql` (nevratná): `board_cases` (id `c_` + 32
hex přežije sloučení vláken — `mergeBoardCaseTx` ve `store/threads.go`
čte jen prosté sloupce, aby data nástěnky nikdy neshodila zápis pošty —
i přesun jiným klientem: vlákno bez viditelných členů nechá případ
`orphaned_at` mimo nástěnku, nové vlákno s některým z `member_ids` ho
převezme, po dni ho smaže údržba; odvozené sloupce jsou cache, sloupce
uživatele autoritativní; `input_key` = hash id a stavů těl počítaných
členů), `board_annotations`, `board_commitments`, `board_runs` a
`board_dirty` plněná triggery na `messages`, `issues`, `issue_items`
a `meta` `issues.me.`. Triggery vkládají jen chybějící řádek, ne
`INSERT OR IGNORE` (konfliktní klauzule příkazu, který trigger spustí,
přebíjí tu jeho), a na tyto tabulky se **nikdy nepíše `INSERT OR
REPLACE`** (obešlo by DELETE triggery). Store `store/board.go`,
`board_cases.go`, `board_maint.go`, `board_triage.go` (`DrainBoard`:
dávka nejvýš 50 vláken, 10 000 členů nebo 500 ms v jedné zápisové
transakci, savepoint na vlákno, vlákno, které selže, se zaloguje jen
id a vypadne ze sady). Core `core/board_worker.go` (worker spuštěný se
synchronizací, budí ho notifier, metody nástěnky, tik 30 s a časovač
nejbližšího remindu; `notify.boardChanged` nejvýš jednou za sekundu,
jmenuje účty), `board_adapter.go` (decider běží v transakci a nesmí volat
store; text členů čte líně, jen `Verdict.TextMembers`), `board_owntext.go`
(vlastní text HTML zpráv se odvozuje mimo transakci, cache 8 MiB),
`board_backfill.go` (první vyhodnocení uložené pošty v `core.Maintain`
po 2000 zprávách, kurzor `meta` `board.rules` = `<verze>:<id>` /
`<verze>:done`; do konce `ready: false`), `board_service.go` (15 metod
`board.*`, preference v `meta` `board.prefs`, ne `config.get`). Hodinová
údržba `boardUpkeep` ukončí běhy otevřené přes 2 h, maže běhy starší
90 dní, prošlé remindy, staré a osiřelé případy, a porovná role složek
s `meta` `board.roles`. API `pkg/api/board.go` (kompatibilní rozšíření
protokolu 2, chyby 1106 `caseNotFound` a 1506 `quoteNotFound`,
`notify.boardChanged`); Swift zrcadlo `MalachiCore/API/BoardAPI.swift`, C#
`windows/src/Malachi.Core/Api/Board.cs` napsané bez sestavení.

Asistent smí na nástěnku **jen přes most** a jen zapisovat poznámky:
**anotaci** případu (stav, titulek, shrnutí, proč, úkoly, termín s
doslovnou citací, odkaz na koncept; nahrazuje se celá) a **závazek**
(slib z vlastní zprávy uživatele s doslovnou citací). Démon nic s modelem
nemluví. Každý řetězec čistí (řídicí, bidi a neviditelné znaky, URL) a
hlídá limity; `inputKey` z fronty porovná a v transakci `board.annotate`
spočítá znovu (`conflict`); anotace po změně členů je `stale` a neplatí
(odkaz na koncept zůstane). Citace se normalizuje na obou stranách stejně
(NFC, URL → zástupný znak, typografické uvozovky → ASCII, mezery) a musí
být přesný podřetězec: termín v uloženém prostém textu počítaného člena,
závazek ve vlastním textu mé zprávy (cizí slova nikdy nejsou můj slib);
datum od dne před do 400 dní po **příchodu** zprávy (`board.Arrival`,
nikdy jen podle `Date`). Koncept musí být koncept účtu případu
odpovídající na jeho člena. Asistent nemůže nastavit stav uživatele,
done, remind, archivovat, zahodit koncept, měnit preference ani spustit
běh. Klient ukazuje poznámky jen jako prostý text označený jako
asistentův, citaci vždy u data. Most (`docs/mcp.md`, Triage of the
board): `list_board` vždy (stránky do 48 KiB, se zapnutým asistentem
i poznámky, jinak zadržené); `--allow-triage` (v `.mcp.json` z
`MALACHI_MCP_ALLOW_TRIAGE`) přidá `list_triage_queue` (do 60 KiB, každý
případ v ohradě s vlastní nonce), `annotate_case`, `add_commitment`
a prompt `triage_board`; `--triage-run` (`MALACHI_MCP_TRIAGE_RUN`) dává
`runId`, `--triage-max` (`MALACHI_MCP_TRIAGE_MAX`, 1–200, výchozí 200)
je tvrdý limit přijatých anotací procesu, fronta pak nevydá víc než limit
+ 3 případy, závazků nejvýš 100. Návrh odpovědi jen u `hot.important`,
`you.addressed`, `jira.assigned`, `jira.reporter`, nikdy `info.*`.
Úroveň triage je nezávislá na `--allow-modify`/`--allow-send` a nic z nich
nezapíná; `install` ani přepínač MCP v Předvolbách ji nepřidávají.

Běh triage v aplikaci (macOS; `docs/mcp.md` The board's triage run in
the app, `docs/security.md` §10.2): tlačítko ✦ Triage v obou toolbarech
nástěnky spustí uživatelův Claude Code s příkazovou řádkou panelu a
mostem `--allow-triage --triage-run <runId> --triage-max <n>`, nikdy
modify/send; `--allowedTools` = čtecí nástroje panelu, `create_draft`
**jen u ručního běhu** (`Assistant.triageAutomaticDrafts = false`) a tři
nástroje triage. S `--triage-run` dělá `create_draft` jen odpověď
(`reply`/`replyAll`, na Jiře veřejný komentář) na zprávu případu, který
procesu vydala fronta, bez `to`/`cc`/`bcc`/`subject`/`messageAccountId`
a `visibility: internal` (`triageReplyAllowed`, jinak pevná chyba
`triageDraftRefusal`). Nabízí se jen s cílem asistenta „V aplikaci
(experimentální)“ (`BoardTriageController.triageNeedsInAppTarget`),
s nalezeným Claude Code, který není odhlášený, a s mostem v bundlu. Dva
souhlasy: panelu `assistant-consent` a nástěnky `board-triage-consent`,
oba jedním listem „Let the Assistant Triage the Board?“, plus preference
démona `assistant` (zapíše se první, klíče až když ji démon uložil; ruční
běh se zeptá, automatický nikdy). Odvolání v Předvolbách → AI → Nástěnka (skupina Board)
zastaví běh a vypne `assistant` i `autoTriage`; bez klíče nástěnky
aplikace `assistant` při každém načtení preferencí zase vypne. Běh:
`board.runStart` (`manual`/`auto`, source `claude-code`), model vlastní
(`board-triage-model`, řádek Model ve skupině Board, výchozí Sonnet,
nezávislý na `assistant-model` panelu, platí od dalšího běhu), nejvýš 40
případů (`Assistant.triageBatch`), automatický navíc nejvýš zbytek
denního stropu, timeout 15 min, `board.runEnd` s třídou chyby a se
spotřebou tokenů z výstupu Claude Code (`assistant.UsageTally`, Swift
`Assistant.UsageTally`: `usage` výsledku, jinak součet různých API zpráv
podle `message.id`, bez nich nic; po dosažení limitu aplikace počká
nejvýš 45 s (`limitGrace`) na závěrečné hlášení Claude Code, aby
spotřeba byla úplná, zastavený nebo vypršený běh hlásí jen dolní mez; Předvolby → AI → Nástěnka ukazují
„Tokens in the Last 24 Hours“ z `triage.usage24h` s rozpisem a počtem
běhů); průběh jen z událostí stream-json (přijaté a odmítnuté `annotate_case`), po dosažení
limitu běh skončí jako úspěch; nic, co model napíše, se neukazuje ani
neloguje. Ukončení aplikace čeká na `board.runEnd` nejvýš 2 s, otevřený
běh démon ukončí jako `failed` při startu nebo po 2 h. Automatická triage
(`BoardAutoTriageScheduler` nad čistým `Board.AutoTriage.decide`,
přepínač „Triage new mail automatically“): výchozí vypnutá; běží jen když
fronta něco má, nejdřív po `autoTriageMinutes` (výchozí 30, nabídka
15/30/60/180), do denního stropu `autoTriageDailyCases` (výchozí 60,
nabídka 20/60/150, počítá démon za místní den), po neúspěších se interval
zdvojuje až na den; rozhoduje při změně vstupů, minutu po nových datech
nástěnky a každých 30 min; démon tyto preference jen ukládá.

Navrhnout odpověď (macOS; `docs/mcp.md` A suggested reply on the board,
`docs/security.md` §10.2): detail případu bez návrhu odpovědi má na jeho
místě pole s nepovinným pokynem a „✦ Navrhnout odpověď“ (pravidla
`Board.suggestReplyOffered`/`suggestReplyView` v `BoardSuggestReply.swift`:
případ s cílem odpovědi, ne hotový, ne K informaci, účet umí odpovědět,
ne vzorová data; dostupnost jako přepis v okně Nová zpráva, souhlas jen
panelu `assistant-consent`, model `assistant-model`). `BoardReplyController`
(jeden pro aplikaci, `AppState`) spustí jednou Claude Code s mostem
`--reply-only <replyMessageId>` a jen `read_message`, `list_messages`
a `create_draft`, koncept z výsledku `create_draft` propojí přes
`board.setDraft`; bez konceptu selže, odmítnuté propojení, Stop, timeout
2 min a ukončení aplikace vytvořený nepropojený koncept smažou
(`draft.delete`); spotřeba se nezapisuje.

Návrh odpovědi se edituje přímo v detailu (macOS, 2026-10-02): je to
lokální koncept, který případ propojuje (`Board.Case.draft`, nikdy ve
složce Koncepty), na nástěnce už není „Otevřít koncept“ ani okno Nová
zpráva. Obsah okna Nová zpráva je `Compose/ComposePane.swift`
(`NSViewController`; okno si nechává toolbar, otázku při zavření, Escape
a přepis a akce mu přeposílá); nástěnka ho používá jako `.inline`
s vlastníkem `.board` (bez řádku Od, editor v režimu `sized` s výškou
podle Core `EditorHeight` 160 až min(480, 0,6 × viditelná výška detailu),
pak roluje uvnitř, patička Přiložit · stav · Zahodit · Odeslat).
`ComposeDraftController` s `DraftOwner.board` při zavření nikdy nemaže,
konflikt řeší na místě (`draft.get`, vyhrává náš text), smazání jinde
hlásí `onLost`, `finish()` vyprázdní editor, uloží a uklidí.
`BoardReplyEditorController` (Core) načte koncept vybraného případu přes
`draft.get` s klíčem případ + účet + koncept, nikdy verze případu.
Pravidla panů drží Core `Controllers/BoardReplyPanes.swift` (testované
s falešnými pany i se skutečným draft controllerem, `BoardReplyPanesTests`);
`Board/BoardReplyEditorHost.swift` (vlastní ho stránka, sdílí ho detail
Seznamu i panelu) jen vyrábí `ComposePane`, stěhuje pohledy a přeposílá
konce. Snadno se rozbije: **slot odpovědi stojí mimo přestavbu
detailu** (sloupec je `upper` – slot – `lower`, `rebuild` sahá jen na
`upper`, `lower` drží trvalý blok konverzace; každý autosave zvedne verzi
případu a nástěnka se načte znovu, editor nesmí přijít o kurzor ani fokus); **jeden živý pane na
okno**, jeho pohled jde do detailu, který případ ukazuje (při přepnutí
stylu se přestěhuje); **nic napsaného ani výsledek Odeslat se neztratí
potichu**: pane, který přestal být vidět (jiný výběr, zavřený panel,
případ zmizel, režim Pošta, zavřené okno, ukončení), i pane, jehož případ
nástěnka ukáže s jiným konceptem nebo bez něj (to není důkaz, že koncept
zmizel), se nejdřív **uloží** (`settle()`, bez úklidu) a zavře až po
úspěchu; opustí ho jen `draftNotFound` draft controlleru (`onLost`, toast
„The suggested reply was removed elsewhere.“). Pane, jehož uložení
selhalo, se drží **bez limitu**, ukládá znovu s rostoucí prodlevou (5 s
až 2 min) a po návratu k případu se ukáže s poznámkou „This reply could
not be saved yet; Malachi Mail keeps trying.“; pane, ke kterému se
uživatel vrátí, zatímco se ukládá, zůstane jeho (dokončené uložení ho
nezavře). **Odesílající pane** se neukládá ani nezavírá, dokud odeslání
neodpoví: úspěch dá toast „Message queued for sending“ (u komentáře Jira
jeho) a `ended(key)` i mimo jeho případ, selhání toast draft controlleru
a mimo případ navíc „Your reply “%s” was not sent; it is still on the
board.“ a pane zůstane (takových čistých nejvýš tři). Ukončení aplikace
(`AppDelegate`, nejdřív odpovědi s limitem `BoardReplyController.endWait`,
pak triage a spojení) se při neuložené či neodeslané odpovědi zeptá
(„Quit without saving a reply?“, *Quit Anyway* / Zrušit; Zrušit nic
nezastaví). Draft controller pane s vlastníkem `.board` **nevolá
`draft.save` bez skutečné změny** (démon bere každé uložení propojeného
návrhu jako úpravu uživatele): `editorReady()` po `ready` editoru jedním
flushem zjistí, jak editor koncept sám zapsal, a `settle()` porovnává dvě
čtení editoru (před a po flushi), nikdy uložený HTML s jeho vykreslením.
**Inline Odeslat nemá klávesovou zkratku** (⌘↩ tlačítka by odeslalo
odkudkoli z okna), ⌘↩ a ⌘S jsou položky menu, které pane dosáhnou
řetězcem responderů jen s klávesnicí uvnitř, jinak je nic neobslouží
a jsou zašedlé. Zahodit: otázka draft controlleru a
`BoardController.discardDraft(_:draft:account:)` jako `discardStored`
(smaže právě ten koncept, dokud ho případ propojuje přes
`board.discardDraft`, jinak `draft.delete`; odmítnutí nechá pane i text),
případ pak nabídne Navrhnout odpověď; po Odeslat i Zahodit jde klávesnice
na pilulku stavu. Účet inline panu je vždy `params.accountID`, nikdy
zástupný. Odpovědět u případu s návrhem dá
klávesnici jeho editoru. Odebrat hvězdičku (`board.unflag`,
`BoardController.unflag`) je odkaz za „Proč je to tady?“ a položka
kontextového menu jen u případu horkého kvůli hvězdičce (`hot.flagged`)
a ne hotového (`Detail.canUnstar`, pro menu `Board.canUnstar`).

macOS UI: `MalachiCore/Board/` (`Board.swift`, `BoardCase.swift`,
`BoardView.swift` s `Board.view(snapshot, viewState, now:)`,
`BoardRemind.swift`, `BoardText.swift`, `BoardTriage*.swift`,
`BoardAutoTriage.swift`; šev `BoardSource.swift` s
`DaemonBoardSource.swift` nad `board.*` — optimistické zápisy, odmítnutý
se vrátí s toastem, `board.get` v cache podle verze — a
`InMemoryBoardSource` nad `BoardSamples.swift`), kontrolery
`Controllers/BoardController.swift` (vlastní ho okno se zdrojem),
`BoardPreferencesController`, `BoardTriageController` a
`BoardAutoTriageScheduler` (vlastní je `AppState`, jeden pro aplikaci),
`Assistant/AssistantTriage.swift`; AppKit `MalachiMail/Board/` (tři
styly Seznam / Sloupce / Dnes, detail s poznámkami pod značkou asistenta
a konverzací jako kartami (`BoardConversationBlock`, `BoardMessageCardView`;
od 2026-10-02 otevřená karta ukáže sanitizované HTML z `message.body`
s `trimQuoted` přes `MessageCache` ve stejném uzamčeném `MessageWebView`
v režimu `sized` jako karty konverzace Pošty — strop 4000 pt, kolečko jde
detailu, odkazy přes `openLink` delegáta z `MessageWindows.track(display:)`;
starší karty sbalené na náhled výňatku z `board.get` bez web view,
nejvýš 4 živé web view a ustoupí nejdéle neotevřená karta, blok se při
obnově nástěnky nepřestavuje, dokud se nezmění případ nebo jeho členové
(id + výňatek), pozdní výška karty nad viewportem posune scroll o tolik,
pravidla v Core `Board.ConversationCards`; jinak, bez HTML části, při
chybě či u vzorových dat výňatek z `board.get`); `BoardActions`:
Hotovo / Vrátit na nástěnku, Připomenout… s předvolbami, Archivovat,
Odebrat hvězdičku, Odpovědět — s návrhem odpovědi jeho editor v detailu —,
Zobrazit v Poště; `BoardReplyEditorHost` s inline `ComposePane`),
`MainWindowController+Mode/+Board/+Triage/+DevStart`,
`App/Integration+Board.swift`, skupina Board v Předvolbách → AI
(`AIPaneViewController`: souhlas, automatická triage s intervalem
a denním stropem, řádek stavu). Toolbar Seznamu: přepínač režimu, přepínač
sidebaru, v sekci sloupce seznamu styl na začátku a filtr účtu a Triage
na konci, sledovací oddělovač na dělicí čáře seznam/detail, pak Hotovo
(nebo Vrátit na nástěnku), Připomenout…, Archivovat a na konci
Odpovědět, bez titulku okna; Sloupce a Dnes: přepínač režimu a styl (oba
`isNavigational`), titulek s podtitulkem, pružná mezera, filtr účtu
a Triage, detail jako vysouvací panel s vlastní lištou akcí. Sekce
toolbaru nemají vlastní přetok, proto má sloupec seznamu minimum 372 pt;
Triage ustupuje do přetoku první. Tooltipy řádků a karet zrušené (AppKit
je ukazuje skrz překrývající panel). Každý řetězec z případu Core znovu
čistí a ořezává (`cleanLine` / `cleanBlock`) a UI ho ukazuje jen přes
`stringValue`. Texty nástěnky a triage mají referenci msgidů v čistém Go
balíčku `ui/internal/board` (`text.go`, `triage.go`, `reply.go`, texty za
rozhraním `Translator` jako `jira.Translator`, v `po/POTFILES`, česky
v `po/cs.po`); macOS `Board.Text` je přebírá s klíčem = msgid, co v něm
zůstane jako `// macOS-only string`, je anglicky. Windows nástěnku nemá,
takže tyto msgidy patří do `windows/parity-exclusions.txt`, dokud ji
nedostane.

GTK UI: `ui/internal/board` obsahuje i pohledové modely, kontroler,
zdroj nad démonem, vzorová data a pravidla dostupnosti a rozvrhu.
`ui/internal/boardtriage` vlastní předvolby, běh a automatickou triage;
`ui/internal/boardreply` návrh odpovědi, načítání propojeného konceptu
a životní cyklus jeho editorů. `window/board*.go` a `board_page.blp`
zapojují režimy Pošta / Nástěnka, Seznam / Sloupce / Dnes, filtry,
detail a vysouvací panel, akce, stav triage a navrhování odpovědí.
Konverzační blok je trvalý i při obnově detailu: starší karty jsou
sbalené na výňatek `board.get`, otevřené načítají `message.body` přes
cache Pošty a zobrazují jen sanitizované HTML ve stejném `htmlview.Card`
jako Pošta. Chyba, chybějící či zadržené HTML ponechá výňatek; zdrojová
HTML pošta se do UI nedostane. Nejvýš čtyři živé web view řídí model
konverzačních karet v `ui/internal/board`; karty si při obnově drží
identitu a po skrytí web view uvolní. Odkazy, části a omezení výšky
přebírají zabezpečení a pravidla Pošty.
Sdílený `compose.Pane` slouží samostatnému oknu i inline odpovědi;
vlastník `OwnerBoard` zachová neuložené či neodeslané odpovědi při
změně výběru nebo režimu. Automatická triage spouští zdroj i po
asynchronním načtení předvoleb a dostupnosti Claude, bez prvního
ručního otevření nástěnky. Výchozí styl je v Předvolbách → Obecné,
souhlas a rozvrh triage v Předvolbách → AI.

Zbývá port do Windows (C# typy API jsou napsané bez sestavení,
`build.ps1 app`/`test` čeká), ruční průchod GTK (všechny styly,
inline odpověď, přepínání výběru a ukončení s neuloženým textem)
a skutečný běh triage a navrhování odpovědi s Claude Code.
Testovací pokrytí GTK je v `ui/internal/board`, `boardtriage`,
`boardreply`, `compose` a `window`; samotná přítomnost testů
nedokládá ruční ověření UI ani skutečného Claude. Volitelný
`MALACHI_GTK_SMOKE=1 go test ./internal/window -run '^TestBoardGTKSmoke$' -count=1`
(z `ui/`, s GTK displejem, případně Broadway) zkouší skutečné Blueprinty,
tři styly, výběr a prázdný stav nad vymyšlenými daty bez démona.
Přidané `MALACHI_GTK_EDITOR_SMOKE=1` ověří skutečný inline WebKit editor,
jeho zachování při obnově a změně stylu a uložení nedotčeného konceptu;
`MALACHI_GTK_CARDS_SMOKE=1` přidá HTML karty, jejich limit, obnovu,
opožděné odpovědi a uvolnění. Obě rozšíření používají vymyšlená data
a odpojeného klienta, nikoli skutečnou schránku.
`MALACHI_GTK_EDITOR_SMOKE=1` přidá inline editor nad falešným konceptem,
`MALACHI_GTK_CARDS_SMOKE=1` HTML karty nad vymyšlenými sanitizovanými těly
v cache: identitu po obnově, změnu výňatku a opožděnou odpověď, strop
živých web view, fallback a uvolnění při odchodu z Nástěnky.
Testy backendu: Go testy
balíčků `board` (s patologickými vstupy z `backend/testdata/board`
a fuzz cíli), `store`, `core` a mostu, sady `swift test` nástěnky
(`Board*`, `AssistantTriageTests`, `DaemonBoardSourceTests`, testy API);
pravidla známých odesílatelů a okno 30 dní pro `you` vznikla po suchém
běhu nad kopií skutečného storu (`TestBoardDryRun`). UI zkouší vlastník
ručně. Zde není doložen skutečný běh triage s Claude Code (ruční ani
automatický), návrh odpovědi v GTK ani Windows build; migrace 0017 je
již zmrazená po provedení v ostrém storu vlastníka (viz níže).

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
  `i18n.Tr`. Windows port je používá všechny (z
  `windows/parity-exclusions.txt` zmizely). Po sloučení `main` (Asistent, 2026-09-30) na Macu je
  `make po` srovnal v kontejneru Fedora 42 (Blueprint 0.16, gettext 0.23.1
  jako v Toolbxu; stačí `blueprint-compiler`, `gtk4`, `libadwaita`,
  `webkitgtk6.0`, `appstream`, `gettext` a `make`, žádné Go ani překlad
  gotk4), takže všechny `.blp` prošly Blueprintem. Sloučené Go soubory GTK
  prošly jen `gopls check`, ne překladem: sestavení a testy čekají na Toolbx.
- `MALACHI_START` (`macos/Sources/MalachiMail/MainWindow/
  MainWindowController+DevStart.swift`) je vývojová pomůcka macOS klienta,
  ne funkce: kroky oddělené `;` (`mail`, `board:list`, `board:list:nav-off`,
  `board:columns`, `board:today`, `size=WxH`, jako poslední krok `quit`)
  provede po startu okno, `MALACHI_START_SIZE` nastaví velikost okna,
  `MALACHI_START_INTERVAL` prodlevu kroků v sekundách (výchozí 3)
  a `MALACHI_START_TRACE` vypisuje každou změnu šířky okna; po každém
  kroku vypíše na stderr polohy položek toolbaru a dělicích čar a po první
  Nástěnce i její fázi, počty, texty a stav triage. Slouží k ověření
  rozložení bez klikání, jen z izolované instance (`MALACHI_DATA_DIR`
  + vlastní `MALACHI_SOCKET`, viz Jira níže), nikdy nad ostrým storem; bez
  proměnné se nic nespustí. `MALACHI_BOARD_SAMPLES=1` (čte se jednou při
  startu) ukáže místo nástěnky démona vymyšlené vzorové případy
  (`InMemoryBoardSource.dummy`); Odpovědět, Zobrazit v Poště, Odebrat
  hvězdičku a Triage pak jen řeknou, že to náhled neumí, a návrh odpovědi
  je statický blok. Krok `compose` otevře a zavře prázdné okno Nová
  zpráva, `reply-pane` (se vzorovými daty) nechá skutečný
  `BoardReplyEditorHost` ukázat prázdný, nikdy neukládaný pane ve slotu
  Seznamu, píše do něj a vypíše výšky, polohu v detailu a kam míří Odeslat;
  `board-html` (jen se vzorovými daty) dá nejnovější kartě vymyšlený
  HTML dokument cestou odpovědi `message.body` a vypíše výšky, živé web
  view, kolik jich vyrobila opakovaná obnova, kam jde kolečko a posun
  scrollu.
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
- Nástěnku zkoušej taky jen nad kopií: migrace 0017 je nevratná (tabulky
  `board_*` a triggery na `messages`, `issues`, `issue_items`, `meta`),
  postup s `MALACHI_DATA_DIR` a vlastním `MALACHI_SOCKET` je stejný jako
  u Jiry výše. Běh triage i nad kopií posílá text pošty z ní přes
  uživatelův Claude Code do Anthropicu. Migrace 0017 je **zmrazená**
  (ostrý store vlastníka ji už provedl): nic se do ní nepřidává, sloupec
  `drafts.local` a další změny patří do 0018.
- `build/Malachi Mail.app` je denní aplikace vlastníka nad jeho skutečnými
  daty: agent nikdy nespouští `make macos`, nesahá na `build/` a bundle
  nespouští. Rozložení se měří debug binárkou
  `macos/.build/debug/MalachiMail` (po `swift build --package-path macos`)
  s `MALACHI_DAEMON=none`, prázdným dočasným `MALACHI_DATA_DIR`, vlastním
  `MALACHI_SOCKET` v něm, `MALACHI_BOARD_SAMPLES=1` a skriptem
  `MALACHI_START` končícím `quit`; nemá bundle identifier a píše doménu
  předvoleb `MalachiMail`, ne `io.github.schotek.Malachi`: před během
  `defaults export MalachiMail`, po něm `defaults import` (import jen
  slučuje, klíče přidané během je třeba smazat) a kontrola, že export
  `io.github.schotek.Malachi` je beze změny.
- `TestBoardDryRun` (`backend/internal/core/board_dryrun_test.go`) je
  suchý běh pravidel nad kopií storu, jinak se přeskočí: z `backend/`
  `MALACHI_BOARD_DRYRUN_STORE=<kopie>/store.db go test ./internal/core/
  -run 'TestBoardDryRun$' -v -count=1`. Zadaný store neotevře: zkopíruje
  `store.db` (i `-wal` a `-shm`) do vlastního dočasného adresáře, kopii
  migruje (0017), klasifikuje hromadnou poštu, vyhodnotí nástěnku
  a nakonec ji smaže; `messages/` vedle zadaného storu jen čte přes
  odkaz; cestu ve skutečných datových adresářích odmítne. Vypíše řádek na
  případ (stav, kód důvodu, účet, jméno protistrany, předmět do 60 znaků,
  počet zpráv) a součty, žádná těla ani adresy; i tak je to skutečná pošta.
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
