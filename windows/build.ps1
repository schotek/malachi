# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Malachi Mail for Windows: the build entry point, the counterpart of
# macos/Makefile. The root Makefile runs it (make windows, run-windows,
# test-windows); without make:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 <target> [options]
#
# Targets:
#   version  the version (git describe, as the root Makefile) and its numeric form
#   go       malachid.exe and malachi-mcp.exe for -Arch into build\windows\go\<arch>\,
#            for the host's architecture also into build\ (.mcp.json, F5, make run-backend)
#   build    build the solution (Debug)
#   icons    render the application icon from docs\malachi_icon.png
#   app      go, then publish the app and the keyring helper self-contained and assemble
#            build\windows\<arch>\Malachi Mail\ (Release)
#   test     every test project of the solution (Debug); the UI smoke tests
#            (tests\Malachi.App.UiTests) drive the app folder of 'app' and are
#            skipped without it; MALACHI_DEVMAIL adds their mail-server suite
#   run      app for the host's architecture, then MalachiMail.exe in this terminal until it quits
#            (Ctrl+C quits it and the daemon it started)
#   lint     dotnet format --verify-no-changes and the conventions tests
#   package  app, then build\windows\Malachi-Mail-<version>-<arch>.zip
#   clean    remove build\windows
#
# Options: -Configuration Debug|Release, -Arch x64|arm64 (default: this
# machine's), -Version (default: git describe), -BuildDir (default: build\ in
# the repository).
#
# It runs on Windows PowerShell 5.1 and on PowerShell 7: no ternaries, ?? or
# &&; files are read and written through .NET as UTF-8 without BOM, never
# with Get-Content/Set-Content; every native command is judged by its exit
# status, and what it writes to stderr is shown, never taken for a failure.
# Keep the file ASCII below the header: Windows PowerShell reads a script
# without BOM in the ANSI code page.
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('version', 'go', 'build', 'icons', 'app', 'test', 'run', 'lint', 'package', 'clean')]
    [string] $Target = 'app',
    [ValidateSet('', 'Debug', 'Release')]
    [string] $Configuration = '',
    [ValidateSet('', 'x64', 'arm64')]
    [string] $Arch = '',
    [string] $Version = '',
    [string] $BuildDir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Here = $PSScriptRoot
$Root = [System.IO.Path]::GetFullPath((Join-Path $Here '..'))
$Utf8 = New-Object System.Text.UTF8Encoding $false

function Get-HostArch {
    # An x86 process on 64-bit Windows sees the machine in PROCESSOR_ARCHITEW6432.
    $machine = $env:PROCESSOR_ARCHITEW6432
    if (-not $machine) {
        $machine = $env:PROCESSOR_ARCHITECTURE
    }
    if ($machine -eq 'ARM64') {
        return 'arm64'
    }
    return 'x64'
}

