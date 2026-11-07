# Asistent přes API — podklady pro plán

Stav: větev `feat/ai-api`; průzkum 2026-11-07, etapy E0 (démon) a E1
(Go reference + GTK) napsané a otestované týž den (§13, §14), brána B2
vlastníka čeká; E2 a E3 potom. Odpovědi vlastníka na Q1–Q19 jsou v §9; z nich
vycházejí rozhodnutí D1–D22 v §5; plán etapy E0+E1 je v §12. Dokument je podklad pro plán, ne plán
sám: návrhy v §6 a §7 jsou tvar, který rozhodnutí dávají, a změní se
podle toho, co ukáže první etapa.

Podklady: [`mcp.md`](../mcp.md) (most, panel, triage, Navrhnout odpověď),
[`chatgpt-integration.md`](../chatgpt-integration.md) (precedens druhého
poskytovatele), [`security.md`](../security.md) §6, §7, §10,
[`mac-app-store.md`](mac-app-store.md) §3.3 a §8.3 (co sandbox
nedovolí), [`api.md`](../api.md) §4.1 (účty), §4.13 (`board.run*`),
§4.18 (`assistant.*`), `ui/internal/assistant`, `ui/internal/assistantpanel`
(šev `Provider`/`Session`), `ui/internal/chatgpt`, `ui/internal/integrations`,
`backend/cmd/malachi-mcp`, `backend/internal/auth`, dokumentace API
Anthropic a OpenAI a serverů Ollama, llama.cpp, LM Studio a vLLM (§4).

## 1. Co se mění

Dnes umí asistent v aplikaci jen to, co spustí z disku uživatele: jeho
Claude Code (`claude -p`, stream-json) nebo Codex (`codex app-server`),
oba s mostem `malachi-mcp` jako zdrojem nástrojů nad poštou. Nově půjde
asistenta napojit i **přes API modelu**: klíč k Anthropic API, klíč
k OpenAI API, nebo libovolný server s OpenAI-kompatibilním rozhraním
(Ollama, llama.cpp, LM Studio, vLLM, brány jako OpenRouter). Všechny
dnešní funkce asistenta (panel, přepis a psaní v okně Nová zpráva, hledání
vlastními slovy, triage nástěnky, Navrhnout odpověď) pak běží nad
zvoleným poskytovatelem (Q1).

Tři viditelné změny:

1. **Poskytovatelé AI jsou integrace.** Předvolby → Integrace dostanou
   katalog AI: Claude Code / Claude Desktop (lokální, s Registrovat
   v Claude), ChatGPT (Codex, lokální), Anthropic API, OpenAI API,
   OpenAI-kompatibilní server. Připojené AI integrace jsou vedle Jiry,
   kalendářů a CRM. Předvolby → AI pak nesou jen nastavení už
   nakonfigurovaného přístupu: který poskytovatel asistenta pohání, model,
   souhlasy, Nástěnka, O mně (Q7).
2. **Smyčka agenta běží v démonu.** Volání API modelu, streamování
   odpovědi a nástroje nad poštou dělá `malachid`; klienti posílají
   dotazy a ukazují události (Q5). Klíče neopustí démon (Q6), souhlas
   s odesláním pošty hlídá démon (Q9), spotřebu tokenů eviduje démon (Q12).
3. **Verze z Mac App Store dostane asistenta v aplikaci.** Sandbox
   nedovolí spustit uživatelův `claude` ani `codex`, ale démon v něm má
   síť i klíčenku, takže API cesta funguje bez nové výjimky. Lokální
   integrace zůstanou v katalogu vidět a Přidat… u nich jen vysvětlí, že
   v této verzi nejdou a že je buď verze z webu, nebo AI přes API (Q11,
   poznámka vlastníka 2026-11-07).

## 2. Proč

- **Mac App Store** (`mac-app-store.md` §3.3): proces spuštěný ze sandboxu
  sandbox dědí, binárku v `~/.local/bin` nepřečte a k `~/.claude` ani
  k položkám klíčenky Claude Code se nedostane. Dnešní App Store build
  proto vypíná cíl „V aplikaci“, ChatGPT, triage, Navrhnout odpověď
  i přihlášení Claude Code (`Distribution.allowsLocalAgents`). Démon
  v něm ale běží s `app-sandbox` + `inherit`, dědí `network.client`
  a klíčenku přes `malachi-keychain` (§8.2 tamtéž), takže HTTPS na API
  modelu z démona je v sandboxu obyčejné spojení jako IMAP nebo Graph.
- **Uživatelé bez Claude Code a Codexu**: klíč k API nebo server doma je
  dnes jediná cesta pro Linux bez Claude Desktop, pro firmy s vlastním
  LLM a pro každého, kdo nechce instalovat agentní CLI.
- **Jedna implementace** (pravidlo 1 a 4): HTTP klienti modelů a smyčka
  s nástroji v Go v démonu místo tří portů v GTK, Swiftu a C#. Codex
  dnes dělá HTTPS volání v aplikaci samé (brána loopbacku vkládá token)
  třikrát; API cesta tenhle vzor neopakuje.

## 3. Dnešní stav (fakta z průzkumu)

### 3.1 Cíl a poskytovatel

Dvě nezávislé volby: **cíl** (`assistant-target`: `desktop` / `code` /
`app`, kam Asistent otevře Claude; „V aplikaci“ je panel) a
**poskytovatel** panelu (`assistant-provider`: `claude` / `chatgpt`, platí
jen pro cíl `app`). Claude Code není `Provider`: nil znamená zavedenou
cestu `claude -p`; `chatgpt.Provider` je jediná implementace rozhraní.
Klíče gschema a jejich zrcadla v Swiftu a C#: `assistant-menu`,
`assistant-target`, `assistant-model` (sonnet/haiku/opus),
`assistant-claude-path`, `assistant-consent`, `assistant-provider`,
`assistant-codex-path`, `assistant-chatgpt-model`,
`assistant-chatgpt-consent-version`, `board-triage-consent`,
`board-triage-model`, `board-triage-chatgpt-model`,
`board-triage-chatgpt-consent-version`
(`data/io.github.schotek.Malachi.gschema.xml`, `ui/internal/settings/store.go`).

### 3.2 Šev `Provider` / `Session` a události

Všechny tři klienty mají tentýž šev, do kterého se dnes zapojuje Codex
(Go `ui/internal/assistantpanel/provider.go`, Swift
`MalachiCore/ChatGPT/AssistantProvider.swift`, C#
`Malachi.Core/Assistants/IAssistantProvider.cs`):

```go
type Provider interface { Model() string; HasConsent() bool; AcceptConsent(); Open(context.Context, SessionSpec) (Session, error) }
type SessionSpec struct { SystemPrompt, JSONSchema, ModelID string; Tools *Tools; Timeout time.Duration }
type Session interface { Running() bool; Submit(context.Context, string) error; Terminate(); Completion() <-chan struct{}; SetHandlers(func([]assistant.Event), func(Exit)) }
```

Panel (`Controller`), jednorázové požadavky (`Request`: přepis, psaní,
hledání), triage (`boardtriage`) i Navrhnout odpověď (`boardreply`) jdou
tudy, jakmile továrna vrátí nenulového poskytovatele. Poskytovatel musí
vydávat normalizované události `assistant.Event` (`events.go`):
`EventInit{BridgeConnected, Tools}` na začátku tahu s nástroji,
`EventTextDelta`, `EventText`, `EventToolUse{Tool, ToolUseID}`,
`EventToolResult{ToolUseID, IsError, ResultText}` s **přesným textem
výsledku mostu** (z něj čtou `ParseDraftResult`, `ParseCalendarResult`,
`ParseProposalResult` a `TriageAnnotatedCase`), `EventResult{Success,
ResultText, Structured}`, `EventFailure` a spotřebu jako
`EventOther{MessageID, Usage, UsageFinal}`. Chybové kódy poskytovatele
mapuje `ProviderFailure` (`codex_*`, `chatgpt_*`) na `FailureNotFound`,
`FailureNotSignedIn`, `FailureLimit`, `FailureStopped`; nové kódy API
cesty se do něj přidají.

### 3.3 Funkce a jejich nástroje

| Funkce | Nástroje | Argumenty mostu | Režim | Model |
|---|---|---|---|---|
| Panel | `assistant.AllowedTools` (list_accounts, list_folders, list_messages, search_messages, read_message, get_attachment, create_draft, kalendář ×5, get_user_profile, propose_profile_note) | `--in-app` | rozhovor, jeden tah = jedna zpráva | `assistant-model` |
| Přepis v Nové zprávě | žádné | bez mostu | jednorázový | `assistant-model` |
| Psaní v Nové zprávě | žádné | bez mostu | jednorázový, JSON vstup | `assistant-model` |
| Hledání vlastními slovy | žádné | bez mostu | jednorázový, `--json-schema` (`SearchSchema`) | `assistant-model` |
| Triage nástěnky | `TriageToolsWith(drafts)` (čtení + create_draft u ručního běhu + list_triage_queue, annotate_case, add_commitment) | `--in-app --allow-triage --triage-run <id> --triage-max <n>` | jednorázový s nástroji, 15 min | `board-triage-model` |
| Navrhnout odpověď | read_message, list_messages, create_draft | `--in-app --reply-only <messageId>` | jednorázový, 130 s | `assistant-model` |

Systémové prompty, pokyny tahů, profil (`WithProfile`), schéma hledání
a povolené nástroje jsou v `ui/internal/assistant` a portované 1:1 do
`MalachiCore` a `Malachi.Core`; most je ve všech případech týž
`malachi-mcp` (`--socket <socket> --in-app …`). Text, který model napíše,
je vždy nedůvěryhodný (Markdown podmnožina, odkazy přes potvrzení).

### 3.4 Most `malachi-mcp`

- Úrovně oprávnění (`mcp.md` Permission tiers): výchozí čtení + koncepty;
  `--allow-modify`, `--allow-send`, `--allow-triage`; `--triage-run`
  a `--reply-only` zužují (bez kalendáře, kontaktů, pravidel
  a `propose_profile_note`; `create_draft` jen odpověď případu / jedna
  odpověď). Nepovolený nástroj se nezaregistruje.
- Ohrada: `clean()`, `oneLine()`, nonce 12 hex, `--- BEGIN/END UNTRUSTED
  MAIL CONTENT <nonce> ---` (`format.go`); dokumenty jen v podřízeném
  procesu `__extract` (PDFium ve wazero), nikdy v démonu
  (`security.md` §10).
- Celý kód nástrojů je balíček `main` (`cmd/malachi-mcp`), importovat
  nejde. Most lze ale spustit jako MCP podproces přes
  `mcp.CommandTransport` z `github.com/modelcontextprotocol/go-sdk`
  (v1.7.0, už v `backend/go.mod`); `tools/list` vrací jméno, popis
  a JSON schéma vstupu, tedy přesně to, co definice nástroje pro
  Anthropic i OpenAI potřebuje. Tak dnes pracuje Codex (aplikace spustí
  most jako sourozence a proxy `tools/call`).
- `clientSource(req)` čte `initialize.clientInfo.name` MCP klienta jako
  `source` běhu nástěnky.

### 3.5 Démon

- **Klíčenka** (`internal/auth`): `Keyring.Get/Set/Delete(account, key,
  value)`, backendy Secret Service, `MALACHI_KEYRING=helper`
  (`malachi-keychain`, `malachi-credentials`; `account` i `key` jen
  `^[A-Za-z0-9._-]{1,128}$`), `none`. Každé tajemství je vázané na účet;
  píší je `account.add` / `account.update` (`credentials.password`,
  write-only), mažou `account.remove` a odhlášení. Tajemství bez účtu
  dnes neexistuje.
- **HTTP klienti** (`update`, `remoteimg`, `eway`, `jira`, `graph`,
  `caldav`, `oneclick`): timeouty 15–60 s, stropy těl 64 KiB – 32 MiB,
  bez přesměrování nebo jen https→https, proxy z prostředí, pevný
  `User-Agent`, do logu třída chyby, nikdy tělo ani token; CalDAV je
  jediný s pinem certifikátu a jediný, který dovolí http (jen loopback);
  `oneclick.Guard` naopak loopback a privátní sítě zakazuje. Kontrola
  verzí je „jediný požadavek, který démon dělá sám a ne na server účtu“
  (`security.md` §7.4) — volání API modelu bude druhý.
- **Konfigurace bez migrace**: `meta` (`assistant.profile`,
  `board.prefs`, `update.prefs`), `preferences` (`config.get/set`, pevná
  struktura), `config.toml` (démon ho nikdy nepíše). Účty: tabulka
  `accounts` se sloupcem `kind` bez omezení; `AccountConfig` je sjednocení
  s podkonfigurací podle druhu (`Jira`, `CalDAV`, `Eway`), eWay účet nemá
  e-mail a má jen capability `crm` (nikdy v Poště), `core/dispatch.go`
  má tabulku supervisorů podle druhu (`kindOrder`).
- **Notifikace** jdou všem ověřeným spojením (`internal/rpc/server.go`),
  cílené doručení jednomu spojení neexistuje; řádek 32 MiB; požadavek
  bez vlastního deadline.
- **Běhy nástěnky**: `board.runStart {trigger, source}` → `runId`,
  `board.runEnd {runId, error?, usage?}` (`BoardUsage` ve tvaru čítačů
  Anthropic), `usage24h` z `board_runs`; denní strop automatické triage
  démon jen ukládá, vynucuje ho plánovač klienta.
- API: `ProtocolVersion` 2, poslední kódy chyb 1007 / 1112 / 1206 /
  1308 / 1401 / 1510; §4.18 `assistant` má dnes jen profil; nové metody,
  pole a notifikace jsou kompatibilní (§6 `api.md`).

### 3.6 Předvolby dnes

Předvolby → AI (všechny tři klienty, pořadí): O mně (+ návrhy
asistenta), MCP (Registrovat v Claude, čekající restart Claude Desktop),
Asistent (In-app provider, Zobrazovat nabídku Asistent, Otevřít v,
Claude Code s cestou a přihlášením, Model), ChatGPT (Codex, Účet, Model,
Spravovat využití), Nástěnka (souhlas, Model, Třídit tyto účty,
automaticky, interval, denní strop, Stav, Tokeny za 24 h). V App Store
buildu navíc nahoře skupina „Další funkce asistenta“ s *Více…*
(`WebsiteFeaturesDialog`) a ve stavovém pruhu odkaz *Více AI funkcí*.

