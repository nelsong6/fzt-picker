use std::ffi::{CStr, CString};
use std::path::Path;

use windows::Win32::Foundation::HWND;
use windows::Win32::System::LibraryLoader::{GetProcAddress, LoadLibraryW};

/// Call the Go picker frontend DLL in-process.
/// The DLL creates a Win32 window for modal state, spawns the frontend
/// process via ConPTY, and runs a proper modal message loop.
pub fn run_picker(
    filter: Option<&str>,
    folders_only: bool,
    start_dir: &str,
    owner_hwnd: HWND,
) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    let dll_path = find_picker_dll()?;

    crate::log(&format!(
        "picker: loading frontend DLL ({dll_path}) filter={filter:?} folders={folders_only} dir={start_dir}"
    ));

    unsafe {
        let wide: Vec<u16> = dll_path.encode_utf16().chain(std::iter::once(0)).collect();
        let hmod = LoadLibraryW(windows::core::PCWSTR(wide.as_ptr()))
            .map_err(|e| format!("LoadLibrary failed: {e}"))?;

        // char* PickFile(char* filter, int foldersOnly, char* startDir, uintptr_t hwndOwner)
        let pick_file_addr = GetProcAddress(hmod, windows::core::s!("PickFile"))
            .ok_or("PickFile not found in picker_frontend.dll")?;
        let pick_file: unsafe extern "C" fn(*const i8, i32, *const i8, usize) -> *mut i8 =
            std::mem::transmute(pick_file_addr);

        let free_string_addr = GetProcAddress(hmod, windows::core::s!("FreeString"))
            .ok_or("FreeString not found in picker_frontend.dll")?;
        let free_string: unsafe extern "C" fn(*mut i8) = std::mem::transmute(free_string_addr);

        let filter_cstr = filter.map(|f| CString::new(f).unwrap());
        let filter_ptr = filter_cstr
            .as_ref()
            .map(|s| s.as_ptr())
            .unwrap_or(std::ptr::null());

        let start_dir_cstr = CString::new(start_dir).unwrap();
        let folders_flag = if folders_only { 1 } else { 0 };
        let hwnd_val = owner_hwnd.0 as usize;

        crate::log("picker: calling PickFile");
        let result_ptr = pick_file(filter_ptr, folders_flag, start_dir_cstr.as_ptr(), hwnd_val);

        if result_ptr.is_null() {
            crate::log("picker: PickFile returned null (cancelled)");
            return Ok(vec![]);
        }

        let result = CStr::from_ptr(result_ptr).to_string_lossy().to_string();
        free_string(result_ptr);

        crate::log(&format!("picker: PickFile returned: {result}"));

        let paths: Vec<String> = result
            .lines()
            .map(|l| l.trim())
            .filter(|l| !l.is_empty())
            .map(String::from)
            .collect();

        Ok(paths)
    }
}

fn find_picker_dll() -> Result<String, Box<dyn std::error::Error>> {
    if let Ok(userprofile) = std::env::var("USERPROFILE") {
        let candidate = Path::new(&userprofile)
            .join("bin")
            .join("picker_frontend.dll");
        if candidate.exists() {
            return Ok(candidate.to_string_lossy().to_string());
        }
    }

    let dev = "D:\\repos\\picker\\frontend\\cgo\\picker_frontend.dll";
    if Path::new(dev).exists() {
        return Ok(dev.to_string());
    }

    Err("picker_frontend.dll not found".into())
}
