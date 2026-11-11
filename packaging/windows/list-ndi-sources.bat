@echo off
cd /d "%~dp0"
echo Looking for NDI sources on the network...
openline-bridge.exe -list
echo.
pause
