# Pravidla pošty (filtry) — návrh

Stav: návrh schválený vlastníkem 2026-11-02, implementace na větvi
`claude/sleepy-hawking-7gu54a` (paralelní agenti T1–T4 po kontraktu T0).
Ověření na skutečných systémech (brány B1–B3) až po dokončení všech částí.

## Kontext

Malachi Mail zatím neumí pravidla pošty. Vlastník chce pravidla v duchu Outlooku a Apple Mailu: podmínky → akce. Vyhodnocuje je **lokálně démon** při příjmu nové pošty. Patří k tomu ruční spuštění nad složkou nebo výběrem, MCP nástroje, odchozí akce (přeposlat, automatická odpověď) a „Vytvořit pravidlo ze zprávy…“.

Práce poběží na větvi `claude/sleepy-hawking-7gu54a` (určená pracovní větev této relace). Paralelní agenti (Opus a Sonnet) postaví jádro, macOS, GTK a Windows. Ověření na skutečných systémech udělá vlastník až nakonec. Pravidlo 6 se tím dodrží: všechny tři klienty jsou v jedné práci a brány B1–B3 přijdou až po dokončení.

Tento plán se v prvním commitu zapíše do repozitáře jako `docs/plans/rules.md` ve formátu ostatních plánů (Rozhodnutí R1–Rn, úkoly, brány, Stav ověření).

## Rozhodnutí (R1–R14)

