# Installs the tello CLI from GitHub Releases on Windows (Windows PowerShell 5.1 or later).
#
#   irm https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.ps1 | iex
#
# Environment:
#   TELLO_VERSION            version to install, e.g. 0.1.0 (default: latest release)
#   TELLO_INSTALL_DIR        directory for tello.exe (default: %LOCALAPPDATA%\Programs\tello)
#   TELLO_DOWNLOAD_BASE_URL  releases URL (default: https://github.com/tello-ai/tello-cli/releases)
#                            Archives are fetched from <base>/download/v<version>/.

# A child scope keeps `irm | iex` from leaving variables or preference changes in
# the caller's session. Errors are thrown, never `exit`, which would close it.
& {
    $ErrorActionPreference = 'Stop'
    # The progress bar makes Invoke-WebRequest very slow on Windows PowerShell 5.1.
    $ProgressPreference = 'SilentlyContinue'
    # GitHub requires TLS 1.2, which Windows PowerShell 5.1 does not always enable.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    function Get-Download($Url, $Path) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Path
        } catch {
            throw "tello installer: could not download ${Url}: $($_.Exception.Message)"
        }
    }

    # PROCESSOR_ARCHITEW6432 is set when 32-bit PowerShell runs on 64-bit Windows.
    $cpu = $env:PROCESSOR_ARCHITEW6432
    if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
    if ($cpu -eq 'AMD64') {
        $arch = 'amd64'
    } elseif ($cpu -eq 'ARM64') {
        $arch = 'arm64'
    } else {
        throw "tello installer: unsupported architecture '$cpu'; prebuilt binaries exist for AMD64 and ARM64"
    }

    $baseUrl = $env:TELLO_DOWNLOAD_BASE_URL
    if (-not $baseUrl) { $baseUrl = 'https://github.com/tello-ai/tello-cli/releases' }
    $baseUrl = $baseUrl.TrimEnd('/')

    $installDir = $env:TELLO_INSTALL_DIR
    if (-not $installDir) { $installDir = Join-Path $env:LOCALAPPDATA 'Programs\tello' }

    $version = $env:TELLO_VERSION
    if (-not $version) {
        try {
            $release = Invoke-RestMethod -UseBasicParsing -Uri 'https://api.github.com/repos/tello-ai/tello-cli/releases/latest'
            $version = $release.tag_name
        } catch {
            throw "tello installer: could not find the latest release ($($_.Exception.Message)); set TELLO_VERSION"
        }
    }
    $version = $version -replace '^v', ''

    $archive = "tello_${version}_windows_${arch}.zip"
    $releaseUrl = "$baseUrl/download/v$version"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('tello-install-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading tello $version (windows/$arch)"
        $zip = Join-Path $tmp $archive
        $sums = Join-Path $tmp 'checksums.txt'
        Get-Download "$releaseUrl/$archive" $zip
        Get-Download "$releaseUrl/checksums.txt" $sums

        $expected = $null
        foreach ($line in Get-Content -LiteralPath $sums) {
            $fields = -split $line
            if ($fields.Count -ge 2 -and ($fields[1] -eq $archive -or $fields[1] -eq "*$archive")) {
                $expected = $fields[0]
                break
            }
        }
        if (-not $expected) { throw "tello installer: checksums.txt has no entry for $archive" }
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash
        if ($actual -ne $expected) {
            throw "tello installer: checksum mismatch for $archive (expected $expected, got $actual)"
        }

        $extracted = Join-Path $tmp 'extract'
        Expand-Archive -LiteralPath $zip -DestinationPath $extracted -Force
        $exe = Join-Path $extracted 'tello.exe'
        if (-not (Test-Path -LiteralPath $exe)) { throw "tello installer: $archive does not contain tello.exe" }

        New-Item -ItemType Directory -Force -Path $installDir | Out-Null
        Copy-Item -LiteralPath $exe -Destination (Join-Path $installDir 'tello.exe') -Force
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
    Write-Host "Installed $(Join-Path $installDir 'tello.exe')"

    # Edit the raw registry value: [Environment]::SetEnvironmentVariable would expand
    # %VARIABLE% entries and turn the REG_EXPAND_SZ value into REG_SZ.
    $envKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $userPath = $envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $entries = @($userPath -split ';' | Where-Object { $_ })
        if ($entries -notcontains $installDir) {
            $envKey.SetValue('Path', (($entries + $installDir) -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
            # Setting and removing a throwaway variable broadcasts WM_SETTINGCHANGE, so
            # terminals started from now on pick up the new PATH.
            $probe = 'TELLO_INSTALLER_' + [Guid]::NewGuid().ToString('N')
            [Environment]::SetEnvironmentVariable($probe, '1', 'User')
            [Environment]::SetEnvironmentVariable($probe, [NullString]::Value, 'User')
            Write-Host "Added $installDir to your user PATH."
        }
    } finally {
        $envKey.Dispose()
    }
    if (($env:Path -split ';') -notcontains $installDir) { $env:Path = "$env:Path;$installDir" }

    Write-Host ''
    Write-Host 'Next steps (open a new terminal if tello is not found):'
    Write-Host '  tello version'
    Write-Host '  tello auth login'
    Write-Host '  tello doctor'
}
