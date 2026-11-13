@echo off
cd /d "%~dp0"
echo Sound card outputs on this PC:
echo.
openline-bridge.exe -devices
echo.
pause
