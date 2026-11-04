# Přizpůsobení lišty — návrh GTK portu (fáze G)

Doplněk k `docs/plans/toolbar-customization.md` (rozhodnutí T1–T11, O1–O6,
model §2, klíče §3, GTK a Windows §5, „As built“ M1–M8 a B1 v §10–§14).
Windows port píše svůj návrh do toho souboru, GTK sem, aby se práce
nepletly. Návrh, ne „As built“: **GTK kód se zatím nemění**, rozhodnutí,
která patří vlastníkovi, jsou na konci (§12) a tady se nerozhodují.

## 0. Ověření větve na Linuxu (2026-11-04)

Stroj: Fedora 44 aarch64 (VM), GTK 4.22, libadwaita 1.9, WebKitGTK 2.54,
Go 1.26, nativní překlad (ne kontejner z CLAUDE.md), relace GNOME na
Waylandu. Větev `feat/toolbar-customization` na `b55f6e47`, čistý worktree.

| Krok | Výsledek |
|---|---|
| `go test ./internal/toolbar` (z `ui/`) | zelené |
| `make build` (démon, most, GTK, schéma `--strict`, `msgfmt --check`) | zelené |
| `go test ./...` v `ui/` (`GSETTINGS_BACKEND=memory`, schéma z `build/`) | zelené, 49 balíčků |
| `make lint` (Blueprint, `go vet`, pot proti zdrojům, desktop, metainfo) | zelené (golangci-lint tu není, běží jen `go vet`) |
| `make po` | beze změny, jen `POT-Creation-Date` (vráceno) |
| smoke `MALACHI_GTK_SMOKE=1 go test ./internal/window -run Smoke` | **prostředí**: na Waylandu i s `GDK_BACKEND=x11` (Xwayland) padají jiné testy při každém běhu (kalendář týden/měsíc/detail, chat, okno Kontaktů; bloky s nulovými rozměry) a běh končí pádem v cgo u `TestBodyFitGTKSmoke` (WebKit); Xvfb tu není (`gtk.sh test-ui` počítá s ním a s obrazovkou 1600×1200). Větev nemění žádný soubor `ui/internal/window`, `ui/data/ui` ani jiný GTK kód (jen nový balíček `ui/internal/toolbar`, gschema a `po/`), takže selhání s ní nesouvisí; proti `main` porovnané nebylo. |

Nic se neopravovalo, žádný `fix:` commit nebyl potřeba.

## 1. Co dnes GTK má

GTK nemá jednu lištu přes okno, ale **hlavičkovou lištu na sloupec**
(`Adw.ToolbarView` + `Adw.HeaderBar` každé `NavigationPage`). To se dobře
kryje se sekcemi modelu: sekce, kterou na macOS dělí sledovací oddělovač,
je v GTK vlastní `Adw.HeaderBar`.

### 1.1 Pošta (`window.blp`)

| Hlavička GTK | Sekce modelu | Dnes v ní (pořadí na obrazovce) |
|---|---|---|
| sloupec složek (`outer_split` sidebar) | `sidebar` | `mode_switch`, Nová zpráva │ … │ hlavní nabídka |
| seznam (`list_page`) | `list` | `refresh_button` │ titulek `list_title` │ `search_button` |
| zpráva (`message` page) | `message` | Odpovědět, Všem, Přeposlat │ … │ Archivovat, Koš ␣ `crm_slot` ␣ Hvězdička, ✦ `assistant_button`, Další akce `message_menu` ␣ `assistant_panel_button` |

### 1.2 Nástěnka (`board_page.blp`)

Jedna hlavička `board_header` přes celou stránku pro **všechny tři styly**
(Seznam, Sloupce, Dnes): `board_mode_switch`, `board_style_switch` │
`board_title` │ Triage + `board_triage_activity`, `board_account_filter`,
`board_menu_button`. Detail má vlastní `Adw.HeaderBar` uvnitř
`board_detail_tree` (Hotovo, Připomenout…, Archivovat, Smazat │ … │
`board_detail_menu`, Odpovědět), a ten **jediný strom** `board_panel.go`
stěhuje mezi sloupcem detailu Seznamu a vysouvacím panelem Sloupců a Dnes.

