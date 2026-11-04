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

Rozhodnutí vlastníka (AskUserQuestion 2026-11-04, po průzkumu kódu):

- **P5 Stránky**: konverzace souvisle (zprávy od nejstarší, oddělené
  čarou a hlavičkou), víc vybraných zpráv každá na nové stránce.
- **P6 Hlavička**: Od, Komu, Kopie, Datum, Předmět, seznam příloh (jména
  a velikosti, bez vložených obrázků), Skrytá kopie jen u vlastních zpráv
  (složka role Odeslané, Koncepty, Outbox), systémové záhlaví a zápatí
  stránky (`NSPrintInfo.isHeaderAndFooter`: název úlohy, datum, číslo
  stránky).
- **P7 Den v Kalendáři** jako seznam událostí dne (agenda: celodenní
  nahoře, pak čas, název, místo).
- **P8 ⌘P v Kalendáři**: vybraná událost, jinak den, na kterém stojí pohled
  (u týdne a měsíce den kotvy).

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

## 15. W (návrh, Windows agent, 2026-11-04)

Návrh portu pro Windows podle §5, napsaný nad dnešním `windows/`. UI se
podle něj zatím nepíše. Otázky v W.12 rozhoduje vlastník.

### W.0 Ověření větve na Windows

Ověřeno 2026-11-04 na Windows 11 Pro x64 nad `b55f6e47`. Změny klíčů
z M1 (pět klíčů v `SettingsKey`/`SettingsStore`, `SettingsTests` 54, sedm
msgid gschema v exclusions) prošly bez úprav:

- `build.ps1 app` prošel (Release x64, NativeAOT helper). Jediné varování,
  NU1903 u `Microsoft.DotNet.ILCompiler`, je i na `main`.
- `build.ps1 lint` prošel: `dotnet format` čistý, konvenční testy 28/28,
  mezi nimi `GschemaCoverageTests` a `StringsCheckTests`.
- `build.ps1 test`: 9 353 testů, 9 314 prošlo, 36 přeskočeno, 3 selhaly.
  `SettingsTests` prošly. Žádné selhání s větví nesouvisí:
  - `NetworkCanaryTests.NoWindowOpened`
    a `NetworkCanaryTests.ActivatedLinksReachTheReaderAndNothingElse`
    jsou známý kanárek WebView2 z `main`;
  - `Model.UnifiedTests.TheModelListCoversItsInboxes` (čeká odznak 5,
    dostane 0) selhává stejně i na čistém `main` (`01c00704`). Nejspíš je
    to přesun Všech doručených do Oblíbených (U3), na Windows napsaný bez
    sestavení. To patří jinam než do této větve.

### W.1 Dnešní stav (Windows)

Windows nemá jednu lištu okna. Má titulkovou lištu a pod ní pruhy
jednotlivých panelů a WinUI pruh nemá vlastní přetok:

- **Titulková lišta** (`MainWindow.xaml` `AppTitleBar`, ve všech režimech):
  přepínač režimu `ModeSwitch`, pole `SearchBox` s `OwnWordsButton`
  a v úzkém rozložení tlačítka panelu a Zpět.
- **Pošta**:
  - řádek postranního panelu (`SidebarPane`: zvýrazněné Nová zpráva
    a „…“ hlavní nabídky);
  - hlavička seznamu (`MessageListPane.HeaderRow`: Zkontrolovat poštu,
    název složky s počty) a pod ní řádek filtru `FilterBar`
    (`SelectorBar` Vše / Nepřečtené / Označené);
  - pruh zprávy (`MessageCommandBar`): Odpovědět, Všem, Přeposlat |
    Archivovat, Koš, eWay-CRM, Hvězdička, přepínač panelu asistenta, ✦,
    Další akce (Nevyžádaná, Nepřečtené, Přečtené, Načíst obrázky,
    Důvěřovat odesílateli, Změnit stav).
- **Nástěnka**:
  - lišta stránky (`BoardPage.Bar`: styl, nadpis, filtr účtu, Triage,
    „…“), společná pro všechny styly;
  - řádek akcí detailu (`BoardDetailView`): Zavřít, Hotovo, Připomenout…,
    Archivovat, Smazat, „…“, Odpovědět. Detail je vedle Seznamu nebo ve
    vysouvacím panelu.
- **Kalendář**: `CalendarBar` (postranní panel, Dnes, ‹ ›, pohledy, nadpis,
  Nová událost, ✦) a „…“ stránky. Pod `CalendarMetrics.BarCompactWidth`
  se pohledy mění v `ComboBox`, protože pruh přetok nemá.
- **Kontakty** jsou dnes jen v „…“ (tlačítko v liště má jen macOS).
  Přepínač postranního panelu Pošty existuje jen jako tlačítko titulkové
  lišty v úzkém rozložení.

### W.2 Kam patří sekce modelu

| lišta / sekce | pruh na Windows |
|---|---|
| `mail` sidebar | řádek `SidebarPane` (před stálým „…“ hlavní nabídky) |
| `mail` list | `MessageListPane.HeaderRow` (před názvem složky) |
| `mail` message | `MessageCommandBar` |
| `board-list` sidebar + list | `BoardPage.Bar` (nejdřív položky sidebaru, oddělovač se nekreslí, protože pruh není zarovnaný se sloupci) |
| `board-list` detail | řádek akcí `BoardDetailView`, dokud styl Seznam ukazuje detail (vedle seznamu i v panelu, je to týž pohled) |
| `board-flow` main | `BoardPage.Bar` ve stylech Sloupce a Dnes |
| `calendar` sidebar + main | `CalendarBar` |

Položky, které Windows kreslí jinde, jsou odchylky do tabulky
`windows/README.md`:

- **`mode`** (pevný) je vždy přepínač titulkové lišty a v žádném pruhu se
  nekreslí.
- **`search` a `calendarSearch`** jsou pole titulkové lišty. Počítá se jen
  to, zda jsou v rozvržení, na jejich pozici nezáleží.
- **`filter`** je řádek `FilterBar` pod hlavičkou seznamu, také jen podle
  přítomnosti (W.12 Q-W7).

Strukturální položky jen pro Windows nejsou v katalogu, jsou pevné
a v editoru se neukazují:

- „…“ hlavní nabídky na konci řádku `SidebarPane`, `BoardPage.Bar`
  a `CalendarBar`;
