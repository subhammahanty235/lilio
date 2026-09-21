// Package fsatomic writes files so that readers and crashes never observe a
// partially written one.
package fsatomic

import (
	"fmt"
	"os"
	"path/filepath"
)

// TempPrefix marks the temporary files this package creates. Directory
// listings that enumerate real files must skip anything with this prefix, or a
// write interrupted before its rename will show up as a file in its own right.
const TempPrefix = ".tmp-"

// WriteFile writes data to path atomically and durably.
//
// os.WriteFile truncates the destination and then writes into it, so anything
// that interrupts it - a crash, a kill, a full disk - leaves a short file where
// a complete one used to be. That file still exists, still has a name, and
// still answers "yes" to anything that merely checks for its presence, which
// makes the damage easy to miss.
//
// Writing to a temporary file and renaming avoids it: rename(2) is atomic
// within a filesystem, so the destination is either the old contents or the
// complete new contents, never a mixture.
//
// The syncs do a different job from the rename. Rename provides atomicity;
// fsync provides durability - first that the bytes reached the disk, then that
// the rename itself did. They are also what makes this roughly an order of
// magnitude slower than a plain write, so use Replace instead wherever
// something other than this file already provides durability.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	tmpName, err := writeTemp(path, data, perm, true)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to commit file: %w", err)
	}
	return syncDir(filepath.Dir(path))
}

// Replace writes data to path atomically, without waiting for it to reach the
// disk.
//
// The destination is never observed torn - a reader or a crashed process sees
// either the previous contents or the complete new contents - but after a power
// loss or a kernel panic the write may not be there at all.
//
// That is the right trade for replicated data. A chunk's durability comes from
// there being copies of it on other nodes, and a scrub restores any copy a node
// loses; paying for an fsync per chunk buys a guarantee that replication
// already provides, at more than ten times the cost of the write itself.
// Unreplicated data - metadata, notably - should use WriteFile.
func Replace(path string, data []byte, perm os.FileMode) error {
	tmpName, err := writeTemp(path, data, perm, false)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to commit file: %w", err)
	}
	return nil
}

// writeTemp writes data to a temporary file beside path and returns its name.
func writeTemp(path string, data []byte, perm os.FileMode, sync bool) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), TempPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}

	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("failed to write temp file: %w", err))
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(fmt.Errorf("failed to set permissions: %w", err))
	}
	if sync {
		if err := tmp.Sync(); err != nil {
			return fail(fmt.Errorf("failed to sync temp file: %w", err))
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	return tmpName, nil
}

// syncDir persists a directory entry change - here, the rename itself.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open directory for sync: %w", err)
	}
	defer d.Close()
	return d.Sync()
}

// IsTemp reports whether name belongs to an in-progress or abandoned write.
func IsTemp(name string) bool {
	return len(name) >= len(TempPrefix) && name[:len(TempPrefix)] == TempPrefix
}