Předvolby → Integrace: Připojené služby = účty démona, které nejsou
poštovní (`integrations.Split`, `RowFor`, přepínač, Upravit, Odebrat,
řazení přes `account.reorder`/`FullOrder`); Dostupné služby = statický
katalog `integrations.Catalog` (Jira, Kalendář CalDAV, eWay-CRM) s Přidat…
podle `api.AccountKind`. Řádek bez `AccountID` dnes model neumí.

### 3.7 App Store build

`Distribution.current` (`direct` / `appstore` z `Info.plist`
`MalachiDistribution`, `MalachiCore/Daemon/Distribution.swift`),
`allowsLocalAgents` a `handsFilesToClaude` jen pro `direct`.
V `appstore`: `ClaudeCodeLocator.locate()` nil, `CodexProvider.executable`
nil, cíl `app` se čte jako Claude Desktop a nezapisuje, poskytovatel
`chatgpt` jako Claude, triage a Navrhnout odpověď nedostupné, restart
Claude Desktop vypnutý. Most je pomocný bundle `Contents/Helpers/Malachi
Mail MCP.app` s vlastním sandboxem (spouští ho Claude), vedle něj
`malachi-mcp-worker` s `inherit`; **proces s vlastním sandboxem nesmí být
potomkem sandboxované aplikace** (padá v `_libsecinit_appsandbox`), proto
registraci dělá LaunchServices se souborem žádosti. Démon
i `malachi-keychain` běží s `inherit`. Windows nemá žádnou bránu
distribuce (MSIX je `runFullTrust`).

### 3.8 Precedens ChatGPT a co tento plán mění

`chatgpt-integration.md` zavedl provider-neutrální kontrakty v klientech
(§3) a rozhodl, že token OpenAI **není** přihlašovací údaj účtu a nepatří
do démona, jeho databáze ani protokolu helperu klíčenky (§2); zároveň
označil „API-key billing“ za „separately labeled, optional future mode“.
Tahle práce je ten režim a rozhodnutí Q6 ho obrací: klíč API je
přihlašovací údaj účtu druhu `ai` v klíčence démona, protože ho používá
démon, ne aplikace. Věta v `security.md` §10.1, že aplikace „never reads,
stores or asks for a credential“, platí dál pro Claude Code; pro účty
`ai` dostane §10 nový oddíl (§6.7). Grant „Sign in with ChatGPT“ zůstává
v klientech, jak je (D15).

## 4. Průzkum API poskytovatelů (stav 2026-11-07)

| | Anthropic | OpenAI | OpenAI-kompatibilní servery |
|---|---|---|---|
| Rozhraní | Messages API `POST /v1/messages` | Responses API `POST /v1/responses` (doporučené; GPT‑6 Astra a GPT‑6.1 Sol vyžadují Responses pro nástroje, od GPT‑5.4 jde Chat Completions s nástroji jen s `reasoning_effort: none`) | Chat Completions `POST /v1/chat/completions` (nejmenší společný jmenovatel; `/v1/responses` mají jen některé) |
| Hlavičky | `Authorization: Bearer` (nebo `x-api-key`), `anthropic-version` povinná, `anthropic-workspace-id` u klíčů bez workspace | `Authorization: Bearer`, volitelně `OpenAI-Organization`, `OpenAI-Project` | `Authorization: Bearer` (Ollama klíč ignoruje; llama.cpp `--api-key`, LM Studio volitelný token, vLLM `--api-key`) |
| Nástroje | `tools[{name, description, input_schema, strict}]`, blok `tool_use`, odpověď `tool_result` v `user` zprávě; `strict: true` zaručí vstup podle schématu | Responses: `tools[{type:"function", name, parameters, strict}]`, položky `function_call` → `function_call_output`; Chat: `tools[{type:"function", function:{…}}]`, `tool_calls` → role `tool` | Chat Completions tvar; Ollama bez `tool_choice` a model bez šablony nástrojů → 400; llama.cpp `--jinja` (dnes výchozí); LM Studio „native“ vs. prompt fallback; vLLM `--enable-auto-tool-choice --tool-call-parser …` |
| Streamování | SSE: `message_start`, `content_block_start/delta/stop` (`text_delta`, `input_json_delta`), `message_delta` (kumulativní `usage.output_tokens`), `message_stop`, `ping`, `error` | Responses: typované události (`response.output_text.delta`, `response.function_call_arguments.delta`, `response.completed`), bez `[DONE]`; Chat: `chat.completion.chunk` s `choices[].delta`, `data: [DONE]`, `stream_options.include_usage` | Chat Completions tvar s `[DONE]`; `include_usage` ne všude |
| Strukturovaný výstup | `output_config.format` (JSON schéma) nebo nástroj se `strict` | `text.format` / `response_format` `json_schema` se `strict: true` (kořen objekt, `additionalProperties: false`, vše `required`) | `response_format: json_schema` (llama.cpp gramatika, Ollama ano, LM Studio ano); jinak pokyn v promptu + validace |
| Seznam modelů | `GET /v1/models` (+ `capabilities`, `max_input_tokens`) | `GET /v1/models` | `GET /v1/models` (Ollama vrací stažené modely, llama.cpp právě jeden) |
| Ověření klíče / adresy | `GET /v1/models`: 401 `authentication_error`, 403 `permission_error`, 400 bez workspace | `GET /v1/models`: 401 „Incorrect API key“, 429 kvóta | 200 i bez klíče (Ollama); 401 u llama.cpp/vLLM se špatným klíčem; llama.cpp `/health` veřejný |
| Spotřeba | `usage.input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens` | `usage.input_tokens/output_tokens` (Responses), `prompt_tokens/completion_tokens` (Chat), `cached_tokens` v detailech | podle serveru, často jen prompt/completion |
| Klíče | `sk-ant-api…` | `sk-proj-`, `sk-svcacct-` (ověřovat jen `sk-`, délka se mění) | libovolné |
| Modely a ceny ($/MTok vstup/výstup) | Fable 5.1 10/50, Opus 5.5 4/20, Sonnet 5.5 2/10, Haiku 5.5 0,10/0,50 | gpt‑6‑astra 10/50, gpt‑6.1‑sol 2/10, gpt‑6.1‑terra 1/5, gpt‑6‑luna 0,10/0,50, gpt‑5.4‑mini 0,75/4,50 | zdarma / podle brány |
| Limity | 429 `rate_limit_error`, 529 `overloaded_error`, `request-id` | hlavičky `x-ratelimit-*`, 429 s `Retry-After` | podle serveru |

Podmínky: Anthropic Commercial Terms a Usage Policy (nová verze od
2026-11-12) dovolují vlastní klíč v aplikaci třetí strany, zakazují
„unauthorized resale or proxying … including routing requests through
consumer subscriptions“ (týká se dnešního cíle „V aplikaci“ nad Claude
Code, viz §11). OpenAI Business Terms zakazují předávat klíče třetím
stranám; klíč, který zůstane na zařízení uživatele a aplikace ho
nesdružuje ani neproxuje, zakázaný není. Oba poskytovatelé u API dat
výchozím způsobem netrénují, což je jiný režim než spotřebitelské
předplatné (pro text souhlasu).

Lokální servery: Ollama `http://localhost:11434/v1` (váže se na
loopback, `OLLAMA_HOST=0.0.0.0` ho otevře, nativní API je `/api/chat`),
llama.cpp `llama-server` `http://127.0.0.1:9931` (starší 8080), LM
Studio `http://localhost:1234/v1`, vLLM `http://localhost:8000/v1`;
brány OpenRouter `https://openrouter.ai/api/v1`, Groq
`https://api.groq.com/openai/v1`, Mistral `https://api.mistral.ai/v1`,
Together `https://api.together.ai/v1` mají týž tvar. Anthropic má
kompatibilní `/v1/chat/completions`, ale „not … production-ready“
(bez cache, `strict` ignorován, `tool_choice` named 400) — nepoužívat.
**Malé lokální modely** (pod ~8B) volají nástroje často špatně nebo
vůbec; obvyklá obrana je validace a oprava výstupu, větší model, nebo
dotaz bez nástrojů s obsahem v promptu. Jednorázové funkce (přepis,
psaní, hledání) nástroje nepotřebují a běží na čemkoli; panel, triage
a Navrhnout odpověď nástroje vyžadují.

Go SDK (`anthropic-sdk-go` v2, `openai-go` v3, oba Apache-2.0) jsou
licenčně přípustné, ale tahají AWS a Azure moduly a mají vlastní retry
a timeout politiku; obě API jsou JSON + SSE nad `net/http` (Q8).

## 5. Rozhodnutí (D1–D22)

| # | Rozhodnutí | Proč |
|---|---|---|
| D1 | Režim API pokrývá všechny dnešní funkce asistenta (Q1). | Jedna cesta pro MAS i pro uživatele bez agentních CLI. |
| D2 | **Smyčka agenta běží v démonu** (Q5): `internal/llm` (klienti API), `internal/agent` (smyčka, nástroje), metody `assistant.session*`, cílená notifikace událostí. | Pravidlo 1 a 4; klíče neopustí démon; MAS bez výjimek (démon má síť); jedna implementace místo tří; později může démon triage plánovat sám. |
| D3 | **Poskytovatel je účet druhu `ai`** (Q6): `AccountConfig.AI{provider, baseUrl, model, boardModel, consent}`, capability `assistant`, klíč jako `credentials.password`; realm = hostitel `baseUrl` (jako Jira), e-mail prázdný (jako eWay); žádný supervisor; nikdy v Poště. | Integrace ho vypíše automaticky (`ShowsInIntegrations`), zapnutí, odebrání, řazení i mazání klíče z klíčenky jde přes `account.*` zdarma; bez migrace (`kind` bez omezení, eWay precedens). |
| D4 | **Vlastní tenký HTTP klient** (Q8), standardní knihovna: Messages API, Responses API, Chat Completions + sdílená čtečka SSE; žádná nová závislost. | Stejná pravidla jako ostatní klienti démona (stropy, bez přesměrování, nic do logu); SDK by přinesla cizí politiku a desítky modulů. |
| D5 | Nástroje smyčky dává **most spuštěný démonem** jako MCP podproces (`mcp.CommandTransport`, `--socket <vlastní socket> --in-app <argumenty úrovně>`), `tools/list` → definice nástrojů, `tools/call` → výsledek beze změny; cestu k mostu předá klient v `sessionStart` (`bridge`, platformně neutrální parametr jako dnes `Tools.Bridge`). In-process volání (přesun handlerů z `main` do importovatelného balíčku) je možná pozdější konsolidace, ne podmínka. | Nulová změna sémantiky nástrojů, úrovní, ohrady, limitů na proces a izolace `__extract`; přesně vzor Codexu, jen o patro níž. V MAS je to kopie s `inherit` (`malachi-mcp-worker`), kterou klient zná. |
| D6 | **Co čte model, dává démon** (revize 2026-11-07, Q19): systémové prompty panelu, triage, Navrhnout odpověď, přepisu, psaní a hledání, postup triage, preambule kontextu, zprávy tahů jednorázových požadavků, schéma hledání, seznamy povolených nástrojů, tvary argumentů mostu a připojení profilu se stěhují z `ui/internal/assistant` do `backend/internal/assistant`. Relace API dostává jen `purpose` a vstup (`PromptInput`: text, kontext s id, pole přepisu/psaní/hledání/odpovědi/triage); lokální cesty (Claude Code, Codex) si totéž berou metodou `assistant.prompt`. **Co čte uživatel, zůstává v klientech**: otázky akcí a předání odkazem, kalendářové dotazy, popisky, souhlasy, chybové věty. Jazyk odpovědi je parametr od klienta. | Jeden zdroj místo tří portů; profil leží v démonu; relace API nemůže od klienta dostat cizí prompt; `sessionStart` se navrhne kolem `purpose` hned, ne až po druhé změně API. Prompty jsou anglické pokyny modelu, ne texty pro uživatele, jazyková neutralita backendu drží. |
| D7 | **Souhlas v démonu, per účet** (Q9): `AI.consent {version}`; `sessionStart` bez souhlasu odmítne `assistantConsentRequired`; otázku ukazuje klient (text podle druhu: Anthropic, OpenAI, vlastní server), verzi textu nese `api.AssistantConsentVersion`. | Jeden souhlas pro všechny tři klienty a vynucený tam, kde pošta odchází. |
| D8 | `baseUrl` smí být **http kamkoli** (Q10); průvodce i řádek účtu ukážou varování u http mimo loopback („klíč i pošta jdou po síti nezašifrované“); https s pinem certifikátu jako CalDAV (`certificateSha256`, jen hostitel `baseUrl`). | Rozhodnutí vlastníka; domácí server bez TLS je běžný případ. |
| D9 | **Spotřebu eviduje démon** (Q12): tokeny per účet a den (tabulka `assistant_usage`, migrace), Integrace a AI ukážou 24 h; bez stropu, automatická triage má dnešní denní limit případů. | Uživatel platí za tokeny; démon vidí každou odpověď. |
| D10 | Pro `openai` Responses API, pro `openaiCompatible` Chat Completions, pro `anthropic` Messages API. | Responses je u OpenAI podmínkou nástrojů u nových modelů; Chat Completions je to, co mají všechny servery. |
| D11 | Strukturovaný výstup (hledání vlastními slovy): Anthropic `output_config.format`, OpenAI `text.format` `json_schema` se `strict`, kompatibilní `response_format: json_schema` a při 400 pokyn v promptu + validace `ParseSearchQuery`. | Totéž schéma `SearchSchema` ve všech třech. |
| D12 | Streamování ke klientovi: `notify.assistantEvent {sessionId, event}` **jen spojení, které relaci založilo** (rozšíření `internal/rpc` o cílenou notifikaci); pád spojení ukončí relaci. | Jinak by přepis četl každý připojený klient včetně mostů. |
| D13 | Tvar událostí v API zrcadlí `assistant.Event` (init, textDelta, text, toolUse, toolResult, result, failure, usage), takže klientský adaptér je mapování 1:1. | Panel, `Request`, triage i Navrhnout odpověď zůstávají beze změny. |
| D14 | Výběr poskytovatele asistenta: `assistant-provider` přestane být enum a bude řetězec `claude` / `chatgpt` / `<accountId účtu ai>`; nástěnka používá týž poskytovatel (model zvlášť: `AI.boardModel`). Neznámý nebo odebraný účet = `claude`. | Jeden klíč, tři klienty, bez nového výčtu při každém poskytovateli. |
| D15 | **Všechno do Integrací naráz** (Q7): katalog AI má pět položek; lokální Claude Code / Claude Desktop (Registrovat v Claude, cesta, přihlášení, restart Claude Desktop) a ChatGPT (Codex, Continue with ChatGPT) jsou **klientské řádky** Integrací (stav z klienta, ne účty démona), účty `ai` řádky démona. | Lokální integrace mají stav na stroji klienta (grant SIWC v klíčence aplikace, cesta k CLI); stěhovat je do démona by rozbilo hotové; stránka jen dostane řádek bez `AccountID`. |
| D16 | Předvolby → AI po změně: O mně, Asistent (Asistent používá, Zobrazovat nabídku, Otevřít v, Model), Nástěnka (jako dnes). Skupina MCP a ChatGPT z AI zmizí; „Otevřít v“ zůstává na AI (Q15). | Zadání vlastníka: na AI jen nastavení už nakonfigurovaného přístupu; cíl je chování nabídky Asistent, ne připojení. |
| D17 | **MAS** (Q11): skupina „Další funkce asistenta“ i odkaz *Více AI funkcí* ve stavovém pruhu zmizí; lokální položky katalogu zůstanou vidět a Přidat… otevře modal (přepoužitý `WebsiteFeaturesDialog` s novým textem: verze z webu, nebo AI přes API). Cíl „V aplikaci“ je v MAS dostupný právě tehdy, když existuje zapnutý účet `ai`; `allowsLocalAgents` zůstává false. | Rozhodnutí vlastníka 2026-11-07. |
| D18 | Model: `AI.model` je výchozí model účtu (z `GET /v1/models` s ručním zápisem jako záloha), `AI.boardModel` volitelný pro triage; řádky Model na AI zapisují do účtu přes `account.update`. | Sdílené mezi klienty; katalog je nápověda, ne důkaz oprávnění. |
| D19 | `board.runStart` dostane `source` `api:<provider>`; démon, který relaci triage běží sám, zapíše `usage` do `board.runEnd` ze svých čítačů (klient ho nepředává). | Přesná spotřeba i u zastaveného běhu. |
| D20 | Plánovač automatické triage zůstává v klientech; přesun do démona (běh bez okna, vynucení denního stropu démonem) je **samostatná etapa po E3** (Q16). | Menší změna teď; démon má dnes jen úložiště preferencí. |
| D21 | Model bez podpory nástrojů (400 kompatibilního serveru): panel, triage a Navrhnout odpověď skončí srozumitelnou chybou `assistantToolsUnsupported`; jednorázové funkce fungují. Žádný tichý fallback bez nástrojů. | Nástroje jsou jediná cesta k poště; bez nich by model odpovídal naslepo. |
| D22 | Dokumenty se mění s kódem: `api.md` §4.1 (`ai`), §4.18, §2 (kódy), §5; `security.md` nový §7.6 (klient API modelů) a §10.4 (agent démona); `mcp.md` oddíl o relacích démona; `chatgpt-integration.md` §2 poznámka o revizi; `macos/README.md` tabulka (MAS); `windows/parity-exclusions.txt`. | Pravidlo 5 a 7. |

