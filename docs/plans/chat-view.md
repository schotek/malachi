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
| D8 | **Pruh nahoře** je pevný (neroluje s obsahem): vpravo *Sbalit vše / Rozbalit vše* a vedle něj přepínač *Zobrazit jako chat* / *Zobrazit jako vlákno* (D9). Pruh je vidět vždy, když je konverzace zobrazená; tlačítko sbalení se samo skryje při méně než dvou kartách jako dnes, přepínač zůstává. | Chat je otevřený na konci; přepínač v rolujícím obsahu by byl mimo obraz. Malá odchylka od dnešního stavu, kdy tlačítko roluje s obsahem. |
| D9 | **Přepínač** je přepínací tlačítko s ikonou bubliny, jehož **popisek a tooltip sledují stav** (nález vlastníka v B1): vypnutý *Zobrazit jako chat* (msgid „Show as Chat“, tooltip „Others on the left, your messages on the right; double-click opens the message“), zapnutý *Zobrazit jako vlákno* (msgid „Show as Thread“, tooltip „Back to the timeline: the messages as cards, newest first“); zapnutý stav navíc vizuálně zvýrazněný (macOS a Windows nádech barvy zvýraznění, GTK nativní zaškrtnutý stav `GtkToggleButton`); klíč gschema `conversation-chat-layout` (bool, výchozí false), zrcadlo v macOS `Settings` a Windows `SettingsStore`. Bez řádku v Předvolbách (přepínač je rozhraní). | Trvalé podle zadání; popisek říká, kam klik vede (B1: jeden popisek pro oba stavy nebyl čitelný). |
| D10 | **Přepnutí za běhu** zobrazí tutéž konverzaci v druhém rozložení bez nového dotazu na seznam členů: chat si bere `text` z těl, která kontroler už drží (jinak si je dožádá), časová osa si své karty a jejich stav (sbalení, odhalený citovaný text) drží, dokud je konverzace vybraná. Pohled se posune na začátek (časová osa) nebo konec (chat). | Dva pohledy nad jedním kontrolerem a jednou cache těl. |
| D11 | **Jira**: karta issue nahoře přes celou šířku (jako dnes, s pilulkou stavu a jejími přechody); popis issue je první bublina (strana podle `Mine`); komentáře podle autora; komentář přes integraci (`via`) nikdy vpravo; události uprostřed. Koncept komentáře vpravo jako koncept pošty. | Totéž pravidlo `Mine` jako pro poštu. |
| D12 | **Mimo rozsah**: blok konverzace na nástěnce (vlastní kód; **od 2026-10-20 má chat taky**, viz §9), přiložené zprávy, okno zprávy, oddělovače dnů („Dnes“, „Včera“) uprostřed chatu (kandidát na další krok), kontextové menu na bublině (kandidát: menu zprávy ze seznamu), klikací odkazy v bublině. | Držet první krok malý, aby šel ověřit na macOS. |
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
- 2026-10-20 vlastník: chat dostal i blok konverzace v detailu případu
  Nástěnky, ve všech třech klientech naráz (§9, mění D12).

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
  - Texty čtyři, každý s `// TRANSLATORS:`: `ChatToggleLabel(on, tr)` =
    msgid „Show as Chat“ (vypnuto) / „Show as Thread“ (zapnuto),
    `ChatToggleTooltip(on, tr)` = „Others on the left, your messages on
    the right; double-click opens the message“ / „Back to the timeline:
    the messages as cards, newest first“ (D9, po B1).
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
  časová osa a hlásí hostiteli) a přepínač *Zobrazit jako chat* /
  *Zobrazit jako vlákno* (vždy,
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
  sponka jako dobře viditelná pilulka (ne slabý glyf; po B1): hned,
  když má souhrn `HasAttachments`, bez počtu; jakmile odpoví
  `message.get`, sponka + počet čipů, které by ukázala karta časové osy
  (`ChipPlan`), a to i při `HasAttachments` false (díl s Content-ID je
  pro IMAP a parser inline, Graph inline přílohy nepočítá), bez čipů
  nic; bubliny s přílohami žádají v blízkém okně i `message.get` jako
  karta časové osy (pravidlo Swift-first
  `Conversation.bubbleAttachmentBadge`), pilulky *Interní* / *via …* / *upraveno* u Jiry,
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
- **msgidy**: nové jen v `chat.go` (4 po B1, s `// TRANSLATORS:`) a gschema
  (2); `portUsesTheTemplatesEntries` a `TestMsgidsInTemplate`
  v týchž úkolech; `POTFILES` abecedně; Windows exclusions ve dvou
  krocích (T2 přidá, W1 odebere).
- **Karta issue** je jeden widget hostitele; volající z menu Změnit stav
  nesmí sahat na rozložení.
- **Cache pod kontrolerem** (`maxLoaded` 64, 32 MiB v okně) se nemění.
- 2026-10-17 commit 1 (T1, T2, T3a, M1–M4): Go reference, klíč nastavení,
  šablona `po`, MalachiCore a macOS hostitel s chatem; revize kódu
  (2 opravy, 4 drobnosti) zapracovaná; jedna karta issue na rozložení
  místo jednoho widgetu hostitele; Swift-first pravidla `chatBody`,
  `bubbleBody`, `bubbleMonospace`, `chatAtEnd`, `chatEndTop`,
  `chatColumn`, `chatCompactDates`, `chatResize` (GTK a Windows je
  portují ze Swiftu). Čeká B1.
- 2026-10-17 nálezy B1 (macOS): pruh má víc místa nad a pod přepínačem
  (8 a 6 pt), popisek a tooltip přepínače sledují stav (*Show as Chat* /
  *Show as Thread*, 2 nové msgid, Windows exclusions), sponka v hlavičce
  bubliny je pilulka s počtem (`Conversation.bubbleAttachmentBadge`).
- 2026-10-17 GTK G1–G2 napsané bez překladu (hostitel, pruh, chat, testy,
  smoke test); odchylky v §7; čeká G3 v kontejneru, pak B2.
- 2026-10-17 Windows W1–W2 napsané bez `dotnet` (Core, hostitel, časová
  osa, chat, bubliny, testy, smoke test) a prošlé revizí čtením; odchylky
  v §8; čeká `build.ps1 app`/`test`/`lint`, UI smoke s `MALACHI_DEVMAIL`,
  ruční průchod a B3.

## 7. As built — GTK (G1–G2)

Written 2026-10-17 on the Mac without a build (`gofmt`, `gopls check`,
`go/types` with `FakeImportC`, code review); G3 builds and tests it in the
container. Deviations from the rows of §6.2:

- **No `conversation_chat.blp`.** The chat rows are built in Go
  (`conversation_chat_row.go`: `chatRow`, `chatBubble`, `chatCenterRow`);
  `conversation_view.blp` holds the strip, the `Stack` of the two layouts
  and the chat's scroller, clamp, column and issue slot.
- **`convChatBubbleMax` lives in `conversation_chat_layout.go`**, not
  `conversation_layout.go`, together with the ported Swift-first rules
  (`chatBody`, `bubbleBody`, `bubbleMonospace`, `convChatAtEnd`,
  `convChatCompactDates`, `convChatColumn`).
- **`conversation.ChatScrollTo` is not called**: the chat opens by scrolling
  to the document's end, which is equivalent (the last row, a draft or a
  message, is at the end).
- **The bubble cap** comes from the width of the clamp's child
  (`column.Width()`), falling back to `convChatColumn` of the scroller's
  width before the first allocation; each bubble sits in an `Adw.Clamp` in
  pixels next to an expanding spacer.
- **`convChatResize`** is kept as the tested port of Swift's `chatResize`
  but the view does not use it (`followSize` and `convChatScroll.toEnd`
  do the same).
- **The toggle is not flat**: its checked state is the native
  `GtkToggleButton` look (D9).
- **Host files**: `conversation_fanout.go` (the rules without widgets,
  tested with fake layouts) next to `conversation_host.go`; the issue card
  per layout is `convIssueSlot` (`conversation_issue.go`);
  `loadingMessageError` (`conversation_card.go`) is the one text of a failed
  body for cards and bubbles.
- **`po`**: `make po` in the container renumbers the
  `conversation_view.ui:77` reference in `po/malachi.pot` and nothing else.

Tests: `conversation_chat_test.go` and `conversation_chat_layout_test.go`
over a fake `convWindow`; `MALACHI_GTK_SMOKE=1 TestConversationChatGTKSmoke`
(real Blueprint, both layouts, toggle through the setting).

## 8. As built — Windows (W1–W2)

Written 2026-10-17 on the Mac without `dotnet` (from the macOS client, the
GTK port and the Go reference, then read through by a reviewer); nothing
under `windows/` has been compiled or run. `docs/windows-port.md` §11.7
("The chat layout") describes the code; deviations from the rows of §6.2
and from the other clients:

- **Core**: the files are `ChatSide.cs`, `ChatRowKind.cs`, `ChatRow.cs`,
  `Conversation.Chat.cs` (W1, port of `chat.go`) and the Swift-first
  `Conversation.ChatBody.cs`, `BubbleBody.cs`, `BubbleAttachments.cs`,
  `ConversationLayout.Chat.cs`, `ConversationLayout.ChatResize.cs`, not a
  change to `ConversationLayout.cs`. Swift's `bubbleBody` and `chatResize`
  are `BubbleBodyFor` and `ChatResizeFor`, the types they return being
  `BubbleBody` and `ConversationLayout.ChatResize`. The glyph `malachi-chat`
  (E8F2) is in `IconGlyphs`; the four msgids are out of
  `windows/parity-exclusions.txt`, the gschema lines stay.
- **Type names are the Swift ones**, not the `ChatBubble.cs` that row W2 names:
  `ConversationBubbleView`, `ConversationChatRow` (avatar and bubble) and
  `ConversationChatCenterRow`, because Core already has `ChatRow`;
  `ConversationGroupPeer` is Windows-only (automation peer of the layouts
  and bubbles). The centred row shows the lines of the change and then
  "sender · date", as macOS.
- **Host**: `ConversationView` stayed the class the reader holds and is the
  host (strip in row 0 with `FoldAllButton` and `ChatToggle`, both layouts in
  row 1); the old timeline is `ConversationTimelineView` (`HeadViews` 2→1).
  The layouts' method for the controller's news of a body is `EntryLoaded`,
  not `Loaded` (which would hide `FrameworkElement.Loaded`), and the rows'
  is `SetColumnWidth`. One spinner in the host, as GTK (macOS has one per
  layout). `ConversationIssueBox` drives the existing `IssueCardView`, one
  per layout, as in macOS and GTK. `ReaderHub` did not change.
- **Hidden timeline** also trims its pool of card views to the two idle ones
  (`IdleWebViews`); GTK only lets go of the web views of its cards.
- **Chat scrolling**: the `ScrollViewer` has `VerticalAnchorRatio="1"` on
  top of the explicit moves (open at the end via `ChatEndTop`, follow the end
  while pinned, keep the row read otherwise, `ChatResizeFor`); the pin ends
  on the wheel, a press or a touch. Unverified that the anchor and the moves
  do not fight.
- **Toggle**: a `ToggleButton` with the chat glyph and the name of the
  layout a click shows; checked, the accent tint through the button's own
  brush resources (light, dark; high contrast keeps the system look), where
  GTK uses the native checked state. Bubble tint likewise
  (`ConversationMineBubbleTintBrush`, high contrast without tint and with the
  system highlight as border).
- **Double tap** on a bubble runs through `AfterRelease` (the window opens
  when the second press is released), as the timeline's cards and the list.
- **Tests**: `Model/ConversationChatTests.cs` (port of `chat_test.go` and
  the Swift tests), `ConversationLayoutTests` (chat measures),
  `ConversationChatSmokeTests` with `ConversationChatFixture` (opt-in,
  `MALACHI_DEVMAIL`; `DevmailFixture.RepositoryRoot` and `AddAccountAsync`
  became `internal` for it).
- **Pending**: `build.ps1 app`/`test`/`lint`, the smoke test, the manual
  pass listed in `docs/windows-port.md` §11.7, then B3 (plus Shift+F10 on a
  bubble does nothing, dark theme, 125 % scale).
