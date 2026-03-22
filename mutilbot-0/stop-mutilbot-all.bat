@echo off
setlocal
chcp 65001 >nul

set "PS1=%~dp0stop-mutilbot-all.ps1"
if not exist "%PS1%" (
  echo [ERROR] Script not found: %PS1%
  exit /b 1
)

powershell -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%PS1%"
exit /b %ERRORLEVEL%
