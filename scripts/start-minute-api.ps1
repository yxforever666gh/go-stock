[CmdletBinding()]
param(
    [string]$DataRoot = (Join-Path $PSScriptRoot '../A股历史分钟线数据包'),
    [string]$Cloudflared = 'H:\Program Files (x86)\Cloudflare\cloudflared-windows-amd64.exe',
    [switch]$Stop
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$runDir = 'H:\Download\go-stock-minute-api'
$stopFile = Join-Path $runDir 'stop.request'
if ($Stop) {
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    Set-Content -LiteralPath $stopFile -Value 'stop'
    Write-Host '已请求停止分钟 API 和隧道。'
    return
}

# Windows Job Object makes child processes exit even if the launcher is terminated.
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
public static class MinuteJob {
    [StructLayout(LayoutKind.Sequential)] struct Basic {
        public long ProcessTime, JobTime; public uint Flags;
        public UIntPtr MinWS, MaxWS; public uint ActiveLimit; public UIntPtr Affinity;
        public uint Priority, Scheduling;
    }
    [StructLayout(LayoutKind.Sequential)] struct IO {
        public ulong ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes;
    }
    [StructLayout(LayoutKind.Sequential)] struct Extended {
        public Basic Basic; public IO IO;
        public UIntPtr ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory;
    }
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr CreateJobObject(IntPtr attr, string name);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool SetInformationJobObject(IntPtr job, int type, ref Extended info, uint size);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll")] public static extern bool CloseHandle(IntPtr handle);
    public static IntPtr Create() {
        var job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero) throw new Win32Exception();
        var info = new Extended(); info.Basic.Flags = 0x2000;
        if (!SetInformationJobObject(job, 9, ref info, (uint)Marshal.SizeOf(info))) {
            var error = new Win32Exception(); CloseHandle(job); throw error;
        }
        return job;
    }
    public static void Attach(IntPtr job, IntPtr process) {
        if (!AssignProcessToJobObject(job, process)) throw new Win32Exception();
    }
}
'@