### 1.3 Kalendář (`calendar_page.blp`)

Jedna hlavička `calendar_header`: `calendar_mode_switch`,
`calendar_sidebar_toggle`, `calendar_today`, ‹ › (linked Box),
`calendar_view_switch` / pod 640 sp `calendar_view_menu` │ `calendar_title`
│ hledání `calendar_search_toggle`, Nová událost, ✦, hlavní nabídka.

### 1.4 Položky katalogu v GTK

| id | GTK | Poznámka |
|---|---|---|
| `mode` | `mode_switch`, `board_mode_switch`, `calendar_mode_switch` | pevná (T4) |
| `toggleSidebar` (Pošta, Nástěnka Seznam) | **chybí** | složky a navigace nástěnky jsou `Adw.NavigationSplitView`, která sloupec mimo sbalený stav skrýt neumí; nabízet až s přechodem na `OverlaySplitView` (O-G4) |
| `toggleSidebar` (Kalendář) | `calendar_sidebar_toggle` | existuje |
| `newMessage` | tlačítko v hlavičce složek (`app.compose`) | GTK ho má v sekci `sidebar`, macOS v `list`; katalog obojí dovoluje |
| `contacts` | **chybí v liště**, jen hlavní nabídka (`app.contacts`) | nové tlačítko je triviální |
| `filter` | **liší se**: `message_filter` je `Adw.ToggleGroup` v seznamu pod hlavičkou, ne položka lišty | v GTK neodebíratelný a mimo katalog (jako na Windows) |
| `refresh` | `refresh_button` | |
| `reply`, `replyAll`, `forward`, `archive`, `trash`, `star` | existují | |
| `crm` | `crm_slot` (dvě tlačítka) | podmíněná, pod 600 sp ji skrývá breakpoint |
| `assistant` | `assistant_button` (viditelnost řídí `bindAssistantButton`) | podmíněná |
| `moreActions` | `message_menu` | |
| `search` | `search_button` v hlavičce **seznamu** | **liší se**: katalog ho pouští jen do `message` (O-G2) |
| `markRead`, `junk`, `moveTo`, `undo`, `loadPictures` | **chybí v liště**, akce existují (`win.mark-read`/`mark-unread`, `win.junk`, `win.move-to` s cílem, `win.undo`, `win.load-images`) | nové tlačítko na položku |
| `print` | chybí, tisk v GTK neexistuje | `FeaturePrint` vypnutý, editor ho nenabízí |
| `boardStyle`, `boardAccount`, `boardTriage` | existují | Triage s `board_triage_activity` jako jedna položka |
| `boardDone`, `boardRemind`, `boardArchive`, `boardTrash`, `boardReply` | existují v hlavičce detailu | |
| `calendarToday`, `calendarStep`, `calendarView`, `calendarNewEvent`, `calendarAssistant` | existují | `calendarView` = toggle group i úzký `DropDown` |
| `calendarSearch` | `calendar_search_toggle` + `SearchBar` | |

Mimo katalog (GTK je drží pevně, uživatel je nevidí, jako menu bar na
macOS): hlavní nabídka (`open-menu`) v Poště, Nástěnce i Kalendáři,
`board_detail_menu`, titulky (`list_title`, `board_title`,
`calendar_title`) a `assistant_panel_button` (sekce inspektoru na macOS je
strukturální).

## 2. Model: co GTK potřebuje navíc

Reference `ui/internal/toolbar` je dnes katalog macOS. GTK se od něj liší
třemi věcmi: položky, které neexistují (`filter`, `toggleSidebar` Pošty
a Seznamu, `print`), hledání Pošty v sekci seznamu a výchozí sada (dnešní
GTK lišta ≠ `Default` macOS: Nová zpráva v hlavičce složek, hledání nad
seznamem, žádné Kontakty).

