# Upozornění na novou verzi — návrh

Stav: fáze 1 na větvi `feat/update-check` (2026-11-02): démon, receptury
balíčků, Go reference a porty všech tří klientů (stav ověření v §10).
Kontrakt služby je [`../services.md`](../services.md) §2; server,
administrace a podepisování jsou v soukromém repozitáři
`admin-malachi-mail` (jeho `docs/plan.md`). Odpovědi vlastníka na Q1–Q6
jsou v §8.

## 1. Co se mění

Dokud Malachi Mail nejde přes obchody platforem (Mac App Store, Microsoft
Store, Flathub), dozví se uživatel o nové verzi jen z webu. Nově se aplikace
jednou denně podívá, jestli existuje novější verze pro **balíček, ze kterého
byla nainstalovaná**, a pokud ano, ukáže nenápadný pruh s poznámkami
k vydání a tlačítkem *Stáhnout*, které otevře instalátor v prohlížeči.
Žádná automatická instalace, nic se nespouští samo.

## 2. Rozhodnutí v návrhu (U1–U14)

| # | Rozhodnutí | Proč |
|---|---|---|
| U1 | **Kontroluje démon**, UI jen ukazuje stav a posílá příkazy. | Pravidlo 1; jedna implementace místo tří; stav je stejný ve všech oknech. |
| U2 | Zdroj je **podepsaný manifest** `GET https://admin.malachimail.app/api/v1/updates`, záloha asset `updates.json` posledního GitHub release (`services.md` §2). | Vlastník určuje, co se nabízí (vydání pro jednu platformu, stažení vadné verze, zpráva uživatelům), bez vazby na názvy souborů. |
| U3 | **Ed25519, soukromý klíč jen u vlastníka**, veřejné klíče zakompilované v démonovi podle `keyId`. Podpis přes dekódované bajty payloadu. | Převzetí Cloudflare ani GitHubu nedovolí poslat uživatele na podvržený instalátor. Instalátory nejsou podepsané kódem (`releasing.md` §7), takže je to dnes jediná ochrana. |
| U4 | Ochrana proti přehrání: démon odmítne manifest se starším `issuedAt`, než měl naposled přijatý, a s `issuedAt` víc než den v budoucnosti. | Útočník se starým podepsaným manifestem by jinak schoval opravu. |
| U5 | **Kanál** (balíček instalace) určuje sestavení: Go `-X main.updateChannel=<kanál>` v recepturách balíčků (`make macos-dmg`, `windows\build.ps1 release`, deb, rpm, Flatpak). Pro testy přebíjí `MALACHI_UPDATE_CHANNEL`. Bez kanálu nebo s verzí, která není čisté `MAJOR.MINOR.PATCH`, se nekontroluje. | Démon pozná OS a architekturu, ne balíček. Proměnná sestavení není platformní kód ve stromu (pravidlo 4) a funguje i tehdy, když UI převezme už běžícího démona. Vývojové buildy nic neposílají. |
| U6 | **Flatpak** kanál se zatím kontroluje a nabízí stažení `.flatpak` jako ostatní. Po vydání na Flathubu sestavení Flathubu kanál nemá a kontrola zmizí; totéž později obchody. | Kanál je jediné místo, kde se rozhoduje, a obchod s vlastními aktualizacemi prostě kanál nedostane. |
| U7 | **Plán kontrol**: 5–15 min po startu démona (náhodně), pak každých 24 h ± 2 h; ne v režimu offline; `Retry-After` se respektuje (nejvýš den). Ruční *Zkontrolovat nyní* nejvýš jednou za minutu. | Nezatěžuje start a server nedostane špičku v celou hodinu. |
| U8 | Požadavek nenese nic kromě `User-Agent: MalachiMail/<verze>`: žádné ID, kanál ani dotaz. Celý manifest dostane každý a vybere si démon. | Soukromí; server se nedozví ani platformu. |
| U9 | HTTP klient `internal/update`: jen `https` na `admin.malachimail.app` a cesty pod `/api/` (odpověď odjinud, třeba přihlašovací stránka Access, je selhání serveru), žádné přesměrování, strop 256 KiB, timeout 30 s, systémová proxy jako `remoteimg`, `If-None-Match`. Démon nikdy neloguje tělo, jen třídu chyby. | Stejné zásady jako ostatní síťový kód démona. |
| U10 | **Stažení otevře prohlížeč** na `Package.url` (jen `github.com` a `malachimail.app` přes https, ověří démon). Stahování a ověřování hashe v aplikaci je druhá fáze (§7), manifest má `sha256` a `size` už teď. | Prohlížeč za nás řeší karanténu macOS, Mark of the Web a SmartScreen. |
| U11 | **Výchozí stav: zapnuto** (Q1) a průvodce prvním spuštěním i Předvolby větou řeknou, co se posílá. Předvolba je v démonovi (`meta` `update.prefs`), ne v gschema, aby platila pro všechny klienty a vypnula i dotaz po startu. | Bez kontroly by uživatel opravu bezpečnosti nedostal; jasná věta, co odchází. |
| U12 | **Ukázání**: pruh v hlavním okně (GTK `Adw.Banner`, macOS karta jako ostatní bannery, Windows `InfoBar`) s *Stáhnout*, *Co je nového*, *Připomenout později* (za 3 dny) a *Přeskočit tuto verzi*. Bezpečnostní vydání má jiný text a *Přeskočit* nenabízí. Žádná systémová notifikace (Q2). | Nevyrušuje, ale je vidět; bezpečnostní opravu nejde jedním klikem trvale ignorovat. |
| U13 | **Oznámení** (`Notice`) se ukáže stejným pruhem jednou na `id`, jen verzím pod `below`. | Kanál pro „0.1.3 rozbíjí X, počkejte na 0.1.4“. |
| U14 | O aplikaci: řádek stavu („Používáte nejnovější verzi“, „Je k dispozici 0.1.3“, „Kontrola selhala“, datum poslední kontroly) a *Zkontrolovat nyní*. | Uživatel si může ověřit, že to funguje. |