- přepínač panelu asistenta v `MessageCommandBar` (= macOS
  `toggleInspector`);
- název složky s počty (= titulek okna macOS) a nadpis Nástěnky
  a Kalendáře;
- Zavřít a „…“ detailu Nástěnky.

### W.3 Core (`Malachi.Core/Toolbars`)

- **`Toolbar.cs`**: `public static partial class Toolbar` ve jmenném
  prostoru `Malachi.Core.Toolbars` (podle vzoru `Boards`/`Board`). Je to
  port Go 1:1: `Bar`, `DisplayMode`, id položek (konstanty se stejnými
  řetězci), `Section`, `Feature`, `Item`, `Catalog`, `Default`, `Normalize`
  (`MaxTokens` 128), `Stored`, `SectionAt`, `CanInsert`, `Immovable`,
  `Allowed`, `Features`, `Present`, `MissingActions`, `SearchItem`,
  `SearchIndex`.
- **`ToolbarTests.cs`** v `Malachi.Core.Tests/Toolbars` jako 1:1 port
  `toolbar_test.go`, případy navíc ze Swift `ToolbarTests`.
- **`SettingsStore`**: `ToolbarLayout(bar)` (čte normalizovaně),
  `SetToolbarLayout(bar, layout)` (zapisuje `Stored`, výchozí jako `[]`)
  a `ToolbarDisplayMode`. Změny se hlásí stávající událostí klíče. Testy
  jsou v `SettingsTests` (round-trip, výchozí ukládá prázdný seznam,
  neznámý nick dá `icon`).
- **`ToolbarFit`** (jen Windows, čistá funkce): ze šířek, priorit
  a dostupné šířky určí, co se nevejde. Ustupuje nejnižší priorita, při
  stejné prioritě položka dál vpravo (jako přetok AppKitu). Pevné
  a strukturální položky neustupují nikdy. Má vlastní testy.
- **`ToolbarEditorController`** popisuje W.6.
- **`Features`** okna skládá kontroler nad stávajícím stavem:
  - `crm`: existuje účet eWay-CRM;
  - `assistant`: `Assistant.Shown`;
  - `triage`: podmínky tlačítka Triage
    (`BoardTriageController.TriageNeedsInAppTarget`);
  - `print`: vždy vypnuto, dokud tisk na Windows neexistuje;
  - `HiddenSections` = detail Nástěnky bez ukázaného případu;
  - `HiddenSeparators` Windows nepotřebuje, oddělovače nekreslí.

### W.4 Pruhy podle `Toolbar.Present`

- **`Malachi.App/Toolbars/ToolbarStrip`** je `Panel`, který řadí potomky
  v pořadí `Present`:
  - `space` je 12 DIP a `flexibleSpace` si dělí zbytek;
  - schovaná položka (T7) je `Collapsed` a drží své místo v pořadí;
  - když se pruh nevejde, `ToolbarFit` určí, co ustoupí;
  - pořadí potomků je i pořadí tabulátoru.
- **Ovládací prvky se vyrábějí jen jednou.** Pro každou lištu jedna
  továrna (`MailToolbarItems`, `BoardToolbarItems`,
  `CalendarToolbarItems`) vrací podle id ovládací prvek, glyf
  (`Icons.Glyph`), popisek a tooltip. Dnešní tlačítka (`ReplyButton`,
  `ArchiveButton`, `StyleSwitch`…) se do ní přesunou a zůstanou cachovaná.
  `CommandBinding`, `ActionPresentation` (skrytí akcí, které účet nemá)
  i automatizační id proto zůstávají. Položka je skrytá, když ji skryje
  `Present` nebo `ActionPresentation`.
- **Přestavba**: změna klíče lišty v `SettingsStore` (z editoru nebo
  z jiného procesu) jen znovu použije `Present`, nic se nevyrábí znovu.
  Změna funkcí (účet eWay, registrace v Claude, cíl asistenta, ukázaný
  případ) jen přepočítá `Features`.
- **Režim zobrazení** (`toolbar-display-mode`, jeden pro všechny lišty,
  O4) je společný styl obsahu tlačítek:
  - Jen ikony = dnešní `FontIcon`;
  - Ikony a text = `FontIcon` a pod ním popisek (jako `AppBarButton`),
    řádek pruhu je pak vyšší;
  - Jen text = popisek bez glyfu.

  Přepínač režimu titulkové lišty, pole hledání a výběr pohledu Kalendáře
  zůstávají v každém režimu stejné.
- **Kotvení**: dnešní pevné `Grid`y pruhů (`StartGroup`/`EndGroup`
  `MessageCommandBar`, sloupce `BoardPage.Bar` a `CalendarBar`) nahradí
  `ToolbarStrip`. Výchozí rozvržení pak musí vypadat jako dnes (W.12
  Q-W5) a ověří se snímky UI smoke testů před změnou a po ní.

### W.5 Položky katalogu na Windows

| id | dnes na Windows | v portu |
|---|---|---|
| `mode` | přepínač v titulkové liště | beze změny (pevný) |
| `toggleSidebar` | jen tlačítko titulkové lišty v úzkém rozložení; Kalendář `SidebarButton` | nové tlačítko v Poště a na Nástěnce Seznam; v Kalendáři existující |
| `newMessage` | zvýrazněné tlačítko `SidebarPane` | existuje, kde stojí podle výchozího rozvržení viz Q-W5 |
| `contacts` | jen „…“ | nové tlačítko (Pošta, Nástěnka, Kalendář) |
| `filter` | řádek `FilterBar` | jen přítomnost, Q-W7 |
| `refresh` | `MessageListPane` | existuje |
| `reply`, `replyAll`, `forward`, `archive`, `trash`, `crm`, `star`, `moreActions` | `MessageCommandBar` | existují |
| `assistant` | ✦ v `MessageCommandBar` | existuje (podmíněná) |
| `search` | pole titulkové lišty | jen přítomnost |
| `markRead` | Další akce | nové přepínací tlačítko (O3, popisek a glyf z `ActionFlags`) |
| `junk`, `loadPictures` | Další akce | nová tlačítka nad stávajícími příkazy |
| `moveTo` | kontextové menu | nové `DropDownButton` s `ContextMenu.MoveTargets` |
| `undo` | Ctrl+Z (`ShortcutCommand.Undo`) | nové tlačítko, zašedlé bez přesunu v zásobníku |
| `print` | chybí | `Feature.Print` vypnutý, v editoru ani v liště není |
| `boardStyle`, `boardAccount`, `boardTriage` | `BoardPage.Bar` | existují (Triage podmíněná) |
| `boardDone`, `boardRemind`, `boardArchive`, `boardTrash`, `boardReply` | řádek akcí `BoardDetailView` | existují, viz Q-W6 |
| `calendarToday`, `calendarStep`, `calendarView`, `calendarNewEvent`, `calendarAssistant` | `CalendarBar` | existují; `calendarView` dál sám přechází na `ComboBox` |
| `calendarSearch` | pole titulkové lišty | jen přítomnost |

