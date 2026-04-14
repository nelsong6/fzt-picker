package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/gdamore/tcell/v2"
	"github.com/nelsong6/fzt/core"
	"github.com/nelsong6/fzt/render"
	"github.com/nelsong6/fzt-terminal/tui"
)

// Win32 API
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	getModuleHandle    = kernel32.NewProc("GetModuleHandleW")
	registerClassExW   = user32.NewProc("RegisterClassExW")
	createWindowExW    = user32.NewProc("CreateWindowExW")
	destroyWindowFn    = user32.NewProc("DestroyWindow")
	showWindowFn       = user32.NewProc("ShowWindow")
	updateWindow       = user32.NewProc("UpdateWindow")
	getMessageW        = user32.NewProc("GetMessageW")
	translateMessageFn = user32.NewProc("TranslateMessage")
	dispatchMessageFn  = user32.NewProc("DispatchMessageW")
	postQuitMessageFn  = user32.NewProc("PostQuitMessage")
	defWindowProcW     = user32.NewProc("DefWindowProcW")
	enableWindowFn     = user32.NewProc("EnableWindow")
	setForegroundWin   = user32.NewProc("SetForegroundWindow")
	setFocus           = user32.NewProc("SetFocus")
	invalidateRect     = user32.NewProc("InvalidateRect")
	beginPaint         = user32.NewProc("BeginPaint")
	endPaint           = user32.NewProc("EndPaint")
	getClientRect      = user32.NewProc("GetClientRect")
	setTimerFn         = user32.NewProc("SetTimer")
	killTimerFn        = user32.NewProc("KillTimer")
	getSystemMetrics   = user32.NewProc("GetSystemMetrics")
	setWindowPos       = user32.NewProc("SetWindowPos")

	// GDI
	createFontW        = gdi32.NewProc("CreateFontW")
	selectObject       = gdi32.NewProc("SelectObject")
	deleteObject       = gdi32.NewProc("DeleteObject")
	setTextColor       = gdi32.NewProc("SetTextColor")
	setBkColor         = gdi32.NewProc("SetBkColor")
	textOutW           = gdi32.NewProc("TextOutW")
	fillRect           = user32.NewProc("FillRect")
	createSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	getTextMetrics     = gdi32.NewProc("GetTextMetricsW")
)

// Win32 constants
const (
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_POPUP            = 0x80000000
	WS_VISIBLE          = 0x10000000
	WS_CAPTION          = 0x00C00000
	WS_SYSMENU          = 0x00080000
	WS_THICKFRAME       = 0x00040000
	WS_EX_TOOLWINDOW    = 0x00000080
	CW_USEDEFAULT       = ^int32(0x7FFFFFFF)
	SW_SHOW             = 5
	WM_DESTROY          = 0x0002
	WM_CLOSE            = 0x0010
	WM_PAINT            = 0x000F
	WM_CHAR             = 0x0102
	WM_KEYDOWN          = 0x0100
	WM_TIMER            = 0x0113
	WM_SIZE             = 0x0005
	WM_ERASEBKGND       = 0x0014
	VK_UP               = 0x26
	VK_DOWN             = 0x28
	VK_LEFT             = 0x25
	VK_RIGHT            = 0x27
	VK_RETURN           = 0x0D
	VK_ESCAPE           = 0x1B
	VK_BACK             = 0x08
	VK_TAB              = 0x09
	VK_DELETE            = 0x2E
	VK_HOME             = 0x24
	VK_END              = 0x23
	VK_PRIOR            = 0x21 // Page Up
	VK_NEXT             = 0x22 // Page Down
	SM_CXSCREEN         = 0
	SM_CYSCREEN         = 1
	SWP_NOZORDER        = 0x0004
	TIMER_CURSOR        = 1
)

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type RECT struct {
	Left, Top, Right, Bottom int32
}

