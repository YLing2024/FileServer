@echo off
rem ============================================================
rem  FileServer build script
rem  First run downloads the Go toolchain to .tools\ (no install)
rem  Steps: go vet  ->  go build   (等价于 Makefile 的 vet + build)
rem ============================================================
setlocal
cd /d "%~dp0"

set "GOEXE=%CD%\.tools\go\bin\go.exe"

if not exist "%GOEXE%" (
    echo [build] Go toolchain not found, downloading...
    powershell -NoProfile -ExecutionPolicy Bypass -File fetch-tools.ps1
    if errorlevel 1 (
        echo [build] toolchain download failed, check network and retry
        exit /b 1
    )
)

echo [build] vet...
"%GOEXE%" vet ./...
if errorlevel 1 goto :fail

if not exist dist mkdir dist

echo [build] compiling FileServer.exe ...
"%GOEXE%" build -trimpath -ldflags "-s -w" -o dist\FileServer.exe ./cmd/fileserver
if errorlevel 1 goto :fail

echo.
echo [build] OK: dist\FileServer.exe
echo [build] double-click dist\FileServer.exe to start the LAN file server
echo [build] release with version: release.ps1 -Version x.y.z
exit /b 0

:fail
echo [build] FAILED, see errors above
exit /b 1
