; SPDX-FileCopyrightText: 2026 Vladislav Janeček
; SPDX-License-Identifier: GPL-3.0-or-later
;
; The installer of Malachi Mail for Windows (Inno Setup 6.4 or newer):
; one per architecture, from an app folder build.ps1 assembled. It installs
; for the current user, without administrator rights, into
; %LOCALAPPDATA%\Programs\Malachi Mail, adds a Start menu shortcut and the
; uninstall entry in Settings -> Apps, and nothing else: the mailto: and
; Run registrations are the app's own (Preferences), never the installer's.
; The uninstaller removes those registrations, and the notification
; registration Windows App SDK writes for the executable, only when they
; point into this installation; the mail data, the preferences and the
; stored passwords stay (they belong to the user, as on macOS).
;
; build-installer.ps1 runs it; defines it expects:
;   AppVersion      the version, 0.1.2
;   NumericVersion  the file version, 0.1.2.0
;   Arch            x64 or arm64
;   SourceDir       the staged app folder
;   OutputDir       where Malachi-Mail-<version>-<arch>-Setup.exe goes

#ifndef AppVersion
  #error AppVersion is not defined
#endif
#ifndef NumericVersion
  #error NumericVersion is not defined
#endif
#ifndef Arch
  #error Arch is not defined
#endif
#ifndef SourceDir
  #error SourceDir is not defined
#endif
#ifndef OutputDir
  #error OutputDir is not defined
#endif

#define AppName "Malachi Mail"
#define AppExe "MalachiMail.exe"

