@echo off
REM ===========================================================================
REM  octo - double-click launcher (Windows)
REM
REM  Double-click this file to open PowerShell in this folder, with octo on the
REM  PATH, and start an interactive session. PowerShell stays open after octo
REM  exits (-NoExit), so any error message remains readable and you can re-run
REM  commands such as `octo config` or `octo chat`.
REM
REM  Kept ASCII-only on purpose: a .cmd with non-ASCII text mangles in the
REM  default console code page. octo itself renders any Chinese/UTF-8 output.
REM
REM  First run? Set your provider and API key with:  octo config
REM ===========================================================================
cd /d "%~dp0"
powershell -NoExit -Command "$env:Path = '%~dp0;' + $env:Path; Write-Host 'octo is ready. First run? type:  octo config' -ForegroundColor Cyan; & '%~dp0octo.exe' chat"
