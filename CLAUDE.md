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
zbývá sestavit a ověřit. Port je hotový, až je každá nová akce zapojená
v UI (tlačítko, menu, kontextové menu, klávesa, otázka, toast), ne když
ji má jádro s testy; chybějící kompilátor neomlouvá nezapojené UI
(kontrolní seznam `docs/windows-port.md` §3.2). Když zadání neříká, kterých klientů se změna
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

GTK klient se od 2026-10-17 vyvíjí a testuje na Macu v kontejneru Fedora 42
přes Apple `container` (rozhodnutí vlastníka; Fedora Toolbx už není
k dispozici): `scripts/gtk-container/gtk.sh build` jednou postaví obraz
`malachi-gtk:f42` (balíčky z `rpm.yml`, verze jako dřívější Toolbx),
`gtk.sh make build` staví do `build/linux/` (cache Go a ccache
v `~/.cache/malachi-gtk`, první překlad gotk4 asi 20 minut, další půl
minuty), `gtk.sh test-ui` pouští testy `ui/` pod Xvfb (obrazovka
1600×1200, smoke testy `MALACHI_GTK_SMOKE=1`), `make po` jde v obrazu
`malachi-po:f42`. Okno ukazuje XQuartz: jednou `defaults write
org.xquartz.X11 nolisten_tcp -bool false` a restart XQuartz; každý `gtk.sh
x11` pak XQuartz sám spustí a kontejner autorizuje cookie displeje z `xauth`
(xhost je jen záloha: hned po startu XQuartz první desítky `xhost +` tiše
selžou). `gtk.sh x11` ukáže vzorová data, `gtk.sh x11 live` běží proti
démonovi na Macu (`gtk.sh forward` přesměruje jeho socket do kontejneru;
takový démon spouští vlastník sám z Terminálu skriptem
`scripts/gtk-container/test-daemon.sh` nad kopií dat s podepsaným helperem
klíčenky z `build/Malachi Mail.app`; po restartu démona restartovat
i klienta, čte jeho klíč při startu). `make run-gtk` udělá všechno samo:
sestaví klienta v kontejneru, spustí XQuartz a klienta s vlastním démonem
a gnome-keyringem (Secret Service) uvnitř kontejneru, data v
`~/.cache/malachi-gtk/data`; `make run-gtk-live` totéž proti démonovi na
Macu. Přihlášení v prohlížeči (Microsoft 365) otevře GNOME Web
v kontejneru, také v okně XQuartz, a přesměrování dojde na 127.0.0.1
kontejneru, kde poslouchá démon. Broadway
(`gtk4-broadwayd`) je jen na snímky při startu — v prohlížeči ztrácí vstup
po popoverech. Po každé práci `container system stop` (`gtk.sh stop`; oba
cíle ho volají samy). Zmínky o Toolbxu níže jsou historické;
`flatpak-builder` a balíčky zůstávají na linuxovém hostiteli.

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
    MCP, macOS, GTK i Windows klient sloučené z `feat/board` do `main`
    2026-10-14 (základní funkce ověřil vlastník); Go model je
    v `ui/internal/board`, běh triage v `boardtriage` a návrhy odpovědí v
    `boardreply`, Windows v `Malachi.Core/Boards` a `Malachi.App/Boards`
    (agenti prošli přes UI Automation nad vzorovými daty, vlastník ne).
    Zbývá ruční ověření GTK a Windows včetně skutečného běhu triage a
    návrhu odpovědi s Claude Code a Codexem, inline odpovědi nad skutečným
    konceptem a ARM64
11. Kalendář (Microsoft 365 přes Graph, CalDAV, pozvánky v poště, MCP)
    — sloučeno z `feat/calendar` do `main` 2026-10-17; démon a macOS
    hotové a ověřené vlastníkem (2026-10-16); GTK port hotový a ověřený vlastníkem v XQuartz
    (2026-10-17); Windows port napsaný, sestavený a prošlý na Windows
    (2026-10-16/17): `build.ps1 app` a `lint`, sada Core a UI smoke testy
    nad vzorky zelené, vlastník ho zkoušel nad svým M365 a Nextcloudem
    (jeho nálezy jsou opravené); zbývá celé `build.ps1 test`, tmavý motiv
    a jiná měřítka displeje, dotyk a pero, odmítnuté a zrušené bloky
    na obrazovce a ARM64

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

Citovaná zpráva a pruh asistenta v okně Nová zpráva (2026-10-14, všechny
tři klienty; macOS na `main` od `f17a379`, GTK a Windows doplněné
necommitnuté). Nová odpověď či přeposlání ukáže originál, který připravil
`draft.create`, odděleně pod editorem v panelu jen pro čtení (editor
začíná `<p><br></p>`); šipka panel jen skryje, zaškrtávátko „Zahrnout do
odpovědi/přeposlání“ rozhodne, jestli ho koncept a zpráva obsahují (tělo
`vlastní + "\n" + originál`). Koncept z Konceptů, komentář Jira a inline
odpověď nástěnky se otevírají celé v editoru, nic se v těle nehledá ani
nedělí. Pruh asistenta dole (jen cíl „V aplikaci“, ne u komentáře, nadpis
jmenuje poskytovatele) napíše odpověď, úvod přeposlání nebo zprávu
z nepovinného pokynu, náhled je prostý text a do editoru jde jen přes
Nahradit (místo vlastního textu, jeden krok zpět). Reference je Go:
`ui/internal/composebody` (rozdělení těla, Swift `ComposeBodyLayout`),
`ui/internal/assistant/compose_write.go` (`WriteMessage` = JSON s poli
mode, instruction, subject, existingText, originalMessage, systémový
prompt, `WriteTexts`, `WriteBarTitle`; Swift `ComposeAssistantPrompt`)
a `assistantpanel.Writer` (stavy jako `Rewriter`). GTK: `compose/quoted.go`
a `compose/write.go`, widgety v novém `compose_extras.blp` (bez
přeložitelných textů, `compose_pane.blp` beze změny), editor má
`ReadOnly` (`editor.ReadOnlyDocument`), `compose.Assistant.UsesChatGPT`.
Windows: Core `Compose/ComposeBodyLayout.cs`, `Assistants/Assistant.Write.cs`
(+ `AssistantWriteMode`, `WriteStrings`; JSON bajt po bajtu jako Go přes
`AppendJsonString(…, escapeHtml: false)`), `Controllers/ComposeWriteController.cs`
(sdílí `ComposeRewriteController.AnswerState`), App `ComposePane.Quote.cs`,
`ComposeAssistantBar.xaml`, `ComposeWindow.Write.cs`, `ComposeWebView`
s `readOnly` (`EditorDocument.ReadOnlyDocument`); odchylka: vyprázdnění
citace jde před editorem, protože kanál editoru hlásí `Changed` vyprázdnění
až po jeho callbacku (jinak by každé uložení znovu označilo koncept za
změněný). macOS bral texty jako anglické jen pro macOS, teď msgid z GTK.
Ověřeno na Macu: Go testy čistých balíčků, `swift build` a `swift test`, kontrola
řetězců macOS; GTK typově zkontrolované ze zdrojů gotk4 bez cgo (žádný
překlad), `po/malachi.pot` a `po/cs.po` upravené skriptem, který na
`HEAD` přesně reprodukoval výstup `xgettext` a `msgmerge` pro Go soubory.
Čeká: v Toolbxu `make build`, `go test ./internal/compose/...
./internal/editor/... ./internal/window/...`, `make lint` a `make po`
(má vyjít beze změny), ručně odpověď, přeposlání, Zahrnout, pruh
s Claude Code i ChatGPT; na Windows (napsané bez `dotnet`) `build.ps1
app`/`test`/`lint` (`ComposeBodyLayoutTests`, `AssistantWriteTests`,
`AssistantRequestTests` Writer*, `EditorBridgeTests`), UI smoke
(`DevmailTests` odpověď bez otázky, `BoardSmokeTests`, mřížka
`ComposePane.xaml` má o řádky víc) a ruční průchod.

Kontextová menu u zpráv a složek (2026-10-14, všechny tři klienty,
commit `bcaf8b0`; rozhodnutí vlastníka přes AskUserQuestion). Pravý klik
(v GTK i dlouhý stisk a Shift+F10 / klávesa Menu, na macOS Control-klik)
na zprávu ji vybere a menu působí na výběr: Otevřít v novém okně (koncept
Upravit), Odpovědět/Komentovat, Odpovědět všem, Přeposlat │ jen použitelné
z Označit jako přečtené/nepřečtené, Hvězdička, Změnit stav │ Archivovat,
Přesunout do ▸ (složky účtu v pořadí sidebaru bez Outboxu, Konceptů, \All
a pohledů Jira; aktuální složka a kontejnery zašedlé), Nevyžádaná, Koš
(Zrušit odesílání) │ Asistent ▸. Na složce (výběr se nemění): Zkontrolovat
poštu (pohled Jira = celý účet) │ Označit vše jako přečtené, Vysypat koš /
nevyžádanou (vždy s potvrzením) │ Oblíbené, Rozbalit/Sbalit; na hlavičce
účtu Zkontrolovat poštu a Rozbalit/Sbalit. Démon dostal dvě kompatibilní
metody protokolu 2: `folder.markRead` a `folder.empty` (jen role
trash/junk, capability `delete`, trvale i u Nevyžádané); obě nad uloženými
zprávami okna `offlineDays` (starší jen na serveru zůstanou, potvrzení to
říká), po dávkách `MaxMessageIDsPerCall` přes `FlagMessages` /
`DeleteMessages` (žádný nový druh operace ani migrace), pak
`notify.messagesChanged` (`core/folder_actions.go`,
`store.FolderMessageIDs`, `docs/api.md` §4.2 a §5, rozhodnutí v
`docs/architecture.md` §7); most MCP beze změny. Reference obsahu menu je
čistý `ui/internal/contextmenu` (`MessageItems`, `FolderItems`,
`AccountItems`, `MoveTargets`, texty), port `MalachiCore/ContextMenu` a
`Malachi.Core` ContextMenu; `actionState.move` / `ActionFlags.move`
(= `Capabilities.Available.Move`) a obecný přesun `moveIDsTo` /
`moveTo(_:folder:)` / `MoveTo`. GTK: `window/context_menus.go`
(`PopoverMenu` na `ListBox`, akce `win.open-message`, `win.reply`,
`win.reply-all`, `win.forward`, `win.move-to`, dočasná skupina
`folderctx`); macOS: `MessageList/MessageContextMenu.swift`,
`MessageListTableView.menu(for:)`, `FolderOutlineView` ve
`FolderSidebarViewController`, akce `openMessage:` a `moveToFolder:`.
Ověřeno na Macu: Go testy démona (celé) a čistých balíčků UI, `swift build`
a celý `swift test`, kontrola řetězců; GTK typově zkontrolované bez cgo;
`po/` skriptem ověřeným na `HEAD`. Čeká: Toolbx `make build`, `go test
./internal/window/...`, `make lint`, `make po` (beze změny), ručně menu
v GTK; macOS ručně v aplikaci (pravý klik na zprávu, konverzaci, koncept,
Outbox, Jira, Přesunout do, složka a účet, Vysypat jen nad kopií dat).
Windows (napsané agentem bez `dotnet`): `Malachi.Core/ContextMenus`
(`ContextMenu`, `ContextMenuText`, testy včetně Go id přes
`ContextMenu.Name`), `ActionsController.MoveTo`/`MarkFolderRead`/
`EmptyFolder`, `MailboxController.TriggerSync(FolderKey)`, API zrcadlo
(72 → 74 metod), rozšířené `OnContextRequested` v `MessageListPane` a
`SidebarPane`, `AssistantMenu.FillMessageMenu`; odchylka: bez
`Model.Folder(k)` se menu složky neotevře. Čeká `build.ps1 app`/`test`/
`lint` (`ContextMenuTests`, `ApiCodingTests`, `GoContractTests`,
`ApiRoundTripTests`, `ActionRulesTests`, `ActionsControllerTests`,
`MailboxControllerFoldersTests`, konvence), UI smoke a ruční průchod
(pravý klik, Shift+F10, klávesa Menu, odsazení Přesunout do em-mezerami).

