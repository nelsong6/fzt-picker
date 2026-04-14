#![windows_subsystem = "windows"]

use windows::core::PCWSTR;
use windows::Win32::Foundation::{HINSTANCE, HWND, LPARAM, LRESULT, WPARAM};
use windows::Win32::System::LibraryLoader::{GetModuleHandleW, GetProcAddress, LoadLibraryW};
use windows::Win32::UI::Shell::{
    Shell_NotifyIconW, NIF_ICON, NIF_MESSAGE, NIF_TIP, NIM_ADD, NIM_DELETE, NOTIFYICONDATAW,
};
use windows::Win32::UI::WindowsAndMessaging::*;

type FnSetHookHandle = unsafe extern "system" fn(HHOOK);

const WM_TRAYICON: u32 = 0x8001;
const IDM_EXIT: u16 = 1;

static mut HOOK_HANDLE: HHOOK = HHOOK(std::ptr::null_mut());

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let dll_path = find_hook_dll()?;

    unsafe {
        let wide: Vec<u16> = dll_path.encode_utf16().chain(std::iter::once(0)).collect();
        let hmod = LoadLibraryW(PCWSTR(wide.as_ptr()))?;
        let hinstance: HINSTANCE = std::mem::transmute(hmod);

        let hook_proc_addr = GetProcAddress(hmod, windows::core::s!("HookProc"))
            .ok_or("HookProc not found")?;
        let hook_proc: HOOKPROC = Some(std::mem::transmute(hook_proc_addr));

        let hook = SetWindowsHookExW(WH_CBT, hook_proc, hinstance, 0)?;
        HOOK_HANDLE = hook;

        let set_handle_addr = GetProcAddress(hmod, windows::core::s!("SetHookHandle"))
            .ok_or("SetHookHandle not found")?;
        let set_handle: FnSetHookHandle = std::mem::transmute(set_handle_addr);
        set_handle(hook);

        // Create hidden window for tray messages
        let hinst = GetModuleHandleW(None)?;
        let class_name = windows::core::w!("PickerTray");

        let wc = WNDCLASSEXW {
            cbSize: std::mem::size_of::<WNDCLASSEXW>() as u32,
            lpfnWndProc: Some(tray_wnd_proc),
            hInstance: hinst.into(),
            lpszClassName: class_name,
            ..Default::default()
        };
        RegisterClassExW(&wc);

        let hwnd = CreateWindowExW(
            WINDOW_EX_STYLE(0),
            class_name,
            PCWSTR::null(),
            WINDOW_STYLE(0),
            0, 0, 0, 0,
            None,
            None,
            hinst,
            None,
        )?;

        // Add tray icon
        let mut tip: [u16; 128] = [0; 128];
        for (i, c) in "fzt picker".encode_utf16().enumerate() {
            if i < 127 { tip[i] = c; }
        }

        let icon = LoadIconW(None, IDI_APPLICATION)?;

        let mut nid = NOTIFYICONDATAW {
            cbSize: std::mem::size_of::<NOTIFYICONDATAW>() as u32,
            hWnd: hwnd,
            uID: 1,
            uFlags: NIF_ICON | NIF_MESSAGE | NIF_TIP,
            uCallbackMessage: WM_TRAYICON,
            hIcon: icon,
            szTip: tip,
            ..Default::default()
        };
        let _ = Shell_NotifyIconW(NIM_ADD, &nid);

        // Message loop
        let mut msg = MSG::default();
        while GetMessageW(&mut msg, None, 0, 0).as_bool() {
            let _ = TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }

        // Clean up
        nid.uFlags = NIF_ICON;
        let _ = Shell_NotifyIconW(NIM_DELETE, &nid);
        let _ = UnhookWindowsHookEx(hook);
    }

    Ok(())
}

unsafe extern "system" fn tray_wnd_proc(
    hwnd: HWND,
    msg: u32,
    wparam: WPARAM,
    lparam: LPARAM,
) -> LRESULT {
    match msg {
        WM_TRAYICON => {
            let event = (lparam.0 & 0xFFFF) as u32;
            if event == WM_RBUTTONUP {
                let menu = CreatePopupMenu().unwrap();
                let _ = AppendMenuW(menu, MENU_ITEM_FLAGS(0), IDM_EXIT as usize, windows::core::w!("Exit"));
                let mut pt = Default::default();
                let _ = GetCursorPos(&mut pt);
                let _ = SetForegroundWindow(hwnd);
                let _ = TrackPopupMenu(menu, TRACK_POPUP_MENU_FLAGS(0), pt.x, pt.y, 0, hwnd, None);
                let _ = DestroyMenu(menu);
            }
            LRESULT(0)
        }
        WM_COMMAND => {
            let id = (wparam.0 & 0xFFFF) as u16;
            if id == IDM_EXIT {
                PostQuitMessage(0);
            }
            LRESULT(0)
        }
        WM_DESTROY => {
            PostQuitMessage(0);
            LRESULT(0)
        }
        _ => DefWindowProcW(hwnd, msg, wparam, lparam),
    }
}

fn find_hook_dll() -> Result<String, Box<dyn std::error::Error>> {
    let exe_dir = std::env::current_exe()?
        .parent()
        .ok_or("no parent dir")?
        .to_path_buf();

    let candidate = exe_dir.join("picker_hook.dll");
    if candidate.exists() {
        return Ok(candidate.to_string_lossy().to_string());
    }

    Err(format!("picker_hook.dll not found (looked in {})", exe_dir.display()).into())
}