type PAINTSTRUCT struct {
	HDC         uintptr
	Erase       int32
	RcPaint     RECT
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

type TEXTMETRIC struct {
	Height           int32
	Ascent           int32
	Descent          int32
	InternalLeading  int32
	ExternalLeading  int32
	AveCharWidth     int32
	MaxCharWidth     int32
	Weight           int32
	Overhang         int32
	DigitizedAspectX int32
	DigitizedAspectY int32
	FirstChar        uint16
	LastChar         uint16
	DefaultChar      uint16
	BreakChar        uint16
	Italic           byte
	Underlined       byte
	StruckOut        byte
	PitchAndFamily   byte
	CharSet          byte
}

// Picker state — accessed from wndProc
var (
	pickerMu      sync.Mutex
	pickerSession *render.Session
	pickerFrame   render.SessionFrame
	pickerGrid    [][]core.StyledRune
	pickerResult  string
	pickerHwnd    uintptr
	ownerHwnd     uintptr
	charW, charH  int
	font          uintptr
	fontBold      uintptr
	fontItalic    uintptr
	gridCols      int
	gridRows      int
)

//export PickFile
func PickFile(filterC *C.char, foldersOnly C.int, startDirC *C.char, hwndOwner uintptr) *C.char {
	filter := ""
	if filterC != nil {
		filter = C.GoString(filterC)
	}
	startDir := ""
	if startDirC != nil {
		startDir = C.GoString(startDirC)
	}
	_ = filter // TODO: use for filtering

	result := runPicker(foldersOnly != 0, startDir, hwndOwner)
	if result == "" {
		return nil
	}
	return C.CString(result)
}

//export FreeString
func FreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}

func runPicker(foldersOnly bool, startDir string, hwndOwner uintptr) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ownerHwnd = hwndOwner
	pickerResult = ""

	title := "Pick a file"
	if foldersOnly {
		title = "Pick a folder"
	}

	// Create the window first — this computes gridCols/gridRows from screen size
	hwnd := createPickerWindow(title)
	if hwnd == 0 {
		return ""
	}
	pickerHwnd = hwnd

	// Build initial items using DirProvider wrapped to filter hidden files
	provider := &pickerDirProvider{inner: core.NewDirProvider()}
	var items []core.Item
	if startDir != "" {
		items = provider.LoadChildren(startDir)
	}
	if len(items) == 0 {
		items = core.ListDriveRoots()
	}

	headerItem := core.Item{Fields: []string{"Name"}, Depth: -1}
	items = append([]core.Item{headerItem}, items...)

	cfg := tui.Config{
		Layout:       "reverse",
		Border:       true,
		Tiered:       true,
		DepthPenalty: 5,
		HeaderLines:  1,
		Nth:          []int{1},
		AcceptNth:    []int{1},
		Title:        title,
		TreeMode:     true,
		FrontendName: "picker",
		Provider:     provider,
		FocusedDir:   startDir,
	}

	pickerSession = tui.NewTreeSession(items, cfg, gridCols, gridRows)

	// Disable owner (modal)
	if ownerHwnd != 0 {
		enableWindowFn.Call(ownerHwnd, 0)
	}

	// Show and focus
	showWindowFn.Call(hwnd, SW_SHOW)
	updateWindow.Call(hwnd)
	setForegroundWin.Call(hwnd)
	setFocus.Call(hwnd)

	// Initial render
	renderAndInvalidate()

	// Modal message loop
	var msg MSG
	for {
		ret, _, _ := getMessageW.Call(
			uintptr(unsafe.Pointer(&msg)),
			0, 0, 0,
		)
		if ret == 0 || ret == uintptr(^uintptr(0)) {
			break
		}
		translateMessageFn.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessageFn.Call(uintptr(unsafe.Pointer(&msg)))
	}

	// Re-enable owner
	if ownerHwnd != 0 {
		enableWindowFn.Call(ownerHwnd, 1)
		setForegroundWin.Call(ownerHwnd)
	}

	// Clean up
	destroyWindowFn.Call(hwnd)
	if font != 0 {
		deleteObject.Call(font)
		font = 0
	}
	if fontBold != 0 {
		deleteObject.Call(fontBold)
		fontBold = 0
	}
	if fontItalic != 0 {
		deleteObject.Call(fontItalic)
		fontItalic = 0
	}

	return pickerResult
}

func renderAndInvalidate() {
	pickerMu.Lock()
	pickerFrame = pickerSession.Render()
	pickerGrid = parseANSIGrid(pickerFrame.ANSI, gridCols, gridRows)
	pickerMu.Unlock()
	if pickerHwnd != 0 {
		invalidateRect.Call(pickerHwnd, 0, 1)
	}
}

func handleKeyInput(key tcell.Key, ch rune) {
	frame, action := pickerSession.HandleKey(key, ch)
	pickerMu.Lock()
	pickerFrame = frame
	pickerGrid = parseANSIGrid(frame.ANSI, gridCols, gridRows)
	pickerMu.Unlock()

	if strings.HasPrefix(action, "select:") {
		pickerResult = strings.TrimSpace(action[7:])
		postQuitMessageFn.Call(0)
		return
	}
	if action == "cancel" {
		pickerResult = ""
		postQuitMessageFn.Call(0)
		return
	}

	invalidateRect.Call(pickerHwnd, 0, 1)
}

