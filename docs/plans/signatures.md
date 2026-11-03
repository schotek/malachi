# Podpisy a šifrování (S/MIME, OpenPGP) — návrh

Stav: návrh (2026-11-02); kontrakt API a Go reference hotové (§11). Rozhodnutí vlastníka: **fáze 1 je
jen ověřování podpisů příchozí pošty**, S/MIME i OpenPGP najednou;
dešifrování, podepisování a šifrování jsou další fáze (§9) a v tomto
návrhu jsou jen kvůli tomu, aby fáze 1 nezavřela cestu k nim. Bezpečnostní
zásady jsou [`../security.md`](../security.md) §5 (EFAIL a podpisy), na
které tento plán navazuje. Odpovědi vlastníka na Q1–Q8 jsou v §8.

## 1. Co se mění ve fázi 1

Malachi Mail dnes podepsanou a šifrovanou poštu jen pozná
(`mime.Parsed.Crypto`, ingest ji nikdy neořezává, `security.md` §2) a ukáže
ji jako obyčejnou zprávu: podpis je příloha `smime.p7s` nebo
`signature.asc`, neprůhledně podepsaná S/MIME zpráva
(`application/pkcs7-mime; smime-type=signed-data`) je jen příloha
`smime.p7m` bez čitelného těla, výňatku a hledání.

Po fázi 1:

- démon ověří podpis S/MIME i OpenPGP/MIME z uložené surové zprávy
  hned při uložení a verdikt uloží; klienti ho dostanou se souhrnem
  zprávy (**odznak i v seznamu**, Q4) i s hlavičkou;
- panel čtení, okno zprávy a karta konverzace mají v hlavičce odznak
  podpisu a podrobnosti (kdo, vydavatel, otisk, platnost, proč ne);
  u S/MIME tlačítko *Zkontrolovat odvolání* (Q3);
- neprůhledně podepsaná S/MIME zpráva je obyčejná zpráva: ingest ji
  rozbalí, takže má tělo, výňatek, přílohy, hledání i nástěnku (Q5);
- inline PGP podpis má šedý odznak „neověřuje se“ (Q6);
- klíče OpenPGP jen bez sítě: Autocrypt, přiložený klíč, ruční import (Q2);
- podpisová část zmizí z dlaždic příloh (je v podrobnostech podpisu);
- most MCP vrací shrnutí verdiktu (Q8);
- nic se nepodepisuje, nic se nedešifruje, žádný soukromý klíč.

## 2. Rozhodnutí v návrhu (S1–S17)

| # | Rozhodnutí | Proč |
|---|---|---|
| S1 | **Ověřuje démon**, UI ukazuje verdikt jako prostý text. | Pravidlo 1; `security.md` §5. Jedna implementace pro tři klienty. |
| S2 | Verdikt se **počítá při uložení zprávy** (`internal/ingest` po `CommitMessageRaw`, mimo zápisovou transakci pošty, jako hook `Stored`) a ukládá do tabulky `message_signatures` (§4) s verzí ověřovače a klíčem důvěry. Stará pošta v okně `offlineDays` se ověří v `core.Maintain` (kurzor `meta` `signature.verified` = `<verze>:<id>`). Při změně důvěry (import či potvrzení klíče OpenPGP, výsledek kontroly odvolání, jiná sada systémových kořenů) se přepočítají jen dotčené zprávy. | Odznak v seznamu (Q4) potřebuje verdikt bez čtení surového souboru; seznam se nesmí zdržet ověřováním. |
| S3 | Podpis se ověřuje nad **přesnými bajty** podepsané části ze surového souboru (kanonizace CRLF podle RFC 8551 / RFC 3156), nikdy nad znovu složenou částí z parseru. Výřez bajtů sdílí kód s `mime.Skeleton` (už umí kopírovat části bajt po bajtu). | Přestavěná část podpis rozbije; parser normalizuje. |
| S4 | Platí jen podpis, který **pokrývá celé tělo**: nejvyšší `multipart/signed` nebo neprůhledný `signed-data` jako kořen zprávy, nebo pod jedním obalem `multipart/mixed`, jehož jediná další část je `text/plain` na konci (patička konference, Q7: verdikt s poznámkou `partialFooter`). Podpis hlouběji (přeposlaná zpráva, příloha) není verdikt zprávy. | Útoky „Johnny, you are fired“ (2019): podepsaná část vložená do nepodepsané zprávy, víc podpisů, podpis jen kousku. |
| S5 | Identita = **adresa z certifikátu** (S/MIME `subjectAltName rfc822Name`, u starých certifikátů `emailAddress` v DN) nebo z **user ID klíče** (OpenPGP), porovnaná s adresou `From` (NFC, malá písmena domény). Nesoulad = varování „podepsal někdo jiný“, nikdy zelená. Jméno v odznaku je vždy z certifikátu/klíče, ne z hlavičky. | `security.md` §5. |
| S6 | `Subject`, `Date`, `To` mimo podpis nejsou chráněné. Pokud zpráva nese chráněné hlavičky (RFC 9788, `protected-headers="v1"`) a liší se od vnějších, podrobnosti to řeknou; fáze 1 je **neukazuje místo** vnějších. | Méně překvapení; chráněné hlavičky naplno až s dešifrováním. |
| S7 | **S/MIME důvěra = systémové kořeny** přes `x509.SystemCertPool` a ověření řetězu Go (na macOS a Windows systémový ověřovač, na Linuxu bundle distribuce či runtime Flatpaku). EKU `emailProtection` nebo `any`, `keyUsage` digitalSignature. Mezilehlé certifikáty jen ze zprávy, nic se nestahuje (žádné AIA). Otisk sady kořenů je součást klíče důvěry (S2); změnu démon zjistí při startu a jednou za den. | Platformně neutrální (pravidlo 4), firemní CA přidaná do systému funguje sama, žádný pomocník. |
| S8 | **Čas ověření**: certifikát musí platit v čase **příchodu** zprávy (`internal_date`, jako `board.Arrival`), ne podle `signingTime` (ten píše podepisující). Certifikát, který mezitím vypršel, je dál platný podpis s poznámkou. | Stará pošta nezčervená; podepisující si nevybere čas. |
| S9 | **Kontrola odvolání jen na kliknutí** (Q3): `message.signatureRevocation` zkusí OCSP z AIA certifikátu, pak CRL z CDP; jen `http`/`https` na veřejné adresy (stejná pravidla jako `internal/oneclick`: bez loopbacku, privátních a lokálních jmen, bez přesměrování), strop 1 MiB a 15 s, odpověď OCSP musí být podepsaná vydavatelem nebo jeho delegovaným respondérem. Výsledek se ukládá k certifikátu (otisk, stav, čas kontroly, čas odvolání) a přepočítá zprávy podepsané tímto certifikátem. Do logu jen třída chyby. | Kontrola prozradí vydavateli, že zprávu čtete; jen když o to uživatel požádá. |
| S10 | **Slabé algoritmy**: MD5 vždy neplatné, SHA-1 a RSA < 2048 jako `weak` (žluté), DSA jako `unsupported`. | Běžná praxe Thunderbirdu a Outlooku. |
| S11 | **OpenPGP ověřuje jen PGP/MIME** (RFC 3156, `multipart/signed; protocol="application/pgp-signature"`). Inline podpis (`-----BEGIN PGP SIGNED MESSAGE-----` na začátku textové části) dostane verdikt `unsupported` s poznámkou `inlinePGP` (Q6) a nic se neověřuje. | Inline podpis nepokrývá přílohy ani HTML a je zdrojem spoofingu; uživatel ale vidí, že tam podpis je. |
| S12 | **OpenPGP důvěra bez web of trust**: klíč je buď `verified` (uživatel ho potvrdil podle otisku), nebo `seen` (nalezený automaticky, TOFU), jinak `unknownKey`. Zdroje klíčů jen bez sítě (§5, Q2). | Web of trust nikdo nepoužívá; TOFU + ruční potvrzení dělá Thunderbird i Delta Chat. |
| S13 | **Žádný kód s GPL v démonu**: OpenPGP přes `github.com/ProtonMail/go-crypto` (BSD-3, RFC 9580 i 4880), CMS/PKCS#7 **vlastní minimální parser a ověřovač** SignedData nad `golang.org/x/crypto/cryptobyte` (jen ověření, jen RSA PKCS#1 v1.5/PSS a ECDSA, SHA-2) s fuzz cíli; OCSP přes `golang.org/x/crypto/ocsp` (BSD). GnuPG a gpgme ne. | Licence jádra (`LICENSING.md`); Go knihovny PKCS#7 jsou opuštěné (`go.mozilla.org/pkcs7`) nebo široké; ověření SignedData je malé a dobře testovatelné, jako vlastní sanitizér a parser vCard. |
| S14 | **Neprůhledně podepsaná S/MIME zpráva se rozbalí v parseru** (Q5): `mime.Parse` pozná `signed-data` jako kořen (nebo pod obalem podle S4), vytáhne z CMS `eContent` (bez ověření, s limity) a parsuje ho jako tělo zprávy; text, HTML, výňatek, přílohy, `cid:`, FTS (`search_docs`), vlastní text nástěnky (`board.OwnText`) i citace v `draft.create` pak vidí vnitřek. Čísla částí se adresují uvnitř obalu (§4). Uložené zprávy se přeparsují v `core.Maintain` (kurzor `meta` `mime.opaqueUnwrapped`), nástěnka dostane nové `RulesVersion` (mění se, co jí store podává). | Obsah není šifrovaný, jen zabalený; uživatel ho čekal čitelný všude. |
| S15 | Podpisová část (`application/pkcs7-signature`, `application/pgp-signature`) se **nevypisuje mezi přílohami** zprávy, která má verdikt; zpráva bez verdiktu (podpis hlouběji, S4) ji vypisuje jako dnes. | Jako Apple Mail a Outlook; podpis je v podrobnostech. |
| S16 | **Nic se neloguje** z certifikátu ani klíče (jméno, adresa, DN); jen kód verdiktu, druh a id zprávy. | Jako kontakty a profil. |
| S17 | Ověřování nikdy **nebrzdí příjem**: selhání ověřovače (panika, limit) je verdikt `error` a zpráva se uloží normálně; ověření běží ve frontě workeru po uložení, `notify.messagesChanged` pošle až s verdiktem. | Hostilní CMS nesmí zastavit synchronizaci. |

## 3. Verdikt

Jeden verdikt na zprávu, z tohoto pořadí první, který platí:

| `status` | Barva v UI | Kdy |
|---|---|---|
| `invalid` | červená | podpis nesedí na obsah, rozbitá struktura podpisu, MD5, certifikát bez povoleného použití |
| `revoked` | červená | S/MIME: kontrola odvolání (S9) zjistila odvolání před příchodem zprávy; OpenPGP: klíč odvolaný (z importovaného podpisu odvolání) před příchodem |
| `mismatch` | žlutá | podpis sedí, ale identita (S5) není adresa `From` |
| `untrusted` | žlutá | S/MIME: řetěz nevede ke kořeni systému, certifikát v čase příchodu neplatil; OpenPGP: klíč propadlý v čase příchodu |
| `unknownKey` | šedá | OpenPGP: klíč nemáme (§5) |
| `weak` | žlutá | sedí a důvěryhodný, ale SHA-1 či krátký klíč (S10) |
| `unsupported` | šedá | algoritmus nebo tvar, který neověřujeme (DSA, víc podepisujících v jednom SignedData, inline PGP) |
| `error` | šedá | ověřovač selhal (limit, poškozená data, S17) |
| `valid` | zelená | sedí, důvěryhodný (S/MIME kořen, OpenPGP `verified`), identita = `From` |
| `validSeen` | zelená bez zatržení | OpenPGP: sedí, identita = `From`, klíč jen `seen` (TOFU) |

