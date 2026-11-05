# Malachi Mail v Mac App Store — analýza

Stav: analýza (2026-11-05), nic se neimplementovalo. Rozhodnutí vlastníka:
nejdřív analýza; **obě varianty vedle sebe** (Q1, 2026-11-05, po této
analýze; původní záměr „jen App Store“ padl kvůli §3.3): DMG s Developer ID
zůstává plná verze, App Store je druhý kanál s funkcemi, které sandbox
nedovolí, vypnutými. Otevřené otázky jsou v §7.

Podklady: `macos/Makefile` (cíl `app`), `macos/Resources/*.entitlements`,
`docs/macos-port.md` §12 („Not done: App Sandbox…“), `docs/releasing.md`
§8, `docs/plans/update-check.md` U6, `LICENSING.md`, `CLA.md`.

## 1. Shrnutí

Technicky to jde a démon se skoro nemění (pravidlo 4 drží: všechno, co se
liší, je parametr, který mu předá macOS klient). Cena je ve třech věcech:

1. **App Sandbox na každé binárce v bundlu.** `malachid`,
   `malachi-keychain` a `malachi-mcp` dnes běží bez sandboxu, data jsou
   v `~/Library/Application Support/Malachi Mail/` a socket
   v `~/.cache/malachi/run/`. V sandboxu se obojí přestěhuje do kontejneru
   aplikace a vše mimo něj je zavřené.
2. **Asistent nad uživatelovým Claude Code a Codexem nejde.** Panel „V
   aplikaci“, přepis a psaní v okně Nová zpráva, hledání vlastními slovy,
   triage nástěnky, Navrhnout odpověď a poskytovatel ChatGPT spouštějí
   `claude` / `codex` z domovského adresáře uživatele. Proces spuštěný ze
   sandboxu sandbox dědí: binárku v `~/.local/bin` nepřečte, a i kdyby,
   nedostane se k vlastnímu přihlášení (`~/.claude`, jeho položky
   klíčenky). Ve verzi z App Store zůstane jen úroveň A (předání do Claude
   Desktop / Claude Code odkazem `claude://`) — a i ta potřebuje registraci
   mostu MCP, viz bod 3. DMG si všechno nechává.
3. **Most MCP a jeho registrace.** Claude Desktop a Claude Code spouštějí
   `…/Malachi Mail.app/Contents/MacOS/malachi-mcp` samy, ne naše aplikace.
   Binárka v bundlu z App Store musí mít sandbox; s pouhým
   `com.apple.security.inherit` by spuštěná cizím procesem spadla. Musí
   mít vlastní sandbox a socket démona musí ležet ve **sdíleném kontejneru
   skupiny** (`application-groups`), ne v kontejneru aplikace. Zápis
   registrace do `~/Library/Application Support/Claude/claude_desktop_config.json`
   a `~/.claude.json` (`malachi-mcp install`) sandbox nedovolí bez
   výjimky nebo bez výběru souboru uživatelem (§3.2).

Ostatní je práce bez rizika: entitlements, migrace dat z DMG verze,
privacy manifest, podpis Apple Distribution, balíček `.pkg`, nový workflow
a metadata v App Store Connect.

## 2. Co App Store vyžaduje

| # | Požadavek | Stav dnes | Co udělat |
|---|---|---|---|
| S1 | App Sandbox (`com.apple.security.app-sandbox`) na aplikaci i každé vnořené binárce | žádný | Entitlements v §4 |
| S2 | Podpis certifikátem **Apple Distribution**, `.pkg` podepsaný **Mac Installer Distribution**, vložený provisioning profile (`embedded.provisionprofile`), entitlements `com.apple.application-identifier` a `com.apple.developer.team-identifier` (tým `PG9F7XU6X8`) | Developer ID + notarizace v CI | Nový cíl a workflow (§5) |
| S3 | Žádný vlastní mechanismus aktualizací | kontrola verzí fáze 1 | Sestavení pro App Store **bez kanálu** (`UPDATE_CHANNEL` prázdný): démon nekontroluje, UI pruh nemá — přesně U6 plánu `update-check.md`, jen ověřit, že pruh, řádek v O aplikaci a Předvolby → Aktualizace se bez kanálu skryjí |
| S4 | Privacy manifest `PrivacyInfo.xcprivacy` s „required reason“ API | žádný | Projít: `UserDefaults` (CA92.1), časy souborů (C617.1), volné místo na disku (`system.storage`, E174.1), čas od startu systému (Go runtime volá `mach_absolute_time`, 35F9.1); manifest v `Contents/Resources` |
| S5 | Vyplněný štítek soukromí v App Store Connect | — | Vývojář nesbírá nic; pošta jde jen na servery uživatele, Microsoft 365 přes Graph. Odkaz na zásady soukromí (stránka z `10c96d9e`) |
| S6 | Export šifrování (`ITSAppUsesNonExemptEncryption`) | — | Šifrování je vlastní (Go TLS, Ed25519, ověřování S/MIME a OpenPGP), ne jen systémové; standardní algoritmy pro hromadný trh obvykle stačí odpovědět „výjimka“, ale odpověď dává vlastník (Q5) |
| S7 | Binárky postavené s aktuálním SDK | `set_sdk` jen na hlavní binárce (paměť: SwiftPM zapisuje sdk 14.0) | Ověřit validací `.pkg` (`xcrun altool --validate-app` / Transporter), jestli kontrola SDK platí i pro `malachid`, `malachi-mcp`, `malachi-keychain`; případně `set_sdk` na všechny |
| S8 | Pravidla review: 2.5.2 (žádný stažený spustitelný kód), 4.2, 5.1.1, 5.1.2(i) (sdílení dat s AI třetí strany jen se souhlasem) | PDFium wasm je v bundlu, ne stahovaný; souhlasy asistenta existují | Popsat v poznámce pro review, co dělá démon a most a že AI je volitelná |

## 3. Co sandbox rozbije

### 3.1 Bez větších potíží (jen entitlement nebo malá změna)

