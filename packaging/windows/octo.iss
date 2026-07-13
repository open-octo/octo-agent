; octo-setup — per-user Windows installer for the Octo desktop app.
;
; Installs the desktop app (octo-desktop.exe) + the octo CLI to
; %LOCALAPPDATA%\Programs\octo, puts that dir on the user PATH (HKCU — no admin,
; no UAC) so `octo` works in a terminal, creates a Start-menu shortcut that
; launches the app, seeds uv into ~/.octo/bin, and opens the app. Per-user is
; deliberate: the install dir stays user-writable so `octo upgrade` (CLI) can
; replace the binary without elevation.
;
; This replaces the old flow (install CLI + autostart `octo serve -d` on login +
; open the browser dashboard) — the desktop app is that UI now, natively. On an
; upgrade from that old installer we remove its leftover login autostart.
;
; Compiled in CI by .github/workflows/release.yml. Defines passed in:
;   AppVersion — the release version, e.g. 0.20.0
;   SourceDir  — the folder holding octo-desktop.exe, octo.exe, LICENSE.txt, and
;                (release builds) uv.exe.
; Compile locally:  ISCC.exe /DAppVersion=0.0.0 /DSourceDir=path\to\bits octo.iss

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceDir
  #define SourceDir "."
#endif
; Target CPU architecture. Defaults keep the amd64 (x64) installer byte-for-byte
; as before; the release passes /DArchAllowed=arm64 /DOutputName=octo-setup-arm64
; for the native Windows-on-ARM build.
#ifndef ArchAllowed
  #define ArchAllowed "x64compatible"
#endif
#ifndef OutputName
  #define OutputName "octo-setup"
#endif

