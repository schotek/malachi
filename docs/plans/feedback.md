# Zpětná vazba z aplikace — návrh

Stav: návrh (2026-10-28), nic není napsané. Kontrakt služby je
[`../services.md`](../services.md) §3 (zatím náčrt); server a schránka
zpětné vazby jsou v soukromém repozitáři `admin-malachi-mail` (jeho
`docs/plan.md` §6.2). Otázky Q1–Q7 v §8 čekají na vlastníka.

## 1. Co se mění

Uživatel může z aplikace poslat připomínku, hlášení chyby nebo návrh
funkce, aniž by musel mít účet na GitHubu. Formulář je ve všech třech
klientech stejný, odesílá ho démon na `api.malachimail.app`; uživatel před
odesláním vidí přesně to, co odejde. Druhá cesta je e-mail z vlastního
účtu. Na GitHub aplikace nikde neodkazuje, ani *Nahlásit problém*
(rozhodnutí vlastníka 2026-10-28, F14).

## 2. Rozhodnutí v návrhu (F1–F14)

| # | Rozhodnutí | Proč |
|---|---|---|
| F1 | **Hlavní cesta je formulář přes API** (`POST /v1/feedback`), **e-mail** je odkaz ve formuláři; odkaz na GitHub ve formuláři není. | Funguje i bez nastaveného účtu a bez odhalení adresy; kdo chce odpověď, má e-mail. |
| F2 | **Odesílá démon** (`feedback.send`), UI jen sestaví formulář a ukáže výsledek. | Pravidlo 1; jedna implementace HTTP, limitů a čištění. |
| F3 | **Kde**: GTK hlavní nabídka (☰) — *Poslat zpětnou vazbu…* před *O aplikaci*; macOS menu **Help → Send Feedback…** pod *Malachi Mail Help* a tlačítko v okně About; Windows nabídka **`…`** — *Poslat zpětnou vazbu…* nad *Konec* a tlačítko v Předvolbách → O aplikaci. *Nahlásit problém* v O aplikaci (F14) otevře týž formulář s druhem *Chyba*. | Místa, kde to uživatel na každé platformě hledá; žádné tlačítko v toolbaru ani nevyžádané dotazy. |
| F4 | **Druh**: *Chyba · Návrh funkce · Jiné* (`bug` / `idea` / `other`), výchozí podle vstupu (z nabídky *Jiné*, z chybového stavu *Chyba*, F12). | Třídění ve schránce; napovídající text podle druhu. |
| F5 | **Text** 1–8000 bajtů UTF-8 s počítadlem, prostý text. Démon ho čistí jako ostatní text uživatele (bez řídicích, bidi a neviditelných znaků, NFC, `\n` zůstávají). | Stejné pravidlo jako profil asistenta. |
| F6 | **E-mail pro odpověď** nepovinný. Pole nabídne adresy účtů z našeptávače, ale **nic nevyplní samo**; démon ho ověří jako adresu (`net/mail`), ≤ 254 B. | Uživatel rozhoduje, jestli se dá kontaktovat. |
| F7 | **Technické údaje** (zaškrtávátko, výchozí zapnuto): verze, kanál (`main.updateChannel`, jinak `dev`), OS a jeho verze, architektura, jazyk UI, klient (`gtk`/`macos`/`windows`). U druhu *Chyba* navíc nejvýš 5 posledních tříd selhání z `SyncState` (`networkError`, `authFailed`, …, bez textu, účtu ani serveru). **Nikdy**: obsah pošty, adresy, názvy účtů, složek a serverů, cesty, logy, id zpráv. | Užitečné pro diagnostiku, ale nic osobního ani z pošty. |
| F8 | **Náhled „Co se pošle“**: rozbalovací blok ukáže přesně JSON, který démon odešle (`feedback.preview` vrací totéž tělo jako `feedback.send` bez odeslání), jako prostý text. | Uživatel vidí každé slovo; žádná skrytá pole. |
| F9 | **Věta o soukromí**: „Odesílá se na malachimail.app. IP adresa se neukládá.“ + odkaz na zásady ochrany osobních údajů na webu. Formulář se v aplikaci objeví až po zveřejnění té stránky. | GDPR; slib, který musí server dodržet (soukromý plán A10). |
| F10 | **Odeslání**: úspěch = toast „Děkujeme za zpětnou vazbu“ a okno se zavře; selhání = okno zůstane s textem, řádek chyby s *Zkusit znovu*; `429` s `Retry-After` = věta „Zkuste to prosím později“. Text rozepsaného formuláře drží klient v paměti do konce běhu, neukládá se. | Nic napsaného se neztratí; démon neuchovává cizí text na disku. |
| F11 | **Limit v démonovi**: nejvýš 5 odeslání za hodinu a 20 za den na instalaci (paměť + `meta` `feedback.sent`), nad limit `invalidArgument` s `data.retryAfter`. Režim offline = chyba `offline`. | Chyba UI nesmí zahltit server; server má vlastní limity (WAF). |
| F12 | **Z chybového stavu** (později, ne v první verzi): banner účtu s chybou a toast selhání sanitizéru nabídnou *Nahlásit…*, formulář se otevře s druhem *Chyba* a třídou selhání v technických údajích, nikdy s textem zprávy ani serveru. | Nejlepší chvíle pro hlášení, ale až když základ funguje. |
| F13 | **E-mail**: odkaz *Raději napsat e-mail* otevře okno Nová zpráva na `feedback@malachimail.app` (Q4) s předmětem „Malachi Mail: zpětná vazba“ a technickými údaji v těle jako prostý text, z výchozího účtu. Jen když existuje účet, který umí psát (`CanComposeNew`); jinak odkaz chybí. | Přirozená cesta pro poštovního klienta; odpověď přijde e-mailem. |
| F14 | **Nikde v aplikaci odkaz na GitHub** (vlastník 2026-10-28): ani ve formuláři (Q6), ani v O aplikaci. *Nahlásit problém* v O aplikaci jde také přes formulář do API a administrace: GTK `Adw.AboutDialog` dostane `issue-url` s vnitřní adresou `malachi-app:feedback?kind=bug`, kterou zachytí signál `activate-link` (vrátí `true`, nic se neotevře v prohlížeči) a otevře formulář s druhem *Chyba*; ověřit, že ho gotk4-adwaita pro libadwaita 1.7 má, jinak `issue-url` vynechat a tlačítko dát do hlavní nabídky. macOS About a Windows Předvolby → O aplikaci mají tlačítko *Nahlásit problém…* se stejným chováním. | Veškerá zpětná vazba končí na jednom místě, ve schránce administrace. |