### W.6 Editor v Předvolbách

- **Místo**: navrhuji vlastní stránku navigace Předvoleb „Lišta“ hned
  za Obecné (tag `toolbar`). Editor je seznam s asi 25 řádky a Obecné
  už má deset skupin. Plán §5 počítá se skupinou v Obecné, proto Q-W1.
- **Nahoře**: výběr lišty (`ComboBox`: Pošta, Nástěnka – Seznam, Nástěnka
  – Sloupce a Dnes, Kalendář). Otevře se na liště režimu, ze kterého
  uživatel přišel. Vedle je výběr Zobrazit (Jen ikony, Ikony a text,
  Jen text), jeden pro všechny lišty.
- **Seznam** je `ListView` se záhlavím sekce (název sekce; záhlaví se
  nedá přetáhnout ani vybrat). Řádek má úchyt, glyf, popisek a zaškrtávátko
  „na liště“. Mezera a pružná mezera mají místo zaškrtávátka ✕.
  - Pevná položka (`mode`) je zaškrtnutá, zašedlá a bez úchytu.
  - Podmíněná položka bez funkce se v editoru neukazuje (T7: jen
    s funkcí), v rozvržení ale zůstává.
  - Odškrtnutá položka zůstane v editoru na konci své domovské sekce
    (neukládá se). Po zaškrtnutí jde do lišty tam, kde v editoru stojí.
- **Řazení** jako na stránce Účty (`AccountListSection`):
  - přetažení za úchyt s mezerou `ListView` pro vložení;
  - Ctrl+↑ a Ctrl+↓ posunou vybraný řádek, a to i přes záhlaví sekce,
    jen když to dovolí `CanInsert`;
  - mezerník přepne zaškrtávátko;
  - upuštění do sekce, kam položka nesmí, se vrátí na původní místo.
- **Tlačítka**:
  - Přidat mezeru a Přidat pružnou mezeru vloží řádek za vybraný, v jeho
    sekci;
  - Obnovit výchozí zapíše `[]` jen pro vybranou lištu (otázka Q-W10).
- **Zápis** je okamžitý jako u ostatních předvoleb (`SetToolbarLayout`)
  a hlavní okno ho sleduje živě. Náhled v Předvolbách není.
- **`ToolbarEditorController`** (Core, testovaný) drží řádky, `SelectBar`,
  `Toggle`, `Move(from, to)` / `MoveUp` / `MoveDown` s `CanInsert`,
  `AddSpace`, `RemoveSpace`, `Reset`, `SetDisplayMode` a při změně klíče
  odjinud načte lištu znovu. Stránka je jen jeho pohled.
- **Vstup**: Předvolby → Lišta. Volitelně pravý klik na pruh s položkou
  „Přizpůsobit lištu…“, která otevře stránku na té liště (Q-W2).

### W.7 ✦, Triage a podmíněné položky (T7)

Schované zůstávají na svém místě (`Collapsed` v pořadí `ToolbarStrip`).

- Dnešní ruční přepínání viditelnosti ✦ v `MessageCommandBar`
  a `CalendarBar`, Triage v `BoardPage` a eWay-CRM `CrmCommand` řídí
  `Features` a `Present`.
- Výměna ✦ tlačítko ↔ nabídka (`Assistant.ButtonOpensPanel`) se odehrává
  uvnitř prvku položky a pozici nemění.
- Když uživatel položku z rozvržení odebral, aplikace ji nevkládá.

### W.8 Dočasné hledání (O5)

- Bez `search` (Pošta) nebo `calendarSearch` (Kalendář) v rozvržení je
  pole titulkové lišty v tom režimu `Collapsed`.
- Ctrl+F nebo Ctrl+E (`ShortcutCommand` hledání) ho ukáže na obvyklém
  místě a dá mu klávesnici. Hledání vlastními slovy (✦ vedle pole) se
  ukazuje s ním.
- Když je pole prázdné a ztratí klávesnici, zase zmizí. Uložené rozvržení
  se nemění.
- `SearchIndex` Windows nepotřebuje, protože pole má pevné místo
  v titulkové liště.

### W.9 Další akce (M6) a „…“

- **Další akce**: `MessageCommandBar.MoreButton` při otevření
  (`Opening`) vloží na začátek `Toolbar.MissingActions(mail, …)` (Odpovědět,
  Všem, Přeposlat, Archivovat, Koš, Hvězdička, eWay-CRM, když by se jeho
  tlačítko ukázalo). Za ně přijde, co `ToolbarStrip` pruhu zprávy schoval
  pro nedostatek místa, a oddělovač. Položky mají tytéž příkazy
  (`CommandBinding`), takže je i stejně validuje.
