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
;   SourceDir  — the folder holding octo.exe, LICENSE.txt, and (release builds
;                only) uv.exe, fetched from astral-sh/uv's GitHub releases at
;                build time (see the Makefile's bundle-tools-windows target) —
;                not checked into this repo — so a plain `git clone` has no
;                uv.exe to find; that's expected, [Files] skips it
;                (skipifsourcedoesntexist) when SourceDir doesn't have it.
;                only uv ships today.
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
AppPublisher=open-octo
AppPublisherURL=https://github.com/open-octo/octo-agent
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
; uv, staged by `make bundle-tools-windows` (see the Makefile) into the same
; SourceDir before ISCC runs. Lands in {%USERPROFILE}\.octo\bin — the same
; per-user, no-admin directory octo's own rgembed cache uses (~/.octo/bin) —
; NOT {app}, so skill scripts can find it via PATH injection
; (internal/tools/sandbox.go) without polluting the user's real PATH.
; skipifsourcedoesntexist lets a local/CI compile that only stages octo.exe
; (windows-installer-check.yml, or `ISCC.exe /DSourceDir=...` by hand) still
; succeed — the release pipeline is the only place this file is expected.
Source: "{#SourceDir}\uv.exe"; DestDir: "{%USERPROFILE}\.octo\bin"; Flags: ignoreversion skipifsourcedoesntexist

[Icons]
; Open a console with octo started. Invoked by full path so it works even
; before the new PATH propagates.
Name: "{userprograms}\octo"; Filename: "{cmd}"; \
  Parameters: "/k ""{app}\octo.exe"" chat"; WorkingDir: "{userdocs}"; \
  Comment: "Start an octo session"

[Registry]
; Start the background server on each login. Per-user (HKCU — no admin, no
; UAC), matching the per-user install. The value runs a hidden .vbs launcher
; (written in [Code]) so `octo serve -d` starts with no console window on the
; desktop. uninsdeletevalue removes the entry on uninstall.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
  ValueType: string; ValueName: "octo"; \
  ValueData: "wscript.exe //B //Nologo ""{app}\octo-autostart.vbs"""; \
  Flags: uninsdeletevalue

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

// WriteDefaultConfigIfMissing seeds ~/.octo/config.yml with workspace_dir:
// auto on a genuinely fresh install (no config.yml at all yet — not even one
// with just an access key, which octo itself writes on first `serve` start).
// This must run before LaunchAndOpenDashboard's `octo serve -d`, or that
// first start would write an access-key-only config.yml first and this step
// would then correctly no-op, leaving the new-web-session default unset.
// An upgrade/reinstall over an existing config.yml is always a no-op here —
// the installer must never overwrite a user's existing settings.
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

// LaunchAndOpenDashboard starts the background server and opens the onboarding
// page. `octo serve -d` blocks until the server is accepting connections (or it
// times out), so the browser opens against a live port rather than racing the
// bind. The dashboard binds 127.0.0.1, which is exempt from access-key auth, so
// the page loads without a key and goes straight into first-run onboarding.
procedure LaunchAndOpenDashboard;
var
  ResultCode: Integer;
begin
  // Start the daemon and wait for it to return (ready or timed out). Hidden so
  // no console window flashes; the daemon itself is detached and outlives this.
  if not Exec(ExpandConstant('{app}\octo.exe'), 'serve -d', '',
              SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    exit;
  // Open the onboarding page regardless of the exact exit code — if the server
  // is up, this lands on onboarding; if it never bound, the browser shows a
  // connection error the user can retry, which is no worse than not opening.
  ShellExec('open', 'http://127.0.0.1:8088', '', '', SW_SHOWNORMAL,
            ewNoWait, ResultCode);
end;

// WriteAutostartScript drops a tiny VBScript beside octo.exe that launches
// `octo serve -d` with a hidden window (WScript.Shell.Run window style 0). The
// HKCU Run entry invokes it on each login, so the daemon returns after a reboot
// with no console window flashing on the desktop. `octo serve -d` refuses to
// start a second daemon, so a login while one is already running is a no-op.
// The exe path is baked in (quoted) to survive a username with spaces.
procedure WriteAutostartScript;
var
  Vbs: string;
begin
  Vbs :=
    'Set sh = CreateObject("WScript.Shell")' + #13#10 +
    'exe = "' + ExpandConstant('{app}\octo.exe') + '"' + #13#10 +
    'sh.Run """" & exe & """ serve -d", 0, False' + #13#10;
  SaveStringToFile(ExpandConstant('{app}\octo-autostart.vbs'), Vbs, False);
end;

// CommandFound reports whether `where <name>` resolves, i.e. the CLI is on
// PATH. Run through cmd so redirection hides its output and no window flashes.
function CommandFound(const Name: string): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(ExpandConstant('{cmd}'), '/C where ' + Name + ' >nul 2>&1', '',
                 SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

// EnsurePowerShell7 best-effort installs PowerShell 7 via winget when it is
// missing. octo runs hook scripts and the terminal tool through pwsh (7+) when
// present, falling back to the clumsier Windows PowerShell 5.1 otherwise, so a
// present pwsh is a better default. Every skip/failure path — pwsh already
// there, no winget (older Windows / enterprise policy), user declined, no
// network — is a no-op: octo simply keeps using 5.1.
//
// The winget package is a machine-wide MSI, so Windows raises a UAC prompt.
// Rather than let that prompt appear out of nowhere and alarm a non-technical
// user, we explain up front what is about to happen and that the Windows
// permission dialog is expected, and let them decline. This is the only
// elevation the otherwise UAC-free per-user installer ever asks for.
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
  // Ignore ResultCode: a failed or declined install just leaves octo on 5.1.
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
begin
  // Before overwriting files on an in-place upgrade, stop a running daemon —
  // install-time launch and the login Run entry mean octo.exe is very likely
  // running, and Windows can't replace an in-use image. Harmless no-op on a
  // first install (no octo.exe yet). ssPostInstall then starts the new build.
  if CurStep = ssInstall then
  begin
    if FileExists(ExpandConstant('{app}\octo.exe')) then
      Exec(ExpandConstant('{app}\octo.exe'), 'serve --stop', '',
           SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;

  if CurStep = ssPostInstall then
  begin
    AddToPath;
    EnsurePowerShell7;
    WriteAutostartScript;
    WriteDefaultConfigIfMissing;
    LaunchAndOpenDashboard;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    // Stop the running daemon first so its octo.exe isn't locked when the
    // uninstaller removes it (Windows can't delete an in-use image).
    Exec(ExpandConstant('{app}\octo.exe'), 'serve --stop', '',
         SW_HIDE, ewWaitUntilTerminated, ResultCode);
    RemoveFromPath;
    // Remove the launcher we wrote at install time; it isn't in the [Files]
    // log, so the uninstaller won't clean it up on its own.
    DeleteFile(ExpandConstant('{app}\octo-autostart.vbs'));
  end;
end;