K tomu `notes` jako kódy (ne texty): `expiredSince`, `revocationUnchecked`
(S/MIME, kontrola neproběhla), `revocationGood` (s časem kontroly),
`revokedAfter` (odvoláno po příchodu zprávy, verdikt platí s poznámkou),
`partialFooter` (S4), `protectedHeadersDiffer` (S6), `inlinePGP` (S11),
`keyChanged` (OpenPGP: pro adresu máme jiný `seen` klíč než dřív —
TOFU varování), `opaque` (S/MIME `signed-data`, S14).

## 4. Démon

- `internal/smime` (čistý): parser CMS `SignedData` s limity (hloubka
  ASN.1 ≤ 32, objekt ≤ 32 MiB, certifikátů ≤ 16, podepisujících = 1),
  vytažení `eContent` pro parser (S14), výřez a kanonizace podepsané
  části, ověření podpisu, výběr certifikátu podepisujícího, ověření
  řetězu proti předanému poolu a času, extrakce identity, OCSP/CRL
  odpovědi (čisté ověření, síť je jinde).
- `internal/pgpsig` (čistý): ověření PGP/MIME podpisu přes go-crypto
  proti předané sadě klíčů, identita z user ID, stav klíče v čase,
  rozpoznání inline podpisu, čtení hlavičky `Autocrypt`.
- `internal/signature` (čistý): rozhodnutí, který podpis je verdikt zprávy
  (S4), poskládání verdiktu (§3), společný typ pro oba formáty,
  `VerifierVersion`.
- `internal/mime`: rozbalení `signed-data` (S14); `Parsed.Opaque`
  (zpráva je zabalená) a čísla částí vnitřní zprávy s prefixem obalu
  (např. `1.inner.2`, přesný tvar podle `message.part` a `remote_parts`),
  `VerifySkeleton` dál kryptozprávy neořezává. Fuzz `FuzzOpaqueUnwrap`.
- `internal/revocation`: síťová část S9 (klient jako `internal/oneclick`).
- `core/signature_worker.go`: fronta po uložení (S2, S17), backfill
  v `core.Maintain`, přepočet při změně důvěry, `notify.messagesChanged`
  pro dotčené zprávy; `core/signature_service.go`: metody API, sada
  systémových kořenů a její otisk (S7), zdroje klíčů OpenPGP (§5).
- Migrace `00NN_signatures.sql` (číslo v době práce, dopředná, žádné
  triggery na `messages`):
  - `message_signatures` (`message_id` PK, `kind`, `status`,
    `signer_name`, `signer_email`, `fingerprint`, `notes`,
    `verifier_version`, `trust_key`, `verified_at`), řádek se smaže se
    zprávou (úklid v témže kroku, kde se mažou `message_files`);
  - `smime_revocation` (`cert_sha256` PK, `state`, `revoked_at`,
    `checked_at`, `source`);
  - `pgp_keys` (otisk PK, armored veřejný klíč, adresy, `trust`
    `seen`/`verified`, `first_seen`, `source` `autocrypt`/`attached`/
    `import`, `revoked_at`) — jen veřejné klíče.
- API (kompatibilní rozšíření protokolu 2, nový oddíl `docs/api.md` §4.21
  `signature`, notifikace v §5, test `TestDocsCoverAllMethods`):
  - `MessageSummary.signature` (opt): `{ kind: "smime"|"pgp", status }`
    — pro odznak v seznamu; `thread.list` souhrn nemá (Q4 platí pro
    řádek zprávy; sbalený řádek vlákna ukazuje verdikt nejnovějšího
    člena, pokud ho má);
  - `Message.signature` (opt): `SignatureSummary { kind, status,
    signer: { name, email, fingerprint }, notes[] }`;
  - `message.signature {messageId}` → `SignatureDetails` pro dialog:
    shrnutí + `signedAt` (deklarovaný, označený jako od podepisujícího),
    `arrivedAt`, `algorithm`, S/MIME řetěz (`subject`, `issuer`,
    `notBefore`, `notAfter`, `sha256` každého certifikátu) a stav odvolání,
    OpenPGP `keyTrust`, `primaryFingerprint`, `userIds`, `importable`
    (zpráva nese klíč, který ještě nemáme);
  - `message.signatureRevocation {messageId}` → `SignatureDetails` po
    kontrole (S9); nejvýš jednou za minutu na certifikát;
  - `pgp.keyImport {messageId, partId}` nebo `{armored}`,
    `pgp.keyVerify {fingerprint}`, `pgp.keyList`, `pgp.keyRemove`;
  - `notify.signaturesChanged {}` při změně důvěry (klienti obnoví
    zobrazené zprávy; dotčené řádky přijdou jako `notify.messagesChanged`);
  - chyby: verdikt není chyba; import klíče a kontrola odvolání bez
    adresy `invalidArgument`, síťové selhání kontroly odvolání nový kód
    `revocationFailed` (1510, `message` nestabilní).
  - Každý řetězec z certifikátu/klíče prochází čištěním jako ostatní
    data ze serveru (řídicí, bidi a neviditelné znaky, délka).
- Most MCP (Q8): `read_message`, `list_messages` a `search_messages`
  vracejí `signature` (`kind`, `status`, `signerEmail`, `notes`), signer
  prochází `clean()` a ohradou jako ostatní data pošty; podrobnosti,
  kontrola odvolání ani správa klíčů ne. Pokyn v instrukcích mostu:
  verdikt `valid` neznamená, že obsah je pravdivý nebo bezpečný.

## 5. Zdroje klíčů OpenPGP (Q2: jen bez sítě)

1. **Hlavička `Autocrypt`** v přijaté poště (klíč pro adresu `addr=`,
   jen když `addr` = `From`, zpráva není hromadná ani v nevyžádané
   a projde limity) → `seen` automaticky.
2. **Přiložený klíč** `application/pgp-keys` nebo `*.asc` ve zprávě →
   `importable` v podrobnostech, po kliknutí *Importovat klíč* `seen`.
3. **Ruční import** v Předvolbách (soubor, vložený text) → `seen`;
   potvrzení otisku v podrobnostech nebo Předvolbách → `verified`.

Žádné WKD ani keyservery. Změna klíče pro adresu, která už měla `seen`
klíč, je `keyChanged`; Autocrypt nový klíč nepřebírá sám, nabídne ho
v podrobnostech (TOFU varování).

## 6. Klienti (pravidlo 6: všechny tři v jedné práci)

- **Go reference** `ui/internal/signature`: texty (msgid) verdiktů
  a poznámek, barva a ikona podle `status`, věta odznaku („Podepsal Jan
  Novák <jan@…>“), řádky dialogu podrobností, malá ikona pro řádek
  seznamu (jen `valid`/`validSeen`/`invalid`/`revoked`/`mismatch`;
  šedé stavy v seznamu nic), tlačítka *Zkontrolovat odvolání*,
  *Importovat klíč*, *Potvrdit klíč*, pravidlo skrytí podpisové části
  v dlaždicích (S15). `po/POTFILES`, čeština.
- **GTK**: ikona v `message_row.blp` (vedle sponky), odznak v hlavičce
  `message_view` a karty konverzace (`GtkButton` s ikonou a popiskem,
  `SetUseMarkup(false)`), dialog podrobností `Adw.Dialog`
  (`signature_details.blp`), Předvolby → Zabezpečení → Klíče OpenPGP,
  dlaždice příloh podle S15.
- **macOS**: `MalachiCore/Signatures` (port), ikona v `MessageCellView`,
  odznak v `MessageHeaderView` a `ConversationCardHeader`, popover
  podrobností, Settings pane Security. Žádné `Security.framework` —
  ověřuje démon.
- **Windows**: `Malachi.Core/Signatures`, ikona v `MessageRowView`, odznak
  v hlavičce čtení a karty, `Flyout` podrobností, stránka Zabezpečení
  v Předvolbách. Ikony v `IconGlyphs`.
- Bublina chatu: bez odznaku; okno zprávy a karta ano.
- Přiložená zpráva (`message.embedded`): fáze 1 bez verdiktu.
- Nástěnka: verdikt nemění pravidla případů (jen S14 mění vlastní text);
  karta konverzace v detailu případu odznak má jako karta Pošty.

## 7. Testy

- `backend/testdata/mime/smime-*.eml` a `pgp-*.eml`, vyrobené skriptem
  `backend/testdata/signatures/make.go` (testovací CA, OCSP respondér
  a klíče OpenPGP natvrdo v testdata, jen pro testy; nikdy v produkčním
  poolu): platný (clear i opaque), změněný byte v těle, LF místo CRLF,
  podpis jiným certifikátem, `From` ≠ certifikát, víc podepisujících,
  vnořený `multipart/signed`, podepsaná část zabalená v přeposlané
  zprávě, podepsaná část + nepodepsaný HTML vedle, patička konference
  (S4), propadlý certifikát (před/po příchodu), SHA-1, MD5, RSA 1024,
  DSA, certifikát bez `emailProtection`, řetěz 20 certifikátů, poškozené
  ASN.1 (zkrácené délky, nekonečné vnoření, obří `eContent`), opaque
  s HTML a `cid:` obrázky uvnitř, opaque uvnitř opaque, chráněné hlavičky
  lišící se od vnějších, bidi a řídicí znaky v CN a user ID, inline PGP,
  Autocrypt s jinou `addr`, Autocrypt v hromadné poště, klíč odvolaný,
  `keyChanged`.
- Odvolání: falešný OCSP/CRL server (`revocation/revocationtest`):
  good, revoked před/po příchodu, nepodepsaná odpověď, odpověď pro jiný
  certifikát, přesměrování, privátní adresa v AIA, obří CRL, timeout.
- Fuzz: `FuzzSignedData`, `FuzzSignedSlice` (výřez a kanonizace),
  `FuzzOpaqueUnwrap`, `FuzzPGPSignature`, `FuzzAutocrypt`.
- Store a core: verdikt po uložení i backfillu, přepočet při importu
  klíče a po kontrole odvolání, přeparsování opaque zpráv (výňatek, FTS,
  přílohy), `TestBoardDryRun` s novým `RulesVersion`.
- Skutečné vzorky (T0, ručně, mimo repozitář nebo anonymizované):
  Outlook/Exchange S/MIME přes IMAP i přes Graph `$value` (Exchange
  přestavuje MIME — ověřit, že `multipart/signed` i `signed-data`
  přežijí, jinak Graph účty dostanou `unsupported` s poznámkou), Apple
  Mail S/MIME, Thunderbird PGP/MIME, Proton (PGP/MIME s Autocrypt),
  Gmail S/MIME (Workspace), konference s patičkou (Mailman).
- Klienti: testy Go reference, porty Swift a C#, smoke GTK nad vzorky.

## 8. Odpovědi vlastníka (2026-11-02, AskUserQuestion)

| # | Otázka | Odpověď |
|---|---|---|
| Q1 | Které podpisy ve fázi 1? | **S/MIME i OpenPGP najednou.** |
| Q2 | Odkud klíče OpenPGP? | **Bez sítě**: Autocrypt, přiložený klíč, ruční import (§5). |
| Q3 | Kontrola odvolání S/MIME? | **Na kliknutí** v podrobnostech (S9). |
| Q4 | Odznak i v seznamu zpráv? | **Ano** → verdikt při uložení a migrace (S2, §4). |
| Q5 | Opaque S/MIME i pro výňatek, hledání a nástěnku? | **Ano, už ve fázi 1** (S14). |
| Q6 | Inline PGP? | **Nepodporovaný** (šedý odznak, S11). |
| Q7 | Patička konference? | **Verdikt s poznámkou** `partialFooter` (S4). |
| Q8 | Most MCP? | **Ano, shrnutí** (§4). |