Výběr více zpráv (2026-10-14, všechny tři klienty, necommitnuté;
rozhodnutí vlastníka přes AskUserQuestion). V seznamu jde vybrat víc
řádků: ⌘/Ctrl-klik, Shift-klik, Shift+šipky, ⌘/Ctrl+A (zobrazené řádky,
ne nadpisy dat), Escape v seznamu výběr více řádků zruší; žádný režim se
zaškrtávátky. Jeden řádek = beze změny. Dva a víc: panel čtení jen
„Vybráno N zpráv“ (plurál `%d message selected`), nic se neoznačí jako
přečtené, v úzkém rozložení se nepřepíná na čtení; akce působí na všechny:
Přečtené / Nepřečtené (když je některý nepřečtený / přečtený), hvězdička
(všem, pokud nemají všechny, jinak všem odebrat), Archivovat a Nevyžádaná
(když aspoň jeden může, zprávy už v cíli zůstanou), Koš a Přesunout do (jen
když mohou všechny; aktuální složka v Přesunout do zašedlá, jen když je
všude stejná), Přeposlat (jedna zpráva, každá vybraná jako příloha `.eml`,
nejvýš 20), Otevřít (okno pro každou, nejvýš 10, popisek „Otevřít %d zpráv
v nových oknech“), Asistent (Shrnout, Úkoly a termíny, „Zeptat se na tyto
zprávy…“, bez Navrhnout odpověď; id nejnovější první); Odpovědět,
Odpovědět všem, Změnit stav, Načíst obrázky a Důvěřovat odesílateli
vypnuté. Outbox: jen Koš = jedna otázka „Zrušit odeslání %d zpráv?“
a jedno `message.delete`. Potvrzení Koše a Nevyžádané s počtem bez
předmětu. **Výběr jen v rámci jednoho účtu**: řádek jiného účtu (hledání
ve všech účtech) nechá jen řádky účtu řádku přidaného naposled
(`selection.Restrict`), proto jedno volání na akci; počty nepřečtených se
upravují po zdrojových složkách (výběr v hledání může mít víc složek
účtu). Po změně seznamu zůstanou klíče, které v něm jsou (člen sbalený
do konverzace → její řádek, bez duplicit), jinak soused prvního
(`selection.Reconcile`); pravý klik do výběru ho nechá, jinam vybere jen
ten řádek. Démon: `draft.create` `forward` s `messageIds` (2–20, výlučně
s `messageId`, z jednoho účtu = `messageAccountId` nebo `accountId`,
kompatibilní rozšíření protokolu 2, `api.MaxForwardMessages`;
`core/forward_many.go`): koncept `Fwd: <předmět nejnovější>` bez těla
a citace, každá zpráva celá jako `message/rfc822` `<předmět>.eml`, jen
když je celá na disku nebo v paměťové cache (nic nestahuje — klient by
čekal), jinak a nad limit přílohy do `skipped` (`remote` u vzdálené);
`docs/api.md` §4.5; most MCP beze změny. Reference: čistý
`ui/internal/selection` (`Flags`, `Combine`, `FlagTarget`, `Restrict`,
`Reconcile`, `MaxOpen`, `MaxForward`, texty), `assistant.MessageActionsFor`
/ `LabelFor`, `contextmenu` `MessageState.Count`/`Open`, `Labels.Count`;
porty `MalachiCore/Selection`, `Assistant/AssistantSelection.swift`
a Windows (viz níže). GTK: `window/selection.go` (`ListBox` v režimu
MULTIPLE nastaveném z Go, `selected-rows-changed`, `selectedKeys`/
`selectedRows`/`selectedIDs` nad víc řádky, nahrazení výběru
`UnselectAll` + `SelectRow` pod `reselecting`, stránka počtu
`Adw.StatusPage` v `message_stack` vytvořená z Go, `restoreSelection`
po přestavbě), `action_rules.go` (`selectionState`), `actions.go` (počty
po složkách, `cancelSends`), `messages.go` (`removeMessageRows` jedním
sladěním), `context_menus.go`, `assistant.go`; macOS:
`ListController.selectedKeys` (`selectedKey` jen při jednom),
`select(keys:)`, `onSeveralSelected`, `selectedIDs([ListRow], …)`,
`ActionsController.cancelSends`/`forwardMessages`/`openMessages`,
`MessageListViewController` (`allowsMultipleSelection`,
`selectionIndexesForProposedSelection` s `restrict`, Escape přes
`cancelOperation`), `MessageContextMenu.make(rows:)`, `AssistantMenu(count:)`,
stránka počtu v `MessageViewController.showSelection(count:)`. Tlačítko ✦
hlavního okna ve všech třech klientech sleduje počet vybraných řádků
(bez Navrhnout odpověď, Zeptat se pro více zpráv): GTK
`assistantMenuCounting` s parametrem `count` v `bindAssistantButton`,
macOS validace `askAssistant` v `MainWindowController` (titulek
a `isHidden`), Windows `AssistantMenu(count:)` při každém otevření.
Windows (napsané agentem bez `dotnet`, `docs/windows-port.md` §11.2):
`Malachi.Core/Selections/Selection.cs` (jmenný prostor `Selections`,
jako `Boards`), `Assistants/Assistant.SelectionActions.cs`,
`ActionFlags.Count`/`Open`, `ListController.SelectedKeys`/`SelectedRows`/
`SelectKeys`/`SeveralSelected`, `IListSelection.SelectedIds` nad řádky,
`ActionsController.ForwardMessages`/`OpenMessages`/`CancelSends`,
`MessageActionRouter`, `ReaderPage.Selection`, `MessageListPane`
v režimu `Extended` (Ctrl+A, Escape, `Restrict` podle řádku s fokusem),
`IconGlyphs` `edit-select-all`; testy `SelectionTests`,
`ContextMenuTests.MessageItemsSeveral`, `MailboxControllerListTests`
(`SeveralRowsShowTheirCountAndFollowTheList`, `SelectedIdsOfSeveralRows`,
změněný `RowsAreKeyedSnapshotsWithTheSelectionAsAKey`),
`ActionsControllerTests` (`SeveralRowsTrashAndCountPerFolder`,
`SeveralRowsForwardAndOpen`), `MessageActionRouterTests.SeveralRowsActTogether`,
`ReaderControllerTests.SeveralRowsShowTheirCount`, API vzorky
(`messageIds`, `GoContractTests` 33 limitů). Hádané chování WinUI:
`SelectedItem` v režimu Extended, pořadí `SelectedItems`, `TryGetPosition`
u Shift+F10, glyf E8B3. 4 nové
msgid (3 plurály) i česky, `po/` skriptem ověřeným na `HEAD`. Ověřeno na
Macu: Go testy démona (`internal/core` včetně `TestForwardMany*`,
`pkg/api`, `go vet ./...`), čisté balíčky UI (`selection`, `contextmenu`,
`assistant`), `swift build` a celý `swift test` (1569, nové
`SelectionTests`, `severalRowsShowTheirCountAndFollowTheList`,
`selectedIDsOfSeveralRows`, `severalRowsTrashAndCountPerFolder`,
`severalRowsForwardAndOpen`), `check-strings.py`; GTK jen typová kontrola
bez cgo. Čeká: Toolbx `make build`, `go test ./internal/window/...`,
`make lint`, `make po` (beze změny), ručně v GTK (Ctrl/Shift-klik,
Shift+šipky, Ctrl+A, Escape, stránka počtu, akce, kontextové menu,
tlačítko ✦, hledání ve všech účtech, přeposlání, otevření oken); macOS
ručně debug binárkou nad kopií dat (totéž, ⌘A z menu Úpravy); Windows
`build.ps1 app`/`test`/`lint`, UI smoke a ruční průchod (totéž, Shift+F10,
sbalení sekce data s vybranými řádky).

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
GTK z výslovného pokynu vlastníka, výjimka z pravidla 6; Windows ji doplnil
2026-10-06, viz níže). Hlavní okno má dva režimy, Pošta (vše dosavadní)
a Nástěnka. Přepíná dvousegmentový přepínač (`envelope` / `square.grid.2x2`)
na začátku každého toolbaru a položky Pošta a Nástěnka na vrcholu menu
Zobrazení, s klávesami ⌘/Ctrl+1 (Pošta) a ⌘/Ctrl+2 (Nástěnka) od 2026-10-08;
režim při spuštění určuje `board-start-mode` (`mail` výchozí / `board` /
`last`, poslední režim je `board-last-mode`; Předvolby → Obecné → Nástěnka →
Při spuštění otevřít). Styl nástěnky při prvním zobrazení po spuštění určuje
klíč `board-default-style` (`last` výchozí, `list`/`columns`/`today`, neznámá
hodnota = Poslední použité; Předvolby → Obecné → Nástěnka → Zobrazení
nástěnky, v kódu „Board View“), `last` bere `board-last-style`, který se
zapisuje při každé změně stylu; po prvním zobrazení platí zvolený styl
(`BoardController.boardWillShow`, `Board.styleOnShow`). Filtr účtu se
pamatuje vždy (`board-account-filter`, použije se, jen když účet existuje). V Nástěnce je obsah okna (sidebar, seznam, čtení, panel
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
jednom ze čtyř stavů `hot` / `you` / `them` / `info` (česky Hoří, Čeká na
vás, Čeká na ně, Pro informaci od 2026-10-08; anglické msgidy Hot, Waiting
for You, Waiting for Them, For Your Information zůstaly). Stav dávají pravidla démona
(`internal/board`, čistý balíček bez storu a hodin, `RulesVersion` "7";
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
nebo nic neodpovídá a nad citací má méně než 300 B) případ sama nikdy
nedělá a uvnitř vlákna (od verze 6, hledá se jen mezi mými zprávami po
nejnovější příchozí, nejvýš deset) projde jako poznámka sobě.
**Poznámka sobě** (od verze 5) = moje zpráva, jejíž příjemci v `To`,
`Cc` a `Bcc` jsou aspoň jeden a všichni moje adresy kteréhokoli účtu
(`Identity.WithSelf`: vlastní adresa a odesílatelé složek Odeslané všech
účtů, v cache s identitou, jejich změna označí poštu nejdelšího okna);
smíšení příjemci ani zpráva bez příjemců poznámkou nejsou. Poznámka se
počítá (počet zpráv, vlajka, odpověď na ni), ale stav, datum, předmět
a výňatek případu dává nejnovější člen, který poznámkou není (od něj
běží okna); vlákno jen z poznámek případ nedělá. Moje odpověď poslaná
jen na jinou mou adresu tak cizí poštu před sebou nezakryje. Rozhodující
člen (nejnovější, který není poznámka ani vlastní přeposlání;
`Verdict.DecidingMessageID`, `DecidingMine`) je-li příchozí: pořadí
`hot.flagged` (od verze 6 platí, i když psal naposled já, a pak se nabízí
Odebrat hvězdičku), `info.yourNote` (od kterékoli mé adresy jen na mé
adresy), `hot.important` (jen známý odesílatel), `you.repliedToYou`,
`you.addressed` (známý), `you.newContact` (neznámý odesílatel, já v `To`;
štítek Nový kontakt), `info.unknownSender` (neznámý, nejsem v `To`),
`info.ccOnly`, `info.notAddressed`. **Známý odesílatel** = jeho `From`
nebo `Reply-To` je v `To`/`Cc` některé zprávy ve složkách `sent`/`outbox`
kteréhokoli povoleného poštovního účtu (nejnovější první, nejvýš 20 000,
čte se hodinově a při změně účtů) nebo mé zprávy ve vlákně; zpráva
mně v `To` od neznámého je `you.newContact`, jinak `info.unknownSender`, a její
`Importance` se nepočítá
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
dokud nepřijde done nebo nový), budoucí termín (jen se zapnutou preferencí `assistant`) a otevřený
závazek (po závazcích, které zavřel tentýž verdikt) drží případ, který by pravidla zahodila (`kept`); done znovu otevře jen
příchozí zpráva, kterou démon uložil po done a která přišla nejdřív den
před ním, a ne kopie zprávy, kterou případ měl už při done (`done_seen`;
tedy ne zfalšované `Date`, backfill ani přesun jiným klientem); done ruší
remind a zavírá otevřené závazky, remind ruší done; cokoli, co případ z done
vrátí (příchozí zpráva, `board.setDone {done:false}`, remind), závazky zavřené
done (`closed_reason='done'`, `closed_at >= done_at`) znovu otevře a příchozí
zpráva navíc smaže `user_state`; hotový případ je v
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
`<verze>:done`; do konce `ready: false`), `board_service.go` (17 metod
`board.*`, preference v `meta` `board.prefs`, ne `config.get`). Hodinová
údržba `boardUpkeep` ukončí běhy otevřené přes 2 h, maže běhy starší
90 dní, prošlé remindy, staré a osiřelé případy, a porovná role složek
s `meta` `board.roles`. API `pkg/api/board.go` (kompatibilní rozšíření
protokolu 2, chyby 1106 `caseNotFound` a 1506 `quoteNotFound`,
`notify.boardChanged`); Swift zrcadlo `MalachiCore/API/BoardAPI.swift`, C#
`windows/src/Malachi.Core/Api/Board.cs` (sestavené a otestované na Windows).

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
případ s cílem odpovědi, ne hotový, ne Pro informaci, účet umí odpovědět,
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
zůstane jako `// macOS-only string`, je anglicky. Windows klient nástěnku
má (viz níže) a msgidy nástěnky používá; v `windows/parity-exclusions.txt`
zůstaly jen popisy klíčů gschema (styl, režim, souhlas a model triage);
GTK „Show %d More“ nahradil `AndMore`, stejný řádek „… a dalších %d“ jako
Windows a macOS.

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
ručního otevření nástěnky. Zobrazení nástěnky je v Předvolbách → Obecné,
souhlas a rozvrh triage v Předvolbách → AI.

