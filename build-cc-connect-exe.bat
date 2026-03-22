@echo off
setlocal EnableExtensions
chcp 65001 >nul

cd /d "%~dp0"
echo [INFO] Workdir: %CD%
echo [INFO] Build cc-connect.exe ...

where go >nul 2>nul
if errorlevel 1 goto :no_go

go build -o cc-connect.exe ./cmd/cc-connect
if not errorlevel 1 goto :build_ok

echo [WARN] Build failed once. Try to unlock cc-connect.exe and retry...
taskkill /F /IM cc-connect.exe >nul 2>nul
timeout /t 1 /nobreak >nul
go build -o cc-connect.exe ./cmd/cc-connect
if errorlevel 1 goto :build_fail

:build_ok
for %%F in ("cc-connect.exe") do echo [OK] File: %%~fF
for %%F in ("cc-connect.exe") do echo [OK] Size: %%~zF bytes
for %%F in ("cc-connect.exe") do echo [OK] LastWrite: %%~tF

echo [INFO] Smoke check: cc-connect.exe -h
cc-connect.exe -h >nul
if errorlevel 1 goto :smoke_fail

echo [DONE] Build success.
exit /b 0

:no_go
echo [ERROR] Go not found in PATH.
exit /b 1

:build_fail
echo [ERROR] Build failed.
exit /b 1

:smoke_fail
echo [WARN] Smoke check failed (exit=%ERRORLEVEL%).
exit /b %ERRORLEVEL%