- **Ostatní pruhy** nemají Další akce. Co se nevejde do `BoardPage.Bar`,
  `CalendarBar`, hlavičky seznamu nebo řádku `SidebarPane`, navrhuji
  ukázat tlačítkem » na konci pruhu, které je jen tehdy, když něco
  ustoupilo (Q-W3). Hlavní nabídku „…“ se tím neplní, její obsah vlastník
  ustálil 2026-11-04 (PR #20).
- **Hlavní nabídka „…“** zůstává beze změny a v katalogu není.

### W.10 Řetězce

- **Popisky položek** jsou existující msgid: `Reply`, `Reply All`,
  `Forward`, `Archive`, Koš podle `trashTooltip`, `Save to eWay-CRM…`,
  `Undo`, `Move To`, `Mark as Junk`, `Mark as _Read` / `Mark as _Unread`,
  `Load _Images`, `_Contacts`, `Check for New Mail`, `New Message`,
  `Today`, `New Event`, `More Actions`, `Done`, `Remind…` a texty Nástěnky
  a Kalendáře.
- **Chybí** popisek přepínače postranního panelu (`Hide Sidebar` je na
  macOS jen anglicky) a Triage pro editor.
- **Editor potřebuje nové msgid**: název stránky nebo skupiny, názvy lišt
  a sekcí, režimy zobrazení, Mezera, Pružná mezera, Přidat mezeru, Přidat
  pružnou mezeru, Obnovit výchozí, volitelně Přizpůsobit lištu…
  - Podle pravidla 6 vzniknou jako Go reference (`ui/internal/toolbar/text.go`
    s `Translator` jako `jira.Translator`, v `po/POTFILES`, `make po`,
    česky v `cs.po`) ve fázi G spolu s editorem GTK.
  - Windows je pak použije podle msgid, viz Q-W4.
- **Sedm msgid popisů klíčů gschema** zůstává v
  `windows/parity-exclusions.txt` natrvalo jako u ostatních klíčů
  („gschema: a key's summary or description…“).

### W.11 Testy a fáze

- **W1 Core**:
  - `Toolbar.cs` a `ToolbarTests` (Go 1:1);
  - `SettingsStore` vlastnosti a testy;
  - `ToolbarFit` a testy.
- **W2 Pruhy podle `Present`** zatím bez nových položek. S výchozím
  rozvržením se nic viditelně nezmění (UI smoke testy beze změny, snímky
  před a po).
- **W3 Nové položky**: `toggleSidebar`, `contacts`, `markRead`, `junk`,
  `moveTo`, `undo`, `loadPictures`; nové glyfy v `IconGlyphs` (test
  `IconGlyphsTests`).
- **W4**: Další akce (M6), dočasné hledání (O5), přetok pruhů (»),
  režim zobrazení.
- **W5 Editor**: `ToolbarEditorController` a testy (přepnutí, posuny přes
  sekce, mezery, obnovení, změna klíče odjinud), stránka Předvoleb.
- **W6**: UI smoke `ToolbarSmokeTests` nad vzorovými daty a devmailem:
  - odškrtnout Archivovat → v `MessageCommandBar` chybí a Další akce ho mají;
  - Ctrl+↓ posune položku;
  - Obnovit výchozí;
  - Ikony a text ukáže popisky;
  - lišty Nástěnky a Kalendáře;
  - Ctrl+F bez hledání v rozvržení.

  Dál dokumentace (`windows/README.md` odchylky W.2 a W.9,
  `docs/windows-port.md` nová §11.x), `build.ps1 app`/`test`/`lint`
  a brána vlastníka B-W.

### W.12 Otevřené otázky pro vlastníka

- **Q-W1 Kde je editor**: vlastní stránka Předvoleb „Lišta“ za Obecné
  (návrh), nebo skupina v Obecné (plán §5)? GTK by mělo totéž.
- **Q-W2 Pravý klik**: má pravý klik na pruh nabízet „Přizpůsobit lištu…“,
  nebo je vstup jen v Předvolbách (T11 je pro macOS)?
- **Q-W3 Přetok**: co se nevejde do pruhu Nástěnky, Kalendáře, hlavičky
  seznamu nebo řádku postranního panelu, ukáže tlačítko » na konci pruhu
  (návrh), nebo položka jen zmizí?
- **Q-W4 Pořadí G a W**: editor GTK (fáze G) první s texty v Go referenci
  a Windows po něm (pravidlo 6), nebo Windows dřív s dočasnými texty jen
  anglicky (`// Windows-only string`)?
- **Q-W5 Výchozí vzhled**: `Default(mail)` má Nová zpráva, Kontakty, Filtr
  a přepínač postranního panelu v sekci seznamu. Windows má dnes zvýrazněné
  Nová zpráva v řádku postranního panelu, Kontakty jen v „…“, Filtr jako
  řádek a přepínač panelu jen v úzkém rozložení. Klíče jsou společné, takže
  výchozí rozvržení je jedno pro všechny platformy. Možnosti:
  - Windows kreslí `Default` doslova: Nová zpráva se přesune do hlavičky
    seznamu a přibudou tlačítka Kontakty a postranní panel;
  - `newMessage` dostane na Windows domov v řádku postranního panelu
    (zvýrazněné tlačítko), což ale mění sdílený model;
  - jiné řešení vlastníka.
- **Q-W6 Detail Nástěnky**: sekce `|detail` platí pro řádek akcí detailu
  ve stylu Seznam vedle seznamu i ve vysouvacím panelu (návrh). Ve stylech
  Sloupce a Dnes je řádek panelu pevný jako na macOS. Souhlas?
- **Q-W7 Filtr**: odškrtnutý `filter` na Windows schová řádek
  Vše / Nepřečtené / Označené (návrh), nebo `filter` na Windows v editoru
  není (jako „Filtr jen pro macOS“ v §6)?
- **Q-W8 Hledání v titulkové liště**: na pozici `search` a `calendarSearch`
  v rozvržení na Windows nezáleží, počítá se jen přítomnost. Je to
  přijatelná odchylka?
- **Q-W9 Ikony a text**: popisek pod ikonou (jako `AppBarButton`, vyšší
  řádky), nebo vedle ní (užší pruhy se dřív přeplní)?
- **Q-W10 Obnovit výchozí**: s otázkou, nebo bez?

## 16. As built (tisk na macOS, 2026-11-04)

- Go reference `ui/internal/printing` (`printing.go`, testy; v
  `po/POTFILES`): `Header`/`HeaderRows` (Předmět, Od, Komu, Kopie, Skrytá
  kopie, Datum, Přílohy; prázdné vynechané, předmět `Strong`), `Escape`,
  `Body` (sanitizované HTML doslova, jinak text v `<pre>`), `Message`, `CSS`,
  `Fragment` s `Single`/`Conversation` (čára)/`Pages` (zalomení, P5),
  `EventSection`, `DaySection`, `MaxMessages` 50, texty `MenuTitle`
  („_Print…“) a `ItemLabel` („Print“). Nové msgid: „_Print…“, „Print“
  a bez kontextu „Date“ a „Attachments“ (dosud jen s kontextem), česky
  Tisk…, Tisk, Datum, Přílohy; všechny čtyři ve
  `windows/parity-exclusions.txt` do Windows portu.
- Swift port `MalachiCore/Printing/Printing.swift` + `PrintingTests` (Go 1:1).
- macOS `MalachiMail/Printing/PrintController.swift`: zprávy (hlavička
  z `message.get` přes cache, tělo z `message.body` s `trimQuoted: false`
  přímo přes `cache.client`, aby se v panelu čtení nepřepnula zobrazená
  varianta; obrázky podle výchozí politiky zprávy; Skrytá kopie jen
  u složek role sent/drafts/outbox), událost (`Cal.detailOf`: čas,
  opakování, místo, kalendář, lidé, účastníci s odpovědí, poznámky) a den
  (`Cal.agenda` na jeden den, prázdný den „No events in this period.“).
  `PrintJob` tiskne skrytý `MessageWebView` (stejná CSP, rule list, proxy
  a `malachi-cid:`) vložený do neviditelného okna mimo obrazovku, vždy
  přes `runModal(for:)`. Synchronní `run()` a pohled mimo okno tiskly
  nekonečně prázdných stránek (první běh vyrobil 678 MB, smazané). Se
  systémovým záhlavím a zápatím.
- Vstupy: akce `printMail:` (ne `print:`, na které odpovídá každý pohled).
  Soubor ▸ Tisk… ⌘P. Hlavní okno tiskne v Poště výběr (jedna zpráva,
  řádek konverzace jako konverzace od nejstarší, víc řádků po stránkách,
  nejvýš 50), v Kalendáři vybranou událost, jinak den kotvy (P8), na
  Nástěnce nic. Okno zprávy tiskne svou zprávu. Položky lišty `print`
  v Poště a Kalendáři (`Feature.print` teď zapnutá, paleta je nabízí).
- Vývojové: krok `print` a `MALACHI_PRINT_PDF=<cesta>` (uloží PDF bez
  panelu, nejvýš 20 stránek).
- Ověřeno: `go test ./internal/printing`, `swift build` bez varování,
  `swift test --filter PrintingTests`, `check-strings.py` (nic nového),
  `make po` (2031 přeložených, bez fuzzy). Izolovaný běh se vzorky
  Kalendáře vytiskl den do PDF: 1 stránka, záhlaví a zápatí, dvě události
  s časem a místem.
- Pád po třetím běhu (SIGABRT): AppKit volá `didRun` modální tiskové
  operace na vlastním vlákně a `host.contentView = nil` tam shodilo
  proces. `didRun` je teď `nonisolated` a úklid jde na hlavní vlákno.
  Sestaveno, opakovaný běh už ne (tři běhy na položku vyčerpané).
- Pád u vlastníka při tisku dvou zpráv (SIGTRAP, „continuation resumed
  more than once“): `MessageCache.fetch` volá čekající po každé polovině
  (message.get, tělo) i při pozdějších změnách. `PrintController.section`
  teď pokračuje jednou (`Once`), a to až když nic neběží
  (`!getting && !fetching`), aby hlavička měla Kopii a přílohy.
- Neověřeno (pro vlastníka): obě opravy pádů, tisk zprávy, konverzace, výběru a události
  (vzorová data bez démona zprávy nemají), panel tisku, obrázky `cid:`
  a `data:` v tisku, okno zprávy.

## 17. Reference for the GTK and Windows ports (2026-11-04)

Společná čistá Go reference, kterou použijí GTK (`ui/internal/window`)
i Windows port (`windows/`). Widgety ani kód Windows se tu neměnily, macOS
(Swift) beze změny.

### 17.1 Rozhodnutí vlastníka (2026-11-04), závazná pro oba porty

- **T12 Vlastní výchozí sada každého klienta**: varianta klienta
  v referenci (co klient nemá, co má navíc, vlastní výchozí sada na lištu),
  takže neupravená lišta na GTK i na Windows vypadá přesně jako dnes.
  Uložené rozvržení (klíče gschema `toolbar-*`) zůstává společné. Položky,
  které klient nemá, při zobrazení vynechá a při uložení z toho klienta
  **nesmí zmizet** (úprava klienta se slévá zpět do uložených tokenů).
- **T13 Editor v obou klientech**: skupina „Lišta“ v Předvolbách → Obecné
  s režimem zobrazení a tlačítkem „Přizpůsobit lištu…“, které otevře dialog
  s editorem (výběr lišty, položky po sekcích, přidat a odebrat, řazení
  přetažením a Ctrl+↑/↓ jen v mezích `CanInsert`, přidat mezeru a pružnou
  mezeru, Obnovit výchozí s potvrzením, které zapíše `[]`).
- **T14** Nic v hlavní nabídce (☰ / „…“, ustálené v PR #20).
- **T15** Pravý klik na prázdné místo lišty nebo pruhu nabídne
  „Přizpůsobit lištu…“.
- macOS zůstává, jak je: jeho varianta je nulová (`Variant{}` = dnešní
  katalog a výchozí sady), `Default`, `Normalize`, `Stored` a ostatní
  funkce bez varianty se chovají beze změny (stávající testy prošly beze
  změny, Swift port se nemění).

### 17.2 API (`ui/internal/toolbar`)

`variant.go`:

- `type Variant struct { Omit map[Bar][]ID; Extra map[Bar]map[ID][]Section; Default map[Bar]Layout }`
  — co klient nemá (nenabízí, nezobrazuje, ale zachová v uloženém), sekce
  navíc ke katalogu (domov zůstává katalogový) a vlastní výchozí sada (nil =
  `Default(bar)` bez vynechaných).
- `var GTK, Windows Variant` (neměnit), `func (v Variant) Omits(bar Bar, id ID) bool`.
- `LookupFor(v, bar, id) (Item, bool)`, `CatalogFor(v, bar) []Item`,
  `DefaultFor(v, bar) Layout`, `NormalizeFor(v, bar, stored []string) Layout`,
  `CanInsertFor(v, bar, id, s) bool`, `ImmovableFor(v, bar) []ID`,
  `AllowedFor(v, bar, f) []ID`, `PresentFor(v, bar, l, f) []Shown`,
  `MissingActionsFor(v, bar, l, f) []ID`,
  `StoredFor(v, bar, stored []string, l Layout) []string`.
- Funkce bez varianty (`Normalize`, `CanInsert`, `Immovable`, `Allowed`,
  `Present`, `MissingActions`) jsou teď obaly `…For(Variant{}, …)`;
  `Default`, `Stored`, `Lookup`, `Catalog`, `SearchIndex` beze změny.

`fit.go`:

- `type FitResult struct { Visible []bool; Overflow []ID }`,
  `Fit(v Variant, bar Bar, shown []Shown, widths []int, available int) FitResult`:
  `shown` je úsek `PresentFor`, který kreslí jeden pruh nebo hlavičková
  lišta, `widths[i]` šířka položky i s mezerou (pružná mezera své minimum).
  Dokud se nevejde, ustoupí nejnižší priorita, při shodě položka nejvíc
  vpravo; oddělovače, mezery, pevné položky, `PriorityUser` a položky
  nulové šířky nikdy. Skrytá položka z `Present` místo nezabírá a do
  `Overflow` nejde. `Overflow` (v pořadí rozvržení) dá klient do Dalších
  akcí nebo do »; `MissingActionsFor` zůstává zvlášť.

`text.go` (`Translator` jako `unified`/`printing`, v `po/POTFILES`):
`GroupTitle`, `CustomizeTitle` (tlačítko i položka kontextového menu, T15),
`DialogTitle`, `DisplayModeTitle`, `DisplayModes`, `DisplayModeLabel(tr, m)`,
`BarTitle(tr, bar)`, `SectionTitle(tr, bar, s)`, `ItemLabel(tr, bar, id)`
(i mezery), `UnavailableSubtitle`, `AddItemTitle`, `RemoveTitle`,
`RestoreTitle`, `RestoreQuestion`, `RestoreBody(tr, bar)`, `RestoreCancel`,
`StripMnemonic`.

`ui/internal/toolbaredit` (čistý kontroler editoru jedné lišty):
`New(v, bar, stored, f) *Editor`, `Bar`, `Reload(stored)` (po zápisu
i po změně odjinud), `SetFeatures(f)`, `Layout`, `Tokens()` (=
`StoredFor`, klient je hned zapíše do `toolbar.Key(bar)`), `Sections()
[]Section` (`Section{Section, Rows []Row}`, `Row{Index, ID, Fixed, Space,
Unavailable, CanMoveUp, CanMoveDown}`; oddělovače jsou nadpisy, ne
řádky), `Available(s) []ID`, `Add(id, s) bool` (konec sekce),
`AddSpace(kind, i) bool` (za řádek i, za nadpis, `-1` = začátek první
sekce; vždy za pevnými), `Remove(i) bool`, `MoveUp(i)`/`MoveDown(i) (int,
bool)` (Ctrl+↑/↓, i přes nadpis sekce na konec sousední, jen kde to
`CanInsertFor` dovolí, nikdy před pevnou položku), `CanDrop(from, to)`
a `Drop(from, to) (int, bool)` (`to` je index vložení v rozvržení bez
taženého řádku; odmítnutý drop nic nemění), `Reset() []string` (Obnovit
výchozí: výchozí sada klienta a `[]` k zápisu), `IsDefault()`,
`MaxTokens` (= `toolbar.MaxTokens − 16`, strop přidávání mezer, aby se
zapsané i se slitými položkami přečetlo celé). Řádek bez funkce
(`Unavailable`) GTK ukáže s podtitulem, Windows ho může vynechat (W.6);
posuny pracují s indexy rozvržení.

### 17.3 Varianty

| lišta | macOS (`Variant{}`) | GTK | Windows |
|---|---|---|---|
| `mail` | `mode flexibleSpace │list toggleSidebar newMessage contacts filter refresh flexibleSpace │message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search` | `mode newMessage flexibleSpace │list refresh flexibleSpace search │message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions` | `mode newMessage │list refresh filter │message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search` |
| `board-list` | `mode flexibleSpace │list toggleSidebar boardStyle contacts flexibleSpace boardAccount boardTriage │detail boardDone boardRemind boardArchive boardTrash flexibleSpace boardReply` | `mode boardStyle flexibleSpace │list boardTriage boardAccount │detail boardDone boardRemind boardArchive boardTrash flexibleSpace boardReply` | `mode boardStyle flexibleSpace │list boardAccount boardTriage │detail …` (jako GTK) |
| `board-flow` | `mode boardStyle contacts flexibleSpace boardAccount boardTriage` | `mode boardStyle flexibleSpace boardTriage boardAccount` | `mode boardStyle flexibleSpace boardAccount boardTriage` |
| `calendar` | `mode flexibleSpace │main toggleSidebar calendarToday calendarStep calendarView flexibleSpace calendarSearch contacts calendarNewEvent calendarAssistant` | `mode │main toggleSidebar calendarToday calendarStep calendarView flexibleSpace calendarSearch calendarNewEvent calendarAssistant` | jako GTK |

- Pořadí je pořadí na obrazovce zleva doprava (GTK balí položky za
  poslední pružnou mezerou do `[end]` obráceně, návrh GTK §4.2). Výchozí
  sady GTK jsou z `window.blp`, `board_page.blp` a `calendar_page.blp`,
  Windows z `MessageCommandBar`, `SidebarPane`, `MessageListPane`,
  `BoardPage.Bar`, `BoardDetailView` a `CalendarBar` (W.1, W.2). Proti
  návrhu GTK §2 chybí v Kalendáři úvodní pružná mezera za `mode`: na
  Windows by jeden pruh `CalendarBar` posunula všechno doprava, v GTK ji
  pravidlo poslední pružné mezery stejně ignoruje.
- **GTK** `Omit`: `mail` `filter` a `toggleSidebar`, `board-list`
  `toggleSidebar` (O-G4: `NavigationSplitView` sloupec neskryje); `Extra`:
  `mail` `search` smí i do sekce `list` (lišta hledání je nad seznamem,
  O-G2). Kontakty a T10 položky má (nové widgety v G2), jen ne ve výchozí
  sadě.
- **Windows** `Omit` nic (W.5: každá položka existuje nebo přibude;
  `mode`, `search`, `calendarSearch` a `filter` stojí na pevném místě
  a počítá se jen přítomnost); `Extra` `mail` `search` do `list` jako GTK,
  aby uložení z Windows nechalo hledání tam, kam ho dalo GTK. `filter` je
  ve výchozí sadě, aby řádek Vše / Nepřečtené / Označené zůstal (Q-W7).
- **Tisk** žádná varianta nevynechává: na GTK i Windows je to
  `FeaturePrint` vypnutý (položka zůstane schovaná na svém místě, editor
  ji nenabízí), dokud tam tisk nebude.

### 17.4 Pravidlo slévání při uložení (`StoredFor`)

1. Úprava klienta se normalizuje pro klienta (`NormalizeFor`).
2. Uložený seznam se přečte s katalogem a sekcemi navíc **všech** klientů
   (aby GTK hledání v sekci seznamu přežilo); prázdný seznam znamená
   `Default(bar)` macOS, takže první úprava na GTK zachová `filter`
   a `toggleSidebar` tam, kde je měla výchozí lišta.
3. Každá položka uloženého seznamu, kterou klient nemá, jde za nejbližší
   předchozí token uloženého seznamu (položku nebo oddělovač, i dříve
   slitou), který výsledek má **ve stejné sekci**; jinak na konec své
   sekce. Pořadí slitých položek zůstane.
4. Prázdný seznam (`[]`, výchozí a budoucí výchozí) se zapíše jen tehdy,
   když úprava je výchozí sada klienta a uložený seznam má z položek,
   které klient nemá, právě ty, které má `Default(bar)` (jejich pozice se
   pak vzdají). S nulovou variantou je `StoredFor` přesně `Stored`.
   Obnovit výchozí zapisuje `[]` vždy (`Editor.Reset`), tedy zahodí i cizí
   položky (rozhodnutí T13).
5. Neznámé tokeny (jiné verze) zahodí čtení jako dosud. Položky, které
   klient má, dostanou rozvržení klienta, i když je jiný klient předtím
   rozestavil jinak (Windows bez kontaktů v Poště je tak po uložení vezme
   i macOS).

Testy: `TestStoredForKeepsWhatTheClientLacks` (sousedé, přesunutý
a odebraný soused, pořadí, prázdný klíč), `TestStoredForRoundTripsBetweenClients`
(macOS → GTK → macOS → Windows), `TestStoredForOnEveryBar`,
`TestStoredForPathological` (samé oddělovače, prázdné a dlouhé tokeny,
tisíc kopií), opakované uložení nic nemění, GTK přečte zpět, co upravil.

### 17.5 Nové msgid

Česky v `po/cs.po` (2055 přeložených, bez fuzzy), všechny ve
`windows/parity-exclusions.txt` s důvodem „toolbar customization: Windows
port pending“: `Toolbar` (Lišta), `Customize Toolbar…` (Přizpůsobit
lištu…), `Customize Toolbar` (titulek dialogu), `Toolbar Display`
(Zobrazení lišty), `Icons Only`, `Icons and Text`, `Text Only` (Jen ikony,
Ikony a text, Jen text), `Board – List`, `Board – Columns and Today`
(Nástěnka – Seznam, Nástěnka – Sloupce a Dnes), `Cases` (Případy),
`Case Detail` (Detail případu), `Sidebar` (Postranní panel: sekce i
přepínač), `Space`, `Flexible Space` (Mezera, Pružná mezera),
`Mode Switch` (Přepínač režimu), `Filter` (Filtr; jen Windows, GTK ho
nemá), `Board Style` (Styl nástěnky), `Previous and Next` (Předchozí
a další), `Calendar View` (Pohled kalendáře), `Hidden while unavailable`
(Skryté, dokud není k dispozici), `Add Item…` (Přidat položku…),
`Restore Defaults` (Obnovit výchozí), `Restore the Default Toolbar?`
(Obnovit výchozí lištu?), `Your changes to the “%s” toolbar will be lost.`
(Vaše úpravy lišty „%s“ se ztratí.). Ostatní popisky jsou existující
msgid: `Reply`, `Reply All`, `Forward`, `Archive`, `Move to Trash`,
`Save to eWay-CRM…`, `Star`, `Assistant`, `More Actions`, `Search`,
`Mark as _Read` a `Load _Images` (bez mnemoniky), `Mark as Junk`,
`Move To`, `Print`, `Undo`, `New Message`, `Contacts`,
`Check for New Mail`, `All Accounts` (kontext `account filter`),
`✦ Triage`, `Done`, `Remind…`, `Delete`, `Today`, `Search Events`,
`New Event`, `Mail`, `Board`, `Calendar`, `Folders`, `Message List`,
`Message`, `Remove`, `Cancel`.

### 17.6 Ověřeno

- Z `ui/`: `gofmt`, `go vet ./internal/toolbar/... ./internal/toolbaredit/...`
  a `go test` obou balíčků zelené (stávající testy `toolbar_test.go` beze
  změny).
- `make po` v kontejneru `malachi-po:f42`, `msgfmt --check --statistics`
  nad `po/cs.po`: 2055 přeložených, žádné fuzzy; kontejner zastavený.
- Neověřeno: sestavení GTK, `make lint`, Windows `build.ps1 lint`
  (`StringsCheckTests` s novými exclusions) a Swift (beze změny, nic
  ke spuštění).

### 17.7 Otevřené body

- O-G2 a O-G4 rozhodla varianta GTK prozatím podle návrhu GTK (hledání
  i v sekci seznamu, bez přepínače postranního panelu v Poště a Seznamu);
  Q-W7 (Filtr na Windows) podle návrhu W (ve výchozí sadě, odebratelný).
  Pokud vlastník rozhodne jinak, mění se jen `GTK`/`Windows` a jejich
  testy.
- Uložení z Windows přesune hledání, které GTK dalo do sekce seznamu, jen
  pokud ho tam Windows nepovolí; teď ho povoluje (`Extra`). macOS ho při
  čtení ukáže na konci sekce zprávy a při svém uložení ho tam i zapíše.

## 18. As built (GTK port, 2026-11-04, necommitnuté)

- Hlavičkové lišty se skládají z `toolbar.PresentFor(toolbar.GTK, …)`
  (`ui/internal/window/toolbar_pack.go`, `toolbar_host.go`, `toolbar_mail.go`,
  `toolbar_board.go`, `toolbar_calendar.go`). Každá lišta se dělí
  u poslední pružné mezery na začátek a konec. Mezera dává 12 px okraje.
  Widgety mimo katalog se nehýbou. Sekce: Pošta `sidebar` → `folders_header`,
  `list` → `list_header`, `message` → `message_header`; Nástěnka Seznam
  `sidebar`+`list` → `board_header`, `detail` → `board_detail_header`
  (detail drží sekci Seznamu i ve Sloupcích a Dnes); Kalendář → `calendar_header`.
  ✦, Triage a eWay jsou zabalené vždy (T7).
- Editor `toolbar_editor.go`: `Adw.Dialog` nad `toolbaredit.Editor`
  (výběr lišty, skupina na sekci, přetažení a Ctrl+↑/↓ podle `CanDrop`,
  Odebrat, Přidat položku… s mezerami, Obnovit výchozí s `Adw.AlertDialog`,
  zápis hned, znovunačtení při změně klíče odjinud). Předvolby → Obecné →
  skupina Lišta z Go (`preferences_toolbar.go`, `preferences.blp` beze
  změny). Pravý klik na prázdné místo lišty (T15). Hlavní nabídka beze
  změny (T14), tisk nezapojený.
- Režim zobrazení přes `Adw.ButtonContent`. Přetok `toolbar_fit.go`
  (tick callback, `toolbar.Fit`, hystereze 12 px) a „Další akce“ s dynamickým
  prvním oddílem (`MissingActionsFor` + přetok). Nové položky
  v `ui/data/ui/toolbar_items.blp` bez textů. Klíče v `settings/store.go`.
  Do reference přibylo `toolbaredit.Editor.DropAtEnd` s testem. Vývojové
  kroky `MALACHI_GTK_START=toolbar-editor[:<bar>]` a `prefs-general`.
- Ověřeno v kontejneru: `gtk.sh make build`, celé `test-ui` (51 balíčků),
  všech 30 smoke testů (`TestToolbarGTKSmoke` nový), `make lint`, `make po`
  (jen odkazy). Nedotčené výchozí lišty změřené proti HEAD: všech sedm
  hlavičkových lišt má stejné pořadí, okraje i šířky.
- Neověřeno: na obrazovce nic (přetahování myší, nabídka pravého kliku,
  režimy zobrazení, přetok při změně šířky). Otevřené: Fit mění nedotčenou
  lištu jen při šířkách, kde by se dnes ořízla (Hvězdička první do „Další
  akce“); Nástěnka a Kalendář nemají „Další akce“, co se nevejde, zmizí
  (O-G7); detail Nástěnky nepřetéká.

## 19. As built (Windows port, 2026-11-04, nesestavené a necommitnuté)

- Napsané na Macu bez `dotnet`. Zkontrolované jen závorky, SPDX hlavičky,
  XAML jako XML a msgid proti šabloně.
- Core `windows/src/Malachi.Core/Toolbars/`: 1:1 porty `Toolbar.cs`,
  `ToolbarVariant.cs`, `Toolbar.Variant.cs`, `Toolbar.Fit.cs`,
  `Toolbar.Text.cs`, `ToolbarEditor.cs`, navíc `Toolbar.Runs.cs` (jedna
  sekce na pruh). `SettingsStore` přístupy ke klíčům (`SetToolbarLayout`
  zapisuje `StoredFor(Windows, …)`). `IconGlyphs` `edit-undo`. Testy
  `Toolbars/*Tests` (porty Go) a `ToolbarRunsTests`, `ToolbarSettingsTests`.
- App: `Toolbars/ToolbarStrip` (Panel řazený podle rozvržení, ovládací prvky
  se jen přesouvají, přetok `Toolbar.Fit` u pruhu zprávy, pravý klik T15),
  `ToolbarLabels` (režim zobrazení), editor `ToolbarEditorView` jako
  ContentDialog, Předvolby → Obecné → skupina Lišta, `MainWindow.Toolbars.cs`
  (pruhy, nové položky, Další akce, dočasné hledání O5). Sekce: Pošta
  `sidebar` → hlavička SidebarPane, `list` → hlavička MessageListPane,
  `message` → MessageCommandBar; Nástěnka → BoardPage bar a řádek akcí
  BoardDetailView; Kalendář → CalendarBar; `mode`, `search`,
  `calendarSearch`, `filter` jen přítomnost.
- `parity-exclusions.txt`: pryč všech 24 textů editoru a „Print“ (používá ho
  `Toolbar.ItemLabel`). Zůstává „_Print…“, „Date“, „Attachments“ (port tisku
  na `feat/print-windows`) a sedm popisů gschema. `README.md`: 6 odchylek.
- Pro Windows agenta: `build.ps1 app`/`test`/`lint`; testy `Toolbar*Tests`,
  `SettingsTests`, `IconGlyphsTests`, `StringsCheckTests`,
  `GschemaCoverageTests`, `SpdxHeaderTests`, `ThemeBrushTests`; UI smoke
  dotčených pruhů; nový `ToolbarSmokeTests` napsat (návrh W.11); vizuálně
  porovnat nedotčené pruhy s dneškem (T12).
- Otevřené: přetéká jen pruh zprávy (Q-W3); Ikony a text dává popisek vedle
  ikony (Q-W9); ✦ v přetoku se v „Další akce“ nenabízí; Zpět není zašedlé bez
  přesunu; sbalení postranního panelu Pošty se neukládá.

## 20. As built (tisk na Windows, 2026-11-04)

Port na větvi `feat/print-windows`, podrobnosti v `docs/windows-port.md` §11.19.

- **Core:**
  - `Malachi.Core/Printing/Print.cs` je port Go 1:1 (`Single` → `One`, `Event` → `PrintedEvent`);
  - `PrintComposer` drží macOS `PrintController` (Pošta a Kalendář) a má testy nad FakeDaemonem;
  - Ctrl+P (`ShortcutCommand.Print`).
- **Tisk:**
  1. Skrytý uzamčený `MessageWebView` vysází dokument do PDF v paměti (`PrintToPdfStreamAsync`).
  2. `Windows.Data.Pdf` z něj vykreslí stránky.
  3. Tiskové okno Windows s náhledem je dostane přes XAML `PrintDocument` a `PrintManagerInterop`.
- **Proč ne tiskové okno WebView2:** nese jméno procesu WebView2 a náhled v něm chybí. Ukázal to průchod vlastníka.
- **Vstupy:** Ctrl+P, Další akce a kontextové menu zprávy (rozhodnutí vlastníka 2026-11-04, doplněk P4 do příchodu položky lišty), nikdy hlavní nabídka „…“.
- **Čtyři msgid** jsou z exclusions pryč.

