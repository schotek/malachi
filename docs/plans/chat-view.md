# Zobrazení konverzace jako chat — návrh (wireframe ke schválení)

Stav: wireframe schválený vlastníkem 2026-10-17, větev `feat/chat-view`.
Nic z toho není implementované; §6 je plán pro všechny tři klienty
(macOS první, pak GTK a Windows).

## 1. Co se mění

Zobrazení konverzace (výběr sbaleného řádku vlákna) dostane druhé
rozložení. Dnešní časová osa (karty pod sebou, avatar a svislá čára
v levém okraji, nahoře úvodní zpráva, pod ní ostatní od nejnovější)
zůstává beze změny a je výchozí. Nové rozložení **Chat** připomíná
aplikace pro zprávy: cizí zprávy jako bubliny vlevo, moje vpravo,
chronologicky odshora dolů, otevřené na konci (nejnovější).

Přepínač je nahoře nad konverzací vedle dnešního tlačítka
*Sbalit vše / Rozbalit vše*. Je trvalý (klíč nastavení, platí pro
všechny konverzace a přežije restart), ne na jednu konverzaci.

Wireframy: `docs/plans/chat-view/w1-macos-chat.svg` (hlavní pohled),
`w2-states.svg` (Jira, koncept, sbalená bublina, úzký panel),
`w3-strip-and-ports.svg` (pruh s přepínačem v obou rozloženích; GTK
a Windows).

## 2. Rozhodnutí v návrhu (D1–D17)

