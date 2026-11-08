# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Packs an app folder that build.ps1 app assembled into an MSIX package:
# Malachi-Mail-<version>-windows-<arch>.msix, unsigned, for the Microsoft
# Store (Partner Center; a release tag builds it), or with -Sign
# Malachi-Mail-<version>-windows-<arch>-test.msix, a test package
# (docs/windows-port.md section 17, docs/releasing.md section 7). Run by
# .github/workflows/windows-msix.yml or on a desktop:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 app -Arch x64
#   powershell -NoProfile -ExecutionPolicy Bypass -File windows\msix\build-msix.ps1 `
#       -AppDir build\windows\x64\Malachi Mail -Version 0.2.0-5-gabc1234 -Arch x64 `
#       -OutDir build\windows\msix -Sign
#
# The app folder is copied into <OutDir>\stage-<arch>\, never changed in
# place; the package's logos are rendered there from data\icons\malachi_icon.png
# (cut as make-icons.ps1 cuts it) and AppxManifest.xml filled in. makeappx
# packs the stage.
#
# -Sign signs the package with a self-signed certificate made for this run
# only (subject = -Publisher, which must match the manifest), exports its
# public half beside the package (.cer) and deletes the key: a tester trusts
# the .cer (Local Machine, Trusted People) and installs the .msix. Each run
# has a new certificate. Without -Sign the package is unsigned, which only
# the Store takes (it re-signs what it publishes; the Identity then has to
# be the one Partner Center reserved).
#
# The package version is Major.Minor.Patch.Commits, so every commit is an
# upgrade of the one before; the Store wants the last part 0, which a
# release tag gives.
#
# Keep the file ASCII below the header (see build.ps1).
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string] $AppDir,
    [Parameter(Mandatory = $true)] [string] $Version,
    [Parameter(Mandatory = $true)] [ValidateSet('x64', 'arm64')] [string] $Arch,
    [Parameter(Mandatory = $true)] [string] $OutDir,
    [string] $IdentityName = 'io.github.schotek.Malachi',
    [string] $Publisher = 'CN=Malachi Mail Test',
    [string] $PublisherDisplayName = 'Malachi Mail (test)',
    [switch] $Sign
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.Drawing

$Here = $PSScriptRoot
$Root = [System.IO.Path]::GetFullPath((Join-Path $Here '..\..'))
$Utf8 = New-Object System.Text.UTF8Encoding $false

# The newest Windows SDK tool for this machine (makeappx, signtool).
function Get-SdkTool([string] $Name) {
    $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    $hostDir = 'x64'
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
        $hostDir = 'arm64'
    }
    $found = @(Get-ChildItem -Path (Join-Path $kits "10.*\$hostDir\$Name") -ErrorAction SilentlyContinue |
            Sort-Object { [version]$_.Directory.Parent.Name } -Descending)
    if ($found.Count -eq 0) {
        throw "$Name was not found under $kits; install the Windows SDK"
    }
    return $found[0].FullName
}

# Shown is what the log shows instead of the arguments (no password there).
function Invoke-Tool([string] $Exe, [string[]] $Arguments, [string] $Shown = '') {
    if (-not $Shown) {
        $Shown = $Arguments -join ' '
    }
    Write-Host ('> ' + $Exe + ' ' + $Shown)
    $ErrorActionPreference = 'Continue'
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$([System.IO.Path]::GetFileName($Exe)) exited with status $LASTEXITCODE"
    }
}

function Get-PackageVersion {
    $release = [regex]::Match($Version, '^(\d+)(?:\.(\d+))?(?:\.(\d+))?(?=$|[-+])')
    $parts = @(0, 0, 0)
    if ($release.Success) {
        foreach ($group in 1, 2, 3) {
            if ($release.Groups[$group].Success) {
                $parts[$group - 1] = [Math]::Min([long]$release.Groups[$group].Value, 65535)
            }
        }
    }
    $commits = 0
    $described = [regex]::Match($Version, '-(\d+)-g[0-9a-f]+(?:-dirty)?$')
    if ($described.Success) {
        $commits = [Math]::Min([long]$described.Groups[1].Value, 65535)
    }
    return '{0}.{1}.{2}.{3}' -f $parts[0], $parts[1], $parts[2], $commits
}

# The rounded square of data\icons\malachi_icon.png at Size x Size pixels, as
# make-icons.ps1 cuts it (1126 px from x 65, y 64).
function Save-Logo([System.Drawing.Image] $Source, [int] $Size, [string] $Path) {
    $frame = New-Object System.Drawing.Bitmap $Size, $Size, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($frame)
    $attributes = New-Object System.Drawing.Imaging.ImageAttributes
    try {
        $g.CompositingMode = [System.Drawing.Drawing2D.CompositingMode]::SourceCopy
        $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
        $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $attributes.SetWrapMode([System.Drawing.Drawing2D.WrapMode]::TileFlipXY)
        $dest = New-Object System.Drawing.Rectangle 0, 0, $Size, $Size
        $g.DrawImage($Source, $dest, 65, 64, 1126, 1126, [System.Drawing.GraphicsUnit]::Pixel, $attributes)
        $frame.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $attributes.Dispose()
        $g.Dispose()
        $frame.Dispose()
    }
}