## 6. Návrh démona

### 6.1 Účet `kind: ai`

```jsonc
// AccountConfig.ai (jen kind "ai")
{
  "provider": "anthropic" | "openai" | "openaiCompatible",
  "baseUrl": "http://192.168.1.20:11434/v1",   // povinné u openaiCompatible, jinak prázdné (pevné https://api.anthropic.com, https://api.openai.com/v1)
  "model": "claude-opus-5-5",                   // výchozí model asistenta
  "boardModel": "",                             // volitelný model triage, prázdné = model
  "consent": { "version": 1, "acceptedAt": "…" }, // D7; null = bez souhlasu
  "certificateSha256": ""                       // jen https u openaiCompatible, jako ServerConfig
}
```

- `account.add {config, credentials:{password}}`: `password` je klíč API
  (u `openaiCompatible` nepovinný); uložený pod `auth.KeyPassword`; jde
  **jen** na hostitele `baseUrl` (jako token Jiry na jeho site).
- `account.test` pro `ai` = `GET /v1/models` s klíčem: výsledek
  `{models:[{id, displayName?}]}` (ověření klíče i adresy, §4); chyby
  `authFailed` (401), `invalidArgument` (adresa), `serverError`,
  `tlsError` s `TLSErrorData` (pin jako u IMAP).
- `Account.capabilities = ["assistant"]`, `Account.state` z posledního
  testu / relace (`connected`, `authFailed`, `error`); Pošta, Nástěnka
  a Kalendář účet ignorují (`capabilities.InMail`, `ShowsInAccounts`
  už dnes filtrují podle druhu).
- `account.remove` smaže klíč z klíčenky a řádky spotřeby; klient,
  který měl účet v `assistant-provider`, spadne na `claude` (D14).
- Unikátnost (email, realm): e-mail prázdný, realm
  `ai:<provider>:<host>:<id>`, takže víc účtů téhož poskytovatele je
  dovoleno (dva klíče Anthropic, dvě Ollamy) a rozlišují je názvy
  (Q13); `store.RealmOf` dostane větev pro `ai`.

### 6.2 `internal/llm` — klienti API modelů

```go
type Provider interface {
    Stream(ctx context.Context, req Request) (Stream, error)   // jeden tah modelu
    Models(ctx context.Context) ([]Model, error)               // GET /v1/models
}
type Request struct { Model string; System string; Messages []Message; Tools []Tool; JSONSchema json.RawMessage; MaxOutputTokens int }
type Message struct { Role string; Blocks []Block }            // text | toolUse{id,name,input} | toolResult{id,text,isError}
type Event interface{}                                         // TextDelta | ToolCall{ID, Name, Input} | Usage | Stop{Reason}
```

- Tři implementace: `anthropic` (Messages API, `anthropic-version`
  připnutá v kódu, `Authorization: Bearer`, `strict: true` na nástrojích,
  `output_config.format` pro schéma), `openai` (Responses API,
  `store: false`, `stream: true`, `text.format`), `openaicompat` (Chat
  Completions, `stream_options.include_usage`, `response_format`; bez
  `tool_choice`, `parallel_tool_calls: false` kde je podporováno).
- Sdílené: čtečka SSE (řádky do 1 MiB, událost do 4 MiB, celek do
  32 MiB), `http.Client` bez přesměrování (`ErrUseLastResponse`),
  `TLSHandshakeTimeout` 10 s, hlavičky 60 s, celý tah nejvýš podle
  `timeout` relace (panel 5 min, triage 15 min), proxy z prostředí,
  `User-Agent: MalachiMail/<verze>`, `Retry-After` jen jako číslo pro
  chybu, žádné vlastní opakování kromě jednoho pokusu při 429/529 s
  pauzou do 30 s.
- Mapování chyb na třídy: `auth` (401), `permission` (403),
  `rateLimit` (429), `overloaded` (529/503), `quota` (účet bez kreditu
  nebo s vyčerpaným limitem: Anthropic `billing_error` nebo 400
  `invalid_request_error` s větou o kreditu, OpenAI 429
  `insufficient_quota`; bez opakování; doplněno po B2), `invalidRequest`
  (400; podtřída `toolsUnsupported` podle textu Ollamy/LM Studia),
  `network`, `protocol` (neplatné SSE/JSON). Do logu jen třída
  a stavový kód; nikdy tělo požadavku ani odpovědi, nikdy klíč,
  nikdy `baseUrl` s uživatelskými údaji.
- Testy: `httptest` servery všech tří dialektů s fixturami streamů
  (`backend/testdata/llm`), patologické případy (oříznuté SSE, neplatný
  JSON v argumentech nástroje, 10 MiB delta, nekonečný `ping`,
  přesměrování, 401 uprostřed streamu).

### 6.3 `internal/assistant` — prompty (D6, Q19)

Čistý balíček bez storu, přesun z `ui/internal/assistant` i s testy:
`SystemPrompt(language, today)`, `TriageSystemPrompt(language, today)`,
`SuggestReplySystemPrompt(followUp)`, `RewriteSystemPrompt()`,
`WriteSystemPrompt()`, `SearchSystemPrompt(today)`, `SearchSchema`,
`ContextPreamble`/`AddedContextPreamble`, `RewriteMessage`,
`WriteMessage`, `SearchMessage`, `TriageMessage`, `SuggestReplyMessage`,
`CleanRewrite`, `CleanSuggestReplyInstruction`, `WithProfile`,
`ProfileFrom`, `AllowedTools`, `TriageTools(trigger)`, `SuggestReplyTools`,
`CalendarTools`, `ProfileTools`, `BridgeArgs(purpose, …)`. Vstup tahu je
`api.PromptInput` (§6.4), výstup `api.PromptResult`; `Build(purpose,
input, profile, language, today)` je jediný vstupní bod pro relace API
i pro `assistant.prompt`. Dnešní tvar textů se nemění (test rovnosti
s Go referencí v E0, §12).

### 6.4 `internal/agent` — smyčka

1. `sessionStart` ověří účet (`ai`, zapnutý, souhlas D7, klíč v klíčence
   nebo `openaiCompatible` bez klíče), `purpose` a jeho pole (triage:
   `runId` ve tvaru `^[A-Za-z0-9._:-]+$` a `maxCases` 1–200; reply:
   `messageId` účtu případu), cestu k mostu (absolutní, spustitelná)
   a z `purpose` odvodí nástroje a argumenty mostu (`assistant.BridgeArgs`:
   žádné, `--reply-only <id>`, `--allow-triage --triage-run <id>
   --triage-max <n>`), vždy s `--in-app`.
2. S nástroji: spustí most (`mcp.CommandTransport`, minimální prostředí
   + `MALACHI_SOCKET` na vlastní socket, `clientInfo.name =
   "malachi-mail"`), `initialize`, `tools/list` ∩ povolené → definice
   nástrojů poskytovatele (schéma beze změny). Most se k démonu připojí
   obvyklým handshakem jako kterýkoli klient.
3. Tah: `PromptInput` → zpráva modelu (`assistant.Build`) do historie
   → `Stream` → `textDelta` ven, `ToolCall` → `tools/call` →
   `toolUse`/`toolResult` ven, výsledek do historie (text ohrady beze
   změny) → další kolo; nejvýš 50 kol nástrojů na tah, výsledek nástroje
   do 256 KiB (most už ořezává), historie do 1 MiB (pak
   `assistantContextFull`). Konec tahu → `result {success, text,
   structured}` + `usage` (součet kol).
4. Relace drží historii jen v paměti; `sessionStop`, pád spojení,
   nečinnost 30 min nebo ukončení démona ji zahodí a most ukončí
   (SIGTERM, po 2 s KILL). Nejvýš 4 relace na spojení, 16 na démona.
5. Spotřeba každého tahu do `assistant_usage` (účet, den, model,
   čítače); u relace s `--triage-run` navíc do běhu nástěnky (D19).

### 6.4a API (`api.md` §4.18, kompatibilní rozšíření protokolu 2)

```jsonc
PromptInput  { "text": "…" (opt),                                  // zpráva uživatele (panel)
               "context": { "accountId", "messageIds": [] | "folderId", "added": bool } (opt),
               "rewrite": { "preset": "polite|shorter|fix|english|custom", "instruction", "passage" } (opt),
               "write":   { "mode": "reply|forward|new", "instruction", "subject", "own", "quote" } (opt),
               "search":  { "words" } (opt),
               "triage":  { "maxCases": 40, "drafts": bool } (opt),
               "reply":   { "accountId", "messageId", "others": [], "instruction", "followUp": bool } (opt) }
PromptResult { "systemPrompt": "…", "message": "…" (opt), "tools": ["list_accounts", …],
               "bridgeArgs": ["--in-app", …], "jsonSchema": {…} (opt) }
AssistantEvent { "sessionId", "kind": "init|textDelta|text|toolUse|toolResult|result|failure|usage",
                 "text" (opt), "tools": [] (init), "tool", "id" (toolUse/toolResult), "isError" (opt),
                 "success", "structured" (opt), "reason" (opt), "class" (failure), "retryAfter" (opt),
                 "usage": { "inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens" } (opt) }
```

- `assistant.prompt {purpose: panel|rewrite|write|search|triage|reply, language, accountId (opt, doplněk profilu), trigger: manual|auto (triage), input: PromptInput (opt)}` → `PromptResult`;
  pro Claude Code a Codex v klientech (D6). Profil připojí démon.
- `assistant.sessionStart {accountId (účet ai), purpose, language, bridge, trigger (opt), triage: {runId} (opt), reply: {messageId} (opt), timeoutSeconds (opt)}` → `{sessionId, tools:[…]}`;
  chyby `accountNotFound`, `accountDisabled`, `assistantConsentRequired`
  (nový 1207), `invalidArgument`, `keyringError`, `assistantUnavailable`
  (nový 1511: most nejde spustit nebo nástroje chybějí), `authFailed`.
- `assistant.sessionSend {sessionId, input: PromptInput}` → `{}`;
  `assistantSessionBusy` (nový 1512) při běžícím tahu;
  `assistantSessionNotFound` (nový 1113); `assistantToolsUnsupported`
  (nový 1513, D21) jako `failure` události i chyba dalšího tahu.
- `assistant.sessionStop {sessionId}` → `{}`.
- `assistant.providerModels {accountId, refresh (opt)}` → `{models:[{id, displayName (opt)}]}` (cache 1 h).
- `assistant.usage {accountId (opt), days (opt, výchozí 1)}` → `{days:[{day, accountId, inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens, sessions, turns}]}`.
- `notify.assistantEvent AssistantEvent` (D12, D13) jen spojení, které
  relaci založilo; `notify.assistantUsageChanged {accountId}` po každém
  tahu.