$HostArch = Get-HostArch
if ($Target -eq 'run') {
    # The app is started here, so it is built for this machine.
    $Arch = $HostArch
}
if (-not $Arch) {
    $Arch = $HostArch
}
$Arch = $Arch.ToLowerInvariant()
$Platform = 'x64'
if ($Arch -eq 'arm64') {
    $Platform = 'ARM64'
}
if (-not $Configuration) {
    if (@('app', 'run', 'package') -contains $Target) {
        $Configuration = 'Release'
    } else {
        $Configuration = 'Debug'
    }
}
if ($Configuration -eq 'debug') {
    $Configuration = 'Debug'
} else {
    if ($Configuration -eq 'release') {
        $Configuration = 'Release'
    }
}
if (-not $BuildDir) {
    $BuildDir = Join-Path $Root 'build'
}
$BuildDir = [System.IO.Path]::GetFullPath($ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($BuildDir)).TrimEnd('\', '/')
$WinBuild = Join-Path $BuildDir 'windows'
$Artifacts = Join-Path $WinBuild 'artifacts'
$AppDir = Join-Path $WinBuild "$Arch\Malachi Mail"
$IconFile = Join-Path $Artifacts 'obj\Malachi.App\Malachi.ico'

# Runs a native command from $WorkingDirectory and fails on a non-zero exit
# status. Windows PowerShell turns the stderr lines of a native command into
# error records once its own stderr is redirected (make in some shells, CI,
# a pipe); they are written back to stderr as plain lines here instead.
function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)] [string] $Exe,
        [string[]] $Arguments = @(),
        [string] $WorkingDirectory = $Here
    )
    Write-Host ('> ' + $Exe + ' ' + ($Arguments -join ' '))
    $ErrorActionPreference = 'Continue'
    Push-Location -LiteralPath $WorkingDirectory
    try {
        if ([Console]::IsErrorRedirected) {
            & $Exe @Arguments 2>&1 | ForEach-Object {
                if ($_ -is [System.Management.Automation.ErrorRecord]) {
                    [Console]::Error.WriteLine($_.ToString())
                } else {
                    [Console]::Out.WriteLine($_)
                }
            }
        } else {
            & $Exe @Arguments
        }
        $status = $LASTEXITCODE
    } finally {
        Pop-Location
    }
    if ($status -ne 0) {
        throw "$([System.IO.Path]::GetFileName($Exe)) exited with status $status"
    }
}

# Stdout of a native command, or $null when it is missing or fails.
function Get-NativeOutput {
    param([string] $Exe, [string[]] $Arguments)
    if (-not (Get-Command $Exe -CommandType Application -ErrorAction SilentlyContinue)) {
        return $null
    }
    $ErrorActionPreference = 'Continue'
    $lines = & $Exe @Arguments 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }
    return (@($lines) -join "`n").Trim()
}

function Get-Tool {
    param([string] $Name, [string] $EnvVar, [string] $Fallback, [string] $Hint)
    if ($EnvVar) {
        $fromEnv = [Environment]::GetEnvironmentVariable($EnvVar)
        if ($fromEnv) {
            return $fromEnv
        }
    }
    $command = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) {
        return $command.Path
    }
    if ($Fallback -and (Test-Path -LiteralPath $Fallback)) {
        return $Fallback
    }
    throw "$Name was not found: $Hint"
}

# The root Makefile's rule: git describe --tags --always --dirty with the
# leading v cut; .version where there is no .git (a source tarball); "dev"
# otherwise. The numeric form (the file version) is Major.Minor.Patch and the
# commits since the tag, 0.0.0.0 without a tag; the full string goes into
# the informational version, as MalachiVersion does on macOS.
function Get-VersionInfo {
    $full = $Version
    if (-not $full) {
        $full = Get-NativeOutput 'git' @('-C', $Root, 'describe', '--tags', '--always', '--dirty')
    }
    if (-not $full) {
        $versionFile = Join-Path $Root '.version'
        if (Test-Path -LiteralPath $versionFile) {
            $full = ([System.IO.File]::ReadAllText($versionFile, $Utf8) -split "`r?`n")[0]
        }
    }
    if (-not $full) {
        $full = 'dev'
    }
    $full = $full.Trim() -replace '^v', ''
    $numeric = '0.0.0.0'
    $release = [regex]::Match($full, '^(\d+)(?:\.(\d+))?(?:\.(\d+))?(?=$|[-+])')
    if ($release.Success) {
        $parts = @()
        foreach ($group in 1, 2, 3) {
            $value = 0
            if ($release.Groups[$group].Success) {
                $value = [Math]::Min([long]$release.Groups[$group].Value, 65535)
            }
            $parts += $value
        }
        $commits = 0
        $described = [regex]::Match($full, '-(\d+)-g[0-9a-f]+(?:-dirty)?$')
        if ($described.Success) {
            $commits = [Math]::Min([long]$described.Groups[1].Value, 65535)
        }
        $numeric = '{0}.{1}.{2}.{3}' -f $parts[0], $parts[1], $parts[2], $commits
    }
    return New-Object PSObject -Property @{ Full = $full; Numeric = $numeric }
}

