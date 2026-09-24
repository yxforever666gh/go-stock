@echo off
setlocal

cd /d "%~dp0"

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\restart.ps1" open %*

if errorlevel 1 (
  echo.
  echo Stock God failed to start. Check runtime\logs.
  pause
  exit /b 1
)

exit /b 0