- Tabulka chyb §2 a `TestDocsCoverAllMethods`.

### 6.5 `internal/rpc`: cílená notifikace

`api.Backend` dostane notifikátor s identitou spojení
(`NotifyConnection(connID, method, params)`); `sessionStart` si uloží
id spojení volajícího a události posílá jen jemu; zavření spojení
volá `agent.Close(conn)`. Broadcastové notifikace se nemění.

### 6.6 Migrace

`0027_assistant_usage.sql`: `assistant_usage(account_id, day, provider,
model, input_tokens, output_tokens, cache_read, cache_creation,
sessions, turns, updated_at)` s primárním klíčem (account_id, day,
model). Nic jiného schéma nepotřebuje (účet `ai` jde do `accounts` jako
eWay).

### 6.7 Bezpečnost (`security.md`)

- Nový §7.6 „Calls to a model API“: druhý požadavek démona mimo servery
  účtů; https s pinem nebo http podle D8 s varováním v UI; bez
  přesměrování; stropy; `Retry-After`; co jde do logu; klíč jen na
  hostitele `baseUrl`; proxy.
- Nový §10.4 „The daemon's own agent“: smyčka je jen další MCP klient
  mostu se stejnými úrovněmi (nikdy `--allow-modify` / `--allow-send`),
  prompt dodává klient, text modelu je nedůvěryhodný (klient ho ukazuje
  jako dnes), souhlas per účet, historie jen v paměti, co odchází
  k poskytovateli (prompt, text uživatele, výsledky nástrojů = pošta
  v ohradě) a že API data poskytovatelé výchozím způsobem netrénují
  (odkazy na podmínky), rozdíl u vlastního serveru.
- Revize §10.1 (věta o přihlašovacích údajích) a `chatgpt-integration.md`
  §2 (token není údaj účtu — platí pro grant SIWC, ne pro klíče API).

## 7. Návrh klientů

### 7.1 Go reference (`ui/internal/…`)

- Nový čistý balíček `ui/internal/apiassistant`: `Provider` nad
  `board.Caller`-podobným RPC rozhraním (`Open` → `assistant.sessionStart`,
  `Submit` → `sessionSend`, `Terminate` → `sessionStop`,
  `notify.assistantEvent` → `assistant.Event` 1:1, `Exit` z `result`
  nebo `failure`), `HasConsent`/`AcceptConsent` nad účtem
  (`account.update` s `consent`), texty (`assistant.APIText(tr)`: jména
  druhů, souhlasy podle druhu, chyby `api_*`), `ProviderFailure`
  rozšířené o `api_consent_required`, `api_auth_failed`,
  `api_rate_limited`, `api_unreachable`, `api_tools_unsupported`.
  Testy proti falešnému démonu (`assistantpanel` má vzor).
- `ui/internal/integrations`: katalog dostane sekci AI
  (`Service.Section`), `Row` variantu bez `AccountID` (`Local`) pro
  Claude Code/Desktop a ChatGPT; `RowFor` pro `ai` (`EditorAI`, titulek
  jméno účtu, podtitulek druh · model · spotřeba 24 h); `FullOrder`
  lokální řádky vynechá.
- `ui/internal/aiaccount` (vzor `jiraaccount`): kontroler průvodce
  a editoru účtu `ai`: druh, název, `baseUrl` (jen kompatibilní; pod
  polem nápověda s příklady adres lokálních serverů a bran OpenRouter,
  Groq, Mistral, Together, nic se nepředvyplňuje — Q18), klíč
  (write-only, „Nahradit klíč…“), Test (modely + stav), výběr modelu,
  varování http, pin certifikátu, souhlas jako poslední stránka: jedna
  společná věta o tom, co odchází (prompt, text uživatele, přečtená
  pošta a přílohy), a doplněk podle druhu — Anthropic a OpenAI
  (podmínky API, data se výchozím způsobem netrénují), vlastní server
  (adresa uživatele; u http nezabezpečeně) — Q17.
- `ui/internal/assistant`: `ProviderID` jako řetězec (D14), výběr
  „Asistent používá“ (`ProviderChoices(accounts)`), texty modalu MAS
  (`WebsiteFeaturesTexts` s novou větou o API), `WriteBarTitle`
  s názvem poskytovatele místo bool; **prompty modelu pryč** (D6):
  `SystemPrompt`, `*SystemPrompt*`, `*Message`, `ContextPreamble`,
  `AllowedTools`, `TriageTools*`, `SuggestReplyTools`, `*BridgeArgs`,
  `WithProfile`, `SearchSchema` nahradí volání `assistant.prompt`
  (`assistantpanel`, `boardtriage`, `boardreply`, `compose`, hledání);
  `Args` dál skládá příkazovou řádku Claude Code z `PromptResult`.
- `ui/internal/settings`: `assistant-provider` typ `s`;
  `board-triage-chatgpt-model` zůstává; žádný nový klíč pro API (model
  i souhlas jsou v démonu).

### 7.2 Předvolby → Integrace

- Dostupné služby: Jira, Kalendář CalDAV, eWay-CRM │ **AI**: Claude Code
  / Claude Desktop, ChatGPT (Codex), Anthropic API, OpenAI API,
  OpenAI-kompatibilní server (Ollama, llama.cpp, LM Studio…). Přidat…
  u lokálních otevře jejich nastavení (dnešní řádky MCP a Claude Code /
  ChatGPT jako dialog), u API účtů průvodce `aiaccount`; v MAS u lokálních
  modal (D17).
- Připojené služby: účty démona (včetně `ai` s přepínačem, Upravit,
  Odebrat) + lokální řádky „Claude Code“ (registrováno v Claude Desktop
  / Claude Code, cesta, přihlášen; tlačítka Nastavení, Odebrat
  = odregistrovat) a „ChatGPT“ (Připojeno jako …, Odpojit). Pořadí:
  účty podle `account.reorder`, lokální řádky za nimi, bez přetahování.
  Lokální integrace je vždy jen na jednom místě (rozhodnutí vlastníka
  2026-11-07 po B2): v katalogu AI s Přidat…, dokud není registrovaná
  / připojená, pak jako řádek v Připojených a z katalogu zmizí
  (`integrations.LocalConnected`, `Offered`). Položky AI nesou kapsli
  (`integrations.Badges`, totéž co kapsle druhu účtu v postranním
  panelu): lokální „This computer“ (Tento počítač), účty přes API
  „API key“ (Klíč API), i na řádku v Připojených; služby kapsli nemají
  (rozhodnutí vlastníka 2026-11-07). Katalog je jedna skupina Dostupné
  služby (služby, pak AI); samostatná skupina „AI“ zmizela, protože dva
  stejně velké nadpisy četl vlastník jako dvě kategorie, ač AI je část
  dostupných služeb (rozhodnutí 2026-11-07; `integrations.Section` je
  jen pořadí).
- Odebrání účtu `ai`: věta podle druhu („…a jeho klíč“).

### 7.3 Předvolby → AI

- O mně (beze změny).
- Asistent: **Asistent používá** (combo: Claude Code, ChatGPT, každý
  zapnutý účet `ai`; položka nedostupného zdroje zašedlá s důvodem),
  Zobrazovat nabídku Asistent, Otevřít v (desktop / code / app jako dnes;
  v MAS `app` jen s účtem `ai`), Model (podle vybraného: enum Claude,
  katalog ChatGPT, nebo `AI.model` účtu přes `providerModels`).
- Nástěnka: jako dnes; Model podle poskytovatele (`AI.boardModel`).
- Pryč: skupina MCP (do Integrací), skupina ChatGPT, řádek In-app
  provider, řádek Claude Code, v MAS skupina Další funkce asistenta.

### 7.4 Zapojení poskytovatele

Továrny `(*Assistant).Provider()` / `boardProvider()` (GTK
`window/chatgpt.go`, macOS `AppState.selectedProvider`, Windows
`AppState.InAppProvider`) vrátí `apiassistant.Provider` pro `acc_…`;
adaptéry `providerSettings`, `providerLocator` (pro API: `Locate` = účet
existuje a je zapnutý, `SignedIn` = souhlas a stav účtu),
`providerBoardTranslator` dostanou třetí větev. `CanRunInApp`,
`TriageAvailable`, `ReplyAvailable`, `RefreshHandlers` berou účet `ai`
jako dostupný bez registrace mostu (jako ChatGPT). Panel: titulek
a patička se jménem účtu; řádek chyby s *Nastavení…* (otevře editor
účtu) místo *Přihlásit se…*.

### 7.5 macOS a Windows

Porty 1:1 (`MalachiCore/APIAssistant`, `Malachi.Core/ApiAssistants`),
`AssistantProvider` implementace nad `RPCClient`/`RpcClient`
a notifikací; `IntegrationsPaneViewController` /
`IntegrationsPage` s lokálními řádky a sekcí katalogu AI; dialogy
účtu `ai` (sheet / `ModalDialog`); `Settings.assistantProvider` řetězec
(v MAS: `claude`/`chatgpt` se čtou jako Claude Desktop jako dnes, účet
`ai` platí). Windows: nic specifického (bez brány distribuce). msgidy:
nové texty v Go reference (`po/POTFILES`), česky v `cs.po`; Windows
použije všechny; texty jen pro MAS modal nese Go reference s výjimkou
ve `windows/parity-exclusions.txt`.

### 7.6 App Store build (D17)

`Distribution.allowsLocalAgents` false dál řídí lokální cesty;
`Assistant.targets` nabídne `app`, když `Settings.assistantProvider`
ukazuje na zapnutý účet `ai`; `BoardTriageController`,
`BoardReplyController`, `AssistantRequest` a panel jdou přes
`apiassistant`, takže brány `ClaudeCodeLocator.locate() == nil`
a `CodexProvider.executable == nil` se jich netýkají. Katalog: Přidat…
u lokálních → `WebsiteFeaturesDialog` s textem „V této verzi z App Store
nejde Claude Code ani Codex; použijte verzi z malachimail.app, nebo
přidejte AI přes API“ a tlačítky *Web* a *Přidat Anthropic API* (nebo
přepnout na položku katalogu). Skupina v AI a odkaz ve stavovém pruhu
zmizí; dnešní texty `WebsiteFeaturesTexts` se upraví. Cesta k mostu
pro démona v MAS = `malachi-mcp-worker` v pomocném bundlu
(`Paths.bridgeWorkerExecutable`), v DMG `Contents/MacOS/malachi-mcp`.

## 8. Etapy a brány

| Etapa | Obsah | Ověření |
|---|---|---|
| E0 | Démon: účet `ai` (§6.1), `internal/llm` (§6.2), `internal/agent` (§6.3), API §6.4, cílená notifikace §6.5, migrace §6.6, `security.md` §6.7, `api.md`. Vývojový nástroj: `malachi-devmail` jako falešný OpenAI-kompatibilní server (skriptované odpovědi s nástroji) pro smoke testy klientů. | Go testy démona (httptest všech dialektů, falešný most, `TestDocsCoverAllMethods`), `go vet`; ruční běh proti skutečné Ollamě na Macu vlastníka a proti klíči Anthropic/OpenAI vlastníka (klíče zadá on sám, nikdy do repozitáře). |
| E1 | Go reference + GTK: `apiassistant`, `aiaccount`, `integrations`, Předvolby AI a Integrace, zapojení poskytovatele, msgidy. | `gtk.sh make build`, `test-ui`, smoke (`MALACHI_GTK_SMOKE=1`) nad falešným serverem, `make po`, `make lint`; brána B2 vlastníka v XQuartz (`make run-gtk-live`). |
| E2 | macOS: porty, Integrace, dialogy, MAS modal a cíl `app` přes účet `ai`. | `swift build -warnings-as-errors`, `swift test`, `check-strings.py`; brána B1 vlastníka nad kopií dat (DMG build) **a** App Store build ve VM nad kopií dat (panel, triage, Navrhnout odpověď přes API; lokální položky s modalem). |
| E3 | Windows: porty, `IntegrationsPage`, `AiPage`, dialogy. | `build.ps1 app`/`test`/`lint`, UI smoke s falešným serverem; brána B3 vlastníka. |
| E4 | Docs: `mcp.md`, `chatgpt-integration.md` poznámka, `macos/README.md`, `windows/README.md`, `CLAUDE.md` odstavec, `releasing.md` (nic), `THIRD-PARTY-NOTICES.md` beze změny (žádný nový modul). | revize vlastníka |

Pořadí podle pravidla 6: démon → Go reference + GTK → macOS → Windows.
Pull requesty (Q14): **E0 + E1 v jednom** (GTK jako reference ukáže
démona v akci a vlastník ho ověří v XQuartz), E2 a E3 každý zvlášť, E4
s posledním z nich. Přesun plánovače triage do démona (Q16) je pátá
etapa po E3 s vlastním PR.

## 9. Otázky a odpovědi vlastníka

- **Q1 — odpovězeno 2026-11-07: všechny dnešní funkce** (panel, přepis
  a psaní, hledání, triage, Navrhnout odpověď).
- **Q2 — odpovězeno 2026-11-07: architekturu prozkoumat a doporučit;**
  porovnání démon / most jako agent / klienti dalo doporučení démon,
  přijaté jako Q5.
- **Q3 — odpovězeno 2026-11-07: konfigurace poskytovatelů v démonu,
  sdílená všemi klienty**, klíče v jeho klíčence; tvar upřesňuje Q6.
- **Q4 — odpovězeno 2026-11-07: podklady jako dokument v `docs/plans`**
  (tento soubor), necommitnutý.
- **Q5 — odpovězeno 2026-11-07: smyčka agenta v démonu** (most jako
  agent spouštěný klientem by musel dostat klíč z démona a v MAS běžet
  jako kopie s `inherit`).
- **Q6 — odpovězeno 2026-11-07: účet nového druhu `ai`**, ne seznam
  v `meta`.
- **Q7 — odpovězeno 2026-11-07: do Integrací všechno naráz** (lokální
  Claude Code/Desktop s Registrovat v Claude, ChatGPT/Codex i účty API).
