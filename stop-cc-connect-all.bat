@echo off
setlocal
chcp 65001 >nul

echo [INFO] Stopping cc-connect instances (PID-file based)...
echo.

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

REM Default config (~/.cc-connect)
set "PID0=%USERPROFILE%\.cc-connect\run\cc-connect.pid"

REM Repo instances
set "PID_A=%ROOT%\instances\instance-a\data\run\cc-connect.pid"
set "PID_B=%ROOT%\instances\instance-b\data\run\cc-connect.pid"
set "PID_C=%ROOT%\instances\instance-c\data\run\cc-connect.pid"
set "PID_CT=%ROOT%\instances\instance-c-translator\data\run\cc-connect.pid"

call :StopByPidFile "%PID0%"
call :StopByPidFile "%PID_A%"
call :StopByPidFile "%PID_B%"
call :StopByPidFile "%PID_C%"
call :StopByPidFile "%PID_CT%"

echo.
echo [DONE] Stop commands issued.
echo [TIP] Run: powershell -Command "Get-Process cc-connect -ErrorAction SilentlyContinue"
exit /b 0

:StopByPidFile
set "PIDFILE=%~1"
if not exist "%PIDFILE%" (
  echo [MISS] PID file not found: %PIDFILE%
  exit /b 0
)

set /p PID=<"%PIDFILE%"
if "%PID%"=="" (
  echo [WARN] empty PID in file: %PIDFILE%
  del "%PIDFILE%" >nul 2>nul
  exit /b 0
)

powershell -NoLogo -NoProfile -Command ^
  "$pid=%PID%; $p=Get-Process -Id $pid -ErrorAction SilentlyContinue; if($p){ Stop-Process -Id $pid -Force; Write-Output ('[OK] stopped PID=' + $pid) } else { Write-Output ('[MISS] PID not running=' + $pid) }"

del "%PIDFILE%" >nul 2>nul
exit /b 0
