// Package cgroup drops a closed agent's page cache from its cgroup v2
// scope (DESIGN §7, ADR-139). Without it the cache stays charged to the
// dying cgroup and counts against the parent slice's MemoryHigh.
package cgroup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procRoot and fsRoot are /proc and the cgroup v2 mount; tests point them
// at a temp dir.
var (
	procRoot = "/proc"
	fsRoot   = "/sys/fs/cgroup"
)

// ErrNoScope: pid is in no cgroup v2 group below the root (no cgroup v2,
// or not Linux).
var ErrNoScope = errors.New("no cgroup v2 scope")

// Of returns the cgroup v2 directory pid runs in.
func Of(pid int) (string, error) {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", fmt.Errorf("read cgroup of %d: %w", pid, err)
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if rel, ok := strings.CutPrefix(l, "0::"); ok && rel != "/" {
			return filepath.Join(fsRoot, rel), nil
		}
	}
	return "", ErrNoScope
}

// ReclaimCache asks the kernel to drop the file pages charged to dir
// (swappiness=0: page cache only, nothing is pushed to swap) and returns
// how many bytes it asked for. It stops waiting when ctx ends; the write
// then finishes on its own, as the kernel bounds it.
func ReclaimCache(ctx context.Context, dir string) (uint64, error) {
	n, err := fileBytes(dir)
	if err != nil || n == 0 {
		return 0, err
	}
	done := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(filepath.Join(dir, "memory.reclaim"), os.O_WRONLY, 0)
		if err == nil {
			_, err = fmt.Fprintf(f, "%d swappiness=0", n)
			err = errors.Join(err, f.Close())
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return n, fmt.Errorf("reclaim %s: %w", dir, err)
		}
		return n, nil
	case <-ctx.Done():
		return n, fmt.Errorf("reclaim %s: %w", dir, ctx.Err())
	}
}

// fileBytes is the "file" line of dir's memory.stat: its page cache.
func fileBytes(dir string) (uint64, error) {
	f, err := os.Open(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return 0, fmt.Errorf("read memory.stat: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "file "); ok {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse memory.stat file: %w", err)
			}
			return n, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read memory.stat: %w", err)
	}
	return 0, nil
}
