package desktop

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DesktopEntry represents a parsed .desktop file
type DesktopEntry struct {
	Name       string            // Default name
	Names      map[string]string // Localized names (locale -> name)
	Exec       string            // Exec command
	Terminal   bool              // Whether to run in terminal
	Categories []string          // Application categories
	Path       string            // Path to .desktop file
}

// GetDesktopPaths returns the standard desktop file locations
func GetDesktopPaths() []string {
	return []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
		filepath.Join(os.Getenv("HOME"), ".local/share/applications"),
	}
}

// ScanDesktopFiles scans for .desktop files in standard locations
func ScanDesktopFiles(resultChan chan<- *DesktopEntry) error {
	defer close(resultChan)

	paths := GetDesktopPaths()

	for _, path := range paths {
		if err := scanDesktopPath(path, resultChan); err != nil {
			// Continue scanning other paths
			continue
		}
	}

	return nil
}

func scanDesktopPath(rootPath string, resultChan chan<- *DesktopEntry) error {
	return filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".desktop") {
			return nil
		}

		entry, err := ParseDesktopFile(path)
		if err != nil {
			// Skip invalid files
			return nil
		}

		resultChan <- entry
		return nil
	})
}

// ParseDesktopFile parses a single .desktop file
func ParseDesktopFile(path string) (*DesktopEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entry := &DesktopEntry{
		Path:  path,
		Names: make(map[string]string),
	}

	scanner := bufio.NewScanner(file)
	var currentSection string
	var inDesktopEntry bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			if currentSection == "Desktop Entry" {
				inDesktopEntry = true
			} else {
				inDesktopEntry = false
			}
			continue
		}

		if !inDesktopEntry {
			continue
		}

		// Parse key=value pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Name":
			entry.Name = value
		case "Exec":
			entry.Exec = value
		case "Terminal":
			entry.Terminal = strings.ToLower(value) == "true"
		case "Categories":
			// Categories are semicolon-separated
			cats := strings.Split(value, ";")
			entry.Categories = make([]string, 0, len(cats))
			for _, cat := range cats {
				cat = strings.TrimSpace(cat)
				if cat != "" {
					entry.Categories = append(entry.Categories, cat)
				}
			}
		default:
			// Check for localized Name[locale]
			if strings.HasPrefix(key, "Name[") && strings.HasSuffix(key, "]") {
				locale := key[5 : len(key)-1]
				entry.Names[locale] = value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Validate required fields
	if entry.Name == "" && entry.Exec == "" {
		return nil, fmt.Errorf("missing required fields")
	}

	// Set default name if not set
	if entry.Name == "" {
		// Use filename without extension
		baseName := filepath.Base(path)
		entry.Name = strings.TrimSuffix(baseName, ".desktop")
	}

	return entry, nil
}

// GetLocalizedName returns the localized name for the given locale, or default name
func (d *DesktopEntry) GetLocalizedName(locale string) string {
	if locale == "" {
		return d.Name
	}

	// Try exact match
	if name, ok := d.Names[locale]; ok {
		return name
	}

	// Try language part (e.g., "en" from "en_US")
	if idx := strings.Index(locale, "_"); idx > 0 {
		lang := locale[:idx]
		if name, ok := d.Names[lang]; ok {
			return name
		}
	}

	// Try language part (e.g., "en" from "en-US")
	if idx := strings.Index(locale, "-"); idx > 0 {
		lang := locale[:idx]
		if name, ok := d.Names[lang]; ok {
			return name
		}
	}

	// Fallback to default
	return d.Name
}

// ExpandExecCommand expands %-codes in Exec command
func (d *DesktopEntry) ExpandExecCommand(filePath string) string {
	exec := d.Exec

	// Replace common field codes
	exec = strings.ReplaceAll(exec, "%f", filePath)
	exec = strings.ReplaceAll(exec, "%F", filePath)
	exec = strings.ReplaceAll(exec, "%u", filePath)
	exec = strings.ReplaceAll(exec, "%U", filePath)
	exec = strings.ReplaceAll(exec, "%i", "")
	exec = strings.ReplaceAll(exec, "%c", d.Name)
	exec = strings.ReplaceAll(exec, "%k", d.Path)

	// Remove % codes that we don't handle
	exec = removeFieldCodes(exec)

	return exec
}

func removeFieldCodes(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '%' && i+1 < len(s) {
			// Skip % and next character if it's a known code
			next := s[i+1]
			if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || next == '%' {
				if next == '%' {
					result.WriteByte('%')
				}
				i += 2
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// IsNoDisplay checks if the entry should be hidden (requires parsing NoDisplay key)
func IsNoDisplay(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var inDesktopEntry bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.Trim(line, "[]")
			inDesktopEntry = (section == "Desktop Entry")
			continue
		}

		if !inDesktopEntry {
			continue
		}

		if after, ok := strings.CutPrefix(line, "NoDisplay="); ok {
			value := strings.TrimSpace(after)
			return strings.ToLower(value) == "true"
		}
	}

	return false
}

// CleanExecCommand removes field codes and extra spaces from exec command
func CleanExecCommand(exec string) string {
	// Remove field codes
	exec = removeFieldCodes(exec)

	// Clean up whitespace
	fields := strings.Fields(exec)
	return strings.Join(fields, " ")
}