| Funkce | Kde | Řešení |
|---|---|---|
| Spuštění `malachid` z bundlu, helper klíčenky z démona | `DaemonSupervisor.swift`, `MALACHI_KEYRING_HELPER` | `app-sandbox` + `inherit` na obou; dědí síť i kontejner |
| IMAP, SMTP, Graph, Jira, CalDAV, eWay, obrázky, kontrola podpisů | démon | `network.client` |
| Přihlášení v prohlížeči (listener na `127.0.0.1`) | `oauth2flow` | `network.server` (dědí démon) |
| Data a socket | `Daemon/Paths.swift` | `HOME` sandboxu je kontejner, cesty se přestěhují samy; socket viz §3.2 |
| Otevření a náhled příloh, Quick Look | dočasný adresář pro otevírání | uvnitř kontejneru funguje; quarantine přidá sandbox sám |
| Uložit jako, Uložit vše, přiložit soubor | `NSSavePanel` / `NSOpenPanel` | `files.user-selected.read-write` |
| Tisk | `Printing/` | `com.apple.security.print` |
| Spuštění po přihlášení | `LoginItemService` (`SMAppService.mainApp`) | funguje beze změny |
| Notifikace, odkazy `claude://`, `mailto:` | `NSWorkspace` | funguje |
| Restart aplikace | `App/Relaunch.swift` (`/bin/sh` + `/usr/bin/open`) | ze sandboxu nespolehlivé; nahradit `NSWorkspace.openApplication(at:configuration:)` s `createsNewApplicationInstance` až po ukončení, nebo restart nabízet jen jako „Ukončit“ |
| `config.toml` s vlastním klientem Gmailu | `~/Library/Application Support/Malachi Mail/config.toml` | přesune se do `~/Library/Containers/io.github.schotek.Malachi/Data/…`; README a Předvolby musí říct novou cestu (nebo tlačítko „Zobrazit ve Finderu“) |

### 3.2 Most MCP (rozhoduje Q2)

- **Socket do skupinového kontejneru.** Entitlement
  `com.apple.security.application-groups` = `PG9F7XU6X8.io.github.schotek.Malachi`
  na aplikaci (dědí démon) i na `malachi-mcp`; socket v
  `~/Library/Group Containers/PG9F7XU6X8.io.github.schotek.Malachi/run/rpc.sock`.
  Délka cesty: s uživatelem `schotek` asi 95 bajtů, limit macOS je 103
  (CLAUDE.md, poznámka o AF_UNIX na Windows). Pro dlouhá jména
  uživatelů je to málo — kratší podadresář (`r/s`) a kontrola při startu
  jako na Windows. Démon dostane cestu přes `--socket`, most přes
  `MALACHI_SOCKET` zapsané v registraci; démon se nemění.
- **`malachi-mcp` s vlastním sandboxem** (ne `inherit`): `app-sandbox`,
  `application-groups`, `network.client` (stahování příloh přes démona je
  socket, síť ani nepotřebuje — ověřit), a dnešní
  `cs.allow-unsigned-executable-memory` kvůli wazero. **Neověřené:** zda
  App Review entitlement `allow-unsigned-executable-memory` u App Store
  binárky přijme (u App Store se hardened runtime nevyžaduje, takže
  v sandboxu bez hardened runtime může být zbytečný — změřit, jestli
  wazero pod sandboxem bez něj PDF přečte).
- **Příloha jako soubor pro Cowork / pracovní adresář Claude Code** (most
  ukládá přílohu do adresáře klienta): ze sandboxu mostu do cizího
  adresáře nezapíše. Buď zůstane jen text přílohy (`get_attachment`),
  nebo soubor ve skupinovém kontejneru a cesta k němu.
- **Registrace v Claude Desktop a Claude Code** — tři možnosti:
  - (a) *Dočasná výjimka* `temporary-exception.files.home-relative-path.read-write`
    na `/Library/Application Support/Claude/` a `/.claude.json`.
    Nejméně práce, Předvolby → AI zůstanou, jak jsou. Riziko zamítnutí
    v review; Apple výjimky schvaluje jen s odůvodněním.
  - (b) *Výběr souboru uživatelem*: přepínač Registrovat v Claude otevře
    `NSOpenPanel` na konfiguraci Claude, aplikace si uloží
    security-scoped bookmark a `install` dostane deskriptor/cestu.
    Review-bezpečné, o krok víc pro uživatele, a `~/.claude.json` je
    skrytý soubor (panel s `showsHiddenFiles`).
  - (c) *Jen návod*: Předvolby ukážou JSON k vložení a příkaz
    `claude mcp add …`. Nejjednodušší, nejhorší pro uživatele.

  **Rozhodnutí (Q2, 2026-11-05): (a)**, při zamítnutí v review přechod
  na (b). Pozor na zápis: `malachi-mcp install` zapisuje atomicky přes
  `os.CreateTemp` vedle cíle a `os.Rename` (`cmd/malachi-mcp/setup.go`).
  V `~/Library/Application Support/Claude/` to s výjimkou na adresář
  projde; u `~/.claude.json` by dočasný soubor vznikal v domovském
  adresáři, kam výjimka na jeden soubor nepustí. Návrh k ověření v A2:
  pevné jméno dočasného souboru (`~/.claude.json.malachi.tmp`) jako druhá
  cesta ve výjimce a přejmenování mezi dvěma povolenými cestami; změna
  v mostu je platformně neutrální (parametr nebo deterministické jméno
  pro všechny platformy). Když přejmenování sandbox nedovolí, zůstane pro
  Claude Code (c) a nikdy přepis na místě (Claude Code ten soubor sám
  často přepisuje). Pro (b) platí: Claude Desktop výběrem adresáře, ne
  souboru (Claude ho za běhu nahrazuje novým souborem).
- **Restart Claude Desktop** (`ClaudeDesktopService`, Apple events,
  `NSRunningApplication.terminate`): v sandboxu jen s výjimkou
  `temporary-exception.apple-events` na bundle Claude. Doporučení:
  vypustit a říct uživateli „Restartujte Claude Desktop“.

### 3.3 V App Store verzi nejde (DMG zůstává beze změny)

