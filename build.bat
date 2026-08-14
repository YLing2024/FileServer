@echo off
rem ============================================================
rem  FileServer 一键构建脚本
rem  首次运行会自动下载 Go 工具链到 .tools\（免安装）
rem ============================================================
setlocal
cd /d "%~dp0"

set "GOEXE=%CD%\.tools\go\bin\go.exe"

if not exist "%GOEXE%" (
    echo [构建] 未找到本地 Go 工具链，正在下载...
    powershell -NoProfile -ExecutionPolicy Bypass -File fetch-tools.ps1
    if errorlevel 1 (
        echo [构建] 工具链下载失败，请检查网络后重试
        exit /b 1
    )
)

echo [构建] 整理依赖...
"%GOEXE%" mod tidy
if errorlevel 1 goto :fail

echo [构建] 编译 FileServer.exe ...
"%GOEXE%" build -trimpath -ldflags "-s -w" -o dist\FileServer.exe .
if errorlevel 1 goto :fail

echo.
echo [构建] 成功: dist\FileServer.exe
echo [构建] 双击 dist\FileServer.exe 即可启动局域网文件服务器
exit /b 0

:fail
echo [构建] 失败，请检查上方错误信息
exit /b 1
