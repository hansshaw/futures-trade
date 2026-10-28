$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This validation must run on Windows.' }

$installer = @(Get-ChildItem "$PSScriptRoot/../release/*-windows-x64-setup.exe")
if ($installer.Count -ne 1) { throw 'Expected exactly one Windows x64 installer.' }
$installDir = Join-Path ([IO.Path]::GetTempPath()) ("CornWatch Test " + [guid]::NewGuid().ToString('N'))
$app = Join-Path $installDir 'CornWatch.exe'

try {
    # NSIS requires /D to be the final, unquoted argument, including spaces.
    $setup = Start-Process $installer[0].FullName -ArgumentList "/S /D=$installDir" -Wait -PassThru
    if ($setup.ExitCode -ne 0) { throw "Installer failed: $($setup.ExitCode)" }
    foreach ($file in @($app, (Join-Path $installDir 'resources/bin/cornwatch.exe'), (Join-Path $installDir 'resources/ui/index.html'))) {
        if (-not (Test-Path $file)) { throw "Missing installed file: $file" }
    }
    node "$PSScriptRoot/electron-smoke.mjs" $app
    if ($LASTEXITCODE -ne 0) { throw "Installed desktop smoke test failed: $LASTEXITCODE" }
} finally {
    $uninstaller = Join-Path $installDir 'Uninstall CornWatch.exe'
    if (Test-Path $uninstaller) {
        $uninstall = Start-Process $uninstaller -ArgumentList '/S' -Wait -PassThru
        if ($uninstall.ExitCode -ne 0) { throw "Uninstaller failed: $($uninstall.ExitCode)" }
        for ($attempt = 0; $attempt -lt 60 -and (Test-Path $app); $attempt++) { Start-Sleep -Seconds 1 }
        if (Test-Path $app) { throw 'Uninstaller did not remove the application.' }
    }
    if (Test-Path $installDir) { Remove-Item $installDir -Recurse -Force }
}
Write-Output 'PASS: Windows x64 silent install, installed desktop workflows, restart persistence, and uninstall.'