function New-TestCertificate([string] $PfxPath, [string] $CerPath, [string] $Password) {
    $cert = New-SelfSignedCertificate -Type Custom -Subject $Publisher -KeyUsage DigitalSignature `
        -FriendlyName 'Malachi Mail MSIX test' -CertStoreLocation 'Cert:\CurrentUser\My' `
        -NotAfter (Get-Date).AddDays(30) `
        -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3', '2.5.29.19={text}')
    try {
        $secure = ConvertTo-SecureString -String $Password -Force -AsPlainText
        Export-PfxCertificate -Cert $cert -FilePath $PfxPath -Password $secure | Out-Null
        Export-Certificate -Cert $cert -FilePath $CerPath | Out-Null
    } finally {
        Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $cert.Thumbprint) -Force
    }
}

$AppDir = [System.IO.Path]::GetFullPath($AppDir).TrimEnd('\')
$OutDir = [System.IO.Path]::GetFullPath($OutDir).TrimEnd('\')
foreach ($required in 'MalachiMail.exe', 'malachid.exe', 'malachi-mcp.exe', 'malachi-credentials.exe') {
    if (-not (Test-Path -LiteralPath (Join-Path $AppDir $required))) {
        throw "$AppDir lacks $required (build.ps1 app)"
    }
}
if ($Publisher -notmatch '^CN=') {
    throw "-Publisher is a distinguished name (CN=...), not $Publisher"
}

$packageVersion = Get-PackageVersion

$stage = Join-Path $OutDir "stage-$Arch"
if (Test-Path -LiteralPath $stage) {
    Remove-Item -LiteralPath $stage -Recurse -Force
}
[System.IO.Directory]::CreateDirectory($stage) | Out-Null
Copy-Item -Path (Join-Path $AppDir '*') -Destination $stage -Recurse

$logos = Join-Path $stage 'Assets\Package'
[System.IO.Directory]::CreateDirectory($logos) | Out-Null
$icon = [System.Drawing.Image]::FromFile((Join-Path $Root 'data\icons\malachi_icon.png'))
try {
    Save-Logo $icon 50 (Join-Path $logos 'StoreLogo.png')
    Save-Logo $icon 44 (Join-Path $logos 'Square44x44Logo.png')
    Save-Logo $icon 150 (Join-Path $logos 'Square150x150Logo.png')
} finally {
    $icon.Dispose()
}

# XML-escaped, as the values go into attributes and elements.
function ConvertTo-XmlText([string] $Text) {
    return [System.Security.SecurityElement]::Escape($Text)
}
$manifest = [System.IO.File]::ReadAllText((Join-Path $Here 'AppxManifest.xml'), $Utf8)
$manifest = $manifest.Replace('{{IdentityName}}', (ConvertTo-XmlText $IdentityName))
$manifest = $manifest.Replace('{{Publisher}}', (ConvertTo-XmlText $Publisher))
$manifest = $manifest.Replace('{{PublisherDisplayName}}', (ConvertTo-XmlText $PublisherDisplayName))
$manifest = $manifest.Replace('{{Version}}', $packageVersion)
$manifest = $manifest.Replace('{{Arch}}', $Arch)
if ($manifest -match '\{\{') {
    throw 'AppxManifest.xml has a token build-msix.ps1 does not fill in'
}
[System.IO.File]::WriteAllText((Join-Path $stage 'AppxManifest.xml'), $manifest, $Utf8)

$name = 'Malachi-Mail-{0}-windows-{1}' -f $Version, $Arch
if ($Sign) {
    $name += '-test'
}
$msix = Join-Path $OutDir "$name.msix"
Invoke-Tool (Get-SdkTool 'makeappx.exe') @('pack', '/d', $stage, '/p', $msix, '/o')

if ($Sign) {
    $pfx = Join-Path $OutDir "$name.pfx"
    $cer = Join-Path $OutDir "$name.cer"
    $password = [Guid]::NewGuid().ToString('N')
    New-TestCertificate $pfx $cer $password
    try {
        Invoke-Tool (Get-SdkTool 'signtool.exe') @('sign', '/fd', 'SHA256', '/f', $pfx, '/p', $password, $msix) `
            "sign /fd SHA256 /f $pfx /p *** $msix"
    } finally {
        Remove-Item -LiteralPath $pfx -Force
    }
    Write-Host "signed with a test certificate for $Publisher; trust $cer to install"
} else {
    Write-Host 'unsigned: only the Store takes this package'
}

$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $msix).Hash.ToLowerInvariant()
Write-Host ('{0}  {1}  ({2:N0} bytes, package version {3})' -f $hash, [System.IO.Path]::GetFileName($msix), (Get-Item -LiteralPath $msix).Length, $packageVersion)