| # | Rozhodnutí |
|---|---|
| R1 | Pravidla vyhodnocuje démon v hooku `afterMailStored` (`backend/internal/core/issue_mail.go:158`). Hook běží synchronně před `notify.newMessage` pro IMAP i Graph. Jen účty `imap`/`graph`, nikdy Jira, CalDAV ani eWay. Server-side pravidla (Graph messageRules, Sieve) se nečtou ani nemění. |
| R2 | Automaticky jen pro zprávy ve složce role `inbox` (pravidlo je pro jeden účet nebo pro všechny poštovní účty). Hook se volá i pro Sent, Drafts, Trash, Junk a Outbox, takže role se filtruje v enginu. |
| R3 | **Nikdy dvakrát a nikdy na starou poštu.** Tabulka `rule_seen (account_id, rfc_message_id)`: každé Message-ID účtu projde automatickými pravidly nejvýš jednou. Ochrání to i před přesunem jiným klientem (delete + nová řádka) a přesunem zpět do Doručené. Zpráva s `internal_date` starším než `rules.enabled_at − 1 den` se přeskočí, takže první synchronizace a backfill pravidla nespouštějí. |
| R4 | Podmínky: Od, Komu, Kopie, Komu nebo kopie, Předmět, Tělo (prostý text, strop 64 KiB), List-Id, hromadná pošta (druh z `internal/bulk`), má přílohy, účet. Operátory: obsahuje, neobsahuje, je, začíná, končí, doména je. Porovnává se bez ohledu na velikost písmen po NFC normalizaci. Regex v 1. verzi není. Spojení podmínek je *všechny* nebo *kterákoli*. |
| R5 | Akce: Přesunout do (složka účtu), Označit jako přečtené, Hvězdička, Koš, Nevyžádaná, Přeposlat na adresu, Automaticky odpovědět textem, Zastavit další pravidla. Pravidla se vyhodnocují v pořadí. Zastavení je příznak pravidla. Akce jednoho pravidla běží v pevném pořadí (přečtené, hvězdička, přeposlat, odpovědět, pak jedno z přesun/nevyžádaná/koš, každý druh nejvýš jednou). Mezi pravidly platí první přesun/koš, další přesuny se ignorují. |
| R6 | Lokální akce jdou existujícími cestami store (`FlagMessages`, `MoveMessages`, `TrashMessages` v `store/ops.go`, junk = přesun do `FolderByRole(RoleJunk)`) a potom `Supervisor.Trigger` a `messagesChanged`. Žádný nový druh operace. Provádějí se synchronně v hooku, aby zpráva přesunutá do koše nebo označená jako přečtená nevyvolala notifikaci. Úkol T1 musí ověřit, že `notifyNew` čte stav až po hooku, případně ho upravit. |
| R7 | Odchozí akce se v hooku jen zařadí do fronty. Zpracuje je worker na účet podle vzoru `crm_follow.go` (`crmEnqueue`, `crmFollowWorker`). Zprávu postaví stejně jako `unsubscribeMailto` (`core/unsubscribe.go:405`) přes `EnqueueOutbox`, `Delivery.Wake` a `outboxChanged`. Přeposlání přiloží originál jako `message/rfc822` přes `storedWhole` (`forward_many.go:107`). Když originál není celý na disku, worker ho stáhne cestou `message.download`. |
| R8 | **Ochrana odchozích akcí (bezpečnost):** nikdy se neodpovídá ani nepřeposílá, když je zpráva `bulk`, má `Auto-Submitted` ≠ `no`, přišla od vlastní adresy (`selfAddresses`, `core/quote.go:67`), od `mailer-daemon`/`postmaster`/`noreply`, leží v Nevyžádané, nebo když pravidlo poslalo víc než 20 zpráv za hodinu. Automatická odpověď jde stejnému odesílateli nejvýš jednou za 4 dny (RFC 3834, tabulka `rule_replies`). Odchozí zprávy nesou `Auto-Submitted: auto-replied` nebo `auto-forwarded` (doplnit `BuildInput.Headers` v `internal/smtp/build.go`). Do `mime/header.go` se kvůli smyčkám přidá `X-Loop`. `docs/security.md` dostane nový oddíl. |
| R9 | Ruční spuštění (`rules.apply` nad složkou nebo `messageIds`) dělá **jen lokální akce**. Odchozí akce se při ručním běhu nikdy neprovedou a výsledek je spočítá jako přeskočené. Ruční běh nepoužívá `rule_seen` ani časový filtr R3. Pracuje v dávkách `MaxMessageIDsPerCall` podle `inBatches` (`folder_actions.go:127`). |
| R10 | Úložiště: nová migrace s dalším volným číslem v okamžiku commitu (dnes 0025, s plánem podpisů se může srazit). Obsahuje tabulky `rules` (id `r_`+hex, position, name, enabled, account_id NULL = všechny, match, conditions JSON, actions JSON, stop, created_at, updated_at, enabled_at), `rule_seen`, `rule_replies` a `rule_stats` (počet použití, poslední použití, poslední chyba). Bez triggerů na `messages`. Odebrání účtu maže jeho řádky. Odebraná cílová složka pravidlo vypne a zapíše `error`. |
| R11 | API, kompatibilní rozšíření protokolu 2, nový oddíl `docs/api.md` (§4.21, nebo §4.22, pokud dřív přijdou podpisy). Metody: `rules.list`, `rules.create`, `rules.update`, `rules.delete`, `rules.reorder`, `rules.test` (které uložené zprávy složky by pravidlo zasáhlo: počet a nejvýš 50 souhrnů), `rules.apply`. Notifikace `notify.rulesChanged`. Chyba 1112 `ruleNotFound`. Limity v `pkg/api/rules.go`: 200 pravidel, 20 podmínek, 10 akcí, řetězec 500 B, text odpovědi 8 KiB, jedna adresa přeposlání. |
| R12 | Pravidlo s odchozí akcí se vytvoří nebo zapne jen s `confirmOutgoing: true` v parametrech. Klient se předtím vždy zeptá. Změna adresy přeposlání nebo textu odpovědi potvrzení vyžaduje znovu. |
| R13 | MCP most: `list_rules` je vždy (jen čtení). `create_rule` a `update_rule` jsou za `--allow-modify`; odchozí akce v nich navíc jen s `--allow-send`. Mazání ani `rules.apply` most nenabízí. Řetězce z pravidel procházejí `clean()`. |
| R14 | UI: v Předvolbách nová stránka **Pravidla** za Integracemi. Obsahuje seznam s přepínačem, řazení přetažením a klávesami, Přidat…, Upravit…, Odebrat a řádek statistiky. Editor je list nebo dialog s řádky podmínek a akcí a s tlačítkem *Vyzkoušet* (`rules.test`). Kontextové menu zprávy má *Vytvořit pravidlo ze zprávy…* (předvyplní Od, případně List-Id a Předmět). Kontextové menu složky má *Použít pravidla* s potvrzením a toastem výsledku. Texty jsou jen v Go referenci. |

## Práce: větve a agenti