## 3. Démon

- Balíček `backend/internal/update` (čistý, bez storu): ověření obálky,
  parser manifestu s limity, porovnání verzí, výběr vydání pro kanál,
  čištění textů. Fuzz cíl nad obálkou a payloadem, patologická data
  v `backend/testdata/update` (špatný podpis, neznámý `keyId`, payload
  s duplicitními verzemi, obří řetězce, `http:` a cizí hosty v URL,
  `issuedAt` v budoucnosti i starší, verze s nulami na začátku, bidi
  znaky v poznámkách, schema 2).
- `backend/internal/update/updatetest`: falešný server (obálka podepsaná
  testovacím klíčem, 304, 429 s `Retry-After`, výpadek), žádná síť.
- `core/update_service.go`: plánovač podle U7, stav v `meta`
  (`update.state`: poslední přijatý manifest, `issuedAt`, ETag, čas
  a třída poslední chyby; `update.prefs`: `enabled`, `skipped`,
  `remindAfter`, `noticesSeen`).
- API, kompatibilní rozšíření protokolu 2, nový oddíl `docs/api.md` §4.20
  `update` a notifikace v §5:
  - `update.status {}` → `UpdateStatus { enabled, channel (opt),
    current, state: "disabled|unsupported|idle|checking|upToDate|available|failed",
    checkedAt (opt), failure (opt): "network|server|signature|format",
    available (opt): UpdateInfo, notice (opt): UpdateNotice }`;
    `UpdateInfo { version, publishedAt, security, notes, infoUrl, url,
    size, skipped, remindAfter (opt) }` (`notes` už v jazyce, který klient
    předá `locale`, jinak `en`);
  - `update.check {}` → `UpdateStatus` po kontrole (nejvýš jednou za
    minutu, jinak vrátí stav bez kontroly);
  - `update.setPrefs {enabled?}`;
  - `update.dismiss {version, action: "skip"|"later"}` a
    `update.dismissNotice {id}`;
  - `notify.updateChanged {}` při každé změně stavu, který klient ukazuje.
  - Žádný nový chybový kód: `invalidArgument` (skip bezpečnostního vydání,
    neznámá verze), ostatní selhání jsou ve stavu, ne v chybě.
