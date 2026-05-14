# Run P3 STATE-01..03 unit tests + (re)generate canonical Alice 100/40 vectors.
#
# Usage from anywhere:
#   pwsh -File p3/script-test/run.ps1
#
# Exits non-zero if any step fails so CI can pick it up.

$ErrorActionPreference = 'Stop'

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..')
Push-Location $repoRoot
try {
    Write-Host '==> go test ./internal/state/... ./pkg/...'
    & go test ./internal/state/... ./pkg/...
    if ($LASTEXITCODE -ne 0) { throw "go test failed (exit $LASTEXITCODE)" }

    Write-Host '==> go run ./p3/script-test/gen_state_vectors'
    & go run ./p3/script-test/gen_state_vectors
    if ($LASTEXITCODE -ne 0) { throw "gen_state_vectors failed (exit $LASTEXITCODE)" }

    Write-Host '==> done'
} finally {
    Pop-Location
}
