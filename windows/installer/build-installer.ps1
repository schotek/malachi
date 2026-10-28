# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Makes the release artefacts of one architecture from an app folder that
# build.ps1 app assembled: Malachi-Mail-<version>-<arch>-Setup.exe (Inno
# Setup, MalachiMail.iss) and Malachi-Mail-<version>-<arch>.zip, the same
# folder to unpack anywhere. build.ps1 installer runs it after app; a
# release built from an older tag runs it directly with that tag's app
# folder:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File windows\installer\build-installer.ps1 `
#       -AppDir <tag>\build\windows\arm64\Malachi Mail -Version 0.1.2 -Arch arm64 `
#       -AssetsFile <tag>\build\windows\artifacts\obj\Malachi.App\project.assets.json `
#       -Licensing LICENSING.md -OutDir build\windows\release
#
# The app folder is copied into <OutDir>\stage-<arch>\Malachi Mail\ and
# completed there, never changed in place: -Licensing replaces its
# LICENSING.md (a tag older than the GPLv3 section 7 permission), and the
# notices of the NuGet packages (copy-notices.ps1) go into
# Microsoft-notices\ when the folder has none. Nothing is signed (no
# certificate yet; docs/windows-port.md section 17).
#
# Keep the file ASCII below the header (see build.ps1).
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string] $AppDir,
    [Parameter(Mandatory = $true)] [string] $Version,
    [Parameter(Mandatory = $true)] [ValidateSet('x64', 'arm64')] [string] $Arch,
    [Parameter(Mandatory = $true)] [string] $OutDir,
    [string[]] $AssetsFile = @(),
    [string] $Licensing = '',
    [string] $Iscc = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Here = $PSScriptRoot
$Utf8 = New-Object System.Text.UTF8Encoding $false
# powershell -File passes a list as one string with commas.
$AssetsFile = @($AssetsFile | ForEach-Object { $_ -split ',' } | Where-Object { $_ })

