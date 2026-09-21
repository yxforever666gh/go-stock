@echo off
setlocal

cd /d "%~dp0"

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\source-run.ps1" open -ResearchCenter %*

if errorlevel 1 (
  echo.
  echo go-stock source runtime failed to start. Check runtime\source-dev\backend.err.log and frontend.err.log.
  pause
  exit /b 1
)

exit /b 0
