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

## Prostředí

Vývoj probíhá v Fedora 42 Toolbx kontejneru (`toolbox enter malachi`).
Kompilace a testy uvnitř, `flatpak-builder` a testování výsledného balíčku
na hostiteli.

Ověřené verze (2026-09-02): Go 1.25, GTK 4.18, libadwaita 1.7, GLib 2.84,
WebKitGTK 2.52 (API 6.0), Blueprint 0.16, SQLite 3.47.

WebKitGTK pro GTK4 je API verze 6.0 (`webkitgtk6.0-devel`), ne 4.x.

gotk4 je připnutý na snapshot `v0.3.2-0.20250703063411` a gotk4-adwaita na
commit `e94555b846b6` (2025-07-03, generováno pro libadwaita 1.7), protože
gotk4 v0.4.x vyžaduje GLib ≥ 2.86 a novější gotk4-adwaita libadwaita 1.9;
Fedora 42 má GLib 2.84 a libadwaita 1.7. Při povýšení runtime (GNOME 50+)
povyš obojí najednou. Čistá kompilace gotk4 trvá ~15 min; `CC="ccache gcc"`
by při další plné rekompilaci většinu času ušetřil.

## Stav a priority

Aktuální fáze: bootstrap / rané stádium. Backend startuje, odpovídá
`notImplemented`, UI zobrazuje dummy data a stav připojení.

Pořadí prací:
1. IMAP — čtení, synchronizace, offline store
2. OAuth2 pro Office 365
3. SMTP a odesílání
4. Renderování HTML s webview (WebKitGTK 6.0, JS vypnutý, CSP)
5. Vyhledávání, threading

Gmail je vědomě odložený — vyžadoval by CASA audit nebo
bring-your-own-credentials režim. Neimplementuj bez zadání.

Otevřená rozhodnutí: viz `docs/architecture.md` §7 (jazyk UI, sanitizační
knihovna, umístění definic účtů, uložení těl zpráv).

## Čeho si být vědom

- `gotk4` je generovaný binding; v některých částech API se vyskytují
  memory leaky a pády. Při podivném chování zvaž, že chyba nemusí být v našem kódu.
- První kompilace `gotk4` trvá desítky minut. Není to zamrznutí.
- Toolbx sdílí domovský adresář s hostitelem (včetně `~/go` a build cache).
- `XDG_RUNTIME_DIR` nemusí být v kontejneru nastavený; backend i UI pak
  používají `~/.cache/malachi/run/rpc.sock`. `MALACHI_SOCKET` přebíjí obojí.
- `make build` musí proběhnout před `scripts/dev-run.sh`; skript binárky nestaví.