Windows klient má nástěnku (2026-10-06, `feat/board`, `docs/windows-port.md`
§11.8, `windows/README.md` → The Board): port macOS s GTK jako reference,
jmenné prostory `Malachi.Core.Boards` a `Malachi.App.Boards` (jmenný prostor
`Board` by skryl třídu `Board`), jádro v `Malachi.Core/Boards` a
`Controllers/Board*` (model, tři styly, `DaemonBoardSource`, triage a
automatická triage, návrh odpovědi, `BoardReplyPanes`), obsah okna Nová
zpráva přesunutý do sdíleného `Compose/ComposePane` s `DraftOwner.Board`,
okno s režimy Pošta / Nástěnka (`MainWindow.Mode.cs`, `Boards/`),
konverzační karty nad `CardWebView` (kanárek `board-card`), kroky ukončení
`QuitSteps.BoardReplies` a `StopTriage`, Předvolby → AI → Nástěnka
(`AiPage.Board.cs`); triage a návrh odpovědi jdou i přes zvoleného poskytovatele
ChatGPT/Codex (`board-triage-chatgpt-model`, vlastní verze souhlasu,
`docs/chatgpt-integration.md` §11). Agenti ho napsali, sestavili, otestovali
(Core testy v `windows/tests/Malachi.Core.Tests`) a prošli přes UI Automation
nad vzorovými daty (`MALACHI_BOARD_SAMPLES=1`) a účtem devmail; vlastník ho
zatím neověřil. Zbývá jemu: skutečný běh triage a návrhu odpovědi s Claude
Code i s Codexem, inline odpověď nad skutečným propojeným konceptem
(autosave, odeslání, zahození, ukončení s neuloženým textem) a ARM64.
Oprava klávesnice GTK (`board_keys.go`, `columns_key.go`: Sloupce a Dnes
berou klávesnici, Vlevo/Vpravo přes `board.SidewaysTarget`) je sloučená, ale
čeká na sestavení a `go test ./internal/window/...` v Toolbxu. Zbývá dál
ruční průchod GTK (všechny styly,
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
automatický), návrh odpovědi v GTK ani ve Windows klientu (ten agenti jen sestavili, otestovali a prošli nad vzorovými daty); migrace 0017 je
již zmrazená po provedení v ostrém storu vlastníka (viz níže).

Opravy nástěnky (2026-10-08, `feat/board`, necommitnuté; plán a audit
mimo repozitář; rozhodnutí vlastníka platí pro všechny tři klienty naráz).
Démon, **bez nové migrace** (0017 je zmrazená a každá migrace ve stromu
dojde na živá data vlastníka): `RulesVersion` "6" (vlastní přeposlání ve
vlákně projde jako poznámka a nezavře závazky, vlastní vlajka je `hot`
bez ohledu na to, kdo psal naposled, `you.newContact`, rozhodující člen
viz výše). **Remind**: příchozí započítaný člen uložený po nastavení
remindu, který tehdy nebyl členem a prošel týmiž třemi zkouškami jako
reopen z done, odložení zruší (případ je živý, `remindedAt` se
nenastaví); remind, který vypršel, vystaví `remindedAt` (jen u `live`),
klienti ho řadí první ve stavu se štítkem Připomenuto, bez notifikace, a
smaže ho každá akce uživatele (`board.setState`, `setDone`, `remind` i
s `null`, `archive`, `unflag`) i nová příchozí zpráva. Bez schématu to jde
dvěma zneužitými sloupci: `done_seen` nese za odložení první řádek
`~remind <razítko>` a id příchozích zpráv v té chvíli (`BoardCase.RemindSetAt`)
a `board_runs.day = 'lowerBound'` značí u neexterních běhů dolní mez
spotřeby; remindy nastavené před touto změnou poštou probuzeny nejsou a
komentář zmrazené 0017, že `day` externího běhu je den volajícího, je
chybný (je to místní den démona). Dále: `kept` se počítá po závazcích
zavřených týmž verdiktem; termín asistenta drží a řadí případ jen se
zapnutým `assistant` (i ve výpisu a prořezu); prořez maže dávno hotové
případy bez ohledu na `user_state`; `board.get` odvodí synchronně nejvýš
8 vlastních textů (zbytek z uloženého, doplní worker); známí odesílatelé
označí jen případy, kterých se změna týká; `BoardArchiveResult.moved`
(zpráva + zdrojová složka) pro Zpět; `usage.lowerBound` a
`usage24h.lowerBound`; `Draft.Text` čištěný jako ostatní řetězce. Most:
pod `--triage-run` `create_draft` **vynucuje** pravidlo návrhu odpovědi
(jen `replyMessageId` případu, `ruleReason` ∈ {`hot.important`,
`you.addressed`, `jira.assigned`, `jira.reporter`}, ne při `hasDraft`,
jeden na případ, pevná chyba bez textu pošty); opakovaná anotace téhož
případu nestojí slot `--triage-max`; fronta zveřejní případy až po
vykreslení; ztracená odpověď po `draft.save` znamená „koncept možná
existuje“ (bez opakování); `--reply-only` dál registruje čtecí nástroje
(omezení je allowlist klienta, u Codexu brána).

Klienti (GTK, macOS i Windows, texty v `ui/internal/board`, Swift a C#
porty s klíčem = msgid): štítky Připomenuto a Nový kontakt, řádek „Drží ho
vaše rozhodnutí“ v „Proč je to tady?“ u stavu uživatele (připnutí zůstává),
položka Odložené v navigaci (Hotovo jen hotové), fráze Dnes jen z nového od
včerejší půlnoci / termínu dnes / vráceného remindu, presety remindu bez
skoků večer a po půlnoci (Později dnes nejvýš 20:00, Dnes večer, Dnes ráno),
„Vrátit na nástěnku hned“ (dřív „Nepřipomínat“), Archivovat s tlačítkem Zpět
(`message.move` zpět a `board.setDone {done:false}`), ✦ Navrhnout
připomenutí u `them.*` (prompt žádá follow-up na vlastní zprávu), názvy
stavů česky Hoří / Čeká na vás / Čeká na ně / Pro informaci, klávesy
⌘/Ctrl+1 a 2 a E (Archivovat), D (Hotovo / Zpět na nástěnku), R
(Připomenout…) mimo textová pole (macOS jako položky menu včetně nového
menu Board, GTK akce `win.show-mail` / `win.show-board`), Escape
dvoustupňově (otevřený popup se zavře; klávesnice v editoru či polích
příjemců jde na pilulku stavu; jinde zavře panel Sloupců a Dnes, v Seznamu
nic; čistá pravidla `board.EscapeFor`), výběr se při změně stylu a zúžení
drží a pane odpovědi se stěhuje do panelu, odpověď v odesílání má při Konci
vlastní otázku („Ukončit, i když se odpověď ještě odesílá?“). Předvolby →
Obecné → Nástěnka: Zobrazovat nástěnku (vypnutá: přepínač režimu skrytý a
Pošta vynucená, vypne i automatickou triage), Zobrazení nástěnky, Při
spuštění otevřít, Ponechat případy po dobu (čtyři okna `windows`, 1–365
dní); Předvolby → AI → Nástěnka: Třídit tyto účty (`triageAccounts`, prázdné
= všechny povolené poštovní; poslední zaškrtnutý účet vypnout nejde) a
spotřeba „nejméně“ u zastavených běhů; souhlasy říkají, že model čte i
ostatní uloženou poštu a přílohy. Codex: selhání poskytovatele se mapují na
třídy (nenalezen, nepřihlášen, nový limit využití) a panel nabízí
Znovu připojit ChatGPT (GTK hotové; macOS E3 a Windows F2 psané); změna
poskytovatele ruší jen běh aktivního a zapíše předvolby jednou; `thread/start`
posílá `sandbox: read-only`, `approvalPolicy: never`, `environments: []`,
`ephemeral: true` (`docs/chatgpt-integration.md`), verze Codexu se jen
ukazuje v Předvolbách a není bránou.