// Window procedure
func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_KEYDOWN:
		switch wParam {
		case VK_UP:
			handleKeyInput(tcell.KeyUp, 0)
			return 0
		case VK_DOWN:
			handleKeyInput(tcell.KeyDown, 0)
			return 0
		case VK_LEFT:
			handleKeyInput(tcell.KeyLeft, 0)
			return 0
		case VK_RIGHT:
			handleKeyInput(tcell.KeyRight, 0)
			return 0
		case VK_RETURN:
			handleKeyInput(tcell.KeyEnter, 0)
			return 0
		case VK_ESCAPE:
			handleKeyInput(tcell.KeyEscape, 0)
			return 0
		case VK_BACK:
			handleKeyInput(tcell.KeyBackspace2, 0)
			return 0
		case VK_TAB:
			handleKeyInput(tcell.KeyTab, 0)
			return 0
		case VK_DELETE:
			handleKeyInput(tcell.KeyDelete, 0)
			return 0
		case VK_HOME:
			handleKeyInput(tcell.KeyHome, 0)
			return 0
		case VK_END:
			handleKeyInput(tcell.KeyEnd, 0)
			return 0
		case VK_PRIOR:
			handleKeyInput(tcell.KeyPgUp, 0)
			return 0
		case VK_NEXT:
			handleKeyInput(tcell.KeyPgDn, 0)
			return 0
		}
	case WM_CHAR:
		ch := rune(wParam)
		if ch >= 32 { // printable
			handleKeyInput(tcell.KeyRune, ch)
			return 0
		}
		// Ctrl+key combos arrive as control characters
		switch ch {
		case 0x03: // Ctrl+C
			handleKeyInput(tcell.KeyCtrlC, 0)
			return 0
		case 0x15: // Ctrl+U
			handleKeyInput(tcell.KeyCtrlU, 0)
			return 0
		case 0x17: // Ctrl+W
			handleKeyInput(tcell.KeyCtrlW, 0)
			return 0
		}
	case WM_PAINT:
		paintWindow(hwnd)
		return 0
	case WM_ERASEBKGND:
		return 1 // we handle background in WM_PAINT
	case WM_CLOSE:
		pickerResult = ""
		postQuitMessageFn.Call(0)
		return 0
	}
	ret, _, _ := defWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

var wndProcCb = syscall.NewCallback(wndProc)

