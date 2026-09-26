# AGENTS.md

Instrukce pro AI asistenty pracující na tomto repozitáři.

## Co je tento projekt

**Malachi Mail** — desktopový emailový klient: jedno jádro v Go s veškerou
logikou (démon `malachid` v `backend/`) a nativní UI pro každou platformu,
dnes GTK4 pro Linux (`ui/`) a Swift/AppKit pro macOS (`macos/`), Windows
(WinUI 3) přijde. UI a démon jsou dva samostatné procesy komunikující přes
JSON-RPC na unix socketu.

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
V UI: `SetUseMarkup(false)` na všem, co zobrazuje data ze serveru.

### 4. Jeden kód na platformu, žádné větvení
Jádro (`backend/`) je platformně neutrální Go: týž strom se beze změny
staví pro Linux i do macOS aplikace, linuxové služby přes D-Bus jsou jen
volitelné za běhu. GTK UI je linuxový kód. Do žádného z nich nepřidávej
build tagy, podmíněnou kompilaci ani abstrakce „pro jistotu“ pro Windows
a macOS; co se jinde liší, řeší démon neutrálním bodem rozšíření voleným
za běhu (jako `MALACHI_KEYRING=helper`), ne platformním kódem ve stromu.
Jiné platformy dostanou vlastní nativní UI (Swift/AppKit pro macOS, WinUI 3
pro Windows) jako samostatné klienty nad API démona, přičemž GTK UI je
mustr, který zrcadlí; nikdy větvení tohoto kódu. Přenositelnost je
zajištěná hranicí na API, ne podmíněnou kompilací.

### 5. Neměň API kontrakt bez aktualizace docs/api.md
Kontrakt (`backend/pkg/api/`) a dokumentace (`docs/api.md`) se mění současně,
v jednom commitu. Test `TestDocsCoverAllMethods` hlídá, že každá metoda,
notifikace a chybový kód je v dokumentu zmíněn. Chybové kódy se nikdy
nepřečíslovávají, jen přidávají. Nekompatibilní změna = bump `ProtocolVersion`.

## Konvence

- Go: standardní formátování, `golangci-lint`, errors wrapované s kontextem
- Struktura balíčků: `internal/` pro implementaci, `pkg/api/` pro veřejný kontrakt
- MCP most (`backend/cmd/malachi-mcp`) je klient démona jako UI: z `backend/`
  importuje jen `pkg/api`, nikdy nevrací HTML, každý řetězec z pošty prochází
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
- Licence: `backend/` je AGPL-3.0-only (duálně licencované jádro, viz
  `LICENSING.md`), vše ostatní GPL-3.0-or-later. Každý nový zdrojový soubor
  (`.go`, `.swift`, `.py`, `.blp`, `.sql`, `.sh`) začíná hlavičkou `SPDX-FileCopyrightText`
  a `SPDX-License-Identifier` podle toho, ve které části leží. Do `backend/`
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
(UI ji otevře z chipu přílohy v samostatném okně). UI je vykresluje ve WebKitGTK 6.0
bez JavaScriptu (`ui/internal/htmlview`, CSP, síť odříznutá), lišta nabízí
načtení obrázků a důvěru odesílateli. Compose posílá formátovaný text
(`richText = true`), odchozí zprávy jsou `multipart/alternative`
(+ `related` pro vložené obrázky, + `mixed` pro přílohy). Odpověď a
přeposlání připravuje backend (`draft.create`, `internal/core/quote.go`):
adresáti, `Re:`/`Fwd:`, originál citovaný jako sanitizované HTML v compose
režimu (první `draft.save` je identita), jeho `cid:` obrázky zkopírované do
úložiště příloh pod novými id (`attachment.get` je vrací editoru); UI dodá
jen lokalizovanou hlavičku citace (`attribution`), `compose.Prefill` je
fallback bez démona. Doplňování příjemců: `contact.search` slévá
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
Vyhledávání zatím `notImplemented`. MCP most pro AI agenty
(`backend/cmd/malachi-mcp`, stdio server, klient socketu importující jen
`pkg/api`; `.mcp.json` v kořeni ho registruje pro Claude Code; výchozí jen
čtení + koncepty (nové, odpověď, odpověď všem, přeposlání přes
`draft.create`), `--allow-modify` / `--allow-send` přes
`MALACHI_MCP_ALLOW_MODIFY` / `MALACHI_MCP_ALLOW_SEND`; nikdy nevrací HTML,
obsah pošty v ohradě s nonce; podpříkazy `status`/`install`/`uninstall
--json` zapisují registraci do konfigurace Claude Desktop a Claude Code a
Předvolby → AI → MCP je v obou UI jen přepínač nad nimi (GTK
`ui/internal/mcpsetup`, macOS `MCPRegistrationController`); viz `docs/mcp.md`). macOS klient (`macos/`, Swift/AppKit, SwiftPM tools 6.0, macOS 14+,
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
helper keyringu démona nad login keychain). Démon dostal jediné
rozšíření: `MALACHI_KEYRING=helper` + `MALACHI_KEYRING_HELPER`
(`internal/auth/helper`, styl git-credential, platformně neutrální);
app spouští `malachid` z bundlu s `--config`/`--store` v
`~/Library/Application Support/Malachi Mail/`, socket na výchozí cestě
démona, `malachi-mcp` je v bundlu. Gmail a Microsoft 365 jdou přes
vlastní přihlášení démona v prohlížeči (client ID v `config.toml`),
doplňování příjemců jen ze sebraných adres, vyhledávání nikde. Odchylky od GTK jen z tabulky
v `macos/README.md` (unified toolbar, skládání panelů bez navigace zpět,
stavový pruh přes spodek okna místo patičky sidebaru, bez tlačítka
hlavní nabídky (je v menu baru), bannery jako karty se symbolem, seznam se stránkuje sám, Settings bez hledání, ⌥⌘↑/↓, volba ⌘R, pořadí tlačítek NSAlert,
quarantine na přílohách, zvuk Glass); `.blp` jsou reference, nová
funkce jde nejdřív do backendu a GTK, pak sem. Ad-hoc podpis: po každém
rebuildu se Keychain jednou zeptá (`make macos SIGN='…'` to řeší).
Kontributorský popis `docs/macos-port.md`. `make macos` / `run-macos` /
`test-macos` jsou jen na Darwinu.

