@echo off
rem Builds DrishtiMap.exe (one file, no installer). Needs Go 1.24 or newer.
rem The icon and header logo are cut from NOC-LOGO4.png:  go run ./build/mklogo
rem (writes build\icon.ico and server\web\logo.png; both are kept in the folder).
setlocal
cd /d "%~dp0"
go vet ./... || exit /b 1
go build -trimpath -ldflags "-s -w -H windowsgui" -o DrishtiMap.exe . || exit /b 1
echo Built DrishtiMap.exe