func createPickerWindow(title string) uintptr {
	hInst, _, _ := getModuleHandle.Call(0)
	className, _ := syscall.UTF16PtrFromString("FztPicker")
	titleW, _ := syscall.UTF16PtrFromString(title)

	type WNDCLASSEX struct {
		Size       uint32
		Style      uint32
		WndProc    uintptr
		ClsExtra   int32
		WndExtra   int32
		Instance   uintptr
		Icon       uintptr
		Cursor     uintptr
		Background uintptr
		MenuName   uintptr
		ClassName  uintptr
		IconSm     uintptr
	}

	// Load arrow cursor
	loadCursor := user32.NewProc("LoadCursorW")
	cursor, _, _ := loadCursor.Call(0, 32512) // IDC_ARROW

	wc := WNDCLASSEX{
		Size:      uint32(unsafe.Sizeof(WNDCLASSEX{})),
		Style:     0x0003, // CS_HREDRAW | CS_VREDRAW
		WndProc:   wndProcCb,
		Instance:  hInst,
		ClassName: uintptr(unsafe.Pointer(className)),
		Cursor:    cursor,
	}
	registerClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	// Get DPI for font scaling — match Windows Terminal's point-based sizing
	getDpi := user32.NewProc("GetDpiForSystem")
	dpi, _, _ := getDpi.Call()
	if dpi == 0 {
		dpi = 96
	}
	// Convert points to pixels: -height for CreateFontW means character height in points
	fontSize := -int(int(dpi) * tui.DefaultFontSize / 72)

	fontName, _ := syscall.UTF16PtrFromString(tui.DefaultFontName)
	font, _, _ = createFontW.Call(
		uintptr(uint32(fontSize)),
		0, 0, 0,
		400, // weight (normal)
		0, 0, 0, // italic, underline, strikeout
		0, 0, 0, 0,
		uintptr(1), // FIXED_PITCH
		uintptr(unsafe.Pointer(fontName)),
	)
	fontBold, _, _ = createFontW.Call(
		uintptr(uint32(fontSize)),
		0, 0, 0,
		700, // weight (bold)
		0, 0, 0,
		0, 0, 0, 0,
		uintptr(1),
		uintptr(unsafe.Pointer(fontName)),
	)
	fontItalic, _, _ = createFontW.Call(
		uintptr(uint32(fontSize)),
		0, 0, 0,
		400,
		1, 0, 0, // italic=1
		0, 0, 0, 0,
		uintptr(1),
		uintptr(unsafe.Pointer(fontName)),
	)

	// Measure character size using a temp DC
	getDC := user32.NewProc("GetDC")
	releaseDC := user32.NewProc("ReleaseDC")
	dc, _, _ := getDC.Call(0)
	oldFont, _, _ := selectObject.Call(dc, font)
	var tm TEXTMETRIC
	getTextMetrics.Call(dc, uintptr(unsafe.Pointer(&tm)))
	charW = int(tm.AveCharWidth)
	charH = int(tm.Height)
	selectObject.Call(dc, oldFont)
	releaseDC.Call(0, dc)

	// 80% of screen, compute grid size from that
	screenW, _, _ := getSystemMetrics.Call(SM_CXSCREEN)
	screenH, _, _ := getSystemMetrics.Call(SM_CYSCREEN)
	winW := int(screenW) * 80 / 100
	winH := int(screenH) * 80 / 100

	// Compute grid dimensions from window size (subtract borders/title)
	clientW := winW - 16
	clientH := winH - 40
	gridCols = clientW / charW
	gridRows = clientH / charH

	x := (int(screenW) - winW) / 2
	y := (int(screenH) - winH) / 2

	style := uintptr(WS_POPUP | WS_CAPTION | WS_SYSMENU | WS_VISIBLE)

	hwnd, _, _ := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titleW)),
		style,
		uintptr(x), uintptr(y),
		uintptr(winW), uintptr(winH),
		ownerHwnd, // owner
		0, hInst, 0,
	)

	return hwnd
}

// colorToBGR converts a tcell color to GDI's BGR format using the shared
// palette from tui/style.go. The defaultRGB is used for tcell.ColorDefault
// (pass tui.TextFgRGB for foreground, tui.BaseBgRGB for background).
func colorToBGR(c tcell.Color, defaultRGB [3]uint8) uint32 {
	var r, g, b uint8
	if c == tcell.ColorDefault {
		r, g, b = defaultRGB[0], defaultRGB[1], defaultRGB[2]
	} else {
		r, g, b = tui.ColorToRGB(c)
	}
	return uint32(b)<<16 | uint32(g)<<8 | uint32(r)
}

var (
	extTextOutW = gdi32.NewProc("ExtTextOutW")
)

const ETO_OPAQUE = 0x0002

