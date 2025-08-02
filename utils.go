package caddy_site_deployer

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func extractTar(logger *zap.Logger, tarReader *tar.Reader, path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break // End of archive
		}

		if err != nil {
			return fmt.Errorf("error reading tar entry: %w", err)
		}

		targetPath := filepath.Join(path, header.Name)

		// Security: Avoid traversal attack, clean the target path
		cleanedTargetPath := filepath.Clean(targetPath)

		absTargetPath, err := filepath.Abs(cleanedTargetPath)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for target %s: %w", cleanedTargetPath, err)
		}

		// Security: Ensure the absolute target path starts with the absolute temporary
		// directory path. This confirms the target is *inside* the temporary directory.
		if !strings.HasPrefix(absTargetPath, path) {
			return fmt.Errorf("tar entry path is outside the temporary directory: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}

		case tar.TypeReg:
			parentDir := filepath.Dir(targetPath)
			if err := os.MkdirAll(parentDir, 0755); err != nil { // Using a default permission for parent dirs
				return fmt.Errorf("failed to create parent directory for file %s: %w", targetPath, err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}

			defer outFile.Close()

			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close() // Ensure file is closed before cleanup on error
				return fmt.Errorf("failed to copy file content to %s: %w", targetPath, err)
			}

		// Handle other types (e.g., Symlink, Link...)
		default:
			if c := logger.Check(zapcore.DebugLevel, "skipping unwanted tar entry type"); c != nil {
				c.Write(
					zap.String("entry name", header.Name),
					zap.String("entry type", string(header.Typeflag)),
				)
			}
		}
	}

	return nil
}

// TODO: implementation
func extractFile(target string, reader io.ReadCloser) error {
	return nil
}

// TODO: implementation
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