Všechno, co spouští uživatelův `claude` nebo `codex`
(`ClaudeCodeLocator`, `ClaudeCodeProcess`, `CodexProvider`,
`AssistantPanelController`, `AssistantRequest`, `BoardTriageController`,
`BoardAutoTriageScheduler`, `BoardReplyController`, přihlášení
`claude auth login`):

- cíl asistenta „V aplikaci (experimentální)“ a jeho panel (B1),
- přepis v okně Nová zpráva, pruh psaní a hledání vlastními slovy (B2),
- triage nástěnky (ruční i automatická) a Navrhnout odpověď/připomenutí,
- poskytovatel ChatGPT/Codex,
- přihlášení Claude Code z aplikace.

Proč ne výběr binárky uživatelem: spuštěný `claude` by zdědil sandbox,
neviděl by `~/.claude` ani svou položku klíčenky a nepřihlásil by se;
navíc by spouštění uživatelem zvoleného programu v review neprošlo
(2.5.2). Nástěnka sama (pravidla démona, ruční stavy, remindy, archivace)
funguje dál, jen bez triage.

Sestavení pro App Store tyto části nesmí nabízet (DMG je nabízí dál):
přepínač v Swiftu
(klíč v `Info.plist`, třeba `MalachiDistribution = appstore`, čtený
v `MalachiCore`) skryje cíl „V aplikaci“, poskytovatele ChatGPT,
tlačítko ✦ Triage, Navrhnout odpověď a skupinu Board v Předvolbách → AI.
Ošetření v kódu (fáze A2), ne jen skryté ovládací prvky:

- Pravidlo dostupnosti v `MalachiCore` (třeba `Distribution.current`
  z `Info.plist` a `Distribution.allowsLocalAgents`), testované
  `swift test` pro obě hodnoty; pohledy se ptají jen jeho.
- Kontrolery se samy odmítnou rozběhnout, ne až UI:
  `ClaudeCodeLocator`, `AssistantPanelController`, `AssistantRequest`,
  `CodexProvider`, `BoardTriageController`,
  `BoardAutoTriageScheduler`, `BoardReplyController`.
- Uložené hodnoty z DMG (přenesené migrací kontejneru) se čtou bezpečně:
  `assistant-target = inapp` jako Claude Code / Claude Desktop, poskytovatel
  ChatGPT jako Claude, automatická triage jako vypnutá. Klíče se
  nepřepisují, aby návrat k DMG nic neztratil.
- Předvolby → AI místo skupiny Board a řádků Claude Code, Model
  a ChatGPT ukážou jednu větu, že asistent v aplikaci a triage nástěnky
  jsou jen ve verzi z webu, s odkazem na web. Text je msgid v `po/`
  s češtinou (Q8); protože ho GTK nepoužívá, nese ho Go reference (třeba
  `ui/internal/assistant`, soubor v `po/POTFILES`) a Windows ho má
  s důvodem ve `windows/parity-exclusions.txt`.

Je to platformní kód jen v `macos/`, ne větvení démona ani GTK (pravidlo
4); GTK a Windows se nemění (pravidlo 6 se netýká, jde o odchylku
distribuce — patří do tabulky v `macos/README.md`).

## 4. Entitlements

| Binárka | Entitlements |
|---|---|
| `MalachiMail` (aplikace) | `app-sandbox`, `network.client`, `network.server`, `files.user-selected.read-write`, `print`, `application-groups`, `application-identifier`, `team-identifier`, `keychain-access-groups` (viz §6), + podle Q2 výjimky |
| `malachid` | `app-sandbox`, `inherit` |
| `malachi-keychain` | `app-sandbox`, `inherit` |
| `malachi-mcp` | `app-sandbox`, `application-groups`, (`network.client`?), (`cs.allow-unsigned-executable-memory`?) — spouští ho Claude, ne aplikace, takže ne `inherit`; jeho podřízený proces `__extract` je táž binárka a sandbox mostu zdědí |

Dnešní `com.apple.security.automation.apple-events` z
`MalachiMail.entitlements` odpadá (Q2).

## 5. Sestavení, podpis a CI

- `make macos-mas` vedle `macos-dmg` (jen na Darwinu): universal bundle
  jako dnes, entitlements z `Resources/mas/*.entitlements`,
  `embedded.provisionprofile`, `PrivacyInfo.xcprivacy`, podpis Apple
  Distribution zevnitř ven (helpery, pak aplikace), `productbuild
  --component … /Applications --sign "3rd Party Mac Developer Installer…"`
  → `Malachi-Mail-<verze>-macos-universal.pkg` (pojmenování podle
  `releasing.md` §1). Bez `UPDATE_CHANNEL`.
- Verze: App Store chce `CFBundleShortVersionString` jako `MAJOR.MINOR.PATCH`
  a rostoucí `CFBundleVersion` — `Info.plist.in` dnes dostává
  `@FULL_VERSION@` z `git describe`; pro App Store číslo buildu z běhu CI.
- Workflow `macos-mas.yml` (jen u tagu a ručně, `ci-only-essentials`):
  nové secrets — certifikát Apple Distribution, Mac Installer
  Distribution (p12 + heslo) a provisioning profile; klíč App Store
  Connect API z `MACOS_*` lze použít i pro nahrání (role Developer
  nahrávat smí). Varianta jako MSIX: artefakt `.pkg` na 30 dní a ruční
  nahrání přes Transporter, nebo rovnou `xcrun altool --upload-package`
  do TestFlight (Q4). Kroky se musí nejdřív ověřit ručně na Macu, ne
  pokusy v CI (`no-ci-trial-and-error`).
- `macos.yml` (DMG, Developer ID, notarizace, kanál aktualizací) zůstává
  beze změny; `releasing.md` §8 dostane druhou podsekci, README a web
  nabídnou obě cesty s vysvětlením rozdílu (App Store: aktualizace
  obchodem, sandbox, bez asistenta v aplikaci; DMG: vše).

## 6. Dvě varianty vedle sebe a přechod mezi nimi

Varianty mají stejné bundle ID (`io.github.schotek.Malachi`, Q7
rozhodnuta), takže
na Macu má být nainstalovaná jen jedna; data mají oddělená (DMG
`~/Library/Application Support/Malachi Mail`, App Store kontejner).

