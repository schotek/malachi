# Profil pro asistenta („O mně“ a doplněk k účtu) — návrh

Stav: návrh, otázky Q1–Q10 rozhodl vlastník 2026-10-21 (§9)
a rozhodnutí v §2 jsou podle nich upravená. Nic z toho není
implementované.

## 1. Co se mění

Uživatel napíše jednou, co má asistent o něm vědět a jak má pracovat
(„píšu stručně“, „schůzky navrhuj na pondělí a středu“), zvolí, v jakém
rodě píše, a k jednotlivým poštovním účtům a účtům Jira napíše doplněk
(„podepisuj jako Studio Paprsek“). Asistent ho pak dostane u **každého**
promptu v aplikaci: panel, přepis v okně Nová zpráva, pruh psaní
odpovědi, hledání vlastními slovy (jen globální část), návrh odpovědi
a triage nástěnky, akce kalendáře, a to u obou poskytovatelů (Claude
Code i ChatGPT/Codex). Pokud to uživatel nevypne, stejný text dostanou
i aplikace připojené přes most MCP (Claude Desktop, Claude Code mimo
aplikaci). Asistent může navrhnout, co si o uživateli zapamatovat;
do profilu se to dostane, až když uživatel návrh potvrdí.

Dnes každá žádost skládá systémový prompt zvlášť:

| Místo | Funkce (Go reference) | Kdo ji volá |
|---|---|---|
| Panel | `assistant.SystemPrompt(language, today)` (`claude.go`) | `assistantpanel` Controller |
| Přepis výběru | `assistant.RewriteSystemPrompt()` (`rewrite.go`) | `assistantpanel/oneshot.go` Rewriter |
| Hledání vlastními slovy | `assistant.SearchSystemPrompt(today)` (`search.go`) | `oneshot.go` Searcher |
| Pruh psaní v okně Nová zpráva | `assistant.WriteSystemPrompt()` (`compose_write.go`) | `assistantpanel/writer.go` |
| Návrh odpovědi na nástěnce | `assistant.SuggestReplySystemPromptFor(…)` (`suggest_reply.go`) | `boardreply` |
| Triage | `assistant.TriageSystemPrompt(language, today)` (`triage.go`) | `boardtriage` |
| Akce kalendáře | `assistant.CalendarPrompt(…)` (`calendar_actions.go`) | panel |
| ChatGPT/Codex | `spec.SystemPrompt` → `baseInstructions` (`ui/internal/chatgpt/provider.go`) | stejné žádosti |
| Most MCP | `serverInstructions` (`backend/cmd/malachi-mcp/main.go`) | každý klient MCP |

Swift (`MalachiCore/Assistant/`) a C# (`Malachi.Core/Assistants/`) to
mají jako porty 1:1.

## 2. Rozhodnutí v návrhu (P1–P16)