- **Q8 — odpovězeno 2026-11-07: vlastní tenký HTTP klient**, ne SDK.
- **Q9 — odpovězeno 2026-11-07: souhlas v démonu, per poskytovatel.**
- **Q10 — odpovězeno 2026-11-07: http kamkoli**, uživatel rozhoduje; UI
  varuje mimo loopback.
- **Q11 — odpovězeno 2026-11-07: v MAS nahradit skupinu v AI i odkaz ve
  stavovém pruhu modalem v Integracích**; poznámka vlastníka z téhož dne:
  lokální integrace zůstanou v katalogu vidět a Přidat… jen ukáže modal
  (verze z webu, nebo AI přes API).
- **Q12 — odpovězeno 2026-11-07: spotřebu eviduje démon**, bez stropu.
- **Q13 — odpovězeno 2026-11-07: víc účtů téhož poskytovatele ano**;
  realm doplněný o id účtu, názvy účtů je rozliší.
- **Q14 — odpovězeno 2026-11-07: E0 + E1 v jednom PR, E2 a E3 zvlášť.**
- **Q15 — odpovězeno 2026-11-07: „Otevřít v“ zůstává na záložce AI**
  (chování nabídky Asistent, ne připojení).
- **Q16 — odpovězeno 2026-11-07: plánovač automatické triage do démona
  jako další etapa po E3** (běh bez okna, denní strop vynucuje démon).
- **Q17 — odpovězeno 2026-11-07: souhlas = společná věta + doplněk podle
  druhu** (Anthropic / OpenAI: podmínky API, data se netrénují; vlastní
  server: adresa uživatele, u http nezabezpečeně). Návrh textů s češtinou
  přijde v E1.
- **Q18 — odpovězeno 2026-11-07: brány jen jako nápověda pod polem
  adresy** položky „OpenAI-kompatibilní server“, nic se nepředvyplňuje,
  žádné samostatné položky katalogu.
- **Q19 — odpovězeno 2026-11-07: prompty modelu do démona hned v E0+E1**
  (revize D6): démon dostane balíček promptů a metodu `assistant.prompt`,
  `sessionStart` bere `purpose` + vstup; GTK v E1 přejde na
  `assistant.prompt` i pro Claude Code a Codex a smaže Go kopie; macOS
  a Windows smažou své porty v E2 a E3 spolu s portem poskytovatele API.

## 10. Co zůstává mimo

- Žádné OAuth k Anthropic ani OpenAI (App Attest Anthropicu by účtoval
  workspace vývojáře; SIWC zůstává u Codexu).
- Žádný Google Gemini, Mistral nativní API ani Bedrock/Vertex: brány
  s OpenAI tvarem je pokryjí přes `openaiCompatible`.
- Žádné obrázky příloh pro model (`get_attachment` vrací obrázek jako
  obsah MCP; první etapa posílá jen text, obrázky jako „nepodporováno“).
- Žádné rozšířené myšlení ani volba úsilí v UI (démon posílá výchozí;
  `effort`/`reasoning` je pozdější volba účtu).

## 11. Rizika

- **Nástroje u lokálních modelů**: i s `--jinja` a správným parserem
  volá model pod ~8B nástroje nespolehlivě; panel pak skončí chybou nebo
  halucinací ids. Průvodce by měl po Testu říct, zda model nástroje
  hlásí (Ollama 400, LM Studio „native“), a dokumentace doporučit
  modely, které prošly ručním testem vlastníka (D21).
- **Streamování přes cílenou notifikaci** je nová schopnost
  `internal/rpc`; chyba v ní by rozbila i ostatní notifikace. Testy
  s více spojeními a pádem spojení uprostřed tahu.
- **Most jako podproces démona**: cesta mostu od klienta musí v MAS být
  kopie s `inherit`; most s vlastním sandboxem spadne. Smoke test v MAS
  buildu ve VM je povinný (E2).
- **Rozšíření klíče `assistant-provider` z výčtu na řetězec**: uložená
  hodnota ze starší verze je `claude`/`chatgpt`, kompatibilní; změna
  typu klíče gschema je ale nekompatibilní pro dconf (`s` místo enumu)
  — ověřit, že dconf starou hodnotu přečte, jinak nový klíč
  `assistant-provider-id` a starý nechat.
- **Náklady**: automatická triage s drahým modelem a bez stropu tokenů
  (Q12) může stát desítky dolarů denně; řádek spotřeby a doporučení
  `boardModel` = levný model v průvodci (Haiku 5.5 / gpt‑6‑luna).
- **Podmínky Anthropicu od 2026-11-12** (zákaz „routing requests through
  consumer subscriptions“) se netýkají klíčů API, ale ztěžují obhajobu
  dnešního experimentálního cíle „V aplikaci“ nad Claude Code; API cesta
  je pro MAS i pro vydání jistější varianta.
- **Přesun promptů do démona** (D6): dokud macOS a Windows nepřejdou na
  `assistant.prompt` (E2, E3), žijí jejich porty vedle démona; test
  v E0 porovná výstup `backend/internal/assistant` s dnešními Go
  referencemi bajt po bajtu, aby přesun nic nezměnil, a E1 Go kopie
  smaže.

## 12. Plán etapy E0+E1 (démon, Go reference, GTK)

Jeden pull request z větve `feat/ai-api` (Q14), žádný commit bez pokynu
vlastníka, pořadí práce podle pravidla 6: démon → Go reference → GTK.
macOS a Windows se v této etapě nemění (E2, E3 mají v §12.6 předávku);
nové msgidy dostanou ve `windows/parity-exclusions.txt` důvod „AI via
API: Windows port pending (E3)“. Čísla řádků odkazují na stav větve
2026-11-07.

### 12.1 Zásady etapy

- Most `malachi-mcp` se mění jen v jednom místě (`list_accounts` vynechá
  účty `ai`, §12.2 E0.9); jeho úrovně, ohrada a limity zůstávají.
- Cesta Claude Code a Codex zůstává funkčně stejná; jediná změna je, že
  si prompt, nástroje a argumenty mostu berou z `assistant.prompt` (D6).
  Test rovnosti (E0.2) zaručí, že démon dává tentýž text jako dnešní Go
  reference.
- Nic, co model napíše, se neloguje; klíč API nikdy neopustí démon
  a nikdy není v logu, chybové větě ani `error.data`.
- Každý nový soubor nese hlavičku SPDX (`AGPL-3.0-only` v `backend/`,
  `GPL-3.0-or-later` v `ui/`); nový Go soubor s texty jde do
  `po/POTFILES`; nový `.blp` do `ui/data/data.go` (`go:generate`).

### 12.2 E0 — démon

