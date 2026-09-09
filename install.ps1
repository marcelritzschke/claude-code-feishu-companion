# Installs claude-companion from a GitHub release: detects the architecture,
# downloads the matching archive, verifies it against the release's
# checksums.txt, and installs the binary.
#
# It then hands the console to `claude-companion init`, so one pasted line
# both installs and sets up. Nothing else is run on the user's behalf.
#
# This is install.sh's Windows sibling and follows it step for step. Where
# the two differ, Windows is the reason: a running image cannot be
# overwritten, PATH is a registry value rather than a shell export, and a
# console is inherited rather than reopened through /dev/tty.
#
# Usage:
#   irm https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.ps1 | iex
#
# Env vars:
#   VERSION      release tag to install, e.g. "v1.2.3" (default: latest)
#   INSTALL_DIR  where to put the binary (default: "$env:LOCALAPPDATA\Programs\claude-companion")
#   SKIP_INIT    set to 1 to install only and not start setup
#
# This script only reads the paths above and the user PATH; it never touches
# the configuration or cache directories.

# Everything runs inside this block so that the helpers below are scoped to
# it. Piped into `iex`, a bare function definition would otherwise stay
# behind in the user's session after the install finished.
& {
    $ErrorActionPreference = 'Stop'

    # Invoke-WebRequest renders a progress bar by redrawing the console on
    # every chunk, which on Windows PowerShell costs more time than the
    # download itself.
    $ProgressPreference = 'SilentlyContinue'

    $repo = 'marcelritzschke/claude-code-feishu-companion'
    $project = 'claude-code-feishu-companion'
    $name = 'claude-companion.exe'

    # Windows PowerShell 5.1 still defaults to protocols GitHub no longer
    # accepts, so the download would fail at the handshake. PowerShell 7
    # negotiates for itself and needs nothing here.
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    # Write-Log writes to stderr, keeping stdout free of progress commentary.
    function Write-Log([string]$Message = '') {
        [Console]::Error.WriteLine($Message)
    }

    # Write-NextStep says why setup did not start and how to start it by
    # hand. It names $Dest, the installed binary, because that path works
    # whether or not the install directory is on PATH yet.
    function Write-NextStep([string]$Why, [string]$Dest) {
        Write-Log
        Write-Log $Why
        Write-Log "Run '$Dest init' to connect a Feishu app and this machine."
    }

    function Get-Arch {
        # A 32-bit PowerShell on 64-bit Windows reports x86 in
        # PROCESSOR_ARCHITECTURE and the real answer in the ...W6432 twin.
        $arch = $env:PROCESSOR_ARCHITEW6432
        if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
        switch ($arch) {
            'AMD64' { return 'amd64' }
            'ARM64' { return 'arm64' }
            default { throw "install.ps1: unsupported architecture: $arch" }
        }
    }

    # Get-LatestVersion returns the tag name of the latest non-prerelease
    # GitHub release, e.g. "v1.2.3".
    function Get-LatestVersion {
        $headers = @{ 'User-Agent' = 'claude-companion-installer' }
        try {
            $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" `
                -Headers $headers -UseBasicParsing
        } catch {
            throw "install.ps1: could not ask GitHub for the latest release: $($_.Exception.Message)"
        }
        return $release.tag_name
    }

    # Get-File names the download it failed on. The bare web exception says
    # only that something returned 404, which for an install that fetches an
    # archive and a checksums file beside it is not enough to act on.
    function Get-File([string]$Url, [string]$Dest) {
        $headers = @{ 'User-Agent' = 'claude-companion-installer' }
        try {
            Invoke-WebRequest -Uri $Url -OutFile $Dest -Headers $headers -UseBasicParsing
        } catch {
            throw "install.ps1: could not download ${Url}: $($_.Exception.Message)"
        }
    }

    # Get-ExpectedSum reads GoReleaser's "<sha256>  <file>" lines. An
    # archive the file does not mention is refused: a checksums.txt that has
    # nothing to say about this download proves nothing about it.
    function Get-ExpectedSum([string]$ChecksumsPath, [string]$Archive) {
        foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
            $fields = @($line -split '\s+' | Where-Object { $_ })
            if ($fields.Count -eq 2 -and $fields[1] -eq $Archive) {
                return $fields[0].ToLowerInvariant()
            }
        }
        throw "install.ps1: checksums.txt does not list $Archive"
    }

    function Test-Checksum([string]$Path, [string]$ChecksumsPath, [string]$Archive) {
        $want = Get-ExpectedSum $ChecksumsPath $Archive
        $got = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($got -ne $want) {
            throw ("install.ps1: checksum verification failed for ${Archive}: " +
                "release says $want, download is $got")
        }
    }

    # Install-Binary puts the new program at $Dest.
    #
    # Windows will not overwrite a running image, and an earlier install's
    # daemon is exactly that. So the daemon is asked to leave first, and
    # whatever is still there is moved aside rather than replaced - renaming
    # a running image is permitted where overwriting one is not. This
    # mirrors what `claude-companion update` does for the same reason.
    function Install-Binary([string]$Staged, [string]$Dest) {
        $old = "$Dest.old"
        if (Test-Path -LiteralPath $Dest) {
            # Best effort, and quiet either way: there may be no daemon to
            # stop, and the program already installed may be too broken to
            # ask. Neither is a reason to fail an install that is about to
            # replace it.
            try {
                & $Dest daemon --stop 2>&1 | Out-Null
            } catch {
                Write-Debug "could not stop the running daemon: $($_.Exception.Message)"
            }
            Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
            try {
                Move-Item -LiteralPath $Dest -Destination $old -Force
            } catch {
                throw ("install.ps1: $Dest is in use and could not be " +
                    "replaced; close anything still running it and retry")
            }
        }
        Move-Item -LiteralPath $Staged -Destination $Dest -Force

        # The sidecar goes if nothing holds it any more. Anything still
        # running the old program keeps it open, so a failure here is
        # ordinary: the file is left for the next install to clear away.
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
    }

    # Add-ToUserPath puts the install directory on PATH for future sessions
    # and for this one. The registry write is what survives; $env:Path is
    # what lets the handoff to init, and anything else the user runs in this
    # console, find the program straight away.
    function Add-ToUserPath([string]$Dir) {
        $current = [Environment]::GetEnvironmentVariable('Path', 'User')
        if (-not $current) { $current = '' }

        # The comparison ignores empty entries and case, because Windows
        # does; the write appends to the value exactly as it was found. A
        # PATH is the user's, not this installer's, and one that happens to
        # carry an empty entry or a trailing separator must come back from
        # an install with those still in it.
        $entries = @($current -split ';' | Where-Object { $_ })
        if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $Dir.TrimEnd('\') })) {
            if ($current -eq '' -or $current.EndsWith(';')) {
                $updated = $current + $Dir
            } else {
                $updated = $current + ';' + $Dir
            }
            # .NET broadcasts WM_SETTINGCHANGE for a User-scoped write, so
            # newly started programs see this without a sign-out.
            [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
            Write-Log "claude-companion: added $Dir to your PATH (new terminals will see it)"
        }
        if (-not (@($env:Path -split ';') | Where-Object { $_ -ieq $Dir })) {
            $env:Path = "$env:Path;$Dir"
        }
    }

    function Install-ClaudeCompanion {
        $arch = Get-Arch

        $version = $env:VERSION
        if (-not $version) {
            $version = Get-LatestVersion
            if (-not $version) {
                throw 'install.ps1: could not determine the latest release; set VERSION=vX.Y.Z and retry'
            }
        }
        $versionNum = $version -replace '^v', ''

        $installDir = $env:INSTALL_DIR
        if (-not $installDir) {
            $installDir = Join-Path $env:LOCALAPPDATA 'Programs\claude-companion'
        }

        $archive = "${project}_${versionNum}_windows_${arch}.zip"
        $baseUrl = "https://github.com/$repo/releases/download/$version"

        $slug = 'claude-companion-' + [Guid]::NewGuid().ToString('N')
        $workdir = Join-Path ([IO.Path]::GetTempPath()) $slug
        New-Item -ItemType Directory -Path $workdir -Force | Out-Null

        try {
            Write-Log "claude-companion: downloading $archive ($version)"
            Get-File "$baseUrl/$archive" (Join-Path $workdir $archive)
            Get-File "$baseUrl/checksums.txt" (Join-Path $workdir 'checksums.txt')

            Write-Log 'claude-companion: verifying checksum'
            Test-Checksum (Join-Path $workdir $archive) (Join-Path $workdir 'checksums.txt') $archive

            $extract = Join-Path $workdir 'extract'
            Expand-Archive -LiteralPath (Join-Path $workdir $archive) -DestinationPath $extract -Force

            $staged = Join-Path $extract $name
            if (-not (Test-Path -LiteralPath $staged)) {
                throw "install.ps1: the archive holds no $name"
            }

            # An archive fetched over the network can carry a mark of the
            # web, which Windows applies to the files unpacked from it and
            # then warns about on every run. The bytes were just checked
            # against the release's own checksum, which is a stronger claim
            # than the mark makes.
            Unblock-File -LiteralPath $staged -ErrorAction SilentlyContinue

            New-Item -ItemType Directory -Path $installDir -Force | Out-Null
            $dest = Join-Path $installDir $name
            Install-Binary $staged $dest

            Write-Log "claude-companion: installed to $dest"
            Add-ToUserPath $installDir

            & $dest --version

            if ($env:SKIP_INIT -eq '1') {
                Write-NextStep 'SKIP_INIT is set, so setup was not started.' $dest
                return
            }

            # Unlike the shell installer, this script's own stdin is not the
            # download: `iex` runs it inside the console the user is sitting
            # at, so init inherits that console with nothing to reopen.
            # Without one - a scheduled task, CI - there is nothing to hand
            # over and setup stays for the user to run later.
            if (-not [Environment]::UserInteractive) {
                Write-NextStep 'No console is attached, so setup was not started.' $dest
                return
            }

            Write-Log
            & $dest init
        } finally {
            Remove-Item -LiteralPath $workdir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    Install-ClaudeCompanion
}
