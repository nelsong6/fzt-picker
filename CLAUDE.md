# picker

Replaces the Windows file picker dialog (IFileOpenDialog) with fzt's fuzzy finder TUI. When any app opens File > Open, a fzt tree view appears instead of the standard dialog.

## Architecture

Three components, all Rust (nightly required for `retour`):

- **hook DLL** (`picker-hook.dll`): Loaded into every GUI process via `SetWindowsHookEx`. Hooks `CoCreateInstance` in `combase.dll` to intercept `CLSID_FileOpenDialog`. Returns a COM proxy implementing `IFileOpenDialog` and `IFileDialog2`.
- **injector** (`picker.exe`): Background process that installs the global CBT hook and maintains a message loop. Killing it removes the hook from new processes.
- **test-trigger** (`test-trigger.exe`): Standalone binary that calls `CoCreateInstance(CLSID_FileOpenDialog)` and `Show()` to test the hook without needing a real app.

## Dependencies

- **Everything (voidtools)**: File indexing service. The walker queries `es.exe` (Everything CLI) for instant file discovery across all NTFS drives. Falls back to `walkdir` if Everything isn't installed.
- **fzt**: Fuzzy finder TUI. Spawned with `--yaml` tree mode via `CREATE_NEW_CONSOLE`.
- **Rust nightly**: Required by the `retour` crate's `static_detour!` macro.

## Building

```sh
cd windows
cargo build
# Artifacts: target/debug/picker.exe, target/debug/picker_hook.dll, target/debug/test-trigger.exe
```

## Running

Deployed to `~/bin` via release build. Starts automatically at login via a startup folder shortcut (created by `init.ps1`).

```sh
# Manual start (if not auto-started)
picker

# Test with the test harness
test-trigger

# Or open any app's File > Open dialog
```

Kill `picker.exe` to remove the hook from new processes. Already-loaded DLLs remain until those apps restart.

## Development workflow

The hook DLL loads into every GUI process and can't be replaced while those processes are running. For iterative development, either:
- Build to a different `--target-dir` each time (e.g. `cargo build --target-dir target2`)
- Or reboot to release all DLL locks before deploying to `~/bin`

## File Discovery

The walker queries Everything (`es.exe`) with no path restriction, capped at 10,000 results. Results are built into a YAML tree with drives (C:, D:, etc.) as top-level nodes, then passed to fzt via `--yaml`. The `description` field on leaf nodes holds the full file path, returned via `--accept-nth=2`.

Falls back to `walkdir` (recursive directory walk, max depth 5) if Everything or `es.exe` is unavailable.

## Logging

The hook DLL logs to `%TEMP%\picker.log` with the host process PID prefix. Each log line is append + close to handle concurrent writes from multiple hooked processes.

## Changelog

### 2026-04-05: Initial implementation

- COM hook via `retour` detours `CoCreateInstance` for `CLSID_FileOpenDialog`
- Full `IFileOpenDialog` proxy (26 methods): Set* methods store state, `Show()` spawns fzt, `GetResult()` returns `IShellItem` via `SHCreateItemFromParsingName`
- Everything-backed file discovery: instant indexed queries across all drives
- YAML tree generation with drives as top-level nodes, single-quoted paths (avoids YAML `\U` unicode escape issues)
- `CREATE_NEW_CONSOLE` spawns fzt in a visible Windows Terminal window
- fzt tree mode with folder icons (requires nerd font as Windows Terminal default profile font)
- Injector via `SetWindowsHookEx(WH_CBT)` for system-wide DLL loading
- Test harness (`test-trigger.exe`) for isolated testing

#### Known limitations

- **IFileSaveDialog**: Detected but passes through to standard dialog (not intercepted yet)
- **SetFileTypes**: File type filter from calling app wired to Everything `ext:` query but untested with real apps
- **SetFolder/SetDefaultFolder**: Calling app's requested start directory not used to pre-scope the tree (Everything queries all drives)
- **No fallback**: If fzt crashes or Everything isn't running, the dialog fails with ERROR_CANCELLED rather than falling back to the original Windows dialog
- **Multi-select**: `GetResults()` only returns the first selected item
- **Excluded processes**: No process exclusion list — the hook loads into every GUI process including system processes
- **No configuration**: fzt path, Everything path, and behavior are hardcoded