| Krok | Soubory | Obsah | Testy |
|---|---|---|---|
| **E0.1 Kontrakt** | `pkg/api/types.go` (`AccountAI AccountKind = "ai"` za l. 223; `AccountConfig.AI *AIConfig` za l. 378; `CapabilityAssistant` za l. 433; `AccountTestResult.AI` l. 683–690), nový `pkg/api/ai.go` (`AIConfig{Provider AIProvider; BaseURL, Model, BoardModel string; Consent *AIConsent; CertificateSHA256 string}`, `AIProvider` `anthropic|openai|openaiCompatible`, `AIConsent{Version int; AcceptedAt time.Time}`, `AssistantConsentVersion = 1`, `PromptInput`, `PromptResult`, `AssistantEvent`, `AssistantUsageDay`, limity `MaxAssistantSessionsPerConn = 4`, `MaxAssistantSessions = 16`, `MaxAssistantTurnTools = 50`), `pkg/api/methods.go` (`MethodAssistantPrompt`, `MethodAssistantSessionStart/Send/Stop`, `MethodAssistantProviderModels`, `MethodAssistantUsage`; `NotifyAssistantEvent`, `NotifyAssistantUsageChanged`; vše na konec `AllMethods`/`AllNotifications`), `pkg/api/errors.go` (1113 `assistantSessionNotFound`, 1207 `assistantConsentRequired`, 1511 `assistantUnavailable`, 1512 `assistantSessionBusy`, 1513 `assistantToolsUnsupported`), `pkg/api/service.go` (`AssistantService` += `Prompt`, `SessionStart`, `SessionSend`, `SessionStop`, `ProviderModels`, `Usage`; nové volitelné rozhraní `AssistantEventNotifier{AssistantEvent(ConnID, AssistantEvent) bool}` a `ConnObserver{ConnClosed(ConnID)}`), nový `pkg/api/conn.go` (`ConnID`, `WithConn(ctx, id)`, `ConnFromContext(ctx)`), `docs/api.md` (§4.1: `kind` += `ai`, blok `AIConfig`, pravidla účtu `ai`, capability tabulka, `account.test` výsledek `ai`, `account.add` chyby; §4.18 nové metody a typy z §6.4a; §5 dvě notifikace a věta o cíleném doručení; §2 pět řádků; §7 changelog) | `AccountConfig.Protocol()` beze změny; `ProfileNoteKinds` beze změny (účet `ai` nemá doplněk profilu); `AIConfig` validace jen tvarová v `api` (`Valid()`), sémantická v core | `pkg/api`: round-trip vzorky (`TestAIJSON` jako `TestCrmJSON`), úprava `TestAssistantMethodsOrder` a posunů v `AllNotifications`, `TestDocsCoverAllMethods`, `TestErrorCodesUnique` |
| **E0.2 Prompty** | nový `internal/assistant/` (`doc.go`, `prompts.go`, `tools.go`, `input.go`, `profile.go`, `build.go`): přesun funkcí z `ui/internal/assistant/{claude,rewrite,compose_write,search,triage,suggest_reply,profile}.go` vyjmenovaných v §6.3; `Build(purpose, input, profile, language, today) (api.PromptResult, error)` | Texty bajt po bajtu stejné jako dnes; `WithProfile` bere `api.AssistantProfile` z démona; `BridgeArgs` vždy začíná `--in-app` | přesunuté testy; **test rovnosti** `backend/internal/assistant/parity_test.go`, který čte `../../../ui/internal/assistant/testdata/prompts.golden` (vygenerovaný v E1 z dnešní Go reference před jejím smazáním) a porovná každý prompt a zprávu; po E1 zůstane golden jako jediný zdroj pravdy |
| **E0.3 Store** | `internal/store/accounts.go` (`RealmOf` l. 52–64: větev `ai` = `"ai:" + siteRealm(effective base URL)`; `accountRealm` l. 86–98 vyžaduje realm i pro `ai`; `AddAccount` l. 143 a `UpdateAccount` l. 254 dovolí prázdný e-mail i pro `ai`; kontrola unikátnosti l. 160–168 se pro `ai` **přeskočí** (Q13: víc účtů téhož poskytovatele)), nová migrace `migrations/0027_assistant_usage.sql` (`assistant_usage(account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, day TEXT NOT NULL, model TEXT NOT NULL, input_tokens INTEGER NOT NULL CHECK (>= 0), output_tokens …, cache_read_input_tokens …, cache_creation_input_tokens …, sessions INTEGER NOT NULL, turns INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (account_id, day, model))`), nový `internal/store/assistant_usage.go` (`AddAssistantUsage(ctx, accountID, day, model, usage, newSession bool)` upsert `ON CONFLICT … DO UPDATE SET input_tokens = input_tokens + excluded.input_tokens …`, `AssistantUsage(ctx, accountID, sinceDay) ([]api.AssistantUsageDay, error)`, `PruneAssistantUsage(ctx, before)`) | `DeleteAccount` smaže řádky přes cizí klíč (ověřit, že `foreign_keys` je zapnuté; jinak explicitní `DELETE`) | `TestAIRealm` v `crm_links_test.go` stylu, migrační test po vzoru `board_test.go:151–166`, `TestAssistantUsageUpsert` |
| **E0.4 Účet `ai` v core** | nový `internal/core/accounts_ai.go` (`validateAI` jako `validateEway` l. 33–61: odmítne cizí bloky, `provider` z výčtu, `baseUrl` povinné jen u `openaiCompatible` a jinak prázdné, normalizace URL, `model` neprázdný, `certificateSha256` jen s https přes `api.NormalizeCertificateSHA256`; `isAIAccount`; `aiSecretMoves` = změna `provider` nebo hostitele `baseUrl`; `aiHTTP(cfg)` z `internal/llm`; `testAI` = `llm.Models` s klíčem z parametru nebo z klíčenky, výsledek `endpointResult` + `AI.Models`), `accounts_eway.go:67–70` (`hasNoMail` += `AccountAI`), `accounts.go` (`Add` l. 72–80: u `anthropic`/`openai` je `credentials.password` povinné, u `openaiCompatible` volitelné; `Update` l. 331: `aiSecretMoves`; `Test` l. 509–511: větev `ai`; `validateAccountConfig` l. 813–834 a 852–921: větev `ai`, zpráva `"kind must be imap, graph, jira, caldav, eway or ai"`), `accounts_jira.go:191–216` (`capabilitiesFor`: `ai` → `[]{CapabilityAssistant}`), `dispatch.go:292` (`kindOrder` += `AccountAI`; supervisor zůstává `noopSupervisor`), `board_service.go:281` (vyloučit `isAIAccount`), `backend.go` (`AIHTTP *http.Client` testovací hák jako `EwayHTTP`) | `Account.State` účtu `ai`: `idle` (zapnutý), `disabled`, `authRequired` po 401 z poslední relace nebo testu (drží `aiState` mapa v core do dalšího úspěchu), `error` po jiném selhání | `core/accounts_ai_test.go` (`TestAIAccount` jako `TestEwayAccount`: add bez klíče u anthropic = invalidArgument, add + keyring, test přes `llmtest` fake, update s přesunem tajemství, remove maže klíč i usage), `capabilities_test.go` += `ai`, `assistant_profile_test.go` (účet `ai` odmítne doplněk), `unified_test.go`, `board_service` test vyloučení |
| **E0.5 `internal/llm`** | `llm.go` (rozhraní `Provider`, `Request`, `Message`, `Block`, `Tool`, události `TextDelta`, `ToolCall`, `Usage`, `Stop`; `Models`), `http.go` (`NewClient(cfg api.AIConfig, key string, override *http.Client)`: `http.Client` bez přesměrování, `TLSHandshakeTimeout` 10 s, `ResponseHeaderTimeout` 60 s, proxy z prostředí, UA `MalachiMail/<verze>`, per-host transport s pinem jako `calDAVTransport` (`accounts_caldav.go:652–710`), http dovoleno kamkoli (D8)), `sse.go` (čtečka: řádek 1 MiB, událost 4 MiB, celek 32 MiB, `event:`/`data:` podle RFC), `errors.go` (třídy `auth`, `permission`, `rateLimit` s `retryAfter`, `overloaded`, `invalidRequest`, `toolsUnsupported`, `network`, `protocol`; mapování na `api.Error` kódy `authFailed`, `serverTimeout`, `serverError`, `networkError`, `tlsError` přes `transport.Classify`), `anthropic/client.go` (Messages API: `Authorization: Bearer`, `anthropic-version` konstanta, `tools` se `strict: true`, `output_config.format` pro schéma, streamové události `message_start` … `message_stop`, `input_json_delta` skládání argumentů, `usage` z `message_start` + `message_delta`), `openai/client.go` (Responses API: `store: false`, `stream: true`, `tools[{type:"function", …, strict}]`, `text.format json_schema`, události `response.output_text.delta`, `response.function_call_arguments.delta/.done`, `response.completed` s `usage`), `openaicompat/client.go` (Chat Completions: `stream_options.include_usage`, `tools[{type:"function", function:{…}}]`, `response_format json_schema` s fallbackem při 400 na pokyn v promptu, `[DONE]`, skládání `tool_calls` z delt, detekce `toolsUnsupported` z textu 400 u Ollamy/LM Studia), `llmtest/` (httptest servery všech tří dialektů se skriptem odpovědí: text, volání nástroje, chyby, oříznutý stream; fixtury `backend/testdata/llm/*.sse`) | Do logu jen třída, stavový kód a `request-id`; nikdy tělo ani URL s údaji; jediné opakování: 429/529 jednou po `Retry-After` ≤ 30 s | tabulkové testy všech tří klientů nad `llmtest`, patologické vstupy (neplatný JSON argumentů, 10 MiB delta, nekonečný `ping`, 401 uprostřed streamu, přesměrování 302 → chyba, http → projde), `Models` nad 200/401/403/400 |
| **E0.6 RPC** | `internal/rpc/server.go` (`conn.handle` l. 589–617: `ctx = api.WithConn(ctx, c.id)` s `id` přiděleným v `addConn`; `Server.NotifyConn(id, method, params) bool` vedle `broadcast` l. 371–393; `removeConn` l. 350–355 volá `OnConnClosed`; implementace `api.AssistantEventNotifier`), `dispatch.go:243–249` (`registerAssistant` += šest metod), `stub.go` (stubs) | Zápis notifikace jednomu spojení pod jeho `wmu`, s write deadline 10 s (dnes žádná; zaseknutý klient by blokoval relaci) | `server_test.go`: `TestAllMethodsRegistered` projde, nový `TestNotifyConnReachesOneConnection` (dvě `dialAuthed`), `TestConnClosedHook`; `assistant_test.go` dispatch po vzoru `board_test.go` |
| **E0.7 `internal/agent`** | `bridge.go` (`startBridge(ctx, path, socket, args)`: `exec.Command` s minimálním prostředím (`PATH`, `HOME`, `TMPDIR`, `LANG`, `MALACHI_SOCKET`), `mcp.CommandTransport`, `clientInfo{Name: "malachi-mail", Version}`, `tools/list` → `[]llm.Tool` ∩ povolené, `call(name, args) (text, isError)`, ukončení SIGTERM → 2 s → KILL), `session.go` (`Session`: historie, `Turn(input)`: smyčka §6.4 body 3, limity, `cancel`), `manager.go` (`Manager`: registr podle `api.ConnID`, limity 4/16, nečinnost 30 min, `CloseConn(id)`, `Stop()`), `events.go` (překlad na `api.AssistantEvent`) | Výsledek nástroje jde do historie i do události **beze změny textu** (ohrada mostu); `toolUse` a `toolResult` nesou `id` modelu; `result.structured` = text odpovědi, když je validní JSON a schéma bylo žádáno | `agent_test.go` nad `mcp.NewInMemoryTransports` (falešný most s nástroji `read_message`, `create_draft`) a falešným `llm.Provider`: panelový tah se dvěma koly, triage s `--triage-run`, limity kol a historie, zrušení uprostřed streamu, pád mostu → `failure` |
| **E0.8 Služba** | nový `internal/core/assistant_prompt.go` (`Prompt` → `assistant.Build` s profilem z `loadProfile`, jazykem z parametru, datem v zóně démona), nový `internal/core/assistant_sessions.go` (`SessionStart`: kontroly z §6.4 bod 1, `api.ConnFromContext`, klíč z `Keyring.Get` jen do paměti relace, `llm.NewClient`, `agent.Manager.Open`; `SessionSend`: `PromptInput` → `assistant.Build` → `Session.Turn` v goroutine, události přes `AssistantEventNotifier.AssistantEvent(conn, …)`; `SessionStop`; `ProviderModels` s cache 1 h v `aiModels` mapě; `Usage`), nový `internal/core/assistant_usage.go` (zápis po tahu, `notify.assistantUsageChanged`, u `--triage-run` doplnění `usage` do `board_runs` při `board.runEnd` bez `usage` — D19; hodinový `PruneAssistantUsage` 400 dní v `Maintain`), `backend.go` (`SocketPath string`, `BridgeArgs`; `StartSync` spustí `agent.Manager`, `Close` ho zastaví; `ConnClosed(id)` → `Manager.CloseConn`), `cmd/malachid/main.go` (po `srv.Listen(*flagSocket)` nastavit `backend.SocketPath = *flagSocket`) | Souhlas: `account.update` s `ai.consent {version}` zapisuje souhlas (`acceptedAt` dává démon); `sessionStart` bez `consent.version == AssistantConsentVersion` → 1207 | `core/assistant_sessions_test.go` nad `llmtest` + falešný most (binárka? ne: `agent.Manager` dostane továrnu mostu, v testu in-memory): start bez souhlasu, panelový tah s událostmi na správné spojení, druhé spojení nic nedostane, `sessionBusy`, pád spojení zavře relaci, usage zapsané, `ProviderModels` cache |
| **E0.9 Most** | `cmd/malachi-mcp/tools_read.go:97–124` (`listAccounts` vynechá `Protocol() == api.AccountAI`), popis l. 25 beze změny | Účty `ai` nejsou poštovní a model je nemá vidět | `tools_read_test.go` s účtem `ai` ve falešném démonu |
| **E0.10 Docs** | `docs/security.md` (§7.6 „Calls to a model API“, §10.4 „The daemon's own agent“, revize §10.1 věty o údajích, odkaz v §6), `docs/mcp.md` (nový oddíl „Sessions run by the daemon (AI via API)“ za „A suggested reply on the board“; v oddílech panelu, triage a odpovědi věta, že prompt a argumenty dává `assistant.prompt`), `docs/architecture.md` §7 (rozhodnutí D2, D3, D6), `docs/chatgpt-integration.md` §2 (poznámka o revizi), `CLAUDE.md` (odstavec „Asistent přes API“ ve Stavu a prioritách, až v předávce) | — | `TestDocsCoverAllMethods` |
| **E0.11 (volitelné) devmail** | `cmd/malachi-devmail` (`--llm` port: falešný OpenAI-kompatibilní server nad `llmtest` se skriptem „přečti zprávu a odpověz“) | Umožní `make run-gtk` ukázku bez skutečného klíče a smoke testy GTK s démonem | — |

Pořadí: E0.1 → E0.2 → E0.3 → E0.4 → E0.5 → E0.6 → E0.7 → E0.8 → E0.9 →
E0.10. Ověření E0: `cd backend && go vet ./... && go test ./...` (dvě
známá selhání jen na macOS, viz paměť), `go test ./pkg/api -run
'Docs|Order|Unique'`, ruční běh démona nad kopií dat s účtem `ai`
na Ollamu vlastníka (`account.add` z GTK v E1).

### 12.3 E1 — Go reference

| Krok | Soubory | Obsah | Testy |
|---|---|---|---|
| **E1.1 Golden promptů** | `ui/internal/assistant/testdata/prompts.golden` + generátor `go test -run TestWritePromptGolden -update` | Před smazáním Go kopií zapsat výstup každé prompt funkce pro pevné vstupy (jazyk `cs`, datum, profil s doplňkem, všechny presety přepisu, módy psaní, triage ruční/auto, odpověď/follow-up); E0.2 ho čte | — |
| **E1.2 `apiassistant`** | nový `ui/internal/apiassistant/` (`provider.go`: `Provider{caller, loop, account api.Account, purpose}` implementuje `assistantpanel.Provider` (+ nová metoda `Name() string` v rozhraní, Codex vrací `ChatGPTText.Name`); `Open` → `assistant.sessionStart`; `session.go`: `Submit` → `assistant.sessionSend` s `PromptInput`, `Handle(method, params) bool` překládá `notify.assistantEvent` na `[]assistant.Event` a `Exit`; `Terminate` → `sessionStop`; `failures.go`: kódy `api_consent_required`, `api_auth_failed`, `api_rate_limited`, `api_unreachable`, `api_tools_unsupported`, `api_context_full` do `assistantpanel.ProviderFailure` a `providerText`; `registry.go`: `Sessions` registr podle `sessionId` pro okno) | `SessionSpec` dostane `Purpose` a `Input` (místo `SystemPrompt`/`Tools`; Claude a Codex cesty je dál dostanou z `assistant.prompt` v `Request.launch`/`Controller.launch`) | `provider_test.go` s falešným `Caller` (záznam volání) a simulovanými notifikacemi; `assistantpanel/provider_test.go` rozšířit o `Name()` |
| **E1.3 `assistantpanel`, `boardtriage`, `boardreply`, `compose`** | `assistantpanel/controller.go` (`launch` l. 1552 a `prompt` l. 1386: prompt a preambule z `assistant.prompt {purpose: panel}`; `Subtitle` l. 708–720 přes `Provider.Name()`), `oneshot.go` (`Request.launch` l. 423 z `PromptResult`; `Rewriter`, `Searcher`, `Writer` posílají `PromptInput`), `boardtriage/controller.go:1031–1057` (`Source` `api:<provider>` u účtu `ai`, `assistant.prompt {purpose: triage, trigger}`), `boardreply/controller.go:495–515` (`purpose: reply`), `compose/rewrite.go`, `compose/write.go:87` (`WriteBarTitle(tr, name)`), `window/search_ownwords.go` | Smazat z `ui/internal/assistant` funkce vyjmenované v §6.3 a jejich testy (golden z E1.1 je nahrazuje); `Args` bere `PromptResult` | existující testy balíčků upravené na `assistant.prompt` přes falešného volajícího |
| **E1.4 `integrations`** | `integrations/integrations.go` (`Catalog` l. 137–162 += sekce `SectionAI` s pěti položkami: `ServiceClaude` (lokální, `Kind` prázdné, `Local: LocalClaude`), `ServiceChatGPT` (`LocalChatGPT`), `ServiceAnthropic`, `ServiceOpenAI`, `ServiceOpenAICompat` (všechny `Kind: api.AccountAI` s `Provider`); `Editor` += `EditorAI`, `EditorClaude`, `EditorChatGPT`; `RowFor` l. 214–245 větev `ai` (ikona `AIIcon`, podtitulek druh · model, `RemoveBody` „…a jeho klíč“); nový typ `LocalRow{Kind LocalKind; Title, Subtitle string; Editor; CanRemove bool}` a `LocalRows(claude ClaudeState, chatgpt ChatGPTState, tr)`; `host()` l. 262–279; `Texts().Description` += AI; `FullOrder` beze změny (lokální řádky mimo)) | Pravidla v čistém Go, bez gotk4 | `integrations_test.go` += katalog, `RowFor(ai)`, `LocalRows` pro registrováno/neregistrováno/přihlášen/odpojen, po test msgidů |
| **E1.5 `aiaccount`** | nový `ui/internal/aiaccount/` (`controller.go`: `Controller` po vzoru `jiraaccount` + `eway_flow`: stránky `Kind` (jen při přidání z katalogu je dané), `Server` (`name`, `baseUrl` s nápovědou adres a varováním http mimo loopback, `key`, pin certifikátu přes `certtrust`), `Test` (`account.test` → modely, stav), `Model` (výběr z modelů + ruční zápis, `boardModel`), `Consent` (text Q17 podle druhu); `Save` → `account.add`/`account.update` s `Credentials{Password: key}`; `ReplaceKey`/`KeyReplaced`; `text.go`: `Strings`, `Texts(tr)`, `ConsentTexts(tr, provider)`, `AddressHint(tr)`, `HTTPWarning(tr)`; `failure.go`: `FailureOf(err)`), `po/POTFILES` += `aiaccount/text.go` | Klíč je write-only: pole se po uložení vyprázdní, editor ukazuje „Klíč uložen · Nahradit klíč…“ | `controller_test.go` s `fakeDaemon` (add anthropic bez klíče, test 401 → věta, test OK → modely, http varování, pin, souhlas povinný před uložením, update bez klíče nechá uložený) |
| **E1.6 `assistant`, `settings`, `capabilities`** | `assistant/assistant.go` (`ProviderChoice{ID, Name, Available, Problem}`, `ProviderChoices(settings, accounts, claude, chatgpt)`, `ProviderSource(id) (kind: claude|chatgpt|account)`), `assistant/chatgpt.go` beze změny, nový `assistant/api.go` (`APIText(tr)`: názvy druhů, „Asistent používá“, chyby `api_*`, text modalu MAS pro Go referenci), `settings/store.go:508–518` (`AssistantProvider` vrací řetězec beze změny kromě prázdného → `claude`; `SetAssistantProvider` přijme `claude`, `chatgpt`, `acc_…`), `data/io.github.schotek.Malachi.gschema.xml` l. 38–41 a 240–245 (`assistant-provider` `type="s"`, výčet pryč, popis „…: claude, chatgpt or the id of an AI account“), `capabilities/capabilities.go:160–166` (`InMail`: `AccountAI` → false), `window/model.go:923–936` (`accountHeaderBadge`: `ai` nikdy v postranním panelu, žádná změna, jen test) | Uložené `claude`/`chatgpt` z výčtu jsou v dconf řetězce, změna typu klíče je kompatibilní | `settings/chatgpt_test.go:30–33` a `store_test.go:294` upravit; `assistant/api_test.go`; `capabilities_test.go` |

### 12.4 E1 — GTK

| Krok | Soubory | Obsah | Testy |
|---|---|---|---|
| **E1.7 Notifikace a zapojení** | `window/notify.go:27–103` (`case api.NotifyAssistantEvent: w.assist.apiSessions.Handle(method, params)`, `case api.NotifyAssistantUsageChanged: w.prefs.refreshUsage`), `window/assistant.go` (`Assistant` += `apiSessions *apiassistant.Sessions`, `aiAccounts []api.Account` z `account.list` (obnova na `notify.accountsChanged`); `Provider()` a `boardProvider()` v `window/chatgpt.go:126–134, 277–291` třetí větev pro `acc_…`; `providerSettings` l. 201–239 (souhlas u `ai` čte účet: `HasConsent` = `Consent.Version == AssistantConsentVersion`, `AcceptConsent` = `account.update`), `providerLocator` l. 241–274 (`Locate` = účet existuje a je zapnutý, `SignedIn` = stav účtu není `authRequired`), `providerBoardTranslator` l. 346–380 (třetí sada vět: „Účet … není dostupný“, „Klíč účtu … byl odmítnut. Nahraďte ho v Integracích.“), `runtimeChanged` l. 107–123 i při změně účtů `ai`; `shown()` l. 265–270, `availability` l. 280–293, `CanRunInApp` l. 328–333, `RefreshHandlers` l. 190–212, `problem` l. 364–383: účet `ai` jako ChatGPT (bez registrace mostu), `window/board_triage.go:110–129`, `board_suggest_reply.go:37,167`, `board_triage_button.go:125–150` (místo Install/Sign In tlačítko „Nastavení…“ → editor účtu), `window/assistant_panel.go:250–264` (patička podle `Provider.Name()`), `window/sync.go:349–362, 460–475` (`editorAI` → editor účtu; `signInAgain` pro `ai` otevře editor s Nahradit klíč…), `window/accounts_page.go:305–317, 391–399` (`editorAI`) | Panel, přepis, psaní, hledání, triage a Navrhnout odpověď fungují přes účet `ai` beze změny svých widgetů | `window/assistant_test.go` (výběr účtu `ai`: `CanRunInApp` bez registrace, `problem` texty), `board_triage_test.go` se `Source` `api:anthropic` |
| **E1.8 Integrace** | `ui/data/ui/preferences.blp` l. 58–74 (`integrations_group` zůstává pro účty; nová `integrations_local_group` pro lokální řádky; `integrations_available_group` dostane dvě sekce s nadpisy „Služby“ a „AI“ přes `Adw.PreferencesGroup integrations_ai_group`), `window/integrations_page.go` (`bindIntegrations` l. 29–41 staví obě sekce katalogu z `Catalog`; `addService` l. 61–70 += `AccountAI` → `addAIAccount(provider)`, `LocalClaude` → `editClaudeIntegration`, `LocalChatGPT` → `editChatGPTIntegration`; `setLocalRows` z `integrations.LocalRows`), nový `window/ai_editors.go` (`addAIAccount`, `editAIAccount` po vzoru `jira_editors.go`), nový `ui/data/ui/ai_account.blp` + `aiaccount/dialog.go` (`Adw.Dialog ai_account_dialog`: `kind_row`, `name_row`, `address_row` + `address_hint`, `key_row` + `key_button`, `test_button`, `models_row`, `board_model_row`, `http_banner`, `consent_group` se zaškrtávátkem, `save_button`), nový `ui/data/ui/claude_integration.blp` + `window/claude_integration.go` (dialog „Claude Code / Claude Desktop“: přesun `mcp_group` + `mcp_switch` + čekající restart a `assistant_claude_code` + `assistant_claude_choose` z AI; `bindMCP` l. 683–822 a `bindClaudeCode` l. 974–1077 se přestěhují sem beze změny logiky), nový `ui/data/ui/chatgpt_integration.blp` + `window/chatgpt_integration.go` (dialog „ChatGPT (Codex)“: přesun `chatgpt_group` řádků `codex_executable`, `chatgpt_connection`, `chatgpt_model`, `chatgpt_usage`; `bindChatGPT` l. 22–263 bez `assistant_provider`), `window/accounts_page.go:181–250` (`newAccountRow` umí `LocalRow`: bez přepínače, Nastavení, Odebrat = odregistrovat/odpojit), `window/accounts_reorder.go` (lokální řádky mimo přetahování), `window/devstart.go` (`ai-account`, `claude-integration`, `chatgpt-integration`), `ui/data/data.go:21` (tři nové `.blp`) | Řádek účtu `ai`: název · druh · model · „12,3 k tokenů za 24 h“ z `assistant.usage` (obnova na `notify.assistantUsageChanged`); Odebrat se zeptá větou s klíčem | `TestPreferencesIntegrationsGTKSmoke` rozšířit (sekce AI, lokální řádky, řádek `ai`), nový `TestAIAccountDialogGTKSmoke` (`aiaccount.New(nil, …)`, stránky, http varování, souhlas), `TestClaudeIntegrationGTKSmoke`, `TestChatGPTIntegrationGTKSmoke` (dnešní `TestPreferencesModelGTKSmoke` přesunout) |
| **E1.9 AI záložka** | `ui/data/ui/preferences.blp` l. 543–689 (`ai_page`: pryč `mcp_group`, `chatgpt_group`, řádky `assistant_provider` a `assistant_claude_code`; `assistant_group` = `assistant_source` (ComboRow „Asistent používá“), `assistant_menu_switch`, `assistant_target`, `assistant_model`; `board_triage_group` beze změny), `window/preferences.go` (`bindAssistant` l. 864–958 bez `bindChatGPT`/`bindClaudeCode`; nový `bindAssistantSource(s)` nad `assistant.ProviderChoices` (položky Claude Code, ChatGPT, každý zapnutý účet `ai`; nedostupná položka zašedlá s `Problem`); `bindProviderModel(row, purpose)` zobecněný z `bindBoardProviderModel` (`preferences_chatgpt.go:266–377`): Claude → výčet, ChatGPT → katalog Codexu, `ai` → `assistant.providerModels` + zápis `account.update {ai.model}` / `{ai.boardModel}`; `bindBoardTriage` l. 1233–1388 texty přes `providerBoardTranslator`), `window/preferences_chatgpt.go` (zbytek bez provider comba), hlavička `preferences.blp` l. 20–24 | Podtitulek „Zobrazovat nabídku Asistent“ odkazuje na Integrace místo na Registrovat v Claude | `TestPreferencesAIGTKSmoke` (nový: zdroj s účty `ai`, model podle zdroje, skupiny pryč) |
| **E1.10 Texty a překlady** | `po/POTFILES` (+ `aiaccount/text.go`, `assistant/api.go`), `make po` v kontejneru, `po/cs.po` česky pro všechny nové msgidy, `windows/parity-exclusions.txt` (nové msgidy s důvodem E3), `macos` nic | Žádný msgid jen pro GTK; texty souhlasů podle Q17 | `make lint` |
| **E1.11 Docs a CLAUDE.md** | `docs/mcp.md` (GTK věty v nových oddílech), `CLAUDE.md` odstavec (stav, brány, co čeká macOS a Windows), `macos/README.md` a `windows/README.md` beze změny do E2/E3 | — | — |

### 12.5 Ověření a brány etapy

1. Backend: `go vet ./...`, `go test ./...` (celé, s vědomím dvou
   macOS selhání), `pkg/api` kontrakty, `internal/rpc`, `internal/agent`
   s `-race` jen po dohodě s vlastníkem (`no-heavy-tests-on-users-machine`).
2. GTK v kontejneru: `scripts/gtk-container/gtk.sh make build`, celé
   `gtk.sh test-ui`, smoke testy `MALACHI_GTK_SMOKE=1` (nové čtyři
   + `TestConversationChatGTKSmoke` a panel), `make po` (jen nové
   msgidy a odkazy), `make lint`; po práci `gtk.sh stop`.
3. Ruční ověření vlastníka (brána B2, `make run-gtk-live` nad kopií
   dat, démon vlastník spouští sám): přidat účet „OpenAI-kompatibilní
   server“ na Ollamu na Macu (http, varování), Test, výběr modelu,
   souhlas; „Asistent používá“ = ten účet; panel (Shrnout, Napsat
   odpověď → karta konceptu), přepis a psaní v Nové zprávě, hledání
   vlastními slovy, ruční triage nástěnky (Zpět v Integracích ukáže
   spotřebu), Navrhnout odpověď; model bez nástrojů → věta
   `api_tools_unsupported`; účet Anthropic s klíčem vlastníka (klíč zadá
   on, nikdy do repozitáře) totéž v kratší podobě; Claude Code přes
   `assistant.prompt` dál funguje (regresní kontrola panelu); Integrace:
   lokální řádky Claude Code (registrace, přihlášení) a ChatGPT; Odebrat
   účet `ai`; dconf po aktualizaci drží `assistant-provider`.
4. Co se v E0+E1 neověřuje: macOS a Windows (E2, E3), MAS build,
   OpenAI Responses proti skutečnému klíči OpenAI (jen pokud vlastník
   klíč má; jinak zůstává u `llmtest`), automatická triage přes API
   (plánovač klientů beze změny, ale spustit jeden automatický běh
   ručně zkrácením intervalu).

### 12.6 Předávka pro E2 (macOS) a E3 (Windows)

- Zrcadla API: `AIConfig`, `AccountAI`, `CapabilityAssistant`,
  `PromptInput`, `PromptResult`, `AssistantEvent`, `AssistantUsageDay`,
  šest metod, dvě notifikace, pět chyb (`MalachiCore/API/*.swift`,
  `Malachi.Core/Api/*.cs`, `GoContractTests`).
- Port `apiassistant` jako `AssistantProvider` (Swift) /
  `IAssistantProvider` (C#) nad `RPCClient`/`RpcClient`; `Name` v
  rozhraní; `SessionSpec` s `purpose` a `input`; `ProviderFailure`
  kódy `api_*`.
- Prompty: smazat `AssistantPanel.swift` prompty, `AssistantRewrite`,
  `AssistantSearch`, `AssistantTriage`, `AssistantSuggestReply`,
  `AssistantProfile.withProfile` a C# `Assistant.*` ekvivalenty; místo
  nich `assistant.prompt`; golden z E1.1 slouží jako test portu
  (Swift i C# volají démona, takže test je proti falešnému démonu
  s pevnou odpovědí).
- Integrace: sekce AI v katalogu, lokální řádky (Claude Code/Desktop
  s Registrovat v Claude a restartem Claude Desktop, ChatGPT), řádek
  účtu `ai` se spotřebou, dialog účtu `ai` (sheet / `ModalDialog`),
  dialogy lokálních integrací z dnešních řádků AI.
- AI záložka: „Asistent používá“, Model podle zdroje, Nástěnka beze
  změny; `Settings.assistantProvider` řetězec (v MAS `claude`/`chatgpt`
  dál jako Claude Desktop, `acc_…` platí).
- MAS (E2): `Distribution.allowsLocalAgents` beze změny; cíl `app`
  nabídnout s účtem `ai`; Přidat… u lokálních položek → modal
  (`WebsiteFeaturesDialog` s textem z `assistant.APIText`); skupina
  „Další funkce asistenta“ a odkaz ve stavovém pruhu pryč; cesta
  k mostu pro démona = `malachi-mcp-worker` v pomocném bundlu
  (`Paths.bridgeWorkerExecutable`); smoke v App Store buildu ve VM nad
  kopií dat.
- Windows (E3): bez brány distribuce; `windows/parity-exclusions.txt`
  vyprázdnit od řádků E3; `IconGlyphs` pro `AIIcon`; UI smoke
  s falešným serverem (E0.11, pokud vznikne) nebo Ollamou.

## 13. As built (E0, 2026-11-07)

Démon podle §12.2, sestavený a otestovaný na Macu (`go build ./...`,
`go vet`, testy balíčků `pkg/api`, `internal/llm`, `internal/assistant`,
`internal/agent`, `internal/rpc`, `internal/store`, cílené `internal/core`
a `cmd/malachi-mcp`); celá sada backendu čeká na souhlas vlastníka.
Odchylky od návrhu a rozhodnutí při psaní:

- **Realm účtu `ai`** je `ai:<host[:port]/cesta>#<id účtu>`: tabulka
  `accounts` má UNIQUE (email, realm) i ve schématu, takže dva účty
  téhož poskytovatele potřebují různý realm; `store.RealmOf` sám (bez
  id) nadále jmenuje endpoint a core ho porovnává (`sameAIEndpoint`).
  Výjimka z kontroly unikátnosti v Go nebyla potřeba.
- **Kontrakt navíc**: třída selhání `limit` (tah překročil 50 kol
  nástrojů; relaci neukončí), `assistant.sessionStart.noteAccountId`
  (účet, jehož doplněk profilu se připojí u přepisu a psaní),
  `assistant.prompt.runId` (argumenty mostu triage nesou běh), výsledek
  testu `AITestResult` = `EndpointTestResult` + `models`.
- **Prompty** (`backend/internal/assistant`): jazyk je BCP 47 tag
  mapovaný na anglické jméno (`LanguageName`, dnes jen `cs`; ostatní =
  angličtina jako dnes); u triage spouštěč `auto` vynutí `drafts=false`,
  `maxCases` se ořeže na 1–200; `Context.folderId` se přijímá, ale
  preambule ho nejmenují (dnešní chování); text vstupů se čistí
  (`ToValidUTF8`, bez řídicích znaků kromě `\n` a `\t`) před týmiž
  funkcemi jako v referenci; golden `ui/internal/assistant/testdata/
  prompts.golden` (23 případů) a `TestParityWithTheGoReference` drží
  rovnost bajt po bajtu.
- **Klienti API** (`internal/llm`): Anthropic s `x-api-key`
  a `anthropic-version: 2023-06-01`, bez `strict`, `tool_choice`
  i `output_config` — schéma hledání jde pokynem na konci systémového
  promptu (klient JSON validuje); OpenAI Responses s `store: false`,
  `text.format json_schema`, položky `function_call` bez `id`
  (jen `call_id`), ukončení `response.completed` i `response.incomplete`,
  `response.refusal.*` jako odmítnutí — **názvy událostí Responses API
  nejsou ověřené proti živému API** (jen z dokumentace), první běh
  s klíčem OpenAI je proto zkouška; kompatibilní servery přes Chat
  Completions s jedním opakováním bez `response_format` po 400, `stop`
  s voláními nástrojů se bere jako `toolUse`; `Retry-After` bez hlavičky
  = 1 s, nad 30 s se neopakuje; 3xx se nikdy nenásleduje; pin
  certifikátu přes `transport.EndpointTLSConfig` jen pro https; chyba
  dopravy nese i `*api.Error` z `transport.Classify` (pin → `tlsError`).
  Falešné servery `llmtest` umí všechny tři dialekty (skript tahů, 401,
  `Retry-After`, oříznutý stream, nestreamovaná odpověď).
- **Smyčka** (`internal/agent`): `usage` se hlásí jednou po každém
  volání modelu (ne po výsledcích nástrojů), výsledek nese součet tahu;
  relaci ukončí třídy `auth`, `permission`, `toolsUnsupported`,
  `contextFull`, `bridge`, `stopped`; pořadí nástrojů je podle SDK
  (abecední `tools/list`); argumenty volání, které nejsou JSON, dostanou
  chybový výsledek bez mostu; `Session.Begin` dělá kontrolu obsazenosti
  synchronně a tah běží v goroutine (`sessionSend` tak vrací
  `assistantSessionBusy` spolehlivě).
- **RPC**: id spojení `c_<pořadí>` v kontextu každého volání
  (`api.WithConn`), `NotifyConn` jednomu spojení, zápis notifikace
  s limitem 10 s (po něm se spojení zavře), `ConnClosed` jen pro
  ověřená spojení po doběhnutí jejich volání.
- **Core**: `Backend.AIProvider` (hák pro testy, výchozí `llm.New`),
  `Backend.SocketPath` nastaví `malachid` po `Listen`; relace se
  zametají každou minutu (nečinnost 30 min, ukončené), spotřeba starší
  400 dní se maže hodinově; `board.runEnd` bez `usage` vezme součet
  relací démona s tím `runId` (D19); odebrání nebo pozastavení účtu `ai`
  ukončí jeho relace (`forgetAIAccount`); stav `authRequired`/`error`
  účtu `ai` podle posledního testu nebo relace (§6.1).
- **Most**: jediná změna je `list_accounts` bez účtů `ai`.
- **Neověřeno**: skutečný most spuštěný démonem jako podproces
  (`agent.StartBridge`; testy jdou přes in-memory transport), skutečné
  API poskytovatelů, zápis spotřeby z reálné relace. To vše ukáže E1
  s GTK nad Ollamou a klíčem vlastníka.

## 14. As built (E1, 2026-11-07)

Go reference a GTK podle §12.3 a §12.4, sestavené a otestované
v kontejneru (`gtk.sh`: `go build ./...`, celá sada `ui/`, 31 smoke
testů bez předem padajícího `TestBodyFitGTKSmoke`, `make po`, `make
lint`); čisté balíčky i na Macu. Nikdo to zatím neviděl na obrazovce
(brána B2 vlastníka). Odchylky a rozhodnutí:

- **Šev** (`assistantpanel`): `Provider` += `Name()`, `SessionSpec` nese
  `Purpose`, `Language`, `NoteAccountID`, `Trigger`, `RunID`,
  `BridgePath` i rozřešený prompt, `Session.Submit` bere
  `api.PromptInput`; nové `Prompter` (`assistant.prompt` přes RPC)
  v `Config`/`RequestConfig`; `Call.Tools` nese jen most a socket,
  zbytek dá `PromptResult`. Panel s více nesdělenými kontexty volá
  `assistant.prompt` jednou na kontext a řádky skládá jako dřív (byte
  přesně); kandidát: `PromptInput.contexts` v démonu.
- **Smazáno** z `ui/internal/assistant`: všechny prompty, seznamy
  nástrojů, argumenty mostu, `WithProfile`, `golden_test.go` (golden
  regeneruje `backend … parity_test.go -update`); zůstaly presety,
  texty, `ParseSearchQuery`, `TriageAnnotatedCase`, `UserMessage`, `Args`
  (nad `api.PromptResult`, `PrefixTools`). Codex (`chatgpt`) má kopie
  bare seznamů nástrojů v `toolPolicy` a ptá se Prompteru dvakrát na
  jednorázový požadavek.
- **`apiassistant`**: `failure` události mapuje na `EventFailure` +
  neúspěšný `EventResult` (démon po selhání výsledek neposílá);
  `AcceptConsent` zapisuje `account.update` na pozadí, `Open` na zápis
  počká; kódy `api_<class>` v `ProviderFailure` a `APIFailureText`.
- **Integrace**: lokální řádky Claude Code/Desktop a ChatGPT jsou
  v Připojených jen připojené (bridge registrovaný / ChatGPT připojený;
  `integrations.LocalRowFor` vrací řádek a zda se ukazuje, `LocalRows`
  jen ukazované, `Offered` nechá v katalogu AI jen nepřipojené; GTK drží
  widget na druh skrytý a skupinu i zástupný řádek „Zatím žádné
  integrace“ podle toho; původně byly oba řádky vždy a zároveň
  v katalogu, vlastník to 2026-11-07 shledal matoucím; msgid „Not
  registered“ zmizel; kapsle „This computer“ / „API key“ (`Service.Badge`,
  `Row.Badge`, `LocalRow.Badge`, GTK `widget.NewSourcePill` ve stylu
  neutrální pilulky); katalog v jedné skupině `integrations_available_group`
  z `integrations.Catalog`, `integrations_ai_group` a `PageTexts.AITitle`
  zrušené; dialogy `claude_integration.blp`,
  `chatgpt_integration.blp` přesunuté z AI záložky, Odebrat
  = odregistrovat / odpojit s potvrzením);
  řádek účtu `ai` ukazuje „%s tokens today“ (účtování po místních dnech,
  `assistant.usage {days: 0}`), ne 24 h; dialog `ai_account.blp` nad
  `aiaccount.Controller` s popovery modelů, pinem přes dialog důvěry
  průvodce, souhlasem jako `SwitchRow`.
- **AI záložka**: `assistant_source` nad `assistant.ProviderChoices`
  (nedostupné položky zašedlé s problémem), `bindProviderModel` společný
  pro panel i nástěnku (účet `ai` zapisuje `account.update` bez
  credentials, `pending` do `notify.accountsChanged`); souhlas nástěnky
  nad účtem `ai` = souhlas účtu (bez klíče gschema), vypnutí přepínače
  nástěnky jen vypne preferenci `assistant` démona.
- **Flatpak**: `Assistant.daemonBridge` najde `malachi-mcp` i ve
  Flatpaku (démon ho spouští uvnitř sandboxu).
- **Windows do E3**: 90 msgidů ve `windows/parity-exclusions.txt`;
  změněný popis Integrací („… CRM and AI“) a nepoužitý „In-app provider“
  shodí `build.ps1 lint`, dokud E3 nepřejde; `windows/src` dál používá
  výčet `AssistantProviderID`.
- **Nálezy B2** (2026-11-07, vlastník na Fedoře přes linuxového agenta
  nad kopií dat, démon 5c9b39c1, klient 786d3022): (1) Test účtu AI
  nešel spustit bez modelu, ač model se vybírá až z jeho výsledku —
  rozhodnutí vlastníka: Test s prázdným modelem u všech tří druhů, model
  povinný až pro Uložit; opraveno v démonu
  (`validateAccountConfigFor(c, forTest)` → `validateAIConfig(c,
  modelRequired)`, `account.test` model nevyžaduje, neplatný dál
  odmítne, `docs/api.md` §4.1) i v kontroleru (`check(forSave, forTest)`,
  `RunTest` přeskočí `ModelMissing`; test `TestTestNeedsNoModelYet`);
  pole Model zůstává i ručně zapisovatelné (server, který modely
  nevypisuje). Postřeh agenta bez změny: odmítnutý Test ukazuje jen
  červený řádek a banner, nic u tlačítka Test. (2) Nabídka modelů pod
  šipkou řádku Model byla široká dva znaky (popover se smrskl na
  elipsované minimum řádků): `ScrolledWindow` má
  `SetPropagateNaturalWidth(true)` s mezemi 280–520 px
  (`modelPopoverMinWidth`/`MaxWidth`). (3) Rozhodnutí vlastníka: dokud
  Test neprojde, jsou skupina Model, souhlas a Uložit zašedlé, dostupné
  zůstávají název, server, klíč, pin a Test (`Controller.Connected`:
  úspěšný test dialogu, zpět ho vezme změna klíče či serveru;
  `Unlocked`, `CanSave`, `Save` bez spojení nic nedělá). Předpoklad
  pro editaci, kterou vlastník nespecifikoval: uložený účet na svém
  klíči a serveru se počítá jako připojený (připojil se při přidání),
  Nahradit klíč… nebo jiný server spojení vezmou zpět do nového Testu.
  (4) Integrace: lokální řádky, kapsle a jedna skupina katalogu
  (výše). (5) Vlastník přidal účet Anthropic (Test bez modelu, výběr,
  souhlas, Uložit prošly; most jako podproces démona nastartoval se 14
  nástroji — první ověření skutečného `agent.StartBridge`), ale tah
  panelu padal třídou `invalidRequest`: Anthropic odpovídá klíči bez
  kreditu 400 `invalid_request_error` „Your credit balance is too low…“,
  zatímco `GET /v1/models` (Test) bez kreditu projde, takže uživatel
  z třídy nepoznal, že má dobít. Rozhodnutí vlastníka (podle
  doporučení): nová třída `quota` (`api.FailureQuota`): Anthropic
  `billing_error` nebo 400/429 s větou o kreditu či kvótě
  (`llm.mentionsQuota`), OpenAI a kompatibilní servery 429
  `insufficient_quota` (zjemnění běží i u 429, ne jen u 400); jediné
  opakování 429 se u ní nedělá; `ToAPIError` → `serverError`; stav účtu
  `error` jako u `invalidRequest`; relaci neukončuje. Klient: věta
  „The account at the provider has no credit or has used up its quota.
  Top it up with the provider, then try again.“ (`APIFailureStrings.Quota`,
  `api_quota` je v panelu `FailureLimit` jako `api_rateLimit`, tedy
  s nabídkou Zkusit znovu); věta poskytovatele se v UI neukazuje
  (cizí text). (6) Dvě drobnosti z téhož běhu: klient posílal
  `assistant.sessionStop` až po zavření spojení (démon relaci ukončí
  sám, jen log hlásil „use of closed network connection“) —
  `apiassistant.Sessions.Wait` počítá rozběhnuté `sessionStop` a
  `Assistant.WaitAPISessions` (main.go, před `rpc.Close()`, nejvýš 2 s)
  je nechá dojít; a po tahu bez tokenů (odmítnutém před odpovědí modelu)
  démon zapisoval řádek spotřeby a posílal `notify.assistantUsageChanged`
  — teď nic nezapíše ani neoznámí, relace se do počtu relací dostane
  s prvním tahem, který tokeny měl (`sessionInfo.counted`).
  (7) Po tahu bez kreditu stál ve stavovém pruhu „Sync error: claude!“
  a v nabídce synchronizace řádek účtu AI s Try Again (kontrola pošty),
  v klidu by tam měl „Up to date“ s tlačítkem kontroly. Rozhodnutí
  vlastníka (podle doporučení): stav účtu `ai` je jen `authRequired`
  po odmítnutém klíči (401 z testu nebo tahu), jiná odpověď
  poskytovatele ho vrátí na `idle` (klíč prošel), selhání před
  odpovědí (síť, timeout, TLS, most, stop) stav nemění; `error` se
  u účtu `ai` nenastavuje nikdy (`aiOutcome` vrací i `known`;
  `docs/api.md` §4.1). Klient: stavový pruh a nabídka synchronizace
  účet `ai` vynechají, dokud není `authRequired` (`inStatusLine`;
  Přihlásit se → editor s Nahradit klíč…), pozastavený nikdy; řádek
  v Integracích dál ukazuje Vyžaduje přihlášení. macOS a Windows to
  přebírají v E2/E3 (jejich stavový pruh a nabídka synchronizace).
  Body (5) a (6) agent ověřil nad `fe89d973` sondou i s vlastníkem
  na obrazovce (třída `quota`, `sessionStop` před zavřením spojení,
  spotřeba beze změny); bod (7) vlastník schválil na obrazovce nad
  `b2b9d13d`. (8) LM Studio (`google/gemma-4-e2b`, OpenAI-kompatibilní
  účet přes http): Test prošel, každý tah panelu padl 400
  `invalidRequest`; agent sondou zjistil, že validátor LM Studia
  vyžaduje u každé funkce `parameters.properties`, a tři nástroje
  mostu bez argumentů (`get_user_profile`, `list_accounts`,
  `list_rules`) mají schéma `{"type":"object","additionalProperties":false}`
  (SDK MCP ho tak generuje; OpenAI i Anthropic ho berou). Jednorázové
  účely bez nástrojů (přepis, hledání vlastními slovy) s touž Gemmou
  fungovaly. Oprava v démonu: `llm.toolSchema` doplní objektovému
  schématu bez `properties` prázdné `"properties": {}` ve všech třech
  dialektech (test `TestToolSchemasAlwaysHaveProperties`); strop
  technické zprávy selhání `maxMessageBytes` zvednutý z 200 na 1000 B,
  aby výpis validátoru nekončil za první položkou (zpráva se nikdy
  neukazuje, jen jde klientům jako technický text).
- **Neověřeno**: zbytek B2 (model, panel s kreditem, přepis, hledání,
  triage, Navrhnout odpověď), živá API Anthropicu a OpenAI s kreditem
  (zatím jen odmítnutí 400 bez kreditu).