- **Registrace mostu:** obě by v Claude zapsaly server téhož jména
  s jinou cestou a jiným `MALACHI_SOCKET`; platí ta zapsaná naposled.
  `malachi-mcp status` musí poznat cizí cestu a Předvolby → AI ji ukázat
  jako „registrováno jinou instalací“, ne jako své.
- **Kanál aktualizací:** DMG ho má dál; App Store žádný (S3).
- **Odchylky:** řádek „Mac App Store“ v tabulce `macos/README.md`
  s výčtem §3.3; Předvolby → AI ve verzi z App Store krátce řeknou, že
  asistent v aplikaci je jen ve verzi z webu.

Přechod je dobrovolný. Kdo z DMG přejde do App Store:

- **Data:** sandboxovaná aplikace starý adresář nevidí. Apple na to má
  `container-migration.plist` v `Contents/Resources`: při prvním startu
  přesune `~/Library/Application Support/Malachi Mail` do kontejneru.
  Starý socket a klíč v `~/.cache/malachi/run` se nepřenáší (vzniknou
  nové). Předvolby `UserDefaults` domény `io.github.schotek.Malachi`
  migrace přenese také (stejné bundle ID). Ověřit nad kopií dat.
- **Klíčenka:** helper ukládá přes `SecItem*` bez data protection
  keychain, tedy do přihlašovací klíčenky s ACL podle podpisu binárky.
  Binárka podepsaná pro App Store má jiný podpis → při prvním čtení se
  macOS zeptá na každou položku (jako po ad-hoc rebuildu), a v sandboxu
  možná nepůjde vůbec. Čistší je přejít v App Store verzi na data
  protection keychain se skupinou `keychain-access-groups`
  (`PG9F7XU6X8.io.github.schotek.Malachi`) a jednorázově přenést staré
  položky (jedna otázka na položku), nebo nechat uživatele znovu se
  přihlásit. Zkoušet jen s povolením vlastníka (`no-keychain-experiments`).
  **Rozhodnutí (2026-11-05, vlastník):** zůstává přihlašovací klíčenka
  přes stávající `malachi-keychain`, bez data protection keychain a bez
  přenosu položek. Vlastníkův test ve VM (jen App Store build, podpis
  Apple Development): sandboxovaný helper položku účtu uložil a po
  restartu VM ji přečetl bez dotazu. Kdo přejde mezi verzemi, založí
  účty znovu (jiný kontejner i jiný podpis); dialog Více AI funkcí to
  říká. Případný importní průvodce v DMG verzi je pozdější samostatná
  práce.
- **Most:** stávající registrace v Claude ukazují na cestu
  `/Applications/Malachi Mail.app/…/malachi-mcp` bez `MALACHI_SOCKET`;
  po přechodu je aplikace musí přepsat (podle Q2).
- Migrace kontejneru proběhne jen jednou a starý adresář přesune; kdo by
  se pak vrátil k DMG, data tam nenajde. Ověřit, jestli
  `container-migration.plist` umí kopii místo přesunu, jinak to říct
  v poznámkách k vydání.

## 7. Otázky na vlastníka

- **Q1 — odpovězeno 2026-11-05: obě varianty.** DMG plná, App Store
  bez §3.3.
- **Q2 — odpovězeno 2026-11-05: (a) dočasná výjimka**, při zamítnutí
  (b); podrobnosti v §3.2.
- **Q3 — odpovězeno 2026-11-05: zdarma, kategorie Productivity.**
- **Q4 — odpovězeno 2026-11-05: CI rovnou do TestFlight** (u tagu
  `xcrun altool --upload-package` s klíčem App Store Connect API;
  odeslání do review dělá vlastník ručně). Ruční spuštění workflow
  jen sestaví a zvaliduje `.pkg`, nic nenahrává.
- **Q5 — odpovězeno 2026-11-05: bod 2, standardní algoritmy místo
  šifrování systému Apple / vedle něj** (Go `crypto/tls`, S/MIME
  a OpenPGP přes Go a `ProtonMail/go-crypto`, Ed25519; nic
  nestandardního). Vlastník projde dotazník při zakládání záznamu (A0);
  když nebude chtít žádný dokument, `Info.plist` App Store verze dostane
  `ITSAppUsesNonExemptEncryption` podle té odpovědi, aby se Apple
  neptal u každého buildu. Kdyby chtěl dokument (třeba kvůli Francii),
  vyřešit dřív, než se Francie zařadí mezi země prodeje.
- **Q6 — odpovězeno 2026-11-05: ano, oddíl v `LICENSING.md`.** Přijde
  s prvním buildem v TestFlight (A5), jako u Windows „první vydání, které
  ho nese“; místo: za „Microsoft components in the Windows client“, před
  „Third-party components“. Návrh textu (vlastník ho před zápisem
  schválí; není to právní posudek):

  > ## The Mac App Store build
  >
  > The copyright holder also distributes the macOS app through the Mac
  > App Store (from release X.Y.Z). That copy is built from the same
  > tagged source as the other packages, with the App Store's sandbox and
  > signature and without the features listed in `macos/README.md`. Its
  > use is governed by Apple's Licensed Application End User License
  > Agreement, which applies only to the copy obtained from the App Store;
  > the copyright holder grants it under the rights the Contributor
  > License Agreement gives them (`CLA.md` §2).
  >
  > This does not limit the licences of the source: everyone keeps the
  > rights of the AGPL-3.0-only (`backend/`) and the GPL-3.0-or-later
  > (everything else) to the source of every release in this repository
  > and to builds made from it. The Corresponding Source of the App Store
  > build is the tagged release named in its About window.

  Původní otázka: vlastník má díky CLA (§2: „under any other licence“)
  právo vydat binárky pod licenčními podmínkami Apple; zdroj zůstává
  GPL/AGPL. Přidat do `LICENSING.md` oddíl o App Store verzi (jako
  doplňkové svolení pro Windows), aby bylo jasné, že App Store EULA
  se vztahuje jen na binárky z obchodu? Závislosti mostu a démona
  (`THIRD-PARTY-NOTICES.md`) jsou permisivní (BSD, MIT, Apache, MPL),
  žádná GPL třetí strany v bundlu.
