# Pocket-Omega 启动脚本
# 用法: .\run.ps1 [--build]

param(
    [switch]$Build
)

$ErrorActionPreference = "Stop"
$exe = "$PSScriptRoot\bin\omega.exe"

# 有 --build 参数或 exe 不存在时自动编译
if ($Build -or !(Test-Path $exe)) {
    Write-Host "🔨 Building..." -ForegroundColor Cyan
    go build -o $exe ./cmd/omega
    if ($LASTEXITCODE -ne 0) {
        Write-Host "❌ Build failed" -ForegroundColor Red
        exit 1
    }
    Write-Host "✅ Build complete" -ForegroundColor Green
}

Write-Host ""
& $exe
