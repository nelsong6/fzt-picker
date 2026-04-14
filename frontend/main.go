// picker-frontend — fzt-powered file picker TUI.
//
// Launched by picker's COM hook DLL when an app opens a file dialog.
// Queries Everything for files, builds a tree, presents an interactive
// picker via fzt, and prints the selected path to stdout.
//
// Usage:
//
//	picker-frontend
//	picker-frontend --filter "*.txt"
//	picker-frontend --folders-only
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/nelsong6/fzt/core"
	"github.com/nelsong6/fzt-terminal/tui"
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
	filter := ""
	foldersOnly := false
	title := "Pick a file"

	startDir := ""

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--filter":
			if i+1 < len(args) {
				filter = args[i+1]
				i++
			}
		case "--folders-only":
			foldersOnly = true
			title = "Pick a folder"
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

	// Use DirProvider for lazy loading if start dir is specified, otherwise fall back to Everything
	var items []core.Item
	var provider *core.DirProvider

	if startDir != "" {
		provider = core.NewDirProvider()
		items = provider.LoadChildren(startDir)
	}

	if len(items) == 0 {
		var err error
		items, err = queryEverything(filter, foldersOnly)
		if err != nil {
			fmt.Fprintf(os.Stderr, "picker-frontend: %v\n", err)
			os.Exit(1)
		}
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "picker-frontend: no files found")
		os.Exit(1)
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
		Provider:     provider,
		FocusedDir:   startDir,
	}

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

// queryEverything queries the Everything CLI and builds a flat item list
// with tree structure (drive > folders > files).
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

	// File extension filter (e.g. "*.txt;*.md" → "ext:txt;md")
	var searchArgs []string
	if filterPattern != "" {
		exts := parseExtensions(filterPattern)
		if len(exts) > 0 {
			searchArgs = append(searchArgs, "ext:"+strings.Join(exts, ";"))
		}
	}

	// Exclusions
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

// parseExtensions extracts extensions from a filter like "*.txt;*.md"
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

// buildTree takes a list of absolute paths and builds a hierarchical item tree
// grouped by drive letter, then by directory.
func buildTree(paths []string) []core.Item {
	type node struct {
		name     string
		fullPath string // only set for leaves
		children map[string]*node
		order    []string // insertion order for children
	}

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
				// Leaf — store full path
				current.children[part].fullPath = p
			}
			current = current.children[part]
		}
	}

	// Flatten tree into core.Item list
	var items []core.Item
	var walk func(n *node, depth int, parentIdx int)
	walk = func(n *node, depth int, parentIdx int) {
		// Sort children: folders first, then alphabetical
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
				// Collect direct children indices
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

// splitPath breaks a Windows path into components: ["C:", "Users", "foo", "file.txt"]
func splitPath(p string) []string {
	p = filepath.Clean(p)
	var parts []string
	for p != "" {
		dir, file := filepath.Split(p)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		if dir == p {
			// Drive root like "C:\"
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
	// Check PATH
	if path, err := exec.LookPath("es"); err == nil {
		if strings.Contains(strings.ToLower(path), "everything") {
			return path, nil
		}
	}

	// Check known winget location
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		winget := filepath.Join(local,
			"Microsoft", "WinGet", "Packages",
			"voidtools.Everything.Cli_Microsoft.Winget.Source_8wekyb3d8bbwe",
			"es.exe")
		if _, err := os.Stat(winget); err == nil {
			return winget, nil
		}
	}

	// Check Program Files
	for _, p := range []string{
		`C:\Program Files\Everything\es.exe`,
		`C:\Program Files\Everything 1.5a\es.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("es.exe not found (install Everything CLI)")
}
