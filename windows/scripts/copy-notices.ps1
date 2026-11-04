# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copies the licence and notice files of the NuGet packages that travel in
# the app folder (the Windows App SDK, WebView2, the .NET runtime packs and
# the other packages the app links) into <Destination>\<package id>\, the
# notices LICENSING.md promises beside the app ("Microsoft components in the
# Windows client"). It reads the packages from the project.assets.json files
# of the restore, so what it copies is exactly what the build used; packages
# that only take part in the build (code generators, metadata, the SDK
# build tools, reference packs) are left out. A package without a licence
# file in its folder (a licence expression only) is listed in
# <Destination>\README.txt with its id and version.
#
# Keep the file ASCII below the header (see build.ps1).
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string[]] $AssetsFile,
    [Parameter(Mandatory = $true)] [string] $Destination,
    # The app folder's architecture: the runtime packs of the other one,
    # restored as well, are left out.
    [Parameter(Mandatory = $true)] [ValidateSet('x64', 'arm64')] [string] $Arch
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Utf8 = New-Object System.Text.UTF8Encoding $false
# powershell -File passes a list as one string with commas.
$AssetsFile = @($AssetsFile | ForEach-Object { $_ -split ',' } | Where-Object { $_ })

# Build-time only: nothing of them reaches the app folder.
$BuildOnly = @(
    '^Microsoft\.Windows\.CsWin32$',
    '^Microsoft\.Windows\.SDK\.BuildTools',
    '^Microsoft\.Windows\.SDK\.Win32Docs$',
    '^Microsoft\.Windows\.SDK\.Win32Metadata$',
    '^Microsoft\.Windows\.WDK\.Win32Metadata$',
    '^Microsoft\.Windows\.SDK\.NET\.Ref$',
    '^Microsoft\.AspNetCore\.App\.',
    '^Microsoft\.NET\.ILLink\.Tasks$'
)
$OtherArch = 'arm64'
if ($Arch -eq 'arm64') {
    $OtherArch = 'x64'
}
$BuildOnly += ('(^|\.)win-' + $OtherArch + '(\.|$)')

function Test-BuildOnly {
    param([string] $Id)
    foreach ($pattern in $BuildOnly) {
        if ($Id -match $pattern) {
            return $true
        }
    }
    return $false
}

# id/version -> folder, from every assets file.
$packages = @{}
$roots = @()
foreach ($file in $AssetsFile) {
    $assets = [System.IO.File]::ReadAllText($file, $Utf8) | ConvertFrom-Json
    foreach ($folder in $assets.packageFolders.PSObject.Properties.Name) {
        if ($roots -notcontains $folder) {
            $roots += $folder
        }
    }
    foreach ($library in $assets.libraries.PSObject.Properties) {
        if ($library.Value.type -eq 'package') {
            $packages[$library.Name] = $library.Value.path
        }
    }
    # The runtime packs (the .NET runtime and the Windows App SDK runtime
    # of a self-contained app, the ILCompiler of NativeAOT) are download
    # dependencies, not libraries.
    foreach ($framework in $assets.project.frameworks.PSObject.Properties) {
        if (-not ($framework.Value.PSObject.Properties.Name -contains 'downloadDependencies')) {
            continue
        }
        foreach ($dependency in $framework.Value.downloadDependencies) {
            $version = ([regex]::Match($dependency.version, '^\[([^,\]]+)')).Groups[1].Value
            $packages["$($dependency.name)/$version"] = "$($dependency.name)/$version".ToLowerInvariant()
        }
    }
}

[System.IO.Directory]::CreateDirectory($Destination) | Out-Null
$withoutFile = @()
foreach ($key in ($packages.Keys | Sort-Object)) {
    $id = $key.Split('/')[0]
    if (Test-BuildOnly $id) {
        continue
    }
    $folder = $null
    foreach ($root in $roots) {
        $candidate = Join-Path $root $packages[$key]
        if (Test-Path -LiteralPath $candidate) {
            $folder = $candidate
            break
        }
    }
    if (-not $folder) {
        # A runtime pack of the other architecture, never restored here.
        continue
    }
    $notices = @(Get-ChildItem -LiteralPath $folder -File | Where-Object { $_.Name -match '^(license|notice|third-party-notices)' })
    if ($notices.Count -eq 0) {
        $withoutFile += $key
        continue
    }
    $target = Join-Path $Destination $id
    [System.IO.Directory]::CreateDirectory($target) | Out-Null
    foreach ($notice in $notices) {
        Copy-Item -LiteralPath $notice.FullName -Destination (Join-Path $target $notice.Name)
    }
}

$readme = @(
    'Licence and notice files of the third-party packages in this app folder,',
    'one folder per NuGet package, as the packages ship them. LICENSING.md',
    'next to this folder says under which permission Malachi Mail is',
    'distributed together with the Microsoft components among them.',
    '',
    'Packages whose licence is only a licence expression in their metadata',
    '(see https://www.nuget.org/packages/<id>/<version>):'
)
foreach ($key in $withoutFile) {
    $readme += '  ' + $key
}
[System.IO.File]::WriteAllText((Join-Path $Destination 'README.txt'), (($readme -join "`r`n") + "`r`n"), $Utf8)
Write-Host "notices of $($packages.Count) packages considered, copied into $Destination"