## 9. Další fáze (jen náčrt, rozhodne se později)

**Fáze 2 — dešifrování.** Soukromé klíče nikdy v démonu: pomocník volený
za běhu jako `MALACHI_KEYRING=helper` (`MALACHI_CRYPTO_HELPER`), na
macOS nad Keychain (S/MIME identity), na Windows nad úložištěm
certifikátů (CNG, čipové karty), na Linuxu nad NSS/p11-kit, pro OpenPGP
gpg-agent (socket protokol Assuan, ne linkování) nebo vlastní klíče
v klíčence. Démon posílá pomocníkovi jen zabalený klíč relace. EFAIL
(`security.md` §5): každá šifrovaná část vykreslená sama, vzdálený obsah
vždy blokovaný, OpenPGP jen s MDC/AEAD, žádné „zobrazit přesto“.
Rozhodnutí: dešifrovaný text v SQLite (FTS, výňatek, nástěnka) ano/ne;
co dostane most MCP a asistent (výchozí nic, přepínač);
`neverStoreAttachments` u šifrovaných; paměťová cache jako
`core/memcache.go`. Podpis uvnitř šifrované zprávy ověří táž cesta jako
ve fázi 1.

**Fáze 3 — podepisování.** Builder `internal/smtp` vyrobí
`multipart/signed` (podpis přes pomocníka), přepínač Podepsat v okně Nová
zpráva a výchozí podle účtu, identita = certifikát/klíč adresy
odesílatele; Autocrypt hlavička v odchozí poště; koncepty na serveru
nepodepsané.

**Fáze 4 — šifrování.** Klíče příjemců (certifikáty z ověřených podpisů
fáze 1, `pgp_keys`, případně WKD, firemní GAL/LDAP), přepínač Šifrovat
s varováním u příjemce bez klíče, kopie do Odeslaných šifrovaná i sobě,
koncepty na serveru šifrované nebo jen lokální, odpověď na šifrovanou
zprávu šifrovaně (citace nikdy v čitelné podobě). Graph `sendMail`
s MIME, Gmail jen klientsky.

## 10. Pořadí prací fáze 1

1. T0: skutečné vzorky (§7), ověření Graph `$value`.
2. Démon, část parseru: rozbalení opaque S/MIME v `internal/mime` (S14),
   přeparsování uložené pošty, nové `RulesVersion` nástěnky; testy a fuzz.
3. Démon, ověřování: `internal/smime`, `internal/pgpsig`,
   `internal/signature`, migrace, worker a backfill, API + `docs/api.md`
   + `security.md` §5 (přepsat z „až přijde“ na popis fáze 1), most MCP;
   nezávislá revize.
4. Démon, odvolání: `internal/revocation` + `message.signatureRevocation`.
5. Go reference `ui/internal/signature` + GTK; brána vlastníka v XQuartz.
6. Porty macOS a Windows (Windows přes agenta vlastníka); brány.

## 11. As built (contract and reference, 2026-11-02)

Krok 1 fáze 1: kontrakt API, `docs/api.md`, Go reference
`ui/internal/signature` a texty v `po/`. Démon nic neověřuje, žádná
migrace ani worker; na tomto oddílu staví čtyři souběžné práce (démon,
GTK, macOS, Windows). Co tu je, je pevné; změna kontraktu = změna
`backend/pkg/api/signature.go` + `docs/api.md` §4.21 v jednom commitu
a zpráva ostatním.

### 11.1 Kontrakt (`backend/pkg/api/signature.go`, `docs/api.md` §4.21)

Kompatibilní rozšíření protokolu 2, `ProtocolVersion` zůstává 2.

- `MessageSummary.signature` (opt) = `MessageSignature { kind, status }`
  — listingy, `thread.get`, `search.query`, `thread.list` jen přes
  `latest` (sbalený řádek vlákna ukazuje `latest.signature`, vlákno
  vlastní agregát nemá), `notify.newMessage` jen když verdikt už byl.
- `Message.signature` (opt) = `SignatureSummary { kind, status,
  signer?: {name?, email?, fingerprint?}, notes?: [], partId? }`;
  zastiňuje summary pod týmž klíčem (jako `invitation`).
- `message.signature {messageId}` → `{ signature: SignatureDetails | null }`
  (null = žádný verdikt: bez podpisu celé zprávy nebo ještě neověřeno);
  chyby: messageNotFound.
- `SignatureDetails` = `SignatureSummary` + `messageId`, `reason?`,
  `signedAt?` (deklarovaný), `arrivedAt`, `algorithm?` („RSA 2048 /
  SHA-256“, „ECDSA P-256 / SHA-256“, „Ed25519 / SHA-512“: veřejný klíč
  / digest, jazykově neutrální, UI ukazuje tak, jak je), `expiredAt?`,
  `revokedAt?`, `protectedHeaders?: [{name, value}]`, `smime?`,
  `pgp?`.
  - `SMIMEDetails { chain: [SignatureCertificate], revocation:
    SMIMERevocation }`; `SignatureCertificate { sha256, subject?, issuer?,
    emails?, notBefore, notAfter, systemRoot?, selfSigned? }` (vlastní typ,
    ne `CertificateInfo` z TLS); `SMIMERevocation { state, checkedAt?,
    revokedAt?, source?, checkable }`.
  - `PGPDetails { keyTrust, keyId?, primaryFingerprint?, userIds?,
    keySource?, firstSeen?, previousFingerprint?, importable?: PGPKeyOffer }`;
    `PGPKeyOffer { fingerprint, partId? | autocrypt? }` (právě jedno;
    Autocrypt klíč pro adresu, která už jiný klíč má, se nabízí tudy —
    hlavička nemá `partId`).
- `message.signatureRevocation {messageId}` → `{ signature:
  SignatureDetails }`; chyby messageNotFound, invalidArgument (bez
  verdiktu, ne S/MIME, bez certifikátu, `checkable` false), offline,
  **1510 `revocationFailed`** s `data = RevocationFailedData { reason:
  network|timeout|server|response|address }`. Do minuty od poslední
  kontroly téhož certifikátu (`RevocationCheckInterval`) vrátí uložený
  stav bez sítě. Celá kontrola ≤ 15 s (`RevocationTimeout`), **klienti
  dají volání timeout 20 s**.
- `pgp.keyImport`: právě jedno z `{messageId, partId}`, `{messageId,
  autocrypt: true}`, `{armored}`, `{data}` (base64 binárního klíče)
  → `{ keys: [PGPKey], added, updated }`; chyby messageNotFound,
  partNotFound, partNotDownloaded, invalidArgument (špatný požadavek) a
  invalidArgument s `data = PGPImportErrorData { reason:
  noKey|malformed|secretKey|tooLarge|tooMany }`. Tajný klíč se odmítne
  celý (nic se neuloží), nikdy se z něj „vytáhne“ veřejná část.
  Uložený klíč si nechá důvěru a přibere user ID, odvolání, expiraci.
- `pgp.keyVerify { fingerprint, verified: bool }` → `{ key: PGPKey }`
  (`verified: false` = zpět na `seen`; *Zrušit potvrzení* v Předvolbách);
  neznámý otisk = invalidArgument.
- `pgp.keyList {}` → `{ keys: [PGPKey] }` řazené podle první adresy,
  pak otisku. `pgp.keyRemove { fingerprint }` → `{}`.
- `PGPKey { fingerprint, userIds, emails, trust (seen|verified), source
  (autocrypt|attached|import), firstSeen, createdAt, expiresAt?,
  revokedAt?, algorithm? }`.
- `notify.signaturesChanged {}` (`SignaturesChangedNotification`,
  rozhraní `SignatureNotifier` asertované jako `BoardNotifier`): klient
  znovu `message.get` zobrazených zpráv, `message.signature` otevřeného
  dialogu a `pgp.keyList` zobrazeného seznamu; změněné řádky přijdou
  jako `notify.messagesChanged`.
- Enumy (otevřené; neznámý `status` = `error`, neznámé poznámky, důvody,
  stavy a zdroje se ignorují):
  - `kind`: `smime`, `pgp`.
  - `status`: `invalid`, `revoked`, `mismatch`, `untrusted`,
    `unknownKey`, `weak`, `unsupported`, `error`, `valid`, `validSeen`.
  - `notes`: `expiredSince`, `revocationUnchecked`, `revocationGood`,
    `revokedAfter`, `partialFooter`, `protectedHeadersDiffer`,
    `inlinePGP`, `keyChanged`, `opaque`.
  - `reason` (nové, plán ho neměl; „proč ne“ v podrobnostech, chybí
    u valid, validSeen, mismatch, unknownKey, revoked): invalid
    `digestMismatch`, `badSignature`, `malformed`, `md5`, `keyUsage`;
    untrusted `unknownIssuer`, `selfSigned`, `notYetValid`, `expired`;
    weak `sha1`, `shortKey`; unsupported `algorithm`,
    `multipleSigners`, `rebuilt` (Exchange/Graph přestavěl MIME),
    `format`; error `limit`, `internal`. Inline PGP má jen poznámku.
  - `RevocationState`: `unchecked`, `good`, `revoked`, `unknown`;
    `RevocationSource`: `ocsp`, `crl`.
  - `PGPKeyTrust`: `unknown` (jen v podrobnostech), `seen`, `verified`;
    `PGPKeySource`: `autocrypt`, `attached`, `import`.
  - `ProtectedHeaderName`: `subject`, `from`, `to`, `cc`, `replyTo`, `date`.
- Limity (konstanty `api`): `MaxSignatureTextBytes` 256 (každý řetězec
  z certifikátu/klíče), `MaxSignatureChain` 16, `MaxSignatureEmails` 16,
  `MaxSignatureUserIDs` 32, `MaxProtectedHeaders` 6, `MaxPGPKeyBytes`
  1 MiB, `MaxPGPImportKeys` 20, `MaxPGPStoredKeys` 5000,
  `MaxRevocationResponseBytes` 1 MiB, `RevocationTimeout` 15 s,
  `RevocationCheckInterval` 1 min.
- Formáty: `signer.fingerprint` = SHA-256 certifikátu podepisujícího (64
  hex) nebo primární otisk klíče (40 v4 / 64 v6), vše **malá hex bez
  mezer**; `keyId` 16 malých hex. Klienti formátují `FormatFingerprint`.
- `partId` = oddělená podpisová část `multipart/signed`; chybí u opaque
  S/MIME a inline PGP. `hasAttachments` ji u zprávy s verdiktem
  **nepočítá** (démon), klienti ji skryjí z dlaždic (S15).
- Metody přidané na konec `AllMethods` (za update), notifikace na konec
  `AllNotifications`; testy pořadí v `pkg/api` přepočítané.
- **Rozhraní služeb** `SignatureService { Signature, Revocation }` a
  `PGPService { KeyImport, KeyVerify, KeyList, KeyRemove }` jsou
  v `service.go`, ale **zatím nejsou v `api.Backend`** (jinak by se
  nepřeložil core). Proto teď padá
  `internal/rpc TestAllMethodsRegistered` (6 metod bez handleru) — první
  úkol démona.

### 11.2 Co musí udělat démon (agent a)

1. `api.Backend` += `Signatures() SignatureService`, `PGP() PGPService`;
   implementace v `core`, `rpc.StubBackend`, `registerSignatures`
   v `internal/rpc/dispatch.go`, fake v `cmd/malachi-mcp/fake_backend_test.go`;
   RPC server implementuje `SignatureNotifier`.
