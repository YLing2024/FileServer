# release.ps1 - 打包发布版本
# 产出:
#   dist\FileServer-lite.zip   单 exe（约 7MB）
#   dist\FileServer-full.zip   单 exe + ffmpeg 组件（完整视频缩略图支持）
param(
    [string]$Version = "1.0.0"
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$dist = Join-Path $root 'dist'
$exe = Join-Path $dist 'FileServer.exe'

if (-not (Test-Path $exe)) {
    Write-Error "未找到 $exe ，请先运行 build.bat"
}

$ffSrc = Join-Path $root '.tools\ffmpeg'
$tmp = Join-Path $dist ".release-tmp"
if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

# ---- lite ----
Copy-Item $exe (Join-Path $tmp 'FileServer.exe')
$liteZip = Join-Path $dist "FileServer-lite-$Version.zip"
Compress-Archive -Path (Join-Path $tmp '*') -DestinationPath $liteZip -Force
Write-Host "lite: $liteZip"

# ---- full ----
if (Test-Path (Join-Path $ffSrc 'ffmpeg.exe')) {
    $ffDir = Join-Path $tmp 'ffmpeg'
    New-Item -ItemType Directory -Path $ffDir -Force | Out-Null
    Copy-Item (Join-Path $ffSrc 'ffmpeg.exe') $ffDir
    Copy-Item (Join-Path $ffSrc 'ffprobe.exe') $ffDir
    $fullZip = Join-Path $dist "FileServer-full-$Version.zip"
    Compress-Archive -Path (Join-Path $tmp '*') -DestinationPath $fullZip -Force
    Write-Host "full: $fullZip"
} else {
    Write-Host "未找到 .tools\ffmpeg，跳过 full 包"
}
Remove-Item $tmp -Recurse -Force
Write-Host '完成。'