Návrh (rozhodne vlastník v O-G1): **varianta klienta** v referenci, aby
ji mohl použít i Windows a Swift zůstal beze změny:

```go
// Variant is a client's difference from the catalog: what it lacks,
// where it may put an item besides, and its own default arrangement.
type Variant struct {
    Omit    map[Bar][]ID              // never offered, dropped by Normalize
    Extra   map[Bar]map[ID][]Section  // sections allowed besides the catalog's
    Default map[Bar]Layout            // nil = Default(bar)
}
var GTK = Variant{…}
func (v Variant) Catalog(bar) / Default(bar) / Normalize(bar, stored) /
    Stored(bar, l) / CanInsert(bar, id, s) / Allowed(bar, f) / MissingActions(…)
```

Funkce bez varianty volají `Variant{}`, takže macOS a jeho testy se
nezmění. Testy: Go tabulka pro `GTK` (výchozí sady GTK projdou
`Normalize` beze změny, vynechané položky z uloženého klíče zmizí,
`search` v `list` zůstane, `Stored(Default)` je prázdné).

Výchozí sady GTK (= dnešní obrazovka, aby se nic nezměnilo uživateli,
který lištu neupravil):

- `mail`: `mode, newMessage, flexibleSpace │list refresh, flexibleSpace,
  search │message reply, replyAll, forward, flexibleSpace, archive, trash,
  space, crm, space, star, assistant, moreActions`
- `board-list`: `mode, boardStyle, flexibleSpace │list boardTriage,
  boardAccount │detail boardDone, boardRemind, boardArchive, boardTrash,
  flexibleSpace, boardReply`
- `board-flow`: `mode, boardStyle, flexibleSpace, boardTriage, boardAccount`
- `calendar`: `mode, flexibleSpace │main toggleSidebar, calendarToday,
  calendarStep, calendarView, flexibleSpace, calendarSearch,
  calendarNewEvent, calendarAssistant`

(Pořadí za pružnou mezerou je pořadí na obrazovce zleva doprava; GTK je
balí do `[end]` obráceně, viz §4.2.)

## 3. Editor v Předvolbách

### 3.1 Umístění

Předvolby → Obecné, nová skupina **„Lišta“** (msgid `Toolbar`) na konci
stránky, jak říká §5 plánu. Alternativa je Vzhled (`appearance_page`, kde
je motiv a hustota), protože jde o vzhled okna (O-G3). Skupina má tři
řádky, editor samotný je `Adw.Dialog` (seznam položek se do skupiny
s ostatními předvolbami nevejde a v hledání Předvoleb by překážel):

```
Lišta
  Zobrazení lišty            [Jen ikony ▾]        ← Adw.ComboRow, toolbar-display-mode
  Přizpůsobit lištu…                         ›    ← Adw.ButtonRow / ActionRow → dialog
```

Dialog jde otevřít i z hlavní nabídky (Zobrazení ▸ „Přizpůsobit lištu…“
v `primary_menu`, akce `win.customize-toolbar`), která ho otevře rovnou
s lištou aktuálního režimu a stylu. Pravý klik na hlavičkovou lištu (T11)
by v GTK musel přebít kontextové menu titulkové lišty okna (window
manager), proto ne.

### 3.2 Dialog „Přizpůsobit lištu“

```
┌ Přizpůsobit lištu ──────────────────────────────── ✕ ┐
│ [ Pošta | Nástěnka – Seznam | Nástěnka – Sloupce a Dnes | Kalendář ] │  ← Adw.ToggleGroup (úzké okno: ComboRow)
│                                                       │
│ Složky                                                │  ← Adw.PreferencesGroup na sekci
│   🔒 Přepínač režimu                                  │     pevná: bez úchytu a Odebrat, zašedlá
│   ⠿ Nová zpráva                    [↑][↓]  [✕]        │
│   ⠿ ── Pružná mezera ──            [↑][↓]  [✕]        │
│ Seznam zpráv                                          │
│   ⠿ Zkontrolovat poštu   …                            │
│ Zpráva                                                │
│   ⠿ Odpovědět   …                                     │
│   ⠿ Uložit do eWay-CRM  (bez účtu eWay skryté)        │  ← podmíněná: podtitul, zůstává v seznamu (T7)
│                                                       │
│   [+ Přidat položku…]                                 │  ← v každé sekci: popover se seznamem
│                                                       │
│                    [Obnovit výchozí]                  │  ← destructive, s Adw.AlertDialog
└───────────────────────────────────────────────────────┘
```