Smazat na nástěnce (2026-10-15, všechny tři klienty;
rozhodnutí vlastníka přes AskUserQuestion). Detail případu má za
Archivovat tlačítko Smazat (msgid „Delete“), totéž v kontextovém menu
případu, v liště panelu Sloupců a Dnes, v toolbaru Seznamu a na macOS
v menu Board s ⌫ (v GTK a na Windows klávesa Delete; `board.DeleteRune`
= U+007F v `KeyFor`, mimo textová pole jako E, D, R). Dělá totéž co
Archivovat, jen do Koše a se **všemi přijatými zprávami případu**:
nová kompatibilní metoda démonu `board.trash` (`core/board_service.go`
`Trash`, `docs/api.md` §4.13) přesune přes `TrashMessages` (jako
`message.delete`) každého viditelného člena vlákna ze kterékoli složky
účtu kromě rolí sent, outbox, drafts a trash a kromě kopií vlastních
zpráv (`TwinOfMine`), pak případ označí jako hotový; `moved` pro Zpět
jako u archivace (ne u nesynchronizovaného koše), bez koše
`folderNotFound`, účet bez capability `delete` (Jira) `invalidArgument`
a klienti ho nenabízejí (`AccountInfo.CanDelete`, `Detail.CanDelete`;
na hotovém případu ano). Ptá se jen se zapnutou předvolbou Pošty
„Ptát se před přesunem do koše“ (`confirm-delete`; „Move to Trash?“,
tělo předmět případu), oznámení má Zpět (`ArchiveOutcome.Trash`, jiný
text při selhání vrácení). Reference `ui/internal/board` (`Trash`,
`Trashed`, `TrashQuestion`, `UndoTrashFailed`, `ActionTrash`,
`KeyTrash`), GTK `board_actions.go` (`win.board-trash`), `board_detail.go`,
`board_keys.go`, `board_page.blp` (`board_trash_button`); macOS
`BoardActions.trash`/`canDelete`/`confirmTrash`, `BoardToolbar`
`boardTrash`, `BoardDetailViewController` (v úzkém panelu ustoupí Smazat
první), `BoardCaseMenu`, `MainMenu` (⌫ jako „\u{8}“ sdílené s Message →
Move to Trash, které je na nástěnce vypnuté). 5 nových msgid, česky.
Ověřeno na Macu: Go testy démona celé (`TestBoardTrash*`,
`TestBoardTrashOnTheWire`), `ui/internal/board`, `boardreply`,
`boardtriage`, celé `swift test` (1571) a kontrola řetězců; GTK jen
typová kontrola. Čeká: Toolbx `make build`, `go test
./internal/window/...`, `make lint`, `make po`; ručně ve všech třech
(tlačítko, kontextové menu, klávesa, otázka podle předvolby, Zpět, Jira
bez Smazat) nad kopií dat.

Koncepty v konverzaci (2026-10-15, všechny tři klienty, commity `4b436b9`
a následující `feat: unsent drafts…`;
rozhodnutí vlastníka přes AskUserQuestion: Jira i pošta, okno komentáře
koncept ponechá, návrh z nástěnky v konverzaci ne). Neodeslaná odpověď
(u Jiry komentář, třeba z MCP `create_draft`) je karta v konverzaci
vlákna a řádek seznamu má červený štítek „Koncept“. Démon (kompatibilní
rozšíření protokolu 2, `docs/api.md` §4.4 a §5): `ThreadSummary.draftCount`
a `thread.get` `withDrafts` → `drafts` (jako `draft.list`, nejnovější
první, nejvýš `api.MaxThreadDrafts` 20); koncept patří vláknu přes
`in_reply_to` → `messages.thread_id` v kterékoli složce účtu
(`store/thread_drafts.go`, bez migrace), ne přeposlání, ne koncept
propojený s případem nástěnky, 0 ve složce Koncepty (kopie je tam
členem), v Outboxu a account-wide, virtuální pohledy Jira ano; nová
notifikace `notify.draftsChanged {accountId, threadIds}` (`core/drafts_notify.go`,
koalescence 250 ms, prázdné `threadIds` = kterékoli vlákno) po
`draft.save`, `draft.delete`, odeslání, smazání konceptu přesunem kopie
a propojení s případem. Reference `ui/internal/conversation/drafts.go`
(`DraftCard`, `Drafts`, `SameDrafts`, texty, `DraftsFirst`: karty hned pod
úvodní kartou, jinak první) a `IsConversationRow` počítá i `DraftCount`;
karty nejsou v `Model.Items` (nejsou členy, nečtou se, nesbalují se).
Karta: odesílatel = uživatel, štítek Koncept (+ Interní), „Koncept uložen
…“, prostý text, Upravit (`draft.get`, okno Nová zpráva / komentáře, nebo
vytáhne okno, které ho už edituje) a Zahodit (otázka, `draft.delete`),
dvojklik na záhlaví = Upravit. Okno komentáře se při zavření chová jako
e-mail (`closesUnasked`, `deletesOnClose` jen u prázdného komentáře,
otázka Uložit koncept / Zahodit / Zrušit, `forgetLate`). GTK:
`window/conversation_draft.go`, `conversation_view.go` (`apply`),
`convWithDrafts`, `thread_model.go`, `widget/message_row.go` +
`message_row.blp` (`draft_pill`), `compose/draft.go`; macOS:
`MalachiCore/Model/ConversationDrafts.swift`, `ConversationController.drafts`,
`ListController.refreshDrafts`, `ActionsController.editThreadDraft`/
`discardThreadDraft`, `MalachiMail/MessageView/ConversationDraftCardView.swift`,
`MessageCellView` (`draftPill`), `ComposeDraftController` (`LateSave`).
Windows: `Malachi.Core/Model/Conversation.Drafts.cs`, `ConversationDraftCard.cs`,
`ConversationController.Drafts`, `MailboxController.RefreshDrafts`,
`ActionsController.EditThreadDraft`/`DiscardThreadDraft`,
`ComposeDraftController` (`ClosesUnasked`, `DeletesOnClose`, `ForgetLate`;
Konec uloží komentář s textem), App `Reader/Conversation/ConversationDraftView.cs`
(karta z kódu), `IssuePill.IsDraft`, `DraftPill` v šestém sloupci řádku.
1 nový msgid (`msgctxt "conversation"` „Draft“, česky Koncept). Ověřeno na
Macu: Go testy démona (`TestThreadDraftMembers`, `TestThreadDrafts`,
`TestDraftsChangedCoalesces`, `TestJiraComments`), `ui/internal/conversation`,
celé `swift test` (`ConversationDraftsTests`, `draftsAreDraftCards`,
`threadDrafts`, Jira compose testy zavírání), kontrola řetězců, `po/`
skriptem ověřeným na `HEAD`; GTK jen typová kontrola bez cgo (Swift
testy na tomto Macu potřebují `-Xswiftc -plugin-path -Xswiftc
/Library/Developer/CommandLineTools/usr/lib/swift/host/plugins/testing`).
Čeká: Toolbx `make build`, `go test ./internal/window/...
./internal/compose/...` (`TestConversationDraftsAreDraftCards`,
`TestOnlyAnEmptyCommentGoesWithTheWindow`), `make lint`, `make po` (beze
změny); Windows (napsané agentem bez `dotnet`) `build.ps1 app`/`test`/`lint`;
ručně ve všech třech nad kopií dat: koncept komentáře z MCP v konverzaci
issue, Upravit, zavřít s uložením, Zahodit, odeslat, odpověď na e-mail
z MCP, štítek v seznamu.