[Setup]
; A stable AppId so re-running a newer installer updates in place rather than
; stacking a second copy. Never change this value.
AppId={{8F2A6B1C-3D4E-4F50-9A6B-7C8D9E0F1A2B}
AppName=Octo
AppVersion={#AppVersion}
AppPublisher=open-octo
AppPublisherURL=https://github.com/open-octo/octo-agent
DefaultDirName={userpf}\octo
DisableProgramGroupPage=yes
DisableDirPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed={#ArchAllowed}
ArchitecturesInstallIn64BitMode={#ArchAllowed}
OutputBaseFilename={#OutputName}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
; Broadcast WM_SETTINGCHANGE after install so Explorer-launched shells pick up
; the new PATH without a logout.
ChangesEnvironment=yes
UninstallDisplayName=Octo {#AppVersion}

[Files]
Source: "{#SourceDir}\octo-desktop.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\octo.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
; uv, staged by `make bundle-tools-windows` into SourceDir before ISCC runs.
; Lands in {app} beside octo-desktop.exe so the app self-provisions it into
; ~/.octo/bin on first launch (bundledUvPath, matching macOS/Linux); the
; postinstall SeedUvToOctoBin also copies it there immediately for CLI-first
; use. skipifsourcedoesntexist lets a compile that only stages the exes succeed.
Source: "{#SourceDir}\uv.exe"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

[Icons]
; Launch the desktop app.
Name: "{userprograms}\Octo"; Filename: "{app}\octo-desktop.exe"; \
  WorkingDir: "{app}"; Comment: "Octo"

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
// rest. Works on a ';'-padded copy so the first/last elements are bounded like
// any other, then trims the padding back off.
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
  Delete(Padded, P, Length(EntryLower) - 1);
  if Length(Padded) >= 2 then
    Path := Copy(Padded, 2, Length(Padded) - 2)
  else
    Path := '';
  RegWriteExpandStringValue(HKEY_CURRENT_USER, EnvKey, 'Path', Path);
end;

// WriteDefaultConfigIfMissing seeds ~/.octo/config.yml with workspace_dir: auto
// on a genuinely fresh install; never overwrites an existing user config.
procedure WriteDefaultConfigIfMissing;
var
  ConfigDir, ConfigPath: string;
begin
  ConfigDir := ExpandConstant('{%USERPROFILE}') + '\.octo';
  ConfigPath := ConfigDir + '\config.yml';
  if FileExists(ConfigPath) then
    exit;
  if not DirExists(ConfigDir) then
    if not CreateDir(ConfigDir) then
      exit;
  SaveStringToFile(ConfigPath, 'workspace_dir: auto' + #13#10, False);
end;

// SeedUvToOctoBin copies the bundled uv into ~/.octo/bin so the CLI has Python
// tooling immediately (the app also self-provisions this on first launch).
procedure SeedUvToOctoBin;
var
  binDir: string;
begin
  if not FileExists(ExpandConstant('{app}\uv.exe')) then
    exit;
  binDir := ExpandConstant('{%USERPROFILE}') + '\.octo\bin';
  ForceDirectories(binDir);
  FileCopy(ExpandConstant('{app}\uv.exe'), binDir + '\uv.exe', False);
end;

// LaunchApp opens the desktop app after install (detached; installer returns).
procedure LaunchApp;
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{app}\octo-desktop.exe'), '', ExpandConstant('{app}'),
       SW_SHOWNORMAL, ewNoWait, ResultCode);
end;

// CommandFound reports whether `where <name>` resolves (CLI on PATH).
function CommandFound(const Name: string): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(ExpandConstant('{cmd}'), '/C where ' + Name + ' >nul 2>&1', '',
                 SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

// EnsurePowerShell7 best-effort installs PowerShell 7 via winget when missing.
// octo runs hook scripts and the terminal tool through pwsh (7+) when present,
// falling back to Windows PowerShell 5.1 otherwise, so a present pwsh is a
// better default. Every skip/failure path is a no-op — octo keeps using 5.1.
procedure EnsurePowerShell7;
var
  ResultCode: Integer;
begin
  if CommandFound('pwsh') then
    exit;
  if not CommandFound('winget') then
    exit;
  if MsgBox(
       'octo works best with PowerShell 7, which is not installed yet.' + #13#10#13#10 +
       'Would you like to install it now? Windows will show a blue "User Account ' +
       'Control" window asking for permission — this is normal and safe; just ' +
       'choose "Yes" there.' + #13#10#13#10 +
       'Choose "No" to skip — octo will still work using the built-in Windows ' +
       'PowerShell.' + #13#10#13#10 +
       'octo 建议使用 PowerShell 7。是否现在安装？期间 Windows 会弹出蓝色的' +
       '"用户账户控制"授权窗口，这是正常且安全的，点"是"即可。选"否"可跳过，' +
       'octo 仍可正常使用系统自带的 PowerShell。',
       mbConfirmation, MB_YESNO) <> IDYES then
    exit;
  WizardForm.StatusLabel.Caption := 'Installing PowerShell 7 (recommended)...';
  Exec(ExpandConstant('{cmd}'),
       '/C winget install --id Microsoft.PowerShell --source winget --silent ' +
       '--accept-package-agreements --accept-source-agreements', '',
       SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

// CleanupLegacyAutostart removes the pre-desktop installer's login autostart:
// the HKCU Run "octo" value and the octo-autostart.vbs it launched. The desktop
// app replaces that surrogate-GUI flow, so the daemon must not keep autostarting.
procedure CleanupLegacyAutostart;
begin
  RegDeleteValue(HKEY_CURRENT_USER,
    'Software\Microsoft\Windows\CurrentVersion\Run', 'octo');
  DeleteFile(ExpandConstant('{app}\octo-autostart.vbs'));
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
begin
  if CurStep = ssInstall then
  begin
    // Stop a running serve daemon (from an old install or `octo serve` by hand)
    // and close a running desktop app, so an in-place upgrade can replace the
    // in-use images. All best-effort no-ops on a first install.
    if FileExists(ExpandConstant('{app}\octo.exe')) then
      Exec(ExpandConstant('{app}\octo.exe'), 'serve --stop', '',
           SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Exec(ExpandConstant('{cmd}'), '/C taskkill /IM octo-desktop.exe /F >nul 2>&1',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    CleanupLegacyAutostart;
  end;

  if CurStep = ssPostInstall then
  begin
    AddToPath;
    EnsurePowerShell7;
    WriteDefaultConfigIfMissing;
    SeedUvToOctoBin;
    LaunchApp;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    // Close a running app / stop any serve daemon so their images aren't locked
    // when the uninstaller removes them.
    Exec(ExpandConstant('{cmd}'), '/C taskkill /IM octo-desktop.exe /F >nul 2>&1',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if FileExists(ExpandConstant('{app}\octo.exe')) then
      Exec(ExpandConstant('{app}\octo.exe'), 'serve --stop', '',
           SW_HIDE, ewWaitUntilTerminated, ResultCode);
    RemoveFromPath;
    CleanupLegacyAutostart;
  end;
end;