- **Q7 — odpovězeno 2026-11-05: stejné jako DMG**
  (`io.github.schotek.Malachi`). Původní otázka: stejné pro obě
  varianty (jedna instalace naráz, předvolby a jméno v Launch Services
  sdílené), nebo App Store s vlastním (`io.github.schotek.Malachi.mas`,
  obě naráz, ale CLAUDE.md zakazuje App ID měnit bez zadání)?
  Doporučení: stejné.
- **Q8 — odpovězeno 2026-11-05: msgid** v `po/` s češtinou a výjimkou
  pro Windows.

## 8. Fáze

| Fáze | Obsah | Ověření |
|---|---|---|
| A0 | Odpovědi Q2–Q7, App ID a skupina v Developer portálu, záznam aplikace a jméno „Malachi Mail“ v App Store Connect | vlastník |
| A1 | Sandboxované sestavení lokálně: entitlements, socket ve skupinovém kontejneru, `container-migration.plist`, `Relaunch`, `config.toml` | debug build nad kopií dat (`MALACHI_DATA_DIR`), nikdy nad ostrými daty ani `build/Malachi Mail.app` |
| A2 | Přepínač distribuce v `MalachiCore`, skrytí §3.3, registrace mostu podle Q2, `malachi-mcp` se sandboxem a čtení PDF | `swift test`, ruční průchod Předvoleb → AI a Claude Desktop |
| A3 | Klíčenka: zrušeno (2026-11-05, rozhodnutí vlastníka), viz §6 Klíčenka | test vlastníka ve VM |
| A4 | `make macos-mas`, privacy manifest, validace `.pkg`, pak `macos-mas.yml` | `altool --validate-app` lokálně, pak jeden běh CI |
| A5 | TestFlight pro Mac, poznámka pro review, odeslání | vlastník |
| A6 | Po schválení: `releasing.md` §8 s oběma variantami, README, web, řádek v `macos/README.md`; DMG beze změny | vlastník |

### 8.1 Stav A0 (2026-11-05)

Hotovo (agent v prohlížeči, vlastník přihlášený):

- App ID `io.github.schotek.Malachi` („Malachi Mail“, explicitní) se
  schopností App Groups. Skupina z §3.2 má prefix týmu
  (`PG9F7XU6X8.io.github.schotek.Malachi`), takže se v portálu
  neregistruje (portál bere jen `group.…`). Vývojový profil ji povoluje
  sám: nese `com.apple.security.application-groups` `PG9F7XU6X8.*`
  a `keychain-access-groups` `PG9F7XU6X8.*` (distribuční ověří A4). Jestli na novějším macOS
  stačí bez dotazu systému, ověří A1.
- Vývojový Mac je zařízení v portálu (kvůli vývojovému profilu pro A1).
- Profil „Malachi Mail Mac App Store“ (Mac App Store Connect, certifikát
  Distribution, platí do 2027-10-28). Zatím nestažený, do CI půjde jako
  secret v A4.
- Záznam v App Store Connect: macOS, „Malachi Mail“, primární jazyk
  English (U.S.), SKU `malachi-mail-macos`, Full Access, kategorie
  Productivity, cena 0 (základ USD).
- Certifikát Apple Development pro vývojový Mac (vlastník v Xcode)
  a profil „Malachi Mail Mac Development“ (macOS App Development, oba
  vývojové certifikáty, vývojový Mac, platí do 2027-11-05) pro A1.

Zbývá na vlastníkovi:

- Soukromé klíče certifikátů Distribution a Mac Installer Distribution
  vyexportovat jako `.p12` pro secrets CI (A4).
- Dostupnost podle zemí, exportní dotazník (Q5) a trader status pro EU
  (DSA) v App Store Connect.

### 8.2 Stav A1 (2026-11-05)

Napsáno:

- `MalachiCore/Daemon/Distribution.swift`: `direct` / `appstore` z
  `Info.plist` `MalachiDistribution` (čte se za běhu, `Info.plist.in`
  `@DISTRIBUTION@`), skupina `PG9F7XU6X8.io.github.schotek.Malachi`
  a její kontejner. `Paths.resolve` dává v App Store verzi socket do
  kořene skupiny jako `rpc.sock` (s klíčem `rpc.sock.key` vedle), aby se
  cesta vešla do 103 B pro uživatelská jména do 25 bajtů;
  `MALACHI_SOCKET` má přednost dál. Data netřeba přesouvat: v sandboxu
  je `~/Library/Application Support` už kontejner aplikace. Testy
  `DistributionTests`.
- `macos/Resources/mas/`: `MalachiMail.entitlements` (sandbox, síť
  klient i server, soubory vybrané uživatelem, tisk, skupina,
  application a team identifier), `inherit.entitlements` (`malachid`,
  `malachi-keychain`), `malachi-mcp.entitlements` (vlastní sandbox se
  skupinou, zatím bez `allow-unsigned-executable-memory`, ověří A2)
  a `container-migration.plist`, který zatím nejde do žádného bundlu
  (data DMG by přesunul, ne zkopíroval; §6).
- `macos/Makefile`: `DISTRIBUTION=appstore PROFILE=…` vybere entitlements
  z `Resources/mas`, vloží `embedded.provisionprofile` a zapíše distribuci
  do `Info.plist`. DMG (`direct`) se liší jen klíčem
  `MalachiDistribution = direct`.

Ověřeno na vývojovém Macu (release build arm64, podpis Apple
Development, vývojový profil, bundle mimo `build/`, prázdná data
v kontejneru přes `MALACHI_DATA_DIR`, vzorová nástěnka, `MALACHI_START`
`mail;board:list;mail;quit`):

- Podpisy nesou přesně entitlements z `Resources/mas`.
- Aplikace i démon v sandboxu naběhly, démon zmigroval nový store
  v kontejneru aplikace, poslouchal na socketu ve skupinovém kontejneru,
  aplikace se připojila a při konci démona zastavila.
