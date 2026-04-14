package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/nelsong6/fzt/core"
	"github.com/nelsong6/fzt-terminal/tui"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	user32                = syscall.NewLazyDLL("user32.dll")
	allocConsole          = kernel32.NewProc("AllocConsole")
	freeConsole           = kernel32.NewProc("FreeConsole")
	getConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	setForegroundWindowFn = user32.NewProc("SetForegroundWindow")
	setConsoleTitleW      = kernel32.NewProc("SetConsoleTitleW")
)

//export PickFile
func PickFile(filterC *C.char, foldersOnly C.int) *C.char {
	filter := ""
	if filterC != nil {
		filter = C.GoString(filterC)
	}

	result := runPicker(filter, foldersOnly != 0)
	if result == "" {
		return nil
	}
	return C.CString(result)
}

//export FreeString
func FreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}

func runPicker(filter string, foldersOnly bool) string {
	title := "Pick a file"
	if foldersOnly {
		title = "Pick a folder"
	}

	items, err := queryEverything(filter, foldersOnly)
	if err != nil || len(items) == 0 {
		return ""
	}

	headerItem := core.Item{Fields: []string{"Name", "Path"}, Depth: -1}
	items = append([]core.Item{headerItem}, items...)

	cfg := tui.Config{
		Layout:       "reverse",
		Border:       true,
		Tiered:       true,
		DepthPenalty: 5,
		HeaderLines:  1,
		Nth:          []int{1},
		AcceptNth:    []int{2},
		Title:        title,
		TreeMode:     true,
		FrontendName: "picker",
	}

	// Allocate a console for the TUI to render into.
	// This creates a visible console window attached to the host process.
	allocConsole.Call()
	defer freeConsole.Call()

	// Set console title
	titleW, _ := syscall.UTF16PtrFromString(title)
	setConsoleTitleW.Call(uintptr(unsafe.Pointer(titleW)))

	// Bring the console window to the foreground
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		setForegroundWindowFn.Call(hwnd)
	}

	// Reopen stdin/stdout/stderr to the new console so tcell can use it
	conin, _ := os.OpenFile("CONIN$", os.O_RDWR, 0)
	conout, _ := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	os.Stdin = conin
	os.Stdout = conout
	os.Stderr = conout

	result, err := tui.Run(items, cfg)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(result)
}

// queryEverything queries the Everything CLI and builds an item tree.
func queryEverything(filterPattern string, foldersOnly bool) ([]core.Item, error) {
	esPath, err := findES()
	if err != nil {
		return nil, err
	}

	args := []string{"-n", "50000"}
	if foldersOnly {
		args = append(args, "/ad")
	} else {
		args = append(args, "/a-d")
	}

	var searchArgs []string
	if filterPattern != "" {
		exts := parseExtensions(filterPattern)
		if len(exts) > 0 {
			searchArgs = append(searchArgs, "ext:"+strings.Join(exts, ";"))
		}
	}
	searchArgs = append(searchArgs, `!.git\`, `!$Recycle.Bin\`, `!node_modules\`)

	cmd := exec.Command(esPath, append(args, searchArgs...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("es.exe failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, nil
	}

	return buildTree(lines), nil
}

func parseExtensions(pattern string) []string {
	var exts []string
	for _, p := range strings.Split(pattern, ";") {
		p = strings.TrimSpace(p)
		if ext, ok := strings.CutPrefix(p, "*."); ok {
			if ext != "*" && ext != "" {
				exts = append(exts, ext)
			}
		}
	}
	return exts
}

type node struct {
	name     string
	fullPath string
	children map[string]*node
	order    []string
}

func buildTree(paths []string) []core.Item {
	root := &node{children: make(map[string]*node)}

	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		parts := splitPath(p)
		if len(parts) == 0 {
			continue
		}

		current := root
		for i, part := range parts {
			if current.children[part] == nil {
				current.children[part] = &node{
					name:     part,
					children: make(map[string]*node),
				}
				current.order = append(current.order, part)
			}
			if i == len(parts)-1 {
				current.children[part].fullPath = p
			}
			current = current.children[part]
		}
	}

	var items []core.Item
	var walk func(n *node, depth int, parentIdx int)
	walk = func(n *node, depth int, parentIdx int) {
		childNames := make([]string, len(n.order))
		copy(childNames, n.order)
		sort.SliceStable(childNames, func(i, j int) bool {
			ci := n.children[childNames[i]]
			cj := n.children[childNames[j]]
			iIsFolder := len(ci.children) > 0
			jIsFolder := len(cj.children) > 0
			if iIsFolder != jIsFolder {
				return iIsFolder
			}
			return strings.ToLower(childNames[i]) < strings.ToLower(childNames[j])
		})

		for _, name := range childNames {
			child := n.children[name]
			isFolder := len(child.children) > 0

			idx := len(items)
			item := core.Item{
				Fields:      []string{child.name, child.fullPath},
				Depth:       depth,
				ParentIdx:   parentIdx,
				HasChildren: isFolder,
			}
			items = append(items, item)

			if isFolder {
				walk(child, depth+1, idx)
				for ci := idx + 1; ci < len(items); ci++ {
					if items[ci].ParentIdx == idx {
						items[idx].Children = append(items[idx].Children, ci)
					}
				}
			}
		}
	}

	walk(root, 0, -1)
	return items
}

func splitPath(p string) []string {
	p = filepath.Clean(p)
	var parts []string
	for p != "" {
		dir, file := filepath.Split(p)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		if dir == p {
			drive := strings.TrimRight(dir, `\/`)
			if drive != "" {
				parts = append([]string{drive}, parts...)
			}
			break
		}
		p = strings.TrimRight(dir, `\/`)
	}
	return parts
}

func findES() (string, error) {
	if path, err := exec.LookPath("es"); err == nil {
		if strings.Contains(strings.ToLower(path), "everything") {
			return path, nil
		}
	}

	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		winget := filepath.Join(local,
			"Microsoft", "WinGet", "Packages",
			"voidtools.Everything.Cli_Microsoft.Winget.Source_8wekyb3d8bbwe",
			"es.exe")
		if _, err := os.Stat(winget); err == nil {
			return winget, nil
		}
	}

	for _, p := range []string{
		`C:\Program Files\Everything\es.exe`,
		`C:\Program Files\Everything 1.5a\es.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("es.exe not found")
}

func main() {}