Zobrazení konverzace jako chat (2026-10-17, větev `feat/chat-view`,
necommitnuté; plán `docs/plans/chat-view.md`, D1–D17, rozhodnutí vlastníka;
výjimka z pravidla 6: macOS hotové a schválené vlastníkem v bráně B1, GTK
napsané, Windows napsané bez překladu). Konverzace
má druhé rozložení vedle časové osy (ta zůstává beze změny a výchozí): cizí
zprávy jako bubliny vlevo, moje a odpovědi z Odeslaných vpravo, koncepty na
konci, události Jiry a řádek starších zpráv uprostřed, od nejstarší, otevřeno
na konci. Bublina je jen text (prostý `text` z `message.body` s `trimQuoted`,
nevybíratelný, bez tlačítek, odkazů a webového pohledu); dvojklik otevře okno
zprávy (koncept okno Nová zpráva); těla se žádají jen poblíž viditelné části.
Přepínač v pevném pruhu nad konverzací („Show as Chat“, zapnutý „Show as
Thread“; popisek i tooltip podle stavu) je trvalý (gschema
`conversation-chat-layout`, výchozí vypnuto, bez řádku v Předvolbách; Go
`settings.Store`, macOS `Settings`, Windows `SettingsStore`). Démon a API
beze změny. Reference Go `ui/internal/conversation/chat.go` (`ChatRows`,
`ChatScrollTo`, `BubbleText`, 4 nové msgid); macOS `MalachiCore/Model/
ConversationChat.swift` (port) a `ConversationChatBody.swift` (Swift-first),
`ConversationLayout.chatBubbleMax`, hostitel `MessageView/
ConversationHostViewController` (jediný odběratel kontroleru, `trim` jen
pro zobrazené rozložení, jedna karta issue na rozložení) s pruhem
`ConversationStripView` a chat `ConversationChatViewController`,
`ConversationChatRow`, `ConversationBubbleView`. macOS ověřeno na Macu: Go
testy `ui/internal/conversation`, celé `swift test`, `check-strings.py`,
izolovaný start; nálezy vlastníka z B1 opravené (odsazení pruhu, popisek
přepínače podle stavu, sponka v bublině jako pilulka s počtem).
GTK (`ui/internal/window`, G1–G2, bez překladu): hostitel
`conversation_host.go` (`conversationHost` nad `convFanout` z
`conversation_fanout.go`: jediný odběratel kontroleru, `takeMarkRead` jednou
při `convOpened` pro obě rozložení, `trim` jen s blízkou množinou zobrazeného
rozložení, jedna karta issue na rozložení přes `convIssueSlot`, spinner a
stavový popisek odkazu v hostiteli, mezerník přes `pageBy`); pruh v
`conversation_view.blp` (vlastní `Adw.Clamp` 900 px s okraji sloupce,
`conversation_fold_all` a `ToggleButton conversation_chat_toggle` s
`Adw.ButtonContent` a ikonou `malachi-chat-symbolic`, nový
`ui/data/icons/malachi-chat-symbolic.svg` instalovaný v `scripts/build.sh`;
přepínač vázaný `settings.Bind`, rozložení se přepíná z `OnChanged`, takže i
změna odjinud; není plochý, zaškrtnutý stav je nativní, D9) a `Stack` se
stránkami časové osy a chatu (skryté rozložení zůstává naživu, takže sbalení
a odhalená citace přežijí návštěvu chatu); chat `conversation_chat.go`
(`conversationChat`), `conversation_chat_row.go` (`chatRow`/`chatBubble`/
`chatCenterRow`, všechno z Go, žádný `conversation_chat.blp`; bublina v
`Adw.Clamp` v pixelech vedle roztažného spaceru, strop
`convChatBubbleMax(column.Width())` ze šířky potomka clampu, záloha
`convChatColumn`; `GtkLabel` s `SetUseMarkup(false)` a `SetSelectable(false)`;
`GestureClick` n == 2 → `convOpenMessage`/`convEditDraft` na rozhraní
`convHost`, těla jen v blízkém okně přes `liveCards`) a
`conversation_chat_layout.go` (Swift-first pravidla `chatBody`, `bubbleBody`,
`bubbleMonospace`, `convChatAtEnd`, `convChatCompactDates`; `convChatResize`
je otestovaný port a pohled ho nepoužívá; `conversation.ChatScrollTo` se
nevolá, rolování na konec dokumentu je rovnocenné). Úpravy časové osy:
`conversation_view.go` hlásí hostiteli sbalení, `conversation_controller.go`
(`convHost` + `convOpenMessage`/`convEditDraft`), `conversation_issue.go`,
`conversation_card.go` (`loadingMessageError`), `conversation_rows.go` (CSS
`.chat-bubble`, `.chat-bubble-mine`, `.chat-placeholder`, `.chat-proportional`,
`.chat-clip-pill`), `window.go`. Testy: jednotkové nad falešným `convWindow`
(`conversation_chat_test.go`, `conversation_chat_layout_test.go`) a
`MALACHI_GTK_SMOKE=1 go test ./internal/window -run
'^TestConversationChatGTKSmoke$'` (skutečný Blueprint, obě rozložení,
přepnutí). Ověřeno tady `gofmt`, `gopls check`, typová kontrola `go/types`
s `FakeImportC`, revize kódu a ruční běh workflow `rpm.yml` na větvi
(2026-10-18: GTK se ve Fedoře přeložilo pro x86_64 i aarch64 po opravě
jednoho středníku v Blueprintu); testy `internal/window` včetně smoke testu
nikde neběžely. Čeká (G3 v kontejneru): `gtk.sh test-ui`, `make po` (má
změnit jen odkaz `conversation_view.ui:77` v `po/malachi.pot`) a `make
lint`; pak B2 vlastníka v XQuartz. Windows (W1–W2, `docs/windows-port.md` §11.7 „The chat layout“,
`docs/plans/chat-view.md` §8): Core `Malachi.Core/Model/` `ChatSide`,
`ChatRowKind`, `ChatRow`, `Conversation.Chat.cs` (port `chat.go`, 4 msgid už
nejsou v exclusions), Swift-first `Conversation.ChatBody.cs`, `BubbleBody`,
`BubbleAttachments`, `ConversationLayout.Chat.cs`/`.ChatResize.cs`, glyf
`malachi-chat` E8F2, `SettingsStore.ConversationChatLayout` (44 klíčů). App
`Reader/Conversation/`: `ConversationView` je hostitel (pruh v řádku 0 s
`FoldAllButton` a `ToggleButton ChatToggle`, obě rozložení v řádku 1, jediný
odběratel `Changed`/`EntryLoaded`, `Trim` jen zobrazeného rozložení, karta
issue na rozložení přes `ConversationIssueBox`, jeden spinner), dosavadní
časová osa je `ConversationTimelineView` (`HeadViews` 1), chat
`ConversationChatView` (`VerticalAnchorRatio` 1, otevření na konci, držení
konce a místa čtení, `ChatResizeFor`, blízká těla přes `LiveCards`),
`ConversationChatRow`/`ConversationChatCenterRow`/`ConversationBubbleView`
(`TextBlock` bez výběru, dvojklik přes `AfterRelease`), štětce nádechu ve
`ConversationStyles.xaml`. Odchylky (`windows/README.md`): kotva dole,
skrytá časová osa ořezává i zásobník webových pohledů, přepínač s nádechem
zvýraznění. Testy `ConversationChatTests`, `ConversationLayoutTests`,
UI smoke `ConversationChatSmokeTests` (`MALACHI_DEVMAIL`). Napsáno bez
`dotnet`; ruční běh `windows.yml` na větvi (2026-10-18, po doplnění
`using Malachi.Core.Presentation`) sestavil x64 i ARM64 a prošel testy Core
(7 462), konvencí (27, včetně glyfů a pokrytí msgid) a Platform až na
`RealDaemonTests.HandshakeCallAndNotification`, který na runneru padá i na
`main` (prostředí), takže krok Lint neběžel; backend `go test` na Windows
padá od sloučení kalendáře i na `main` (`calendar/tz` a `TestInvitationCard`,
nesouvisí s chatem). Čeká `build.ps1 lint`, UI smoke s `MALACHI_DEVMAIL`,
ruční průchod (viz sekce v `windows-port.md`) a B3.

Archivace v Poště = Hotovo (2026-10-13, `feat/board`, necommitnuté; jen
démon, klienti beze změny — všechny tři to dostanou přes
`notify.boardChanged`). Vlastník archivoval vyřízenou poštu v Poště a
případy zůstaly: pravidla počítala člena v Archivu jako kteréhokoli
jiného. `RulesVersion` "7": verdikt říká, kde příchozí zprávy leží
(`Archived` = žádná kopie v Doručené a nejnovější v Archivu,
`NewestInboundInInbox`), store (`DrainBoard`) z toho čte **přechody**
proti poslednímu hodnocení (`can_archive`, `reply_folder_id`; bez
migrace): živý nebo odložený případ, jehož vlákno uživatel archivoval
kterýmkoli klientem (i telefonem, přesun přijde jako smazaný a nový
řádek), je hotový jako po `board.archive` (remind pryč, závazky zavřené
`done`, stav uživatele zůstává); nejnovější příchozí zpráva, kterou
případ měl při Hotovo, vrácená do Doručené ho vrátí jako `board.setDone
{done:false}` (stav zůstává, závazky zavřené Hotovem se otevřou);
„Vrátit na nástěnku“ drží, i když se archivované vlákno hodnotí znovu
(přečtení, hvězdička); zpráva, která přišla už archivovaná (vyřízená
na telefonu), hotový případ neotevře ani remind neukončí, dokud není
v Doručené; už archivované vlákno nový případ nedělá; případy z pravidel
před 7 se při prvním přepočtu uzavřou (`boardRulesSawArchive`); případ
`them` archivovaný po odpovědi je hotový taky (došlá odpověď ho otevře);
Gmail (All Mail nesynchronizované) dál končí jako osiřelý případ.
Dokumentace `docs/api.md` §4.13 „Archived threads“, `docs/architecture.md`
§3.7 a §7. Testy zelené na tomto Macu: `TestArchiveState` (pravidla),
`TestBoardArchiveInMailView` (store i core), celé `internal/board`,
`pkg/api`, most a `store`/`core` s filtrem Board. Vlastník ověří nad
kopií storu: první start přepočítá všechny případy a už archivované
uzavře (objeví se v Hotovo), pak archivaci v Poště, Vrátit na nástěnku
a přesun zpět do Doručené.

Stav ověření oprav (2026-10-08, upřímně). Na tomto Macu proběhly a jsou
zelené: `go vet` a Go testy démona (`pkg/api`, `internal/board`, most
`malachi-mcp`, `internal/rpc`, `store` a `core` s `-run 'Board|Merge|Thread'`),
Go reference `ui/internal/board`, `boardtriage`, `boardreply`, `assistant`,
`assistantpanel`; `swift build` celého balíčku bez varování a filtrované
testy `MalachiCore` (625, filtr Board/Triage/Settings/Assistant/ChatGPT);
`make po` v Dockeru (`cs.po` bez fuzzy a nepřeložených), Blueprint a
`glib-compile-schemas` tamtéž. Tři revize (Go, macOS, GTK + Windows čtením)
proběhly a jejich nálezy jsou opravené (mimo jiné: Zpět po archivaci
na Gmailu se nenabízí, protože All Mail není synchronizovaný a `moved`
chybí; odstraněný účet v `triageAccounts` už neblokuje zápis předvoleb;
znovuotevření poštou otevře i závazky zavřené Hotovem; výběr při změně
stylu zůstává jen u případu, který uživatel sám vybral nebo má živý pane;
Escape nechá přednost popoverům; Ctrl/⌘+1/2 podle fyzické klávesy kvůli
české QWERTZ). GTK `ui/internal/window`, `compose`, `settings`, `widget`,
`style` a `chatgpt` jsou napsané naslepo, prošly `gofmt`/`gopls` a ručně
spuštěný workflow `rpm.yml` na `feat/board` je přeložil v kontejneru
Fedora (x86_64 i aarch64, `scripts/build.sh`), testy `internal/window`
ale nespustil; Windows C# a XAML byly napsané naslepo a ručně spuštěný
`windows.yml` je po třech opravných commitech (`1ffbcd3`, `79ae4d2`,
`b131547`: CA1708 u bloků `extension`, CS8625, CA1822, vzorky round-trip
API, pořadí navigace, `lowerBound`) sestavil, otestoval, zlintoval a
zabalil pro x64 i ARM64 — na skutečném Windows zbývá UI smoke a ruční
průchod. Nespuštěno: `TestBoardDryRun`, `codex_linux_test.go` (jen Toolbx),
fuzz cíle, celé sady, aplikace macOS (izolovaný start visel na čtení
keychainu v `AppState.init`: `ChatGPTKeychain.tokens()` ignoruje
`MALACHI_DATA_DIR`, rozhodnutí vlastníka čeká), takže rozložení okna
(oddělovač toolbaru po Pošta → Nástěnka → Pošta, pane v panelu, Dnes bez
kalendáře, kapsle Zpět, menu Nástěnka s D/R/E, monitor ⌘1/⌘2) se neměřilo.
Čeká na vlastníka (CP2): macOS nad kopií dat s vlastním socketem — první
start přepočítá nástěnku (pravidla v7) — Odložené, Připomenuto po vypršení,
probuzení poštou, Nový kontakt, Zpět u Archivovat (u Gmailu bez Zpět),
Escape dvoustupňově, ⌘1/⌘2/E/D/R, pane při změně stylu a zúžení, Předvolby
(Zobrazovat nástěnku, Zobrazení, Při spuštění, Ponechat případy, Třídit
tyto účty), Konec s neuloženou a odesílající odpovědí, Znovu připojit
ChatGPT v panelu; (CP3) v Toolbxu `make build`, `go test
./internal/window/... ./internal/settings/... ./internal/chatgpt/...
./internal/boardtriage/...`, `make lint`, `make po`, smoke testy
`MALACHI_GTK_SMOKE=1`, ručně Escape s otevřenými popovery, kontextové menu
Archivovat, Ctrl+1/2 na české klávesnici, R v úzkém Seznamu; na Windows
`build.ps1 app`/`test`/`lint` a smoke; skutečný běh triage a návrhu
odpovědi s Claude Code i Codexem. GTK nemá okno zkratek
(`win.show-help-overlay` ukazuje nikam), takže Ctrl+1/2 a E/D/R nejsou
nikde vypsané (rozhodnutí vlastníka).

