package tools

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

var broken struct {
	sync.Mutex
	path string
	mod  time.Time
	size int64
	err  error
}

// Broken returns the parse error of home's tools.toml, naming the file and
// line, or nil when it parses or is absent. It re-reads the file only when its
// size or mtime changed, so it is cheap enough to call on every tool result.
func Broken(home string) error {
	path := Path(home)
	fi, err := os.Stat(path)
	if err != nil {
		return nil // absent or unreadable: LoadWith treats it the same way
	}
	broken.Lock()
	defer broken.Unlock()
	if broken.path == path && broken.mod.Equal(fi.ModTime()) && broken.size == fi.Size() {
		return broken.err
	}
	broken.path, broken.mod, broken.size, broken.err = path, fi.ModTime(), fi.Size(), nil
	if _, err := conf.ParseFile(path); err != nil {
		broken.err = fmt.Errorf("%s: %w", path, err)
	}
	return broken.err
}