- **Výběr lišty**: `Adw.ToggleGroup` nahoře (čtyři `toolbar.Bars`),
  výchozí = lišta, ze které byl dialog otevřen.
- **Řádky**: `Adw.ActionRow` na položku `Normalize` (oddělovače jsou
  nadpisy skupin, ne řádky). Popisek, ikona položky jako prefix, přípona
  úchyt ⠿ + tlačítka. Mezera a pružná mezera jako řádek se svým jménem.
- **Řazení**: přetažení za úchyt a Ctrl+↑/↓ přesně jako stránka Účty
  (`accounts_reorder.go`: `DragSource` na řádku, prepare jen nad úchytem,
  `DropTarget` s náhledem nad/pod, Ctrl+↑/↓ na řádku, obnova fokusu po
  přestavbě). Přetažení i klávesa může položku dostat **do jiné sekce**
  (pod nadpis), ale jen podle `CanInsert`; jinak se drop odmítne
  a klávesa na hranici sekce zastaví. Pevné položky drop nepřijímají
  před sebe (zůstávají na začátku sekce, jak je `Normalize` vrací).
- **Odebrat** (✕): pevné nemají. Odebraná položka se objeví v „Přidat
  položku…“.
- **Přidat položku…**: `MenuButton` s popoverem pod sekcí, nabídka =
  `Allowed` ∖ položky v rozvržení, jen ty, které `CanInsert` do té sekce
  pustí, plus Mezera a Pružná mezera. Přidá se na konec sekce.
- **Obnovit výchozí**: zeptá se (`Adw.AlertDialog`, vybraná lišta) a zapíše
  prázdný klíč (`[]` = výchozí, i budoucí verze).
- **Zápis**: každá změna hned `settings.Set(toolbar.Key(bar),
  Variant.Stored(...))`; okno ji dostane přes `OnChanged` a lištu postaví
  znovu (§4.4). Žádné Uložit/Zrušit; Ctrl+Z dialog nemá.
- Kód: čistý kontroler `ui/internal/toolbaredit` (bez gotk4: stav
  rozvržení, `Move(i, delta)`, `Drop(from, to)`, `Add(id, section)`,
  `Remove(i)`, `Reset()`, výsledné tokeny, popisky přes `Translator`),
  testovaný tabulkou; pohled `window/toolbar_editor.go` +
  `toolbar_editor.blp`. Windows ho může vzít jako referenci editoru.

### 3.3 Režim zobrazení

`toolbar-display-mode` platí pro všechny lišty (O4). Řádek ve skupině
Lišta (ComboRow: Jen ikony / Ikony a text / Jen text) i v dialogu nad
seznamem. V GTK:

- `icon`: dnešní stav (ikony, popisek v tooltipu).
- `icon-and-label`: `Adw.ButtonContent` (ikona + popisek) jako `child`
  tlačítka i `MenuButton`; `ToggleButton` hvězdičky stejně; `Adw.ToggleGroup`
  stylu a pohledů má popisky už teď, přepínač režimu zůstává jen s ikonami
  (B1 macOS: `mode` bez popisků).
- `label`: `Button.SetLabel`, `MenuButton.SetLabel` (bez ikony).
- `crm_slot` se dvěma tlačítky a spinnerem: popisek vedle ikony v jeho
  vlastním `Box`; `board_account_filter` a `calendar_view_menu` jsou
  `DropDown` a nemění se.

Lišty s popisky jsou široké: na úzkém okně rozhoduje přetečení (§5).

