package config

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A watcher may still be decoding the previous config when another FileStore
// replaces it. Windows readers must permit delete/rename sharing so that this
// read handle does not block Save's replacement of the directory entry.
func openConfigFile(path string) (*os.File, error) {
	name, err := configWindowsPath(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	encoded, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(encoded, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// MoveFileEx, used by os.Rename, refuses to replace an open destination even
// when its readers permit delete sharing. POSIX rename semantics keep those
// readers on the old file while new opens see the complete replacement.
func replaceConfigFile(oldPath, newPath string) error {
	fail := func(err error) error {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	source, err := configWindowsPath(oldPath)
	if err != nil {
		return fail(err)
	}
	sourceUTF16, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return fail(err)
	}
	target, err := configWindowsPath(newPath)
	if err != nil {
		return fail(err)
	}
	targetUTF16, err := windows.UTF16FromString(target)
	if err != nil {
		return fail(err)
	}
	handle, err := windows.CreateFile(sourceUTF16, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fail(err)
	}
	defer windows.CloseHandle(handle)

	// FILE_RENAME_INFO has pointer-sized padding before RootDirectory. Use the
	// native layout rather than hard-coding an amd64/arm64 byte offset.
	type renameInfo struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	var header renameInfo
	buffer := make([]byte, int(unsafe.Offsetof(header.FileName))+len(targetUTF16)*2)
	info := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.FileNameLength = uint32((len(targetUTF16) - 1) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(targetUTF16)), targetUTF16)
	if err := windows.SetFileInformationByHandle(handle, windows.FileRenameInfoEx,
		&buffer[0], uint32(len(buffer))); err != nil {
		return fail(err)
	}
	return nil
}

func configWindowsPath(path string) (string, error) {
	name, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Direct Windows API calls need extended paths for long filenames.
	if !strings.HasPrefix(name, `\\?\`) && !strings.HasPrefix(name, `\\.\`) {
		if strings.HasPrefix(name, `\\`) {
			name = `\\?\UNC\` + name[2:]
		} else {
			name = `\\?\` + name
		}
	}
	return name, nil
}
