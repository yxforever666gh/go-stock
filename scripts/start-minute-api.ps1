param(
    [string]$DataRoot,
    [string]$Cloudflared = 'H:\Program Files (x86)\Cloudflare\cloudflared-windows-amd64.exe',
    [switch]$Stop
)
$ErrorActionPreference = 'Stop'
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$pointerPath = Join-Path $projectDirectory 'runtime\current.json'
$pointer = Get-Content -LiteralPath $pointerPath -Raw | ConvertFrom-Json
if ($pointer.kind -ne 'python') { throw 'Deploy the Python release before starting minute services.' }
& (Join-Path $PSScriptRoot 'release.ps1') inspect --candidate $pointer.releaseDirectory | Out-Null
$env:PYTHONPATH = Join-Path $pointer.releaseDirectory 'src'
$env:PYTHONUTF8 = '1'
$env:PYTHONDONTWRITEBYTECODE = '1'
$env:STOCK_GOD_ROOT = $projectDirectory
$commandArguments = @('-m','stock_god.market.tunnel','--root',$projectDirectory)
if ($Stop) { $commandArguments += '--stop' }
else {
    $commandArguments += @('--cloudflared',$Cloudflared)
    if ($DataRoot) { $commandArguments += @('--data-root',$DataRoot) }
}
& $pointer.pythonExecutable @commandArguments
if ($LASTEXITCODE -ne 0) { throw "Minute service failed (exit $LASTEXITCODE)" }
