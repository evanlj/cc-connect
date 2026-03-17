@echo off
setlocal
chcp 65001 >nul

set "EXE=D:\ai-github\cc-connect\cc-connect.exe"
set "CFG=D:\ai-github\cc-connect\mutilbot\instance-mutilbot5\config.toml"

echo [INFO] Starting instance-mutilbot5 with provider: codez

if not exist "%EXE%" (
  echo [ERROR] EXE not found: %EXE%
  exit /b 1
)
if not exist "%CFG%" (
  echo [ERROR] Config not found: %CFG%
  exit /b 1
)

echo [RUN] "%EXE%" -config "%CFG%"
"%EXE%" -config "%CFG%"
set "RC=%ERRORLEVEL%"
echo [EXIT] instance-mutilbot5(codez) exited with code %RC%
exit /b %RC%