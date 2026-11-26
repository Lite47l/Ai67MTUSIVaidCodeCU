@echo off
setlocal
cd /d "%~dp0"
if not exist "data\pulse.db" (
  "bin\mgts-pulse-windows-amd64.exe" -command demo
  if errorlevel 1 goto fail
)
echo Open http://127.0.0.1:8080 in your browser. Stop: Ctrl+C.
"bin\mgts-pulse-windows-amd64.exe"
if errorlevel 1 goto fail
exit /b 0
:fail
echo Startup failed. Read the error above and README.md.
pause
exit /b 1