function Get-Languages {
    $text = [System.IO.File]::ReadAllText((Join-Path $Root 'po\LINGUAS'), $Utf8)
    $languages = @()
    foreach ($line in ($text -split "`r?`n")) {
        $line = ($line -replace '#.*$', '').Trim()
        if ($line) {
            $languages += ($line -split '\s+')
        }
    }
    return , $languages
}

$V = Get-VersionInfo
$MsbuildProperties = @(
    "-p:MalachiVersion=$($V.Full)",
    "-p:MalachiNumericVersion=$($V.Numeric)",
    "-p:MalachiBuildDir=$BuildDir"
)

function Get-Dotnet {
    return Get-Tool 'dotnet' 'DOTNET' (Join-Path $env:ProgramFiles 'dotnet\dotnet.exe') 'install the .NET SDK 10.0.4xx (windows\global.json)'
}

# Copies a host-architecture binary into build\, where .mcp.json, make
# run-backend and F5 (Directory.Build.targets) expect it. A daemon started
# from there keeps its file locked; that copy is then left as it was.
function Copy-DevBinary {
    param([string] $Path)
    [System.IO.Directory]::CreateDirectory($BuildDir) | Out-Null
    $destination = Join-Path $BuildDir ([System.IO.Path]::GetFileName($Path))
    try {
        [System.IO.File]::Copy($Path, $destination, $true)
    } catch {
        $cause = $_.Exception
        if ($cause.InnerException) {
            $cause = $cause.InnerException
        }
        if ($cause -is [System.IO.IOException] -or $cause -is [System.UnauthorizedAccessException]) {
            Write-Warning "$destination is in use (a daemon started from it?), left as it was: $($cause.Message)"
        } else {
            throw
        }
    }
}

# The daemon and the MCP bridge are pure Go (modernc.org/sqlite), so no C
# compiler; GOWORK=off builds the backend module alone, as the Flatpak does.
function Invoke-GoBuild {
    $go = Get-Tool 'go' 'GO' (Join-Path $env:ProgramFiles 'Go\bin\go.exe') 'install Go 1.25 or newer, or set GO to go.exe'
    $goArch = 'amd64'
    if ($Arch -eq 'arm64') {
        $goArch = 'arm64'
    }
    $out = Join-Path $WinBuild "go\$Arch"
    [System.IO.Directory]::CreateDirectory($out) | Out-Null
    $saved = @{}
    foreach ($name in 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOWORK') {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name)
    }
    try {
        $env:GOOS = 'windows'
        $env:GOARCH = $goArch
        $env:CGO_ENABLED = '0'
        $env:GOWORK = 'off'
        foreach ($command in 'malachid', 'malachi-mcp') {
            Invoke-Native $go @('build', '-trimpath', '-ldflags', "-X main.version=$($V.Full)",
                '-o', (Join-Path $out "$command.exe"), "./cmd/$command") (Join-Path $Root 'backend')
        }
    } finally {
        foreach ($name in $saved.Keys) {
            [Environment]::SetEnvironmentVariable($name, $saved[$name])
        }
    }
    if ($Arch -eq $HostArch) {
        foreach ($command in 'malachid', 'malachi-mcp') {
            Copy-DevBinary (Join-Path $out "$command.exe")
        }
    }
}

function Invoke-SolutionBuild {
    Invoke-Native (Get-Dotnet) (@('build', 'Malachi.slnx', '-c', $Configuration, "-p:Platform=$Platform") + $MsbuildProperties)
}

function Invoke-Icons {
    & (Join-Path $Here 'scripts\make-icons.ps1') -Source (Join-Path $Root 'docs\malachi_icon.png') -OutFile $IconFile
    Write-Host "rendered $IconFile"
}