func paintWindow(hwnd uintptr) {
	var ps PAINTSTRUCT
	hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	// Select font
	oldFont, _, _ := selectObject.Call(hdc, font)

	pickerMu.Lock()
	grid := pickerGrid
	pickerMu.Unlock()

	if grid != nil {
		lastFont := font
		for y, row := range grid {
			x := 0
			for x < len(row) {
				cell := row[x]
				if cell.Char == 0 {
					cell.Char = ' '
				}
				fg, bg, attrs := cell.Style.Decompose()

				// Select font based on attributes
				wantFont := font
				if attrs&tcell.AttrBold != 0 {
					wantFont = fontBold
				} else if attrs&tcell.AttrItalic != 0 {
					wantFont = fontItalic
				}
				if wantFont != lastFont {
					selectObject.Call(hdc, wantFont)
					lastFont = wantFont
				}

				// Check if this is a wide character (icon)
				cp := int(cell.Char)
				isWide := cp > 0xFFFF || (cp >= 0xE000 && cp <= 0xF8FF)

				if isWide {
					// Draw wide character spanning 2 cells, centered
					setTextColor.Call(hdc, uintptr(colorToBGR(fg, tui.TextFgRGB)))
					setBkColor.Call(hdc, uintptr(colorToBGR(bg, tui.BaseBgRGB)))

					rc := RECT{
						Left:   int32(x * charW),
						Top:    int32(y * charH),
						Right:  int32((x + 2) * charW),
						Bottom: int32((y + 1) * charH),
					}

					// Fill background first
					extTextOutW.Call(hdc,
						uintptr(rc.Left), uintptr(rc.Top),
						ETO_OPAQUE, uintptr(unsafe.Pointer(&rc)),
						0, 0, 0,
					)

					// Center the icon in the 2-cell rect
					chars := syscall.StringToUTF16(string(cell.Char))
					charLen := len(chars) - 1
					if charLen < 1 {
						charLen = 1
					}
					// Draw centered: offset by half a cell width
					iconX := int32(x*charW) + int32(charW/2)
					textOutW.Call(hdc,
						uintptr(iconX),
						uintptr(rc.Top),
						uintptr(unsafe.Pointer(&chars[0])),
						uintptr(charLen),
					)
					x += 2
					continue
				}

				// Collect run of same-style non-wide cells
				var run []rune
				runStart := x
				for x < len(row) {
					c := row[x]
					if c.Char == 0 {
						c.Char = ' '
					}
					cp := int(c.Char)
					if cp > 0xFFFF || (cp >= 0xE000 && cp <= 0xF8FF) {
						break // wide char — draw separately
					}
					cfG, cbG, cAttrs := c.Style.Decompose()
					if cfG != fg || cbG != bg || cAttrs != attrs {
						break
					}
					run = append(run, c.Char)
					x++
				}

				setTextColor.Call(hdc, uintptr(colorToBGR(fg, tui.TextFgRGB)))
				setBkColor.Call(hdc, uintptr(colorToBGR(bg, tui.BaseBgRGB)))

				rc := RECT{
					Left:   int32(runStart * charW),
					Top:    int32(y * charH),
					Right:  int32(x * charW),
					Bottom: int32((y + 1) * charH),
				}

				chars := syscall.StringToUTF16(string(run))
				charLen := len(chars) - 1
				if charLen < 1 {
					charLen = 1
				}
				extTextOutW.Call(hdc,
					uintptr(rc.Left),
					uintptr(rc.Top),
					ETO_OPAQUE,
					uintptr(unsafe.Pointer(&rc)),
					uintptr(unsafe.Pointer(&chars[0])),
					uintptr(charLen),
					0,
				)
			}
		}
	} else {
		// No grid yet — fill with background
		var rc RECT
		getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		setBkColor.Call(hdc, uintptr(colorToBGR(tcell.ColorDefault, tui.BaseBgRGB)))
		extTextOutW.Call(hdc, 0, 0, ETO_OPAQUE, uintptr(unsafe.Pointer(&rc)), 0, 0, 0)
	}

	selectObject.Call(hdc, oldFont)
	endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
}

// StyledCell holds a character and its style from the ANSI grid
// We need this exported from render package, or we parse ANSI ourselves

// parseANSIGrid parses ANSI output into a grid of styled cells
func parseANSIGrid(ansi string, cols, rows int) [][]core.StyledRune {
	// Use core.ParseANSI to get styled runes per line
	lines := strings.Split(ansi, "\n")
	grid := make([][]core.StyledRune, rows)

	for y := 0; y < rows; y++ {
		grid[y] = make([]core.StyledRune, cols)
		for x := range grid[y] {
			grid[y][x] = core.StyledRune{Char: ' ', Style: tcell.StyleDefault}
		}

		if y < len(lines) {
			styled := core.ParseANSI(lines[y])
			for x, sr := range styled {
				if x >= cols {
					break
				}
				grid[y][x] = core.StyledRune{
					Char:  sr.Char,
					Style: sr.Style,
				}
			}
		}
	}

	return grid
}

// pickerDirProvider wraps DirProvider and filters out hidden/system files on Windows.
type pickerDirProvider struct {
	inner *core.DirProvider
}

func (p *pickerDirProvider) LoadChildren(parentPath string) []core.Item {
	items := p.inner.LoadChildren(parentPath)
	var filtered []core.Item
	for _, item := range items {
		name := item.Fields[0]
		fullPath := filepath.Join(parentPath, name)
		if isHiddenFile(fullPath) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func isHiddenFile(path string) bool {
	pathW, _ := syscall.UTF16PtrFromString(path)
	attrs, err := syscall.GetFileAttributes(pathW)
	if err != nil {
		return false
	}
	const FILE_ATTRIBUTE_HIDDEN = 0x2
	const FILE_ATTRIBUTE_SYSTEM = 0x4
	return attrs&(FILE_ATTRIBUTE_HIDDEN|FILE_ATTRIBUTE_SYSTEM) != 0
}

func main() {}
