# Přizpůsobení lišty nástrojů (Toolbar customization)

## Context

Uživatel si chce sám poskládat lištu hlavního okna: přidat akce, které dnes jsou
jen v menu (Nevyžádaná, Přesunout do, Přečtené…), odebrat ty, které nepoužívá,
a změnit jejich pořadí. Inspirace je nativní paleta AppKitu (`NSToolbar`
s `allowsUserCustomization`), kterou používá i Mail. Démon a API se nemění,
je to čistě preference UI (pravidlo 1 beze změny).

Rozhodnutí vlastníka (AskUserQuestion 2026-11-04):

- **T1 Varianta B**: na macOS nativní paleta AppKitu, ale konfiguraci **neukládá
  AppKit** (`autosavesConfiguration = false`). Pořadí identifikátorů se ukládá do
  vlastních klíčů gschema (na macOS `UserDefaults` s klíči GSettings). Katalog
  položek a pravidla jsou čistý Go balíček `ui/internal/toolbar`, portovaný 1:1
  do `MalachiCore`.
- **T2 Rozsah**: hlavní okno v režimu **Pošta**, **Nástěnka** (styl Seznam a styl
  Sloupce/Dnes) a **Kalendář**. Okno zprávy, okno Nová zpráva a ostatní okna
  zůstávají pevná.
- **T3 Platformy**: **macOS první**, GTK a Windows později (výjimka z pravidla 6
  jako u Nástěnky). Dostanou editor v Předvolbách nad stejným katalogem a klíči.
- **T4 Pevné položky** (nejdou odebrat ani přesunout): přepínač režimu, název
  složky s počtem (titulek okna), přepínač panelu asistenta (sekce inspektoru)
  a oddělovače sekcí. Hledání, přepínač postranního panelu a ostatní položky
  odebrat jdou.
- **T5 Přeplnění**: položky mají `visibilityPriority` z katalogu. Co se v sekci
  nevejde, zmizí z lišty a objeví se v menu „…“ (Další akce).
- **T6 macOS 14**: úprava jen od **macOS 15**. Na macOS 14 se „Přizpůsobit
  lištu…“ nenabízí a lišta je výchozí jako dnes (stávající kód beze změny).
  Oprava při M2 (hlavička SDK): `toolbarImmovableItemIdentifiers`
  a `toolbar(_:itemIdentifier:canBeInsertedAt:)` jsou už od macOS 13. Od
  macOS 15 je jen `NSToolbarItem.isHidden` (podmíněná položka schovaná na
  svém místě, T7) a `allowsDisplayModeCustomization` (režim zobrazení
  v paletě, T9). Hranice macOS 15 tedy zůstává, jen s tímto důvodem.
- **T7 Podmíněné položky** (eWay-CRM jen s účtem eWay, ✦ jen s registrací
  v Claude, Triage jen s cílem „V aplikaci“, akce bez capability účtu): zůstávají
  v uložené konfiguraci. Bez funkce se na liště schovají (`isHidden`, macOS 15+)
  a po návratu funkce se vrátí na své místo. V paletě jsou jen tehdy, když funkce
  existuje.
- **T8 Menu „…“** doplňkově ukazuje akce, které na liště nejsou nebo se nevejdou.
- **T9 Režim zobrazení** (Jen ikony / Ikony a text / Jen text) z palety je povolený
  a ukládá se. Tlačítko „Uložit do eWay-CRM“ se v režimu Jen ikony chová jako
  ostatní položky (bez textu).
- **T10 Nové položky katalogu Pošty**: Přečtené/Nepřečtené, Nevyžádaná,
  Přesunout do, Tisk, Zpět, Načíst obrázky.
- **T11 Vstupy**: pravý klik na lištu (standard AppKitu) a Zobrazení ▸
  Přizpůsobit lištu… Žádné tlačítko v Předvolbách.

## 1. Dnešní stav (macOS)

- `MainWindow/MainToolbar.swift`: lišta Pošty (`NSToolbar.Identifier("main")`)
  a sekce zprávy pro okno zprávy (`messageSectionItems`). `defaultItems`:
  `mode, flexibleSpace │ sidebarTrackingSeparator, toggleSidebar, newMessage,
  contacts, filter, refresh, flexibleSpace │ listSeparator, reply, replyAll,
  forward, flexibleSpace, archive, trash, space, crm, space, star, assistant,
  moreActions, search` a sekce inspektoru `inspectorTrackingSeparator,
  flexibleSpace, toggleInspector`, dokud existuje panel asistenta.
  Už teď `allowsUserCustomization = false`, `autosavesConfiguration = false`.
