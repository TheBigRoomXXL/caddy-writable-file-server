package caddy_site_deployer

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const ID_LENGTH = 8

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

// Delete any file or directory that was deployed and try to restore backup
func rollback(id string, target string) error {
	// Check backup exist
	targetbackup := getBackupPath(id, target)
	_, err := os.Stat(targetbackup)
	if errors.Is(err, os.ErrNotExist) {
		return nil // No backup to rollback
	}
	if err != nil {
		return fmt.Errorf("failed to stat backup during rollback: %w", err)
	}

	// First we cleanup the targetDirectory if it still exist
	err = os.RemoveAll(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not cleanup target ddirectory during rollback: %w", err)
	}

	// Then we restore the original directory
	err = os.Rename(targetbackup, target)
	if err != nil {
		return fmt.Errorf("could not restore backup during rollback: %w", err)
	}

	// Finally we remove the backup
	os.RemoveAll(targetbackup)
	return nil
}

// Return a backup path next to the target.
//
// Using a path next to the target ensure it is on the same file system, allowing us
// to use os.Rename for atomic update. (/tmp is often on a RAM file system)
//
// This function does not check if the temporary path is already used.
func getBackupPath(id string, target string) string {
	if strings.HasSuffix(target, "/") {
		return strings.TrimSuffix(target, "/") + "." + id + "-backup/"
	}
	return target + "." + id + "-backup"
}

// Return a temporary path next to the target.
//
// Using a path next to the target ensure it is on the same file system, allowing us
// to use os.Rename for atomic update. (/tmp is often on a RAM file system)
//
// This function does not check if the temporary path is already used.
func getTempPath(id string, target string) string {
	if strings.HasSuffix(target, "/") {
		return strings.TrimSuffix(target, "/") + "." + id + "-tmp/"
	}
	return target + "." + id + "-tmp"
}

func GetId() string {
	b := make([]byte, ID_LENGTH)
	_, err := rand.Read(b)
	if err != nil { // Very unlikely
		log.Fatal(fmt.Errorf("failed to generate nonce: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