## 3. Kontrakt služby

`services.md` §3 se z náčrtu změní na závazný kontrakt v1 (tělo podle F4–F7,
`201 {"id"}`, chyby `invalid`, `tooLarge`, `rateLimited`, `unavailable`).
Pole `diagnostics`:

```jsonc
"diagnostics": {
  "arch": "arm64",
  "client": "macos",
  "failures": ["networkError", "authFailed"]   // jen u kind = bug, ≤ 5
}
```

## 4. Démon

- `backend/internal/feedback` (čistý): sestavení těla z parametrů
  a prostředí, čištění, limity, kontrola e-mailu; testy s patologickými
  vstupy (`backend/testdata/feedback`: obří text, bidi, neplatné UTF-8,
  nulové bajty, e-mail s `\r\n`, řídicí znaky).
- HTTP klient jako u aktualizací (`internal/update`): jen `https`
  na `api.malachimail.app`, bez přesměrování, timeout 30 s, systémová
  proxy, tělo odpovědi nejvýš 4 KiB, nic z těla se neloguje.
- Falešný server `feedback/feedbacktest` (201, 400, 413, 429, 503, výpadek).
- API, kompatibilní rozšíření protokolu 2, `docs/api.md` §4.20 `feedback`:
  - `feedback.preview {kind, text, contact?, includeDiagnostics, client}`
    → `{ body: "<přesný JSON k odeslání>" }`;
  - `feedback.send {…stejné…}` → `{ id }`; chyby `invalidArgument`
    (prázdný či dlouhý text, špatný e-mail, limit s `data.retryAfter`),
    `offline`, `networkError`, `serverError`, `serverTimeout`.
- Most MCP beze změny (asistent zpětnou vazbu neposílá).

## 5. Klienti (pravidlo 6)

- **Go reference** `ui/internal/feedback`: texty (msgid), napovídající
  texty podle druhu, pravidla tlačítka Odeslat (prázdný text, limit),
  text e-mailu (F13), mapování chyb na věty; testy. Do `po/POTFILES`.
- **GTK**: `window/feedback.go` + `feedback_dialog.blp` (`Adw.Dialog`:
  `Adw.ToggleGroup` nebo tři přepínací tlačítka druhu, `GtkTextView`,
  `Adw.EntryRow` e-mailu, `Adw.ExpanderRow` náhledu s `GtkLabel`
  `SetUseMarkup(false)`, `SetSelectable(true)`), akce `app.feedback`
  v hlavní nabídce, `issue-url` + `activate-link` v O aplikaci (F14).
- **macOS**: `MalachiCore/Feedback` (port s testy), okno jako sheet nad
  hlavním oknem, Help → Send Feedback…, tlačítko v About, `stringValue`
  všude.
- **Windows**: `Malachi.Core/Feedback` (port s testy), `ContentDialog`
  nebo modální okno přes `ModalDialog`, položka v nabídce `…`, tlačítko
  v Předvolbách → O aplikaci; staví a testuje Windows agent vlastníka.

## 6. Server (soukromý repozitář)

Podle soukromého plánu §6.2: validace stejná jako v démonu, uložení do D1
bez IP, denní strop, schránka v administraci, uchování podle A-Q5. Musí
být nasazený dřív než klienti s formulářem.

## 7. Pořadí

1. Zásady ochrany osobních údajů na webu (`web/privacy.html`, cs i en).
2. Server: `POST /v1/feedback` a schránka (soukromý repozitář).
3. `services.md` §3 závazně, démon, `docs/api.md`, `docs/security.md`.
4. Go reference + GTK, `make po`.
5. macOS port.
6. Windows port (předávka Windows agentovi).
7. Později: *Nahlásit…* z chybových stavů (F12).

## 8. Otázky pro vlastníka

- **Q1**: Technické údaje výchozí zapnuté, nebo vypnuté?
- **Q2**: Třídy posledních selhání u druhu *Chyba* posílat, nebo jen verzi a OS?
- **Q3**: Formulář jako samostatné okno, nebo sheet / dialog nad hlavním oknem (návrh: dialog)?
- **Q4**: Adresa pro e-mailovou cestu — `feedback@malachimail.app` (je potřeba zřídit přeposílání, např. Cloudflare Email Routing), nebo tvoje stávající adresa?
- **Q5**: Má uživatel dostat potvrzení s číslem (`id`), nebo jen poděkování?
- ~~**Q6**: Odkaz na GitHub ve formuláři ponechat, nebo jen v O aplikaci?~~ Nikde; i *Nahlásit problém* jde přes formulář (vlastník 2026-10-28).
- **Q7**: Má i asistent v panelu umět navrhnout „pošlete to jako zpětnou vazbu“ (otevře předvyplněný formulář), nebo ne?
