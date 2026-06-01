//go:build !unix

package logdup

import "fmt"

// Setup is not supported on non-Unix platforms.
func Setup(path string) (cleanup func(), err error) {
	_ = path
	return nil, fmt.Errorf("logdup: not supported on this platform")
}