## 4. Skládání hlavičkových lišt z `toolbar.Present`

### 4.1 Widgety zůstávají v Blueprintu

Každý widget položky zůstane v `.blp` se svým ID, `action-name`, vazbami
(`active: bind …`) a settery breakpointů (`crm_slot.visible`). Go je jen
**přeskládá**: `toolbarHost` na `Adw.HeaderBar` při stavbě všechny
položkové widgety z lišty vyndá (`HeaderBar.Remove`) a podle rozvržení
znovu zabalí (`PackStart` / `PackEnd`). `Adw.HeaderBar` pořadí měnit
neumí, proto vždy celé přebalení. Widgety drží `Window` (silné reference
v Go structu, gotk4 je jinak uvolní, CLAUDE.md „Čeho si být vědom“);
odebraná položka nemá rodiče, ale žije, takže kód, který na ni sahá
(`starButton.SetActive`, `presentReply`, bind ✦), se nemění. Pevné
widgety GTK mimo katalog (§1.4) zůstávají v Blueprintu na svém místě a do
přebalení nevstupují (hlavní nabídka a `assistant_panel_button` vždy
nejkrajnější `[end]`, titulek `title-widget`).

Nové položky T10 (`markRead`, `junk`, `moveTo`, `undo`, `loadPictures`)
a `contacts` dostanou widgety v novém `ui/data/ui/toolbar_items.blp`
(objekty bez rodiče, jen ikony a `action-name`; tooltipy z Go, takže
soubor bez msgid jako `board_page.blp`). `markRead` je jedno tlačítko
(O3): ikona, tooltip a akce `win.mark-read`/`win.mark-unread` z
`selectionState` při každé změně výběru. `moveTo` je `MenuButton`
s `SetCreatePopupFunc`, který při otevření postaví nabídku z
`contextmenu.MoveTargets` (stejně jako Přesunout do v `context_menus.go`).

### 4.2 Sekce → hlavičky a start/konec

| Lišta | Sekce → `Adw.HeaderBar` |
|---|---|
| `mail` | `sidebar` → hlavička složek, `list` → hlavička seznamu, `message` → hlavička zprávy |
| `board-list` | `sidebar` + `list` → `board_header`, `detail` → hlavička `board_detail_tree` |
| `board-flow` | `main` → `board_header` |
| `calendar` | `sidebar` + `main` → `calendar_header` |

Hlavička má jen dvě strany kolem titulku. Pravidlo: v běhu sekcí jedné
hlavičky rozdělí položky **poslední pružná mezera**: před ní `PackStart`
v pořadí, za ní `PackEnd` v obráceném pořadí (první `PackEnd` je
nejvpravo). Dřívější pružné mezery se v GTK ignorují (hlavička má jednu
mezeru, titulek); bez pružné mezery je všechno vlevo. `Space` = okraj 12 px
předchozí položky (jako dnes `margin-end: 12` u Koše a okraje `crm_slot`),
tedy CSS třída `.toolbar-gap` na sousedním widgetu, ne prázdný widget.
Pravidlo je čistá funkce `toolbarPack(shown []toolbar.Shown) (start, end
[]toolbar.ID, gaps map[toolbar.ID]bool)` v `window/toolbar_pack.go`,
testovaná bez GTK.

`board_header` je sdílený Seznamem i Sloupci/Dnes, takže přepnutí stylu
(`board.go` `setStyle`) ho přebalí z druhé lišty. Hlavička detailu je
jeden strom pro všechny styly; ve Sloupcích a Dnes ji macOS nepřizpůsobuje
(`board-flow` sekci detailu nemá). Návrh: strom detailu se skládá vždy ze
sekce `detail` lišty `board-list`, i v panelu (O-G5).

Sbalená rozvržení (pod 900/600 sp) nic nemění: každá hlavička patří své
stránce, takže `HiddenSeparators`/`HiddenSections` GTK nepotřebuje.
`Features.HiddenSections` použije jen detail Seznamu bez vybraného
případu, pokud ho dnes `board_detail.go` skrývá (ověřit při G2).