2. Plnit `MessageSummary.signature` všude, kde se staví souhrn (listingy,
   `thread.get`, `thread.list` `latest`, `search.query`), a
   `Message.signature` v `message.get`/`message.download`; nikdy
   v `message.embedded`.
3. Každý řetězec z certifikátu/klíče čistit (řídicí, format/bidi,
   neviditelné znaky, řádkové oddělovače) a ořezat na 256 B na hranici
   runy; seznamy na limity výše. Nic z certifikátu do logu (S16).
4. `signer` vyplnit vždy u valid, validSeen, mismatch, untrusted, weak,
   revoked. `reason` podle tabulky výše. `notes` v pořadí výčtu.
5. Most MCP (Q8): `signature` = `kind`, `status`, `signerEmail`, `notes`
   (`docs/mcp.md`), `security.md` §5 přepsat.

### 11.3 Go reference `ui/internal/signature` (pro GTK a porty 1:1)

Soubory: `signature.go` (pravidla a modely, bez msgid), `text.go` (všechny
msgid; v `po/POTFILES` jako poslední řádek), testy `signature_test.go`
a `po_test.go` (každý msgid je v šabloně a naopak). Překladač je
`jira.Translator` (`T`, `N`, `C`); GTK předá `i18n.Tr`.

Exportované API: `KindName`, `SMIMEName`/`OpenPGPName`/`ArmorPlaceholder`
(nepřekládá se), `Severity` + `SeverityOf`, `IconOf`, `StyleOf`;
`ListIconFor(*api.MessageSignature) (ListIcon{Icon, Style, Tooltip}, bool)`;
`BadgeFor(*api.SignatureSummary) Badge{Visible, Severity, Icon, Style, Text,
Tooltip}`; `SignerLabel`; `HidesPart`, `TileAttachments` (S15);
`DetailsFor(details, from, busy Action, date func(time.Time) string)
Details{Severity, Icon, Style, Title, Explanation, Rows, Notes, Sections,
Caveat, Buttons, RevocationCaption, KeyFingerprint}` s `Row{Key, Label,
Value, Monospace}`, `Section{Key, Title, Rows}`, `Button{Action, Label,
Enabled}`; `CanCheckRevocation`, `ImportParams`, `ConfirmParams`;
`RevocationFailureOf`, `ImportFailureOf` (čtou `error.data` ze struktury
i z dekódovaného JSON), `RevocationFailedText`, `ImportFailedText`,
`ImportedText`; `PastedParams`, `FileParams` (armor → `armored`, jinak
`data`; klient čte nejvýš `MaxPGPKeyBytes`+1 B); `KeyRows`/`KeyRowFor`
→ `KeyRow{Fingerprint, Title, Subtitle, Origin, TrustPill, Verified,
StatePill, Actions}`; `StatusTitle`, `NoteText`, `ReasonText`,
`DialogTitle`, `BadgeTooltip`, `CloseLabel`, `RevocationCaption`,
`ConfirmKeyQuestion`, `RemoveKeyQuestion` (`Question{Heading, Body,
Cancel, Accept, Destructive}`), `KeyConfirmedText`,
`ConfirmationRemovedText`, `KeyRemovedText`, `ImportDialogTexts`,
`SecurityPageTexts`; `FormatFingerprint` (16/40/64 hex → velká písmena
po čtyřech, jinak "").

Pravidla:

- **Status → závažnost → barva → ikona**:

  | status | Severity | GTK třída | macOS | Windows | ikona |
  |---|---|---|---|---|---|
  | valid | Good | `success` | systemGreen | SystemFillColorSuccess | `security-high-symbolic` |
  | validSeen | GoodSeen | `success` | systemGreen | SystemFillColorSuccess | `security-medium-symbolic` |
  | mismatch, untrusted, weak | Warning | `warning` | systemOrange | SystemFillColorCaution | `dialog-warning-symbolic` |
  | invalid, revoked | Bad | `error` | systemRed | SystemFillColorCritical | `dialog-error-symbolic` |
  | unknownKey, unsupported, error, neznámý | Neutral | `dim-label` | secondaryLabelColor | TextFillColorSecondary | `security-low-symbolic` |

  Ikony jsou existující symbolické ikony Adwaity (plný štít, poloviční,
  prázdný; varování; chyba), **žádná vlastní SVG**; stránka Předvoleb má
  `IconPage` = `security-high-symbolic`. GTK agent ověří v kontejneru, že
  `security-{high,medium,low}-symbolic` v `/usr/share/icons/Adwaita` jsou;
  kdyby ne, vyrobí `malachi-signature-{valid,seen,unknown}-symbolic.svg`
  a změní konstanty v referenci i tady. Windows: `IconGlyphs` má
  `dialog-warning` a `dialog-error`, doplní `security-high`,
  `security-medium`, `security-low`. macOS doporučení SF Symbols:
  `checkmark.shield.fill`, `shield.lefthalf.filled`,
  `exclamationmark.triangle.fill`, `xmark.octagon.fill`, `shield`.
- **Seznam**: ikona jen pro valid, validSeen, invalid, revoked, mismatch
  (`ListIconFor`); untrusted, weak a šedé v seznamu nic. Sbalený řádek
  vlákna předá `latest.signature`.
- **Odznak** (`BadgeFor`): tlačítko s ikonou a větou v hlavičce panelu
  čtení, okna zprávy a karty konverzace (i na Nástěnce); ne v bublině
  chatu ani v přiložené zprávě; klik otevře dialog (`message.signature`).
  Věta: valid/validSeen „Signed by %s“, mismatch „Signed by someone else:
  %s“, untrusted „Signed by %s, but the certificate is not trusted“ /
  „…, but the key had expired“ (pgp), weak „Signed by %s with a weak
  algorithm“, revoked „Signed with a revoked certificate/key“, invalid
  „Invalid signature“, unknownKey „Signed with an unknown OpenPGP key“,
  unsupported „Signature not verified“ nebo s `inlinePGP` „Inline OpenPGP
  signature, not verified“, error „Signature could not be verified“; bez
  signera náhradní věty bez %s. `%s` = `SignerLabel` („Jméno <adresa>“,
  jinak adresa, jinak jméno).
- **Dialog** (`DetailsFor`): nadpis `StatusTitle` s ikonou, odstavec
  `Explanation` (u mismatch s adresou signera a `From`, proto parametr
  `from`), hlavní řádky v pořadí Signed by, Signature type, Problem,
  Signed (as stated), Arrived, Algorithm, pak S/MIME Revocation, OpenPGP
  Key, Key fingerprint (nebo Key ID u neznámého), Key source, First seen,
  User IDs (víc řádků, `\n`), Previous key, pak Expired, Revoked (jen
  pgp); `Notes` (bez `revocationUnchecked`/`revocationGood`, ty říká
  řádek Revocation); sekce certifikátů (podepisující, zprostředkující,
  kořen = poslední se `systemRoot` nebo `selfSigned`) s řádky Issued to,
  E-mail addresses, Issued by, Valid („%s to %s“), SHA-256 fingerprint;
  sekce Signed Headers; `Caveat` jen u valid/validSeen; tlačítka v pořadí
  Check Revocation (S/MIME s řetězem a `checkable`; po první kontrole
  „Check Revocation Again“, během „Checking…“ vypnuté; pod ním
  `RevocationCaption`), Import Key (`importable`), Confirm Key… (pgp
  `seen` s otiskem a ne invalid/revoked; otázka `ConfirmKeyQuestion` +
  `KeyFingerprint` monospace pod textem). Hodnoty jsou prostý text,
  vybíratelný; `Monospace` u otisků. `date` = formát data a času hlaviček
  zprávy daného klienta. Zavřít: vlastní tlačítko dialogu platformy, jinak
  `CloseLabel` („Close“).
- **Dlaždice příloh**: `TileAttachments(atts, message.signature)` před
  dosavadními pravidly (inline obrázky, `.ics` pozvánky).
- **Předvolby → Zabezpečení** (`SecurityPageTexts`): titul
  `C("preferences page", "Security")` (GTK `Adw.PreferencesPage`
  s `IconPage`, macOS pane Security, Windows navigační položka), skupina
  „OpenPGP Keys“ s popisem, tlačítko „Import Key…“ (dialog
  `ImportDialogTexts`: text area s placeholderem `ArmorPlaceholder`,
  „Choose File…“ s filtrem „OpenPGP keys“ `*.asc`, `*.gpg`, `*.pgp`,
  `*.key`, „_Cancel“, „_Import“ aktivní jen s `PastedParams` ok), prázdný
  stav „No OpenPGP Keys“, chyba načtení „Could not load the keys.“, řádky
  `KeyRows` (titulek, otisk monospace, `Origin`, pilulka důvěry —
  `Verified` zeleně —, pilulka stavu, nabídka akcí Confirm Key… / Remove
  Confirmation, Remove Key… s `RemoveKeyQuestion`). Toasty
  `ImportedText`, `KeyConfirmedText`, `ConfirmationRemovedText`,
  `KeyRemovedText`; chyby `ImportFailedText(ImportFailureOf(err))`
  a `RevocationFailedText(RevocationFailureOf(err))` (jiné chyby přes
  obvyklý `RpcErrorText`/`widget` klienta).
- Všechno ze serveru jen jako prostý text (`SetUseMarkup(false)`,
  `stringValue`, `TextBlock.Text`); reference čistí znovu (`clean`).

### 11.4 Texty (msgid) podle funkcí

Všechny jsou v `po/malachi.pot` a česky v `po/cs.po` (ručně ve formátu
xgettext/msgmerge, ověřeno přečtením a znovu zapsáním celého souboru beze
změny; `make po` by měl vyjít beze změny, nanejvýš přečíslované `#:`).
Sdílené existující msgid: `certificate` | `SHA-256 fingerprint`, `Issued
to`, `Issued by`, `Valid`, `%s to %s`; `Close`; `_Cancel`; `_Remove`.
Nic není ve `windows/parity-exclusions.txt`: Windows použije všechny.
Žádný klient nepřidává pro podpisy vlastní msgid; kdyby GTK dávalo
texty do Blueprintu, musí to být přesně tytéž msgid a kontexty.