| # | Rozhodnutí | Proč |
|---|---|---|
| P1 | **Data drží démon**, ne předvolby klienta (gschema, `UserDefaults`, registr). | Profil musí být stejný ve všech třech klientech a musí ho vidět most MCP, tedy i Claude Desktop. Pravidlo 1. |
| P2 | **Globální text** (prostý text, nejvýš 2000 znaků po čištění), **rod** (P13) a **doplněk ke každému účtu druhu `imap`, `graph` a `jira`** (nejvýš 500 znaků). eWay a CalDAV doplněk nemají (Q4, Q8). | Asistent píše jménem uživatele zprávy a komentáře Jira; do eWay a kalendáře nepíše. |
| P3 | Úložiště v `meta` pod klíčem `assistant.profile` (JSON: `text`, `gender`, `accounts: {id: note}`, `shareWithMCP`, `updatedAt`), stejně jako `board.prefs`; čekající návrhy (P14) pod `assistant.profile.proposals`. **Bez migrace** a bez změny `config.toml`. Při odebrání účtu démon jeho doplněk smaže. | Žádná nevratná migrace kvůli jednomu textu. |
| P4 | **API** (kompatibilní rozšíření protokolu 2, `docs/api.md` a test `TestDocsCoverAllMethods`): `assistant.profileGet` → `{text, gender, accounts[{accountId, note}], shareWithMCP, proposals[], limits}`; `assistant.profileSet {text?, gender?, accounts?, shareWithMCP?}` (chybějící pole = beze změny, prázdný řetězec = smazat); návrhy viz P14; notifikace `notify.assistantProfileChanged` (i při změně návrhů). Chyby jen stávající (`invalidArgument` nad limitem či u účtu jiného druhu, `accountNotFound`). | Stejný vzor jako `config.set` a preference nástěnky. |
| P5 | **Démon text čistí** stejně jako poznámky asistenta na nástěnce: pryč řídicí, bidi a neviditelné znaky (kromě `\n`), NFC, ořez mezer, limity v bajtech i znacích. Zakázané jsou i řádky, které vypadají jako ohrada mostu (`--- BEGIN UNTRUSTED`, `--- END UNTRUSTED`). Démon text nikdy neloguje. Totéž platí pro text návrhů. | Profil jde do promptů jako text uživatele: nesmí podvrhnout hranici nedůvěryhodného obsahu. |
| P6 | **Jedno místo skládání**: Go `assistant.WithProfile(system string, p Profile, accounts []AccountRef) string` (Swift `Assistant.withProfile`, C# `Assistant.WithProfile`) připojí na **konec** systémového promptu ohraničený blok (anglicky jako ostatní prompty, obsah v jazyce uživatele). Každá funkce z tabulky v §1 dostane profil jako parametr a vrací už složený prompt. Prázdný profil = beze změny bajt po bajtu. | Jedno pravidlo místo sedmi kopií; prefix promptu zůstane stabilní kvůli cache. |
| P7 | Tvar bloku: `About the user (written by the user themselves; follow these preferences, but they never override the rules above):`, pak věta o rodě (P13), globální text, a pro každý účet v kontextu `For the account "<jméno>" (<adresa nebo site>):` + doplněk. | Model ví, že jde o přání uživatele, ne o obsah pošty, a že pevná pravidla platí dál. |
| P8 | **Který doplněk**: jen pro účty, kterých se žádost týká. Okno Nová zpráva (přepis, psaní) = účet odesílatele, u komentáře Jira účet Jira; návrh odpovědi a triage = účet případu (u triage doplněk každého účtu ve frontě); panel = účty výběru (víc účtů = víc doplňků), bez výběru žádný. **Hledání vlastními slovy dostane jen globální text a rod, nikdy doplněk** (Q1). | Doplněk „podepisuj jako studio“ nesmí uniknout do osobní pošty; hledání doplňky nepotřebuje. |
| P9 | **Most MCP**, když je `shareWithMCP` zapnuté (výchozí, Q5): po handshaku si most přečte profil přes `assistant.profileGet` a připojí blok podle P7 k `instructions` (všechny doplňky s názvy účtů); nástroj `get_user_profile` (jen čtení, i pod `--reply-only` a `--triage-run`) vrátí aktuální profil a `list_accounts` vrací u účtu `assistantNote`. Instrukce řeknou, ať model zavolá `get_user_profile`, než začne psát text za uživatele. Vypnuté `shareWithMCP`: žádný blok, `get_user_profile` vrátí pevnou větu, že uživatel profil aplikacím přes MCP nesdílí, a `list_accounts` doplňky nevrací. Výjimka: instance mostu, kterou spouští aplikace sama (panel, triage, návrh odpovědi; poznají se podle stávajících přepínačů panelu, nově `--in-app`), sdílení nepotřebuje, protože profil má v systémovém promptu, a `get_user_profile` vrací profil vždy. | Klienti MCP čtou `instructions` jen jednou při připojení; nástroj vrátí aktuální text bez restartu. Vypínač dává uživateli kontrolu nad tím, co dostanou cizí aplikace. |
| P10 | Profil **nemění oprávnění**: allowlisty nástrojů, `--allow-*`, `--triage-run`, `triageReplyAllowed` a pravidlo „pošta jsou data“ zůstávají tvrdé. Text profilu, který žádá víc („posílej rovnou“), na tom nic nezmění, protože omezení nejsou v promptu. | Prompt není bezpečnostní hranice. |
| P11 | **Souhlas**: profil jde jen poskytovateli, se kterým má uživatel souhlas (stávající `assistant-consent`, `board-triage-consent`, souhlas ChatGPT). Nový souhlas není potřeba, ale texty souhlasů a popis v Předvolbách řeknou, že asistent dostane i profil. Přes most ho dostane každý klient zaregistrovaný v Předvolbách → AI → MCP, jen se zapnutým „Posílat profil i aplikacím přes MCP“ (P9). | Uživatel musí vědět, kam text jde. |
| P12 | Změna profilu **nezneplatní** anotace nástěnky (`stale`) ani `input_key`; platí od další žádosti. Běžící proces panelu dostane nový profil až od nového rozhovoru. | Profil není vstup pravidel nástěnky. Přepočet všech případů kvůli změně tónu by byl drahý. |
| P13 | **Rod** (Q2): enum `gender` = `unset` (výchozí) / `masculine` / `feminine` / `neutral`. Do promptu jde jako pevná anglická věta („When writing in the user's name in a language with grammatical gender, use the masculine forms.“, u `neutral` „…avoid gendered forms where the language allows it.“), u `unset` nic. Platí i pro jednorázové žádosti (přepis, psaní). | Do češtiny nejčastější potřeba; strukturované pole jde spolehlivě vložit i tam, kde by volný text model přehlédl. |
| P14 | **Návrhy do profilu** (Q3): asistent smí jen **navrhnout** krátký text (nejvýš 300 znaků) do globální části nebo do doplňku jednoho účtu. Démon: `assistant.profilePropose {text, accountId?, source}` uloží návrh do fronty (nejvýš 20, nejstarší vypadne, duplicity podle normalizovaného textu se slijí), `assistant.profileResolve {id, accept, text?}` ho přijme (připojí jako nový řádek; s `text` upravený uživatelem) nebo zahodí. Přijetí nad limitem profilu = `invalidArgument`, návrh zůstane. Nic se do profilu nezapíše bez kliknutí uživatele. | Asistent se „učí“, ale uživatel zůstává jediným autorem profilu. |
| P15 | **Kdo smí navrhovat**: nástroj mostu `propose_profile_note` jen v panelu aplikace (most s `--in-app`) a v klientech MCP se zapnutým `shareWithMCP`; **nikdy** pod `--triage-run`, `--reply-only` ani v jednorázových žádostech (přepis, psaní, hledání nemají nástroje). Nejvýš 3 návrhy na proces mostu. Instrukce řeknou, ať model navrhuje jen to, co uživatel v rozhovoru sám řekl o sobě nebo o tom, jak chce pracovat, nikdy obsah pošty. `source` = `panel` / `mcp`. | Triage a návrh odpovědi čtou cizí poštu ve velkém, takže vložený text z e-mailu by se snadno přeměnil v „návrh“. Potvrzení uživatelem je hlavní ochrana, omezení kanálů druhá. |
| P16 | **Zobrazení návrhů**: v panelu karta pod odpovědí („Asistent navrhuje zapamatovat si: …“, u doplňku jméno účtu) s tlačítky Uložit, Upravit… (upraví jen text návrhu, Q9) a Zahodit; v Předvolbách → AI → O mně seznam čekajících návrhů se stejnými tlačítky; položka nabídky panelu „Upravit, co o vás asistent ví…“ nese počet čekajících návrhů. Návrhy z MCP se jinde neohlašují: žádná notifikace ani odznak (Q10). Text návrhu je vždy prostý text. | Návrh z panelu uživatel vyřídí hned; návrh z Claude Desktop najde v Předvolbách. |

## 3. Démon (`backend/`)

- `pkg/api/assistant_profile.go`: `AssistantProfile`, `ProfileGender`,
  `ProfileProposal`, `AssistantProfileSetParams`,
  `ProfileProposeParams`, `ProfileResolveParams`, limity
  `MaxProfileText = 2000`, `MaxProfileAccountNote = 500`,
  `MaxProfileProposal = 300`, `MaxProfileProposals = 20` (znaky;
  v bajtech čtyřnásobek), notifikace. `docs/api.md` nový oddíl a řádky
  metod.
- `internal/core/assistant_profile.go`: get/set nad `meta`, čištění
  (sdílené s čištěním poznámek nástěnky, pokud jde vytáhnout bez
  závislosti na `internal/board`), kontrola druhu účtu (`imap`, `graph`,
  `jira`), smazání doplňku a návrhů účtu v cestě odebrání účtu, fronta
  návrhů, notifikace s koalescencí 250 ms.
- Most `cmd/malachi-mcp`: `instructions()` dostane profil podle
  `shareWithMCP`, přepínač `--in-app` (předává ho aplikace v příkazové
  řádce panelu, triage a návrhu odpovědi), nástroje `get_user_profile`
  a `propose_profile_note` (P15, počítadlo na proces), `assistantNote`
  v `list_accounts`, `docs/mcp.md`. Text profilu **nejde** přes
  `clean()` a ohradu s nonce jako pošta (je od uživatele), ale stejným
  čištěním znaků; ohrada mostu se v něm nesmí objevit (P5).
- Testy: patologické vstupy (bidi, nulové znaky, 10 MiB, podvržená
  ohrada, neplatné UTF-8, id cizího účtu, účet eWay a CalDAV), round
  trip, smazání s účtem, fronta návrhů (limit, slití duplicit, přijetí
  nad limitem profilu, úprava při přijetí), instrukce mostu se sdílením
  i bez, `--in-app`, `get_user_profile` pod `--reply-only`
  a `--triage-run`, `propose_profile_note` pod nimi chybí, čtvrtý návrh
  procesu odmítnut.

## 4. Čistá logika klientů (Go reference, pak porty)

- `ui/internal/assistant/profile.go`: `Profile`, `Gender`, `AccountRef`,
  `WithProfile`, `ProfileAccountsFor(…)` (P8), věty rodu (P13), texty
  Předvoleb a karty návrhu a jejich msgid. Každá `*SystemPrompt`
  funkce dostane `Profile` a účty; volající v `assistantpanel`,
  `boardreply`, `boardtriage` a `chatgpt` si profil vezmou ze sdíleného
  stavu aplikace (načtený při startu a po
  `notify.assistantProfileChanged`).
- `ui/internal/assistant/events.go`: výsledek nástroje
  `propose_profile_note` v stream-json se stane položkou přepisu
  „návrh“ (id návrhu z výsledku), stejně jako karta „Otevřít koncept“.
- Testy: prázdný profil nemění žádný prompt (porovnání bajtů se
  současným výstupem), pořadí bloků, rod, výběr doplňků podle P8
  (hledání bez doplňků), limity, položka návrhu z události.
- Porty: Swift `MalachiCore/Assistant/AssistantProfile.swift`, C#
  `Malachi.Core/Assistants/Assistant.Profile.cs`, oba s testy převzatými
  z Go (u C# kontrola bajtů UTF-8 jako u ostatních promptů).

## 5. UI (všechny tři klienty)

Inspirace je okno z referenčního snímku, ne jeho kopie: žádná nová
záložka s podzáložkami (Q6).

- **Předvolby → AI**, nová skupina **O mně** (msgid „About You“) nad
  skupinou Asistent:
  - řádek **Píšu v rodě** s nabídkou Neuvádět / Mužském / Ženském /
    Neutrálně;
  - víceřádkové pole (GTK `GtkTextView` v rámečku, macOS `NSTextView`,
    Windows `TextBox` s `AcceptsReturn`), zástupný text s příkladem,
    počítadlo „%d / %d“, uloží se po 1 s klidu a při opuštění stránky;
  - pod ním řádek pro každý poštovní účet a účet Jira (tečka barvy
    účtu, jméno, u Jiry kapsle JIRA, vpravo stav „Bez doplňku“ / první
    slova doplňku); klik řádek rozbalí na pole doplňku přímo na místě
    (GTK `Adw.ExpanderRow`, macOS disclosure, Windows `Expander`), ne
    v samostatném dialogu;
  - přepínač **Posílat profil i aplikacím přes MCP** (výchozí zapnuto)
    s podtitulkem „Claude Desktop a další aplikace zaregistrované
    v MCP; změna platí po jejich restartu.“;
  - **Čekající návrhy** (jen když nějaké jsou): text, odkud přišel
    (panel / MCP), u doplňku jméno účtu, tlačítka Uložit, Upravit…
    a Zahodit;
  - jedna věta pod skupinou, kam text jde: „Asistent ho dostane
    v aplikaci (Claude Code, ChatGPT) a podle přepínače i v aplikacích
    připojených přes MCP, jen s vaším souhlasem.“
- **Panel asistenta**: karta návrhu (P16) v přepisu; v nabídce panelu
  položka „Upravit, co o vás asistent ví…“ (s počtem čekajících návrhů,
  když nějaké jsou), která otevře skupinu O mně.
- Skupina je vidět, i když asistent není zapnutý (profil pak používá
  jen most), ale pole jsou neaktivní, dokud démon neodpoví.
- Text profilu i návrhů se ukazuje jen jako prostý text
  (`SetUseMarkup(false)`, `stringValue`, `TextBox.Text`).

Nové msgid (odhad 18–22): název skupiny, rod a jeho volby, zástupné
texty, počítadlo, „Bez doplňku“, přepínač MCP s podtitulkem, popis,
čekající návrhy, karta návrhu a její tlačítka, položka nabídky panelu.
Do `po/POTFILES` `ui/internal/assistant/profile.go`, česky v
`po/cs.po`.

## 6. Bezpečnost (`docs/security.md`)

Nový odstavec v kapitole o asistentovi:
- profil je jediný text v promptu, který model bere jako přání
  uživatele; stojí za pevnými pravidly a výslovně je nepřebíjí (P7);
- skutečné hranice (nástroje, přepínače mostu, pravidla triage
  a návrhu odpovědi) nejsou v promptu, takže je profil nezmění (P10);
- démon text čistí a neloguje (P5); odchází jen poskytovatelům se
  souhlasem a, je-li sdílení zapnuté, zaregistrovaným klientům MCP
  (P9, P11);
- návrhy jsou cesta, kterou by vložený text z e-mailu mohl zkusit
  dostat do všech budoucích promptů: proto jen kanály, kde uživatel
  s asistentem mluví (P15), limit na proces, prostý text v UI
  a **vždy** potvrzení uživatele (P14); triage ani návrh odpovědi
  navrhovat nemohou;
- útočník, který může zapisovat do socketu démona, profil změnit
  umí, ale to už dnes platí pro všechno (`docs/security.md` §8).

## 7. Pořadí prací a brány (Q7: macOS první)

Výjimka z pravidla 6 se stejným postupem jako u chatu:

1. **Démon a most** (API, úložiště, čištění, návrhy,
   `get_user_profile`, `propose_profile_note`, `--in-app`,
   dokumentace, testy). Ověření: `go test` démona a mostu, `go vet`.
2. **Go reference** `ui/internal/assistant` + napojení všech volajících
   v `assistantpanel`, `boardreply`, `boardtriage`, `chatgpt`; Go testy
   na Macu.
3. **macOS** (port `AssistantProfile`, Předvolby → AI, karta návrhu
   v panelu, `--in-app` v příkazových řádkách); `swift build`,
   `swift test`, `check-strings.py`, krok `MALACHI_START` `prefs-ai`
   nad izolovanou instancí. Brána **P-B1** vlastníka nad kopií dat.
4. **GTK** (Předvolby → AI, sdílený stav profilu, karta návrhu,
   položka v panelu); kontejner: `gtk.sh make build`, `test-ui`,
   `make po`, `make lint`. Brána **P-B2** vlastníka v XQuartz.
5. **Windows** port napsaný na Macu bez `dotnet` a předaný Windows
   agentovi vlastníka (sestavení, testy, lint, UI smoke); nikdy přes
   `windows.yml`. Brána **P-B3**.

Ruční ověření v každém klientu: napsat profil, rod a doplněk (pošta
i Jira), zeptat se v panelu („jak se podepisuju?“), přepis v okně Nová
zpráva z účtu s doplňkem a bez něj, komentář Jira, hledání vlastními
slovy (bez doplňku), návrh odpovědi na nástěnce, triage, návrh
v panelu („pamatuj si, že…“) a jeho Uložit / Upravit / Zahodit,
Claude Desktop přes most se sdílením i bez (`get_user_profile`,
instrukce po restartu, návrh z Claude Desktop v Předvolbách), smazání
účtu s doplňkem.

## 8. Mimo rozsah

- Návrhy, které se do profilu zapíší bez potvrzení uživatele.
- Více profilů nebo profil podle složky či odesílatele.
- Doplněk pro eWay a CalDAV.
- Strukturovaná pole kromě rodu.
- Synchronizace profilu mezi počítači.

## 9. Otevřené otázky — rozhodnutí vlastníka (2026-10-21)

| # | Otázka | Rozhodnutí | Kde v plánu |
|---|---|---|---|
| Q1 | Dostane profil i hledání vlastními slovy? | **Jen globální text** (a rod), doplňky účtů ne. | P8 |
| Q2 | Strukturované pole „Píšu v rodě“? | **Ano, jedno pole** nad volným textem. | P13, §5 |
| Q3 | Má asistent umět navrhnout doplnění profilu? | **Ano, hned v první verzi**, vždy s potvrzením uživatele. | P14–P16, §6 |
| Q4 | Doplněk i pro integrace? | **Pošta a Jira**; eWay a CalDAV ne. | P2, §5 |
| Q5 | Profil i pro Claude Desktop a další klienty přes most? | **Ano, s přepínačem** „Posílat profil i aplikacím přes MCP“, výchozí zapnuto. | P9, P11, §5 |
| Q6 | Kde v Předvolbách? | **Skupina O mně v Předvolbách → AI.** | §5 |
| Q7 | Pořadí ve třech klientech? | **macOS první**, pak GTK a Windows, s branami P-B1 až P-B3 (výjimka z pravidla 6 jako u chatu). | §7 |
| Q8 | Limity délky? | **2000 znaků globálně, 500 na účet** (návrh 300). | P2, §3 |

Otázky, které z rozhodnutí vyplynuly (rozhodnuté 2026-10-21):

| # | Otázka | Rozhodnutí | Kde v plánu |
|---|---|---|---|
| Q9 | Kam se zapíše přijatý návrh a co ukáže Upravit…? | **Uložit připojí návrh na konec** profilu (nebo doplňku účtu) jako nový řádek; **Upravit… otevře jen text návrhu**, ne celý profil. | P14, P16 |
| Q10 | Jak ohlásit návrhy z MCP (Claude Desktop)? | **Jen seznam v Předvolbách → AI → O mně a počet čekajících v nabídce panelu**; žádná notifikace ani odznak. | P16, §5 |

Žádná otázka teď otevřená není.