function Get-Iscc {
    if ($Iscc) {
        return $Iscc
    }
    if ($env:ISCC) {
        return $env:ISCC
    }
    $command = Get-Command 'ISCC.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) {
        return $command.Path
    }
    # winget installs Inno Setup per user or per machine.
    foreach ($candidate in @(
            (Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 6\ISCC.exe'),
            (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
            (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe'))) {
        if (Test-Path -LiteralPath $candidate) {
            return $candidate
        }
    }
    throw 'ISCC.exe was not found: install Inno Setup 6 (winget install JRSoftware.InnoSetup) or set ISCC'
}

# The file version: Major.Minor.Patch and the commits since the tag, as
# build.ps1 computes it.
function Get-NumericVersion {
    $release = [regex]::Match($Version, '^(\d+)(?:\.(\d+))?(?:\.(\d+))?(?=$|[-+])')
    if (-not $release.Success) {
        throw "$Version is not a release version"
    }
    $parts = @()
    foreach ($group in 1, 2, 3) {
        $value = 0
        if ($release.Groups[$group].Success) {
            $value = [Math]::Min([long]$release.Groups[$group].Value, 65535)
        }
        $parts += $value
    }
    $commits = 0
    $described = [regex]::Match($Version, '-(\d+)-g[0-9a-f]+(?:-dirty)?$')
    if ($described.Success) {
        $commits = [Math]::Min([long]$described.Groups[1].Value, 65535)
    }
    return '{0}.{1}.{2}.{3}' -f $parts[0], $parts[1], $parts[2], $commits
}

function Invoke-Iscc {
    param([string[]] $Arguments)
    $exe = Get-Iscc
    Write-Host ('> ' + $exe + ' ' + ($Arguments -join ' '))
    $ErrorActionPreference = 'Continue'
    & $exe @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "ISCC.exe exited with status $LASTEXITCODE"
    }
}

$AppDir = [System.IO.Path]::GetFullPath($AppDir).TrimEnd('\')
$OutDir = [System.IO.Path]::GetFullPath($OutDir).TrimEnd('\')
if (-not (Test-Path -LiteralPath (Join-Path $AppDir 'MalachiMail.exe'))) {
    throw "$AppDir is no app folder (build.ps1 app)"
}
if ($Version -match '-dirty$') {
    throw "${Version}: the tree was not clean; a release is built from a clean tag"
}
$numeric = Get-NumericVersion

$stageRoot = Join-Path $OutDir "stage-$Arch"
$stage = Join-Path $stageRoot 'Malachi Mail'
if (Test-Path -LiteralPath $stageRoot) {
    Remove-Item -LiteralPath $stageRoot -Recurse -Force
}
[System.IO.Directory]::CreateDirectory($stage) | Out-Null
Copy-Item -Path (Join-Path $AppDir '*') -Destination $stage -Recurse

if ($Licensing) {
    Copy-Item -LiteralPath $Licensing -Destination (Join-Path $stage 'LICENSING.md') -Force
}
$notices = Join-Path $stage 'Microsoft-notices'
if (-not (Test-Path -LiteralPath $notices)) {
    if ($AssetsFile.Count -eq 0) {
        throw "$AppDir has no Microsoft-notices folder; pass -AssetsFile (project.assets.json of the build)"
    }
    & (Join-Path $Here '..\scripts\copy-notices.ps1') -AssetsFile $AssetsFile -Destination $notices -Arch $Arch
}
foreach ($required in 'LICENSE.txt', 'LICENSE-backend.txt', 'LICENSING.md', 'THIRD-PARTY-NOTICES.md',
    'Microsoft-notices\Microsoft.WindowsAppSDK\license.txt', 'Microsoft-notices\Microsoft.Web.WebView2\LICENSE.txt') {
    if (-not (Test-Path -LiteralPath (Join-Path $stage $required))) {
        throw "the staged folder lacks $required"
    }
}
$licensingText = [System.IO.File]::ReadAllText((Join-Path $stage 'LICENSING.md'), $Utf8)
if ($licensingText -notmatch 'Additional permission under GPLv3') {
    throw 'LICENSING.md lacks the GPLv3 section 7 permission for the Microsoft components; pass -Licensing'
}

[System.IO.Directory]::CreateDirectory($OutDir) | Out-Null
Invoke-Iscc @('/Q', "/DAppVersion=$Version", "/DNumericVersion=$numeric", "/DArch=$Arch",
    "/DSourceDir=$stage", "/DOutputDir=$OutDir", (Join-Path $Here 'MalachiMail.iss'))
$setup = Join-Path $OutDir ('Malachi-Mail-{0}-{1}-Setup.exe' -f $Version, $Arch)

# The portable zip: one Malachi Mail\ folder, entry by entry with / as the
# separator (build.ps1 package says why).
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = Join-Path $OutDir ('Malachi-Mail-{0}-{1}.zip' -f $Version, $Arch)
if (Test-Path -LiteralPath $zip) {
    Remove-Item -LiteralPath $zip -Force
}
$archive = [System.IO.Compression.ZipFile]::Open($zip, [System.IO.Compression.ZipArchiveMode]::Create)
try {
    foreach ($file in [System.IO.Directory]::GetFiles($stage, '*', [System.IO.SearchOption]::AllDirectories)) {
        $relative = $file.Substring($stage.Length).TrimStart('\').Replace('\', '/')
        [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $file, "Malachi Mail/$relative",
            [System.IO.Compression.CompressionLevel]::Optimal) | Out-Null
    }
} finally {
    $archive.Dispose()
}

foreach ($artefact in $setup, $zip) {
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $artefact).Hash.ToLowerInvariant()
    Write-Host ('{0}  {1}  ({2:N0} bytes)' -f $hash, [System.IO.Path]::GetFileName($artefact), (Get-Item -LiteralPath $artefact).Length)
}
