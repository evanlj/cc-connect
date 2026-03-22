@echo off
setlocal
chcp 65001 >nul

set "EXE=D:\ai-github\cc-connect\cc-connect.exe"
set "CFG=D:\ai-github\cc-connect\instances\instance-a\rigthcode.toml"

echo [INFO] Starting instance-a with provider: rightcode

if not exist "%EXE%" (
  echo [ERROR] EXE not found: %EXE%
  exit /b 1
)
if not exist "%CFG%" (
  echo [ERROR] Config not found: %CFG%
  exit /b 1
)

powershell -NoLogo -NoProfile -Command ^
  "$cfg='%CFG%';" ^
  "$p=Get-CimInstance Win32_Process | Where-Object { $_.Name -ieq 'cc-connect.exe' -and $_.CommandLine -like ('* -config ' + $cfg + '*') };" ^
  "if($p){ Write-Output ('[SKIP] already running PID=' + ($p | Select-Object -First 1 -ExpandProperty ProcessId)); exit 2 } else { exit 0 }"

if errorlevel 2 exit /b 0
if errorlevel 1 exit /b 1

echo [RUN] "%EXE%" -config "%CFG%"
echo [TIP] Running in current window. Press Ctrl+C to stop.
"%EXE%" -config "%CFG%"
set "RC=%ERRORLEVEL%"
echo [EXIT] instance-a(rightcode) exited with code %RC%
exit /b %RC%

