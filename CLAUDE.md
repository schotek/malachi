# CLAUDE.md

Instrukce pro AI asistenty pracující na tomto repozitáři.

## Co je tento projekt

**Malachi Mail** — desktopový emailový klient pro Linux. Backend v Go, UI v GTK4.
Dva samostatné procesy komunikující přes JSON-RPC na unix socketu.

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

### 4. Linux only
Nepřidávej kód, build cesty ani abstrakce pro Windows a macOS.
Přenositelnost je zajištěná hranicí na API, ne podmíněnou kompilací.

### 5. Neměň API kontrakt bez aktualizace docs/api.md
Kontrakt (`backend/pkg/api/`) a dokumentace (`docs/api.md`) se mění současně,
v jednom commitu. Test `TestDocsCoverAllMethods` hlídá, že každá metoda,
notifikace a chybový kód je v dokumentu zmíněn. Chybové kódy se nikdy
nepřečíslovávají, jen přidávají. Nekompatibilní změna = bump `ProtocolVersion`.

## Konvence

- Go: standardní formátování, `golangci-lint`, errors wrapované s kontextem
- Struktura balíčků: `internal/` pro implementaci, `pkg/api/` pro veřejný kontrakt
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
- Licence: `backend/` je AGPL-3.0-only (duálně licencované jádro, viz
  `LICENSING.md`), vše ostatní GPL-3.0-or-later. Každý nový zdrojový soubor
  (`.go`, `.blp`, `.sql`, `.sh`) začíná hlavičkou `SPDX-FileCopyrightText`
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
inboxu po minutě, `sendMail`), token výhradně z GNOME Online Accounts
(`internal/auth/goa`, D-Bus), žádné vlastní client ID ani PKCE flow;
core rozděluje účty podle `kind` na IMAP a Graph supervisor
(`internal/core/dispatch.go`); průvodce nabízí účty z GOA (`account.linked`)
a pro M365 adresy bez přihlášení odkazuje do Nastavení → Účty online.
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
(+ `related` pro vložené obrázky, + `mixed` pro přílohy). Doplňování příjemců: `contact.search` slévá
sebrané adresy (`collected_addresses`, plní outbox worker po doručení a
jednorázový backfill ze složek Odeslané, nikdy z příchozího `From`)
s knihami EDS účtu odesílatele (`internal/contacts/eds`, D-Bus `Sources5`
+ `AddressBook10`, jen čtení, bez EDS tiše prázdné). Threading a
vyhledávání zatím `notImplemented`.

Pořadí prací:
1. ~~IMAP — čtení, synchronizace, offline store~~ hotovo
2. ~~SMTP a odesílání~~ hotovo (přílohy, outbox, kopie do Sent)
3. ~~Microsoft 365 přes Graph + GNOME Online Accounts~~ hotovo (místo
   XOAUTH2/IMAP; zdůvodnění v `docs/architecture.md` §7)
4. ~~Sanitizér HTML (compose i view) a renderování s webview~~ hotovo
   (vlastní sanitizér, `htmlWithheld`, `message.part`, stahování obrázků
   démonem, multipart/alternative)
5. Vyhledávání, threading

Gmail je vědomě odložený — vyžadoval by CASA audit nebo
bring-your-own-credentials režim. Neimplementuj bez zadání. XOAUTH2 pro
IMAP a vlastní OAuth2 flow (typy `AuthOAuth2`/`OAuth2Config` v API) jsou
rezervované pro desktopy bez GOA; neimplementuj bez zadání.

Otevřená rozhodnutí: viz `docs/architecture.md` §7 (jazyk UI, sanitizační
knihovna, umístění definic účtů, uložení těl zpráv, Microsoft účty).

## Čeho si být vědom

- `gotk4` je generovaný binding; v některých částech API se vyskytují
  memory leaky a pády. Při podivném chování zvaž, že chyba nemusí být v našem kódu.
- První kompilace `gotk4` trvá desítky minut. Není to zamrznutí.
- Toolbx sdílí domovský adresář s hostitelem (včetně `~/go` a build cache).
- `XDG_RUNTIME_DIR` nemusí být v kontejneru nastavený; backend i UI pak
  používají `~/.cache/malachi/run/rpc.sock`. `MALACHI_SOCKET` přebíjí obojí.
- `make build` musí proběhnout před `scripts/dev-run.sh`; skript binárky nestaví.
  `make run-dev` / `run-backend` / `run-frontend` build zajistí samy.