| # | Rozhodnutí | Proč |
|---|---|---|
| D1 | **Strana**: vpravo je zpráva s `Item.Mine` nebo `Item.Sent` (odpověď z Odeslaných poslaná z jiné identity má `Mine` false), jinak vlevo. Koncepty jsou vždy vpravo. Události Jira (změna stavu, řešitele) a řádek „N starších zpráv se nezobrazuje“ jsou uprostřed jako systémové řádky bez bubliny. | `Mine` platí i pro vlastní zprávy ve složce (kopie sobě, Odeslané) a pro komentáře Jira; `Sent` jen pro odpovědi, které složka nemá. |
| D2 | **Pořadí** v chatu: chronologicky odshora (nejstarší nahoře, karta issue a řádek starších zpráv úplně nahoře), **otevřeno na konci**. Nová příchozí zpráva: když je pohled na konci, zůstane na konci; jinak drží místo čtení. | Jako IM. Dnešní rozložení drží své pořadí (úvodní karta, pak od nejnovější, otevřeno nahoře). |
| D3 | **Bublina je jen text** (rozhodnutí vlastníka 2026-10-17): odesílatel, datum, nepřečtená tečka, sponka s počtem příloh, u Jiry pilulky *Interní* a *via …*, a tělo jako **prostý text** (u HTML pošty text odvozený démonem, citovaná historie oříznutá jako `trimQuoted`). Bez tlačítek Odpovědět/Všem/Přeposlat, bez čipů příloh, bez rozbalení příjemců, bez šipky sbalení, bez HTML papíru, bez lišt (obrázky, hromadná pošta, pozvánka), bez „•••“. **Dvojklik kamkoli do bubliny** otevře zprávu v samostatném okně zprávy jako dnes (koncept v okně Nová zpráva), kde je vše. Odkazy v textu nejsou klikací. Časová osa zůstává přesně jako dnes. | Chat je přehled rozhovoru, ne čtečka; vše ostatní má okno zprávy. Chat tak nepotřebuje žádný webový pohled, limity živých pohledů ani měření výšky. |
| D4 | **Šířka bubliny**: nejvýš 80 % sloupce (sloupec je dnešní clamp 900 px bez okrajů), krátká zpráva se **smrští na šířku textu** (nejméně na šířku hlavičky: jméno + datum), zbytek je prázdná strana. Pod 420 px šířky sloupce je prázdná strana nejméně 24 px. | Bez webového pohledu má bublina vlastní přirozenou šířku, takže se chová jako v aplikacích pro zprávy. |
| D5 | **Avatar** stojí vně bubliny na její vnější straně (vlevo u cizích, vpravo u mých), zarovnaný k hornímu okraji; svislá čára časové osy v chatu není. Tečky událostí nejsou (řádek je uprostřed). | Avatar nese iniciály/monochrom podle dnešního nastavení; čára patří k časové ose. |
| D6 | **Barva**: cizí bublina = dnešní karta; moje bublina = jemný nádech barvy zvýraznění (asi 12 % přes pozadí karty) a okraj v barvě zvýraznění, stejně jako je dnes tónovaný můj avatar; koncept (vždy můj) má týž nádech. Text bubliny je nativní text v barvě motivu (světlý i tmavý). | Žádný HTML papír: v chatu není webový pohled (D3). |
| D7 | **Bez sbalení v chatu**: bubliny nemají šipku a *Sbalit vše / Rozbalit vše* je v chatu skryté (tlačítko se skrývá už dnes při méně než dvou kartách; přepínač zůstává). Dlouhý text se ukáže celý (citovaná historie je oříznutá, D13). Sbalení časové osy se nemění. | Textové bubliny jsou krátké samy od sebe; sbalení by přidalo stav, který chat nepotřebuje. |
| D8 | **Pruh nahoře** je pevný (neroluje s obsahem): vpravo *Sbalit vše / Rozbalit vše* a vedle něj přepínač *Zobrazit jako chat*. Pruh je vidět vždy, když je konverzace zobrazená; tlačítko sbalení se samo skryje při méně než dvou kartách jako dnes, přepínač zůstává. | Chat je otevřený na konci; přepínač v rolujícím obsahu by byl mimo obraz. Malá odchylka od dnešního stavu, kdy tlačítko roluje s obsahem. |
| D9 | **Přepínač** je přepínací tlačítko s ikonou bubliny a textem *Zobrazit jako chat* (msgid „Show as Chat“, zapnutý stav vizuálně zvýrazněný: macOS a Windows nádech barvy zvýraznění, GTK nativní zaškrtnutý stav `GtkToggleButton`); klíč gschema `conversation-chat-layout` (bool, výchozí false), zrcadlo v macOS `Settings` a Windows `SettingsStore`. Bez řádku v Předvolbách (přepínač je rozhraní). | Trvalé podle zadání; jeden msgid pro oba stavy, stav nese tlačítko. |
| D10 | **Přepnutí za běhu** zobrazí tutéž konverzaci v druhém rozložení bez nového dotazu na seznam členů: chat si bere `text` z těl, která kontroler už drží (jinak si je dožádá), časová osa si své karty a jejich stav (sbalení, odhalený citovaný text) drží, dokud je konverzace vybraná. Pohled se posune na začátek (časová osa) nebo konec (chat). | Dva pohledy nad jedním kontrolerem a jednou cache těl. |
| D11 | **Jira**: karta issue nahoře přes celou šířku (jako dnes, s pilulkou stavu a jejími přechody); popis issue je první bublina (strana podle `Mine`); komentáře podle autora; komentář přes integraci (`via`) nikdy vpravo; události uprostřed. Koncept komentáře vpravo jako koncept pošty. | Totéž pravidlo `Mine` jako pro poštu. |
| D12 | **Mimo rozsah**: blok konverzace na nástěnce (vlastní kód), přiložené zprávy, okno zprávy, oddělovače dnů („Dnes“, „Včera“) uprostřed chatu (kandidát na další krok), kontextové menu na bublině (kandidát: menu zprávy ze seznamu), klikací odkazy v bublině. | Držet první krok malý, aby šel ověřit na macOS. |
| D15 | **Text bubliny není k označení** (ani kopírování): dvojklik kamkoli do bubliny otevře okno zprávy, kde text označit jde. Jinak by dvojklik ve vybíratelném textu označil slovo a zároveň otevřel okno. Lze obrátit (text k označení, dvojklik jen na řádek hlavičky). | Jedno gesto bez kolize. |
| D16 | **Koncepty v chatu jsou na konci** (po všech zprávách, nejstarší uložený první): neodeslaná odpověď je to poslední v rozhovoru. V časové ose zůstávají hned pod úvodní kartou jako dnes. | Chronologie chatu. |
| D17 | **Načítání textu**: chat ukazuje `snippet` zprávy šedě jako zástupný text, dokud nepřijde `message.body`; těla žádá jen pro bubliny do dvou výšek pohledu od viditelné oblasti (dnešní pravidlo blízkého okna, od konce), zbytek až při rolování. Načtená těla drží kontroler v jedné cache pro obě rozložení (strop 64 MiB jako dnes). | Konverzace má až `api.MaxThreadMessages` členů; sanitizace všech naráz by démona zbytečně zatížila. |
| D13 | **Text dává démon**: chat ukazuje `text` z `message.body` s `trimQuoted` (varianta bez citované historie, i když ji uživatel v časové ose odhalil) (prostý text, nebo text odvozený z HTML, když zpráva textovou část nemá); klient HTML nikdy nepřevádí. Koncept ukazuje svůj text jako dnes karta konceptu. Volitelná optimalizace později: parametr, který `html` vynechá. | Pravidlo 1 (logika v backendu) a 2 (HTML neopouští sanitizér); žádná změna kontraktu. |
| D14 | **Koncept v chatu**: bublina vpravo s pilulkou *Koncept* (+ *Interní*), „Koncept uložen …“ a textem; bez tlačítek, dvojklik = Upravit (okno Nová zpráva / komentáře), Zahodit je v něm. Označení přečtení, `MarkRead` a akce v liště okna fungují jako dnes. | Jedno pravidlo dvojkliku pro všechny bubliny. |

