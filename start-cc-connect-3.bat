@echo off
setlocal
chcp 65001 >nul

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"
set "EXE=%ROOT%\cc-connect.exe"

echo [INFO] Start 3 cc-connect instances (a/b/c)
echo [INFO] Root: %ROOT%

if not exist "%EXE%" (
  echo [ERROR] EXE not found: %EXE%
  exit /b 1
)

echo.
echo [STEP] Sync skills (instance-a -> shared) before start
call "%ROOT%\\sync-cc-connect-skills.bat"

call :StartOne "instance-a" "%ROOT%\instances\instance-a\config.toml"
call :StartOne "instance-b" "%ROOT%\instances\instance-b\config.toml"
call :StartOne "instance-c" "%ROOT%\instances\instance-c\config.toml"

echo.
echo [DONE] start commands processed.
echo [TIP] check process: powershell -NoLogo -NoProfile -Command "Get-Process cc-connect -ErrorAction SilentlyContinue"
exit /b 0

:StartOne
set "NAME=%~1"
set "CFG=%~2"

if not exist "%CFG%" (
  echo [WARN] config not found: %CFG%
  exit /b 0
)

powershell -NoLogo -NoProfile -Command ^
  "$cfg='%CFG%';" ^
  "$p=Get-CimInstance Win32_Process | Where-Object { $_.Name -ieq 'cc-connect.exe' -and $_.CommandLine -like ('* -config ' + $cfg + '*') };" ^
  "if($p){ Write-Output ('[SKIP] already running: %NAME% PID=' + ($p | Select-Object -First 1 -ExpandProperty ProcessId)); exit 2 } else { exit 0 }"

if "%ERRORLEVEL%"=="2" exit /b 0
if not "%ERRORLEVEL%"=="0" (
  echo [WARN] failed to check existing process: %NAME%
  exit /b 0
)

start "cc-connect-%NAME%" "%EXE%" -config "%CFG%"
echo [OK] started: %NAME% => "%EXE%" -config "%CFG%"
exit /b 0
