<p align="center">
  <img src="data/icons/malachi_icon.png" width="160" alt="Ikona Malachi Mail: okřídlený posel s obálkou">
</p>

<h1 align="center">Malachi Mail</h1>

<p align="center">
  Rychlá nativní pošta pro Linux, macOS a Windows.<br>
  Vaše pošta zůstává ve vašem počítači, bez účtu u nás a bez našich serverů mezi vámi a poštou.
</p>

<p align="center">
  <a href="https://malachimail.app/cs/download.html"><b>Stáhnout</b></a> ·
  <a href="https://malachimail.app/cs/features.html">Funkce</a> ·
  <a href="https://malachimail.app/cs/news.html">Co je nového</a> ·
  <a href="https://malachimail.app/cs/#report">Nahlásit problém</a> ·
  <a href="README.md">English</a>
</p>

<p align="center">
  <a href="https://malachimail.app/assets/screenshots/macos/cs/inbox.webp"><img src="https://malachimail.app/assets/screenshots/macos/cs/inbox.webp" width="820" alt="Malachi Mail na macOS: postranní panel se všemi účty, seznam zpráv seskupený podle konverzací a samotná zpráva"></a>
</p>

## Co umí

- **Všechny vaše účty.** IMAP/SMTP s průvodcem, který server najde sám,
  Microsoft 365 a Outlook.com s přihlášením v prohlížeči, Gmail a na
  Linuxu účty GNOME Online Accounts.
- **Pošta i bez připojení.** Složky jsou uložené ve vašem počítači,
  hledání funguje offline a změny se na server pošlou, jakmile je
  dostupný.
- **Bezpečné HTML.** Zprávy se před zobrazením vyčistí, bez skriptů
  a bez vzdálených obrázků, dokud je nepovolíte.
- **Konverzace jako chat**, řádek rychlé odpovědi, Všechny doručené,
  pravidla, odhlášení z newsletteru jedním klikem a ověřované podpisy
  S/MIME a OpenPGP.
- **Pro práci:** kalendář (Microsoft 365 a CalDAV) s pozvánkami, issues
  z Jiry čtené jako pošta, ukládání pošty do eWay-CRM a nástěnka, která
  třídí, co čeká na vás a co na ostatní.
- **Asistent na vašem vlastním účtu** (Claude nebo ChatGPT): shrne,
  najde úkoly a připraví odpověď, a to jen když o to požádáte.
- Česky a anglicky.

Celou prohlídku najdete na
[malachimail.app](https://malachimail.app/cs/features.html).

## Stažení

Hotové balíčky jsou na
[malachimail.app](https://malachimail.app/cs/download.html) a na
[stránce vydání](https://github.com/schotek/malachi/releases):

| Systém | Ke stažení |
|---|---|
| macOS 14 a novější, Apple silicon i Intel | obraz disku, podepsaný a notarizovaný Applem |
| Windows 11, x64 a ARM64 | instalátor, nepotřebuje práva správce |
| Linux, x64 a arm64 | Flatpak, nebo `.deb` / `.rpm` pro distribuci s libadwaita 1.7 a novější |

Stránka ke stažení poradí, který balíček pro Linux zvolit, co dělat
s varováním SmartScreen ve Windows, a uvádí kontrolní součty všech
souborů.

## Nahlášení problému

Použijte formulář na [malachimail.app](https://malachimail.app/cs/#report)
(aplikace ho otevře přes *Nahlásit problém*), nebo tady založte
[issue](https://github.com/schotek/malachi/issues). Bezpečnostní problém
prosím hlaste formulářem, ne veřejným issue.

## O tomto repozitáři

Každý commit je zdrojový kód jednoho vydání, přesně ty soubory, které
jsou potřeba k jeho sestavení, a balíčky na stránce vydání z něj staví
workflowy v `.github/workflows`. Samotný vývoj probíhá jinde, proto se
sem pull requesty nepřijímají.

### Sestavení ze zdrojů

Zdroje vydání se sestavují na systému, pro který jsou určené:

- **Linux**: Go 1.25, překladač C, GTK 4, libadwaita 1.7 a novější,
  WebKitGTK 6.0, Blueprint a gsound (nepovinně). Pak `make build`;
  aplikace je `build/malachi` a svého démona `build/malachid` si spustí
  sama. `make deb`, `make rpm` a `make flatpak` staví balíčky. První
  sestavení překládá vazby na GTK a trvá desítky minut.
- **macOS**: macOS 14, Xcode se Swiftem 6 a Go. Pak `make macos`;
  aplikace je `build/Malachi Mail.app`, podepsaná ad hoc pro Mac, na
  kterém vznikla.
- **Windows**: Windows 11, .NET 10 SDK, nástroje MSVC (Visual Studio
  nebo jeho Build Tools), Go a Git for Windows. Pak
  `windows\build.ps1 app` z krátké cesty (překladač XAML nezvládá dlouhé
  cesty); aplikace je v `build\windows\<arch>\Malachi Mail\`.

## Licence

Jádro (`backend/`: démon `malachid` a jeho API) je pod licencí
[AGPL-3.0-only](backend/LICENSE), aplikace pro Linux, macOS a Windows
a vše ostatní pod [GPL-3.0-or-later](LICENSE). Podrobnosti včetně
výjimky pro komponenty Microsoftu, které nese aplikace pro Windows, jsou
v [LICENSING.md](LICENSING.md), oznámení třetích stran
v [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

---

*Malachi* je prorok Malachiáš (hebrejsky *malʼākî*, „můj posel“).