- `badgeText`: `Valid signature`; `Signed by %s`; `Signed by someone else`; `Signed by someone else: %s`; `Untrusted signature`; `Signed by %s, but the key had expired`; `Signed by %s, but the certificate is not trusted`; `Weak signature`; `Signed by %s with a weak algorithm`; `Signed with a revoked key`; `Signed with a revoked certificate`; `Invalid signature`; `Signed with an unknown OpenPGP key`; `Inline OpenPGP signature, not verified`; `Signature not verified`; `Signature could not be verified`
- `StatusTitle`: `Valid signature`; `Valid signature, key not confirmed`; `Signed by someone else`; `Untrusted signature`; `Weak signature`; `Invalid signature`; `Revoked key`; `Revoked certificate`; `Unknown key`; `Signature not verified`; `Signature could not be verified`
- `explanation`: `The signature matches the message, and you confirmed the key.`; `The signature matches the message, and the certificate was issued by an authority this computer trusts.`; `The signature matches the message. The key was found automatically: compare its fingerprint with the sender to confirm it.`; `The signature is valid, but it belongs to %s, not to the sender %s.`; `The signature is valid, but it does not belong to the sender's address.`; `The signature matches the message, but the key had expired when the message arrived.`; `The signature matches the message, but the certificate cannot be trusted.`; `The signature matches the message, but it uses an algorithm that is no longer considered safe.`; `The message may have been changed after it was signed. Do not trust its content.`; `The key was revoked before the message arrived. Do not trust its content.`; `The certificate was revoked before the message arrived. Do not trust its content.`; `The message is signed with an OpenPGP key that Malachi Mail does not have. Import the sender's key to verify it.`; `Malachi Mail cannot verify this kind of signature.`; `Malachi Mail could not verify the signature.`
- `caveat`: `A valid signature tells who sent the message, not that its content is true or safe.`
- `NoteText`: `The key has expired since the message arrived.`; `The certificate has expired since the message arrived.`; `Whether the certificate was revoked has not been checked.`; `The certificate was not revoked when last checked.`; `The key was revoked after the message arrived.`; `The certificate was revoked after the message arrived.`; `A footer added after signing, such as a mailing list's, is not covered by the signature.`; `The signed headers differ from the ones shown.`; `Inline OpenPGP signatures are not verified: they do not cover attachments or formatting.`; `This sender used a different key before.`; `The signed content was unpacked from the signature.`
- `ReasonText`: `The message was changed after it was signed.`; `The signature does not match the signer's certificate or key.`; `The signature is damaged.`; `The signature uses MD5, which is broken.`; `The certificate is not meant for signing e-mail.`; `The certificate was not issued by an authority this computer trusts.`; `The certificate is self-signed.`; `The certificate was not valid yet when the message arrived.`; `The key had expired when the message arrived.`; `The certificate had expired when the message arrived.`; `The signature uses SHA-1, which is no longer considered safe.`; `The signing key is shorter than 2048 bits.`; `The signature uses an algorithm Malachi Mail does not support.`; `The message has more than one signer, which Malachi Mail does not support.`; `The mail server changed the message, so its signature cannot be checked.`; `Malachi Mail does not support this kind of signed message.`; `The signature is too large or too complex to check.`
- `rowLabel`: `signature` | `Signed by`; `signature` | `Signature type`; `signature` | `Problem`; `signature` | `Signed, as stated by the signer`; `signature` | `Arrived`; `signature` | `Algorithm`; `certificate` | `SHA-256 fingerprint`; `signature` | `Key fingerprint`; `signature` | `Key ID`; `signature` | `Key`; `signature` | `Key source`; `signature` | `First seen`; `signature` | `User IDs`; `signature` | `Previous key`; `signature` | `Expired`; `signature` | `Revoked`; `signature` | `Revocation`; `certificate` | `Issued to`; `certificate` | `Issued by`; `certificate` | `Valid`; `signature` | `E-mail addresses`
- `validity`: `certificate` | `%s to %s`
- `keyTrustText`: `Confirmed by you`; `Found automatically, not confirmed`; `Not in Malachi Mail`
- `keySourceText`: `Autocrypt header of a message`; `Attached to a message`; `Imported by you`
- `revocationText`: `Not revoked (checked %s)`; `Revoked on %s`; `revocation` | `Revoked`; `Unknown to the issuer (checked %s)`; `Not checked`
- `sectionTitle`: `Signer's Certificate`; `Intermediate Certificate`; `Root Certificate`; `Signed Headers`
- `headerName`: `signed header` | `Subject`; `signed header` | `From`; `signed header` | `To`; `signed header` | `Cc`; `signed header` | `Reply-To`; `signed header` | `Date`
- `DialogTitle`: `Signature Details`
- `BadgeTooltip`: `Show Signature Details`
- `CloseLabel`: `Close`
- `buttonLabel`: `Checking…`; `Check Revocation Again`; `Check Revocation`; `Import Key`; `Confirm Key…`; `Remove Confirmation`; `Remove Key…`
- `RevocationCaption`: `Checking tells the certificate's issuer that the certificate is in use.`
- `ConfirmKeyQuestion`: `Confirm This Key?`; `Compare this fingerprint with the one the sender gives you in person or in another way you trust.`; `_Cancel`; `_Confirm Key`
- `RemoveKeyQuestion`: `Remove This Key?`; `Messages signed with it will show an unknown key until it is imported again.`; `_Cancel`; `_Remove`
- `KeyConfirmedText`: `Key confirmed`
- `ConfirmationRemovedText`: `Confirmation removed`
- `KeyRemovedText`: `Key removed`
- `RevocationFailedText`: `Could not reach the certificate's issuer.`; `The certificate's issuer did not answer in time.`; `The certificate's issuer could not answer.`; `The issuer's answer could not be verified.`; `The certificate names an address Malachi Mail does not contact.`; `Could not check whether the certificate was revoked.`
- `ImportFailedText`: `This is not an OpenPGP public key.`; `The key cannot be read.`; `This is a secret key. Import only the public key: Malachi Mail never stores secret keys.`; `The key data is too large.`; `Malachi Mail cannot store more keys. Remove keys you no longer need.`; `The key could not be imported.`
- `ImportedText`: `%d key imported` / `%d keys imported` (plural); `%d key updated` / `%d keys updated` (plural); `The key was already imported.`
- `ImportDialogTexts`: `Import OpenPGP Key`; `Paste a public key, or choose a file with one.`; `Choose File…`; `OpenPGP keys`; `_Cancel`; `_Import`
- `SecurityPageTexts`: `preferences page` | `Security`; `OpenPGP Keys`; `Keys of people whose signed mail you receive. Malachi Mail finds them in your mail and never looks them up online.`; `No OpenPGP Keys`; `Import Key…`; `Could not load the keys.`
- `keyTrustPill`: `key trust` | `Confirmed`; `key trust` | `Not confirmed`
- `keyStatePill`: `key state` | `Revoked`; `key state` | `Expired`
- `keyOrigin`: `Found in a message header on %s`; `Imported from a message on %s`; `Imported on %s`

Česky klíčové termíny: Podepsal(a) %s, Platný podpis, Neplatný podpis,
Zkontrolovat odvolání, Importovat klíč, Potvrdit klíč…, Zabezpečení,
Klíče OpenPGP, Otisk klíče, Potvrzený / Nepotvrzený, Odvolaný, Propadlý.

### 11.5 Pro porty a GTK navíc

- macOS: `MalachiCore/Signatures` (port 1:1 včetně testů
  `signature_test.go`), API zrcadlo z `docs/api.md` §4.21, `L10n.T/N/C`
  s klíčem = msgid. Windows: `Malachi.Core/Signatures` (jmenný prostor
  `Signatures`, třída třeba `SignatureView`, aby se nestínila), zrcadlo
  API, `GoContractTests` (6 metod a 1 notifikace navíc, nový kód 1510,
  nové limity).
- Timeout `message.signatureRevocation` 20 s; ostatní obvyklý.
- Po `notify.signaturesChanged` obnovit zobrazené zprávy, otevřený dialog
  a seznam klíčů; po `pgp.keyImport`/`keyVerify`/`keyRemove` stačí
  notifikace (démon ji pošle), dialog může obnovit hned sám.
- Dokud démon metody neimplementuje, vracejí `notImplemented` (1000):
  dialog pak ukáže jen to, co nese `Message.signature` (odznak se bez
  verdiktu neukazuje vůbec).

## As built (GTK)

Krok 5 fáze 1, GTK klient (2026-11-02, necommitnuté; napsané na stroji
bez GTK, gotk4 a blueprint-compileru). Reference `ui/internal/signature`
je použitá tak, jak je: **žádný nový msgid**, žádný text v Blueprintu,
nové Go soubory nemají `i18n.T/N/C`, proto nejsou v `po/POTFILES`.
Ikony `security-{high,medium,low}-symbolic` v Adwaitě jsou (ověřeno
v tagech 47.0 a 48.0 zrcadla adwaita-icon-theme na GitHubu; Fedora 42 má
48), záložní SVG z §11.3 tedy není potřeba.

- **Seznam**: `message_row.blp` má `Image signature_icon` mezi sponkou
  a hvězdičkou; `widget.Message.Signature` a `widget.Thread.Signature`
  (`signature.ListIcon`) plní `summaryMessage` (`model.go`)
  a `summaryThread` přes `threadSignatureIcon` (`latest.signature`);
  `MessageRow.setSignature` přepíná třídu stylu, tooltip je
  `StatusTitle`; `SignatureIcon()` pro testy.
- **Odznak** (`window/signature.go`): `signatureBadge` = plochý
  `GtkButton` (třída `signature-badge`) s ikonou a popiskem
  (`SetUseMarkup(false)`, zalamovaný), barva třídou na ikoně i popisku.
  Panel čtení a okno zprávy: `messageView.bindSignatureBadge` ho z Go
  vloží hned **pod datum** (`InsertChildAfter` v rodiči
  `message_date`; `window.blp` ani `message_window.blp` se neměnily),
  jen u pohledu s `invitation_card_slot` — pohled přiložené zprávy ho
  nemá. `render` volá `renderSignature`. Karta konverzace: odznak je ve
  `WrapBox` hlavičky za štítky (`convCard.sig`), ukazuje se i sbalený,
  dvojklik na něm okno neotevírá (`onHeader`). Verdikt je
  `shownSignature`: `Message.signature` z `message.get`; dokud
  `message.get` neodpověděl, odznak není (aby věta za chvíli nezměnila
  text); po selhaném `message.get` jen druh a stav ze souhrnu.
  `conversationController.needsBody` žádá `message.get` i pro člena se
  `signature` (jinak by karta neměla podepisujícího). Bublina chatu
  odznak nemá.
- **Dialog** `signature_details.blp` + `window/signature_details.go`:
  `Adw.Dialog` (titul `DialogTitle`, zavírá vlastní tlačítko hlavičky
  a Escape), hned ukáže shrnutí z odznaku a spinner, pak
  `message.signature` (selhání, `notImplemented` i `null` nechají
  shrnutí, jen se zaloguje). Řádky a sekce jako `GtkGrid` (popisek
  `dim-label`, hodnota vybíratelná, nefokusovatelná, otisky `monospace`),
  poznámky, výhrada; tlačítka Check Revocation (+ spinner, popisek pod
  ním; timeout 20 s, `signatureRevocationTimeout`; chyba
  `RevocationFailedText(RevocationFailureOf(err))` v řádku `error`),
  Import Key (`ImportParams`, toast `ImportedText` v hlavním okně,
  chyba `ImportFailedText`), Confirm Key… (`ConfirmKeyQuestion` jako
  `Adw.AlertDialog` s otiskem monospace v `extra-child`). Tlačítka se
  nepřestavují (klávesnice zůstává), čekající předá fokus scrolleru.
  Jeden dialog na hlavní okno (`sigDialogs`).
- **Předvolby → Zabezpečení** (`window/preferences_security.go`):
  stránka postavená z Go a přidaná **za AI** (`bindSecurity` v seznamu
  úklidu `NewPreferences`), ikona `IconPage`, skupina „OpenPGP Keys"
  s popisem a tlačítkem Import Key… v hlavičce skupiny; řádky
  `KeyRows` jako vlastní `GtkListBoxRow` (titulek, otisk monospace
  vybíratelný, původ, pilulka důvěry — `key-verified` zeleně —, pilulka
  stavu `key-state` červeně, `MenuButton` s akcemi v popoveru), stavový
  řádek prázdný / chyba načtení. Confirm Key… a Remove Key… se ptají
  (`askSignatureQuestion`), Remove Confirmation ne; toasty
  `KeyConfirmedText`/`ConfirmationRemovedText`/`KeyRemovedText`.
  Import: `Adw.AlertDialog` s `GtkTextView` (monospace, zástupný text
  `ArmorPlaceholder` jako překryv), _Import aktivní jen s
  `PastedParams` ok, Choose File… zavře dialog a otevře `GtkFileDialog`
  s filtrem „OpenPGP keys" (`*.asc`, `*.gpg`, `*.pgp`, `*.key`), čte
  nejvýš `MaxPGPKeyBytes`+1 B (víc = `ImportFailedText(tooLarge)` bez
  démona), pak `FileParams`.