### 4.3 Podmíněné položky (T7)

`Present` vrátí `Hidden` pro `crm`, `assistant`, `boardTriage`,
`calendarAssistant` bez funkce. GTK je **zabalí vždy** a viditelnost dál
řídí dnešní kód (`bindAssistantButton`, `refreshCRM`, triage
v `board_triage.go`): ten už schovává na místě, takže T7 platí beze
změny. Jen `Features` pro editor (podtitul „Skryté, dokud …“) a pro
`MissingActions` se počítají z téhož stavu (`Assistant.Shown`, účet eWay,
`board.TriageOffered`).

### 4.4 Přestavba za běhu

`settings.OnChanged` na pěti klíčích → `rebuildToolbars()` (jen hlavičky
dotčené lišty; režim zobrazení všechny). Přestavba neztrácí fokus: když
měl fokus widget položky, po přebalení se mu vrátí (`GrabFocus` přes
`glib.IdleAdd`), jinak seznamu. Změna z jiného procesu (dconf) se
projeví stejně.

## 5. Přetečení (T5) a Další akce (T8, M6)

GTK hlavička přetok nemá, dnes ho řeší breakpointy (`crm_slot` pod
600 sp). Návrh: `window/toolbar_fit.go` při změně šířky stránky (notify
`width` na `NavigationPage` / breakpointy, debounce `IdleAdd`) skrývá
položky od nejnižší `Priority` (u stejné od konce), dokud
`MeasureMinimum` hlavičky nepřekročí šířku; pevné a `PriorityUser` nikdy.
Výběr je čistá funkce `toolbar.Fit(shown, widths, available)` v referenci
(i pro Windows), testovaná tabulkou. Breakpoint `crm_slot.visible: false`
pak může zmizet (O-G6).

Další akce (`message_menu`) dostane `SetCreatePopupFunc`: první sekce =
`MissingActions(bar, layout, f)` + položky, které `Fit` právě skryl, jako
položky `GMenu` s jejich `win.*` akcemi (`win.reply`, `win.reply-all`,
`win.forward`, `win.archive`, `win.trash`, hvězdička přes `win.star`,
pokud existuje, jinak nová akce), za ní dnešní `message_menu_model`.
eWay-CRM už v Dalších akcích je (`crm_actions.go`), tedy bez duplicity.
Nástěnka a Kalendář mají jen hlavní nabídku, ne Další akce: skryté
položky tam jdou do `board_detail_menu` (detail) a do nabídky
`calendar_menu_button` jako první sekce (O-G7).

## 6. Hledání a Ctrl+F (O5)

V GTK je hledání `SearchBar` pod hlavičkou seznamu (`search.go`)
a `calendar_search_toggle` nad `SearchBar` kalendáře; tlačítko jen
přepíná lištu. Bez položky `search`/`calendarSearch` v rozvržení proto
Ctrl+F otevře lištu hledání stejně jako dnes (`search_bar.search-mode`)
a zavře ji Escape; dočasné pole na liště (`SearchIndex`) GTK nepotřebuje,
protože pole nikdy na liště nebylo. `SearchIndex` se v GTK nevolá.

## 7. ✦, Triage, eWay-CRM

- ✦ (`assistant`, `calendarAssistant`) a Triage: zabalené na místě
  rozvržení, viditelnost jako dnes (§4.3). Odebrané uživatelem: bind
  (`bindAssistantButton`) běží dál nad nezabaleným widgetem, nic se
  nevkládá.
- `crm`: `crm_slot` jako celek; v režimu Jen ikony beze změny (T9).
- `board_triage_activity` stojí vždy hned za Triage (jedna položka).

## 8. Řetězce

- Popisky editoru a režimu Ikony a text: tabulka id → msgid v referenci
  `ui/internal/toolbar/text.go` za rozhraním `Translator` (jako
  `jira.Translator`, GTK adaptér `i18n.Tr`), přidaná do `po/POTFILES`;
  Windows je vezme s klíčem = msgid, macOS má popisky svých položek dál
  vlastní. Reference je dnes bez textů (§10 plánu), tím se to mění (O-G8).
