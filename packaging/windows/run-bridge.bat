@echo off
cd /d "%~dp0"
if not exist bridge.json (
  echo bridge.json not found. Copy bridge.example.json to bridge.json and edit it first.
  pause
  exit /b 1
)
openline-bridge.exe -config bridge.json
echo.
echo The bridge stopped. See the messages above.
pause
