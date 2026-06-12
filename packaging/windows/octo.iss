; octo-setup — per-user Windows installer for octo.
;
; Installs octo.exe to %LOCALAPPDATA%\Programs\octo, adds that directory to the
; user PATH (HKCU — no administrator, no UAC), creates a Start-menu shortcut,
; and registers an uninstaller in "Add or remove programs". Per-user is
; deliberate: the install directory stays user-writable, so `octo upgrade` can
; overwrite the binary in place without elevation.
;
; Compiled in CI by .github/workflows/release.yml. Two defines are passed in:
;   AppVersion — the release version, e.g. 0.20.0
;   SourceDir  — the folder holding octo.exe and LICENSE.txt
; Compile locally:  ISCC.exe /DAppVersion=0.0.0 /DSourceDir=path\to\bits octo.iss

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceDir
  #define SourceDir "."
#endif

[Setup]
; A stable AppId so re-running a newer installer updates in place rather than
; stacking a second copy. Never change this value.
AppId={{8F2A6B1C-3D4E-4F50-9A6B-7C8D9E0F1A2B}
AppName=octo
AppVersion={#AppVersion}
AppPublisher=Leihb
AppPublisherURL=https://github.com/Leihb/octo-agent
DefaultDirName={userpf}\octo
DisableProgramGroupPage=yes
DisableDirPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputBaseFilename=octo-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
; Broadcast WM_SETTINGCHANGE after install so Explorer-launched shells pick up
; the new PATH without a logout.
ChangesEnvironment=yes
UninstallDisplayName=octo {#AppVersion}

[Files]
Source: "{#SourceDir}\octo.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
; Open a console with octo started. Invoked by full path so it works even
; before the new PATH propagates.
Name: "{userprograms}\octo"; Filename: "{cmd}"; \
  Parameters: "/k ""{app}\octo.exe"" chat"; WorkingDir: "{userdocs}"; \
  Comment: "Start an octo session"

[Code]
const
  EnvKey = 'Environment';

// PathContains reports whether Entry appears as a complete ;-delimited element
// of Path (case-insensitive), so a prefix like C:\octo doesn't match C:\octo2.
function PathContains(const Path, Entry: string): Boolean;
begin
  Result := Pos(';' + Lowercase(Entry) + ';', ';' + Lowercase(Path) + ';') > 0;
end;

procedure AddToPath;
var
  Path, Entry: string;
begin
  Entry := ExpandConstant('{app}');
  if not RegQueryStringValue(HKEY_CURRENT_USER, EnvKey, 'Path', Path) then
    Path := '';
  if PathContains(Path, Entry) then
    exit;
  if (Path <> '') and (Path[Length(Path)] <> ';') then
    Path := Path + ';';
  RegWriteExpandStringValue(HKEY_CURRENT_USER, EnvKey, 'Path', Path + Entry);
end;

// RemoveFromPath strips exactly our {app} element, preserving the case of the
// rest. It works on a ';'-padded copy so the first/last elements are bounded
// like any other, then trims the padding back off.
procedure RemoveFromPath;
var
  Path, Padded, EntryLower: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, EnvKey, 'Path', Path) then
    exit;
  Padded := ';' + Path + ';';
  EntryLower := ';' + Lowercase(ExpandConstant('{app}')) + ';';
  P := Pos(EntryLower, Lowercase(Padded));
  if P = 0 then
    exit;
  // Drop the leading ';' + entry, keeping the trailing ';' as the separator.
  Delete(Padded, P, Length(EntryLower) - 1);
  if Length(Padded) >= 2 then
    Path := Copy(Padded, 2, Length(Padded) - 2)
  else
    Path := '';
  RegWriteExpandStringValue(HKEY_CURRENT_USER, EnvKey, 'Path', Path);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    AddToPath;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RemoveFromPath;
end;
