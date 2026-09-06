package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// CheckWritable reports whether the database can be created beside dbPath.
//
// SQLite's own answers are "unable to open database file (14)" for a directory
// it cannot write and "attempt to write a readonly database (8)" for a file it
// cannot, and neither names the directory, the user the process runs as, or
// anything to do about it. A bind-mounted data directory whose ownership does
// not match the container's uid is the first thing a deployment can get wrong,
// so it is worth one probe to say so in words.
func CheckWritable(dbPath string) error {
	dir := filepath.Dir(dbPath)
	f, err := os.CreateTemp(dir, ".darkrouter-writable-*")
	if err != nil {
		return fmt.Errorf(
			"%s is not writable by uid %d, which is the user this process runs as: %w",
			dir, os.Getuid(), err)
	}
	name := f.Name()
	f.Close()
	// Removed rather than reused: the probe's only job is to have succeeded.
	return os.Remove(name)
}