- `Board/BoardToolbar.swift`: dvě lišty, `boardList` (Seznam: sekce sidebaru,
  seznamu a za `boardDetailSeparator` sekce detailu s Hotovo, Připomenout…,
  Archivovat, Smazat, Odpovědět) a `board` (Sloupce a Dnes, bez sekcí).
- `Calendar/CalendarToolbar.swift`: lišta `calendar` (Dnes, ‹ ›, pohledy, hledání,
  Kontakty, Nová událost, ✦).
- Lišty se dnes **vkládají a odebírají programově** a místa hledají podle
  sousedů (`firstIndex { … == ID.reply }`): `setListSeparator` (sbalený seznam),
  `setAssistant(visible:)` a `setAssistant(opensPanel:)` (výměna tlačítka
  za menu), `setAssistantPanel` (sekce inspektoru), výměna hledací lupy a pole
  (`searchLens`/`searchItem` pod jedním id), `BoardToolbar.setTriage`,
  `setDetailSection`, `rebindSidebarSeparator`, `rebindDetailSeparator`,
  `keepSidebarToggle`, `CalendarToolbar` ✦. `ActionPresentation.present` skrývá
  nepodporované akce účtu přes `isHidden` (jen macOS 15+).
- Tisk v aplikaci zatím **neexistuje** (žádný msgid, žádná akce `print:`).

## 2. Model (Go reference `ui/internal/toolbar`)