Na `claude/sleepy-hawking-7gu54a` nejdřív sekvenčně proběhne **T0**. Potom běží **T1–T4 paralelně**, každý agent ve vlastním git worktree (`isolation: worktree`) na podvětvi `rules/<x>`. Agenti se dotýkají disjunktních stromů: `backend/`, `ui/internal/window`+`ui/data/ui`, `macos/`, `windows/`. Do `po/`, `pkg/api` ani `docs/api.md` po T0 nikdo nesahá a nový msgid nikdo nepřidává. Když agent msgid potřebuje, nahlásí ho orchestrátorovi a ten ho doplní do T0 vrstvy. Orchestrátor (já) potom podvětve postupně sloučí, vyřeší konflikty a pushne.

| Úkol | Model | Obsah | Ověření v tomto kontejneru |
|---|---|---|---|
| **T0 kontrakt a reference** | Opus (orchestrátor) | `docs/plans/rules.md`; `pkg/api/rules.go` + `methods.go` + `service.go` (`RulesService`) + `errors.go` (1112) + stub v `rpc/stub.go`; `docs/api.md` nový oddíl + §5 + §2 tabulka chyb; čistá Go reference `ui/internal/rules` (model pravidla, validace formuláře, předvyplnění ze zprávy, souhrn pravidla jako věta, texty, `po_test.go`); `contextmenu` položky `createRule` (MessageItems) a `applyRules` (FolderItems) s testy; `po/POTFILES` a msgidy do `po/malachi.pot` + `po/cs.po` (skriptem jako dřív, `make po` později v kontejneru vlastníka) | `go vet`, `go test ./pkg/api/... ./ui/internal/rules/... ./ui/internal/contextmenu/...` (`TestDocsCoverAllMethods`) |
| **T1 démon a most** | Opus | balíček `internal/rules` (čistý matcher a plánovač akcí s patologickými testy a fuzz cílem, data `backend/testdata/rules`), migrace, `store/rules.go`, `core/rules_service.go` (metody R11), `core/rules_engine.go` (hook R1–R6), `core/rules_outgoing.go` (worker R7–R8), `smtp.BuildInput.Headers`, `rpc/dispatch.go` `registerRules`, notifikace, MCP `tools_rules.go` + instrukce v `main.go`, `docs/security.md`, `docs/architecture.md` §3.x a §7 | `go vet ./...`, `go test ./...` celé, `-race` na `core` filtrem Rules, nezávislá revize (`/code-review` agentem) |
| **T2 GTK** | Sonnet | `window/preferences_rules.go` + stránka `rules_page` v `preferences.blp`, editor `rules_editor.go` + `rules_editor.blp`, ovladač `ui/internal/rulesctl` (čistý, s falešným volajícím, testovaný), napojení kontextových menu v `context_menus.go`, toast výsledku, `notify.rulesChanged`, vývojový krok `MALACHI_GTK_START=prefs-rules`, `rules-editor`, smoke test | `go test ./ui/internal/rulesctl/...`; GTK bez cgo jen `gofmt` a `gopls check` (případně typová kontrola s FakeImportC). Překlad je až v kontejneru vlastníka |
| **T3 macOS** | Opus | `MalachiCore/API/RulesAPI.swift`, `MalachiCore/Rules/*` (port `ui/internal/rules`), `Controllers/RulesController.swift`, `ContextMenu` port, `MalachiMail/Preferences/RulesPaneViewController.swift` (+ záložka v `PreferencesWindowController`), editor jako sheet, položky v `MessageContextMenu.swift` a `FolderSidebarViewController.swift`, testy (`RulesTests`, `APICodingTests+Rules`, počet metod 119→126 v `APICodingTests`), kroky `MALACHI_START=prefs-rules`, `rules-editor` | napsáno bez překladu; `swift build`/`swift test`/`check-strings.py` až na Macu |
| **T4 Windows** | Sonnet | `Malachi.Core/Api/Rules.cs` + `API.cs`, `ApiJsonContext`, `Notifications`, `RpcTimeouts`; `Malachi.Core/Rules/*` (port), `RulesController`, `ContextMenus` port; App `Preferences/RulesPage.xaml(.cs)` + `NavigationViewItem`, `RulesEditorWindow` (modální přes `ModalDialog`), `MessageListPane`/`SidebarPane` kontextová menu; testy `GoContractTests` (nový test + počet 119→126), `ApiRoundTripTests`, `RulesTests`, `ContextMenuTests`; exclusions bez řádků „rules“; `docs/windows-port.md` §11.x | napsáno bez `dotnet`; build, test a lint až Windows agent vlastníka |
| **T5 sloučení a revize** | Opus | sloučit `rules/*` do pracovní větve; nezávislá revize klientů proti referenci (parita textů a akcí); doplnit `CLAUDE.md` (odstavec Pravidla a položka 18 v Pořadí prací) a `docs/plans/rules.md` §Stav ověření s přesným seznamem toho, co čeká | Go testy znovu po sloučení, `git diff --check`, push |