[Setup]
; The application ID, as CLAUDE.md fixes it: both architectures are one
; installation, so one replaces the other.
AppId=io.github.schotek.Malachi
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher=Vladislav Janeček
AppPublisherURL=https://github.com/schotek/malachi
AppSupportURL=https://github.com/schotek/malachi/issues
AppUpdatesURL=https://github.com/schotek/malachi/releases
VersionInfoVersion={#NumericVersion}
VersionInfoProductVersion={#NumericVersion}
VersionInfoProductTextVersion={#AppVersion}
VersionInfoDescription={#AppName} Setup
; Per user: {userpf} is %LOCALAPPDATA%\Programs.
PrivilegesRequired=lowest
DefaultDirName={userpf}\{#AppName}
DisableDirPage=yes
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif
MinVersion=10.0.22000
OutputDir={#OutputDir}
OutputBaseFilename=Malachi-Mail-{#AppVersion}-{#Arch}-Setup
SetupIconFile={#SourceDir}\Assets\Malachi.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName={#AppName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
; Restart Manager closes what holds a file of the folder, typically a
; malachi-mcp.exe that Claude Desktop or Claude Code runs (they start it
; again); the app and its daemon are asked to quit first (PrepareToInstall).
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "czech"; MessagesFile: "compiler:Languages\Czech.isl"

[CustomMessages]
english.AppRunning=Malachi Mail is running. Quit it (Quit in its menu or on its notification-area icon), then try again.
czech.AppRunning=Malachi Mail je spuštěný. Ukončete ho (Konec v jeho nabídce nebo na jeho ikoně v oznamovací oblasti) a zkuste to znovu.

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[InstallDelete]
; Files of an older version that this one no longer has would stay beside
; it; the managed assemblies and the Windows App SDK resolve files by name.
Type: filesandordirs; Name: "{app}\*"

[Icons]
Name: "{userprograms}\{#AppName}"; Filename: "{app}\{#AppExe}"; WorkingDir: "{app}"

[Run]
Filename: "{app}\{#AppExe}"; Description: "{cm:LaunchProgram,{#AppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; What the app left in its own folder (nothing today; a crash dump, a
; file moved aside by an update).
Type: filesandordirs; Name: "{app}"

[Code]
const
  SHCNE_ASSOCCHANGED = $08000000;
  SHCNF_IDLIST = 0;
  RunKey = 'Software\Microsoft\Windows\CurrentVersion\Run';
  StartupApprovedKey = 'Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run';
  MailtoProgId = 'Software\Classes\io.github.schotek.Malachi.mailto';
  MailClient = 'Software\Clients\Mail\Malachi Mail';
  RegisteredApplications = 'Software\RegisteredApplications';
  ClassesClsid = 'Software\Classes\CLSID';
  ClassesAumid = 'Software\Classes\AppUserModelId';
  NotifyIconSettings = 'Control Panel\NotifyIconSettings';
  TileProperties = 'Software\Microsoft\Windows\CurrentVersion\Start\TileProperties';
  MuiCache = 'Software\Classes\Local Settings\Software\Microsoft\Windows\Shell\MuiCache';
  JumplistData = 'Software\Microsoft\Windows\CurrentVersion\Search\JumplistData';
  PushNotificationsBackup = 'Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup';

procedure SHChangeNotify(EventId: Integer; Flags: Cardinal; Item1, Item2: Integer);
  external 'SHChangeNotify@shell32.dll stdcall';

function AppPrefix(): String;
begin
  Result := Lowercase(AddBackslash(ExpandConstant('{app}')));
end;

function PointsIntoApp(Value: String): Boolean;
begin
  Result := Pos(AppPrefix(), Lowercase(Value)) > 0;
end;

// The app, its daemon or its keyring helper running from this folder, by
// their executable's path (another copy elsewhere is no concern).
// malachi-mcp.exe is left to Restart Manager: Claude runs it, not the app.
function AppRunning(): Boolean;
var
  Locator, Service, Processes, Process: Variant;
  I: Integer;
  Path: String;
begin
  Result := False;
  try
    Locator := CreateOleObject('WbemScripting.SWbemLocator');
    Service := Locator.ConnectServer('.', 'root\CIMV2');
    Processes := Service.ExecQuery('SELECT ExecutablePath FROM Win32_Process WHERE ' +
      'Name = ''MalachiMail.exe'' OR Name = ''malachid.exe'' OR Name = ''malachi-credentials.exe''');
    for I := 0 to Processes.Count - 1 do
    begin
      Process := Processes.ItemIndex(I);
      if not VarIsNull(Process.ExecutablePath) then
      begin
        Path := Process.ExecutablePath;
        if Pos(AppPrefix(), Lowercase(Path)) = 1 then
        begin
          Result := True;
          Exit;
        end;
      end;
    end;
  except
    // Without WMI, Restart Manager still sees the files in use.
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  if AppRunning() then
    Result := CustomMessage('AppRunning');
end;

function InitializeUninstall(): Boolean;
begin
  Result := True;
  while AppRunning() do
  begin
    if SuppressibleMsgBox(CustomMessage('AppRunning'), mbError, MB_RETRYCANCEL, IDCANCEL) = IDCANCEL then
    begin
      Result := False;
      Exit;
    end;
  end;
end;

// Launch at login (Preferences -> General): the Run value and its
// StartupApproved twin, when they start this installation.
procedure RemoveRun();
var
  Command: String;
begin
  if RegQueryStringValue(HKCU, RunKey, 'Malachi Mail', Command) and PointsIntoApp(Command) then
  begin
    RegDeleteValue(HKCU, RunKey, 'Malachi Mail');
    RegDeleteValue(HKCU, StartupApprovedKey, 'Malachi Mail');
  end;
end;

// The mailto: capabilities (MailtoRegistration.Unregister), when they open
// this installation. The user's default choice is Windows' (UserChoice);
// Windows falls back by itself once the ProgID is gone.
function RemoveMailto(): Boolean;
var
  Command: String;
begin
  Result := False;
  if RegQueryStringValue(HKCU, MailtoProgId + '\shell\open\command', '', Command) and PointsIntoApp(Command) then
  begin
    RegDeleteKeyIncludingSubkeys(HKCU, MailtoProgId);
    RegDeleteKeyIncludingSubkeys(HKCU, MailClient);
    RegDeleteValue(HKCU, RegisteredApplications, 'Malachi Mail');
    Result := True;
  end;
end;

// The notification registration of an unpackaged app
// (AppNotificationManager.Register), three keys: a COM activator under
// CLSID whose LocalServer32 starts this installation's executable, the
// AppUserModelId {guid} entry that names it as its CustomActivator, and the
// AppUserModelId entry named after the executable's path whose
// NotificationGUID names that entry.
procedure RemoveNotifications();
var
  Classes, Aumids: TArrayOfString;
  I, J, K: Integer;
  Server, Activator, Guid: String;
begin
  if not RegGetSubkeyNames(HKCU, ClassesClsid, Classes) then
    Exit;
  if not RegGetSubkeyNames(HKCU, ClassesAumid, Aumids) then
    SetArrayLength(Aumids, 0);
  for I := 0 to GetArrayLength(Classes) - 1 do
  begin
    if RegQueryStringValue(HKCU, ClassesClsid + '\' + Classes[I] + '\LocalServer32', '', Server) and
      PointsIntoApp(Server) then
    begin
      for J := 0 to GetArrayLength(Aumids) - 1 do
        if RegQueryStringValue(HKCU, ClassesAumid + '\' + Aumids[J], 'CustomActivator', Activator) and
          (CompareText(Activator, Classes[I]) = 0) then
        begin
          for K := 0 to GetArrayLength(Aumids) - 1 do
            if RegQueryStringValue(HKCU, ClassesAumid + '\' + Aumids[K], 'NotificationGUID', Guid) and
              (CompareText(Guid, Aumids[J]) = 0) then
              RegDeleteKeyIncludingSubkeys(HKCU, ClassesAumid + '\' + Aumids[K]);
          RegDeleteKeyIncludingSubkeys(HKCU, ClassesAumid + '\' + Aumids[J]);
          // The notification platform's copy of the app's settings.
          RegDeleteKeyIncludingSubkeys(HKCU, PushNotificationsBackup + '\' + Aumids[J]);
        end;
      RegDeleteKeyIncludingSubkeys(HKCU, ClassesClsid + '\' + Classes[I]);
    end;
  end;
end;

// What the shell remembers about the executables of this folder: the
// notification-area icon's settings, the Start menu tile's properties and
// the names in the MUI cache. Windows writes them for every program it
// meets; they are removed here so that no key names a deleted folder.
procedure RemoveShellTraces();
var
  Names: TArrayOfString;
  I: Integer;
  Path: String;
begin
  if RegGetSubkeyNames(HKCU, NotifyIconSettings, Names) then
    for I := 0 to GetArrayLength(Names) - 1 do
      if RegQueryStringValue(HKCU, NotifyIconSettings + '\' + Names[I], 'ExecutablePath', Path) and
        PointsIntoApp(Path) then
        RegDeleteKeyIncludingSubkeys(HKCU, NotifyIconSettings + '\' + Names[I]);
  // A tree of keys along the folder's path: W~C:\Users\...\Malachi Mail.
  RegDeleteKeyIncludingSubkeys(HKCU, TileProperties + '\W~' + RemoveBackslash(ExpandConstant('{app}')));
  if RegGetValueNames(HKCU, MuiCache, Names) then
    for I := 0 to GetArrayLength(Names) - 1 do
      if Pos(AppPrefix(), Lowercase(Names[I])) = 1 then
        RegDeleteValue(HKCU, MuiCache, Names[I]);
  if RegGetValueNames(HKCU, JumplistData, Names) then
    for I := 0 to GetArrayLength(Names) - 1 do
      if Pos(AppPrefix(), Lowercase(Names[I])) = 1 then
        RegDeleteValue(HKCU, JumplistData, Names[I]);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    RemoveRun();
    RemoveNotifications();
    RemoveShellTraces();
    if RemoveMailto() then
      SHChangeNotify(SHCNE_ASSOCCHANGED, SHCNF_IDLIST, 0, 0);
  end;
end;
