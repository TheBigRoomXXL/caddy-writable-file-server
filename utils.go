package caddy_site_deployer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// create target and copy the content of reader into it.
func extractFile(target string, reader io.ReadCloser) error {
	defer reader.Close()
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, FILE_PERM)
	if err != nil {
		return fmt.Errorf("failed to open target file '%s' for extraction: %w", target, err)
	}
	defer file.Close()

	// Stream from reader to file in chunks
	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("failed to copy data to file '%s' for extraction: %w", target, err)
	}

	return nil
}

// TODO: implementation extractDirectory
func extractDirectory(target string, reader io.ReadCloser, contentType string) error {
	return nil
}

// Delete ay file or directory that was deployed and try to restore backup
func rollback(target string) error {
	// Check backup exist
	targetbackup := getBackupPath(target)
	if _, err := os.Stat(targetbackup); err != nil {
		return fmt.Errorf("backup does not exist during rollback: %w", err)
	}

	// First we cleanup the targetDirectory if it still exist
	err := os.RemoveAll(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not cleanup target ddirectory during rollback: %w", err)
	}

	// Then w restore the original directory
	err = os.Rename(targetbackup, target)
	if err != nil {
		return fmt.Errorf("could not restore backup during rollback: %w", err)
	}

	return err
}

// Return a backup path next to the target.
//
// Using a path next to the target ensure it is on the same file system, allowing us
// to use os.Rename for atomic update. (/tmp is often on a RAM file system)
//
// This function does not check if the temporary path is already used.
func getBackupPath(target string) string {
	if strings.HasSuffix(target, "/") {
		return strings.TrimSuffix(target, "/") + ".backup/"
	}
	return target + ".backup"
}

// Return a temporary path next to the target.
//
// Using a path next to the target ensure it is on the same file system, allowing us
// to use os.Rename for atomic update. (/tmp is often on a RAM file system)
//
// This function does not check if the temporary path is already used.
func getTempPath(target string) string {
	if strings.HasSuffix(target, "/") {
		return strings.TrimSuffix(target, "/") + ".tmp/"
	}
	return target + ".tmp"
}