- Existující msgid (z `po/malachi.pot`): `Reply`, `Reply All`, `Forward`,
  `Archive`, `Move to Trash`, `Star`, `More Actions`, `Search`,
  `Check for New Mail`, `Contacts`, `New Message`, `Mark as Junk`,
  `Move To`, `Undo`, `Mark as _Read` / `Mark as _Unread` a `Load _Images`
  (mnemonika se v popisku odstraní jako na macOS, O3), `Today`,
  `New Event`, `Done`, `Remind…`, `Mail`, `Board`, `Calendar`, `Remove`.
- Nové: `Toolbar`, `Customize Toolbar…`, `Toolbar Display`, `Icons Only`,
  `Icons and Text`, `Text Only`, `Restore Defaults` + otázka a tělo,
  `Add Item…`, `Space`, `Flexible Space`, `Mode Switch`, názvy lišt
  (`Board – List`, `Board – Columns and Today`), názvy sekcí (`Folders`,
  `Messages`, `Message`, `Detail` — část existuje jako tituly stránek),
  podtitul podmíněné položky, `Previous and Next`, `View`, `Account`
  (filtr), `Triage` (dnes jen z `ui/internal/board`), `Style`. Konečný
  seznam při G1, česky v `cs.po`, `make po`. Každý nový msgid buď použije
  Windows port, nebo jde do `windows/parity-exclusions.txt` s důvodem
  (`StringsCheckTests`).
- Žádné `.blp` s novým msgid kromě skupiny v `preferences.blp`
  a `toolbar_editor.blp`; `toolbar_items.blp` je bez textů.

## 9. Testy

- Reference: `Variant` (§2), `Fit` (§5), texty (každé id katalogu má
  popisek, žádný msgid prázdný), Swift testy beze změny (zero variant).
- `ui/internal/toolbaredit`: přesun v sekci, přes hranici sekce jen podle
  `CanInsert`, pevné nejdou ven, přidání a odebrání, Obnovit výchozí →
  prázdný klíč, tokeny po každé změně = `Variant.Stored`.
- `window/toolbar_pack_test.go`: start/konec podle poslední pružné
  mezery, mezery jako okraje, výchozí sady GTK dají přesně dnešní pořadí
  widgetů (regrese: neupravená lišta se nesmí změnit).
- Smoke `MALACHI_GTK_SMOKE=1 TestToolbarGTKSmoke` (nový, skutečné
  Blueprinty, bez démona): pro každou lištu výchozí sada → pořadí dětí
  hlaviček rovno dnešnímu; vlastní rozvržení zapsané do `settings` → okno
  se přestaví (položky, `[start]`/`[end]`, mezery), odebraná položka bez
  rodiče a dál funkční (`win.archive` z klávesy), ✦ skrytý na místě,
  přepnutí stylu Nástěnky přebalí `board_header`, režim Ikony a text dá
  `Adw.ButtonContent`, úzké okno skryje položky nízké priority a Další
  akce je nabídne. `TestToolbarEditorGTKSmoke`: dialog, výběr lišty,
  Ctrl+↑/↓, přidání, Obnovit výchozí s otázkou. Kroky
  `MALACHI_GTK_START=prefs-toolbar|toolbar-editor:<bar>|toolbar=<tokens>`
  pro Broadway/XQuartz jako na macOS.
- Stávající smoke testy, které berou widgety z builderu
  (`board_gtk_smoke_test.go`, `calendar_*_gtk_test.go`), musí projít
  i po přebalení (widgety zůstávají, mění se jen rodič).

## 10. Fáze

1. **G1** reference: `Variant` + `GTK`, `Fit`, `text.go`, `toolbaredit`;
   `po/POTFILES`, `make po`, `cs.po`. Swift beze změny.