- Přístup ke skupině macOS povolil bez dotazu
  (`containermanagerd`: „Requestor's signature allows it to access
  a TCC-protected group container“); skupinu `group.…` tedy netřeba.
- V systémovém logu žádné zamítnutí sandboxu.

Neověřeno (zůstává):

- Restart z Předvoleb (`Relaunch`, `/bin/sh` + `/usr/bin/open`) pod
  sandboxem; žádný krok `MALACHI_START` ho nespouští.
- Účet se sítí, přihlášení v prohlížeči, helper klíčenky, Uložit jako,
  přílohy, tisk: potřebují skutečný účet nebo kliknutí (A2, A3).
- Most MCP v sandboxu a registrace v Claude (A2).
- Testový bundle nechal kontejnery
  `~/Library/Containers/io.github.schotek.Malachi` a
  `~/Library/Group Containers/PG9F7XU6X8.io.github.schotek.Malachi`;
  z LaunchServices je odregistrovaný. macOS je cizímu procesu smazat
  nedovolí; uklidí je vlastník ve Finderu až po posledním testu (A5).

### 8.3 Stav A2, první část (2026-11-05): lokální agenti

Napsáno:

- `Distribution.allowsLocalAgents` (jen `direct`). Brány, ne jen skryté
  ovládání: `ClaudeCodeLocator.locate()` v App Store verzi vždy nil (z něj
  vychází každý běh Claude Code: panel, jednorázové požadavky, triage,
  Navrhnout odpověď, `--version`, `auth status`, přihlášení),
  `CodexProvider.executable` nil, dostupnost triage navíc podle
  distribuce, `AppState` nečte přihlášení ChatGPT z klíčenky.
- `Settings(defaults:distribution:)`: v App Store verzi se `assistant-target
  = inapp` čte jako Claude Desktop a `assistant-provider = chatgpt` jako
  Claude; zápis těchto hodnot se tam ignoruje, takže uložené hodnoty DMG
  zůstanou pro návrat k DMG. `Assistant.targets` a
  `AssistantController.targets` jsou bez In App.
- Předvolby → AI: bez řádku poskytovatele a skupiny ChatGPT; skupina
  Nástěnka, řádky Claude Code a Model zmizí samy (cíl nikdy není In App);
  popis skupiny Asistent doplní věta `assistant.WebsiteOnlyText` (nový
  msgid v `ui/internal/assistant/distribution.go`, česky, ve
  `windows/parity-exclusions.txt`). Řádek v tabulce odchylek
  `macos/README.md`.

Ověřeno: `swift build -Xswiftc -warnings-as-errors`, filtrované
`swift test` (775 testů, nové `DistributionAgentsTests`),
`check-strings.py` beze změny proti HEAD kromě nového volání, `make po`
v kontejneru (`cs.po` úplné), Go test reference. Neověřeno na obrazovce:
Předvolby → AI v sandboxovaném bundlu ani v DMG.

Zbývá v A2: most MCP v sandboxu (registrace s `MALACHI_SOCKET` do skupiny,
PDF bez `allow-unsigned-executable-memory`, příloha jako soubor),
registrace v Claude podle Q2 (a), restart Claude Desktop vypustit,
`Relaunch` pod sandboxem.

Nález z běhu A1 (log systému, 2026-11-05): `malachi-mcp` s vlastním
sandboxem a skupinou ze sandboxované aplikace (`status` při startu)
spadl čtyřikrát hned po startu: `taskgated-helper` „Disallowing
io.github.schotek.Malachi.malachi-mcp because no eligible provisioning
profiles found“ a `secinit` „Unable to get bundle identifier for
container id io.github.schotek.Malachi.malachi-mcp“. Samostatná binárka
bez bundlu nemůže nést provisioning profile ani mít vlastní kontejner,
takže varianta z §4 (most s vlastním sandboxem jako holá binárka) nejde.
Možnosti viz Q9.

Pomocný bundle (2026-11-05, vlastník zvolil): App ID
`io.github.schotek.Malachi.malachi-mcp` se skupinou, profily „Malachi Mail
MCP Mac Development“ (do 2027-11-05) a „Malachi Mail MCP Mac App Store“
(do 2027-10-28); `macos/Makefile` v App Store verzi dává most do
`Contents/Helpers/Malachi Mail MCP.app` (`Resources/mas/mcp-Info.plist.in`,
vlastní profil `MCP_PROFILE`, application identifier v entitlements),
`Paths.bridgeExecutable` ho tam najde. Měření:

- Spuštěný zvenku (jako ho spustí Claude) s `MALACHI_SOCKET` na socket ve
  skupině: sandbox ho pustí, připojí se k démonovi aplikace (handshake,
  protokol 2) a `list_accounts` odpoví. Tahle polovina funguje.
- Spuštěný aplikací (`malachi-mcp status --json` z
  `MCPRegistrationController`): padá v `_libsecinit_appsandbox`
  (`EXC_BREAKPOINT`, rodič `MalachiMail`). Proces s vlastním sandboxem
  nesmí být potomkem sandboxované aplikace; `inherit` by zase nefungoval
  pod Claude. Registraci (`status`, `install`, `uninstall`) proto
  aplikace nemůže spouštět jako podproces mostu; možnosti viz Q10.
- `secinitd` u prvního spuštění aplikací zapsal „user consented to
  launching … malachi-mcp with container …io.github.schotek.Malachi/Data“;
  ověřit u vlastníka, zda viděl dialog.

