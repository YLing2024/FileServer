# fetch-tools.ps1 - 下载构建工具链到 .tools\ 目录（免安装、免管理员权限）
# 用法:
#   .\fetch-tools.ps1              # 仅下载 Go 工具链（构建必需）
#   .\fetch-tools.ps1 -WithFfmpeg  # 额外下载 ffmpeg（用于自测服务端视频缩略图）
#
# 说明: 优先使用 Python 下载（更稳定、支持断点续传），无 Python 时回退 PowerShell 下载。
param(
    [switch]$WithFfmpeg
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$tools = Join-Path $root '.tools'
New-Item -ItemType Directory -Path $tools -Force | Out-Null

$python = $null
foreach ($cand in @('python', 'python3', 'py')) {
    $cmd = Get-Command $cand -ErrorAction SilentlyContinue
    if ($cmd) { $python = $cand; break }
}

if ($python) {
    Write-Host "[fetch] 使用 $python 下载（支持断点续传）"
    $dlArgs = @((Join-Path $root 'dl.py'))
    if ($WithFfmpeg) { $dlArgs += '--with-ffmpeg' }
    & $python @dlArgs
    if ($LASTEXITCODE -ne 0) { throw 'Python 下载失败' }
    exit 0
}

# ---------- 回退: PowerShell 下载 ----------
Write-Host '[fetch] 未找到 Python，使用 PowerShell 下载'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$goExe = Join-Path $tools 'go\bin\go.exe'
if (-not (Test-Path $goExe)) {
    Write-Host '[1/2] 获取 Go 最新版本信息...'
    $meta = Invoke-RestMethod 'https://go.dev/dl/?mode=json' -TimeoutSec 30
    $release = $meta | Where-Object { -not $_.prerelease } | Select-Object -First 1
    $file = $release.files | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'archive' } | Select-Object -First 1
    if (-not $file) { throw '未找到 Windows amd64 的 Go 工具链' }
    $url = 'https://dl.google.com/go/' + $file.filename
    $zip = Join-Path $env:TEMP $file.filename
    Write-Host "[1/2] 下载 Go $($release.version)..."
    Invoke-WebRequest $url -OutFile $zip
    Write-Host '[1/2] 解压中...'
    Expand-Archive -Path $zip -DestinationPath $tools -Force
    Remove-Item $zip -Force
    Write-Host "[1/2] Go 工具链就绪: $goExe"
} else {
    Write-Host '[1/2] Go 工具链已存在，跳过'
}

if ($WithFfmpeg) {
    $ffmpegExe = Join-Path $tools 'ffmpeg\ffmpeg.exe'
    if (-not (Test-Path $ffmpegExe)) {
        try {
            Write-Host '[2/2] 下载 ffmpeg...'
            $zip = Join-Path $env:TEMP 'ffmpeg-release-essentials.zip'
            Invoke-WebRequest 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip' -OutFile $zip
            $tmp = Join-Path $tools 'ffmpeg_tmp'
            if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
            Expand-Archive -Path $zip -DestinationPath $tmp -Force
            $ffDir = Join-Path $tools 'ffmpeg'
            New-Item -ItemType Directory -Path $ffDir -Force | Out-Null
            $exe = Get-ChildItem $tmp -Recurse -Filter 'ffmpeg.exe' | Select-Object -First 1
            $pExe = Get-ChildItem $tmp -Recurse -Filter 'ffprobe.exe' | Select-Object -First 1
            if (-not $exe) { throw '解压后未找到 ffmpeg.exe' }
            Copy-Item $exe.FullName (Join-Path $ffDir 'ffmpeg.exe') -Force
            if ($pExe) { Copy-Item $pExe.FullName (Join-Path $ffDir 'ffprobe.exe') -Force }
            Remove-Item $tmp -Recurse -Force
            Remove-Item $zip -Force
            Write-Host "[2/2] ffmpeg 就绪: $ffDir"
        } catch {
            Write-Warning "ffmpeg 下载失败（不影响构建）: $($_.Exception.Message)"
        }
    } else {
        Write-Host '[2/2] ffmpeg 已存在，跳过'
    }
}

Write-Host '全部完成。'
