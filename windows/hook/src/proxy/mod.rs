mod open;
mod state;

use std::ffi::c_void;
use std::sync::atomic::{AtomicUsize, Ordering};

use windows::core::{Interface, GUID};
use windows::Win32::System::Memory::{VirtualProtect, PAGE_PROTECTION_FLAGS, PAGE_READWRITE};
use windows_core::HRESULT;

static ORIGINAL_QI: AtomicUsize = AtomicUsize::new(0);

unsafe extern "system" fn logging_qi(
    this: *mut c_void,
    riid: *const GUID,
    ppv: *mut *mut c_void,
) -> HRESULT {
    let original: unsafe extern "system" fn(*mut c_void, *const GUID, *mut *mut c_void) -> HRESULT =
        std::mem::transmute(ORIGINAL_QI.load(Ordering::SeqCst));
    let hr = original(this, riid, ppv);
    crate::log(&format!("picker: QI {:?} -> 0x{:08X}", *riid, hr.0));
    hr
}

/// Create a FileOpenDialog proxy and return it through the COM ppv out-pointer.
///
/// # Safety
/// `riid` and `ppv` must be valid pointers from the original CoCreateInstance call.
pub unsafe fn create_file_open_dialog(
    riid: *const GUID,
    ppv: *mut *mut c_void,
) -> Result<HRESULT, Box<dyn std::error::Error>> {
    crate::log(&format!(
        "picker: create_file_open_dialog riid={:?}",
        *riid
    ));
    let proxy = open::FileOpenDialogProxy::new();
    let unknown: windows::core::IUnknown = proxy.into();
    let hr = unknown.query(&*riid, ppv as *mut _);
    crate::log(&format!("picker: QueryInterface returned 0x{:08X}", hr.0));

    // Patch the returned interface's vtable to log all subsequent QI calls
    if hr.is_ok() && !(*ppv).is_null() {
        let vtable_ptr = *(*ppv as *const *mut usize);
        let mut old_protect = PAGE_PROTECTION_FLAGS(0);
        if VirtualProtect(
            vtable_ptr as *const c_void,
            std::mem::size_of::<usize>(),
            PAGE_READWRITE,
            &mut old_protect,
        )
        .is_ok()
        {
            ORIGINAL_QI.store(*vtable_ptr, Ordering::SeqCst);
            *vtable_ptr = logging_qi as usize;
            let _ = VirtualProtect(
                vtable_ptr as *const c_void,
                std::mem::size_of::<usize>(),
                old_protect,
                &mut old_protect,
            );
            crate::log("picker: QI logging patched");
        } else {
            crate::log("picker: VirtualProtect failed, QI logging not available");
        }
    }

    Ok(hr)
}