Registrace přes LaunchServices (2026-11-05): most dostal platformně
neutrální parametry `--claude-code-config`, `--socket` (položka s
`env.MALACHI_SOCKET`) a `--output` (výsledek či `{"error": …}` do
souboru) a dočasný soubor s pevným jménem `.<jméno>.malachi-mcp.tmp`
(`docs/mcp.md`, Go testy); pomocník má dočasné výjimky na
`~/Library/Application Support/Claude/`, `~/.claude.json`,
`~/..claude.json.malachi-mcp.tmp` (zápis) a `~/.claude/` (čtení).
Ověřeno: pomocník v sandboxu oba soubory Claude přečte a výsledek do
skupiny zapíše; spuštěný přes LaunchServices z shellu (`open -n … --args
install`) zapsal do testovacího souboru ve složce Claude správnou položku
se socketem ve skupině. Nefunguje: spuštěný přes LaunchServices
z *aplikace* (`NSWorkspace.openApplication` s `arguments`) běží, sandbox
se mu nastaví, ale příkaz neprovede a nezapíše nic ani do složky Claude.
Nejspíš sandboxovaná aplikace argumenty spouštěné aplikaci nepředá
(neověřeno v dokumentaci). Možnosti viz Q11. Konfigurace Claude jsou
beze změny (otisk Claude Desktop shodný se zálohou, položka `malachi`
v `~/.claude.json` shodná).

Soubor se žádostí (2026-11-05, vlastník zvolil, Q11): aplikace zapíše
příkazovou řádku do `mcp-setup-request.json` ve skupině, pomocníka spustí
přes LaunchServices bez argumentů a jeho `Info.plist` (`LSEnvironment`
`MALACHI_MCP_SETUP_REQUEST`, „~/“ = domov z databáze účtů) ho na žádost
nasměruje; most ji přečte, smaže a provede jen s `--output`
(`setup.go` `runSetupRequest`, test `TestSetupRequestFile`). Claude
spouští tutéž binárku přímo, bez proměnné, jako server. Spouštěč
(`MalachiMail/App/BridgeSetupLauncher.swift`, háček
`MCPRegistrationController.setupLauncher`) řadí volání za sebe.
Vývojové kroky `mcp-status`, `mcp-install`, `mcp-uninstall`
(`MainWindowController+DevStartMCP.swift`; píšou skutečné konfigurace).

Ověřeno ze sandboxované aplikace (vývojový podpis, Claude Desktop
neběžel, obě konfigurace předem zazálohované): `status` při startu bez
chyby; `install` zapsal do Claude Desktop i `~/.claude.json` položku
s příkazem pomocníka a `env.MALACHI_SOCKET` do skupiny, přes pevný
dočasný soubor bez zbytku; `status` pak `registered=true`; `uninstall`
obě položky odebral. Po testu Claude Desktop vrácen ze zálohy bajt po
bajtu, v `~/.claude.json` vrácena původní položka `malachi` (zbytek
souboru mezitím přepisoval Claude Code). Spolu s dřívějším měřením
(pomocník spuštěný zvenku s `MALACHI_SOCKET` odpoví `list_accounts`) je
cesta Claude → most → démon App Store verze ověřená až na skutečné
spuštění samotným Claude Desktop nebo Claude Code.

Zbývá v A2: restart Claude Desktop v App Store verzi (bez Apple events
vypustit), `Relaunch` pod sandboxem, PDF bez
`allow-unsigned-executable-memory`, příloha jako soubor, ruční průchod
Předvoleb → AI a spuštění mostu skutečným Claude.

Další body A2 (2026-11-05):

- **Restart Claude Desktop:** v App Store verzi vypnutý
  (`ClaudeDesktopController.Platform.canRestart`, `ClaudeDesktopService`
  podle `Distribution.current == .direct`). Změna se zapíše hned a dokud
  Claude Desktop běží, zůstane čekat v řádku „Claude Desktop změnu načte
  po restartu“ bez tlačítka Restart; zapíše se znovu, až se Claude Desktop
  ukončí sám. Test `withoutARestartTheChangeWaitsForClaudeDesktopsOwnQuit`.
- **`Relaunch`:** změřeno vývojovým krokem `relaunch` — v sandboxu se
  `terminate:` zavolal, ale aplikace se neukončila, a čekající `sh` se po
  minutě vzdal. V App Store verzi `Relaunch.available` je false, Předvolby →
  Vzhled restart nenabízejí a velikost textu platí od dalšího spuštění.
- **PDF:** pomocník v sandboxu jako worker přečetl
  `invoice-libreoffice.pdf` bez `allow-unsigned-executable-memory`
  (1240 bajtů textu, stejně jako bez sandboxu); entitlement se nepřidává.
- **Worker pod mostem:** podřízený proces s vlastním sandboxem pod
  sandboxovaným mostem by spadl jako pomocník pod aplikací, proto pomocný
  bundle nese `malachi-mcp-worker`, kopii mostu podepsanou s `inherit`,
  a most ji použije, když leží vedle něj (`besideWorker`, neutrální
  pravidlo, test `TestBesideWorker`). Neměřeno: spuštění workeru mostem
  pod Claude (chce zprávu s PDF v sandboxovaném démonovi, tj. účet
  a klíčenku); ověřit v A5 se skutečnou přílohou.
- **Příloha jako soubor pro Claude** (Cowork, pracovní adresář Claude
  Code): aplikace ji zapisuje do svého adresáře pro otevírání, v App Store
  verzi uvnitř kontejneru aplikace, který macOS ostatním aplikacím chrání
  (viz `ls` z shellu: Operation not permitted). **Rozhodnutí (Q12,
  vlastník 2026-11-05): jen text přes most.** V App Store verzi položka
  „Zeptat se asistenta…“ u přílohy není (`Distribution.handsFilesToClaude`,
  `AttachmentChipView.offerAssistant`); Claude čte text PDF, DOCX a XLSX
  přes `get_attachment`.

Okno O aplikaci (2026-11-05, vlastník, všechny tři klienty): pod řádkem
aktualizací řádek odkud kopie je, `updates.EditionLine`
(`ui/internal/updates/edition.go`, msgidy „Version from malachimail.app“,
„… the Mac App Store“, „… the Microsoft Store“ s češtinou). GTK vždy
web, macOS podle `Distribution` (`Updates.Edition.of`), Windows podle
identity balíčku MSIX (`PackageIdentity.IsPackaged` → Microsoft Store;
i testovací balíček). Windows napsaný bez `dotnet`
(`UpdateCheck.Edition.cs`, `UpdateEditionTests`, `AlertService.About`).