- 2026-10-19 B2 (GTK v XQuartz, `x11 dev`) prošla; G3 v kontejneru zelené
  (testy, smoke, `make po`, `make lint`); `main` (PR #6, CI Windows) sloučený
  do větve; sloučení do `main` s B3 na Windows nevyřízenou.

## 9. Nástěnka (2026-10-20)

Blok konverzace v detailu případu Nástěnky (D12 ho dosud vynechával) má
druhé rozložení, chat, ve všech třech klientech naráz. Necommitnuté,
vlastník zatím neověřil. Rozhodnutí vlastníka přes AskUserQuestion:

- **Všechny tři klienty najednou**, žádný sdílený základ v démonu nebo API.
- **Sdílený klíč** `conversation-chat-layout` s Poštou, žádný nový klíč:
  přepínač v Nástěnce a v Poště je jedna volba.
- **Dvojklik na bublinu** otevře zprávu v samostatném okně (koncept okno
  Nová zpráva) jako v Poště; bublina není k označení.
- **Text bubliny** je text, který případ už nese z `board.get`
  (`MessageCard.Text`: citovaná historie ořezaná jako `trimQuoted`, podpis
  pryč, nejvýš 8000 B). Žádné `message.body`, žádný webový pohled, žádný
  zástupný text a vzorová data se chovají stejně.
- **Bez rolování na konec**: detail se otevírá nahoře jako dosud; chat čte
  od nejstarší, nejnovější bublina je dole, kde je v kartách otevřená
  nejnovější karta.
- **Přepínač** je v řádku nadpisu bloku vpravo, nalevo od „Zobrazit v
  Poště“, vzhledem jako přepínač Pošty.

Rozhodnutí orchestrace: démon u Jiry označí popis a komentář, který napsal
vlastní uživatel účtu (ne přes bota), jako `mine` v `board.get` i
`board.queue`, jako už `issue.mine` (`backend/internal/store/board_cases.go`,
`boardItemMe`, `boardItemMine`; `BoardMember.Mine` a pravidla beze změny;
`docs/api.md`; test `TestBoardJiraMessagesMine`), aby jeho komentáře ležely
vpravo. Zobrazení jen: pravidla, `board.commit` a `board.trash` dál berou
„moje“ podle role složky. Avatary jako v Poště s iniciálami z nového
`MessageCard.Sender` (skutečný odesílatel i u vlastních zpráv, jejichž
hlavička dál říká „Vy“ = `From`); datum bubliny z nového `MessageCard.Date`
ve formátu chatu Pošty (plné, v úzkém sloupci kompaktní). Jeden nový msgid:
tooltip zapnutého stavu přepínače („Back to the cards: the messages as
cards, oldest first, the newest open“, česky „Zpět na karty: zprávy jako
karty, od nejstarší, nejnovější otevřená“); ostatní tři msgidy přepínače
sdílí s `ui/internal/conversation/chat.go`.

### Go reference

`ui/internal/board/conversation_chat.go`: `ChatBubble{Index, Right}`,
`ChatRows`, `ChatOpens`; `text.go` (`ChatToggleLabel`, `ChatToggleTooltip`),
`view.go` (`MessageCard.Sender`, `Date`); testy `conversation_chat_test.go`.

### Klienti

Společné: karty a chat jsou dvě stránky téhož bloku, přepíná je klíč;
skrytá stránka drží stav (sbalení, HTML), jen chat při zobrazení uvolní
všechny webové pohledy karet (brána `live`) a karty je po přepnutí zpět
znovu vyžádají. Řádky chatu se mění stejnou změnou `ConversationCards`
jako karty.

- **GTK** (`ui/internal/window/board_conversation_chat.go`, `boardChat`):
  přepínač vázaný `settings.Bind` a `OnChanged`, nehomogenní `gtk.Stack`
  se stránkami cards a chat, řádky znovu používají `chatRow` Pošty, dvojklik
  přes sdílené `Window.withMessage` (vytažené z `notify_open.go`), toasty
  `board.Later`, `ShowInMailGone`, `ShowInMailFailed`; brána `!chat.shown`
  v `board_conversation.go`; smoke `smokeBoardConversationChat` v
  `TestBoardGTKSmoke`, test `TestBoardChatMissingText`.
- **macOS** (`MalachiCore/Board/BoardConversationChat.swift`, `BoardView`,
  `BoardText`; `MalachiMail/Board/BoardConversationBlock.swift`: přepínač,
  `chatColumn`, brána `live`, pozorovatel nastavení, dvojklik přes
  `env.cache.lookUpOutcome` → `delegate?.openMessage`): nový sdílený
  `MessageView/ConversationLayoutToggle.swift` vytažený z
  `ConversationStripView`, `ConversationBubbleView.showPlain(sender:date:)`,
  vývojový krok `board-chat`, testy `BoardConversationChatTests`.
- **Windows** (`Malachi.Core/Boards/BoardConversationChat.cs`,
  `BoardViewTypes.cs`, `BoardView.cs`, `BoardText.cs`;
  `Malachi.App/Boards/Conversation/BoardConversationBlock.cs`: přepínač,
  sloupec chatu, brána `Live`, `Settings.OnChange`, dvojklik přes
  `AfterRelease` → `Cache.LookUp` → `Router.OpenMessage`,
  `IBoardConversationHost.Toast`): `ConversationBubbleView.ShowPlain`,
  sdílený styl `ConversationChatToggleStyle` v `ConversationStyles.xaml` a
  štětce v novém `ConversationChatToggle.xaml` (používá i
  `ConversationView.xaml`), vzorová data teď blok dostanou
  (`MainWindow.BoardReply.cs`), testy `BoardConversationChatTests`, UI smoke
  `BoardSmokeTests.TheConversationToggleShowsTheChatAndTheCardsAgain`.

### Ověřeno a co čeká

- **Go/GTK** v kontejneru Apple `container`: `gtk.sh make build`,
  `go test ./internal/window/... ./internal/board/...`, smoke s
  `MALACHI_GTK_CARDS_SMOKE=1` i bez (s ním v kontejneru
  `WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1`), `make po` (přečíslování),
  `make lint`. V XQuartz neviděno.
- **macOS**: `swift build` bez varování, filtrované `swift test` (568),
  celá sada 1849 s jedním nesouvisejícím selháním
  (`AssistantControllerTests.closeDropsLateRepliesAndObservers`, časový
  test podprocesu shellu, padá i samotný, kód se nezměnil),
  `check-strings.py`, jedno měření debug binárkou nad vzorky (řádky chatu na
  správných stranách, 0 živých webových pohledů, karty zpět se stejnými
  rámci).
- **Windows**: napsané bez `dotnet`, nepřeloženo; čeká `build.ps1 app`,
  `test`, `lint`, UI smoke a ruční průchod (`docs/windows-port.md` §11.8).
- **Vlastník**: průchod ve všech třech (GTK v XQuartz `make run-gtk-live`,
  macOS nad kopií dat, Windows).

## 10. Řádek rychlé odpovědi (návrh 2026-10-20, ke schválení)

Zadání vlastníka: dole v chatovém rozložení klasický chatovací řádek
s rychlou odpovědí — volba jen Odpovědět / Odpovědět všem, odeslání,
a možnost otevřít velké okno (přeposlání, přílohy). Wireframe
`docs/plans/chat-view/w4-quick-reply.svg`.

| # | Rozhodnutí | Proč |
|---|---|---|
| Q1 | **Kde**: jen v chatovém rozložení Pošty, pevný řádek pod rolovací oblastí chatu, zarovnaný na sloupec (clamp 900, okraje 16), oddělený čarou. Časová osa ho nemá; chat na Nástěnce ne (ta má vlastní inline editor odpovědi); okno zprávy ne. | Zadání; Nástěnka už odpověď řeší jinak. |
| Q2 | **Obsah** zleva: tlačítko režimu s nabídkou (ikona + text *Odpovědět* / *Odpovědět všem*, položka *Přeposlat…* otevře rovnou velké okno), textové pole na 1–5 řádků (roste, pak roluje) se zástupným textem *Napsat odpověď…* (u Jiry *Napsat komentář…*), tlačítko *Otevřít v okně* (ikona šipky ven) a *Odeslat* (ikona letadla, zvýrazněné, vypnuté při prázdném poli). | Vzor IM aplikací, nic navíc. |
| Q3 | **Cíl** odpovědi: nejnovější člen konverzace, který není můj (`Item.Mine`/`Sent`); když takový není, nejnovější člen. *Odpovědět všem* a *Přeposlat…* podle schopností účtu jako tlačítka karty (`conversation.CardActions`: jen capabilities; souhrn zprávy `Cc` nenese, takže pravidlo podle příjemců není možné — revize 2026-10-20); Jira: jen *Komentovat* (veřejný), interní viditelnost až ve velkém okně. Cíl jmenuje tooltip tlačítka režimu („Reply to: %s“, jméno v 1. pádu, bez jména, když `Item.Sender` chybí), zástupný text pole zůstává *Napsat odpověď…*. | Rychlá odpověď míří na poslední cizí zprávu, jako v chatu. |
| Q4 | **Klávesy**: Enter = nový řádek, ⌘/Ctrl+Enter = Odeslat (jako okno Nová zpráva), Escape vrátí klávesnici seznamu. Mezerník v poli píše (listování seznamu platí jen s klávesnicí v seznamu). | Shoda s oknem Nová zpráva; nic se neodešle omylem. |
| Q5 | **Odeslání** = dnešní cesta démona: `draft.create` (`reply`/`replyAll` na cíl, u Jiry komentář) → `draft.save` s tělem z napsaného textu → `message.send`; toast „Message queued for sending“; pole se vyprázdní. Citovaný originál jde s odpovědí tak, jak ho `draft.create` připraví (výchozí „Zahrnout do odpovědi“ okna Nová zpráva), bez zaškrtávátka v řádku. | Jedna cesta odesílání; příjemci mimo chat čekají kontext. |
| Q6 | **Tělo** z prostého textu dělá čistá funkce reference (`QuickReplyHTML`: odstavce `<p>`, řádky `<br>`, text escapovaný; vlastní text uživatele, ne pošta) s testy; porty 1:1. Žádný Markdown (`draft.markdown` ne). | Rychlá odpověď je prostý text; žádné překvapivé formátování. |
| Q7 | **Odeslaná bublina**: v prvním kroku bez dočasné bubliny „Odesílá se…“; bublina přibude, až kopie dorazí do Odeslaných (dnešní pravidlo `thread.get withSent`). Dočasná bublina je kandidát na další krok. | Démon nedává klientu Message-ID odeslané zprávy; párování by bylo hádání. |
| Q8 | **Otevřít v okně** otevře okno Nová zpráva dnešní cestou odpovědi (`draft.create` v zvoleném režimu, citace v panelu) s napsaným textem předvyplněným v editoru jako vlastní text (`Params.OwnHTML`), označeným jako změna (autosave a otázka při zavření platí); řádek se vyprázdní, až když se okno skutečně otevře. Z řádku se nikdy neukládá koncept mimo Odeslat. *Přeposlat…* z nabídky totéž s konceptem přeposlání cíle a textem jako úvodem. | Vše, co řádek neumí, má okno; žádný osiřelý koncept. |
| Q9 | **Rozepsaný text** drží klient v paměti na konverzaci (vlákno) až do odeslání, otevření v okně nebo vyprázdnění; přepnutí konverzace nebo rozložení ho nechá; ukončení aplikace ho ztratí (není to koncept démona). | Koncept démona by se v chatu ukázal jako bublina konceptu vedle řádku (zdvojení). |
| Q10 | **Dostupnost**: řádek je skrytý, když účet neumí odpovědět (capability), když konverzace nemá cíl, při výběru více řádků, při hledání ve všech účtech beze změny (konverzace se tam neukazuje). Při odesílání je řádek dočasně vypnutý; chyba `draft.create`/`save`/`send` → toast, text zůstane. | Nic se neztratí potichu. |
| Q11 | **Úzký panel**: tlačítko režimu jen ikona, pole se zúží, tlačítka zůstanou. | |
| Q12 | **Texty**: šest nových msgid: „Write a reply…“ a „Write a comment…“ (zástupné texty), „Reply to: %s“ a „Reply all to: %s“ (tooltipy tlačítka režimu, jméno v 1. pádu), „Open in a Window“ (tooltip), „Forward…“ (položka nabídky, otevírá okno); *Reply*, *Reply All*, *Comment*, *_Send* (bez mnemoniky mimo GTK), „Send (Ctrl+Enter)“, „Message queued for sending“ a `jira.CommentQueued` existují. | Nic z pošty se neskloňuje ani neskládá do vět. |
| Q13 | **Přístupnost**: pole pojmenované zástupným textem, tlačítka tooltipem; řetězce z pošty (jméno cíle) jen jako prostý text. | Pravidlo 3. |
| Q14 | **Mimo rozsah**: přílohy a formátování v řádku, dočasná bublina (Q7), rychlá odpověď v časové ose a na Nástěnce, našeptávač příjemců. | Malý první krok. |
| Q15 | **Tlačítko ✦ Asistent** v řádku, totéž co ✦ v liště okna (otevře panel asistenta, nebo nabídku), jen když je Asistent zobrazený; rozhodnutí vlastníka 2026-10-20. Stojí vpravo od pole, vlevo od *Otevřít v okně* (pole zůstane široké). | Asistent je u odpovědi po ruce; jedno pravidlo s lištou (`assistant.ButtonOpensPanel`, `assistant.Shown`). |

Odpovědi vlastníka (2026-10-20, wireframe schválen): Q5 citovaný originál
ano, jako v okně; Q7 dočasná bublina až později; Q2 *Přeposlat…* v nabídce
zůstává.

Ověření Q5 v úložišti (2026-10-20, macOS nad ostrými daty vlastníka):
rychlá odpověď uložená do Odeslaných nese citovaný originál v HTML
(`<p>vlastní</p>` + `<p><br></p><div>atribuce</div><blockquote>…`) i v textové
části (řádky `>`), stejně jako odpovědi z okna. Čtečka ji ve složce Odeslané
schovává pod „•••“ (`trimQuoted`) jako u každé odpovědi; to není chyba
rychlé odpovědi.

## 11. Plán implementace řádku rychlé odpovědi (2026-10-20, po revizi proti kódu)

Větev `feat/chat-reply`. Pořadí jako u chatu: Go reference, macOS (brána
R-B1 u vlastníka), pak GTK (R-B2 v XQuartz) a Windows (CI, R-B3 až na
Windows). Každý úkol dělá subagent; nikdy dva ve stejných souborech.
Revize plánu (2026-10-20) opravila dva blokující body: text předvyplněný
do okna musí být označený jako změna (jinak se okno zavře bez otázky
a text je pryč) a řádek se vyprázdní až po skutečném otevření okna.

### 11.1 Architektura

- **Démon a API beze změny.** Odeslání jde dnešní cestou: `draft.create`
  (`reply`/`replyAll` na cíl s `attribution` jako okno; u Jiry `reply` =
  koncept komentáře; před ním `message.download`, když `replyNeedsDownload`,
  chyba stažení se ignoruje jako v okně) → `draft.save` s `created.Draft`,
  v němž je `HTMLBody` nahrazené tělem níže, `TextBody` = vlastní text
  (démon ho při HTML stejně odvodí), `Attachments` = `[{ID}]` z
  `created.Draft.Attachments` (jinak obrázky citace propadnou), `To`
  prázdné pole místo nil — zrcadlo `build()` v `compose/draft.go` →
  `message.send {accountId, draftId, version}`; selhání `message.send`
  po uložení → `draft.delete` (neznámý koncept není chyba) a toast
  `RPCErrorText("Sending")`, text v řádku zůstane. Odeslání z řádku nikdy
  nepadá na `compose.Prefill` jako okno; sdílí jen `composeSource`,
  `compose.Attribution`, `replyNeedsDownload` a test komentáře.
- **Čistá reference** `ui/internal/conversation/reply.go` (v `po/POTFILES`
  za `quoted.go`; `po_test.go` `exercise()`), porty
  `MalachiCore/Model/ConversationReply.swift` a
  `Malachi.Core/Model/Conversation.Reply.cs` (+ typy po souborech):
  - `QuickReplyTarget(m Model) (Item, bool)`: nejnovější `ItemMessage` s
    `!Mine && !Sent`, jinak nejnovější `ItemMessage`; false bez položek.
  - `QuickReplyMode` (`Reply`, `ReplyAll`, `Forward`) a
    `QuickReplyModes(a capabilities.Actions) []QuickReplyMode` podle
    `a.Reply`/`a.ReplyAll`/`a.Forward`; při `a.Comment` jen `Reply`
    s popiskem `jira.ReplyLabel`.
  - `QuickReplyHTML(text string) string`: vlastní text → odstavce oddělené
    prázdným řádkem jako `<p>…</p>`, řádky uvnitř `<br>`, escapování
    `html.EscapeString`, CRLF/CR → LF; prázdný vstup "". Nikdy neořezává:
    nad `api.MaxDraftBodyBytes` odmítne uložení démon, uživatel dostane
    toast a text zůstane.
  - `QuickReplyBlank(text) bool` = test prázdnoty jako `jira.SendProblem`
    (mezery, řídicí a formátovací znaky); Odeslat vypnuté, když platí.
  - `QuickReplyBody(mode api.ComposeMode, own string, d api.Draft) string`
    = `composebody.New(mode, d.Comment != nil, composebody.DraftBody(d))
    .HTML(QuickReplyHTML(own), true)`, kde nové čisté
    `composebody.DraftBody(d)` vrací `HTMLBody`, jinak escapovaný `TextBody`
    (totéž, co dnes dělá `compose.FromDraft`, které ho nově volá). Výsledek
    je bajt po bajtu to, co okno uloží se zaškrtnutým *Zahrnout*; u
    komentáře jen vlastní HTML. Swift přes `ComposeBodyLayout(ComposeParams
    (kind:bodyHTML:comment:))` + `escapeText`, C# `new ComposeBodyLayout(mode,
    comment, body)` + `Prefill.EscapeText`.
  - Texty (6 msgid, každý s `// TRANSLATORS:`; viz Q12). `reply.go` nesmí
    přes `tr.T` znovu použít existující msgid (posunul by počet odkazů
    v šabloně); *Reply*/*Reply All*/*Forward*/*_Send* bere pohled přímo
    z `i18n`/`L10n`.
- **Okno Nová zpráva dostane předvyplněný vlastní text** `Params.OwnHTML`
  (Swift `ComposeParams.ownHTML`, C# `ComposeParams.OwnHtml`; ne
  `EditorHTML`, to jméno už znamená vlastní text + citace): nastavuje ho
  volající po `FromDraft`/`Prefill` jako dnes `Attribution`; uplatní se na
  jednom místě na klienta — Go `compose/quoted.go` `bodyLayout`, Swift
  `ComposeBodyLayout.init(_ params:)`, C# `ComposeBodyLayout(ComposeParams)`
  — neprázdné nahradí `EmptyBody` v editoru u odpovědi, přeposlání
  i komentáře (citace zůstává v panelu); po načtení editoru se koncept
  označí jako změněný (`dc.markDirty()` po `wireActions` v `pane.go`, Swift
  a C# `markDirty` v `ComposeDraftController`), aby autosave a otázka při
  zavření platily jako po psaní (B1). Doc `Params.BodyHTML` doplní, že
  bezpečné HTML dává i `QuickReplyHTML`. Kurzor zůstane na začátku
  (`FocusStart`), přijatelné.
- **Kontroler řádku jako čistý balíček** `ui/internal/quickreply` (jako
  `boardreply`: injektovaný `Caller` a `Loop`, žádné gotk4, testy běží na
  Macu), porty `MalachiCore/Controllers/QuickReplyController.swift` a
  `Malachi.Core/Controllers/QuickReplyController.cs`: stav **na vlákno**
  (`text`, `mode`, `sending`; odpověď démona se páruje s vláknem, v němž
  odeslání začalo — při přepnutí konverzace během odesílání se vyprázdní
  a toastuje to vlákno, ne zobrazené; test), `Update(model, actions, account)`
  → `target`, `modes`, `visible` (chat zobrazený ∧ cíl ∧ `Reply`),
  `Send(attribution)` (download → create → save → send → vyprázdnit, toast;
  chyby výše), `OpenWindow()`/`Forward()` přes hostitele
  `OpenCompose(kind, id, ownHTML, opened func())` — text se vyprázdní až
  v `opened` (B2: `openComposeFrom` tiše končí při `composing[id]` nebo
  chybějícím souhrnu a `openCommentFor` jen toastuje). Rozhraní hostitele
  je malé a vlastní (volání, toast, download, otevření okna), ne rozšíření
  `convHost`; GTK glue `window/quick_reply.go` ho plní z `*Window`
  (`openComposeWith(parent, kind, id, ownHTML, opened)` jako nová funkce,
  `openComposeFrom` je její obal — šest volajících beze změny; větev
  komentáře předá `ownHTML` do `openCommentFor`). Swift přes
  `MessageActionDelegate` (jediný implementátor `MessageActionsController`),
  C# `MessageActionRouter` (+ `MessageActionRouterTests`). Hostitel
  konverzace (jediný odběratel kontroleru) volá `Update` při každé změně
  (`convFanout.onChange`, `changed(_:)`, handler `Changed`) s
  `ctrl.actions(cíl)` / `actions(for:)` / `Actions(s)`.
- **Pohled řádku** v každém klientu (jen chat Pošty; chat Nástěnky je
  `boardChat` a řádek nedostane): pevně pod rolovací oblastí chatu,
  zarovnaný na sloupec (clamp 900, okraje 16, čára nahoře): tlačítko režimu
  s nabídkou (ikona + text, v úzkém sloupci jen ikona; tooltip „Reply to:
  %s“), pole 1–5 řádků (`max-content-height`/`MaxHeight` 112 px jako panel
  asistenta: GTK `assistant_panel.blp` `propagate-natural-height` +
  přístupný popisek z Go, macOS znovu použije `AssistantInputView` s novým
  přepínačem `returnSends = false`, Windows `TextBox MaxHeight=112
  AcceptsReturn TextWrapping=Wrap`), tlačítko ✦ Asistent (Q15: ikona,
  tooltip a přístupný popisek tlačítka ✦ lišty, viditelné podle
  `assistant.Shown` se stejným odběrem jako lišta; klik = akce ✦ lišty
  hlavního okna, `assistant.ButtonOpensPanel`: panel s fokusem pole, jinak
  nabídka Asistent lišty pod tlačítkem; macOS `showAssistant(from:)`
  `MainWindowController` přes `onAssistant` hostitele, GTK a Windows
  stejně přes akci okna), *Otevřít v okně* (ikona, tooltip),
  *Odeslat* (ikona letadla, zvýrazněné, vypnuté při `QuickReplyBlank` nebo
  `sending`; přístupný popisek `_Send` bez podtržítka, tooltip „Send
  (Ctrl+Enter)“, macOS „(⌘↩)“). Klávesy: Enter nový řádek; ⌘/Ctrl+Enter
  Odeslat (GTK `EventControllerKey` ve fázi capture jako `assistant_panel.go`,
  macOS `sendMessage:` + `validateUserInterfaceItem` na kontroleru řádku
  v řetězci responderů, aby fungovala i položka Message ▸ Send; Windows
  `PreviewKeyDown` s `Handled`); Escape → klávesnice do seznamu, je-li
  vidět (macOS přes `textView(_:doCommandBy:)` jako `AssistantInputView`).
  Jednoklávesové zkratky pošty v poli vypíná dnešní brána. macOS: chat
  má `bottom == bar.top`, `bar.bottom == container.bottom`, skrytý řádek
  se složí na nulu (výška 0, ne jen `isHidden`); GTK skrytý potomek
  `conversation_body` a Windows `Collapsed` v řádku 2 `Root` se složí samy.
- `created.Skipped` (necitované obrázky) se potichu přijme (okno toastuje;
  u rychlé odpovědi bez panelu citace není co ukázat).

### 11.2 Úkoly

| ID | Úkol | Soubory | Testy / ověření | Agent | Po |
|---|---|---|---|---|---|
| R1 | Go reference + composebody + texty | `ui/internal/conversation/reply.go`, `reply_test.go`, `po_test.go`, `po/POTFILES`; `ui/internal/composebody` (`DraftBody`, `OwnHTML` v `Layout`/`New` nebo `WithOwn`), `layout_test.go`; `compose/prefill.go` (`Params.OwnHTML`, doc, `FromDraft` přes `DraftBody`), `compose/quoted.go` `bodyLayout`, `pane.go` (`markDirty` po načtení s `OwnHTML`); `po/malachi.pot`, `po/cs.po` skriptem jako T3a; `windows/parity-exclusions.txt` (+6 „quick reply: Windows port pending“) | na Macu: `go test ./internal/conversation/ ./internal/composebody/` (cíl, režimy, `QuickReplyHTML` patologické: CRLF, `<script>`, prázdné řádky, bidi, 1 MiB bez ořezu, `QuickReplyBlank`, `QuickReplyBody` bajt po bajtu proti `composebody`), `TestMsgidsInTemplate`; `compose` a `window` jen `gopls check` (cgo) | Opus | — |
| R2 | Čistý kontroler řádku + GTK glue | `ui/internal/quickreply/controller.go` + `_test.go` (falešný hostitel: pořadí volání, `attachments[].id`, tělo s citací, `send` selže → `draft.delete`, text na vlákno, `sending` na vlákno při přepnutí, `opened` callback, Jira bez citace, download před create), `window/quick_reply.go` (hostitel nad `*Window`: volání přes `w.client`, toast, download, `openComposeWith`), `compose_open.go` (`openComposeWith`, `openCommentFor` s `ownHTML`), `po/POTFILES` pro `window/quick_reply*.go`, pokud volá `i18n.T` | `go test ./internal/quickreply/` na Macu; `gopls check` | Opus | R1 |
| M1 | MalachiCore | `Model/ConversationReply.swift`, `Compose/ComposeParams` (`ownHTML`), `ComposeBodyLayout.init(_ params:)`, `ComposeDraftController` (`markDirty` po načtení), `Compose/Prefill.swift` (`draftBody`), `Controllers/QuickReplyController.swift` (port `quickreply`), `ActionsController` (`openCompose(kind, id, ownHTML:, opened:)` bez refaktoru `createDraft`), testy `ConversationReplyTests`, `QuickReplyControllerTests` (MailFixture `DraftCreate`/`DraftSave`/`MessageSend`/`DraftDelete` jako `JiraActionsTests.onDraftCreate`), `ComposeBodyLayoutTests` (+`ownHTML`), `ConversationTests.portUsesTheTemplatesEntries` (21 položek, `replyPort` 6 volání), `check-strings` | `swift test` | Opus | R1 |
| M2 | MalachiMail | `MessageView/ConversationQuickReplyBar.swift` (nad `AssistantInputView` s `returnSends`, `NSPopUpButton` režimu, tlačítka), `ConversationHostViewController` (řádek pod chatem, omezení výšky, `Update` při změně), `sendMessage:` responder, Escape, `MessageActionsController` (`openCompose(… ownHTML:opened:)`), `Integration+Conversation`; docs `macos-port.md` §5, `security.md` §3.2, `macos/README.md` (⌘↩ tooltip) | `swift build`, `swift test`, izolovaný start; revize kódu | Opus | M1 |
| C1 | Commit 1 `feat: quick reply bar of the chat layout (Go reference, macOS)` + `CLAUDE.md` odstavec | | | orchestrátor | M2 |
| **R-B1** | **Brána vlastníka macOS**: odpověď, odpověď všem, Jira komentář, Otevřít v okně s textem (a zavření okna se ptá), Přeposlat…, ⌘↩, Enter, Escape, prázdné pole, démon zastavený → toast a text zůstane, přepnutí konverzace během odesílání, úzký panel, bublina po doručení do Odeslaných | | | vlastník | C1 |
| G1 | GTK UI | `ui/data/ui/conversation_view.blp` (`conversation_reply_bar` v `conversation_body` pod `Stack`, `propagate-natural-height`, `max-content-height: 112`), `window/quick_reply_bar.go` (TextView, `MenuButton` režimu, tlačítka, `EventControllerKey` capture Ctrl+Enter a Escape, přístupné popisky), `conversation_host.go` (viditelnost, `Update`), `conversation_chat_gtk_smoke_test.go` (řádek přes injektovaný caller: psaní, Ctrl+Enter → `draft.create`/`save`/`send`, prázdné pole, Otevřít v okně) | kontejner (orchestrátor): `gtk.sh make build`, `test-ui`, smoke, `make po`, `make lint` | Opus | R-B1 |
| C2 | Commit 2 + **R-B2** v XQuartz (`gtk.sh x11 dev`) | | | orchestrátor, vlastník | G1 |
| W1 | Windows Core | `Model/Conversation.Reply.cs` + typy, `Compose/ComposeParams` (`OwnHtml`), `ComposeBodyLayout(ComposeParams)`, `ComposeDraftController` (`MarkDirty` po načtení), `Prefill` (`DraftBody`), `Controllers/QuickReplyController.cs`, `ActionsController` (`OpenCompose(kind, id, ownHtml, opened)`), `MessageActionRouter` + testy, FakeDaemon testy; `parity-exclusions.txt` bez šesti řádků | `windows.yml` | Opus | R-B1 |
| W2 | Windows App | `Reader/Conversation/ConversationQuickReplyBar.xaml(.cs)`, `ConversationView.xaml` řádek 2 (`Auto`, `Collapsed` mimo chat), `ComposePane` (`OwnHtml`), UI smoke rozšíření (`ConversationChatSmokeTests` přes devmail: napsat, Ctrl+Enter, bublina), docs `windows-port.md` §11.7, `windows/README.md` | `windows.yml` (build, testy, lint), revize kódu | Opus | W1, G1 |
| C3 | Commit 3, push, CI; **R-B3** na Windows u vlastníka | | | orchestrátor | W2 |

### 11.2.1 As built — GTK (G1)

Řádek je poslední potomek `conversation_body` (`conversation_view.blp`
`conversation_reply_bar`, skrytý mimo chat a při `Visible` vypnutém
kontroleru, takže časová osa vyplní stránku). `window/quick_reply_bar.go`:
nabídka režimu z Go nad akcemi `qr.mode` (radio, cíl `reply`/`reply-all`)
a `qr.forward`; u Jiry místo tlačítka popisek *Komentovat* bez nabídky
(jako macOS); ✦ je `MenuButton` vázaný týmž `Assistant.bindAssistantButton`
(předpona `win`) jako ✦ lišty, takže panel, nabídka i viditelnost platí
stejně. Klávesy v `EventControllerKey` ve fázi capture: Ctrl+Enter odešle
(spotřebuje se vždy, jen při povoleném Odeslat volá `Send`), Escape dá
klávesnici seznamu, jen když je namapovaný. Během odesílání pole drží
klávesnici a zůstává editovatelné (brána psaní tak dál vypíná
jednoklávesové zkratky), ale buffer zastaví `insert-text`
a `delete-range`; tlačítka jsou necitlivá. Ikona bez popisku podle
`convQuickReplyIconOnly(šířka stránky)`, přepočtená při změně šířky
sloupce chatu (`followSize` → `chatColumnChanged`). Hostitel volá
`Show`/`Update` při každé změně kontroleru
(`conversationController.quickReplyInput`, port Swiftu) a `SetChatShown`
v `applyLayout`. Smoke test vymění kontroler přes `useQuickReply` za jeden
nad falešným hostitelem a ověřuje psaní, Ctrl+Enter → `draft.create`/
`draft.save`/`message.send`, odmítnutou úpravu během odesílání, prázdný
text, Odpovědět všem, Otevřít v okně, Přeposlat…, Escape a ✦.
Ověřeno v kontejneru Fedora 42 (2026-10-20): `gtk.sh make build`, celá
sada `test-ui`, smoke test, `make po` (jen nové odkazy `#:`) a `make lint`;
jediná oprava byla v testu: `gtk.Button.Activate()` vyšle `clicked` až po
250 ms stisku, takže test klepe `Emit("clicked")` jako ostatní smoke testy.
Z nezávislé revize: tlačítka řádku mají `focus-on-click` vypnuté (klik na
Odeslat by při znecitlivění tlačítka pustil klávesnici a brána psaní by
zapnula jednoklávesové zkratky), nabídka režimu se staví znovu jen při
změně nabízených režimů (přestavba zavírá otevřený popover), přepočet
ikony bez popisku jde přes `glib.IdleAdd` (hlásí se z alokace), undo
bufferu je během odesílání vypnuté a popisek Komentovat má ikonu akce
Komentovat (`chat-message-new-symbolic`). Nechané: IME (preedit mimo
buffer, jako panel asistenta) a změna id vlákna pod zobrazenou konverzací
(text zůstane pod starým id).

### 11.2.2 As built — Windows (W1–W2)

Napsané na Macu bez `dotnet`, podle okolního kódu; nic se nepřeložilo ani
nespustilo (`docs/windows-port.md` §11.7 „The quick-reply bar“). **W1
(Core)**: `Model/Conversation.Reply.cs` (port `reply.go`, šest msgid pryč
z `parity-exclusions.txt`), `QuickReplyMode` + `QuickReplyModeExtensions`
(`ComposeMode`, `ComposeKind`), `ConversationLayout.QuickReplyIconOnly`,
`ComposeParams.OwnHtml` (uplatní jen `ComposeBodyLayout(ComposeParams)`),
`ComposeBodyLayout.DraftBody`, `ComposeDraftController.OwnTextPending`
(drží ho `DraftState`) a `EditorLoaded(bool ownText)` (flush pro každého
vlastníka, jak to Windows dělal vždy; `EditorReady` = `EditorLoaded(false)`),
`ConversationController.QuickReplyInput`, `QuickReplyController`
+ `IQuickReplyHost` + `QuickReplyState` (Swiftové `onChange` je událost
`Changed`, `failed` je `Failed(Exception)`, `KeptThreads` veřejné, volání
Odeslat běží v úloze bez čekání a výsledek vrací přes `Post`),
`ActionsController.OpenCompose(kind, id, parent, ownHtml, opened)`,
`ReplyAttribution`, `PrepareReply` (po zavření scope schránky `done`
nezavolá) a za nimi `MessageActionRouter` (`QuickReplyAttribution`,
`PrepareQuickReply`, `OpenCompose`); testy `ConversationReplyTests`,
`QuickReplyControllerTests`, `ComposeDraftOwnTextTests` a rozšířené
`ComposeBodyLayoutTests`, `PrefillTests`, `ActionsControllerTests`,
`JiraActionsTests`, `JiraComposeControllerTests`,
`ConversationControllerTests`, `MessageActionRouterTests`. **W2 (App)**:
`Reader/Conversation/ConversationQuickReplyBar.xaml(.cs)` v řádku 2 mřížky
`ConversationView` (třetí řádek `Auto`, `Collapsed` mimo chat a při
vypnutém `Visible`, takže nezabírá místo), čára nahoře, obsah ve sloupci
(900, okraje 16): `DropDownButton` režimu s `MenuFlyout` (režimy odpovědi
jako `RadioMenuFlyoutItem`, oddělovač, *Přeposlat…*; přestavba jen při
změně nabízených režimů), u Jiry popisek *Komentovat* s ikonou akce
(`QuickReplyCommentLabel`, `StackPanel` jako jeden textový prvek UIA),
`TextBox` (`AcceptsReturn`, `MaxHeight` 112), ✦ (viditelné podle
`AssistantController.Shown`, klik = nové `MainWindow.ShowAssistantFrom`:
s cílem V aplikaci panel s fokusem pole, jinak nabídka `AssistantMenu`
lišty nad tlačítkem), *Otevřít v okně* (glyf `window-new` E8A7, nový
v `IconGlyphs`, jinak by padal `EveryIconNameOfTheGtkUiHasAGlyph` kvůli
`conversation_view.blp`) a *Odeslat* (accent, tooltip „Send (Ctrl+Enter)“,
přístupný popisek `_Send` bez mnemoniky). Tooltip tlačítka režimu je
`QuickReplyTargetTooltip`; v úzkém panelu jen glyf (`QuickReplyIconOnly`
šířky řádku při každém `SizeChanged`). Tlačítka si klikem fokus neberou
(`AllowFocusOnInteraction`). Klávesy v `PreviewKeyDown` pole: Enter nový
řádek, Ctrl+Enter odešle (spotřebuje se vždy), Escape dá klávesnici
seznamu, jen když je na obrazovce (`MainWindow.MessageListOnScreen`, do
hostitele ho dává `ReaderHub` jako `ListOnScreen`), jinak ho nechá být.
Text hlásí pole přes `TextChanging` (synchronně, nikdy při vlastním
vykreslení) a znovu před každou akcí. Během odesílání jsou tlačítka
vypnutá, pole drží klávesnici a zůstává zapnuté (brána psaní
`KeyboardRouting.IsTextInput` dál vypíná jednoklávesové zkratky), ale
`BeforeTextChanging` každou úpravu mimo vlastní vykreslení zruší a Ctrl+Z
a Ctrl+Y spolkne; `IsReadOnly` by fokus také držel, ale schová kurzor
a vypadá jako vypnuté. Hostitel (`ConversationView`) vytvoří kontroler
na okno nad `QuickReplyHostAdapter` (volání přes `AppState.Client`,
`Post` přes `DispatcherQueue` okna, atribuce, stažení a okno přes
`MessageActionRouter`, `Sent` toast okna Nová zpráva nebo
`Jira.CommentQueued`, `Failed` toast `RpcErrorText.Text("Sending", …)`
a řádek logu jen s typem výjimky), při každé změně kontroleru konverzace
volá `Show` a `Update(QuickReplyInput())`, `ShowLayout` volá
`SetChatShown`. `ComposePane` nastaví `draft.OwnTextPending` před načtením
editoru (jen okno, nikdy inline pane Nástěnky) a `Ready` editoru volá
`draft.EditorLoaded(ownText)`. UI smoke
`ConversationChatSmokeTests.TheQuickReplyBarSendsAndOpensTheComposeWindow`
nad druhou konverzací `ConversationChatFixture` (první zpráva má
`Reply-To` na testovacího uživatele, aby devmail odpověď doručil):
řádek pod chatem a ne pod časovou osou, psaní zapne Odeslat, *Otevřít
v okně* otevře okno odpovědi a vyprázdní pole, zavření se ptá (Zahodit),
Ctrl+Enter (`Uia.PressKey` s modifikátorem) vyprázdní pole, odpověď
v Odeslaných a v INBOX serveru a její bublina v chatu. Odchylka v tabulce
`windows/README.md`. Čeká: `windows.yml` / `build.ps1 app`, `test`, `lint`,
smoke s `MALACHI_DEVMAIL`, ruční průchod a brána R-B3 (seznam v
`windows-port.md`); hádané chování: `TextChanging` u IME, kurzor po
zrušeném `BeforeTextChanging`, `ShowAt` nabídky Asistenta u ✦ řádku,
když je zároveň `Flyout` tlačítka lišty.

### 11.3 Rizika

- **Osiřelý koncept**: jediný `draft.save` je uvnitř Odeslat; po selhání
  `message.send` se maže (selhání mazání nechá viditelnou bublinu konceptu,
  ne tichou ztrátu). Otevřít v okně nic neukládá (`OwnHTML`) a okno se
  po předvyplnění ptá při zavření (B1).
- **Vypršení `message.send`** po skutečném zařazení: `draft.delete`
  neznámého konceptu projde, toast selhání, text zůstane; nové odeslání by
  zprávu zdvojilo. Vzácné; zapsat do dokumentace.
- **`portUsesTheTemplatesEntries`** 15 → 21 a přesný seznam volání.
- **Dvojí odeslání**: `sending` na vlákno; ⌘↩ při prázdném poli nic nedělá;
  `composing[id]` hlídá jen okno (řádek odesílá bez okna, souběh s otevřeným
  oknem téže zprávy je dovolený: dva různé koncepty).
- **Windows lint mezi C1 a C3**: šest exclusions v C1, pryč ve W1.
- **Message ▸ Send na macOS** v hlavním okně: zapne se jen s klávesnicí
  v poli řádku.

## 12. Řádek rychlé odpovědi i pod jednou zprávou (návrh 2026-10-20, ke schválení)

Zadání vlastníka: řádek rychlé odpovědi má být i u klasické (jedné)
zprávy, vždy dole v panelu čtení na stejném místě jako pod chatem.
Rozhodnutí vlastníka 2026-10-20: **jedna zpráva a chat** (časová osa ne),
**samostatné okno zprávy ne**, pořadí klientů stejné (macOS první, GTK,
Windows; brány S-B1/S-B2/S-B3). Wireframe
`docs/plans/chat-view/w5-quick-reply-message.svg`.

| # | Rozhodnutí | Proč |
|---|---|---|
| S1 | **Kde**: řádek je potomkem panelu čtení, ne stránky konverzace — pod celým zásobníkem stránek (GTK `message_stack` v `toast_overlay`, macOS `ReadingPaneViewController`, Windows `MessageView` v režimu `Pane`), takže stojí na témže místě pod jednou zprávou i pod chatem. Ukazuje se se stránkou jedné zprávy a s chatem konverzace; skrytý na prázdné stránce, u „Vybráno N zpráv“, v časové ose, na Nástěnce a v samostatném okně zprávy (Q1, w5 D). | Zadání; jedno místo, jeden pohled, jeden kontroler. |
| S2 | **Cíl u jedné zprávy** = zobrazená zpráva, i vlastní (ve složce Odeslané odpověď na vlastní zprávu, jako dnes `QuickReplyTarget` bez cizí zprávy). V Outboxu a Konceptech řádek není (zpráva nemá komu odpovídat; u konceptu je Upravit). Režimy podle schopností účtu jako dosud (`capabilities`), u Jira položky jen Komentovat; u události Jiry (řádek bez těla) řádek není. | Stejné pravidlo jako v chatu, jen s jedním členem. |
| S3 | **Stav na vlákno** zůstává: jedna zpráva má `ThreadID`, takže rozepsaný text sdílí se svou konverzací (seskupené zobrazení) a přežije přepnutí seznamu, hledání i přepnutí rozložení; `sending` se páruje s vláknem jako dosud. | Žádný nový stav, žádná ztráta textu při přepnutí seskupování. |
| S4 | **Vstup kontroleru**: `Update(model, actions, account)` dostane u jedné zprávy model s jedním členem (`conversation.Model` s jediným `ItemMessage`, `Mine` podle role složky jako v konverzaci), u chatu model konverzace jako dnes; `SetChatShown` se zobecní na `SetShown(bool)` (hostitel: stránka jedné zprávy ∨ chat konverzace). Reference `quickreply.Controller`, porty 1:1. | Nejmenší změna kontroleru; `QuickReplyTarget` platí beze změny. |
| S5 | **Odeslání, Otevřít v okně, Přeposlat…, ✦, klávesy, úzký panel**: beze změny (Q4–Q8, Q11, Q15). Escape dá klávesnici seznamu stejně. Citace z `draft.create` jako v okně. | Nic nového k rozhodování. |
| S6 | **Místo v rozložení**: pod tělem zprávy stojí dnes lišty (vzdálené obrázky, hromadná pošta, karta pozvánky, „•••“) uvnitř rolovací oblasti; řádek je **mimo ni**, pevně dole, čára nahoře, clamp a okraje sloupce jako v chatu. Rolovací oblast se o výšku řádku zkrátí; skrytý řádek nezabírá nic. | Jako w4: řádek je pevný, obsah roluje nad ním. |
| S7 | **Časová osa konverzace** řádek nemá (rozhodnutí vlastníka; karty mají vlastní tlačítka). Přepnutí chat ↔ časová osa řádek ukáže / skryje bez ztráty textu (S3). | |
| S8 | **Texty**: žádný nový msgid. Tooltipy, zástupné texty a popisky z §10 Q12. | |
| S9 | **Mimo rozsah**: samostatné okno zprávy (rozhodnutí vlastníka), přiložená zpráva `.eml`, Nástěnka, dočasná bublina (Q7), časová osa. | |

### 12.1 Plán implementace (S-R1 → S-M → S-G → S-W)

Architektura: kontroler i pohled řádku zůstávají, mění se **hostitel**:

- **Go/GTK**: řádek `conversation_reply_bar` odejde z `conversation_view.blp`
  do nového `quick_reply_bar.blp` (fragment, stejné id widgetů) a vloží se
  z Go pod `message_stack` — `toast_overlay.child` se stane svislý `Box`
  s `message_stack` (`vexpand`) a řádkem; `quickReplyBar` a `quickreply.
  Controller` vlastní `*Window` (`w.quickReply`), ne `conversationHost`.
  Hostitel konverzace mu jen hlásí změny (`updateQuickReply` → `w.quickReply.
  Update(...)`, `SetShown` podle chatu); `message_view.go` `showMessage`
  / `clearMessage` / stránka výběru hlásí jednu zprávu (`quickReplyInputFor
  (s api.MessageSummary)`: model s jedním členem přes `conversation.
  SingleModel(s, mine)` — nová čistá funkce v `ui/internal/conversation`
  s testem, porty Swift/C#), `SetShown(true)` na stránce „message“ bez
  Outboxu/Konceptů/události, jinak `false`. `followWidth` z šířky panelu
  čtení (`toast_overlay`), ne ze sloupce chatu. Smoke test: řádek pod
  jednou zprávou (viditelnost podle stránky a složky, cíl = zpráva,
  Ctrl+Enter → tři volání), přepnutí zpráva → konverzace → chat → časová
  osa bez ztráty textu.
- **macOS**: `ConversationQuickReplyBar` a `QuickReplyController` se
  přesunou z `ConversationHostViewController` do `ReadingPaneViewController`
  (řádek dole pod oběma potomky: `reader.view.bottom == bar.top`,
  `conversation.view.bottom == bar.top`, skrytý řádek výška 0);
  `show(_ row:)` krmí kontroler z `reader` (jedna zpráva:
  `Conversation.singleModel(summary, mine:)`), `controller.changed` z
  konverzace jako dnes; `setShown` podle stránky a rozložení.
  `QuickReplyHostAdapter` beze změny. Krok `MALACHI_START` `quick-reply`
  přidá variantu nad jednou zprávou.
- **Windows**: `ConversationQuickReplyBar` z řádku 2 `ConversationView`
  do `MessageView` (režim `Pane`; v režimu okna se nevytváří), pod
  `Grid` se stránkami; `ReaderController` hlásí jednu zprávu,
  `ConversationView.UpdateQuickReply` volá sdílený kontroler okna přes
  `MessageView`; UI smoke: řádek pod jednou zprávou přes devmail.
- **Démon a API beze změny**, žádný nový msgid.

| ID | Úkol | Soubory | Testy / ověření | Agent | Po |
|---|---|---|---|---|---|
| S-R1 | Go reference + kontroler (jen Go, změna rozsahu vlastníka 2026-10-20) | `ui/internal/conversation/reply.go` (`SingleModel`), `reply_test.go`; `ui/internal/quickreply/controller.go` (`SetShown` místo `SetChatShown`), `_test.go` (jedna zpráva, Outbox/Koncepty, přepnutí stránek); `window/conversation_host.go` jen oprava překladu (`SetShown`) | na Macu `go test ./internal/conversation/ ./internal/quickreply/`, `gopls check` | Opus | — |
| S-M | MalachiCore + MalachiMail | `MalachiCore/Model/ConversationReply.swift` (`singleModel`), `Controllers/QuickReplyController.swift` (`setShown`), `ReadingPaneViewController` (řádek + kontroler), `ConversationHostViewController` (bez řádku), `MessageViewController` (hlášení jedné zprávy), `Integration+Conversation`, DevStart `quick-reply`; testy `ConversationReplyTests`, `QuickReplyControllerTests` (+ jedna zpráva), `check-strings`; docs `macos-port.md`, `security.md` §3.2 | `swift build`, `swift test`, izolovaný start | Opus | S-R1 |
| S-C1 | Commit `feat: quick reply bar under a single message (Go reference, macOS)` + `CLAUDE.md`; **S-B1** vlastníka na macOS (jedna zpráva v Doručené, Odeslané, Outbox bez řádku, Jira položka, přepnutí seskupování bez ztráty textu, chat ↔ osa) | | | orchestrátor, vlastník | S-M |
| S-G | GTK hostitel | `ui/data/ui/quick_reply_bar.blp` (z `conversation_view.blp`, stejná id), `ui/data/data.go` (`go:generate` seznam), `window.blp` (`toast_overlay` → svislý Box s `message_stack` a řádkem), `window/quick_reply_bar.go` (vlastní ho `*Window`, šířka z panelu čtení), `window/quick_reply.go`, `conversation_host.go` (jen hlášení `Update`/`SetShown`), `message_view.go` (jedna zpráva přes `conversation.SingleModel`), `selection.go`; smoke testy (`TestConversationChatGTKSmoke` přes `w.quickReplyBar`, nový pod jednou zprávou) | kontejner `gtk.sh make build`, `test-ui`, smoke, `make po` (jen odkazy), `make lint`; **S-B2** vlastníka v XQuartz | Opus | S-B1 |
| S-W | Windows Core + App | `Conversation.Reply.cs` (`SingleModel`), `QuickReplyController.cs` (`SetShown`), `MessageView.xaml(.cs)` (řádek dole v režimu Pane), `ConversationView` (bez řádku, hlášení), `ReaderController`/`ReaderHub`, UI smoke; docs `windows-port.md` §11.7, `windows/README.md`, poznámka As built v §12 | předávka Windows agentovi vlastníka (`build.ps1 app`/`test`/`lint`, UI smoke; nikdy `windows.yml` — rozhodnutí vlastníka 2026-10-20), pak **S-B3** | Opus | S-B1 |

Rizika: GTK `toast_overlay.child` je dnes přímo `message_stack` a `Window`
si ho bere z builderu — přestavba musí zachovat id a `vexpand`; macOS
`ReadingPaneViewController` dosud jen skládá dva pohledy přes sebe,
přibude třetí s omezením výšky; Windows `MessageView` je sdílený s oknem
zprávy (režim `Window`) a řádek se smí vytvořit jen v `Pane`.

### 12.2 As built — Go reference (S-R1)

Vlastník rozsah zúžil (2026-10-20): S-R1 je jen Go reference a kontroler,
přesun řádku v GTK (S-G) přijde až po bráně S-B1 jako v §11. Démon, API,
texty ani `po/` se nemění.

- `conversation.SingleModel(s, mine)` (na konci `reply.go`, pod posledním
  msgid, aby se odkazy `#:` v šabloně neposunuly; proto bez importu
  `jira`, událost pozná podle `Issue.Item` přímo): model s jednou položkou
  `ItemMessage` pro řádek pod stránkou jedné zprávy, `Thread` = vlákno
  zprávy, `Sender` vyčištěný jako v konverzaci, `Mine` dodá volající
  (pravidlo `Item.Mine`), bez karty issue, `MarkRead` prázdné, `ScrollTo` 0.
  `QuickReplyTarget` beze změny vrací tuto zprávu, i vlastní (Odeslané).
  Rozhodnutí: událost Jiry (řádek bez těla) je v modelu `ItemEvent` bez
  vět, takže řádek nemá cíl a skryje se sám — pravidlo S2 je v referenci,
  ne v hostiteli; zpráva bez id dá prázdný model. Outbox a Koncepty
  souhrn sám nepozná (role složky), ty zůstávají na hostiteli
  (`SetShown(false)`).
- `quickreply.Controller.SetShown(bool)` nahradil `SetChatShown` (pole
  `paneShown`): hostitel říká, že panel ukazuje místo řádku (stránka jedné
  zprávy mimo Outbox a Koncepty, nebo chat konverzace); `Visible` =
  `SetShown` ∧ zobrazené vlákno ∧ cíl ∧ `Reply`. Skrytí nemaže text ani
  režim žádného vlákna a rozběhnuté Odeslat doběhne. Stav zůstává na
  vlákno, takže jedna zpráva sdílí text se svou konverzací.
- `window/conversation_host.go` jen volá `SetShown` (dvě místa), aby
  `ui/internal/window` šel přeložit; chování GTK se nemění (řádek dál jen
  pod chatem).
- Testy: `TestSingleModel` (cíl, vlastní zpráva, popis a událost Jiry,
  bidi a nový řádek ve jménu, prázdný model), v `quickreply`
  `TestSingleMessage` (cíl a `draft.create` na zobrazenou zprávu, vlastní
  zpráva, událost skrytá, komentář Jiry), `TestSetShown`,
  `TestTextKeptAcrossPages` (zpráva → konverzace v ose → chat → osa → jiná
  zpráva téhož vlákna → zpráva jiného vlákna a zpět, i s režimem),
  `TestOutboxAndDraftsHaveNoBar`, `TestSendEndsWhileHidden`; ostatní
  testy přejmenované na `SetShown`.
- Poznámka k S2: zpráva bez `threadId` (nepropojená starším démonem)
  řádek nemá, dokud ji backfill nepropojí (`SingleModel` dá vlákno "",
  `Visible` je nepravda a `SetText` text zahodí); chování zůstává záměrně.
- Opravy po revizi (2026-10-20):
  - **Stav na účet a vlákno.** `ThreadID` je jedinečné jen v účtu (Jira
    `jira:<issueId>` se opakuje na jiném site, Graph má `conversationId`
    serveru), takže hledání ve všech účtech mohlo ukázat issue 10001 site A
    a pak site B a text napsaný u A se objevil u B (a ⌘↩ by ho poslal na B).
    Kontroler drží stav pod klíčem (účet, vlákno): `Show(account, t)`
    (obojí "" = nic; vlákno "" je nic bez ohledu na účet), `Update` dál
    přebírá `m.Thread`, když se liší (s účtem z argumentu), a u
    zobrazeného vlákna bez účtu převezme `account.ID`; `State.Account`
    vedle `State.Thread`; Odeslat a Otevřít v okně si klíč zachytí,
    `finish` a `opened` ho používají. GTK: `conversationController.account`
    (účet nejnovějšího člena řádku, jinak souhrnu; `rowAccount`) se
    nastavuje v `show(row)` spolu s vláknem, totožnost řádku je (účet,
    vlákno), maže se v `reset`; `conversation_host.updateQuickReply` volá
    `Show(h.ctrl.account, h.ctrl.thread)`.
  - **Bez modelu prázdný model.** Při přechodu jedna zpráva → konverzace
    téhož vlákna bylo `Show` beze změny a `quickReplyInput` nil, takže
    v chatu řádek dál ukazoval cíl, akce a účet jedné zprávy, a po
    `thread.get`, který selhal bez souhrnu, už navždy.
    `updateQuickReply` teď bez modelu volá `Update` s prázdným modelem
    (bez cíle → skrytý řádek, text zůstává) a řádek čeká na skutečný.
  - Testy: `TestSameThreadOfTwoAccounts` (stejné id ve dvou účtech má
    vlastní text, režim i odesílání, které skončí v účtu, kde začalo),
    `TestUpdateAdoptsTheAccount`, `TestEmptyModelHidesAndKeepsText`;
    `conversation_controller_test.go` kontroluje `account` po výběru
    a po vyčištění; ostatní testy volají `Show(mailAcc.ID, …)`.
- Ověřeno na Macu: `go test -race ./internal/conversation/
  ./internal/quickreply/`, `go vet`, `gofmt -l`, `gopls check`
  `window/conversation_host.go`, `quick_reply*.go` a smoke testu. Překlad
  `ui/` s cgo ani smoke testy neběžely (beze změny chování; přijdou se S-G
  v kontejneru). Porty `setShown`/`SetShown` a `singleModel`/`SingleModel`
  dělají S-M a S-W.

### 12.3 As built — macOS (S-M)

Démon, API, texty ani `po/` beze změny; žádný nový msgid (seznamy volání
`L10n` v `ConversationReply.swift` a ostatních souborech zůstaly, takže
`portUsesTheTemplatesEntries` i `check-strings` sedí).

- **Core**: `Conversation.singleModel(_:mine:)` (port `SingleModel` na
  konci `ConversationReply.swift`; vlákno `threadId ?? ""`, odesílatel přes
  `Conversation.sender`, který přestal být `private`, událost Jiry přes
  `Jira.isEvent` jako `.event` bez vět, zpráva bez id prázdný model).
  `Conversation.isMine(_:account:)` je veřejný obal soukromého pravidla
  `Item.mine` (adresa účtu; u Jiry `MessageIssue.mine` bez `via`) — Go ho
  zatím nemá, GTK si `mine` spočítá ve `window` při S-G.
  `QuickReplyController.setShown(_:)` nahradil `setChatShown` (pole
  `paneShown`, `visible` = shown ∧ vlákno ∧ cíl ∧ Reply; skrytí nemaže text
  ani režim, rozběhnuté Odeslat doběhne). Swift-first:
  `ConversationController.quickReplyInput(single:)` vrací pro jednu zprávu
  model, akce karty (`actions(for:)`), účet a `shown` = ne Outbox
  (`MailModel.inOutbox`) a ne Koncepty (`inDrafts`); účet bez Reply skryje
  kontroler, událost model.
- **AppKit**: řádek a kontroler vlastní `ReadingPaneViewController` (ne
  `ConversationHostViewController`): `reader.view` i `conversation.view`
  končí na `bar.top`, `bar.bottom` = spodek panelu, skrytý řádek má výšku 0
  (dosavadní `appliedVisible`/`collapsed`). `show(_ row:)`: řádek konverzace
  → `controller.show(row)` a `conversationChanged()` (`show(thread)`,
  `update(quickReplyInput())`, `setShown(conversation.chatShown)`), totéž
  při každé změně kontroleru i přepnutí rozložení přes nové
  `ConversationHostViewController.onQuickReplyChange` (ignoruje se, dokud
  panel ukazuje jednu zprávu — `controller.clear()` hlásí změnu taky);
  jedna zpráva → `quickReplyInput(single:)` → `showSingleReply` (`show`,
  `update`, `setShown(shown)`); `nil` a `showSelection(count:)` →
  `show("")`, `setShown(false)`. Adaptér `QuickReplyHostAdapter` beze změny,
  jeho delegát je nově `readingPane.quickReplyDelegate = messageActions`
  (`Integration+Conversation.swift`), kam se přestěhovaly i `onFocusList`
  a `onAssistant`; odběr `AssistantController.onChange` pro ✦ drží panel.
  Šířka pro ikonu bez popisku je šířka řádku = šířka panelu čtení
  (`ConversationLayout.quickReplyIconOnly(pane:)` beze změny). Pozadí panelu
  (`ReadingPaneSurfaceView`) sleduje zobrazený pohled: barva čtečky pod
  zprávou, povrch konverzace pod chatem (dřív řádek ležel na povrchu
  konverzace). Escape, ✦, Message ▸ Send (`sendMessage:` na kontroleru
  řádku v řetězci responderů) a vše z §11 beze změny.
- Poznámka k S2: zpráva bez `threadId` (nepropojená starším démonem)
  řádek nemá, dokud ji backfill nepropojí (`singleModel` dá vlákno "",
  `visible` je nepravda a `setText` text zahodí); chování zůstává záměrně.
- **Opravy po revizi** (2026-10-20, porty Go 1:1 kde jde o kontroler):
  - Stav na účet a vlákno: `QuickReplyController.show(_ account:
    AccountID?, _ t: ThreadID)` (nil nebo "" a "" = nic), soukromý `Key`,
    `QuickReplyState.account`, `update` přebírá vlákno s účtem a účet
    zobrazenému vláknu bez účtu; `ConversationController.account`
    (`public private(set)`, účet nejnovějšího člena řádku, jinak souhrnu)
    se nastavuje s `thread`, totožnost řádku je (účet, vlákno);
    `ReadingPaneViewController` předává `controller.account` a u jedné
    zprávy `accountId` zprávy, skrytí `show(nil, "")`.
  - Klávesnice při skrytí: `ConversationQuickReplyBar.setVisible(false)`
    se zaměřeným polem už nedává `makeFirstResponder(nil)` (jednopísmenné
    zkratky okna by pak působily na vybranou konverzaci), ale předá
    klávesnici pohledu, který panel ukazuje (`keyboardTarget`: HTML tělo
    nebo tělo jen pro čtení zprávy, rolovací pohled zobrazeného rozložení
    konverzace — `ConversationScrollView` proto přijímá klávesnici, ale
    není zastávkou Tabu), jako GTK `shownScroller().GrabFocus()`; okno ji
    dostane jen, když pohled odmítne (skryté pole ji nesmí držet).
  - `show(_:)` a `showSelection(count:)` přepnou na jednu zprávu
    (`setConversation(false)`) **před** `controller.clear()`: `.cleared`
    dřív došlo do `conversationChanged()` ještě s `showsConversation`,
    takže řádek při přechodu konverzace → zpráva téhož vlákna problikl
    skrytý a pustil klávesnici.
  - `conversationChanged()` bez modelu volá `update` s prázdným modelem
    (`Capabilities.Actions.empty`, nový veřejný ekvivalent nulových akcí
    Go), takže řádek nečeká se starým cílem jedné zprávy.
  - Testy: porty `sameThreadOfTwoAccounts`, `updateAdoptsTheAccount`,
    `emptyModelHidesAndKeepsText` (`isKept` pro klíč), `ConversationControllerTests`
    kontroluje `account`; ostatní testy volají `show(mailAcc.id, …)`.
- **DevStart**: nový krok `quick-reply-message` najde skutečný panel čtení
  hlavního okna, ukáže v čtečce vymyšlenou zprávu a pod ní řádek
  s vymyšleným účtem pošty (Jiry) a vypíše `LAYOUT:` (panel, čtečka, řádek,
  zda čtečka končí na řádku, tlačítko režimu a jeho tooltip, zástupný text):
  široký, úzký, vlastní zpráva, událost Jiry, komentář Jiry, Outbox, nic
  vybraného. Krok `quick-reply` používá `setShown`.
- **Testy**: `ConversationReplyTests.singleModel` (port `TestSingleModel`
  + `isMine` a zpráva bez vlákna), `QuickReplyControllerTests`
  `singleMessage`, `setShown`, `textKeptAcrossPages`,
  `outboxAndDraftsHaveNoBar`, `sendEndsWhileHidden` (porty Go testů, ostatní
  přejmenované na `setShown`), `ConversationControllerTests.
  quickReplyInputOfASingleMessage` (cíl, vlastní zpráva v Odeslaných,
  Koncepty a Outbox bez řádku; harness umí `extraFolders`).
- **Ověřeno na Macu** (2026-10-20): `swift build` bez varování Swiftu,
  celé `swift test` 1894 testů ve 202 sadách zelené, `check-strings
  --strict` bez chybějících msgid (končí 1 kvůli 12 starým varováním
  v souborech mimo tuto práci), izolovaný start debug binárky s krokem
  `quick-reply-message` (řádek 46 pt pod zprávou, čtečka končí na jeho
  horní hraně, skrytý řádek výška 0, v úzkém panelu 308 pt jen ikona).
  Neověřeno: skutečné odeslání a okno odpovědi pod jednou zprávou nad daty
  (brána S-B1). Po opravách z revize: `swift build` bez varování, celé
  `swift test` 1897 testů ve 202 sadách zelené, `check-strings --strict`
  se stejnými 12 starými varováními; předání klávesnice při skrytí
  a přepnutí konverzace → zpráva se neměřily v aplikaci (izolovaný start
  znovu neběžel).

**Brána S-B1 (vlastník, macOS nad kopií dat)**:

1. Jedna zpráva v Doručené: řádek dole v panelu čtení, tooltip režimu
   jmenuje odesílatele; Odpovědět, Odpovědět všem (podle účtu), ⌘↩ odešle,
   toast, pole se vyprázdní, odpověď dorazí do Odeslaných s citací.
2. Zpráva v Odeslaných: řádek je, odpověď jde na vlastní zprávu.
3. Outbox a Koncepty: řádek není (u konceptu pruh Upravit).
4. Jira: položka issue (popis, komentář) → *Komentovat*, komentář odejde;
   událost (změna stavu) bez řádku.
5. Rozepsaný text u zprávy → zapnout/vypnout seskupování (Předvolby →
   Seznam zpráv): text zůstane u konverzace v chatu, zmizí v časové ose
   a vrátí se po přepnutí chat ↔ časová osa; jiné vlákno začíná prázdné.
6. Prázdný výběr a výběr více zpráv: řádek není; rozběhnuté odeslání
   doběhne (toast).
7. Úzký panel: tlačítko režimu jen ikona.
8. Escape v poli dá klávesnici seznamu; Enter nový řádek; prázdné pole
   ⌘↩ nic.
9. *Otevřít v okně* a *Přeposlat…* pod jednou zprávou: okno s textem,
   zavření se ptá, řádek se vyprázdní až po otevření okna.
10. ✦ v řádku dělá totéž co ✦ v liště; pozadí řádku pod zprávou ladí se
    čtečkou, pod chatem s konverzací.
11. Odeslat zpod jedné zprávy se seskupeným seznamem v časové ose: řádek
    zmizí s příchodem kopie do Odeslaných, klávesnice přejde na zobrazený
    pohled, rozepsaný text zůstane v paměti vlákna.
12. Hledání ve všech účtech se stejným vláknem ve dvou účtech (třeba
    issue téhož čísla na dvou site Jira): text napsaný u jednoho se
    u druhého neukáže.

**Otevřená otázka pro vlastníka (S7)**: když odeslaná odpověď udělá
z řádku konverzaci a panel ji ukáže v časové ose, řádek s rozepsaným
textem (třeba P.S.) zmizí uživateli pod rukama; text zůstane, klávesnice
přejde na zobrazený pohled. Má řádek s neprázdným textem zůstat vidět i
v časové ose? S7 zatím platí beze změny.

### 12.4 As built — GTK (S-G)

Napsané na Macu bez cgo a bez Blueprintu (jen `gofmt`, `gopls check`
a Go testy čistých balíčků); démon, API, `po/*.po*` ani msgid se nemění.

- **Co se přesunulo kam.** Widgety řádku (stejná id `conversation_reply_*`,
  jediný přeložitelný `_("Send (Ctrl+Enter)")`) odešly
  z `conversation_view.blp` do nového fragmentu `ui/data/ui/quick_reply_bar.blp`
  (kořen `Box conversation_reply_bar`, zapsaný v `go:generate` seznamu
  `ui/data/data.go`; Makefile bere `*.blp` zástupným znakem, takže build
  i `make po` ho najdou samy). Ve `window.blp` je dítě `toast_overlay`
  svislý `Box reading_column` s `message_stack` (`vexpand: true`); řádek
  do něj vloží Go jako posledního potomka (`Window.attachQuickReply`,
  volané v `New` po navázání ✦ lišty). Zvolil jsem fragment místo zápisu
  přímo do `window.blp`: řádek má jedinou definici v Blueprintu a smoke
  testy si ho postaví do vlastního sloupce. `*Window` vlastní
  `quickReply` (`quickreply.Controller` nad `quickReplyHost`) a
  `quickReplyBar`; `conversationHost` je už nevlastní a jen hlásí změny.
- **Jedno místo, které krmí kontroler**: `Window.updateQuickReply`
  (`window/quick_reply.go`) podle stránky `message_stack`: „message“ →
  `Show(účet, vlákno)` zobrazené zprávy (`w.paneMessage`, nastavuje ho
  `showPaneMessage` ze `showMessage` ještě před přepnutím stránky),
  `Update(mailModel.singleQuickReplyInput(s))` a `SetShown` (ne Outbox,
  ne Koncepty, ne prázdný souhrn); stránka konverzace → `Show`/`Update`
  z `conversationController.quickReplyInput()` (bez modelu prázdný model)
  a `SetShown(chatShown)`; každá jiná stránka (prázdná, bez účtů, výběr
  více řádků) → `Show("", "")`, `SetShown(false)`. Spouští ho
  `notify::visible-child-name` zásobníku, `showPaneMessage` (zpráva po
  zprávě stránku nemění) a hostitel konverzace při každé změně kontroleru
  i přepnutí rozložení. Vyčištěná konverzace (`hideConversation` běží dřív,
  než se vybere další stránka) nehlásí nic (`ctrl.thread == ""`), takže
  řádek při přechodu konverzace → zpráva téhož vlákna nezmizí a nepustí
  klávesnici (oprava macOS ze S-M jinou cestou). Kroky jednoho hlášení se
  vykreslí najednou (`quickReplyBar.hold`), aby `Show` jiného vlákna před
  jeho modelem řádek na okamžik neschoval.
- **Jedna zpráva**: `singleQuickReplyInput` = `conversation.SingleModel(s,
  mine)`, kde `mine` je pravidlo `Item.Mine` (nové
  `conversation.IsMine`, port Swiftu `Conversation.isMine`, na konci
  `reply.go` pod posledním msgid) nebo složka role `sent`; akce jako karta
  konverzace (`conversation.CardActions`, pro neznámý účet žádné, událost
  Jiry žádné a `SingleModel` jí nedá cíl); účet z `mailModel.account`.
  Zpráva bez `ThreadID` řádek nemá (kontroler bez vlákna nic nedrží).
- **Šířka**: řádek měří sám sebe — `followWidth(bar.root.Width())` (řádek
  je široký jako panel čtení) na nejbližším idle po každém `changed`
  vodorovné adjustace rolovacího okna pole (`queueFollowWidth`; textové
  pole ji nastavuje při každé alokaci a je tak široké, jak panel dovolí).
  Jeden spouštěč pro stránku zprávy i chat, skrytý řádek nic nepočítá;
  `chatColumnChanged` hostitele a jeho volání z `followSize` zmizely.
- **Klávesnice při skrytí**: drží-li ji řádek (fokus okna je v něm —
  `holdsKeyboard`, ne `HasFocus`, aby platilo i pro neaktivní okno),
  dostane ji pohled, který panel ukazuje (`Window.focusReadingView`: tělo
  zprávy `pane.stack`, rolovací okno zobrazeného rozložení konverzace,
  jinak cokoli na stránce), a když ji nic nevezme, nikdo
  (`SetFocus(nil)`); nikdy seznam (ten jen Escape, beze změny).
- **Pozadí**: pod chatem dostane řádek třídu `conversation-pane`
  (`setConversationSurface`), pod zprávou nic, takže ladí se stránkou nad
  sebou jako na macOS.
- **„•••“ (nález S-B1 z macOS)**: v GTK je `quoted_row` `Adw.Clamp` mimo
  rolovací tělo, ale skrývá se celý (`showQuotedButton` → `SetVisible(false)`
  na řádku) a skrytý widget v GTK místo nezabírá, takže pás nad řádkem
  nevzniká a oprava není potřeba.
- **Testy**: `quick_reply_bar_test.go` + `TestSingleQuickReplyInput`
  (Doručená, vlastní zpráva v Odeslaných i z aliasu, Outbox podle složky
  i značky, Koncepty, neznámý účet, žádná zpráva, událost Jiry);
  `TestIsMine` v `ui/internal/conversation`. `TestConversationChatGTKSmoke`
  staví sloupec se zásobníkem a řádkem (`attachQuickReply`,
  `w.useQuickReply`, `w.quickReplyBar`) a navíc hlídá pozadí pod chatem.
  Nový `MALACHI_GTK_SMOKE=1 TestQuickReplyMessageGTKSmoke` nad skutečným
  `window.blp` (`reading_column`) a `quick_reply_bar.blp`: řádek je
  potomek sloupce hned za `message_stack`, skrytý na prázdné stránce;
  pod zprávou a3 viditelný, tooltip jmenuje carol, výška zásobníku + řádku
  = sloupec, pravidlo ikony odpovídá vlastní šířce; Ctrl+Enter →
  `draft.create` (MessageID a3, účet a, reply) / `draft.save` /
  `message.send` přes falešného hostitele; rozepsaný text projde
  konverzací téhož vlákna v chatu (přepnutí stránky ani
  `hideConversation` řádek nehnou) a zpět; skrytý na prázdné stránce a pod
  zprávou v Outboxu i s klávesnicí mimo řádek a bez místa ve sloupci;
  skrytý u výběru více řádků a v Konceptech; v Odeslaných pro vlastní r1
  viditelný se stejným textem vlákna; zpráva bez vlákna bez řádku.
- **Čeká** (kontejner, orchestrátor): `gtk.sh make build` (Blueprint
  přeloží `quick_reply_bar.blp` a nový `window.blp` poprvé), `gtk.sh
  test-ui`, oba smoke testy (`MALACHI_GTK_SMOKE=1 go test ./internal/window
  -run '^(TestConversationChatGTKSmoke|TestQuickReplyMessageGTKSmoke)$'
  -count=1 -v`), `make po` (posunou se jen odkazy `#:` — `window.go`,
  `quick_reply_bar.go`, nový `.ui` pro „Send (Ctrl+Enter)“) a `make lint`.
  Nevyzkoušené: zda `notify::visible-child-name` přijde i z prvního
  `AddNamed` (smoke test s tím počítá jen jako s neškodným hlášením), zda
  `pane.stack.GrabFocus()` pod zprávou opravdu vezme klávesnici (jinak
  `SetFocus(nil)`), přepočet ikony při zúžení okna (smoke test kontroluje
  jen shodu s vlastní šířkou při 1200 px) a skutečné odeslání nad daty.
  Pak brána **S-B2** vlastníka v XQuartz (body S-B1 1–12 v GTK, navíc
  pozadí řádku pod zprávou a pod chatem).

Z nezávislé revize GTK (2026-10-20): řádek, který drží klávesnici,
ji při výměně konverzace pod rukama (zpráva zmizela ze seznamu, soused
vybraný místo ní; `hold` vykreslí až po přepnutí, takže by pole tiše
dostalo text jiného vlákna) předá zobrazenému pohledu jako macOS
(`quickReplyBar.leaveIfOther`); `focusReadingView` už nemá obecnou zálohu
na box stránky (dala by klávesnici vybíratelnému popisku předmětu).
Ponecháno a pojmenováno: po skrytí řádku s klávesnicí (seskupený seznam,
příchod kopie do Odeslaných nebo cizí odpovědi mění řádek na konverzaci
v časové ose) jde klávesnice na tělo či rolovací pohled a brána psaní
zapne jednoklávesové zkratky (`a`, `j`, `s`, `u`, Delete) nad konverzací
— stejně jako macOS; rozhodnutí vlastníka v S-B2 (viz otázka v §12.3:
řádek s textem nebo klávesnicí by mohl zůstat viditelný i v časové ose).
Vstup jedné zprávy se počítá při `showMessage`, ne při pozdějším načtení
rolí složek nebo účtů (jako banner konceptu); dorovná se při další změně
zprávy či stránky.

### 12.5 As built — Windows (S-W)

Napsané na Macu bez `dotnet`, podle okolního kódu; nic se nepřeložilo ani
nespustilo. Podle pravidla vlastníka se Windows klient neověřuje přes
GitHub Actions: tahle sekce je předávka agentovi vlastníka na stroji
s Windows. Démon, API, texty ani `po/` beze změny (žádný nový msgid; české
„Napsat rychlou odpověď…“ je jen v `po/cs.po`).

- **Core (port Go 1:1)**: `Model/Conversation.Reply.cs`
  `Conversation.SingleModel(MessageSummary, bool mine)` (jedna položka
  `Message`, událost Jiry `Event` bez vět, takže bez cíle; zpráva bez id
  prázdný model; bez `ThreadId` vlákno ""), `Model/Conversation.cs`
  `Conversation.IsMine` (Swift `isMine`, obal soukromého `Mine`).
  `Controllers/QuickReplyController.cs`: stav pod klíčem (účet, vlákno)
  (soukromý `Key`, `None`), `Show(AccountId?, ThreadId)` (vlákno "" = nic
  bez ohledu na účet), `Update` převezme vlákno modelu s účtem z argumentu
  a účet zobrazenému vláknu bez účtu, `SetShown(bool)` místo
  `SetChatShown`, `IsKept(account, thread)` (Swift `isKept`), `KeptThreads`
  dál vlákna libovolného účtu; Send a Otevřít v okně si klíč zachytí.
  `QuickReplyState.Account` (v `Equals`, `GetHashCode` — vnořené
  `HashCode.Combine`, osm argumentů je strop — a `ToString`).
  `Controllers/ConversationController.cs`: `ThreadAccount` (Swift a Go
  `account`; jméno `Account` drží metoda vyhledání účtu) se nastavuje
  s `Thread` z řádku (účet nejnovějšího člena, jinak souhrnu), totožnost
  řádku je (účet, vlákno), maže se v `Reset`; nové přetížení
  `QuickReplyInput(MessageSummary single)` vrací model jedné zprávy, akce
  karty, účet a `Shown` (ne `MailModel.InOutbox`, ne `InDrafts`). Odkazy
  `cref` na `QuickReplyInput` mají kvůli přetížení závorky.
- **App (hostitel)**: řádek i kontroler vlastní `Reader/MessageView.xaml(.cs)`
  jen v `ReaderMode.Pane` (okno zprávy a přiložené zprávy ho nemají):
  poslední řádek `Auto` mřížky `Root` (nový osmý `RowDefinition`, řádek
  přidaný z kódu `Grid.SetRow(bar, 7)`), pod `Pages` a mimo každou
  rolovací oblast; sbalený nezabírá místo. `FeedQuickReply` při každé
  změně `ReaderController.Page` (po přepnutí viditelnosti stránek)
  a každém `Rendered`: stránka zprávy → `Show(s.AccountId, s.ThreadId)`,
  `Update(QuickReplyInput(s))`, `SetShown(Shown)`; stránka konverzace →
  `Show(ThreadAccount, Thread)`, `Update(QuickReplyInput())` nebo prázdný
  model, `SetShown(ChatShown)`; prázdná stránka, Žádné účty a počet více
  řádků → `Show(null, "")`, `SetShown(false)`.
  `Reader/Conversation/ConversationView.xaml(.cs)` už řádek nemá (třetí
  řádek mřížky pryč, `ListOnScreen` pryč): hlásí `QuickReplyChanged` při
  každé změně kontroleru kromě `Cleared` a při každém přepnutí rozložení,
  dává `Controller`, `ChatShown` a `TakeKeyboard()`. Proč ne `Cleared`: hub
  maže konverzaci jen pro řádek, který konverzací není, a stránka, kterou
  čtečka ukáže hned potom (zpráva, prázdná, počet), řádek nakrmí sama;
  odpovídá macOS opravě „přepnout stránku před `controller.clear()`“, takže
  řádek při přechodu konverzace → zpráva téhož vlákna neproblikne skrytý.
  `ReaderHub` dává `FocusList` a `ListOnScreen` panelu (`MessageView`), ne
  konverzaci. `ConversationQuickReplyBar` zůstal ve složce
  `Reader/Conversation` se stejnými AutomationId; šířku pro ikonu bez
  popisku dostává od panelu (`SetPaneWidth` ze `SizeChanged` `MessageView`,
  i když je sbalený, takže se ukáže hned správně), vlastní `SizeChanged`
  zmizel.
- **Klávesnice skrývajícího se řádku**: `Render`, který řádek sbalí
  s fokusem uvnitř (`OwnsFocus`), zavolá `KeyboardTarget`; panel ji předá
  až po tahu (`DispatcherQueue.TryEnqueue`), protože čtečka jde ze zprávy
  do konverzace přes prázdnou stránku (`LeaveForConversation` volá
  `Clear()`), a dá ji zobrazenému pohledu: tělu zprávy (`FocusBody`),
  rolovací oblasti zobrazeného rozložení konverzace
  (`ConversationView.FocusScroller`: `Scroller` není zastávka Tabu, takže se
  jí stane jen na dobu fokusu a při `LostFocus` přestane, jako macOS
  `ConversationScrollView`), jinak seznamu, je-li na obrazovce.
- **Pás nad řádkem (nález S-B1)**: na Windows není co opravovat. `QuotedRow`
  („•••“) leží mimo rolovací tělo, ale startuje `Collapsed` a sbaluje se
  spolu s tlačítkem (`QuotedRow.Visibility = QuotedButton.Visibility`),
  ostatní lišty jsou nad tělem. Pozadí: `ConversationView` je průhledná,
  řádek leží pod zprávou i pod chatem na pozadí `Root` panelu, takže
  sledování pozadí jako na macOS není potřeba.
- **Testy**: `Malachi.Core.Tests` `Model/ConversationReplyTests.SingleModel`
  (port `TestSingleModel` + `IsMine` a zpráva bez vlákna),
  `Controllers/QuickReplyControllerTests` `SingleMessage`, `SetShown`,
  `TextKeptAcrossPages`, `OutboxAndDraftsHaveNoBar`, `SendEndsWhileHidden`,
  `SameThreadOfTwoAccounts`, `UpdateAdoptsTheAccount`,
  `EmptyModelHidesAndKeepsText` (ostatní volají `SetShown` a
  `Show(MailAcc.Id, …)`), `Controllers/ConversationControllerTests`
  (`ThreadAccount` po výběru a vyčištění, `QuickReplyInputOfASingleMessage`;
  harness umí `extraFolders`); UI smoke
  `ConversationChatSmokeTests.TheQuickReplyBarStandsUnderASingleMessage`
  nad osamělou zprávou `ConversationChatFixture.SingleSubject` (od Ady
  s `Reply-To` na testovacího uživatele; INBOX serveru teď čeká + 5): řádek
  pod stránkou zprávy a ne konverzace, Odeslat vypnuté a po psaní zapnuté,
  text zůstane přes jiný řádek a zpět, stránka více řádků bez řádku
  (`SelectionItemPattern.AddToSelection`), Ctrl+Enter → Sent a INBOX
  serveru + 1, řádek se stane konverzací v chatu s řádkem a text napsaný
  tam přežije návštěvu časové osy. Outbox a Koncepty jen v Core testech
  (fixtura se k nim nedostane).

**Ručně zkontrolováno na Macu**: každý použitý člen existuje (včetně
`MailModel.InOutbox`/`InDrafts`, `Jira.IsEvent`, `Conversation.Sender`/`Mine`,
`MainWindow.ShowAssistantFrom`, `MessageListOnScreen`, `FocusMessageList`),
nullable (`AccountId?` a `ThreadId?` normalizované na "", `Normal`), stínění
pole `account` parametrem `Show` (`this.account`), složené závorky a
závorky všech změněných souborů, hlavičky SPDX beze změny (žádný nový
soubor), jmenné prostory XAML beze změny, žádný literál `L10n.` navíc
v `Conversation.Reply.cs` (test `TheTextsAreTheTemplatesMsgids` počítá
volání), žádný nový řetězec pro uživatele.

**Pro agenta na Windows** (z `windows\`): `build.ps1 app`, `build.ps1 test`
(hlavně výše jmenované sady a `ApiCodingTests`/`GoContractTests` beze změny),
`build.ps1 lint` (formát, konvence, `IconGlyphsTests` proti novému
`ui/data/ui/quick_reply_bar.blp` z S-G — ikony v něm mají glyf), UI smoke
s `MALACHI_DEVMAIL` (`ConversationChatSmokeTests` celé třídy, i dosavadní
`TheQuickReplyBarSendsAndOpensTheComposeWindow`, kterému se změnil hostitel)
a ruční průchod níže.

**Brána S-B3 (ruční průchod na Windows, nad kopií dat nebo devmailem)**:

1. Jedna zpráva v Doručené: řádek dole v panelu čtení, tooltip režimu
   jmenuje odesílatele; Odpovědět, Odpovědět všem (podle účtu), Ctrl+Enter
   odešle, toast, pole se vyprázdní, odpověď dorazí do Odeslaných s citací.
2. Zpráva v Odeslaných: řádek je, odpověď jde na vlastní zprávu.
3. Outbox a Koncepty: řádek není (u konceptu pruh Upravit).
4. Jira: popis a komentář issue → *Komentovat*, komentář odejde; událost
   bez řádku.
5. Rozepsaný text u zprávy → zapnout/vypnout seskupování: text zůstane
   u konverzace v chatu, zmizí v časové ose a vrátí se po přepnutí;
   jiné vlákno začíná prázdné.
6. Prázdný výběr a výběr více zpráv (Ctrl/Shift-klik): řádek není;
   rozběhnuté odeslání doběhne (toast).
7. Úzký panel (900/600 px skládání): tlačítko režimu jen ikona, i když se
   řádek objeví až po zúžení.
8. Escape v poli dá klávesnici seznamu (seznam vidět; ve složeném
   rozložení nic); Enter nový řádek; prázdné pole Ctrl+Enter nic.
9. *Otevřít v okně* a *Přeposlat…* pod jednou zprávou: okno s textem,
   zavření se ptá, řádek se vyprázdní až po otevření okna.
10. ✦ v řádku dělá totéž co ✦ v liště, pod zprávou i pod chatem.
11. Odeslat zpod jedné zprávy se seskupeným seznamem v časové ose: řádek
    zmizí s příchodem kopie do Odeslaných a klávesnice skončí v rolovací
    oblasti časové osy (šipky rolují), ne na skrytém poli a ne v seznamu;
    Tab z ní jde dál a zpět se na ni nevrací.
12. Přechod chat → zpráva téhož vlákna (rozbalit konverzaci a vybrat člena)
    s fokusem v poli: řádek nepoblikne a fokus zůstane v poli.
13. Hledání ve všech účtech se stejným id vlákna ve dvou účtech: text
    napsaný u jednoho se u druhého neukáže.
14. Okno zprávy (dvojklik) řádek nemá; tmavý a vysoce kontrastní motiv.

**Hádané chování WinUI (k ověření)**: že `Focus(Programmatic)` na
`ScrollViewer`, kterému se `IsTabStop` zapne těsně předtím, projde a že
`LostFocus` (bublá i od potomků) ho spolehlivě vrátí; že sbalení
zaměřeného `TextBox` nepřesune fokus někam dřív, než doběhne odložené
předání (přebije ho, ale mohl by problikat fokusový rámeček); že
`Rendered` čtečky chodí pro každou zobrazenou zprávu (krmení i při změně
zprávy beze změny stránky); že `SizeChanged` `MessageView` přichází i ve
složeném rozložení; že UIA `AddToSelection` na `ListViewItem` v režimu
`Extended` udělá výběr více řádků (jinak ať smoke test použije Ctrl-klik);
dosavadní hádání z §11.2.2 platí dál.