Pořadí prací:
1. ~~IMAP — čtení, synchronizace, offline store~~ hotovo
2. ~~SMTP a odesílání~~ hotovo (přílohy, outbox, kopie do Sent)
3. ~~Microsoft 365 přes Graph + GNOME Online Accounts~~ hotovo (místo
   XOAUTH2/IMAP; zdůvodnění v `docs/architecture.md` §7)
4. ~~Sanitizér HTML (compose i view) a renderování s webview~~ hotovo
   (vlastní sanitizér, `htmlWithheld`, `message.part`, stahování obrázků
   démonem, multipart/alternative)
5. ~~Threading~~ hotovo (backend i seskupený seznam v UI)
6. Vyhledávání

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
Průvodce v obou UI nabízí „Důvěřovat certifikátu…“ až po výslovném
potvrzení s otiskem (pravidla v `ui/internal/certtrust`, zrcadlo
`MalachiCore/Wizard/CertTrust.swift`), změna hosta nebo portu pin zahodí,
stránka Servery ho ukáže se Zapomenout, účet s odmítnutým nebo změněným
certifikátem má stav a banner s „Upravit účet…“. Žádné obecné
„ignorovat certifikát“ (`docs/security.md` §7).

Otevřená rozhodnutí: viz `docs/architecture.md` §7 (jazyk UI, sanitizační
knihovna, umístění definic účtů, uložení těl zpráv, Microsoft účty).

## Čeho si být vědom

- `gotk4` je generovaný binding; v některých částech API se vyskytují
  memory leaky a pády. Při podivném chování zvaž, že chyba nemusí být v našem kódu.
- První kompilace `gotk4` trvá desítky minut. Není to zamrznutí.
- Toolbx sdílí domovský adresář s hostitelem (včetně `~/go` a build cache).
- `XDG_RUNTIME_DIR` nemusí být v kontejneru nastavený; backend i UI pak
  používají `~/.cache/malachi/run/rpc.sock`. `MALACHI_SOCKET` přebíjí obojí.
  Ve Flatpaku (`FLATPAK_ID`) leží socket v `$XDG_RUNTIME_DIR/app/<app-id>/`,
  jediné části runtime dir sdílené mezi instancemi sandboxu (`api.SocketBase`).
- Démona nespouští nic na desktopu: UI si ho spustí samo (`ui/internal/daemon`,
  hledá `malachid` vedle vlastní binárky, `MALACHI_DAEMON=none` vypne) a při
  ukončení aplikace ho zastaví. Běžícího démona (`make run-backend`) použije
  a nechá být.
- `make build` musí proběhnout před `scripts/dev-run.sh`; skript binárky nestaví.
  `make run-dev` / `run-backend` / `run-frontend` build zajistí samy.
