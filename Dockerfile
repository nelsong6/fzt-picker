# escape=`
FROM mcr.microsoft.com/windows/servercore:ltsc2022

SHELL ["powershell", "-Command", "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';"]

# ── Go ───────────────────────────────────────────────────────────
ARG GO_VERSION=1.26.1
RUN Invoke-WebRequest -Uri "https://go.dev/dl/go${env:GO_VERSION}.windows-amd64.zip" -OutFile go.zip; `
    Expand-Archive go.zip -DestinationPath C:\; `
    Remove-Item go.zip

# ── Rust nightly ─────────────────────────────────────────────────
RUN Invoke-WebRequest -Uri "https://win.rustup.rs/x86_64" -OutFile rustup-init.exe; `
    .\rustup-init.exe -y --default-toolchain nightly --profile minimal; `
    Remove-Item rustup-init.exe

# ── MSYS2 + MinGW GCC ───────────────────────────────────────────
RUN Invoke-WebRequest -Uri "https://github.com/msys2/msys2-installer/releases/download/nightly-x86_64/msys2-base-x86_64-latest.sfx.exe" -OutFile msys2.exe; `
    .\msys2.exe -y -oC:\; `
    Remove-Item msys2.exe; `
    C:\msys64\usr\bin\bash.exe -lc 'pacman -Syu --noconfirm'; `
    C:\msys64\usr\bin\bash.exe -lc 'pacman -S --noconfirm mingw-w64-ucrt-x86_64-gcc'

# ── Git (for go get) ────────────────────────────────────────────
RUN Invoke-WebRequest -Uri "https://github.com/git-for-windows/git/releases/download/v2.47.1.windows.2/MinGit-2.47.1.2-64-bit.zip" -OutFile git.zip; `
    Expand-Archive git.zip -DestinationPath C:\git; `
    Remove-Item git.zip

# ── PATH ─────────────────────────────────────────────────────────
RUN [Environment]::SetEnvironmentVariable('PATH', `
    'C:\go\bin;' + `
    $env:USERPROFILE + '\.cargo\bin;' + `
    'C:\msys64\ucrt64\bin;' + `
    'C:\git\cmd;' + `
    $env:PATH, `
    [EnvironmentVariableTarget]::Machine)

SHELL ["cmd", "/S", "/C"]
