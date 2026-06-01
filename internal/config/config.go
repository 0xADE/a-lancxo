package config

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/kelseyhightower/envconfig"
)

const idxrc = "~/.config/ade/indexd.rc"

var (
	globalConfig *config
	once         sync.Once
)

type config struct {
	static  env
	dynamic rc
	watcher *fsnotify.Watcher
}

type (
	env struct {
		Path       string `envconfig:"PATH"`
		Terminal   string `envconfig:"ADE_DEFAULT_TERM"`
		UnixSocket string `envconfig:"ADE_INDEXD_SOCK"`
		Workers    int    `envconfig:"ADE_INDEXD_WORKERS" default:"4"`
		ListLimit  int    `envconfig:"ADE_INDEXD_LIST_LIMIT" default:"128"`
		LogPath    string `envconfig:"ADE_CTLD_LOG"`
	}
	rc struct {
		sync.RWMutex
		additionalPaths []string
	}
)

// Init initializes and loads configuration
func Init() error {
	var err error
	once.Do(func() {
		globalConfig = &config{}

		// Load environment variables
		if err = envconfig.Process("", &globalConfig.static); err != nil {
			return
		}

		// Set default socket path if not provided
		if globalConfig.static.UnixSocket == "" {
			currentUser, err := user.Current()
			if err != nil {
				return
			}
			globalConfig.static.UnixSocket = fmt.Sprintf("/tmp/ade-%s/indexd", currentUser.Uid)
		}

		// Expand tilde in socket path
		if strings.HasPrefix(globalConfig.static.UnixSocket, "~") {
			home, err := os.UserHomeDir()
			if err != nil {
				return
			}
			globalConfig.static.UnixSocket = strings.Replace(globalConfig.static.UnixSocket, "~", home, 1)
		}

		if globalConfig.static.LogPath != "" {
			globalConfig.static.LogPath = expandPath(globalConfig.static.LogPath)
		}

		// Load rc file
		if err = globalConfig.loadRC(); err != nil {
			return
		}

		// Setup file watcher
		if err = globalConfig.setupWatcher(); err != nil {
			return
		}
	})
	return err
}

// Run starts the configuration watcher loop
func Run() error {
	if globalConfig == nil {
		if err := Init(); err != nil {
			return err
		}
	}

	go globalConfig.watchLoop()
	return nil
}

// Get returns the global config instance
func Get() *config {
	if globalConfig == nil {
		Init()
	}
	return globalConfig
}

func (c *config) loadRC() error {
	rcPath := expandPath(idxrc)

	// Create directory if it doesn't exist
	rcDir := filepath.Dir(rcPath)
	if err := os.MkdirAll(rcDir, 0750); err != nil {
		return err
	}

	// Try to read rc file
	file, err := os.Open(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create empty file
			file, err = os.Create(rcPath)
			if err != nil {
				return err
			}
			file.Close()
			return nil
		}
		return err
	}
	defer file.Close()

	c.dynamic.Lock()
	defer c.dynamic.Unlock()

	c.dynamic.additionalPaths = []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		expanded := expandPath(line)
		c.dynamic.additionalPaths = append(c.dynamic.additionalPaths, expanded)
	}

	return scanner.Err()
}

func (c *config) setupWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	c.watcher = watcher
	rcPath := expandPath(idxrc)
	rcDir := filepath.Dir(rcPath)

	// Watch the directory
	if err := watcher.Add(rcDir); err != nil {
		return err
	}

	return nil
}

func (c *config) watchLoop() {
	for {
		select {
		case event, ok := <-c.watcher.Events:
			if !ok {
				return
			}
			rcPath := expandPath(idxrc)
			if event.Name == rcPath && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) {
				if err := c.loadRC(); err != nil {
					// Log error but continue
					fmt.Fprintf(os.Stderr, "Error reloading config: %v\n", err)
				}
			}
		case err, ok := <-c.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "Config watcher error: %v\n", err)
		}
	}
}

// Path returns all paths to search (PATH + additional paths from rc)
func (c *config) Path() []string {
	c.dynamic.RLock()
	defer c.dynamic.RUnlock()

	paths := strings.Split(c.static.Path, ":")
	// Filter empty paths
	filtered := make([]string, 0, len(paths)+len(c.dynamic.additionalPaths))
	for _, p := range paths {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	filtered = append(filtered, c.dynamic.additionalPaths...)
	return filtered
}

// Terminal returns the default terminal command
func (c *config) Terminal() string {
	if c.static.Terminal != "" {
		return c.static.Terminal
	}
	// Fallback to TERM env var
	if term := os.Getenv("TERM"); term != "" {
		return term
	}
	return "xterm" // Ultimate fallback
}

// UnixSocket returns the Unix socket path
func (c *config) UnixSocket() string {
	return c.static.UnixSocket
}

// Workers returns the number of worker goroutines for indexing
func (c *config) Workers() int {
	if c.static.Workers <= 0 {
		return 4 // Default
	}
	return c.static.Workers
}

// ListLimit returns the configured list limit
func (c *config) ListLimit() int {
	if c.static.ListLimit <= 0 {
		return 128 // Default
	}
	return c.static.ListLimit
}

// LogPath returns the path for duplicate process logging (stdout/stderr), or empty if disabled.
func (c *config) LogPath() string {
	return c.static.LogPath
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return strings.Replace(path, "~", home, 1)
	}
	return path
}