$mutex = [Threading.Mutex]::new($false, 'Local\GoStockMinuteAPI')
$locked = $false
$job = [IntPtr]::Zero
$api = $null
$indexProcess = $null
$relay = $null
$tunnel = $null
try {
    try { $locked = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) { throw '分钟 API 启动脚本已在运行。' }
    foreach ($addressFile in @('public-url.txt', 'mcp-url.txt')) {
        Remove-Item -LiteralPath (Join-Path $runDir $addressFile) -ErrorAction SilentlyContinue
    }
    if (-not (Test-Path -LiteralPath $DataRoot -PathType Container)) { throw "数据根目录不存在：$DataRoot" }
    if (-not (Test-Path -LiteralPath $Cloudflared -PathType Leaf)) { throw "cloudflared 不存在：$Cloudflared" }
    if (Get-NetTCPConnection -State Listen -LocalPort 18080 -ErrorAction SilentlyContinue) { throw '18080 已被占用，请先停止占用程序。' }
    if (Get-NetTCPConnection -State Listen -LocalPort 17844 -ErrorAction SilentlyContinue) { throw '17844 已被占用。' }
    if (Get-NetTCPConnection -State Listen -LocalPort 17845 -ErrorAction SilentlyContinue) { throw '17845 已被占用。' }
    if (-not (Get-NetTCPConnection -State Listen -LocalPort 7890 -ErrorAction SilentlyContinue)) { throw '请先启动本地 7890 代理。' }
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    Remove-Item -LiteralPath $stopFile -ErrorAction SilentlyContinue
    $binary = Join-Path $runDir 'minute-api.exe'
    $relayBinary = Join-Path $runDir 'cloudflare-proxy.exe'
    Push-Location (Join-Path $PSScriptRoot '..')
    try {
        & go build -o $binary ./cmd/minute-api
        if ($LASTEXITCODE -ne 0) { throw '分钟 API 构建失败。' }
        & go build -o $relayBinary ./cmd/cloudflare-proxy
        if ($LASTEXITCODE -ne 0) { throw 'Cloudflare 代理转发程序构建失败。' }
    } finally { Pop-Location }
    $indexDir = Join-Path $runDir 'auction-index'
    Write-Host '正在准备全部竞价索引；首次需扫描全部文件，重启仅更新变化文件。'
    $job = [MinuteJob]::Create()
    $apiArguments = @('-data-root', ('"' + [IO.Path]::GetFullPath($DataRoot) + '"'), '-index-dir', ('"' + $indexDir + '"'))
    $indexLog = Join-Path $runDir 'index.stdout.log'
    $indexProcess = Start-Process -FilePath $binary -ArgumentList ($apiArguments + @('-prepare-data')) -WindowStyle Hidden -PassThru -RedirectStandardOutput $indexLog -RedirectStandardError (Join-Path $runDir 'index.stderr.log')
    [MinuteJob]::Attach($job, $indexProcess.Handle)
    $lastIndexProgress = ''
    while (-not $indexProcess.WaitForExit(500)) {
        if (Test-Path -LiteralPath $stopFile) { return }
        $line = Get-Content -LiteralPath $indexLog -Tail 1 -ErrorAction SilentlyContinue
        if ($line -and $line -ne $lastIndexProgress) { Write-Host $line; $lastIndexProgress = $line }
    }
    if ($indexProcess.ExitCode -ne 0) { throw '索引准备失败，查看 index.stderr.log；已完成文件保留，下次可继续。' }
    if (Test-Path -LiteralPath $stopFile) { return }
    $api = Start-Process -FilePath $binary -ArgumentList $apiArguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $runDir 'api.stdout.log') -RedirectStandardError (Join-Path $runDir 'api.stderr.log')
    [MinuteJob]::Attach($job, $api.Handle)
    $query = '/api/bars?symbol=sh600941&period=1m&start=2026-08-12&end=2026-08-12'
    $ready = $false
    for ($i = 0; $i -lt 20; $i++) {
        if ($api.HasExited) { throw 'API 已退出，请查看 api.stderr.log。' }
        try { $null = Invoke-RestMethod -Uri ('http://127.0.0.1:18080' + $query) -TimeoutSec 3; $ready = $true; break } catch { Start-Sleep -Milliseconds 500 }
    }
    if (-not $ready) { throw '本机 API 检查失败。' }
    $relay = Start-Process -FilePath $relayBinary -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $runDir 'relay.stdout.log') -RedirectStandardError (Join-Path $runDir 'relay.stderr.log')
    [MinuteJob]::Attach($job, $relay.Handle)
    for ($relayWait = 0; $relayWait -lt 20; $relayWait++) {
        if ($relay.HasExited) { throw '代理转发程序已退出。' }
        if (Get-NetTCPConnection -State Listen -LocalPort 17845 -ErrorAction SilentlyContinue) { break }
        Start-Sleep -Milliseconds 100
    }
    Write-Host '正在申请本次临时隧道地址（最多等待60秒）...'
    $null = Invoke-WebRequest -Uri 'http://127.0.0.1:17845/prepare' -Method Post -TimeoutSec 65
    $tunnelLog = Join-Path $runDir 'tunnel.stderr.log'
    # Internal flags route both provisioning and encrypted tunnel traffic through the local 7890 proxy.
    $tunnel = Start-Process -FilePath $Cloudflared -ArgumentList @('tunnel', '--protocol', 'http2', '--edge', '127.0.0.1:17844', '--quick-service', 'http://127.0.0.1:17845', '--http-host-header', '127.0.0.1:18080', '--url', 'http://127.0.0.1:18080') -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $runDir 'tunnel.stdout.log') -RedirectStandardError $tunnelLog
    [MinuteJob]::Attach($job, $tunnel.Handle)
    $publicUrl = $null
    for ($i = 0; $i -lt 120; $i++) {
        if ($tunnel.HasExited -or $relay.HasExited) { throw '隧道或代理转发已退出，请查看运行日志。' }
        $logText = Get-Content -LiteralPath $tunnelLog -Raw -ErrorAction SilentlyContinue
        if ($logText -match 'https://[a-z0-9-]+\.trycloudflare\.com') { $publicUrl = $Matches[0] }
        if ($publicUrl -and $logText -match 'Registered tunnel connection') { break }
        if (Test-Path -LiteralPath $stopFile) { return }
        Start-Sleep -Milliseconds 500
    }
    if (-not $publicUrl -or $logText -notmatch 'Registered tunnel connection') { throw '隧道未能连接，请查看 tunnel.stderr.log。' }
    Set-Content -LiteralPath (Join-Path $runDir 'public-url.txt') -Value $publicUrl
    Set-Content -LiteralPath (Join-Path $runDir 'mcp-url.txt') -Value "$publicUrl/mcp"
    Write-Host "公网地址（隧道连接完成后可用）：$publicUrl"
    Write-Host "ChatGPT MCP 地址（认证选无）：$publicUrl/mcp"
    Write-Host 'MCP 共51个工具：个股、指数、集合竞价与44项蝶梦接口。'
    Write-Host '电脑或隧道重启后，请重新复制本次 MCP 地址并填写到 ChatGPT；旧连接不会自动更新。'
    Write-Host "查询示例：$publicUrl$query"
    Write-Host '按 Ctrl+C 停止，或另开 PowerShell 运行本脚本并加 -Stop。'
    while (-not (Test-Path -LiteralPath $stopFile)) {
        if ($api.HasExited -or $tunnel.HasExited -or $relay.HasExited) { throw 'API、隧道或代理转发已退出，请查看运行日志。' }
        Start-Sleep -Milliseconds 500
    }
} finally {
    if ($null -ne $indexProcess -and -not $indexProcess.HasExited) { $indexProcess.Kill() }
    if ($null -ne $tunnel -and -not $tunnel.HasExited) { $tunnel.Kill() }
    if ($null -ne $relay -and -not $relay.HasExited) { $relay.Kill() }
    if ($null -ne $api -and -not $api.HasExited) { $api.Kill() }
    if ($job -ne [IntPtr]::Zero) { [void][MinuteJob]::CloseHandle($job) }
    if ($locked) {
        Remove-Item -LiteralPath $stopFile -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $runDir 'public-url.txt') -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $runDir 'mcp-url.txt') -ErrorAction SilentlyContinue
        $mutex.ReleaseMutex()
    }
    $mutex.Dispose()
}
