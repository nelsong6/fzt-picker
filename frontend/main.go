// picker-frontend — fzt-powered file picker TUI.
//
// Standalone frontend for the picker. Uses DirProvider for lazy tree browsing
// with hidden/system file filtering. Starts from drive roots or a specified directory.
//
// Usage:
//
//	picker-frontend
//	picker-frontend --folders-only
//	picker-frontend --start-dir "D:\repos"
package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/nelsong6/fzt/core"
	"github.com/nelsong6/fzt-terminal/tui"

	"github.com/nelsong6/fzt-picker/frontend/picker"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	user32              = syscall.NewLazyDLL("user32.dll")
	getConsoleWindow    = kernel32.NewProc("GetConsoleWindow")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
)

func bringToFront() {
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		setForegroundWindow.Call(hwnd)
	}
}

func main() {
	foldersOnly := false
	title := ""
	startDir := ""

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--folders-only":
			foldersOnly = true
		case "--title":
			if i+1 < len(args) {
				title = args[i+1]
				i++
			}
		case "--start-dir":
			if i+1 < len(args) {
				startDir = args[i+1]
				i++
			}
		}
	}

	provider := picker.NewDirProvider(foldersOnly)

	var items []core.Item
	if startDir != "" {
		items = provider.LoadChildren(startDir)
	} else {
		items = core.ListDriveRoots()
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "picker-frontend: no items found")
		os.Exit(1)
	}

	headerItem := picker.HeaderItem("Name")
	items = append([]core.Item{headerItem}, items...)

	cfg := picker.NewConfig(picker.Options{
		FoldersOnly: foldersOnly,
		StartDir:    startDir,
		Provider:    provider,
		AcceptNth:   []int{1},
		Title:       title,
	})

	bringToFront()

	result, err := tui.Run(items, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "picker-frontend: %v\n", err)
		os.Exit(1)
	}

	if result == "" {
		os.Exit(130)
	}

	fmt.Println(strings.TrimSpace(result))
}