- **Chyby bez věty v referenci** (selhané `pgp.keyVerify`/`keyRemove`):
  `widget.RPCErrorText(actionWhat(a), err)`, kde `actionWhat` je popisek
  tlačítka bez mnemoniky a výpustky („Confirm Key failed: …"). Lepší by
  byl msgid v průběhovém tvaru („Confirming the key"), ale nový msgid by
  rozbil `StringsCheckTests` Windows; kdyby se přidával, tak do reference
  a do všech tří klientů najednou.
- **S15**: `tileAttachments(m)` = `signature.TileAttachments` před
  `chipAttachments` v panelu, kartách i počtu sponky bubliny chatu.
- **`notify.signaturesChanged`** (`notify.go`): `signaturesChanged`
  znovu zavolá `message.get` pro každou zprávu v cache, která verdikt
  má (nejvýš 64), uloží a ukáže ji všude (`showLoaded`); otevřený
  dialog znovu `message.signature`; stránka Zabezpečení poslouchá přes
  `listenSignatures`/`dispatchSignatures` a znovu `pgp.keyList`.
  Callbacky RPC všude přes `glib.IdleAdd`.
- CSS (`internal/style/style.go`): `label.issue-pill.key-verified`,
  `.key-state`, `button.signature-badge`.
- **Nedělá**: karty konverzace v detailu případu Nástěnky (§6 to chce)
  odznak nemají — nesdílejí hlavičku s kartou Pošty a `message.get`
  nevolají (jen `fetchBodyOnly`); doplnit by znamenalo `message.get` na
  kartu nástěnky (otázka pro vlastníka). Okno přiložené zprávy a bublina
  chatu záměrně bez odznaku.
- **Testy**: `window/signature_test.go` (čisté: `shownSignature`,
  odznak, `fromAddress`, S15 i u sponky bubliny, ikony řádků zprávy
  a vlákna, `actionWhat`) a `window/signature_gtk_smoke_test.go`
  (`MALACHI_GTK_SMOKE=1 go test ./internal/window -run 'Signature'`:
  ikona řádku nad skutečným `message_row.blp`, odznak a jeho barvy,
  dialog nad vymyšlenými S/MIME a OpenPGP podrobnostmi ze skutečného
  `signature_details.blp`, stránka Zabezpečení bez démona a s klíči).
- `po/`: posunuté jen odkazy `#:` šesti souborů z `po/POTFILES`
  (`widget/message_row.go`, `window/message_view.go`,
  `conversation_card.go`, `notify.go`, `preferences.go`,
  `attachments.go`), ručně skriptem podle diffu proti `HEAD`; `make po`
  má vyjít beze změny.
- **Ověřeno zde**: `gofmt`, typová kontrola `go/types` s `FakeImportC`
  nad zdroji gotk4 (balíčky `widget` a `window` včetně testů, bez
  chyb kromě šumu C konstant), `go test ./internal/signature`.
- **Čeká v kontejneru Fedora 42**: `scripts/gtk-container/gtk.sh build`
  (beze změny obrazu), `gtk.sh make build` (Blueprint
  `signature_details.blp`, překlad), `gtk.sh test-ui`, smoke
  `gtk.sh sh -c 'cd ui && MALACHI_GTK_SMOKE=1 go test ./internal/window
  -run Signature -count=1 -v'`, `make po` (beze změny), `make lint`;
  ručně v XQuartz nad démonem s verdikty (až ho démon bude mít): ikona
  v seznamu, odznak v panelu, okně zprávy a kartě konverzace, dialog
  (S/MIME s kontrolou odvolání, OpenPGP s importem a potvrzením),
  skrytá podpisová dlaždice, Předvolby → Zabezpečení (import vložením
  i souborem, potvrzení, zrušení, odebrání), obnova po
  `notify.signaturesChanged`.

## As built (macOS)

Krok 6 fáze 1, macOS klient (2026-11-02, necommitnuté; napsané na stroji
bez Swiftu, takže nic není přeložené ani spuštěné). Reference
`ui/internal/signature` je portovaná 1:1, **žádný nový msgid** (klíč =
GTK msgid, `check-strings.py` 0 missing); texty jen pro macOS jsou tři
anglické věty selhání (viz níže).

- **MalachiCore**: `API/SignatureAPI.swift` (zrcadlo §4.21: otevřené enumy
  `SignatureKind/Status/Note/Reason`, `RevocationState/Source`,
  `PGPKeyTrust/Source`, `RevocationFailure`, `PGPImportFailure`, typy
  `MessageSignature`, `SignatureSummary`, `SignatureDetails` (Go vložení
  jako `summary`, ploché kódování jako `Message`), `PGPKey`, params
  a výsledky, metody `API.MessageSignatureGet/Revocation`, `PGPKey*`,
  `API.signatureMethods`, limity `API.Limits.maxSignature*`/`maxPGP*`,
  `RPCError.revocationFailedData`/`pgpImportErrorData`);
  `ErrorCode.revocationFailed` 1510; `RPCTimeouts.revocation` 20 s;
  `Notify.signaturesChanged`; `MessageSummary.signature`
  (`MessageSignature`) a `Message.signature` (`SignatureSummary`, zastiňuje
  summary pod týmž klíčem). `Signatures/SignatureView.swift` + `SignatureText.swift`
  = port `signature.go` a `text.go` (`enum SignatureView`, jmenovitě jako
  na Windows; `statusTitle`, `badge(for:)`, `listIcon(for:)`, `details(...)`,
  `keyRows`, `tileAttachments`, `pastedParams`/`fileParams`, otázky,
  `symbolName` = mapování ikon na SF Symbols z §11.3). Kontrolery:
  `SignatureController` (popover podrobností: `message.signature`, Check
  Revocation s timeoutem 20 s, Import Key, Confirm Key… s otázkou; do
  odpovědi daemona ukazuje shrnutí z `Message.signature`, `notImplemented`
  nechá shrnutí), `PGPKeysController` (Nastavení → Zabezpečení: `pgp.keyList`,
  import vložením/souborem, Confirm Key…, Remove Confirmation, Remove Key…),
  `SignatureState` (počítadlo a pozorovatelé `notify.signaturesChanged`),
  `MessageCache.refreshSignatures` (znovu `message.get` pro zprávy v cache,
  které verdikt mají; zobrazení se překreslí přes `onLoaded`),
  `ConversationController.needsBody` žádá `message.get` i pro člena se
  `signature` (jako GTK, karta bez něj nezná podepisujícího). **S15**:
  `ChipPlan` volá `SignatureView.tileAttachments` před `chipAttachments`
  (panel, karty i počet sponky v bublině chatu).
- **MalachiMail**: `Shared/SignatureBadgeView.swift` (`SignatureTint`:
  systemGreen/Orange/Red/secondary; borderless `NSButton` s SF Symbolem
  a větou jako `attributedTitle` jen s fontem, barvou a odstavcem);
  odznak v `MessageHeaderView` (pod datem; panel čtení a okno zprávy, ne
  přiložená zpráva) a jako řádek `Slot.signature` v `ConversationCardView`
  (pod hlavičkou, nad pruhy; není v bublině chatu); ikona vedle sponky
  v `MessageCellView` (jen stavy z `listIcon`, u sbaleného vlákna
  `row.message` = nejnovější člen); `MessageView/SignatureDetailsPopover.swift`
  (`NSPopover` 380 × nejvýš 520, hodnoty prostý vybíratelný text, otisky
  monospace, tlačítka z modelu; otázka Confirm This Key? jako list
  s otiskem v `accessoryView`: nová metoda `Alerts.confirmKey`);
  `MessageActionDelegate.showSignature(of:relativeTo:)`;
  `Preferences/SecurityPaneViewController.swift` (poslední karta Nastavení,
  skupina „OpenPGP Keys“, tlačítko Import Key… v záhlaví skupiny, řádek
  klíče `SecurityKeyRowView`, list `SecurityImportSheet` s textovým polem,
  Choose File… a čtením nejvýš `maxPGPKeyBytes + 1` B);
  `App/Integration+Signatures.swift` (notifikace → cache + `SignatureState`;
  `AppState.addSignaturesChanged`, `AppState.signatures`).
- **Vývojové kroky** `MALACHI_START`: `signature-popover` (S/MIME s řetězem),
  `signature-popover:pgp`, `signature-popover:bad`, `prefs-security`
  (`MainWindowController+DevStartSignature.swift`, bez démona:
  `SignatureController.seed`).
- **Odchylky** (řádky v tabulce `macos/README.md`): podrobnosti jako popover,
  odznak karty jako samostatný řádek (ne ve `WrapBox` hlavičky, rozložení
  `ConversationHeaderLayout` počítá jen pevné části), sbalená karta odznak
  neukazuje (obsah karty se při sbalení skrývá celý; GTK ho ukazuje),
  Zabezpečení jako poslední karta, tři anglické věty selhání
  (`// macOS-only string`: "The key could not be confirmed.", "…changed.",
  "…removed.") — reference pro selhané `pgp.keyVerify`/`keyRemove` věty
  nemá, GTK použilo `RPCErrorText(actionWhat)`. Přidání msgidu by šlo do
  reference a všech tří klientů najednou (otázka pro vlastníka).
- **Nedělá**: karty konverzace v detailu případu Nástěnky (§6) odznak nemají
  (stejně jako GTK; `BoardConversationBlock` nevolá `message.get`).
- **Testy** (Swift Testing, `swift test`): `SignatureViewTests` (port
  `signature_test.go` včetně hostilních vstupů), `APICodingSignatureTests`,
  `SignatureControllerTests` (`SignatureController`, `PGPKeysController`,
  `SignatureState` proti `FakeDaemon`); upravené `APICodingTests`
  (125 metod, 54 kódů, nová notifikace), `APICodingTests+Calendar`,
  `NotificationDecodeTests`.
- **Čeká na Macu**: `swift build --package-path macos` (hlavně strict
  concurrency: `SignatureController.run`/`PGPKeysController.run` vrací
  `@MainActor` closure, vzory `case .x:` nad strukturami, `NSPopover`,
  `NSGridView`), `swift test --filter 'SignatureView|APICodingSignature|SignatureController|PGPKeys|APICoding|NotificationDecode|ChipPlan|Conversation'`
  (pak celé), `python3 macos/scripts/check-strings.py` (dnes 0 missing),
  ruční průchod s démonem s verdikty: ikona v seznamu, odznak v panelu,
  okně zprávy a kartě, popover (Check Revocation, Import Key, Confirm
  Key…), skrytá dlaždice podpisu, Nastavení → Security (import vložením
  i souborem, potvrzení, odebrání), obnova po `notify.signaturesChanged`;
  `MALACHI_START='signature-popover;signature-popover:pgp;prefs-security;quit'`
  s `MALACHI_DAEMON=none` a dočasným `MALACHI_DATA_DIR`.

## As built (Windows)

Krok 6 fáze 1, Windows klient (2026-11-02, necommitnuté; napsané na stroji
bez .NET, takže nic není přeložené ani spuštěné; podrobně a se seznamem
ručních kontrol `docs/windows-port.md` §11.17). Reference
`ui/internal/signature` je portovaná 1:1 přímo z Go (macOS port vznikal
souběžně), **žádný nový msgid** a nic v `windows/parity-exclusions.txt`;
tři anglické věty selhání (`// Windows-only string`) viz níže.

- **Malachi.Core/Api**: `Signature.cs` (zrcadlo §4.21: jedenáct otevřených
  enumů, záznamy s `Pgp`/`Smime` místo Go `PGP`/`SMIME`; `SignatureDetails`
  dědí z `SignatureSummary` jako Go vložení), `MessageSummary.Signature`
  (`MessageSignature`) a `Message.Signature` (`SignatureSummary`, sdílí klíč
  `signature` v `MessageConverter`), `API.cs` (6 metod na konci `Methods`,
  125; `Notify.SignaturesChanged`; 9 limitů, celkem 102),
  `ErrorCode.RevocationFailed` 1510 (54 kódů), `RpcTimeouts.SignatureRevocation`
  20 s, `DaemonNotification.SignaturesChanged` a `NotificationHub.AddSignaturesChanged`.
- **Malachi.Core/Signatures**: `SignatureView` (signature.go), `SignatureText`
  (text.go), `SignatureTypes` (modely; Go `Details` je `SignatureDetailsView`).
  Controllery `SignatureDetailsController` (podrobnosti jedné zprávy: začne
  shrnutím z `message.get`, `notImplemented` ho nechá; Check Revocation s
  busy tlačítkem a třídou selhání; Import Key; Confirm Key; `Refresh`) a
  `PgpKeysController` (Předvolby → Zabezpečení). `MessageRow` má
  `SignatureIcon/-Style/-Tooltip` (u sbaleného vlákna nejnovější člen);
  `AttachmentChips.ChipAttachments` bere verdikt a skryje podpisovou část
  (S15; panel, karty i sponka bubliny); `ConversationController.NeedsBody`
  žádá `message.get` i pro člena s verdiktem; `IconGlyphs`:
  `security-high/-medium/-low` = jeden štít `U+EA18` (ověřit na Windows 11),
  rozdíl dělá barva a u `-medium` průhlednost 60 %.
- **Malachi.App**: `Controls/SignatureGlyph` (štít v barvě verdiktu),
  sloupec v `MessageRowView`, `Signatures/SignatureBadgeButton` (odznak pod
  datem v `MessageView`, v hlavičce otevřené `ConversationCard`; ne v bublině
  chatu ani v okně přiložené zprávy), `SignatureDetailsPresenter` (`Flyout`
  z kódu; otázka Confirm Key… je `ContentDialog` s otiskem monospace, sezení
  přežije zavření flyoutu), `ImportKeyDialog`, `Preferences/SecurityPage`
  (za Kontakty), `Integration` mění `notify.signaturesChanged` v načtení
  zpráv všech povolených účtů jako `notify.messagesChanged`.
- **Odchylky** (řádky v `windows/README.md`): flyout místo dialogu/popoveru,
  jeden glyf štítu, tři anglické věty selhání ("Confirming the key",
  "Removing the confirmation", "Removing the key" přes `RpcErrorText`),
  stránka Zabezpečení za Kontakty, sbalená karta konverzace odznak nemá,
  dokud se neotevře (podepisující přijde s `message.get`).
- **Nedělá**: karty konverzace v detailu případu Nástěnky odznak nemají
  (`board.get` verdikt nenese; stejná mezera jako GTK a macOS).
- **Testy**: `Signatures/SignatureViewTests` (port `signature_test.go`),
  `Controllers/SignatureControllersTests` (proti `FakeDaemon`),
  `Presentation/SignatureMessageRowTests`; upravené `GoContractTests` (125
  metod, 11 enumů, `EveryEnumAndStructOfSignatureGoIsMirrored`, 102 limitů),
  `ApiCodingTests`, `ApiRoundTripTests` (vzorek každého záznamu, `Message`
  s `signature`), `NotificationDecodeTests`.
- **Čeká na Windows agenta**: `build.ps1 app`, `test` (třídy výše, pak
  `IconGlyphsTests`, `Malachi.Conventions.Tests`, celá sada), `lint`; ruční
  průchod podle `docs/windows-port.md` §11.17 (ikona v seznamu, odznak v
  panelu, okně zprávy a kartě, flyout s Check Revocation / Import Key /
  Confirm Key…, skrytá podpisová dlaždice, Předvolby → Zabezpečení, tmavý a
  kontrastní motiv, jiná měřítka). Hádané bez překladače: `Flyout` s
  `FlyoutPresenter` stylem z kódu, `ContentDialog` zůstávající otevřený přes
  `args.Cancel`, glyf `U+EA18`, `VisualStateManager` na `FontIcon`,
  `SettingsCard.Description` jako panel, `record SignatureDetails :
  SignatureSummary` přes source generator.

## As built (daemon)

Kroky 2–4 fáze 1, démon (2026-11-02, necommitnuté). Kontrakt §11.1
**beze změny tvaru** (žádné pole, metoda ani kód navíc); upřesnění jen
v textu `docs/api.md` §4.21 (viz „Změny kontraktu“). UI agenti: metody už
nevracejí `notImplemented`, verdikty přicházejí v souhrnech, v
`message.get` i po backfillu staré pošty.

- **Napojení**: `api.Backend` += `Signatures()`, `PGP()` (komentář
  „not in Backend yet“ pryč), `rpc.StubBackend` (+ falešný backend mostu
  přes vložený stub), `registerSignatures` v `rpc/dispatch.go`, server
  implementuje `api.SignatureNotifier`; `TestAllMethodsRegistered` zelený.
- **`internal/mime`** (`opaque.go`): `walk` = Walk s IMAP číslováním,
  který rozbalí neprůhledný `signed-data` (kořen nebo první část kořenového
  `multipart/mixed`, i bez `smime-type`, uvnitř znovu, nejvýš 3 obaly;
  obsah z `smime.ExtractContent`, nic se neověřuje). **Čísla částí uvnitř
  obalu = číslo obalu + „.“ + číslování vnitřní zprávy** (kořenový obal:
  text `1.1`, části `1.1.1`, `1.2`, …; obal v mixed totéž), takže projdou
  `ValidPartID`, `message.part`, `cid:` a `ExtractPart` (používá týž walk).
  `Parsed.Opaque` (a `Crypto`), obal sám už není příloha; co se nerozbalí,
  zůstane přílohou `smime.p7m`. `FuzzOpaqueUnwrap`, testy v `opaque_test.go`.
- **`internal/smime`**: vlastní BER→DER (uzly, hloubka 32, 32 MiB,
  100 000 prvků; DER projde bajt po bajtu), parser ContentInfo/SignedData
  nad `cryptobyte` (≤ 16 certifikátů, opakovaný podepsaný atribut =
  malformed, nepodepsané atributy se ignorují), ověření (RSA PKCS#1 v1.5
  a PSS s parametry, ECDSA, Ed25519; SHA-2; MD5 → invalid, SHA-1 a RSA
  < 2048 / P-224 → weak, DSA → unsupported), `keyUsage` = EKU
  emailProtection/any **a** KU digitalSignature **nebo nonRepudiation**
  (plán říkal jen digitalSignature), řetěz `x509.Verify` proti předanému
  poolu v čase příchodu, `selfSigned`/`unknownIssuer`/`expired`/`notYetValid`.
  `FuzzSignedData`.
- **`internal/pgpsig`** (go-crypto v1.5.2): ověření odděleného podpisu
  (jen typ binary/text, právě jeden paket, MD5 poznaný z hlavičky paketu
  → invalid, DSA/ElGamal → unsupported, klíč podle KeyID i z otisku v6),
  stav klíče v čase příchodu (odvolání před → revoked, po → revokedAfter;
  expirace → untrusted/expired nebo expiredSince; **klíč vytvořený po
  příchodu → untrusted/notYetValid**), `ReadKeys` (tajný klíč kdekoli =
  `secretKey`, nic se neuloží), `ParseAutocrypt` (právě jedna hlavička,
  neznámý kritický atribut ji zneplatní), `IsInlineSigned`.
  `FuzzPGPSignature`, `FuzzAutocrypt`.
- **`internal/signature`**: `Locate` (S4 nad surovými bajty, vlastní
  dělení multipart podle RFC 2046 — `mime.Skeleton` proudově přestavuje,
  pro přesný výřez se nehodí, sdílí se jen pravidla oddělovačů), `Canonical`
  (LF → CRLF; nesedí-li, zkusí se bajty jak jsou), `ProtectedHeaders`
  (RFC 9788 `protected-headers="v1"`), `Compose` (pořadí §3), `Check`
  (celý verdikt + `SignatureDetails` s vyčištěnými řetězci), `Clean`
  (`board.CleanText` + 256 B), `VerifierVersion` "1". `FuzzSignedSlice`,
  tabulka nad všemi fixturami (`check_test.go`).
- **`internal/revocation`** + `revocationtest`: OCSP (POST), pak CRL;
  pravidla sítě z `internal/oneclick` (`Guard`, `RefusedHost`, proxy jen
  z prostředí), bez přesměrování, 1 MiB, 15 s; **delegovaný respondér
  musí mít EKU OCSPSigning a být vydaný vydavatelem** (`x/crypto/ocsp` to
  sám nekontroluje), odpověď víc než 7 dní po `nextUpdate` se nebere.
- **Migrace `0025_signatures.sql`**: `message_signatures` (FK na
  `messages` ON DELETE CASCADE — řádek jde se zprávou kteroukoli cestou
  mazání, žádný trigger), `smime_revocation`, `pgp_keys` (binární veřejný
  klíč, vyčištěné user ID a adresy). `store/signatures.go`; souhrny
  přidává `attachSignaturesTo` vedle CRM příznaku ve všech výpisech.
- **Core** (`signature_worker.go`, `signature_service.go`): hook `Stored`
  pošty (`afterMailStored`) jen zařadí (`noteSignature`); worker
  (startuje se `StartSync`) ověří po uložení, při změně souhrnu pošle
  `notify.messagesChanged`, vezme Autocrypt (§5), přepočítá `stale`
  řádky; panika = verdikt `error` (S17), do logu jen třída chyby a id
  (S16). Jen účty `imap` a `graph`; u `graph` je nesedící čistý podpis
  `unsupported/rebuilt`. Kořeny: `SignatureRoots` (výchozí
  `x509.SystemCertPool`, otisk = SHA-256 subjektů; na macOS a Windows pool
  subjekty nevypíše, otisk tam jmenuje jen platformu), čtení při startu
  a denně, jiný otisk = S/MIME verdikty přes `trust_key` znovu.
  `core.Maintain`: `backfillOpaque` (meta `mime.opaqueUnwrapped`
  `1:<id>`/`1:done`, `SetMessageBody` znovu → FTS a nástěnka přes
  triggery) a `backfillSignatures` (meta `signature.verified`; kandidáti
  podle typů v `attachments_json` nebo inline značky na začátku textu).
  **S15**: `has_attachments` nepočítá podpisovou část ani část pozvánky
  (obě cesty volají `hasOtherAttachment(…, parts...)`). Nástěnka
  `RulesVersion` "9" (první start přepočítá případy).
- **Identita (S5), zpřísněno**: shoda jen s **jedinou** adresou `From`;
  hlavička se dvěma poli `From` nebo `From` uložený v řádku (obálka
  serveru), který není adresou hlavičky, = žádná shoda → `mismatch`.
  Porovnání NFC a **bez ohledu na velikost písmen celé adresy**.
- **Autocrypt, zpřísněno**: klíč se vezme, jen když všechny jeho user ID
  jmenují `addr` (jinak by odesílatel podstrčil klíč na cizí adresu
  a příští podvržená zpráva by byla `validSeen`). Nabídka `importable`
  (`autocrypt: true`) vzniká, kdykoli zpráva nese Autocrypt pro svůj
  `From` a klíč není uložený (i u hromadné pošty a v nevyžádané).
  `keyChanged`: jiný uložený klíč téže adresy viděný dřív (nebo současně)
  a podpisový klíč není potvrzený.
- **Most MCP**: `list_messages`/`search_messages` mají `signature {kind,
  status}` (víc souhrn nenese; `signerEmail` a `notes` by stály
  `message.get` na řádek), `read_message` ve fenci `signature:`,
  `signature-signer:`, `signature-notes:`; věta v instrukcích. Žádný
  nový modul v mostu (`go list -deps` bez go-crypto), `THIRD-PARTY-NOTICES.md`
  beze změny.
- **Změny kontraktu (jen text `docs/api.md`)**: porovnání identity
  case-insensitive a jen s jedinou `From`; most vrací u výpisů jen
  `kind` a `status`; changelog už neříká „notImplemented“.
- **Testdata**: `backend/testdata/signatures/make.go` (`go run
  ./testdata/signatures/make.go` z `backend/`; testovací CA s
  mezilehlou, klíče PEM, klíče OpenPGP `.asc`, jen pro testy) a
  39 fixtur `testdata/mime/smime-*.eml`, `pgp-*.eml` (všechny případy §7
  kromě „nekonečného vnoření“ jako skutečně nekonečného — je to 5000×
  `30 80`; nic nezávisí na dnešním datu: platnost certifikátů je
  pevná, čas příchodu dává test).
- **Nedělá**: chráněné hlavičky jen porovná (S6), nikdy je neukáže místo
  vnějších; přiložená zpráva verdikt nemá; skutečné vzorky T0 (Outlook,
  Graph `$value`, Apple Mail, Thunderbird, Proton) neověřené; `board.get`
  verdikt nenese (karty nástěnky ve všech UI bez odznaku, viz výše).

## Doplněk 2026-11-03: věty selhání u klíčů OpenPGP (rozhodnutí vlastníka)

Vlastník rozhodl přidat vlastní msgid pro selhání `pgp.keyVerify`
a `pgp.keyRemove` do všech tří klientů naráz; tím neplatí poznámky výše
o `widget.RPCErrorText` s popiskem tlačítka (GTK), třech anglických
větách `// macOS-only string` (macOS) a „Confirming the key“ přes
`RpcErrorText` (Windows). Reference `KeyVerifyFailedText(verified)`
(„The key could not be confirmed.“ / „The confirmation could not be
removed.“) a `KeyRemoveFailedText` („The key could not be removed.“),
česky „Klíč se nepodařilo potvrdit.“, „Potvrzení se nepodařilo zrušit.“,
„Klíč se nepodařilo odebrat.“; GTK `keyActionFailedText` v
`window/signature_details.go`, macOS `SignatureView.keyVerifyFailedText`
/ `keyRemoveFailedText`, Windows `SignatureText.KeyVerifyFailedText` /
`KeyRemoveFailedText`. Řádky odchylek v `macos/README.md`
a `windows/README.md` jsou pryč. Ověřeno jen `go test
./internal/signature` a `check-strings.py`; GTK, Swift a C# čekají na
sestavení jako zbytek fáze 1.

## Board card badge (macOS)

Karty konverzace v detailu případu nástěnky mají odznak podpisu jako karty
Pošty (napsáno bez překladu, čeká `swift build` a `swift test`). Core:
`BoardMessage.signature` (`SignatureSummary`) → `Board.CaseMessage.signature`
→ `Board.MessageCard.signature`; `ConversationCards.Key` nese
`signatures` vedle členů (verdikt není součástí identity karty, tělo a
sbalení zůstávají) a změna verdiktu vrátí `.members(kept: identita,
reload: [])`, takže blok karty jen aktualizuje, nic nestahuje znovu.
`DaemonBoardSource.signaturesChanged()` (z `notify.signaturesChanged`,
`MainWindowController.wireBoardSource`) zeptá se `board.get` znovu na
držené konverzace a vymění je na místě. AppKit: `BoardMessageCardView`
vyrábí `SignatureBadgeView` líně pod záhlavím, otevřená karta ho ukazuje,
sbalená ne (jako Pošta), v chat bublině není; klik jde přes nové
`MessageActionDelegate.showSignature(of:summary:relativeTo:)` s id členu
a verdiktem karty (`showSignature(of:relativeTo:)` ho volá s verdiktem
z cache). Vzorová data: nejnovější zpráva prvního případu je podepsaná
(`MALACHI_BOARD_SAMPLES`; klik v nich jen řekne, že náhled to neumí). Bez
nového msgid. Testy: `APICodingBoardTests` (`getResultCarriesTheSignature…`),
`BoardConversationCardsTests` (klíč, změna verdiktu, nový člen s verdiktem).
Neřešeno: verdikt, který se objeví až po `board.get`, přinese další verze
případu (jako GTK `ReloadConversations` po `notify.messagesChanged` účtu).

## Board card badge (Windows)

Karty konverzace v detailu případu nástěnky mají odznak podpisu jako karty
Pošty (napsáno bez .NET, čeká `build.ps1 app`/`test`/`lint`). Core:
`BoardMessage.Signature` → `Board.CaseMessage.Signature` →
`Board.MessageCard.Signature`; oba záznamy mají vlastní `Equals`, které
porovnává verdikt hodnotou (`Board.SameSignature`: `Notes` záznamu
`SignatureSummary` se jinak porovnávají odkazem a každý `board.get` je staví
znovu). `ConversationCards.Key` nese `Verdicts` (řetězec `VerdictOf` na člena)
vedle členů; verdikt není součástí identity karty, takže změna verdiktu vrátí
`Change.Members` s identitou a prázdným `Reload` (sbalení a tělo zůstanou,
nic se nestahuje znovu) a blok jen zavolá `BoardMessageCardView.Update`.
`DaemonBoardSource.SignaturesChanged()` (z `notify.signaturesChanged` v
`Integration.Board.cs`, protože `board.get` se drží podle verze případu a
důvěra ji nemění) označí držené konverzace za zastaralé: zobrazenou zeptá
hned a ponechá starou do odpovědi, ostatní zapomene a načtou se při zobrazení.
App: `BoardMessageCardView` drží `SignatureBadgeButton` pod záhlavím otevřené
karty (`BoardConversationBlock.AttachBadge` mu dá `ReaderServices` a okno
detailu), sbalená karta, člen bez aplikace a chat bublina žádný; klik otevře
tentýž `SignatureDetailsPresenter` s id člena. Vzorová data: nejnovější
zpráva prvního případu je podepsaná (S/MIME, platný), je bez id, takže odznak
se ukáže, ale klik detaily nepožádá (`SignatureBadgeButton.Open` bez id nic
nedělá). Bez nového msgid. Testy: `ApiRoundTripTests` (`BoardMessage`),
`BoardConversationCardsTests` (`ASignatureChangeCountsAndKeepsTheCards`,
`CardsAndDetailsCompareTheirVerdictsByValue`), `DaemonBoardSourceTests`
(`SignaturesChangedAsksTheShownConversationAgain`), `BoardSampleTests`
(`ASampleMemberIsSigned`). Bez překladu stavěné a hádané: `badge.Margin`
(`PadH - 8`, tlačítko má vlastní odsazení 8), pořadí `column.Children`
(záhlaví, odznak, tělo) a výška karty při objevení odznaku (jde přes
`ChangingHeight` v `Update`/`Apply`). Čeká i UI smoke nad
`MALACHI_BOARD_SAMPLES=1` (odznak na nejnovější kartě prvního případu).

## Board card badge (GTK)

Karty konverzace v detailu případu nástěnky mají odznak podpisu jako karty
Pošty (napsáno bez GTK, necommitnuté). Reference Go: `BoardMessage.Signature`
→ `board.CaseMessage.Signature` (`convertMessage`) → `board.MessageCard.Signature`
(`view.go`); `board/signature.go`: `SignatureKey`/`SignatureKeyOf` (verdikt
v porovnatelném tvaru) a `SameCard` (karta s verdiktem porovnaným hodnotou;
dřívější `==` by kvůli ukazateli viděl změnu po každém `board.get`).
`ConversationKey.Signatures` vedle členů: verdikt není součástí identity
karty (`ConversationMember` beze změny), změna samotného verdiktu vrátí
`ConversationChangeMembers` s identitou v `Kept` a prázdným `Reload`
(sbalení, tělo i rozběhnutý dotaz na tělo zůstanou), chybějící položka =
bez verdiktu. `DaemonSource.ReloadConversations(account)` označí držené
konverzace účtu („“ = všech) za zastaralé (`staleConversation`) a tu, na
kterou se `LoadMessages` ptal naposled (`lastOpened`, detail), zeptá hned;
staré zprávy zůstanou do odpovědi. Volá ho `Window.boardVerdictsChanged`
(`board.go`) z `notify.signaturesChanged` (`signature.go`, „“) i
z `notify.messagesChanged` účtu (`notify.go`): verdikt, který worker
spočítá až po `board.get`, verzi případu nemění (macOS a Windows to druhé
nemají). GTK: `window/board_conversation_signature.go` (`bindSignature`,
`showSignature`, `openSignature`) — tentýž `signatureBadge` na vlastním
řádku pod záhlavím karty, viditelný i u sbalené karty (jako karta Pošty
v GTK; macOS a Windows ho u sbalené nemají), v bublině chatu ne;
`update` porovnává `board.SameCard`, verdikt se kreslí uvnitř
`changingHeight`; chat porovnává totéž. Klik: nové
`Window.openSignatureDetails(parent, id, from, sum)` (z něj je teď
i `showSignatureDetails`), `from` = `MessageCard.Address`; karta vzorku
(id „“) ukáže jen verdikt — `load`, `start` a `askConfirm` dialogu se bez
id nic neptají. Vzorová data: nejnovější zpráva prvního případu (Ada
Example, „Attached is the final version…“) má S/MIME `valid`, podepisující
„Ada Example“ <ada@example.invalid>, bez poznámek a `partId` (jako macOS;
Windows má e-mail `ada@example.org`). Bez nového msgid; posunuté jen
odkazy `#:` `board/view.go` a `window/board_conversation.go` v `po/`.
Testy: `board/signature_test.go` (klíč, `SameCard`, klíč detailu, změna
verdiktu drží karty a těla, `convertMessage`, podepsaný vzorek),
`TestDaemonReloadConversations`; smoke `smokeBoardSignatureBadge`
v `TestBoardGTKSmoke` (`window/board_signature_gtk_smoke_test.go`: odznak
vzorku, sbalená karta, rovný verdikt nic nemění, změněný a odebraný
verdikt na téže kartě, dialog s id i bez). Ověřeno zde: `gofmt`,
`go vet` a `go test` (i `-race`) `internal/board`, `boardtriage`,
`boardreply`, `signature`, `conversation`; typová kontrola `go/types`
s `FakeImportC` balíčku `window` i s testy (jen dvě známé chyby C
konstant). Čeká v kontejneru: `gtk.sh make build`, `gtk.sh test-ui`,
`gtk.sh sh -c 'cd ui && MALACHI_GTK_SMOKE=1 go test ./internal/window
-run "^TestBoardGTKSmoke$" -count=1 -v'` (i s `MALACHI_GTK_CARDS_SMOKE=1`),
`make po` (beze změny), `make lint`; ručně odznak na nástěnce nad vzorky
(`MALACHI_BOARD_SAMPLES=1`) a nad kopií dat s podepsanou poštou (klik,
Check Revocation, Import Key, Confirm Key…, obnova po importu klíče).
