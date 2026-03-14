@echo off
setlocal EnableDelayedExpansion
chcp 65001 >nul

if "%~1"=="" goto :usage

set "TARGET=%~1"
set "OUT=%~2"

set "SCRIPT=%~dp0extract-replies.ps1"
if not exist "%SCRIPT%" (
  echo [ERROR] Missing script: %SCRIPT%
  exit /b 1
)

REM 判断是目录还是文件
if exist "%TARGET%\NUL" (
  if not "%OUT%"=="" (
    echo [WARN] Directory mode ignores output arg.
  )
  pwsh -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" -Target "%TARGET%"
  exit /b %ERRORLEVEL%
)

if not exist "%TARGET%" (
  echo [ERROR] File not found: %TARGET%
  exit /b 1
)

if "%OUT%"=="" (
  pwsh -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" -Target "%TARGET%"
) else (
  pwsh -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" -Target "%TARGET%" -Out "%OUT%"
)
exit /b %ERRORLEVEL%

:usage
echo Usage:
echo   1) Single file:
echo      extract-replies.bat "D:\...\file.jsonl"
echo   2) Single file + custom output:
echo      extract-replies.bat "D:\...\file.jsonl" "D:\...\file.replies.txt"
echo   3) Directory (process all *.jsonl):
echo      extract-replies.bat "D:\...\2026-03-12"
echo.
echo Note:
echo   The output .replies.txt starts with a header (lines begin with '#')
echo   including: ts / model / mode / prompt preview.
exit /b 1
