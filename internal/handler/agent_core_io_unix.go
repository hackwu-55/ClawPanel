//go:build !windows

package handler

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func splitUnixAbsolutePathSegments(path string) []string {
	clean := filepath.Clean(path)
	if clean == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(clean, "/"), "/")
}

func openDirSegments(baseFD int, segments []string, create bool) (int, error) {
	current := baseFD
	for _, segment := range segments {
		next, err := unix.Openat(current, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil && create && err == unix.ENOENT {
			if mkErr := unix.Mkdirat(current, segment, 0o755); mkErr != nil && mkErr != unix.EEXIST {
				if current != baseFD {
					_ = unix.Close(current)
				}
				return -1, mkErr
			}
			next, err = unix.Openat(current, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			if current != baseFD {
				_ = unix.Close(current)
			}
			if !create && err == unix.ENOENT {
				return -1, os.ErrNotExist
			}
			if err == unix.ELOOP {
				return -1, errAgentCoreFileWorkspaceSymlink
			}
			return -1, err
		}
		if current != baseFD {
			_ = unix.Close(current)
		}
		current = next
	}
	return current, nil
}

func openAgentCoreWorkspaceDir(root, rel string, create bool) (int, error) {
	if !filepath.IsAbs(root) {
		return -1, fmt.Errorf("workspace root 必须是绝对路径")
	}
	baseFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, err
	}
	rootFD, err := openDirSegments(baseFD, splitUnixAbsolutePathSegments(root), create)
	if err != nil {
		_ = unix.Close(baseFD)
		return -1, err
	}
	if rootFD == baseFD {
		rootFD, err = unix.Dup(baseFD)
		if err != nil {
			_ = unix.Close(baseFD)
			return -1, err
		}
	}
	_ = unix.Close(baseFD)
	workspaceFD, err := openDirSegments(rootFD, splitPathSegments(rel), create)
	if err != nil {
		_ = unix.Close(rootFD)
		return -1, err
	}
	if workspaceFD == rootFD {
		return rootFD, nil
	}
	_ = unix.Close(rootFD)
	return workspaceFD, nil
}

func loadAgentCoreFile(workspace agentCoreWorkspaceLocation, name string) (string, int64, time.Time, bool, error) {
	dirFD, err := openAgentCoreWorkspaceDir(workspace.Root, workspace.Rel, false)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, time.Time{}, false, nil
		}
		return "", 0, time.Time{}, false, err
	}
	defer unix.Close(dirFD)

	fileFD, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if err == unix.ENOENT {
			return "", 0, time.Time{}, false, nil
		}
		if err == unix.ELOOP {
			return "", 0, time.Time{}, false, errAgentCoreFileSymlink
		}
		return "", 0, time.Time{}, false, err
	}
	file := os.NewFile(uintptr(fileFD), name)
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", 0, time.Time{}, false, err
	}
	if info.IsDir() {
		return "", 0, time.Time{}, false, fmt.Errorf("核心文件路径不能是目录")
	}

	var content []byte
	if info.Size() <= agentCoreFileMaxBytes {
		content, err = io.ReadAll(file)
	} else {
		content, err = io.ReadAll(io.LimitReader(file, agentCoreFileMaxBytes))
		if err == nil {
			content = append(content, []byte("\n\n... (文件过大，已截断)")...)
		}
	}
	if err != nil {
		return "", 0, time.Time{}, false, err
	}
	return string(content), info.Size(), info.ModTime(), true, nil
}

func saveAgentCoreFile(workspace agentCoreWorkspaceLocation, name string, content []byte) error {
	dirFD, err := openAgentCoreWorkspaceDir(workspace.Root, workspace.Rel, true)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)

	var stat unix.Stat_t
	switch err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); {
	case err == nil:
		if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return errAgentCoreFileSymlink
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			return fmt.Errorf("核心文件路径不能是目录")
		}
	case err == unix.ENOENT:
		// Missing file is fine.
	case err == unix.ELOOP:
		return errAgentCoreFileSymlink
	default:
		return err
	}

	var tmpName string
	var tmpFD int
	for attempt := 0; attempt < 8; attempt++ {
		tmpName = fmt.Sprintf(".%s.tmp-%d-%d", name, os.Getpid(), time.Now().UnixNano()+int64(attempt))
		tmpFD, err = unix.Openat(dirFD, tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, 0o644)
		if err == nil {
			break
		}
		if err != unix.EEXIST {
			if err == unix.ELOOP {
				return errAgentCoreFileSymlink
			}
			return err
		}
	}
	if err != nil {
		return err
	}
	tmpFile := os.NewFile(uintptr(tmpFD), tmpName)
	keepTemp := false
	defer func() {
		_ = tmpFile.Close()
		if !keepTemp {
			_ = unix.Unlinkat(dirFD, tmpName, 0)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := unix.Renameat(dirFD, tmpName, dirFD, name); err != nil {
		return err
	}
	keepTemp = true
	return nil
}