## 3. Co zůstává stejné

Model `ui/internal/conversation` (položky, `Mine`, `Sent`, koncepty,
`MarkRead`, sbalení, citovaný text) a kontroler s cache těl se nemění.
Přibývá čistá vrstva chatu (strana, pořadí, text bubliny, šířka) a v
každém klientu druhý pohled nad týmž kontrolerem: nativní textové
bubliny bez webového pohledu. Časová osa zůstává beze změny včetně
webových pohledů v režimu `sized`.

## 4. Odpovědi vlastníka (2026-10-17)

1. D8: pevný nerolující pruh nahoře platí i pro dnešní rozložení. **Ano.**
2. D12: oddělovače dnů. **Až později**, ne v prvním kroku.
3. D4: krátké bubliny se smršťují na šířku textu. **Ano.**

## 5. Historie

- 2026-10-17 první návrh: bublina = dnešní karta i s HTML tělem.
- 2026-10-17 vlastník: bublina jen text, vše ostatní po dvojkliku v okně
  zprávy; časová osa beze změny (D3, D4, D6, D7, D10, D13, D14).
- 2026-10-17 vlastník schválil upravený wireframe (odpovědi v §4);
  doplněna D15–D17 a plán §6; §6 opravený podle revize proti kódu
  (28 nálezů: jeden odběratel změn kontroleru, pořadí commitů a lint
  Windows, šablona `po`, `Sent` vpravo, názvy polí, karta issue).

## 6. Plán implementace

Pořadí podle pravidla 6 a pokynu vlastníka: čistá logika v Go jako
reference, pak macOS (kontroluje se na něm), po jeho schválení GTK
a Windows. Každý úkol dělá subagent (model v tabulce), orchestrátor
hlídá rozhraní a brány. Commity po fázích, conventional commits,
nic se nepushuje bez pokynu. Plán prošel revizí proti kódu
(2026-10-17, 28 nálezů zapracovaných níže).

### 6.1 Architektura (platí pro všechny tři klienty)

- **Model se nemění; kontroler dostává jen fan-out.** `conversation.Model`,
  `Item.Mine`/`Sent`, koncepty, `MarkRead`, `Folds` a cache těl kontroleru
  zůstávají. Kontroler (Go `conversationController`, Swift
  `ConversationController`) má **jeden** slot `onChange`/`onLoaded`
  (Go `conversation_controller.go:129`, Swift `:80,84`; C# má multicast
  `Changed`/`EntryLoaded`). Proto vzniká **hostitel konverzace** (jeden
  na panel čtení), který slot vlastní a předává změny (`loading`,
  `opened`, `updated`, `cleared`; zvlášť `onLoaded`) **aktivnímu
  rozložení**; sám jednou ozbrojí `takeMarkRead` při `opened`, jako
  jediný volá `trim(near)` (s blízkou sadou aktivního rozložení), vlastní
  pruh, spinner, stavový popisek odkazu, **kartu issue** (jeden widget
  vkládaný na vrchol sloupce aktivního rozložení; `popupStatus` /
  `statusMenu` / `OpenStatusMenu`, `conversationSetIssueBusy`,
  `conversationApplyIssue` jdou přes hostitele) a směruje mezerník
  (`page(up)`). Rozložení = dnešní časová osa (beze změny chování) a nový
  chat, obě se stejným malým rozhraním: `open(model)`, `update(model)`,
  `clear()`, `loaded(id)`, `setShown(bool)`, `page(up)`, `near()`.
  Skryté rozložení nevolá `updateLive`/`trim` a pustí živé webové
  pohledy; po návratu si stav karet (sbalení, odhalení) drží (D10).
