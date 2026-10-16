[English](README.en.md) | [简体中文](README.md)

# FileServer

A cross-platform file server (Windows / Linux / macOS): a single executable lets phones and computers on the same LAN read-only browse, preview and download a chosen directory; optional password + TOTP two-factor authentication; and new/edit text files.

## What it does

- **Single-file run**: compiled into a single ~8MB executable; double-click (Windows) or run from the command line, no runtime installation; release packages can optionally bundle ffmpeg components
- **Cross-platform**: Windows / Linux / macOS are all first-class; Linux/macOS run from the command line or systemd and can be exposed only behind a reverse proxy (`--host 127.0.0.1`)
- **Addresses found automatically**: detects the local LAN IP at startup and prints every access address to the console (`--browser` also opens the browser)
- **Terminal QR code**: renders a QR code for each address so phones can scan directly (`--no-qr` disables it)
- **Optional authentication**: `--auth-mode totp|password|password+totp`; a first-run guide sets it up, with "remember me", logout and session management; a CLI reset recovers a lost authenticator
- **Text new/edit**: whitelisted text files can be created and edited in the browser (atomic writes + conflict detection); oversized files are read-only
- **Online video playback**: native formats stream directly; with support for less common formats enabled, MKV/RMVB/AVI/WMV/HEVC etc. are transcoded to H.264 in real time and played while transcoding
- **Odd-container MP4 detection**: identifies MP4 files with fragmented mdat / oversized moov; use the separate mp4norm project when you need permanent normalization (see below)
- **Video thumbnails**: ordinary MP4/WebM frames are extracted by the browser at zero server cost; less common formats are frame-extracted server-side once support is enabled
- **Hand-drawn player**: progress seeking, speed, volume, fullscreen, with space / arrow keys / M / F shortcuts
- **Image lightbox and online preview**: the lightbox supports zoom, rotation, keyboard and touch; video, audio, PDF, text and code can be previewed
- **Directory zip download**: streams any folder as a zip; **recursive search** with depth and count limits
- **Limited write scope**: only text file create/edit; delete, rename, move and upload are intentionally not supported. Cache lives in the hidden `.FileServer` folder under the served directory
- **Path safety**: prevents directory traversal and symlink escapes

## Quick start

1. Unzip and put the executable in the folder to share (or elsewhere with `--dir`), then run it
2. If the firewall prompts on first run (Windows "Private networks" / macOS "allow incoming connections"), allow it
3. Connect a phone or computer to the same WiFi and open the address printed by the console, in the form `http://<LAN address>:8080`

No password is enabled by default, and any device on the same LAN can open it; use
`--auth-mode password` (or `password+totp` / `totp`) to restrict access — the first visit opens a setup guide.

Stopping the service: close the window, or press `Ctrl+C` (graceful exit that also terminates any running transcode processes).

## Command-line options

| Option | Description |
|---|---|
| `--port 9000` | listening port, default 8080; auto-increments when occupied (tries 20 in turn) |
| `--host 127.0.0.1` | listen address, default `0.0.0.0` (LAN reachable). Use `127.0.0.1` to expose only behind a reverse proxy |
| `--dir <path>` | served directory, default the executable's directory |
| `--browser` | open the default browser after startup, off by default |
| `--hidden` | show hidden files (leading dot). When off (default), hidden files are invisible / inaccessible in the list, search, direct download, thumbnails and zip; `.FileServer` is a reserved name and is always invisible / inaccessible regardless |
| `--auth-mode <mode>` | authentication mode: `none` (default, open) \| `totp` \| `password` \| `password+totp` |
| `--auth user:pass` | compatibility: equals `--auth-mode password` plus a preset password |
| `--remember-days 30` | "remember me" persistent cookie lifetime in days, default 30 |
| `--session-hours 12` | session lifetime when "remember me" is unchecked (hours), default 12 |
| `--trust-proxy` | trust the reverse proxy's `X-Forwarded-For` (leftmost value is the client IP); off by default |
| `--cookie-secure` | force the `Secure` flag on the auth cookie (automatic under https) |
| `--reset-auth` | clear all credentials and sessions then exit (recovery while the service is stopped) |
| `--reset-totp` | clear only the TOTP binding then exit (recovery after losing an authenticator) |
| `--text-max-bytes N` | editable text size limit in bytes, default 2097152 (2 MiB) |
| `--ffmpeg` | enable real-time transcoding of less common formats and server-side thumbnail frame extraction (needs ffmpeg; CPU/GPU cost; off by default), also toggleable from the web toolbar |
| `--no-qr` | do not show address QR codes in the terminal |
| `-v` | output access logs |

