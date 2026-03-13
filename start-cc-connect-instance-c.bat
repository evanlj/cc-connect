@echo off
setlocal
chcp 65001 >nul

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"
set "EXE=%ROOT%\cc-connect.exe"
set "CFG=%ROOT%\instances\instance-c\config.toml"

set "RESTART=0"
if /i "%~1"=="--restart" set "RESTART=1"
if /i "%~1"=="-r" set "RESTART=1"
if /i "%~2"=="--restart" set "RESTART=1"
if /i "%~2"=="-r" set "RESTART=1"

echo [INFO] Starting instance-c
if "%RESTART%"=="1" echo [INFO] restart enabled: will stop existing process first

if not exist "%EXE%" (
  echo [ERROR] EXE not found: %EXE%
  exit /b 1
)
if not exist "%CFG%" (
  echo [ERROR] Config not found: %CFG%
  exit /b 1
)

set "RUN_DIR=%ROOT%\instances\instance-c\data\run"
set "PID_FILE=%RUN_DIR%\cc-connect.pid"

if not exist "%RUN_DIR%" (
  mkdir "%RUN_DIR%" >nul 2>nul
)

REM Stop by pid file (no WMI needed)
if "%RESTART%"=="1" (
  if exist "%PID_FILE%" (
    set "PID="
    set /p PID=<"%PID_FILE%"
    if not "%PID%"=="" (
      powershell -NoLogo -NoProfile -Command ^
        "$pid=%PID%; $p=Get-Process -Id $pid -ErrorAction SilentlyContinue; if($p){ Stop-Process -Id $pid -Force; Write-Output ('[STOP] PID=' + $pid) } else { Write-Output ('[MISS] PID not running=' + $pid) }"
    ) else (
      echo [WARN] empty pid file: %PID_FILE%
    )
    del "%PID_FILE%" >nul 2>nul
  )
)

REM Skip if already running
if "%RESTART%"=="0" (
  if exist "%PID_FILE%" (
    set "PID="
    set /p PID=<"%PID_FILE%"
    powershell -NoLogo -NoProfile -Command ^
      "$pid=%PID%; $p=Get-Process -Id $pid -ErrorAction SilentlyContinue; if($p){ Write-Output ('[SKIP] already running PID=' + $pid); exit 2 } else { exit 0 }"
    if errorlevel 2 exit /b 0
  )
)

REM Start hidden and record pid
set "PID="
for /f "usebackq delims=" %%p in (`powershell -NoLogo -NoProfile -Command "$exe='%EXE%'; $cfg='%CFG%'; $proc=Start-Process -FilePath $exe -ArgumentList @('-config',$cfg) -PassThru -WindowStyle Hidden; $proc.Id"`) do set "PID=%%p"

if "%PID%"=="" (
  echo [ERROR] failed to start instance-c
  exit /b 1
)

echo %PID%>"%PID_FILE%"
echo [OK] started instance-c PID=%PID% => "%EXE%" -config "%CFG%"
echo [LOG] %ROOT%\instances\instance-c\data\run\cc-connect.log
exit /b 0
