# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# The Windows screenshots of docs/screenshots.md (§6): the sample world of
# stack.sh started by hand, as the document's PowerShell block does, and
# the published app (windows\build.ps1 app) put through the scenes with
# MALACHI_START (MainWindow.DevStart.cs), each scene written as a PNG by
# the app itself (the `shot=` step: the window's visible frame at the
# screen's scale, from the screen).
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\screenshots\windows.ps1 start en|cs [DIR]
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\screenshots\windows.ps1 shots en|cs OUTDIR [DIR]
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\screenshots\windows.ps1 stop [DIR]
#
# DIR (default %TEMP%\malachi-shots; short, the socket path has at most
# 107 bytes and must be under the user's profile) gets bin\ (the Go
# binaries, built here with go.exe from GO, the PATH or Program Files),
# data\ (the daemon's config, store and mail, wiped by every start),
# appdata\ (the app's own data directory, wiped by every run) and the
# logs. The ports are those of stack.sh (11143, 11025, 11080, 11090,
# 11095), overridden by the same SHOTS_* variables. The app runs with
# MALACHI_DAEMON=none, the stack's socket, its own MALACHI_DATA_DIR, the
# preferences key io.github.schotek.Malachi.shots (deleted afterwards),
# MALACHI_CALENDAR_SAMPLES=1 and MALACHI_LANGUAGES (en-GB or cs-CZ):
# nothing of it touches the user's data, preferences or registrations.
# The app must stay in front while it runs (about two minutes a run): a
# window over it would be in the shots.
[CmdletBinding()]
param(
    [Parameter(Position = 0)] [ValidateSet('start', 'shots', 'stop')] [string] $Command = '',
    [Parameter(Position = 1)] [string] $Lang = '',
    [Parameter(Position = 2)] [string] $Out = '',
    [Parameter(Position = 3)] [string] $Dir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
if ($Command -eq 'stop') {
    # stop [DIR]: the second word is the directory.
    if ($Lang) { $Dir = $Lang }
} elseif ($Command -eq 'start') {
    # start en|cs [DIR]
    if ($Out) { $Dir = $Out }
}
if (-not $Dir) {
    $Dir = Join-Path $env:TEMP 'malachi-shots'
}
$Dir = [System.IO.Path]::GetFullPath($Dir)
$Sock = if ($env:MALACHI_SHOTS_SOCKET) { $env:MALACHI_SHOTS_SOCKET } else { Join-Path $Dir 'rpc.sock' }
$Imap = if ($env:SHOTS_IMAP_PORT) { $env:SHOTS_IMAP_PORT } else { 11143 }
$Smtp = if ($env:SHOTS_SMTP_PORT) { $env:SHOTS_SMTP_PORT } else { 11025 }
$Control = if ($env:SHOTS_CONTROL_PORT) { $env:SHOTS_CONTROL_PORT } else { 11080 }
$Jira = if ($env:SHOTS_JIRA_PORT) { $env:SHOTS_JIRA_PORT } else { 11090 }
$Eway = if ($env:SHOTS_EWAY_PORT) { $env:SHOTS_EWAY_PORT } else { 11095 }
$Pids = Join-Path $Dir 'pids'
$SettingsKey = 'io.github.schotek.Malachi.shots'

function Usage {
    [Console]::Error.WriteLine('usage: windows.ps1 start en|cs [DIR] | windows.ps1 shots en|cs OUTDIR [DIR] | windows.ps1 stop [DIR]')
    exit 2
}

function Stop-Stack {
    # The servers themselves, every malachid and malachi-devmail that runs
    # from DIR\bin (never a pid from a file: a launcher's pid may have been
    # given to another program since), then their cmd.exe launchers, then a
    # wait until the IMAP port is free: a server left behind would keep the
    # port, the next serve would fail to bind, and the daemon would sync
    # the old server's flags.
    $inside = (Join-Path $Dir 'bin') + '\'
    foreach ($p in Get-Process -Name 'malachid', 'malachi-devmail' -ErrorAction SilentlyContinue) {
        $path = $null
        try { $path = $p.Path } catch {}
        if ($path -and $path.StartsWith($inside, [System.StringComparison]::OrdinalIgnoreCase)) {
            try { Stop-Process -Id $p.Id -Force -ErrorAction Stop } catch {}
        }
    }
    $launcher = Join-Path $Dir 'run-'
    foreach ($c in Get-CimInstance Win32_Process -Filter "Name = 'cmd.exe'" -ErrorAction SilentlyContinue) {
        if ($c.CommandLine -and $c.CommandLine.IndexOf($launcher, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
            try { Stop-Process -Id $c.ProcessId -Force -ErrorAction Stop } catch {}
        }
    }
    if (Test-Path -LiteralPath $Pids) { Remove-Item -LiteralPath $Pids -Force }
    for ($i = 0; $i -lt 40; $i++) {
        if (-not (Get-NetTCPConnection -LocalPort $Imap -State Listen -ErrorAction SilentlyContinue)) { break }
        Start-Sleep -Milliseconds 250
    }
    if (Get-NetTCPConnection -LocalPort $Imap -State Listen -ErrorAction SilentlyContinue) {
        throw "stack: something still listens on port $Imap"
    }
    Start-Sleep -Milliseconds 500
}

function Get-Go {
    if ($env:GO) { return $env:GO }
    $command = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return $command.Path }
    foreach ($candidate in @((Join-Path $env:ProgramFiles 'Go\bin\go.exe'), (Join-Path $env:LOCALAPPDATA 'Programs\go\bin\go.exe'))) {
        if (Test-Path -LiteralPath $candidate) { return $candidate }
    }
    throw 'go.exe was not found: install Go 1.25 or newer, or set GO'
}

# The socket's key file inherits the directory's ACL and the app refuses a
# key other users may read (WindowsKeyFilePolicy); %TEMP% may grant another
# principal access, so DIR gets an ACL of its own, the user and SYSTEM only,
# as the UI smoke tests' AppSession gives its folder.
function New-PrivateDirectory {
    param([string] $Path)
    if (Test-Path -LiteralPath $Path) { return }
    $security = New-Object System.Security.AccessControl.DirectorySecurity
    $security.SetAccessRuleProtection($true, $false)
    $inherit = [System.Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
    foreach ($who in @([System.Security.Principal.WindowsIdentity]::GetCurrent().User,
            (New-Object System.Security.Principal.SecurityIdentifier([System.Security.Principal.WellKnownSidType]::LocalSystemSid, $null)))) {
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($who, 'FullControl', $inherit, 'None', 'Allow')
        $security.AddAccessRule($rule)
    }
    [System.IO.Directory]::CreateDirectory($Path, $security) | Out-Null
}

# A server started in the background with its output in files, through a
# hidden cmd.exe (ShellExecute): started by PowerShell itself, a server
# would inherit this script's standard handles and hold a caller's pipe
# open for as long as it runs (what docs/windows-port.md §5 says of the
# app's console). The launcher's pid goes to the pids file; Stop-Stack
# also finds the servers by their path.
function Start-Hidden {
    param([string] $Name, [string] $Exe, [string[]] $Arguments, [string] $OutFile, [string] $ErrFile)
    $quoted = @($Arguments | ForEach-Object { if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ } })
    $launcher = Join-Path $Dir "run-$Name.cmd"
    $text = "@echo off`r`n`"$Exe`" $($quoted -join ' ') > `"$OutFile`" 2> `"$ErrFile`"`r`n"
    [System.IO.File]::WriteAllText($launcher, $text, (New-Object System.Text.UTF8Encoding $false))
    $p = Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\cmd.exe') -ArgumentList @('/c', "`"$launcher`"") -WindowStyle Hidden -PassThru
    Add-Content -LiteralPath $Pids -Value $p.Id
    return $p
}

switch ($Command) {
    'stop' {
        Stop-Stack
        Write-Host 'stack: stopped'
        exit 0
    }
    'start' {
        if ($Lang -notin @('en', 'cs')) { Usage }
        Stop-Stack
        New-PrivateDirectory $Dir
        New-PrivateDirectory (Split-Path -Parent $Sock)
        New-Item -ItemType Directory -Force (Join-Path $Dir 'bin') | Out-Null
        $data = Join-Path $Dir 'data'
        if (Test-Path -LiteralPath $data) { Remove-Item -LiteralPath $data -Recurse -Force }
        New-Item -ItemType Directory -Force $data | Out-Null
        if (Test-Path -LiteralPath $Sock) { Remove-Item -LiteralPath $Sock -Force }

        # The daemon, the sample servers and the RPC tool, from this tree
        # (the Go build cache makes a repeat a no-op); the backend module
        # alone, as build.ps1 builds it.
        $go = Get-Go
        $saved = $env:GOWORK
        $env:GOWORK = 'off'
        try {
            foreach ($bin in 'malachid', 'malachi-devmail', 'malachi-rpc') {
                Push-Location (Join-Path $Root 'backend')
                try {
                    & $go build -o (Join-Path $Dir "bin\$bin.exe") "./cmd/$bin"
                    if ($LASTEXITCODE -ne 0) { throw "go build ./cmd/$bin failed" }
                } finally {
                    Pop-Location
                }
            }
        } finally {
            $env:GOWORK = $saved
        }
        $devmail = Join-Path $Dir 'bin\malachi-devmail.exe'
        $daemon = Join-Path $Dir 'bin\malachid.exe'
        $rpc = Join-Path $Dir 'bin\malachi-rpc.exe'

        $env:MALACHI_DEVMAIL_KEYRING = Join-Path $data 'keyring.json'
        Start-Hidden 'devmail' $devmail @('serve', '-lang', $Lang, '-imap-port', $Imap, '-smtp-port', $Smtp, '-control-port', $Control,
            '-jira-port', $Jira, '-eway-port', $Eway) (Join-Path $Dir 'devmail.out') (Join-Path $Dir 'devmail.err') | Out-Null
        # The new server, and no other, listens: its process owns the port.
        $owner = $null
        for ($i = 0; $i -lt 40 -and -not $owner; $i++) {
            Start-Sleep -Milliseconds 250
            $listener = Get-NetTCPConnection -LocalPort $Imap -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($listener) { $owner = $listener.OwningProcess }
        }
        $server = if ($owner) { Get-Process -Id $owner -ErrorAction SilentlyContinue } else { $null }
        $serverPath = $null
        if ($server) { try { $serverPath = $server.Path } catch {} }
        if (-not $server -or $server.Name -ne 'malachi-devmail' -or -not $serverPath -or
            -not $serverPath.StartsWith((Join-Path $Dir 'bin') + '\', [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "stack: malachi-devmail did not take port $Imap; see $Dir\devmail.err"
        }

        # The daemon's keyring is the sample servers' JSON file (the helper
        # protocol of internal/auth/helper), its zone a fixed one so that
        # the dates of the set read the same on every machine.
        $env:MALACHI_KEYRING = 'helper'
        $env:MALACHI_KEYRING_HELPER = $devmail
        if (-not $env:MALACHI_TIME_ZONE) { $env:MALACHI_TIME_ZONE = 'Europe/Prague' }
        $env:MALACHI_LOG_LEVEL = 'info'
        Start-Hidden 'malachid' $daemon @('--config', (Join-Path $data 'config.toml'), '--store', (Join-Path $data 'store.db'), '--socket', $Sock) `
            (Join-Path $Dir 'malachid.out') (Join-Path $Dir 'malachid.log') | Out-Null
        $i = 0
        while (-not ((Test-Path -LiteralPath $Sock) -and (Test-Path -LiteralPath "$Sock.key"))) {
            $i++
            if ($i -gt 80) { throw "stack: the daemon did not open $Sock; see $Dir\malachid.log" }
            Start-Sleep -Milliseconds 250
        }
        Start-Sleep -Seconds 1

        # The mail, Jira and eWay-CRM accounts of the sample user;
        # add-account also saves the set's saved conversation into the CRM.
        & $devmail add-account -socket $Sock -imap-port $Imap -smtp-port $Smtp -jira-port $Jira -eway-port $Eway -lang $Lang
        if ($LASTEXITCODE -ne 0) { throw 'stack: add-account failed' }

        # The first synchronisation is done when the board has evaluated
        # the set: 17 cases in either language.
        $want = 17
        $n = 0
        $i = 0
        while ($n -lt $want) {
            $i++
            if ($i -gt 90) { throw "stack: only $n of $want board cases after 90 s; see $Dir\malachid.log" }
            Start-Sleep -Seconds 1
            $listed = & $rpc --socket $Sock --timeout 10s board.list '{}' 2>$null
            $n = @(($listed | Out-String) -split "`n" | Where-Object { $_ -match '"id":\s*"c_' }).Count
        }
        Write-Host "stack: ready ($Lang): $n board cases; socket $Sock; IMAP $Imap, SMTP $Smtp, control $Control, Jira $Jira, eWay $Eway"
        Write-Host "stack: the client runs with MALACHI_DAEMON=none MALACHI_SOCKET=$Sock and its own MALACHI_DATA_DIR"
        exit 0
    }
    'shots' {
        if ($Lang -notin @('en', 'cs') -or -not $Out) { Usage }
        if (-not (Test-Path -LiteralPath $Sock)) { throw "windows.ps1: no stack at $Sock; run windows.ps1 start $Lang first" }
        New-Item -ItemType Directory -Force $Out | Out-Null
        $Out = [System.IO.Path]::GetFullPath($Out)
        $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'x64' }
        $app = Join-Path $Root "build\windows\$arch\Malachi Mail\MalachiMail.exe"
        if (-not (Test-Path -LiteralPath $app)) { throw "windows.ps1: no app at $app; run windows\build.ps1 app first" }

        # The language and the region (MALACHI_LANGUAGES: the catalogue and
        # the formats), the subjects and folders of the sample set (server
        # folder names; a substring of the subject, case-insensitively). The
        # Czech subjects are spelled with character codes: Windows PowerShell
        # reads a script without a BOM in the ANSI code page (build.ps1).
        switch ($Lang) {
            'en' { $languages = 'en-GB'; $quote = 'Quotation'; $meeting = 'Q4 planning'; $request = 'Request for a quote' }
            'cs' {
                $languages = 'cs-CZ'
                $quote = 'Nab' + [char]0x00ED + 'dka'      # Nabidka with an i-acute
                $meeting = 'Porada'
                $request = 'Popt' + [char]0x00E1 + 'vka'   # Poptavka with an a-acute
            }
        }

        # The pointer out of the window's way (a hover would show); the
        # shell's own coordinates are the virtual, unscaled ones.
        Add-Type -AssemblyName System.Windows.Forms
        $bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
        [System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point ($bounds.Right - 1), ($bounds.Bottom - 1)

        function Invoke-Run {
            param([string] $Script)
            # A fresh data directory for every run; WebView2's browser
            # processes hold the previous run's for some seconds after the
            # app has gone (the UI smoke tests' AppSession measured more
            # than five), so the removal is tried again for a while.
            $appdata = Join-Path $Dir 'appdata'
            for ($attempt = 0; $attempt -lt 120 -and (Test-Path -LiteralPath $appdata); $attempt++) {
                try { Remove-Item -LiteralPath $appdata -Recurse -Force -ErrorAction Stop } catch { Start-Sleep -Milliseconds 250 }
            }
            if (Test-Path -LiteralPath $appdata) { throw "windows.ps1: $appdata is still in use" }
            New-Item -ItemType Directory -Force $appdata | Out-Null
            $start = New-Object System.Diagnostics.ProcessStartInfo $app
            $start.UseShellExecute = $false
            $start.WorkingDirectory = $Out
            $start.RedirectStandardError = $true
            $start.RedirectStandardOutput = $true
            $e = $start.Environment
            $e['MALACHI_DAEMON'] = 'none'
            $e['MALACHI_SOCKET'] = $Sock
            $e['MALACHI_DATA_DIR'] = $appdata
            $e['MALACHI_SETTINGS_KEY'] = $SettingsKey
            $e['MALACHI_CALENDAR_SAMPLES'] = '1'
            $e['MALACHI_LANGUAGES'] = $languages
            $e['MALACHI_START'] = $Script
            $e['MALACHI_START_SIZE'] = if ($env:SIZE) { $env:SIZE } else { '1440x900' }
            $e['MALACHI_START_INTERVAL'] = if ($env:INTERVAL) { $env:INTERVAL } else { '4' }
            foreach ($name in 'MALACHI_LOCALE_DIR', 'MALACHI_KEYRING', 'MALACHI_KEYRING_HELPER', 'MALACHI_BOARD_SAMPLES') { $e.Remove($name) | Out-Null }
            $p = [System.Diagnostics.Process]::Start($start)
            # Both pipes drained by tasks (a script block as an event handler
            # would run on a thread without a runspace and end the shell).
            $errTask = $p.StandardError.ReadToEndAsync()
            $outTask = $p.StandardOutput.ReadToEndAsync()
            # The run ends with the app's quit step; a hung app is killed.
            $watchdog = if ($env:WATCHDOG) { [int]$env:WATCHDOG } else { 300 }
            if (-not $p.WaitForExit($watchdog * 1000)) {
                [Console]::Error.WriteLine('windows.ps1: the app did not quit, killing it')
                try { $p.Kill($true) } catch {}
                $p.WaitForExit(5000) | Out-Null
            }
            $p.WaitForExit()
            $text = $errTask.Result + $outTask.Result
            $err = Join-Path $Dir "app-$Lang.err"
            [System.IO.File]::WriteAllText($err, $text, (New-Object System.Text.UTF8Encoding $false))
            ($text -split "`r?`n") | Where-Object { $_ -match 'MALACHI_START (row|folder|board|shot|resize|step \d+ quit)' } | ForEach-Object { "  $_" }
        }

        try {
            # Mail (grouped by conversation), the chat, the invitation, the
            # newsletter, a Jira issue, the Board in both styles, the
            # Calendar's week and a blank New Message window.
            Invoke-Run "group=on;chat=off;folder=inbox;row=DevConf;shot=$Out\inbox.png;row=$quote;chat=on;shot=$Out\chat.png;chat=off;row=$meeting;shot=$Out\invitation.png;row=Design Weekly;shot=$Out\newsletter.png;folder=Web;row=WEB-1;shot=$Out\jira.png;board:list:select;shot=$Out\board-list.png;board:columns;shot=$Out\board-columns.png;calendar:week;shot=$Out\calendar-week.png;mail;folder=inbox;row=DevConf;compose-open;shot=$Out\compose.png;quit"
            # eWay-CRM: the saved conversation in Sent and the Save to
            # eWay-CRM dialog over the request in the Inbox (a flat list: the
            # Sent copy is one row).
            Invoke-Run "group=off;chat=off;folder=sent;row=$quote;shot=$Out\crm-saved.png;folder=inbox;row=$request;crm-save;shot=$Out\crm-dialog.png;quit"
        } finally {
            # The throwaway preferences key (the app's watcher recreates a
            # key deleted while it runs, so only now).
            Remove-Item -Path "HKCU:\Software\$SettingsKey" -Recurse -Force -ErrorAction SilentlyContinue
        }
        Get-ChildItem -LiteralPath $Out -Filter *.png | ForEach-Object { "windows.ps1: $($_.FullName) ($($_.Length) bytes)" }
        exit 0
    }
    default { Usage }
}