Same-named environment variables act as defaults (command-line wins): `FILESERVER_PORT`,
`FILESERVER_HOST`, `FILESERVER_DIR`, `FILESERVER_HIDDEN`, `FILESERVER_FFMPEG`,
`FILESERVER_AUTH`, `FILESERVER_AUTH_MODE`, `FILESERVER_REMEMBER_DAYS`,
`FILESERVER_SESSION_HOURS`, `FILESERVER_TRUST_PROXY`, `FILESERVER_COOKIE_SECURE`,
`FILESERVER_TEXT_MAX_BYTES`. When locating ffmpeg it also checks
`%LOCALAPPDATA%\FileServer\ffmpeg`.

## Authentication

When enabled (`--auth-mode` other than `none`), the first visit opens a **setup guide**
to set a password (≥ 8 chars) and bind TOTP (`totp` requires binding; `password+totp` can
skip it and degrade to single-factor). The login form offers "remember me" (checked =
stay signed in up to 30 days; unchecked = session cookie that expires when the browser
closes). Credentials are stored only server-side in `.FileServer/auth.json` (0600):
Argon2id password hash, TOTP secret (0600-protected) and sha256 hashes of session tokens.
Lost authenticator / password: with the service stopped, run `--reset-totp` (TOTP only)
or `--reset-auth` (all credentials), then restart. See `doc/security.md`.

## Online video playback

After opening a video the frontend asks `/api/video-info`, and the server decides in milliseconds from the file extension and MP4 header (without waiting for ffprobe).

Less common format support off (default): native formats (H.264 MP4, WebM etc.) play directly (Range streaming + hardware decoding); MKV/RMVB/AVI/WMV/HEVC etc. cannot be played online and can be downloaded.

With less common format support on: native formats still play directly; odd-container MP4 goes through ffmpeg `-c copy` remuxing into HLS segments (zero transcode, zero quality loss); less common formats are transcoded to H.264 in real time (GPU first, CPU fallback) and played while transcoding.

Shared mechanisms:

- **Encoder probed and preferred by measurement**: at startup it tries h264_nvenc / h264_amf / h264_qsv in turn, falling back to libx264 when none is usable; playback works with no discrete GPU
- **Hardware decoding on demand**: only H.264/HEVC use the matching `-hwaccel` (cuda / d3d11va / qsv); RMVB etc. hang with CUDA hardware decoding and automatically fall back to CPU decoding
- **Session management**: 3-second segments; 4 concurrent copy sessions and 2 concurrent transcode sessions; first-segment wait cap 60s and segment-request blocking cap 28s; stalled 60s or idle 10 minutes terminates automatically; leaving the player page notifies the server to terminate (`abandon`, with taskkill as a fallback)
- **Process safety**: ffmpeg child processes are attached to a Job Object and terminated together when the service exits or crashes, leaving no orphans
- **Playlist**: an EVENT playlist is returned while transcoding (no ENDLIST yet) and switches to VOD when complete; the progress bar shows the true total duration sent by the server
- **Seek convergence and cache reuse**: dragging to a position not yet generated converges to the generated range and can continue once transcoding catches up; full transcode results are cached for 3 days

## Odd-container detection

- **Criterion**: more than 4 mdat blocks and a file ≥ 256MB counts as odd-container (the result is cached, in milliseconds), used only to decide whether to use HLS copy for faster playback.