- **Nová čistá vrstva** `ui/internal/conversation/chat.go` (reference;
  porty `MalachiCore/Model/ConversationChat.swift` a
  `Malachi.Core/Model/Conversation.Chat.cs` + `ChatSide.cs`,
  `ChatRowKind.cs`, `ChatRow.cs`, jeden typ na soubor s hlavičkou podle
  `docs/windows-port.md` §3.1), bez widgetů:
  - `ChatSide` (`Left`, `Right`, `Center`) a `ChatRow{Kind
    Bubble|Draft|Event|Truncated, Side, Item, Draft}`;
    `ChatRows(m Model, drafts []DraftCard) []ChatRow`: řádek starších
    zpráv první (Center), pak položky modelu v jeho pořadí (nejstarší
    první; bublina `Mine || Sent` → Right, jinak Left; událost → Center;
    Jira `Via` už nuluje `Mine`), pak koncepty na konci (Right, nejstarší
    uložený první, D16). `ChatScrollTo(rows)` = poslední řádek (index do
    `ChatRows`, ne `Model.ScrollTo`).
  - `BubbleText(text string) string`: jen normalizace načteného
    `message.body` `text` přes `board.CleanBlock` s týmž stropem jako
    koncepty (`draftTextBytes` 64 KiB, nejvýš jeden prázdný řádek,
    řídicí znaky pryč, CR/NEL/U+2028 → „\n“), aby bublina zprávy
    a konceptu četly stejně. Stavy těla (`pending`, `tooBig`, `failed`,
    prázdné) a chyby volání ukazuje pohled dnešními texty `bodyText` /
    `LoadedMessageText` (Downloading…, This message is too large…,
    could not be read, (Empty message), `RPCErrorText`); dokud tělo není
    ani žádané, `snippet` šedě jako zástupný text (D17).
  - Texty jen dva, oba s `// TRANSLATORS:`: `ChatToggleLabel(tr)` =
    msgid „Show as Chat“, `ChatToggleTooltip(tr)` = „Others on the left,
    your messages on the right; double-click opens the message“.
    Přístupný název bubliny je „odesílatel, datum“ jako dnes (bez msgid);
    sponka má dnešní „Has attachments“.
  - Metriky a blízké okno **nejsou** v balíčku `conversation`
    (`convCompactHeader`, `liveCards` jsou neexportované v `window`):
    vedle `convCardWidth`/`convCompactDates` přibude
    `convChatBubbleMax(column)` (Swift `ConversationLayout.chatBubbleMax`,
    C# `ConversationLayout.ChatBubbleMax`): řádek chatu je
    [odstup avatara 40 na straně bubliny][bublina ≤ max][prázdná strana
    ≥ `chatGap`], kde `column` = vnitřní šířka clampu bez okrajů 16,
    `chatGap = max(24, 0.2 × column)` (spojité, bez skoku),
    `max = column − 40 − chatGap`; minimum bubliny = její hlavička.
    Blízké okno = dnešní `liveCards(frames, html=false…)` /
    `ConversationLayout.live` / `.Live` nad řádky chatu (dvě výšky
    pohledu), měřené od konce, protože chat je otevřený na konci.
- **Pruh nahoře (D8)** je ve všech třech klientech pevný pohled nad
  rolovacím oknem uvnitř stránky konverzace, zarovnaný na sloupec karet
  (vlastní clamp 900 a okraje 16): vpravo *Sbalit vše / Rozbalit vše*
  (jen v časové ose a jen při ≥ 2 kartách jako dnes; stav dál počítá
  časová osa a hlásí hostiteli) a přepínač *Zobrazit jako chat* (vždy,
  když je konverzace zobrazená). Přepínač je vázaný na klíč (GTK
  `settings.Bind`, macOS `Settings`, C# `bindings.Toggle`), rozložení
  se přepíná **z `OnChanged(key)`**, ne z kliknutí, takže funguje
  i změna zvenčí. Odebráním tlačítka ze sloupce se mění kotvy:
  macOS `headViews` 2→1, `currentAnchor` (`[foldAllBox, issueBox] +
  itemViews`), `removeAll`, `foldAllTop`/`topInset`; GTK `topAnchor`,
  `currentAnchor`, `removeAll`, `showFoldAll` (fokus); Windows
  `HeadViews` 2→1.
- **Klíč** `conversation-chat-layout` (gschema, bool, výchozí false;
  summary „Show conversations as a chat“, description „Others' messages
  on the left, yours on the right, oldest first; off shows the timeline“)
  v Go `settings.Store` (`TestMemoryDefaults`), macOS `Settings`
  (`Key.allCases.count` 39→40), Windows `SettingsKey` + `SettingsStore`
  (`Defaults` 43→44, `GschemaCoverageTests`); bez řádku v Předvolbách.
  Zmínky „43 klíčů“ v `docs/windows-port.md` (§ Settings) a `CLAUDE.md`
  se opraví.
- **Bublina** (každý klient nativně): hlavička = nepřečtená tečka (jen
  cizí nepřečtené), jméno odesílatele (`Item.Sender`, bez markup),
  sponka (`HasAttachments`; počet z `message.get`, když je načtené,
  jinak jen ikona), pilulky *Interní* / *via …* / *upraveno* u Jiry,
  datum vpravo (plné, v úzkém sloupci krátké s tooltipem jako dnes);
  tělo = **nevybíratelný** prostý text (D15; macOS `NSTextField`, ne
  `MessageBodyTextView`; GTK `Label` `selectable: false`,
  `use-markup: false`; Windows `TextBlock` `IsTextSelectionEnabled=False`),
  text z varianty bez citace: `loaded[id].body` pokud citace není
  odhalená, jinak `otherBody`, je-li načtená (Go `body`/`otherBody`,
  Swift `LoadedMessage.body`/`otherBody`, C# `Loaded[id].Body`/`OtherBody`).
  Moje: nádech barvy zvýraznění ~12 % přes pozadí karty
  (`controlAccentColor` / `@accent_bg_color` /
  `AccentFillColorDefaultBrush` s průhledností, světlý i tmavý motiv
  i vysoký kontrast), 1 px okraj v barvě zvýraznění; cizí: dnešní karta.
  Avatar vně na vnější straně, horní okraj, dnešní komponenta
  (`AvatarView` s `monochrome`/`accent`, `adw.Avatar` s třídou
  `conversation-own`, `Avatar` s `Monochrome`/`Accent`), zvýraznění
  podle `Mine || Sent`. Událost a řádek starších zpráv: **vystředěná
  varianta** dnešních řádků (ty jsou na plnou šířku s datem vpravo:
  přidat `halign center`, max šířka, text na střed), bez tečky.
  Koncept: bublina vpravo s pilulkou Koncept (+ Interní) a „Draft saved
  %s“, text konceptu, bez tlačítek. Dvojklik kamkoli do bubliny =
  `openMessage(summary)` (GTK `Window.openMessage` přes nové metody
  rozhraní hostitele `convHost`, aby šly testovat s falešným hostitelem;
  Swift přes `MessageActionDelegate.openMessage` z
  `windows.track(display:)` v `App/Integration+Conversation.swift`; C#
  `pane.Services.Router.OpenMessage` v `AfterRelease`), u konceptu
  `editThreadDraft`.
- **Chování**: otevření na konci (Windows `VerticalAnchorRatio="1"`);
  příchod nového člena při pohledu na konci zůstane na konci, jinak
  drží místo; `MarkRead` jako dnes při otevření (hostitel); mezerník /
  Shift+mezerník listují přes hostitele (macOS `ReadingPane.page(up:)`,
  Windows `ReaderHub.SetConversationPaging`, GTK
  `addConversationPaging`); kolečko roluje konverzaci; změna zoomu
  textu platí i pro bubliny; kontextové menu, odkazy a výběr textu
  nejsou (D12, D15).
- **Mimo**: blok konverzace na nástěnce, přiložené zprávy, okno zprávy,
  `docs/api.md` (beze změny), MCP most.

### 6.2 Úkoly

| ID | Úkol | Soubory | Testy / ověření | Agent | Po |
|---|---|---|---|---|---|
| T1 | Go reference chatu | `ui/internal/conversation/chat.go` (+ `po_test.go` `exercise()`), `po/POTFILES` (abecedně před `conversation.go`) | `chat_test.go`: pořadí a strany (pošta, `Sent` z jiné identity vpravo, Jira `Mine`/`via`, události, truncated, koncepty na konci, `ChatScrollTo` na koncept), `BubbleText` (CR, řídicí znaky, 1 MB, prázdný), `TestMsgidsInTemplate` (po T3a) | Opus | — |
| T2 | Klíč nastavení v Go a gschema + Windows konvence | `data/io.github.schotek.Malachi.gschema.xml`, `ui/internal/settings/store.go`, `windows/src/Malachi.Core/Settings/SettingsKey.cs` + `SettingsStore.cs` (řádek + accessor), `windows/tests/…/SettingsTests.cs` (44), `windows/parity-exclusions.txt` (summary a description „gschema: …“; „Show as Chat“ a tooltip „chat: Windows port pending“) | `store_test.go` (`TestMemoryDefaults`, set+notify); Windows testy čtením (bez `dotnet`), ověří ruční `windows.yml` | Sonnet | — |
| T3a | Šablona a čeština pro T1/T2 | `po/malachi.pot`, `po/cs.po` | skript reprodukující `xgettext`/`msgmerge` nad `HEAD` (jako u dřívějších prací na Macu); `TestMsgidsInTemplate` zelený | Sonnet | T1, T2 |
| M1 | MalachiCore: port `chat.go`, klíč, metrika | `MalachiCore/Model/ConversationChat.swift`, `ConversationLayout.swift` (`chatBubbleMax`), `Settings.swift` | `ConversationChatTests.swift` 1:1 s Go, `ConversationLayoutTests` (metrika), `SettingsTests` (40, výchozí, set+notify), `ConversationTests.portUsesTheTemplatesEntries` (+1 soubor se 2 voláními, 13 položek), `scripts/check-strings.py` | Opus | T3a |
| M2 | MalachiMail: hostitel a pruh | nový `MessageView/ConversationHostViewController.swift` (pruh `ConversationStripView` se *Sbalit vše* a `NSButton` toggle `bubble.left.and.bubble.right` ze `Settings`, spinner, stavový popisek, karta issue, fan-out `onChange`/`onLoaded`, `takeMarkRead`, `trim`, `page(up:)`), `ConversationViewController.swift` → rozložení časové osy (ven: foldAllBox, spinner, statusBox, issueBox vlastnictví; `headViews` 2→1, kotvy), `ReadingPaneViewController.swift` (jeden potomek konverzace), `App/Integration.swift`, `App/Integration+Conversation.swift` (`windows.track(display:)` na hostitele), volající `issueCard` (`:152,572`) | `swift build`; `swift test` celé (žádná regrese); ručně: časová osa jako dnes, pruh neroluje, sbalení s pohledem uprostřed drží kotvu | Opus | M1 |
| M3 | MalachiMail: chat | `MessageView/ConversationChatViewController.swift`, `ConversationBubbleView.swift`, `ConversationChatRow.swift`, vystředěné varianty `ConversationEventRow`/`ConversationTruncatedRow` | `swift build`, `swift test`; žádná vzorová data konverzací neexistují (`MALACHI_START` je nemá), rozložení ověří vlastník v B1 | Opus (týž jako M2) | M2 |
| M4 | macOS dokumentace a revize | `docs/macos-port.md` §5 a §7, `docs/security.md` §3.2 macOS (chat = nativní text bez webového pohledu, bez výběru a odkazů), `macos/README.md` (jen je-li odchylka), `CLAUDE.md` odstavec stavu; revize kódu M2+M3 (revizní agent) a oprava nálezů | kontrola řetězců, `swift test` | Sonnet (doc) + Opus (revize) | M3 |
| **C1** | **Commit 1** `feat: chat layout of the conversation view (Go reference, macOS)` = T1, T2, T3a, M1–M4 | — | `go test ./internal/conversation/... ./internal/settings/...` z `ui/`, `swift test`, `go vet` | orchestrátor | M4 |
| **B1** | **Brána vlastníka: macOS** nad kopií dat (`MALACHI_DATA_DIR` + vlastní `MALACHI_SOCKET`, debug binárka): přepínač a trvalost, chat pro poštu s odpověďmi v Odeslaných (i z jiné identity), Jira s událostmi a `via`, koncept na konci, dvojklik (zpráva i koncept), zástupný text a dočtení, stavy těla, označení přečtení, příchod nové zprávy na konci i uprostřed, úzký panel, tmavý režim, mezerník, monochromní avatary, Změnit stav z menu nad chatem, přepnutí tam a zpět bez ztráty sbalení | — | — | vlastník | C1 |
| G1 | GTK: hostitel, pruh a přepínač | `ui/data/ui/conversation_view.blp` (nový kořenový `Box` v `Overlay conversation_page`: `conversation_strip` s vlastním `Adw.Clamp` a `ToggleButton conversation_chat_toggle` + `Stack conversation_layouts` se stránkami časové osy a chatu), `ui/data/data.go` (`go:generate` seznam), `ui/data/icons/malachi-chat-symbolic.svg` (SPDX komentář) + `scripts/build.sh` (install řádek jako u asistenta), `window/conversation_host.go` (fan-out, `takeMarkRead`, `trim`, karta issue, mezerník), `conversation_view.go` (časová osa: `topAnchor`, `currentAnchor`, `removeAll`, `showFoldAll` hlásí hostiteli; `page.ConnectMap` → `updateLive` jen když zobrazená), `window.go:918` a `conversation_issue.go` (karta issue přes hostitele), `settings.Bind` + `OnChanged` | `go build` v kontejneru; `conversation_controller_test.go` beze změny; `TestConvDisplayOrder` beze změny | Opus | B1 |
| G2 | GTK: chat | `window/conversation_chat.go`, `conversation_chat_row.go`, `ui/data/ui/conversation_chat.blp`, `conversation_layout.go` (`convChatBubbleMax`), `conversation_rows.go` (vystředěná varianta události a truncated), CSS v `convCSS` (`.chat-bubble`, `.chat-bubble-mine`, `.chat-placeholder`), `convHost` rozhraní `openMessage`/`editThreadDraft`, `po/POTFILES` jen pokud nový soubor má texty (nemá mít) | `conversation_chat_test.go` s falešným `convWindow` (otevření na konci, těla jen pro blízké řádky, dvojklik → `openMessage`, koncept → `editThreadDraft`, přepnutí bez `thread.get`, `trim` jen z hostitele), `conversation_layout_test.go` (metrika spojitá), `MALACHI_GTK_SMOKE=1 TestConversationChatGTKSmoke` podle vzoru `board_conversation_gtk_smoke_test.go` (odpojený klient, `cv.ctrl.open(...)`, paměťový store nastavení, Blueprinty, obě rozložení, přepnutí) | Opus (týž jako G1) | G1 |
| G3 | GTK ověření | — | `gtk.sh make build`, `gtk.sh test-ui` (celé `ui/`, smoke), `make lint`, T3b `gtk.sh` `make po` (beze změny) | Sonnet | G2 |
| G4 | GTK dokumentace a revize | `CLAUDE.md`, `docs/architecture.md` §7 (rozhodnutí), `docs/security.md` §3.2 GTK; revize kódu G1+G2 a oprava | — | Sonnet + Opus | G3 |
| **C2** | **Commit 2** `feat(gtk): chat layout of the conversation view` | — | G3 zelené | orchestrátor | G4 |
| **B2** | **Brána vlastníka: GTK** v XQuartz (`make run-gtk-live`): totéž co B1 | — | — | vlastník | C2 |
| W1 | Windows Core | `Malachi.Core/Model/Conversation.Chat.cs`, `ChatSide.cs`, `ChatRowKind.cs`, `ChatRow.cs`, `ConversationLayout.cs` (`ChatBubbleMax`), `windows/parity-exclusions.txt` (pryč řádky „chat: Windows port pending“) | `Model/ConversationChatTests.cs` (port Go a Swift), `ConversationLayoutTests`, `StringsCheckTests`, `GschemaCoverageTests` | Opus | B1 |
| W2 | Windows App | `Reader/Conversation/ConversationView.xaml(.cs)` → hostitel (Grid s `RowDefinitions`: řádek 0 pruh s `MaxWidth 900`/`Padding 16` a `ToggleButton ConversationChatToggle` glyf E8F2 + `HyperlinkButton` Sbalit vše, řádek 1 obě rozložení; `StatusBox`/`Spinner` s `Grid.RowSpan`; `HeadViews` 2→1; `OpenStatusMenu` přes hostitele; přepis hlavičkového komentáře XAML), `ConversationTimelineView` (dnešní sloupec vyjmutý) nebo rovnocenné dělení, `ConversationChatView.xaml(.cs)` (`VerticalAnchorRatio="1"`), `ChatBubble.cs`, `ConversationStyles.xaml` (štětce nádechu světlý/tmavý/vysoký kontrast), vystředěné řádky, `ReaderHub.cs` (`SetConversationPaging` přes hostitele), dvojklik přes `AfterRelease` | `build.ps1 app`, `build.ps1 lint`; UI smoke `ConversationChatSmokeTests` nad `MALACHI_DEVMAIL` (odpověď sobě dá dvoučlennou konverzaci; zapnout `group-by-conversation` a `conversation-chat-layout` přes `AppSession.Start(preferences:)`) | Opus | W1, G2 |
| W3 | Windows dokumentace a ověření | `docs/windows-port.md` §11.7 a počet klíčů, `windows/README.md` (řádky tabulky: pruh, nevybíratelný text, `VerticalAnchorRatio`), `docs/security.md` §3.2 Windows, `CLAUDE.md` | ruční spuštění `windows.yml` (build, testy, lint, zipy); revize kódu a oprava | Sonnet + Opus | W2 |
| **C3** | **Commit 3** `feat(windows): chat layout of the conversation view` | — | `windows.yml` zelený | orchestrátor | W3 |
| **B3** | **Brána vlastníka: Windows**: totéž co B1 + Shift+F10 nic, tmavý motiv, 125 % měřítko | — | — | vlastník | C3 |

### 6.3 Pořadí a paralelní skupiny

1. **Skupina A (hned, paralelně):** T1 (Opus), T2 (Sonnet).
2. **Po A:** T3a (Sonnet), pak M1 (Opus).
3. **Po M1, sériově:** M2 → M3 (jeden agent, soubory `MessageView/`) →
   M4 (dokumentace Sonnet, souběžně revize Opus) → oprava nálezů
   → **C1** → **B1**.
4. **Po B1, paralelně:** GTK G1 → G2 → G3 → G4 → **C2** → **B2**
   (jeden agent pro G1+G2) a Windows W1 (jiný strom). W2 až po W1
   **a po G2** (GTK je mustr, macOS zdroj portu), pak W3 → **C3** → **B3**.

Nikdy dva agenti ve stejných souborech. Windows se tu nestaví; W1 a W2
píše agent podle okolního kódu, ověřuje ruční běh `windows.yml`
a vlastník. Commit 1 už nese klíč a exclusions pro Windows (T2), aby
`GschemaCoverageTests` a `StringsCheckTests` nepadaly do W1.

### 6.4 Ověření na tomto Macu

- Go: z `ui/` `go test ./internal/conversation/... ./internal/settings/...`
  (čisté balíčky, bez cgo); GTK `internal/window` testy a build jen
  v kontejneru (`scripts/gtk-container/gtk.sh`).
- Swift: `swift build --package-path macos` a `swift test --package-path
  macos -Xswiftc -plugin-path -Xswiftc
  /Library/Developer/CommandLineTools/usr/lib/swift/host/plugins/testing`,
  `macos/scripts/check-strings.py`.
- Rozložení macOS: debug binárka `macos/.build/debug/MalachiMail`
  s `MALACHI_DAEMON=none`, prázdným `MALACHI_DATA_DIR`, vlastním
  `MALACHI_SOCKET`, `defaults export MalachiMail` před a import po;
  nikdy `build/Malachi Mail.app`, nikdy `make macos`. Vzorová data
  konverzací nejsou; chat nad skutečnými daty ověří vlastník (B1).
- Démon beze změny: `go test ./pkg/api/...` jen pro jistotu.

### 6.5 Rizika a co hlídat

- **Hostitel** je jediný odběratel kontroleru, jediný, kdo ozbrojí
  `takeMarkRead` a volá `trim`; skryté rozložení pustí živé webové
  pohledy a nic neměří.
- **Pruh nad rolováním** mění kotvení časové osy ve všech třech
  klientech: regresní testy rozložení + ruční sbalení s pohledem
  uprostřed.
- **Dvojklik vs. gesto karty**: v chatu celá bublina, nevybíratelný
  text (D15).
- **Těla**: chat nesmí žádat 500 těl naráz (D17); zástupný text do
  načtení; stavy těla a chyby dnešními texty; varianta bez citace
  (`body`/`otherBody`).
- **`Mine` je podle `From`** (padělatelné): bublina vpravo ukazuje jen
  umístění, nic víc.
- **msgidy**: nové jen v `chat.go` (2, s `// TRANSLATORS:`) a gschema
  (2); `portUsesTheTemplatesEntries` a `TestMsgidsInTemplate`
  v týchž úkolech; `POTFILES` abecedně; Windows exclusions ve dvou
  krocích (T2 přidá, W1 odebere).
- **Karta issue** je jeden widget hostitele; volající z menu Změnit stav
  nesmí sahat na rozložení.
- **Cache pod kontrolerem** (`maxLoaded` 64, 32 MiB v okně) se nemění.