# NativeAOT links with the MSVC tools; with Visual Studio 2026 their
# vcvarsall.bat needs vswhere.exe on PATH, which the installer does not add.
function Add-VsWhereToPath {
    $installer = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer'
    if ((Test-Path -LiteralPath (Join-Path $installer 'vswhere.exe')) -and
        -not (Get-Command 'vswhere.exe' -CommandType Application -ErrorAction SilentlyContinue)) {
        $env:PATH = "$installer;$env:PATH"
    }
}

function Test-AppFolder {
    # The Insights resource DLL: without it AppNotificationManager.Register()
    # fails (Malachi.App.csproj, MalachiInsightsResource).
    $expected = @('MalachiMail.exe', 'MalachiMail.pri', 'malachid.exe', 'malachi-mcp.exe', 'malachi-credentials.exe',
        'Microsoft.WindowsAppRuntime.Insights.Resource.dll', 'Assets\Malachi.ico',
        'LICENSE.txt', 'LICENSE-backend.txt', 'LICENSING.md')
    foreach ($language in (Get-Languages)) {
        $expected += "locale\$language.po"
    }
    $missing = @($expected | Where-Object { -not (Test-Path -LiteralPath (Join-Path $AppDir $_)) })
    if ($missing.Count -gt 0) {
        throw "$AppDir is incomplete, missing: $($missing -join ', ')"
    }
}

function Invoke-AppBuild {
    Invoke-GoBuild
    $dotnet = Get-Dotnet
    if (Test-Path -LiteralPath $AppDir) {
        Remove-Item -LiteralPath $AppDir -Recurse -Force
    }
    # The platform picks the app's RuntimeIdentifier and self-contained
    # comes from Malachi.App.csproj: a -r here would restore the referenced
    # projects for that runtime as well and rewrite their lock files.
    # EnableMsixTooling (Malachi.App.csproj) keeps MalachiMail.pri in the
    # publish output; without it the app dies at start.
    Invoke-Native $dotnet (@('publish', 'src\Malachi.App\Malachi.App.csproj', '-c', $Configuration,
            "-p:Platform=$Platform", '-o', $AppDir) + $MsbuildProperties)
    Add-VsWhereToPath
    $helperOut = Join-Path $Artifacts "publish\malachi-credentials-$Arch"
    try {
        Invoke-Native $dotnet (@('publish', 'src\Malachi.Credentials\Malachi.Credentials.csproj', '-c', $Configuration,
                '-r', "win-$Arch", '-o', $helperOut) + $MsbuildProperties)
    } catch {
        throw ("$($_.Exception.Message); malachi-credentials is NativeAOT and links with the MSVC build tools " +
            "for $Arch (Visual Studio or its Build Tools, 'Desktop development with C++'; for arm64 on an x64 " +
            "machine also the ARM64 build tools)")
    }
    Copy-Item -LiteralPath (Join-Path $helperOut 'malachi-credentials.exe') -Destination $AppDir
    foreach ($command in 'malachid', 'malachi-mcp') {
        Copy-Item -LiteralPath (Join-Path $WinBuild "go\$Arch\$command.exe") -Destination $AppDir
    }
    # The app is GPL-3.0-or-later, malachid and malachi-mcp AGPL-3.0-only
    # (LICENSING.md); .txt so that a double click opens them.
    Copy-Item -LiteralPath (Join-Path $Root 'LICENSE') -Destination (Join-Path $AppDir 'LICENSE.txt')
    Copy-Item -LiteralPath (Join-Path $Root 'backend\LICENSE') -Destination (Join-Path $AppDir 'LICENSE-backend.txt')
    Copy-Item -LiteralPath (Join-Path $Root 'LICENSING.md') -Destination $AppDir
    Test-AppFolder
    if ($Arch -eq $HostArch) {
        Copy-DevBinary (Join-Path $AppDir 'malachi-credentials.exe')
    }
    Write-Host "built $AppDir ($($V.Full), $Configuration, $Arch)"
}