- Most MCP beze změny.

## 4. Klienti (pravidlo 6: všechny tři v jedné práci)

- **Go reference** `ui/internal/updates`: texty (msgid), pravidlo, kdy
  a jaký pruh ukázat (`BannerFor(status, now)`), řádek O aplikaci, formát
  poznámek (prostý text, odstavce), testy. Do `po/POTFILES`.
- **GTK**: `window/update_banner.go` + `Adw.Banner` v `window.blp`,
  *Co je nového* jako `Adw.Dialog` s `GtkLabel` (`SetUseMarkup(false)`),
  otevření odkazu přes `gtk.UriLauncher`; Předvolby → Obecné → Aktualizace
  (přepínač, věta o tom, co se posílá); O aplikaci řádek stavu
  a *Zkontrolovat nyní*; průvodce prvním spuštěním věta (U11).
- **macOS**: port do `MalachiCore/Updates` (testy 1:1), banner jako karta
  se symbolem, `NSWorkspace.open`, Settings → General, About okno,
  `stringValue` všude.
- **Windows**: port do `Malachi.Core/Updates`, `InfoBar` nad seznamem,
  `Launcher.LaunchUriAsync`, Předvolby → Obecné, O aplikaci; staví a
  testuje Windows agent vlastníka, ne `windows.yml`.

## 5. Vydávání

- `docs/releasing.md` §3 dostane krok: po nahrání všech balíčků vlastník
  v administraci (`admin.malachimail.app`) připraví manifest z GitHub
  release, stáhne payload, lokálně ho podepíše (nástroj v soukromém
  repozitáři) a nahraje obálku; pak `gh release upload vX.Y.Z updates.json`
  (záloha, U2). Windows balíčky přicházejí později: stejné kroky znovu,
  nový `issuedAt`.
- Receptury balíčků předávají `-X main.updateChannel` (U5): `Makefile`
  (`macos-dmg`, deb, rpm), `windows/build.ps1 release`, manifest Flatpaku.
  `make macos` (denní build vlastníka) a vývojové buildy kanál nemají.
- Veřejné klíče: vlastník vygeneruje první pár nástrojem soukromého
  repozitáře, veřejný klíč (`keyId` `2026-1`) jde do
  `backend/internal/update/keys.go`.

## 6. Bezpečnost a soukromí

- `docs/security.md` dostane oddíl: co démon posílá (jen User-Agent),
  co ověřuje (podpis, `issuedAt`, hosty URL), že nic nespouští a nestahuje.
- Web dostane stránku zásad ochrany osobních údajů (kontrola aktualizací
  a později zpětná vazba); server IP neukládá (soukromý plán).
- Poznámky a oznámení jsou nedůvěryhodný text: čistí je démon, klienti
  jen jako prostý text.

## 7. Druhá fáze (ne teď)

Stažení v aplikaci: `update.download` stáhne `Package.url` do adresáře
Stažené soubory přes `staging`, ověří `size` a `sha256` z podepsaného
manifestu, nastaví karanténu / Mark of the Web jako u příloh a vrátí cestu;
klient nabídne *Otevřít instalátor* (Windows: ukončit aplikaci, zastavit
démona, spustit Setup.exe). Formát manifestu se kvůli tomu nemění.

## 8. Otázky pro vlastníka (zodpovězené 2026-11-02)

- **Q1**: Výchozí stav kontroly zapnuto (s větou v průvodci), nebo opt-in?
  → **zapnuto**.
- **Q2**: Jen pruh v okně, nebo i jedna systémová notifikace u bezpečnostního vydání?
  → ani jedno: u bezpečnostního vydání se při příjmu **jednou** ukáže modální
  okno s *Stáhnout* a zaškrtávátkem „Po zahájení stahování ukončit Malachi Mail“
  (předvybrané); ať uživatel zvolí cokoli, pruh zůstane viditelný až do
  aktualizace (bez *Připomenout později* i *Přeskočit*). „Jednou“ hlídá démon
  (`UpdateInfo.prompt`, `update.dismiss prompted`), takže se neptá ani jiné okno
  či klient.
