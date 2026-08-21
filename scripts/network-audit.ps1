param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$AuditArgs
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ToolDir = Join-Path (Split-Path -Parent $ScriptDir) "tools\network-audit"

Push-Location $ToolDir
try {
    & go run . @AuditArgs
    if ($LASTEXITCODE -ne 0) { throw "network-audit exited with code $LASTEXITCODE" }
} finally {
    Pop-Location
}