function Invoke-Tests {
    Invoke-Native (Get-Dotnet) (@('test', '--solution', 'Malachi.slnx', '-c', $Configuration, "-p:Platform=$Platform",
            '--results-directory', (Join-Path $WinBuild 'TestResults'), '--report-trx') + $MsbuildProperties)
}

function Invoke-Lint {
    $dotnet = Get-Dotnet
    Invoke-Native $dotnet @('format', 'Malachi.slnx', '--verify-no-changes')
    Invoke-Native $dotnet (@('test', '--project', 'tests\Malachi.Conventions.Tests\Malachi.Conventions.Tests.csproj',
            '-c', $Configuration) + $MsbuildProperties)
}

# The app attaches to this console by itself (docs/windows-port.md §5
# Console): its log and the daemon's reach the terminal, and Ctrl+C reaches
# the app, which quits gracefully and stops the daemon it started (the
# daemon, in a process group of its own, is spared the Ctrl+C). So it is
# started without pipes (a pipe would be the app's stderr instead, and on
# Ctrl+C PowerShell kills a piped program and orphans its daemon;
# spikes2/INPUT-SPIKES.md §4.3), and waited for in a loop that Ctrl+C
# interrupts, then up to 20 s more while the app shuts down. Never & $exe
# (it returns at once) or & $exe | ...
function Invoke-Run {
    Invoke-AppBuild
    $exe = Join-Path $AppDir 'MalachiMail.exe'
    Write-Host "running $exe; Quit (Ctrl+Q), closing its window or Ctrl+C here ends this command"
    $start = New-Object System.Diagnostics.ProcessStartInfo $exe
    $start.UseShellExecute = $false
    $start.WorkingDirectory = $AppDir
    $app = [System.Diagnostics.Process]::Start($start)
    $done = $false
    try {
        while (-not $app.WaitForExit(200)) {
        }
        $done = $true
    } finally {
        if (-not $done) {
            # Ctrl+C: the app got it too and is quitting; wait for it.
            [void]$app.WaitForExit(20000)
        }
    }
    $status = $app.ExitCode
    if ($status -ne 0) {
        throw "MalachiMail.exe exited with status $status"
    }
}

function Invoke-Package {
    Invoke-AppBuild
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = Join-Path $WinBuild ('Malachi-Mail-{0}-{1}.zip' -f $V.Full, $Arch)
    if (Test-Path -LiteralPath $zip) {
        Remove-Item -LiteralPath $zip -Force
    }
    # Entry by entry with / as the separator: Windows PowerShell's
    # CreateFromDirectory and Compress-Archive may write backslashes, which
    # other unzip tools take as part of the name.
    $archive = [System.IO.Compression.ZipFile]::Open($zip, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        $folder = [System.IO.Path]::GetFileName($AppDir)
        foreach ($file in [System.IO.Directory]::GetFiles($AppDir, '*', [System.IO.SearchOption]::AllDirectories)) {
            $relative = $file.Substring($AppDir.Length).TrimStart('\').Replace('\', '/')
            [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $file, "$folder/$relative",
                [System.IO.Compression.CompressionLevel]::Optimal) | Out-Null
        }
    } finally {
        $archive.Dispose()
    }
    Write-Host "packaged $zip"
}

try {
    switch ($Target) {
        'version' {
            Write-Output "version $($V.Full)"
            Write-Output "file-version $($V.Numeric)"
        }
        'go' { Invoke-GoBuild }
        'build' { Invoke-SolutionBuild }
        'icons' { Invoke-Icons }
        'app' { Invoke-AppBuild }
        'test' { Invoke-Tests }
        'run' { Invoke-Run }
        'lint' { Invoke-Lint }
        'package' { Invoke-Package }
        'clean' {
            if (Test-Path -LiteralPath $WinBuild) {
                Remove-Item -LiteralPath $WinBuild -Recurse -Force
            }
            Write-Host "removed $WinBuild"
        }
    }
} catch {
    [Console]::Error.WriteLine("build.ps1 ${Target}: $($_.Exception.Message)")
    exit 1
}
exit 0
