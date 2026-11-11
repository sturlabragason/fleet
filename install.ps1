# Install fleet on Windows from a GitHub release, without admin (ADR-117).
#
#   irm https://raw.githubusercontent.com/sturlabragason/fleet/main/install.ps1 | iex
#
# Environment:
#   FLEET_VERSION      release tag to install (vX.Y.Z); default: the latest release
#   FLEET_INSTALL_DIR  where fleet.exe goes; default: %LOCALAPPDATA%\Programs\fleet
#   FLEET_DRY_RUN      1 = show what would be downloaded and where; change nothing
#
# Downloads fleet-windows-<arch>.exe and SHA256SUMS, refuses on a checksum
# mismatch, installs fleet.exe and adds its directory to the user PATH.
# Running it again replaces fleet.exe in place, even while fleet runs.

function Install-Fleet {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is slow with it on
	[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

	$repo = 'sturlabragason/fleet'
	$base = if ($env:FLEET_BASE_URL) { $env:FLEET_BASE_URL } else { "https://github.com/$repo/releases" }
	function Say($m) { Write-Host "fleet-install: $m" }

	$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
	switch ($arch) {
		'AMD64' { $arch = 'amd64' }
		'ARM64' { $arch = 'arm64' }
		default { throw "fleet-install: unsupported architecture '$arch': releases are built for amd64 and arm64" }
	}

	$v = $env:FLEET_VERSION
	if (-not $v) {
		$v = (Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repo/releases/latest").tag_name
		if (-not $v) { throw "fleet-install: could not find the latest release of $repo (set FLEET_VERSION=vX.Y.Z)" }
	}
	if ($v -match '^\d+\.\d+\.\d+$') { $v = "v$v" }
	if ($v -notmatch '^v\d+\.\d+\.\d+') { throw "fleet-install: not a release tag: '$v' (want vX.Y.Z)" }

	$dir = if ($env:FLEET_INSTALL_DIR) { $env:FLEET_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\fleet' }
	$asset = "fleet-windows-$arch.exe"
	$url = "$base/download/$v"
	$exe = Join-Path $dir 'fleet.exe'
	Say "fleet $v for windows-$arch"
	Say "from    $url/$asset"
	Say "to      $exe"
	if ($env:FLEET_DRY_RUN -eq '1') { Say 'dry run: nothing downloaded or installed'; return }

	$tmp = Join-Path ([IO.Path]::GetTempPath()) ("fleet-install-" + [guid]::NewGuid())
	New-Item -ItemType Directory -Force -Path $tmp | Out-Null
	try {
		Invoke-WebRequest -UseBasicParsing "$url/$asset" -OutFile "$tmp\$asset"
		Invoke-WebRequest -UseBasicParsing "$url/SHA256SUMS" -OutFile "$tmp\SHA256SUMS"
		$want = $null
		foreach ($line in Get-Content "$tmp\SHA256SUMS") {
			$f = $line -split '\s+', 2
			if ($f.Count -eq 2 -and $f[1].TrimStart('*') -eq $asset) { $want = $f[0].ToLower(); break }
		}
		if (-not $want) { throw "fleet-install: SHA256SUMS has no line for $asset" }
		$got = (Get-FileHash -Algorithm SHA256 "$tmp\$asset").Hash.ToLower()
		if ($got -ne $want) { throw "fleet-install: checksum mismatch for ${asset}: SHA256SUMS says $want, the download is $got; not installing" }
		Say "sha256  ok ($got)"

		New-Item -ItemType Directory -Force -Path $dir | Out-Null
		# Windows renames a running exe but never overwrites it: move it aside.
		$old = "$exe.old"
		if (Test-Path $old) { Remove-Item -Force $old -ErrorAction SilentlyContinue }
		if (Test-Path $exe) { Move-Item -Force $exe $old }
		Move-Item -Force "$tmp\$asset" $exe
		if (Test-Path $old) { Remove-Item -Force $old -ErrorAction SilentlyContinue }
	} finally {
		Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
	}
	Say "installed: $(& $exe version)"

	$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
	$parts = @($userPath -split ';' | Where-Object { $_ })
	if ($parts -notcontains $dir) {
		[Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User')
		Say "added $dir to your user PATH (new terminals pick it up)"
	}
	if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }

	Write-Host ''
	Write-Host 'Next:'
	Write-Host '  fleet spawn --fake a1 .   try it with the fake agent (no tokens)'
	Write-Host '  fleet spawn w1 claude .   a Claude agent over ACP (needs Node.js and Claude Code)'
	Write-Host '  fleet --help              all commands'
}

Install-Fleet
