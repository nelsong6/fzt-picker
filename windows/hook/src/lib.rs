mod bridge;
mod hook;
mod proxy;

use std::ffi::c_void;
use windows::Win32::Foundation::{BOOL, HINSTANCE, LPARAM, LRESULT, TRUE, WPARAM};
use windows::Win32::System::SystemServices::{DLL_PROCESS_ATTACH, DLL_PROCESS_DETACH};
use windows::Win32::UI::WindowsAndMessaging::{CallNextHookEx, HHOOK};

static mut HOOK_HANDLE: HHOOK = HHOOK(std::ptr::null_mut());

/// Called by the injector after SetWindowsHookEx to communicate the hook handle.
///
/// # Safety
/// Must be called exactly once from the injector process before any messages are dispatched.
#[no_mangle]
pub unsafe extern "system" fn SetHookHandle(handle: HHOOK) {
    HOOK_HANDLE = handle;
}

/// CBT hook callback required by SetWindowsHookEx. Does nothing — the real work
/// happens in DllMain where we install the CoCreateInstance detour.
#[no_mangle]
pub unsafe extern "system" fn HookProc(code: i32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    CallNextHookEx(HOOK_HANDLE, code, wparam, lparam)
}

#[no_mangle]
unsafe extern "system" fn DllMain(
    _hinstance: HINSTANCE,
    reason: u32,
    _reserved: *mut c_void,
) -> BOOL {
    match reason {
        DLL_PROCESS_ATTACH => {
            if let Err(e) = hook::install() {
                log(&format!("picker: hook install failed: {e}"));
            }
        }
        DLL_PROCESS_DETACH => {
            hook::uninstall();
        }
        _ => {}
    }
    TRUE
}

/// Append a log line to %TEMP%\picker.log.
/// Safe to call from any process — opens, appends, closes each time
/// to handle concurrent writes from multiple hooked processes.
fn log(msg: &str) {
    use std::io::Write;
    let Ok(temp) = std::env::var("TEMP") else {
        return;
    };
    let path = std::path::Path::new(&temp).join("picker.log");
    let Ok(mut f) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
    else {
        return;
    };
    let pid = std::process::id();
    let _ = writeln!(f, "[{pid}] {msg}");
}