Čistý balíček bez gotk4, testovaný. Port `MalachiCore/Toolbar/` (Swift),
později `Malachi.Core/Toolbars/` (C#).

**Lišty** (`Bar`): `mail`, `board-list`, `board-flow`, `calendar`.

**Katalog** (`Item`): stabilní id (stejné jako dnešní `NSToolbarItem.Identifier`,
aby se nic nepřejmenovávalo), sekce (`sidebar`, `list`, `message`, `detail`,
`flow`), msgid popisku palety, priorita viditelnosti, příznaky `Fixed`
(nejde odebrat ani přesunout), `Structural` (vkládá ho jen aplikace, uživatel ho
v paletě nevidí ani neukládá: `listSeparator` při sbaleném seznamu, sekce
inspektoru, sekce detailu Nástěnky) a `Conditional` (funkce může chybět: `crm`,
`assistant`, `boardTriage`, `calendarAssistant`).

**Rozvržení** (`Layout`): uspořádaný seznam id po sekcích. Do klíče se ukládá
jako jeden seznam, ve kterém oddělovače sekcí stojí jako pevné značky
(`|list`, `|message`, …; první sekce oddělovač nemá). Mezery (`space`,
`flexibleSpace`) se ukládají taky, jako opakované tokeny bez přípon.

**Pravidla**:
- `Default(bar)`: dnešní výchozí lišty (sekce 1) beze změny.
- `Normalize(stored, bar) Layout`: prázdný klíč znamená výchozí lištu, takže
  kdo nic neupravil, dostane změny výchozí lišty v příštích verzích. Neznámá id
  (z novější nebo starší verze) se zahodí. Chybějící pevné položky a oddělovače
  se vrátí na své místo. Položka v cizí sekci se přesune do své (pravidla sekcí
  níže). Duplicity se zahodí.
- `CanInsert(item, section)`: položky zprávy (Odpovědět, Archivovat…) jen
  v sekci zprávy, nástroje seznamu (Filtr, Zkontrolovat poštu, Nová zpráva,
  Kontakty) jen v sekcích sidebaru a seznamu. Mezery kamkoli. Na macOS to volá
  `toolbar(_:itemIdentifier:canBeInsertedAt:)`.
- `Overflow(layout, available)`: které akce mají jít do „…“. Jsou to ty, které
  na liště nejsou, a ty, které AppKit právě schoval do přetoku (T5, T8). Menu „…“
  si to spočítá při každém otevření.
- `Present(layout, features) []Shown`: z uloženého rozvržení a dostupných funkcí
  udělá, co se ukáže. Podmíněné položky bez funkce jsou schované, ne odebrané (T7).

**Nové položky katalogu Pošty (T10)** a jejich výchozí stav: **mimo výchozí lištu**
(v paletě, ne na liště), aby se lišta stávajícím uživatelům nezměnila.
Kontextové menu a menu Zpráva je mají už dnes:

| id | popisek (msgid) | akce | poznámka |
|---|---|---|---|
| `markRead` | jedno přepínací tlačítko (O3), paletteLabel nový msgid | přepíná podle výběru | popisek jako menu Zpráva |
| `junk` | `Mark as Junk` (existuje) | `markAsJunk:` | |
| `moveTo` | `Move To` (existuje) | `NSMenuToolbarItem` se složkami (`contextmenu.MoveTargets`) | |
| `print` | nový | **nová funkce Tisk** (sekce 8), i v liště Kalendáře | |
| `undo` | `Undo` (existuje) | `undo:` přes řetězec responderů | zašedlé bez přesunu v zásobníku |
| `loadPictures` | `Load _Images` / `_Download Pictures` (existují) | podle pruhu zprávy | zašedlé, když zpráva nic neblokuje |

## 3. Klíče gschema

V `data/io.github.schotek.Malachi.gschema.xml` (GTK je zatím jen čte do
Předvoleb až v portu; přidání klíče GTK nerozbije):

- `toolbar-mail`, `toolbar-board-list`, `toolbar-board-flow`, `toolbar-calendar`:
  `as`, výchozí `[]` (= výchozí lišta).
- `toolbar-display-mode`: enum `icon` / `icon-and-label` / `label`, výchozí
  `icon`. Jeden pro všechny čtyři lišty (viz otázka O4).

Windows: `GschemaCoverageTests` hlídá pokrytí klíčů. Dokud Windows port
nepřijde, patří těchto pět klíčů s důvodem do `windows/parity-exclusions.txt`
(„toolbar customization: Windows port pending“), stejně jako nové msgid.

## 4. macOS (fáze M)

Jen pro macOS 15+ (`if #available(macOS 15, *)`). Na macOS 14 zůstane dnešní cesta.

1. **M1 Port modelu**: `MalachiCore/Toolbar/ToolbarCatalog.swift`,
   `ToolbarLayout.swift` a testy, které portují Go testy 1:1.
   `Settings.toolbarLayout(_ bar:)` / `setToolbarLayout` a `toolbarDisplayMode`.
2. **M2 Společný základ lišt**: nový `MainWindow/ToolbarCustomization.swift`
   (sdílený delegátový kód pro tři lišty):
   - `allowsUserCustomization = true` na 15+, `autosavesConfiguration = false`;
   - `toolbarAllowedItemIdentifiers` = katalog bez strukturálních položek,
     podmíněné jen s dostupnou funkcí;
   - `toolbarDefaultItemIdentifiers` = `Layout.Default`;
   - `toolbarImmovableItemIdentifiers` = pevné položky a oddělovače sekcí;
   - `toolbar(_:itemIdentifier:canBeInsertedAt:)` = `CanInsert` podle sekce
     indexu;
   - instalace lišty: po `NSToolbar(identifier:)` se položky poskládají
     z `Normalize(klíč)`. AppKit nemá „nastav položky“, takže se postaví
     vložením v pořadí;
   - uložení: `NSToolbar.willAddItemNotification` a `didRemoveItemNotification`
     a konec palety (`NSWindow` pozoruje `customizationPaletteIsRunning` přes KVO)
     → přečíst `toolbar.items`, vynechat strukturální, zapsat klíč jednou po
     zavření palety. Během palety se klíč nepíše;
   - `displayMode` se při změně z palety zapíše do `toolbar-display-mode`
     a použije na ostatní lišty okna.
3. **M3 Vkládání bez sousedů**: dnešní `insertItem(… at: firstIndex { sousedé })`
   přestane předpokládat pevné pořadí. Pozici strukturálních položek počítá model
   (`Layout.IndexOf(structural, currentItems)`). Podmíněné položky (`crm`,
   `assistant`, `boardTriage`, `calendarAssistant`) se na 15+ přestanou vkládat
   a odebírat a jen se schovávají (`isHidden`, T7). Výměna ✦ tlačítko ↔ menu
   a lupa ↔ pole zůstanou výměnou pod týmž id na místě, kde položka je; když
   ji uživatel odebral, nic se nevkládá.
4. **M4 Oddělovače sekcí**: `rebindSidebarSeparator`, `rebindDetailSeparator`
   a `keepSidebarToggle` se musí zavolat i po skončení palety. Paleta lištu
   přeskládá a sledování dělicí čáry se pak podle CLAUDE.md nenaváže samo.
   Měřit krokem `MALACHI_START` (M8).
5. **M5 Nové položky (T10)**: `markRead`, `junk`, `moveTo` (`NSMenuToolbarItem`
   s nabídkou `MessageContextMenu`), `undo`, `loadPictures`, `print` (podle O1).
   Validace přes stávající `validateUserInterfaceItem` okna a `ActionPresentation`
   (nepodporované akce účtu se schovají jako dnes).
6. **M6 Menu „…“ (T8)**: Další akce doplní akce z `Overflow` (nejsou na liště,
   nebo jsou v přetoku AppKitu, tedy `toolbar.items` s `isVisible == false`
   v přetoku; podle O5) jako první sekci menu.
7. **M7 Vstupy (T11)**: pravý klik na lištu dává AppKit sám, když
   `allowsUserCustomization` je zapnuté. Do menu Zobrazení přibude položka
   „Přizpůsobit lištu…“ s akcí `runToolbarCustomizationPalette:` (NSWindow ji
   validuje sám). Na macOS 14 položka není.
8. **M8 Ověření rozložení**: kroky `MALACHI_START` `toolbar:mail`,
   `toolbar:board-list`, `toolbar:board-flow`, `toolbar:calendar`
   s `MALACHI_TOOLBAR_LAYOUT=<seznam id>`. Krok rozvržení použije, vypíše
   `LAYOUT:` řádky (polohy položek, oddělovačů a sekcí) a ověří přepnutí
   Pošta → Nástěnka → Pošta s vlastní lištou. Spouští se jen debug binárkou
   nad izolovanými daty a vzorky, nikdy `build/Malachi Mail.app` (CLAUDE.md).

Brána **B1**: vlastník na macOS 15+ (debug binárka nad kopií dat): paleta
ze všech tří režimů, pevné položky nejdou ven, odebrání a vrácení položky,
přeplnění a „…“, přepínání režimů a stylů Nástěnky, sbalený seznam, panel
asistenta, eWay bez účtu a s účtem, Jen ikony / Ikony a text, restart
aplikace s uloženou lištou, „Výchozí sada“ z palety.

## 5. GTK a Windows (fáze G a W, po B1)

GTK ani WinUI nemají paletu. Oba dostanou v Předvolbách → Obecné skupinu
„Lišta“ s editorem nad katalogem (výběr lišty, seznam položek se zaškrtávátky
a pořadím přetažením a klávesami jako stránka Účty, Obnovit výchozí). Klíče
jsou stejné, takže konfigurace sedí mezi platformami. Hlavičkové lišty GTK
(`window.blp`) i titulková lišta Windows se pak skládají z `toolbar.Present`.
Podrobný návrh G a W vznikne po B1. Odchylka macOS (paleta místo editoru
v Předvolbách) přijde do tabulky v `macos/README.md`.

## 6. Řetězce

Nové msgid (Go reference, `po/POTFILES`, `make po`, česky v `cs.po`): popisky
palety nových položek (podle O1 a O3), případně „Přizpůsobit lištu…“
(viz O2). Položky lišty jen pro macOS (Filtr) zůstávají `// macOS-only string`.
Do `windows/parity-exclusions.txt` přijdou s důvodem do fáze W.

## 7. Rizika

- **Sledovací oddělovače**: AppKit po přeskládání lišty paletou nemusí
  `.sidebarTrackingSeparator` a vlastní `NSTrackingSeparatorToolbarItem` znovu
  navázat na dělicí čáru (zkušenost z Nástěnky). M4 a měření M8 to musí pokrýt.
- **Sekce bez přetoku**: unified toolbar nemá přetok po sekcích. Minimum sloupce
  seznamu (372 pt) počítá s dnešními položkami. Víc položek v sekci seznamu může
  sloupec rozšířit nebo vytlačit titulek. Priority (T5) a případně strop
  v `CanInsert` (O6).
- **`isHidden` a validace**: schovaná položka se nevaliduje (dnes to obchází
  `ActionPresentation.present` pro celou lištu). Podmíněné položky ji musí
  ukazovat znovu ze stejného místa.
- **Paleta a cache položek**: `MainToolbar.items` cachuje položky podle id.
  Paleta vyrábí vlastní kopie (`willBeInsertedIntoToolbar: false`) a ty se nesmí
  dostat do cache.
- **Okno zprávy** sdílí `MainToolbar` (`messageSectionItems`), ale je mimo rozsah
  (T2). Úprava nesmí změnit jeho lištu.

## 8. Tisk (součást této práce)

Rozhodnutí vlastníka (AskUserQuestion 2026-11-04, O1):

- **P1** Tisk vzniká **v rámci této práce**, ne zvlášť. Položka `print` je
  v katalogu Pošty (a Kalendáře, viz P2).
- **P2 Rozsah**: jedna zpráva, celá konverzace, víc vybraných zpráv (každá
  na nové stránce) a v Kalendáři událost a den.
- **P3 Klienti**: **macOS první**, GTK a Windows po ověření macOS (stejná výjimka
  z pravidla 6 jako u lišty, i když tisk je funkce mimo lištu).
- **P4 Vstupy**: Soubor ▸ Tisk… s ⌘P (GTK a Windows Ctrl+P) a položka lišty.
  Ne v menu „…“ ani v kontextovém menu (pokud ho tam nedoplní T8, protože
  na liště není).

Návrh (podrobnosti po průzkumu, než začne M5):

- **Jen sanitizované HTML.** Tiskový dokument skládá klient z `message.body`
  (`html`, bez `trimQuoted`, tedy s celou citací) a z hlavičky. Nikdy ze surové
  zprávy (pravidlo 2). Tiskne se z uzamčeného webového pohledu se stejnou CSP,
  vypnutým JS obsahu a odříznutou sítí jako při čtení. Vzdálené obrázky jen
  podle politiky zprávy (co je načtené, to se vytiskne). Zpráva bez HTML se
  tiskne jako prostý text v `<pre>`, escapovaný.
- **Hlavička tisku** (Od, Komu, Kopie, Datum, Předmět, přílohy jménem
  a velikostí) je čistá funkce Go reference (`ui/internal/printing`: řádky
  hlavičky, texty, pořadí zpráv konverzace od nejstarší, zalomení stránek mezi
  zprávami) s escapováním každého řetězce. Port do `MalachiCore/Printing`.
  Hlavička je HTML, které skládá aplikace, ne text zprávy, a do dokumentu přijde
  jako samostatný blok před sanitizovaným tělem.
- **Více zpráv a konverzace**: jeden tiskový dokument, každá zpráva
  ve vlastním `<section>` s `break-before: page` (u konverzace bez zalomení,
  jen oddělovač; rozhodne se v B1). Těla se načtou předem přes `MessageCache`.
  Strop počtu (návrh 50, jako u Otevřít je 10) a souhrnný počet v tiskovém
  dialogu.
- **Kalendář**: událost = detail (název, čas, místo, opakování, účastníci
  s odpověďmi, poznámky jako prostý text). Den = agenda dne. Obojí skládá
  aplikace jako HTML z dat API, bez obsahu pošty.
- **macOS**: `WKWebView.printOperation(with:)` nad skrytým uzamčeným
  `MessageWebView` (stejná konfigurace jako čtení, `NSPrintOperation` s okny
  jako sheet). Měřit kroky `MALACHI_START` `print:message|conversation|selection|event|day`
  se zápisem do PDF (`NSPrintJobSavingURL` do izolovaného adresáře), nikdy na
  skutečnou tiskárnu.
- **GTK** (později): `WebKitPrintOperation` nad `htmlview`. **Windows** (později):
  `CoreWebView2.PrintAsync` / `ShowPrintUI` nad `HardenedWebView`.
- **Bezpečnost**: doplnit `docs/security.md` (tiskový dokument nedostane nic,
  co nedostává čtení; žádné vzdálené požadavky při tisku; PDF přes systémový
  dialog je věc uživatele).

## 9. Odpovědi na otázky (2026-11-04)

- **O1** viz sekce 8.
- **O2 „Přizpůsobit lištu…“**: zatím otevřené, rozhodne se měřením v M7. Pravý
  klik lokalizuje AppKit sám podle jazyka bundlu. Pokud AppKit nedá český titul
  i položce v menu Zobrazení, přibude vlastní msgid.
- **O3 Přečtené**: **jedno přepínací tlačítko**. Podle výběru označí jako
  přečtené nebo nepřečtené a mění ikonu i popisek. Jako popisek se použije
  `Mark as _Read` / `Mark as _Unread` bez mnemoniky, paletteLabel jeden nový
  msgid.
- **O4 Režim zobrazení**: **jeden pro všechny lišty** (`toolbar-display-mode`).
- **O5 ⌘F bez hledání na liště**: pole se **dočasně ukáže** na konci sekce zprávy
  (u Kalendáře na konci lišty) jen na dobu hledání. Po vyprázdnění a ztrátě
  klávesnice zmizí. Uložená lišta se nemění (dočasné pole je strukturální
  položka).
- **O6 Strop**: **jen priority**, žádný pevný strop.

## 10. As built (M1, 2026-11-04)

- Go reference `ui/internal/toolbar` (`toolbar.go`, `toolbar_test.go`):
  `Bar`, `Key`, `DisplayMode`, `ID` (macOS identifiers, AppKit items as
  `space`/`flexibleSpace`/`toggleSidebar`, separators `|list`, `|message`,
  `|detail`, `|main`), `Section`, `Feature` (`crm`, `assistant`, `triage`,
  `print`), priorities with AppKit's raw values (user 2000, high 1000,
  standard 0, search −500, low −1000; values from today's toolbars),
  `Catalog`, `Default`, `Normalize` (at most `MaxTokens` 128 tokens),
  `Stored`, `SectionAt`, `CanInsert`, `Immovable`, `Allowed`, `Present`,
  `MissingActions`, `SearchItem`, `SearchIndex` (−1 = none). No texts, so
  nothing in `po/POTFILES`; the labels stay the clients'. Mail's sidebar
  section has no separator token: the first section starts the layout.
- Swift port `MalachiCore/Toolbar/Toolbar.swift` (`enum Toolbar`,
  `searchIndex` returns nil for −1) and `ToolbarTests` (Go tests 1:1);
  `Settings` keys `toolbar-mail`, `-board-list`, `-board-flow`,
  `-calendar`, `-display-mode`, `toolbarLayout(_:)` /
  `setToolbarLayout(_:_:)` (normalized read, `Toolbar.stored` write),
  `toolbarDisplayMode`; `SettingsTests.toolbarLayouts`, 50 keys.
- gschema: enum `ToolbarDisplayMode` and five keys at the end of the
  schema; `make po` in the container added their seven summary and
  description msgids, `cs.po` translated (2027 messages, no fuzzy).
- Windows (written without `dotnet`): `SettingsKey.Toolbar*` before `CtrlR`,
  the five entries in `SettingsStore` with `ToolbarDisplayModeNicks`,
  `SettingsTests` count 54, the seven gschema msgids in
  `windows/parity-exclusions.txt`. No properties yet (Windows port later).
- Verified on this Mac: `go vet` and `go test ./internal/toolbar`,
  `swift build`, `swift test --filter 'ToolbarTests|SettingsTests'` (44).
  Not run: `build.ps1 test`/`lint` on Windows (`GschemaCoverageTests`,
  `SettingsTests`, `StringsCheckTests`), `make lint` in the container.

## 11. As built (M2, 2026-11-04)

- `MainWindow/ToolbarCustomization.swift`: one per customizable toolbar
  (Mail, the Board's List and Columns/Today, Calendar). It maps the
  model's tokens to identifiers (`|list` → `.sidebarTrackingSeparator`,
  Mail `|message` → `listSeparator`, Board `|detail` →
  `boardDetailSeparator`, Calendar `|main` → `.sidebarTrackingSeparator`)
  and gives the delegates `defaultIdentifiers` (the user's arrangement,
  while the palette runs the default set), `allowedIdentifiers`,
  `immovableIdentifiers`, `canInsert(_:at:in:)` (section from the
  separators before the place, never into the inspector section),
  `insertionIndex(of:in:)` (where the app puts a separator or section
  back) and `isShown`. `attach` sets the display mode from
  `toolbar-display-mode` and follows its changes. From macOS 15 it also
  turns on `allowsDisplayModeCustomization` and `allowsUserCustomization`,
  the latter only while no section or separator is out of the toolbar.
  The palette's changes (`toolbarWillAddItem`/`DidRemoveItem` while
  `customizationPaletteIsRunning`) are saved. At the end of the window's
  sheet (`NSWindow.didEndSheetNotification`, the palette is a sheet; no
  KVO) the display mode is saved and `onPaletteEnd` rebinds the sidebar
  separator and toggle (and the Board List's detail separator).
- Delegates: `MainToolbar` (main window only, `settings:`; a message
  window's toolbar stays fixed), `BoardToolbar` (`settings`,
  `onPaletteEnd`), `CalendarToolbar` (`settings:`). The palette gets fresh
  items that are never cached. Mail's search, ✦ and eWay-CRM are plain
  image items there, because making them has effects or their view is
  empty. From macOS 15 ✦ (Mail, Calendar) and Triage stay in the toolbar
  and are hidden with `isHidden` instead of being inserted and removed.
  Mail's list separator and the Board's detail section come back where
  the arrangement has them. Jira's Change Status goes into More Actions'
  own item (`MainToolbar.moreActionsItem`,
  `ChangeStatusMenus.install(into:)`), also when the user took it off.
- Verified: `swift build` without warnings; one isolated run of the debug
  binary (`MALACHI_DAEMON=none`, empty `MALACHI_DATA_DIR`, board samples,
  `mail;board:list;calendar:week;mail;quit`) with a stored Mail
  arrangement: the Mail toolbar was built from it, ✦ hidden in place, the
  Board and Calendar toolbars default. The `MalachiMail` domain was
  restored afterwards and `io.github.schotek.Malachi` stayed unchanged.
- Known: an item of the arrangement that the delegate cannot make yet
  (`junk` and the other T10 items until M5) is left out by AppKit. In that
  run the mode switch was in Mail's overflow with a 200 pt sidebar
  (priority 0 in `MainToolbar`, `.user` on the Board); this probably
  predates M2 and was not compared with `HEAD`. Not tried: the palette
  itself (opening, dragging, the default set, display mode), which is the
  owner's B1.

## 12. As built (M3–M5, 2026-11-04)

- **M3**: what the app puts back goes where the arrangement has it
  (`ToolbarCustomization.insertionIndex`, from M2: Mail's list separator,
  the Board's detail section). From macOS 15 ✦ and Triage are hidden
  instead of inserted. ⌘F (and text the app puts into the field) while the
  arrangement has no search field (O5): Mail inserts the field unfolded at
  the end of the message section (`MainToolbar.insertTemporarySearch`) and
  removes it once it is empty and has lost the keyboard
  (`removeTemporarySearch`, instead of folding to the lens). The Calendar
  does the same (`CalendarToolbar.focusSearch`, `controlTextDidEndEditing`).
  The temporary field is never saved (`ToolbarCustomization.transient`).
  The assistant panel's section keeps going after the search field, or at
  the end without one.
- **M4**: `onPaletteEnd` (M2) rebinds the sidebar separator and toggle in
  all three modes and the Board List's detail separator. Mail's own list
  separator is cached and immovable, so it is not made anew. Not measured
  after a real palette run.
- **M5**: Mail items `markRead` (one item, O3: Mark as Read while any
  selected row is unread, else Mark as Unread, with image, label and
  action set by `MainWindowController.validateToolbarItem`; the palette
  label is the existing `Mark as _Read`, so no new msgid), `junk`
  (`markAsJunk:`), `moveTo` (`MoveToToolbarItem`, the selection's
  account's folders through `MessageContextMenu.moveToMenu(mailbox:rows:)`,
  enabled by the window's `allows(moveToFolder:)`), `undo`
  (`ProvidedToolbarItem` sending `undo:` to `MainWindow`, enabled by
  `MainWindow.toolbarUndoEnabled`) and `loadPictures` (`loadImages:`), all
  at the catalog's low priority. **Print is not there yet**: printing does
  not exist (section 8), `Feature.print` is never on, so the palette does
  not offer the item.
- Verified: `swift build` without warnings; a second isolated run (Mail
  arrangement with all five new items and no search field,
  `mail;search-own=abc;mail;search-clear;mail;quit`): every item was made,
  the search field came in for the search and went with search-clear, and
  the stored arrangement stayed as written. The temporary field measured
  33 pt against 240 preferred in a full toolbar (to look at in B1). The
  `MalachiMail` domain was restored afterwards and
  `io.github.schotek.Malachi` stayed unchanged.
- Still open: M6 (More Actions adds what the toolbar lacks), M7
  (View ▸ Customize Toolbar…), M8 (`MALACHI_START toolbar:*` steps), print
  (section 8), B1.

## 13. As built (M6–M8, 2026-11-04)

- **M6**: More Actions (`MainToolbar.menuNeedsUpdate`) starts with the
  actions of the **default** toolbar that the user's arrangement does
  not show: Reply, Reply All, Forward, Archive, Move to Trash, Star, and
  Save to eWay-CRM… while its button would show (`CRM.Offer.shown`). They
  are marked items, rebuilt at every opening, validated and titled by the
  window like the menu bar's, followed by a separator. Change against
  the plan (T8): the palette's own items (T10) are not `InMore` in the
  catalog (Go and Swift, tests changed), because they are in the menu bar
  and the context menu already, and an untouched toolbar keeps its More
  Actions as it was. AppKit's overflow (») lists what did not fit, so
  More Actions does not repeat it.
- **M7 / O2**: View ▸ Customize Toolbar… (`runToolbarCustomizationPalette:`,
  NSWindow validates it) from macOS 15 only. Decided by the menu's
  convention: an English `// macOS-only string`, like Hide Sidebar and
  Hide Message List there. No new msgid; the toolbar's context menu is
  AppKit's own and localized by the system.
- **M8**: `MALACHI_START` steps (`MainWindowController+DevStartToolbar.swift`)
  `toolbar=<tokens>`, `toolbar-display=<mode>`, `toolbar-palette`,
  `toolbar-palette-end`, which act on the toolbar in front and dump its
  items, the stored arrangement and the display mode. They replace the
  `MALACHI_TOOLBAR_LAYOUT` the plan named. For them and for changes made
  elsewhere (another process, `defaults write`) the toolbar is built again
  from the stored arrangement when it differs
  (`ToolbarCustomization.reloadIfChanged`, never while the palette runs,
  a section is out or the toolbars themselves are saving).
  `MainWindowController.currentToolbarCustomization` names the toolbar in
  front.
- Verified: `go test ./internal/toolbar`, `swift build` without warnings,
  `swift test --filter 'ToolbarTests|SettingsTests'` (44),
  `check-strings.py` (no new warnings), a third isolated run
  (`toolbar=` in Mail, Board List and Calendar, `toolbar-display=icon-and-label`,
  `toolbar-palette` / `toolbar-palette-end`). Each toolbar was built again
  from its arrangement and the display mode reached every toolbar and was
  stored. The palette opened (`customizationPaletteIsRunning` 1) and
  ended, and the arrangement stayed. The `MalachiMail` domain was restored
  and `io.github.schotek.Malachi` stayed unchanged.
- Not verified: dragging in the palette (no step does it), the default
  set, More Actions on screen, the mode switch in Mail's overflow at a
  200 pt sidebar (see section 11). The owner's B1 remains; print
  (section 8) is still to be built.

## 14. B1 (owner, 2026-11-04)

Owner verified the palette in all three modes (right click, the items,
saving, display mode). Findings, fixed:

- **Labels running into each other in the default set.** The mode
  switch's segment labels are wider than its icons. They now have none
  (`ModeSwitch.item`: segments, group label and palette label empty; the
  names stay tooltips and accessibility descriptions), because an empty
  label made AppKit show the palette label cut to "P". The palette's copy
  is a plain item with a segmented control sized to its icons
  (`ModeSwitch.paletteItem`), because AppKit measured the item group
  narrower than its segments there and the style switch ran over it.
- **Triage squeezed to "Zas ta…"** in the Board's default set (its row is
  narrower than the Columns toolbar). The palette's copy has the plain
  title and the standard priority.
- `toolbar-palette-end` dumps the palette's texts and the row of the
  default set. `MALACHI_START_TOOLBAR_SHOT=<png>` writes that row, which
  AppKit draws as one image, to look at. A third isolated run showed "P"
  and the squeezed Triage gone, and the style switch over the mode switch,
  which led to `ModeSwitch.paletteItem`. That last fix was built but not
  looked at (three runs are the limit); for the owner.
- Console: `CRMToolbarItem` warned about an ambiguous zero size at every
  measuring of the toolbar. It hid itself with `isHidden` already, but
  also hid every view of its stack, so the stack had no size. From macOS
  15 its save button now stays in the hidden item's view (macOS 14
  unchanged: no hidden items there, the item empties). This exposed an
  M2 bug: `MainToolbar.hideIfMissing` cleared the item's own `isHidden`
  (Mail's features always count eWay-CRM as there) and now skips
  `CRMToolbarItem`. Two isolated runs (Mail, palette open and closed): no
  warning, the item `hidden 1` and off the window. The palette's mode
  switch on the Board's Columns and Today stands ahead of the title as in
  the real toolbar (`ModeSwitch.paletteItem(navigational:)`); the owner
  saw the default sets right.