Kalendář (stav 2026-10-17, větev `feat/calendar`; návrh a poznámky „As
built“ každého úkolu v `docs/calendar.md`, rozhodnutí D1–D32 a odpovědi
vlastníka v jeho §1.2 a §7, shrnutí v `docs/architecture.md` §3.8 a §7,
bezpečnost `docs/security.md` §4.3). Démon a macOS klient jsou hotové
a vlastníkem ověřené nad skutečnými účty (M365 přes Graph i Nextcloud
přes CalDAV); GTK klient je hotový a vlastník ho prošel proti svým účtům
v okně XQuartz (viz GTK klient kalendáře níže); Windows klient je
portovaný, sestavený a otestovaný na Windows a vlastník ho zkoušel nad
svými účty M365 a Nextcloud (viz Windows klient kalendáře níže). Oba
porty přišly až po macOS (výjimka
z pravidla 6 jako u nástěnky; `.blp` a Go reference `ui/internal/calendar`
jsou mustr, macOS zdroj portu). Zdroje: Microsoft 365
přes Graph na existujícím účtu `kind: graph` (přepínač `calendar.sync`,
výchozí vypnuto i u stávajících účtů) a nový druh účtu `caldav` jen pro
kalendář (capabilities přesně `["calendar"]`, žádné složky, každá metoda
zpráv `invalidArgument`, v Poště se nikdy neukazuje; klienti ukazují
v Poště jen `imap`, `graph`, `jira`); Google později pod účtem Gmail
dalším providerem bez změny kontraktu. Kompatibilní rozšíření protokolu 2
(`docs/api.md` §4.14–4.16: `calendar.*` 7, `event.*` 9, `invitation.*` 3,
`account.discoverCalDAV` a `account.listCalDAVCalendars`, notifikace
`notify.calendarChanged`/`calendarState`/`eventAlert`/`invitationsChanged`,
chyby 1006, 1007, 1107, 1108, 1204, 1306, 1507, 1508, limity
`pkg/api/calendar.go`). Migrace `0020_calendar.sql` (nevratná, jen nové
tabulky `cal_*` a FTS5 `cal_events_fts` přes `cal_events_fts_docs`, žádný
sloupec ani trigger na existujících tabulkách, na `cal_events` nikdy
`INSERT OR REPLACE`); kalendářová data jsou cache serveru, vypnutí sync,
jiný server či uživatel nebo odebrání účtu je smaže (`dropCalendarCache`),
autoritativní jsou jen sloupce uživatele (`visible`, `user_color`,
`cal_responses`, stav upozornění, meta `calendar.prefs`). Balíčky:
`internal/calendar` čistý (`ics` vlastní parser s limity, `tz` s tabulkou
Windows zón vygenerovanou z CLDR `windowsZones.xml` a `SetPreferred`,
`recur` nad `teambition/rrule-go` (MIT, jediná nová závislost) s validací
pravidla a `UNTIL` oříznutým na okno, `imip`, `text.go`, `freebusy.go`),
`internal/calsync` (engine: `Provider`, syncer na účet, okno
`today − keepDays` až `today + 400`, materializované `cal_occurrences`,
upozornění, pro `caldav` zároveň `SyncSupervisor` druhu, Graph běží vedle
pošty přes `graphWithCalendar` s vlastním `CalendarSyncState`), vlastní
CalDAV klient `internal/caldav` (+ `caldavtest` s profily serverů),
`graph/calendar*.go` (delta per kalendář s fallbackem `list`, jeden
`graph.NewLimiter(4)` na účet sdílený s poštou a outboxem), `core`
(`accounts_caldav.go`, `calendar_service.go`, `event_service.go`,
`calendar_freebusy.go`, `invitation_service.go`, `calendar_imip.go`,
`calendar_backfill.go`). Zápisy jsou server-first (offline = chyba,
konflikt = verze serveru v `error.data`), iMIP staví démon a řadí do
existujícího outboxu poštovního účtu; CalDAV s `calendar-auto-schedule`
posílá pozvánky sám. Pozvánky: hook `Stored` (`noteInvitation`) přečte
nejvýš dvě kalendářové části do `cal_invitations` ještě před
`notify.newMessage`, `ingest.Decide` je nikdy nenechá na serveru, starší
pošta v `offlineDays` se projde v `core.Maintain` (`calendar.invites.scan`);
odpověď jde jen na uložený `ORGANIZER`, přijatý REPLY mění jen účastníka,
kterým je From, CANCEL nikdy nemaže, Koš a Nevyžádaná nic nepoužijí.
Zpráva s kalendářovou částí není počítaný člen případu nástěnky
(`store.boardMembersFrom`, `board.RulesVersion` "8", první start nad
storem přepočítá nástěnku). Microsoft: kalendářové scopes jen při
přihlášení pro kalendář (`account.oauthStart` `calendar: true`), udělený
scope uložený v samostatné položce keyringu `oauth2.scope`
(`oauth2flow.KeyGrantedScope`; refresh token zůstává holý, chybějící položka
= jen pošta, maže se s účtem), bez něj stav `consentRequired`
a pošta běží dál; GOA `CalendarDisabled` jako `MailDisabled`. CalDAV:
heslo jen na origin `serverUrl` a na origin principalu a home setu na téže
registrovatelné doméně přes https (`calDAVOrigin`, iCloud `pNN-caldav`
projde), objevování bez přihlašovacích údajů, pin certifikátu jen pro
hostitele `serverUrl` (`calDAVTransport`; CalDAV je jediný HTTP klient
s pinem), realm = host[:port] + cesta `serverUrl`. Zóna démona
z `MALACHI_TIME_ZONE` (nastavuje ji macOS i Windows klient), pak `TZ`,
pak `/etc/localtime` (`cmd/malachid/timezone.go`). Most MCP
(`tools_calendar.go`, `docs/mcp.md` Calendar): `list_calendars`,
`list_events`, `get_event`, `find_free_time`, `list_invitations` jen čtení
(ne pod `--reply-only` a `--triage-run`), `create_event` za
`--allow-modify`, s účastníky navíc `--allow-send`; odkaz na videohovor
nikdy nevrací. Čistá logika klientů a všechny texty jsou v Go
`ui/internal/calendar` (U1), režim Kalendář je třetí hodnota
`board.Mode` (`ModeCalendar`, gschema `board-start-mode` += `calendar`,
klíč `calendar-view`), rychlé akce asistenta v `ui/internal/assistant`
(`PanelActionsWith`). Oznámení třetích stran: rrule-go a data CLDR jsou
v `THIRD-PARTY-NOTICES.md` (`docs/releasing.md` §9). Z živého testu vlastníka (2026-10-16): provider hlásí verzi dat
(`calsync.Versioned`, meta `calendar.provider.<účet>`, Graph 2) a při její
změně engine kalendáře účtu vypíše znovu celé (delta by opravené mapování
do uložených řádků nedostala); `calendar.ParserVersion` (3) stejně
přestaví uložené karty pozvánek; odpověď daná v jiném klientu se bere
z propojené události (D29), `InvitationPrevious` nese i místo
a opakování (D30), vlastní pozvánka je `ownEvent` a vyřízená (D32), karta
REPLY nese odpověď účastníka, zjišťování CalDAV bere 401 bez hlavičky
`DAV` jako kandidáta (Nextcloud). Nevyzkoušeno živě: `TestBoardDryRun`
s pravidly "8"; C# zrcadlo API (`windows/src/Malachi.Core/Api/Calendar.cs`)
je od Windows portu přeložené a otestované na stroji s Windows (viz níže).

macOS klient kalendáře (2026-10-16, `feat/calendar`; M1–M11 podle
`docs/calendar.md` §5 a jeho poznámek „As built“, ověřený vlastníkem): zrcadlo API
(`MalachiCore/API/CalendarAPI.swift`, `CalendarNotification` zvlášť vedle
`DaemonNotification`, hub v `App/Integration+Calendar.swift`), čistá logika
jako port `ui/internal/calendar` ve jmenném prostoru `Cal`
(`MalachiCore/Calendar/*`, `Calendar` by stínil Foundation), zdroj dat
`CalendarDataSource` (`DaemonCalendarSource`, `InMemoryCalendarSource` +
`CalendarSamples`, `MALACHI_CALENDAR_SAMPLES=1`), kontrolery
`Controllers/Calendar*`, `EventDetailController`, `EventEditorController`,
`InvitationController`, `CalDAVWizardController`, `CalendarAlertsController`;
třetí režim okna `Board.Mode.calendar` (⌘3, `board-start-mode` += `calendar`,
klíč `calendar-view`), toolbar `Calendar/CalendarToolbar.swift` (přepínač
postranního panelu je ve všech třech toolbarech hned za sledovacím
oddělovačem, protože třísegmentový přepínač režimu sekci sidebaru vyplní —
odchylka v `macos/README.md`), stránka `CalendarPageViewController` se
sidebarem (minikalendář, Pozvánky ▸ Nezodpovězené, kalendáře s kapslí
druhu), pohledy týden/den (`CalendarTimeGridView`), měsíc, agenda, hledání;
popover detailu (`EventDetailPopover.show(…)`), okno Nová událost
(`EventEditor/*`, `EventEditorManager.shared.openNew/openEdit`), karta
pozvánky v Poště (`MessageView/InvitationCardView.swift`, štítek
`Shared/InvitationPill.swift`, `MainWindowController+ShowInCalendar`),
Předvolby (`Preferences/CalendarAccountGroups.swift`, General → Calendar),
sheet `AccountWizard/CalDAV/CalDAVSheetController.swift`
(`AppState.showAddCalDAVCalendar(from:)`), pruh „Dnes v kalendáři“
(`Board/BoardTodayCalendarStrip.swift`), akce asistenta
(`MalachiCore/Assistant/AssistantCalendar.swift`), oznámení (kategorie
EVENT_ALERT/EVENT_ALERT_JOIN/INVITATION/CANCELLATION), `MALACHI_TIME_ZONE`
z `DaemonSupervisor`, `Paths.isIsolated` (izolovaná instance klíčenku
ChatGPT nečte ani nepíše). Rozložení změřené debug binárkou (`MALACHI_START`
kroky `calendar:day|week|month|agenda`, `event-popover`, `event-editor`,
`invitation-card`, `caldav-sheet`; `LAYOUT:` řádky) odpovídá wireframům
a vlastník prošel brány 1 i 2 (`docs/calendar.md` §6.3) nad skutečnými
účty; po jeho nálezech přibyly pruhy vícedenních událostí v měsíci
(`MonthLanes` → `Cal.monthLanes`), neprůhledné bloky, agenda vždy od
dneška (`SwitchAnchor`), samostatná záložka Předvolby → Kalendář
(`CalendarPaneViewController`), celodenní upozornění ve dnech v 9:00
(D31), Smazat v patičce okna Upravit událost (`EditorDelete`), vypnuté
kalendáře jen v Předvolbách (`ActiveCalendars`) a vlastní banner listu
CalDAV. Žádný `// macOS-only string` kalendář nezavedl; `po/cs.po` je
úplné (1462). Strftime msgid musí mít `// xgettext:no-c-format`, jinak
katalog macOS ukáže „%-ld“.

