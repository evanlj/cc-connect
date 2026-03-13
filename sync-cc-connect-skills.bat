@echo off
setlocal
chcp 65001 >nul

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

set "SRC=%ROOT%\instances\instance-a\.codex\skills"
set "DST=%ROOT%\instances\.shared-skills\skills"

echo [INFO] Sync cc-connect skills (instance-a -> shared)
echo [INFO] SRC: %SRC%
echo [INFO] DST: %DST%

if not exist "%SRC%" (
  echo [ERROR] Source skills not found: %SRC%
  exit /b 1
)

rem If instance-a skills is already a junction to the shared skills folder,
rem syncing would be redundant and may cause robocopy to hang (same source/target).
powershell -NoLogo -NoProfile -Command ^
  "$src='%SRC%'; $dst='%DST%';" ^
  "try { $it = Get-Item -LiteralPath $src -Force; " ^
  "  if($it.LinkType -eq 'Junction' -and ($it.Target -contains $dst)) { exit 0 } else { exit 3 }" ^
  "} catch { exit 5 }"
if "%ERRORLEVEL%"=="0" (
  echo [SKIP] SRC is already a junction to DST, no sync needed.
  exit /b 0
)

if not exist "%DST%" (
  echo [INFO] Creating shared skills dir: %DST%
  mkdir "%DST%" >nul 2>&1
)

robocopy "%SRC%" "%DST%" /MIR /R:2 /W:1 /NFL /NDL /NP /NJH /NJS >nul

echo [OK] skills synced.
echo [TIP] instance-b / instance-c skills are junctions to shared, so they see updates immediately.
exit /b 0
