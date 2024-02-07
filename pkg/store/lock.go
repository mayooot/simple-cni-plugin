package store

import (
	"os"
	"path/filepath"

	"github.com/alexflint/go-filemutex"
)

// 如果 lockPath 是目录，锁住的是 lockPath/lock 这个文件
// 如果 lockPath 是文件，锁住的就是 lockPath 这个文件
func newFileLock(lockPath string) (*filemutex.FileMutex, error) {
	stat, err := os.Stat(lockPath)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		lockPath = filepath.Join(lockPath, "lock")
	}

	f, err := filemutex.New(lockPath)
	if err != nil {
		return nil, err
	}
	return f, nil
}
