# picker

Replaces the Windows file picker dialog (IFileOpenDialog) with fzt's fuzzy finder TUI. When any app opens File > Open, a fzt tree view appears instead of the standard dialog.

## Architecture

Two layers:

- **COM hook (Rust, `windows/`)**: Hook DLL + injector. Intercepts `CoCreateInstance` for `IFileOpenDialog` via `retour` detours. Returns a COM proxy implementing `IFileOpenDialog`, `IFileDialog`, `IFileDialog2`, and `IFileDialogCustomize`. When `Show()` is called, spawns the Go frontend with the calling app's filter and folder-only flags.
- **Frontend (Go, `frontend/`)**: Standalone fzt frontend built on `fzt-terminal/tui`. Queries Everything (`es.exe`) for files, builds a hierarchical tree, runs `tui.Run()`, and prints the selected path to stdout. This is picker's own fzt frontend — it does NOT shell out to fzt or fzt-automate.

### COM hook components

- **hook DLL** (`picker-hook.dll`): Loaded into every GUI process via `SetWindowsHookEx`. Hooks `CoCreateInstance` in `combase.dll` to intercept `CLSID_FileOpenDialog`. Returns a COM proxy.
- **injector** (`picker.exe`): Background process that installs the global CBT hook and maintains a message loop. Killing it removes the hook from new processes.
- **test-trigger** (`test-trigger.exe`): Standalone binary that calls `CoCreateInstance(CLSID_FileOpenDialog)` and `Show()` to test the hook without needing a real app.

### Data flow

1. App calls `CoCreateInstance(CLSID_FileOpenDialog)` → hook returns proxy
2. App configures proxy (SetFileTypes, SetOptions, etc.)
3. App calls `Show()` → proxy spawns `picker-frontend.exe --filter "*.txt"` with `CREATE_NEW_CONSOLE`
4. Frontend queries Everything, builds tree, runs fzt TUI in a new Windows Terminal window
5. User picks a file → frontend prints path to stdout → proxy reads it via pipe
6. App calls `GetResult()` → proxy returns `IShellItem` for the selected path

## Dependencies

- **Everything (voidtools)**: File indexing service. The frontend queries `es.exe` (Everything CLI) for instant file discovery across all NTFS drives.
- **fzt / fzt-terminal**: The frontend imports `fzt/core` for item types and `fzt-terminal/tui` for the interactive TUI.
- **Rust nightly**: Required by the `retour` crate's `static_detour!` macro.
- **Windows Terminal**: Set as default terminal application. `CREATE_NEW_CONSOLE` opens a new WT window (requires `windowingBehavior: "useNew"` in WT settings).

## Building

```sh
# Rust (COM hook + injector)
cd windows
cargo build

# Go (frontend)
cd frontend
go build -o picker-frontend.exe .
```

## Running

Deployed to `~/bin` via release build. Starts automatically at login via a startup folder shortcut (created by `init.ps1`).

```sh
# Manual start
picker

# Standalone folder picker (also used by the 'explore' at-command)
picker-frontend --folders-only
```

Kill `picker.exe` to remove the hook from new processes. Already-loaded DLLs remain until those apps restart.

## Development workflow

The hook DLL loads into every GUI process and can't be replaced while those processes are running. To deploy a new DLL:
1. Kill `picker.exe`
2. Rename the old DLL (Windows allows renaming locked files)
3. Copy the new DLL
4. Restart `picker.exe`
5. New processes get the new DLL; old processes keep the old one until they restart

## Logging

The hook DLL logs to `%TEMP%\picker.log` with the host process PID prefix. Each log line is append + close to handle concurrent writes from multiple hooked processes. QI (QueryInterface) logging is patched into the proxy's vtable for debugging.

## Changelog

### 2026-04-05: Initial implementation

- COM hook via `retour` detours `CoCreateInstance` for `CLSID_FileOpenDialog`
- Full `IFileOpenDialog` proxy (26 methods): Set* methods store state, `Show()` spawns fzt, `GetResult()` returns `IShellItem`
- Everything-backed file discovery with YAML tree generation
- `CREATE_NEW_CONSOLE` spawns fzt in a visible terminal window
- Injector via `SetWindowsHookEx(WH_CBT)` for system-wide DLL loading

### 2026-04-06: Bug fixes

- Fixed folder-only mode blank display
- Added Everything exclusions (`.git`, `$Recycle.Bin`, `node_modules`)
- Bumped result limit 10,000 → 50,000

### 2026-04-13: Architecture overhaul

- **IFileDialogCustomize stub** — Notepad queries for this interface after getting IFileOpenDialog. Without it, the proxy was silently dropped. Added no-op implementation for all 27 methods.
- **QI logging** — Patched proxy vtable's QueryInterface to log all interface requests. Diagnosed the IFileDialogCustomize failure.
- **Fixed Everything CLI args** — Exclusion terms were passed as a single argument instead of separate args, causing zero results.
- **Go frontend** — Replaced Rust walker + fzt subprocess with a standalone Go binary (`picker-frontend.exe`) that imports `fzt-terminal/tui` directly. The frontend owns Everything queries, tree building, and TUI rendering. Eliminates the Rust→YAML→file→Go round-trip.
- **AllowSetForegroundWindow** — DLL grants foreground rights to the frontend process so its window comes to front.
- **`explore` at-command** — Added to `at-commands.ps1`, calls `picker-frontend --folders-only` and opens the result in Explorer. Leaf added to at-menu cloud database.

### Known limitations

- **Notepad "not responding"** — `Show()` blocks the calling app's UI thread while the frontend starts. Windows shows "not responding" ghost window during the delay.
- **IFileSaveDialog**: Detected but passes through to standard dialog
- **No fallback**: If frontend crashes or Everything isn't running, the dialog fails with ERROR_CANCELLED
- **Multi-select**: Only returns the first selected item