Každý agent dostane: tento plán, cestu ke vzorovým souborům (Kontakty: `ui/internal/contacts`, `contactsctl`, `ContactsPaneViewController`, `ContactsPreferencesPage`; Integrace pro stránku Předvoleb; `contextmenu` pro menu), pravidla z CLAUDE.md (SPDX hlavičky, `SetUseMarkup(false)` / `stringValue` / `TextBlock.Text`, `L10n.T` s klíčem = msgid, žádný nový msgid) a povinnost napsat do předávky, co zbývá sestavit.

## Klíčové soubory k opakovanému použití

- hook `backend/internal/core/issue_mail.go:158` (`afterMailStored`), fronta a worker `core/crm_follow.go`
- store operace `backend/internal/store/ops.go` (`FlagMessages`, `MoveMessages`, `TrashMessages`), dávky `core/folder_actions.go` (`inBatches`)
- odeslání bez konceptu `core/unsubscribe.go` (`unsubscribeMailto`), příloha celé zprávy `core/forward_many.go` (`storedWhole`)
- vlastní adresy `core/quote.go:67` (`selfAddresses`), hromadná pošta `internal/bulk` (`Classify`, `autoSubmitted`)
- MCP vzor `cmd/malachi-mcp/tools_calendar.go` (`registerCalendarModifyTools`), anotace `format.go`
- vzor migrace `store/migrations/0021_crm_links.sql`

## Ověření

- **V tomto kontejneru (T0–T5):** démon a Go reference kompletně (`go vet`, `go test ./...` v `backend/` a v čistých balíčcích `ui/`), závod u enginu, fuzz cíl krátce. Testy enginu: pravidlo nespustí backfill, neopakuje se po přesunu zpět, lokální akce potlačí notifikaci, odchozí akce respektují R8 (bulk, Auto-Submitted, vlastní adresa, limit 4 dní a 20/h), ruční běh odchozí akce přeskočí, odebraná složka pravidlo vypne.
- **Brány vlastníka na skutečných systémech (až po dokončení všeho):**
  - **B1 macOS:** `swift build`, `swift test`, `check-strings.py`. Ručně nad kopií dat (`MALACHI_DATA_DIR` + vlastní `MALACHI_SOCKET`; migrace je nevratná): vytvořit pravidlo ze zprávy, přesun nové pošty, Použít pravidla na složku, automatická odpověď na testovací adresu.
  - **B2 GTK:** `gtk.sh make build`, `test-ui`, smoke, `make po`, `make lint`, totéž ručně v XQuartz.
  - **B3 Windows:** `build.ps1 app`/`test`/`lint`, UI smoke, ruční průchod.
- Odchozí akce zkoušet jen mezi vlastními testovacími adresami vlastníka.

## Odpovědi vlastníka (2026-11-02)

1. Pravidla = pravidla pošty (filtry), ne pravidla nástěnky.
2. Vyhodnocuje lokálně démon, ne server (Graph messageRules / Sieve).
3. První verze: ruční spuštění nad složkou, MCP nástroje, odchozí akce
   (přeposlat, automatická odpověď) a návrh pravidla z vybrané zprávy.
4. Implementace paralelními agenty (Opus / Sonnet) pro jádro, macOS, GTK
   a Windows; ověření na reálných systémech až po dokončení.

## Stav ověření

- **T0 (2026-11-02):** kontrakt `backend/pkg/api/rules.go` (+ `methods.go`,
  `service.go`, `errors.go` 1112, stub v `rpc/stub.go`, `registerRules`
  v `rpc/dispatch.go`, zástupný `core/rules_service.go`, který T1
  nahradí), `docs/api.md` §4.21, §5 a §7; Go reference `ui/internal/rules`
  (`rules.go` model, validace, předvyplnění; `text.go` všechny texty)
  a položky `contextmenu` `createRule` a `applyRules`; 125 nových msgid
  v `po/malachi.pot` a česky v `po/cs.po` zapsané skriptem bez xgettext
  (`make po` je přerovná a přečísluje). Ověřeno: `go vet`, `go test`
  `pkg/api`, `rpc`, `ui/internal/rules`, `contextmenu`.