- **Q3**: Má i kanál `windows-zip-*` dostávat upozornění? → zip už neexistuje;
  kanály jsou přesně tabulka `services.md` §2.3.
- **Q4**: deb a rpm: kontrolovat? → ano (kanály `linux-deb-*`, `linux-rpm-*`).
- **Q5**: *Připomenout později* = 3 dny? → **ano**; konec odkladu ohlásí démon
  `notify.updateChanged`, klienti hodiny nepotřebují.
- **Q6**: Pořadí prací → všechny tři klienty v jedné práci (pravidlo 6).

Upřesnění při implementaci: U14 *Zkontrolovat nyní* je ve všech třech
klientech v Předvolbách → Obecné → Aktualizace (spolu s řádkem stavu),
protože standardní okna O aplikaci (Adw.AboutDialog, panel AppKitu) tlačítko
neunesou; O aplikaci ukazuje jen řádek stavu, kde to jde. Pruh je v hlavním
okně ve všech režimech (Pošta, Nástěnka, Kalendář). Plán U7 doplňuje, že
první kontrola po startu není dřív než den (± 2 h) po poslední, takže
restarty aplikace server nezatěžují. Démon přijme jen text, který je už
čistý (správce v administraci odmítá totéž), místo aby ho čistil.

Doplněno po prvním běhu vlastníka (2026-11-02): poznámky v *Co je nového*
se čtou jako Markdown parserem asistenta (nadpisy, odrážky, tučné, kód;
odkazy jen jako text), *Další informace* otevírá vždy
`https://malachimail.app/news.html` (`updates.NewsURL`, ne `infoUrl`
vydání) a ruční *Zkontrolovat nyní* ruší *Připomenout později*.
Z průchodu vlastníka na Linuxu (2026-11-02, rozhodnutí přes
AskUserQuestion ze tří možností): pruh GTK je jeden přes celou šířku
okna nad všemi hlavičkovými lištami (`Adw.Bin update_bar` nad
`mode_stack` ve svislém Boxu obsahu okna), tedy nejblíž kartě macOS;
ne `Adw.Banner` nad seznamem zpráv ani pod lištami Nástěnky a Kalendáře.
Uvnitř je svislý: titulek zalamovaný po slovech a pod ním tlačítka
v `Adw.WrapBox` (v úzkém sloupci se dřív titulek lámal do čtyř řádků
vedle mřížky tlačítek).

## 9. Úkoly

1. Démon: `internal/update`, `updatetest`, testdata, `core/update_service.go`, API, `docs/api.md` §4.20, `docs/security.md`.
2. Receptury balíčků s `main.updateChannel`, `docs/releasing.md`.
3. Go reference `ui/internal/updates` + GTK, `make po`.
4. macOS port.
5. Windows port (předávka Windows agentovi).
6. První podepsaný manifest z administrace, ruční ověření nad vydáním 0.1.3.

## 10. Stav ověření (2026-11-02)

- Démon: Go testy nad 69 fixturami správce a živým manifestem 0.2.0,
  `core` i s `-race`; běh proti skutečnému API a záloze na GitHubu přes
  `make run-macos` s `VERSION=0.1.2` a `MALACHI_UPDATE_CHANNEL=macos-dmg`.
- macOS: vlastník nad skutečnými daty prošel pruh, *Co je nového*
  (Markdown, otevřené nahoře), *Připomenout později* → *Zkontrolovat
  nyní*, *Další informace*, bezpečnostní dialog s ukončením po stažení,
  *Přeskočit tuto verzi* a větu na stránce bez účtů; vše funkční.
- GTK: vlastník nad kopií dat na Fedoře 44 aarch64 (Linux agent sestavil
  `VERSION=0.1.2 UPDATE_CHANNEL=linux-rpm-arm64`); po přesunu pruhu přes
  celé okno „vše ostatní v pořádku“.
- Windows: napsané bez `dotnet`, nepřeložené; staví a prochází Windows
  agent (`docs/windows-port.md` §11.16).

