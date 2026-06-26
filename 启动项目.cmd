@echo off
setlocal

cd /d "%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\restart.ps1" open

if errorlevel 1 (
  echo.
  echo go-stock failed to start. Check logs\monitor.windows.log for details.
  pause
  exit /b 1
)

exit /b 0
