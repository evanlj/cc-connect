@echo off
setlocal
chcp 65001 >nul

echo [INFO] Starting all mutilbot instances (provider=codez)...
start "mutilbot1" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot1.bat"
start "mutilbot2" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot2.bat"
start "mutilbot3" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot3.bat"
start "mutilbot4" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot4.bat"
start "mutilbot5" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot5.bat"

echo [DONE] launch commands sent.
exit /b 0