Odkaz na verzi z webu (2026-11-05, vlastník; jen macOS, App Store build):
Předvolby → AI začínají skupinou *Další funkce asistenta* s tlačítkem
*Více…* a stavový pruh má před *Nahlásit problém* odkaz *Více AI
funkcí*; obojí otevře týž dialog (`WebsiteFeaturesDialog`, texty
`assistant.WebsiteFeaturesTexts`): proč v sandboxu chybí asistent
v aplikaci s Claude Code či ChatGPT, psaní a úpravy s asistentem,
hledání vlastními slovy, třídění nástěnky, Navrhnout odpověď a předání
přílohy jako souboru; že obě verze jsou zdarma a účty se mezi nimi zatím
nepřenášejí. Tlačítko jen otevře https://malachimail.app, nic nestahuje
(Guidelines 2.4.5); do poznámek pro App Review patří, že jde o odkaz na
vlastní bezplatnou verzi (3.2.1(i)) a proč je kód agentů vypnutý (2.5.19).

### 8.4 Stav A4, první část (2026-11-05): `.pkg` a privacy manifest

Napsáno:

- `macos/Resources/mas/PrivacyInfo.xcprivacy`: bez sběru dat a sledování,
  důvody pro `UserDefaults` (CA92.1), časy souborů (C617.1) a čas od
  startu systému (35F9.1, Go runtime a hodiny Swiftu); volné místo na
  disku kód nečte. Jde do `Contents/Resources` aplikace i pomocníka mostu.
- `BUILD_NUMBER` (výchozí `SHORT_VERSION`) dává `CFBundleVersion` obou
  bundlů (`@BUILD@`); App Store chce číslo rostoucí s každým nahráním,
  workflow předá číslo běhu.
- `make -C macos pkg` (`productbuild --component … /Applications --sign
  "$(INSTALLER_SIGN)"`, kontrola `pkgutil --check-signature`)
  a `validate-pkg` (`xcrun altool --validate-app` s klíčem App Store
  Connect API přes `NOTARY_KEY`, `NOTARY_KEY_ID`, `NOTARY_ISSUER`);
  kořenový `make macos-mas` (universal, `DISTRIBUTION=appstore`, bez
  kanálu aktualizací).

Ověřeno: `make app DISTRIBUTION=appstore BUILD_NUMBER=17` s vývojovým
podpisem (arm64): oba bundly mají `0.4.0` / `17`, oba privacy manifesty
jsou na místě, podpis prochází.

Podpis (2026-11-05): nové certifikáty Apple Distribution a Mac Installer
Distribution vytvořené v Xcode na MacBooku Air (původní z 2026-10-28
zůstaly na MacBooku Pro), oba profily Mac App Store („Malachi Mail Mac
App Store“, „Malachi Mail MCP Mac App Store“) přegenerované na nový
certifikát Distribution, platí do 2027-11-05. Universal `.pkg`
(`BUILD_NUMBER=1`) se postaví a podepíše; `pkgutil --check-signature`
prochází. `xcrun altool --validate-app` (2026-11-05) skončil „VERIFY
SUCCEEDED with no errors“, takže kontrola SDK (S7) ani privacy manifest
nic nevytkly. Validuje nový týmový klíč App Store Connect API „Malachi
App Store“ (role Developer, vedle „Malachi CI notarization“), u vlastníka
v `~/.appstoreconnect/private_keys/`. Zbývá `macos-mas.yml` a jeho
secrets (certifikáty z MacBooku Air jako `.p12`, oba profily, klíč).
- `.github/workflows/macos-mas.yml` (2026-11-05): ručně postaví
  a zvaliduje `.pkg`, u tagu `v*` ho navíc nahraje (`altool --upload-app`)
  do App Store Connect; `CFBundleVersion` = číslo běhu. Secrets
  `MACOS_MAS_CERTIFICATES` (+ `_PASSWORD`), `MACOS_MAS_PROFILE`,
  `MACOS_MAS_MCP_PROFILE`, `MACOS_MAS_API_KEY`, `MACOS_MAS_API_KEY_ID`,
  issuer sdílený `MACOS_NOTARY_ISSUER`; bez nich se job zastaví
  s varováním. Lokálně ověřené jsou stavba, podpis a validace; nahrání
  ne (poprvé u tagu nebo ručně po souhlasu vlastníka).

### 8.5 Stav A5, první část (2026-11-05): záznam v App Store Connect

Windows řádek O aplikaci ověřil vlastník (2026-11-05). První build do
TestFlightu nahraje CI s příštím tagem (rozhodnutí vlastníka; ruční běh
`macos-mas.yml` na `main` prošel: podpis, stavba, validace).

Vyplněno (agent v prohlížeči, vlastník přihlášený; nic neodesláno):
podtitul „Mail, calendar and a board“, URL zásad ochrany soukromí
`https://malachimail.app/privacy.html`, údaje o soukromí „Data Not
Collected“ (uložené, nepublikované), propagační text, popis, klíčová
slova (bez cizích ochranných známek, 2.3.7), URL podpory
`https://malachimail.app/#report` a marketingu, copyright a poznámky
pro App Review (sandbox a pomocníci, vypnutý kód agentů podle 2.5.19,
odkaz na vlastní bezplatnou verzi podle 3.2.1(i), žádný sběr dat
a kontrola aktualizací). Screenshoty 1440×900 z
`web/assets/screenshots/macos/en` (inbox, chat, board-list,
calendar-week, invitation, jira, crm-saved, newsletter) připravené
mimo repozitář; nahrává je vlastník (prohlížeč agenta soubory nenahraje).

Screenshoty nahrál vlastník. Verze záznamu je 0.4.1 (rozhodnutí
vlastníka: první build do TestFlight přijde z tagu `v0.4.1`).
`privacy.html` (i česky) jmenuje edici z Mac App Store vedle
Microsoft Store, obě aktualizace nekontrolují.

Zbývá na vlastníkovi: kontaktní údaje a testovací účet pro review,
publikovat App Privacy, věkové hodnocení, Content Rights, šifrování
(Q5: standardní algoritmy → App Store Connect chce dokumentaci, nebo
`ITSAppUsesNonExemptEncryption` podle odpovědi), DSA (trader status)
a odeslání do review.