**For permanent normalization use [mp4norm](https://github.com/YLing2024/mp4norm)**: it supports batch detection (`mp4norm scan <dir>` directly gives "needs processing / no processing needed / cannot process"), batch normalization, in-place replacement (automatic backup, recoverable) and folder monitoring. FileServer no longer includes normalization.

## Video thumbnails

**Ordinary MP4/WebM: browser frame extraction, always (zero server cost)**

The browser loads the truncated frame-extraction source returned by `/api/thumb-src` with a hidden `<video>` and captures a frame to canvas after seeking:

- Why not the original file: Chromium sends open-range Range requests (`bytes=0-`) for video sources, which would return the whole large file
- Frame-extraction source: the server returns only the header sample region + moov (a valid MP4 ≤16MB), so the browser reads metadata in seconds and seeks to a sample
- Lazy loading and playback priority: extraction happens only when a card enters the viewport (3 concurrent, 20s fallback timeout, keeping the icon when decoding is unsupported or the file is broken); opening a video aborts in-flight extraction, and returning to the list resumes it

**Less common formats (MKV/RMVB/AVI/WMV/HEVC etc.):**

- With support enabled: the server extracts one JPEG frame with ffmpeg (at 1s, skipping black frames) and caches it under `.FileServer\thumb\` (cleaned after 7 days), 2 concurrent at low priority
- With support disabled: the file icon is kept and no server cost is incurred

## Cache directory

```
<served directory>\.FileServer\
├── thumb\      image thumbnails + server-side frame thumbnails for less common formats (jpg, cleaned after 7 days)
├── hls\        HLS transcode/remux sessions (index.m3u8 + seg_*.m4s, cleaned after 3 days)
├── auth.json   credentials and sessions (0600, present only when auth is enabled)
└── backup\     historical backups (legacy from the old normalization; can still be restored manually, or handled with mp4norm)
```

- It does not write to system directories; it falls back to the system temp directory only when the served directory is not writable
- Deleting `.FileServer` clears all cache without affecting functionality; regardless of `--hidden`, it never appears in lists, search or zip and cannot be accessed through direct links, thumbnails or frame-extraction sources (including case variants)

## Build and test

Requires Windows and network access (the Go toolchain is downloaded automatically to `.tools\` on first use, no installation):

```
build.bat                  # build to dist\FileServer.exe
release.ps1 -Version 1.2.3 # package dist\FileServer-lite-<version>.zip and -full-<version>.zip
```

Manual build and test:

```
.tools\go\bin\go.exe vet ./...
.tools\go\bin\go.exe build -trimpath -ldflags "-s -w" -o dist\FileServer.exe .\cmd\fileserver
.tools\go\bin\go.exe test ./internal/...
```

End-to-end tests (Python 3.12 + Playwright, service must be started first):

```
dist\FileServer.exe --dir .\testdata --port 8099 --no-qr
python scripts\smoke_test.py        # smoke
python scripts\regression_test.py   # regression
python scripts\mobile_test.py       # mobile + lightbox + zip
python scripts\nav_test.py          # navigation/history
python scripts\special_e2e_test.py  # special files end-to-end
python scripts\hls_e2e_test.py      # HLS end-to-end
python scripts\frontthumb_test.py   # frontend frame extraction (needs a server without ffmpeg)
```

## Project structure

```
cmd/fileserver/          entry: flag parsing, LAN IP detection, console output and QR code, graceful exit
internal/server/         HTTP service: routing, files and video, playback decisions, thumbnails, HLS, security and paths
internal/server/web/     embedded frontend (index.html / style.css / app.js / hls.min.js)
internal/qrcode/         pure-Go QR encoding and terminal rendering
internal/platform/       OS differences (console encoding, opening the browser, child process reaping)
scripts/                 Playwright test scripts
testdata/ + doc/         test samples (*.mp4 not committed); project docs
build.bat + release.ps1  build and packaging
```

## FAQ

- **Phone cannot open it**: make sure it is on the same network and the firewall allows it; if the router has AP isolation enabled, turn it off
- **Port occupied**: the program tries ports after 8080 automatically and the console shows the actual address
- **Which videos need transcoding, and why some do not play even with less common formats enabled**: native formats play directly, while MKV/RMVB/AVI/WMV/HEVC etc. need less common format support; if they still do not play, make sure the server can find ffmpeg (`ffmpeg\` next to the exe, next to the exe, or on PATH)
- **A large video waits the first time / no thumbnail**: a fragmented MP4 on a mechanical disk takes seconds to tens of seconds for a cold read, and a cache is generated after playing it once; for odd-container MP4 that should open instantly forever, use the separate [mp4norm](https://github.com/YLing2024/mp4norm) project; with less common format support off, HEVC/MKV having no thumbnail and keeping the icon is normal

## License

MIT, see `LICENSE`.
