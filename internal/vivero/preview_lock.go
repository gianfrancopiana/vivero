package vivero

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type previewLock struct {
	file *os.File
}

func (a *App) lockPreview(previewID string) (*previewLock, error) {
	return a.acquireLockFile(safePathComponent(previewID, "preview")+".lock", false)
}

func (a *App) tryLockPreview(previewID string) (*previewLock, bool, error) {
	lock, err := a.acquireLockFile(safePathComponent(previewID, "preview")+".lock", true)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return nil, false, nil
	}
	return lock, lock != nil && err == nil, err
}

func (a *App) lockRuntimeCapacity() (*previewLock, error) {
	return a.acquireLockFile("runtime-capacity.lock", false)
}

func (a *App) acquireLockFile(name string, nonblocking bool) (*previewLock, error) {
	dir := filepath.Join(a.Home, "run", "locks")
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	operation := syscall.LOCK_EX
	if nonblocking {
		operation |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", name, err)
	}
	return &previewLock{file: file}, nil
}

func (l *previewLock) unlock() {
	if l == nil || l.file == nil {
		return
	}
	file := l.file
	l.file = nil
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