GTK klient kalendáře (2026-10-17, `feat/calendar`, necommitnuté; úkoly
G1–G19 podle `docs/calendar.md` §6.5, poznámky „As built (G…)“ v jeho
§5.12): port macOS klienta se stejným rozložením (sidebar 240 px
s minikalendářem, Pozvánky ▸ Nezodpovězené a kalendáři s kapslí druhu;
týden/den 60 px na hodinu s neprůhlednými bloky; měsíc s pruhy přes dny;
agenda od dneška; hledání; popover detailu 380 px; okno Nová/Upravit
událost 760×800 se Smazat vlevo; karta a štítky pozvánek v Poště;
Předvolby → Kalendář jako `Adw.PreferencesPage`; dialog Přidat kalendář
CalDAV; pruh „Dnes v kalendáři“ na nástěnce; akce asistenta; notifikace
s tlačítky). Čisté balíčky bez gotk4: `ui/internal/calendardata`
(`DataSource`, `DaemonSource` nad `board.Caller`/`board.Loop`,
`InMemorySource` se vzorovými daty `MALACHI_CALENDAR_SAMPLES=1`)
a `ui/internal/calendarctl` (porty Swift kontrolerů: `CalendarController`,
`EventDetailController`, `InvitationController`, `EventEditorController`,
`CalDAVWizardController`, `CalendarPreferencesController`,
`CalendarAccountController`, `CalendarAlertsController`; `Options`
zmrazené, `Answer` v `detail.go`); pohledy v `ui/internal/window/calendar*.go`,
`event_editor*.go`, `invitation_card.go`, `preferences_calendar.go`,
`board_today_calendar.go` a Blueprinty `calendar_page.blp`,
`event_detail.blp`, `event_editor.blp`, `invitation_card.blp`,
`caldav_sheet.blp`; CSS v `ui/internal/style/calendar*.go` (třídy
`.cal-c-N`, barvy ověřené regulárem, výplně `mix()`); třetí režim okna
`board.ModeCalendar` (Ctrl+3, Ctrl+Shift+1–4 pohledy, klíč
`calendar-view`); bloky týdne jako tlačítka v `Gtk.Overlay` nad
`DrawingArea`, pruhy měsíce přes sloupcové rozpětí `Gtk.Grid`. Texty jen
z `ui/internal/calendar` přes `i18n.Tr` (žádný nový msgid; do
`po/POTFILES` přibyly `calendar_detail.go`, `event_editor_popovers.go`,
`preferences_calendar.go`, `calendarctl/account.go`). Odchylky od macOS
v §5.12 (hlavičkové lišty místo unified toolbaru, hledání jako
`Gtk.SearchBar`, dialogy `Adw.Dialog`/`Adw.AlertDialog`, notifikace
s tlačítky, `mode_stack` nehomogenní). Sestaveno a otestováno v kontejneru
Fedora 42 na Macu (`scripts/gtk-container/gtk.sh make build` / `test-ui`,
celá sada `ui/` zelená, smoke testy `MALACHI_GTK_SMOKE=1` pro každý pohled
a okno) a prohlédnuto přes Broadway (`gtk4-broadwayd` v kontejneru,
`GDK_BACKEND=broadway`, vývojové kroky `MALACHI_GTK_START` jako
`MALACHI_START` na macOS: `calendar:week|day|month|agenda`, `event-popover`,
`event-editor[:edit]`, `caldav-dialog`, `prefs-calendar`, `size=WxH`,
`quit`). Vlastník prošel GTK klienta 2026-10-17 proti svým účtům v okně XQuartz
(démon nad kopií dat): podle jeho nálezů přibylo propojení pozvánky na
výskyt s řádkem Graphu podle času a odpověď z `event.respond` pod
týmž klíčem (`store.InvitationLinkedEvent`), mapování výskytů Graphu se
`seriesMasterId` jako výskytů řady (`graphDataVersion` 3), přestavba
sidebaru a stránky Předvoleb jen při změně struktury (ne při každé změně
stavu — jinak widgety mizí pod kurzorem). Do `windows/` GTK port nesáhl
(Windows port běží jinde).

