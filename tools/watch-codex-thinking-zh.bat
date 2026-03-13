@echo off
setlocal EnableDelayedExpansion
chcp 65001 >nul

set "SCRIPT=%~dp0watch-codex-thinking-zh.ps1"
if not exist "%SCRIPT%" (
  echo [ERROR] Missing script: %SCRIPT%
  exit /b 1
)

if "%~1"=="" goto :usage

REM Pass-through wrapper. Use PowerShell-style args, e.g.:
REM   watch-codex-thinking-zh.bat -Instance instance-c
REM   watch-codex-thinking-zh.bat -Path "D:\...\2026-03-12"
pwsh -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" %*
exit /b %ERRORLEVEL%

:usage
echo Usage:
echo   1) Watch latest trace for an instance:
echo      watch-codex-thinking-zh.bat -Instance instance-c
echo.
echo   2) Watch a date folder (auto follow latest *.jsonl):
echo      watch-codex-thinking-zh.bat -Path "D:\ai-github\cc-connect\instances\instance-c\data\traces\codex\2026-03-12"
echo.
echo   3) Watch a specific jsonl file:
echo      watch-codex-thinking-zh.bat -Path "D:\...\xxx_turn_1.jsonl"
echo.
echo Tips:
echo   - Add -FromStart to replay from file beginning.
echo   - Add -AgentMessage to also show final assistant messages.
exit /b 1