2. **G2** skládání: `toolbarHost`, `toolbar_pack.go`, `toolbar_items.blp`,
   nové položky T10 a Kontakty, přebalení všech čtyř lišt z výchozích sad
   GTK (obrazovka beze změny), `OnChanged`.
3. **G3** editor: skupina Lišta, dialog, hlavní nabídka.
4. **G4** režim zobrazení, přetečení a Další akce.
5. **G5** smoke testy, `make lint`, `make po`, ruční průchod; brána
   vlastníka (GTK) v XQuartz/na Linuxu: neupravená lišta stejná jako
   dnes, editor ve všech čtyřech lištách, přetažení a klávesy, odebrání
   a vrácení, Obnovit výchozí, režimy zobrazení, úzké okno, ✦ a eWay bez
   funkce, přepnutí režimů a stylů, změna z `gsettings set` za běhu.

Odchylka GTK (editor v Předvolbách místo palety, žádný pravý klik na
lištu, Filtr v seznamu) přijde do tabulky odchylek a do „As built“.

## 11. Rizika

- **Přebalení a fokus**: `HeaderBar.Remove` widgetu s fokusem posune
  fokus jinam; jednoklávesové zkratky seznamu se pak mohou zapnout nebo
  vypnout jinak než dnes (`focusTakesTypingHere`). Přebalovat jen při
  změně rozvržení, ne při každém výběru.
- **gotk4 a reference**: widget bez rodiče drží jen Go; ztráta reference
  = pád při dalším `SetSensitive`. Držet všechny v jednom structu.
- **Šířky**: `Adw.ButtonContent` v hlavičkách dělá okno širší než
  dnešní minimum (`width-request` sloupců); `Fit` musí běžet dřív, než
  okno vynutí větší minimum.
- **Sdílený `board_header`** mezi styly: přebalení při každém přepnutí
  stylu, včetně `board_triage_activity` a breakpointu, který skrývá
  `board_triage_progress`.

## 12. Otevřené otázky pro vlastníka

- **O-G1 Výchozí lišta GTK.** Má neupravená lišta GTK zůstat jako dnes
  (varianta klienta s vlastními výchozími sadami, §2), nebo přejít na
  výchozí sady macOS (Nová zpráva a Kontakty v hlavičce seznamu, hledání
  v hlavičce zprávy)? Klíče jsou v obou případech stejné; uložená lišta
  se mezi stroji nesdílí.
- **O-G2 Hledání Pošty.** Smí položka `search` v GTK stát v sekci
  seznamu (kde lišta hledání je), nebo jen v sekci zprávy jako na macOS?
- **O-G3 Umístění editoru.** Předvolby → Obecné → Lišta (podle §5
  plánu), nebo Vzhled? A má být „Přizpůsobit lištu…“ i v hlavní nabídce?
- **O-G4 Přepínač postranního panelu** v Poště a Nástěnce: GTK ho nemá
  (`NavigationSplitView`). Vynechat z katalogu GTK, nebo ho doplnit
  (přechod sloupce složek na `OverlaySplitView`, mimo rozsah lišty)?
- **O-G5 Hlavička detailu Nástěnky** ve Sloupcích a Dnes: skládat ji ze
  sekce detailu lišty Seznam (jeden strom), nebo ji tam nechat pevnou
  jako macOS?
- **O-G6 Přetečení.** Skrývat podle priorit dynamicky (`Fit`), nebo jen
  rozšířit dnešní breakpointy (pod 600 sp schovat položky nízké priority)?
- **O-G7 Skryté položky Nástěnky a Kalendáře**: do jejich hlavní nabídky
  (detail: `board_detail_menu`), nebo nikam?
- **O-G8 Texty v referenci.** Popisky položek do `ui/internal/toolbar`
  (společné pro GTK a Windows), nebo každému klientovi zvlášť jako na
  macOS?
- **O-G9 Pravý klik na lištu** (T11): v GTK vynechat (koliduje s nabídkou
  okna ve správci oken), nebo nabídnout na prázdném místě hlavičky
  „Přizpůsobit lištu…“?