Windows klient kalendáře (2026-10-16, `feat/calendar`; port macOS klienta
podle pravidla 6 a `docs/calendar.md` §6.4, napsaný týmem agentů na stroji
s Windows 11 a .NET SDK 10.0.400; poznámky „As built on Windows“ W2–W9
v `docs/calendar.md` §5, `docs/windows-port.md` §11.9, odchylky v tabulce
`windows/README.md` a jeho oddíl The Calendar): zrcadlo API
`Malachi.Core/Api/Calendar.cs` (Go `Event` = `CalendarEvent`, `EventID`
a `AlertID` = `CalendarEventId` a `CalendarAlertId`, `Date` = `CalendarDate`,
`TimeZone` = `TimeZoneName`; vložené `Occurrence` a `InvitationSummary`
jsou základní záznamy `CalendarEvent` a `InvitationCard`), `GoContractTests`
nově hlídá i směr Go → C# (`EveryEnumOfCalendarGoIsAWireEnum`,
`EveryStructOfCalendarGoHasARecord`; první překlad našel chybu ve vzorcích
round-trip: oddělovač raw stringu C# spolkl první uvozovku). Čistá logika
je port `ui/internal/calendar` ve `public static partial class Cal`
ve jmenném prostoru `Malachi.Core.Calendars` (jmenný prostor `Calendar` by
skryl `System.Globalization.Calendar`; typy podle Swiftu, funkce podle Go):
`Cal.cs` (`format.go` a `view.go`: `Env` se `StrftimeWriter`,
`EnglishStrftime` a `CultureStrftime` nad `I18n.Strftime`, `View`,
`Range` nesoucí zónu, `ViewRange`, `Step`, `SwitchAnchor`, `WeekStart`,
`WeekNumber`, `Title`, `Subtitle`, dny nad `DateTimeOffset`/`TimeZoneInfo`,
`OccurrenceKey`), `CalendarText.cs`, `CalendarWeekLayout.cs`,
`CalendarMonthGrid.cs`, `CalendarMonthLayout.cs`, `CalendarAgenda.cs`,
`CalendarToday.cs`, `CalendarDetail.cs`, `CalendarRecurrence.cs`,
`CalendarEditor.cs` (`IanaZoneName` Windows → IANA),
`CalendarAvailability.cs`, `CalendarInvitation.cs`,
`CalendarInvitationReading.cs` + `Model/InvitationReading.cs` (Swift-first),
`CalendarNotificationText.cs`, `CalendarPreferences.cs` (`Cal.Prefs`),
`CalendarKeys.cs` (`Press.FromVirtualKey`: Shift zastupuje ⌥ a ⌃ reference,
Nová událost je Ctrl+Shift+N, pohledy Ctrl+Shift+1–4), `CalendarActive.cs`,
`CalDAVWizard.cs`, `MiniMonth.cs`, `CalendarSamples.cs`; zdroj
`ICalendarDataSource` (`Cal.Snapshot`, `Cal.SourceChange` s případy
`SnapshotChanged`/`OccurrencesChanged`/`EventChanged`/`AlertChanged`/`Failed`,
protože Swiftové `event` a `error` jsou pro CA1716 klíčová slova VB,
`InMemoryCalendarSource` nad vzorky, `DaemonCalendarSource`: snímky
porovnává přes JSON, `Stop()` zapomene rozběhnuté načtení — oprava proti
Swiftu). Kontrolery `CalendarController` (+ `.Sidebar.cs`; Swiftové
`Filter`/`Sidebar` jsou `ContentFilter`/`SidebarModel`),
`CalendarAlertsController` nad `ICalendarAlertNotifier`,
`EventDetailController`, `EventEditorController` (`Mode.NewEvent`/
`Mode.EditEvent`, `ShowsReloadEvent`, `SetAttendees`), `InvitationController`,
`CalDAVWizardController`, `CalendarPreferencesController` (zápisy ve
frontě, odmítnutý vrátí jen svou změnu), `CalendarAccountController`
(Odebrat účet a přihlášení přesunuté do Core podle `docs/windows-port.md`
§7.4); asistent `Assistants/Assistant.Calendar.cs`
a `AssistantPanelController.RunCalendar`/`OpenInCalendar`, karta
`Controllers/CalendarContent.cs`. Mimo kalendář v Core: `Board.Mode.Calendar`
(2) se `StartChoice.Calendar`, klíč `calendar-view`
(`SettingsStore.CalendarView`, 44 klíčů gschema), Ctrl+3 jako
`ShortcutCommand.ShowCalendar` v `ShortcutMap`, `NotificationHub.AddCalendar`,
`AccountsPageController` CalDAV účty nevypisuje (při přeřazení je drží za
vypsanými), `FolderTree.AccountHeaderBadge` umí `CALDAV`, toasty zpráv
s kalendářovou částí (`DesktopNotification`, `DesktopNotificationActions`),
`DaemonSupervisor` nastaví `MALACHI_TIME_ZONE` z windowsové zóny a regionu
uživatele (`ZoneName`: Central Europe Standard Time + CZ → Europe/Prague;
bez IANA jména nic a zdrojem zůstane `TZ`; hodnota v prostředí vyhrává).
Platform: `Notifications/CalendarAlertActivation`, `CalendarAlertResponse`,
`InvitationActivation`, `InvitationNotificationResponse`,
`NotificationArguments.ForAlert`/`ForInvitation`. App (jmenný prostor
`Malachi.App.Calendars`): třísegmentový přepínač režimu
(`ModeCalendarButton`; s vypnutou nástěnkou zůstává se zašedlým segmentem
Nástěnka jako na macOS, dřív se na Windows schoval), Pošta/Nástěnka/Kalendář
v nabídkách `…` postranního panelu, nástěnky i kalendáře
(`MainWindow.Mode.cs`, `MainWindow.Calendar.cs`, `MainWindow.CalendarPage.cs`,
`Shell/AppState.Calendar.cs` se zdrojem na okno a `MALACHI_CALENDAR_SAMPLES`,
`Integration.Calendar.cs`); stránka `Calendars/CalendarPage` s lištou
`CalendarBar` (přepínač postranního panelu, Dnes, ‹ ›, Den/Týden/Měsíc/Agenda
jako přepínací tlačítka, pod 640 DIP `ComboBox`, nadpis s podtitulkem, +,
✦ jako přepínač, nabídka `…`), hledání je pole titulkové lišty se zástupným
textem Hledat události (jedno pole pro Poštu i Kalendář, text každého
režimu zvlášť, hledání vlastními slovy zůstává poštovní), `CalendarContentView`
s postranním panelem `CalendarSidebar` (240 DIP pevně, minikalendář
`MiniMonthView`, Pozvánky ▸ Nezodpovězené, účty s kapslí včetně CALDAV,
kalendáře s CheckBoxem, řádek souhlasu, Přidat kalendář…), pohledy
`CalendarWeekView` (60 DIP na hodinu, neprůhledné výplně syté barvy
kalendáře u bloků, celodenních pruhů, pruhů měsíce a pilulek s bílým
textem, na světlé barvě téměř černým, kde bílá nemá 4,5:1
(`CalendarPalette.TextOn`), barevný proužek o 25 % tmavší, nezávazné jako
světlá výplň (25 % barvy přes povrch) s textem motivu a proužkem 4 DIP
šrafovaným barvou a povrchem, časovaná pilulka měsíce jen dutou tečkou
(druhý nález vlastníka 2026-10-17 v pohledu Měsíc nad kalendáři Outlooku),
nezodpovězené jako povrch s čárkovaným obrysem a textem v barvě,
odmítnuté a zrušené 18 % přes povrch #F9F9F9 / #282828, výběr 2 DIP kroužek
v barvě zdůraznění (rozhodnutí vlastníka 2026-10-17 po živém testu na
Windows, macOS zůstává na 18 % pro každý blok; řádek v tabulce
`windows/README.md`), barvy v `CalendarPalette` a `CalendarStyles.xaml`
se světlou, tmavou a vysoce kontrastní variantou, červená čára teď na
minutovém časovači, první posun do pracovní doby, dokud uživatel
nescrolluje), `CalendarMonthView` (pruhy z
`MonthLanes`, štítky, „+N dalších“ ve `Flyout`, kapacita ze skutečné výšky
buňky), `CalendarAgendaView` (skupiny, stránkování, výsledky hledání „den ·
čas“, filtr Nezodpovězené); prázdný stav leží nad pohledy; výskyty jsou
zastávky klávesnice a prvky UIA (`CalendarBlock:<klíč>`, `CalendarAllDay:`,
`CalendarMonthBar:`, `CalendarPill:`, `CalendarAgendaRow:`,
`CalendarMore:<datum>`); klávesy přes `Cal.KeyFor` v `PreviewKeyDown`
stránky (Ctrl+T, Ctrl+←/→, Ctrl+Shift+1–4, Return, šipky, Escape
dvoustupňově; Delete otevře detail, jehož Odebrat se zeptá, místo mazání);
D28 přesunem `AssistantPanelView` okna do slotu stránky a zpět; pod 360 DIP
obsahu se sbalí postranní panel, pak panel asistenta; stavový pruh
`SetCalendar(řádek, poznámka)` spojený s poznámkou triage. Detail jako
WinUI `Flyout`, jeden pro aplikaci (`EventDetailFlyout`,
`EventDetailPresenter`, `EventDetailView`; vpravo od kotvy, jinak vlevo,
jinak přes ni; otázky jako ContentDialog v pořadí Windows se Zrušit jako
výchozí, tři volby série jako tlačítka v obsahu dialogu,
`Shell/AlertService.Calendar.cs`; poznámky jako prostý `TextBlock`
s odkazy jako tlačítky pod ním; během dialogu zůstává otevřený zrušením
`Closing`, za běhu neověřené; Najít jiný termín jako na macOS přes Upravit
s otázkou série a pak `findFreeSlot`). Okno `Calendars/EventEditor/EventEditorWindow`
+ `EventEditorManager` + `EventEditorPane` (`.Dates` s `CalendarDatePicker`
a `TimePicker`, `.Video`, `.Attendees` s `RecipientTokenBox`),
`AvailabilityView`, `TimeZonePicker`, `CustomRepeatView` (760×800, min.
560×520; klávesy okna zprávy Ctrl+Enter = primární a Ctrl+S; Vložit
odkaz… jako dialog; Konec se ptá přes `QuitSteps.SaveDrafts`; konec relace
Windows neuložené změny ztratí bez otázky, protože Uložit by rozeslalo
pozvánky). Karta pozvánky `Reader/InvitationCardView` +
`InvitationDayStripView` + `InvitationCardPresenter` (v panelu čtení
a okně zprávy uvnitř posuvné oblasti hlavičky, v kartě konverzace jako
první pruh uvnitř sbalitelné části, ne u přiložené zprávy; „Přidat do:“
`ComboBox`, Odmítnout `SplitButton`, Změnit odpověď `DropDownButton`,
Navrhnout jiný čas… se zvláštním výběrem data a času; karty porovnané přes
JSON, takže odpověď zůstane, dokud `message.get` nepřinese novou),
štítek `Controls/InvitationPill` ve vlastním sloupci `Main/MessageRowView`
za předmětem, Zobrazit v Kalendáři `MainWindow.ShowInCalendar.cs`.
Toasty upozornění `Platform/CalendarAlertNotifier` (EVENT_ALERT
a EVENT_ALERT_JOIN: Připojit se jen s videohovorem, Odložit; klik →
Zobrazit v Kalendáři; zavření toastu se nebalené aplikaci nehlásí, takže
upozornění pro ostatní klienty nezruší — řádek tabulky README) a pozvánek
(INVITATION: Přijmout a Zobrazit, CANCELLATION: Odebrat z kalendáře; texty
zkrácené jako u zprávy; Přijmout a Odebrat → `AppState.AnswerInvitation`
přes `message.get` a `InvitationController`, Zobrazit a klik otevřou zprávu
ve vlastním okně jako každý toast pošty). Pruh `Boards/BoardTodayCalendarStrip`
(řádky klikatelné → Zobrazit v Kalendáři, Odpovědět → zpráva v Poště,
Otevřít Kalendář ›, vypisuje i nezodpovězené pozvánky snímku), čipy panelu
asistenta Co mě dnes čeká (pošle hned) a Najít volný termín (čeká na
slova), Zeptat se na tuto událost vyplní pole panelu
(`Assistants/AssistantPanel.Prefill.cs`), karta Zobrazit v Kalendáři.
Předvolby → Kalendář za Obecné (`Preferences/CalendarPreferencesPage` nad
vlastním zdrojem okna Předvoleb, `CalendarAccountSection`, `CalendarRowView`,
`CalendarSwatch`; skupina Zobrazení, sekce účtů; název CalDAV účtu se
edituje tady, protože ho Účty nevypisují; Nahradit heslo… jako dialog
přes `IAlerts.PasswordAsync`; Odebrat účet na konci sekce; u IMAP účtu
s capability `calendar` — vzorový Gmail — bez přepínače synchronizace
a řádků Pozvánky jako na macOS, ač `Cal.Prefs.PreferencesLayout` i Go
reference je vypisují pro každý účet mimo CalDAV: Core a pohled macOS se
rozcházejí) a okno `Wizard/CalDAVWizardWindow` (modální 560×560 přes
`ModalDialog`, chyby v InfoBaru, Přidat a pak Zrušit, Enter spustí Zjistit
kalendáře a pak Přidat, výběr poštovního účtu jen když server nechá
plánování klientovi, dialog důvěry certifikátu poštovního průvodce; vstupy:
nabídka `…`, stránka Žádné účty, postranní panel kalendáře, Předvolby →
Kalendář; nabídka „+“ v Předvolbách → Účty ho nemá, jako
macOS M9). Testy: `tests/Malachi.Core.Tests/Calendars/*` (Go testy 1:1
a případy Swiftu, asi 614), `Controllers/Calendar*Tests`,
`Boards/CalendarModeTests`, `Platform.Windows.Tests/Notifications/
CalendarAlertArgumentsTests` a `InvitationArgumentsTests`. Ověřeno na
tomto stroji (2026-10-17): všechny projekty se staví bez varování,
`build.ps1 app` aplikaci opakovaně publikoval, `build.ps1 lint` prošel
(formát čistý a konvenční testy 27/27, včetně `StringsCheckTests` a
`GschemaCoverageTests`; v `windows/parity-exclusions.txt` nezůstal žádný
řádek „calendar: Windows port pending“), sada Core je zelená (7 433
testů, 9 přeskočených jako vždy) a UI smoke testy kalendáře nad
`MALACHI_CALENDAR_SAMPLES=1` (`CalendarSmokeFixture`,
`CalendarSmokeTests`, `CalendarDetailSmokeTests`,
`CalendarInvitationSmokeTests` s `CalendarBoardSmokeFixture` nad oběma
sadami vzorků, `CalendarPreferencesSmokeTests`) proběhly: 26 testů,
0 selhání, 1 přeskočený (test řádku pruhu Dnes se o víkendu přeskakuje).
Vlastník zkoušel publikovanou aplikaci se svým M365 (Graph) i Nextcloudem
(CalDAV) a podle jeho nálezů přibyly čtyři opravy: syté barvy bloků
s pravidlem textu 4,5:1, nezávazné jako světlá výplň se šrafovaným
proužkem, šrafování s `Reflect` (WinUI při kreslení přechodu `Repeat`
zpolovičí ostatní plné barvy téhož průchodu; ověřeno změřenými snímky
izolované instance) a okna otevřená dvojklikem (editor i okno zprávy
v Poště), která se ukážou až po uvolnění tlačítka myši (ověřeno skutečným
vstupem). Mezery jsou zavřené: poštovní postranní panel CalDAV účty vynechává
(`FolderTree.MailSidebarAccounts`, port macOS `mailSidebarAccounts`);
Ctrl+Shift+N je v `ShortcutMap` (`ShortcutCommand.NewEvent`, hlavní okno
v každém režimu za `Board.Allows`, akcelerátory stránky zrušené; testy
`CtrlShiftNIsNewEventOfTheMainWindowOnly`
a `NewEventIsShortcutMapsChordInEveryMode`), takže do okna dojde, i když
klávesnici drží WebView2; hlavička `Shell/AppState.Calendar.cs` popisuje
vstupní body tak, jak fungují. Neověřeno: šrafování v tmavém motivu
a při jiném měřítku displeje než 100 %, dvojtapy dotykem a perem,
odmítnuté a zrušené bloky na obrazovce (vzorky je neukážou), celé
`build.ps1 test` (zbylá selhání v `Malachi.Platform.Windows.Tests`, čtyři
testy souboru s klíčem a skutečného démona a jeden test typu souboru
v shell verdiktu, jsou prostředí a předcházejí port) a ARM64. GTK port
vznikl souběžně na Macu (GTK klient kalendáře výše).

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
- Nástěnku zkoušej taky jen nad kopií (první start s `RulesVersion` 7 přepočítá
všechny případy a poštu nejdelšího okna, stavy uživatele, Done a remindy
zůstanou; vlastníkovi to říct před testem): migrace 0017 je nevratná (tabulky
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
