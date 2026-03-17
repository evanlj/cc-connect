@echo off
setlocal
chcp 65001 >nul

echo [INFO] Starting all mutilbot instances (provider=rightcode)...
start "mutilbot1" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot1-rightcode.bat"
start "mutilbot2" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot2-rightcode.bat"
start "mutilbot3" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot3-rightcode.bat"
start "mutilbot4" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot4-rightcode.bat"
start "mutilbot5" cmd /k "call D:\ai-github\cc-connect\mutilbot\start-mutilbot5-rightcode.bat"

echo [DONE] launch commands sent.
exit /b 0