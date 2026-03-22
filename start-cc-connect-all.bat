@echo off
setlocal
chcp 65001 >nul

echo [INFO] Starting all cc-connect processes...
echo.

set "P1_EXE=C:\Users\Xverse\AppData\Roaming\npm\cc-connect.exe"
set "P1_CFG=C:\Users\Xverse\.cc-connect\config.toml"

set "P2_EXE=D:\ai-github\cc-connect\cc-connect.exe"
set "P2_CFG=D:\ai-github\cc-connect\instances\instance-b\config.toml"

set "P3_EXE=D:\ai-github\cc-connect\cc-connect.exe"
set "P3_CFG=D:\ai-github\cc-connect\instances\instance-a\config.toml"

call :StartOne "cc-connect-global" "%P1_EXE%" "%P1_CFG%"
call :StartOne "cc-connect-instance-b" "%P2_EXE%" "%P2_CFG%"
call :StartOne "cc-connect-instance-a" "%P3_EXE%" "%P3_CFG%"

echo.
echo [DONE] Launch commands issued.
echo [TIP] Use Task Manager or Get-Process cc-connect to verify.
exit /b 0

:StartOne
set "TITLE=%~1"
set "EXE=%~2"
set "CFG=%~3"

if not exist "%EXE%" (
  echo [WARN] EXE not found: %EXE%
  exit /b 0
)
if not exist "%CFG%" (
  echo [WARN] CONFIG not found: %CFG%
  exit /b 0
)

start "%TITLE%" "%EXE%" -config "%CFG%"
echo [OK] %TITLE% => "%EXE%" -config "%CFG%"
exit /b 0

