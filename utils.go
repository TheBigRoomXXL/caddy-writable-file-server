package caddy_site_deployer

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func extractTarToTemp(logger *zap.Logger, tarReader *tar.Reader) (string, error) {
	tempDir, err := os.MkdirTemp("", "artifact-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tempDir)
		}
	}()

	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break // End of archive
		}

		if err != nil {
			return "", fmt.Errorf("error reading tar entry: %w", err)
		}

		targetPath := filepath.Join(tempDir, header.Name)

		// Security: Avoid traversal attack, clean the target path
		cleanedTargetPath := filepath.Clean(targetPath)

		absTargetPath, err := filepath.Abs(cleanedTargetPath)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path for target %s: %w", cleanedTargetPath, err)
		}

		// Security: Ensure the absolute target path starts with the absolute temporary
		// directory path. This confirms the target is *inside* the temporary directory.
		if !strings.HasPrefix(absTargetPath, tempDir) {
			return "", fmt.Errorf("tar entry path is outside the temporary directory: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return "", fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}

		case tar.TypeReg:
			parentDir := filepath.Dir(targetPath)
			if err := os.MkdirAll(parentDir, 0755); err != nil { // Using a default permission for parent dirs
				return "", fmt.Errorf("failed to create parent directory for file %s: %w", targetPath, err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return "", fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}

			defer outFile.Close()

			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close() // Ensure file is closed before cleanup on error
				return "", fmt.Errorf("failed to copy file content to %s: %w", targetPath, err)
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

	return tempDir, nil
}

func cleanupDirectory(logger *zap.Logger, directory string) {
	err := os.RemoveAll(directory)
	if err != nil {
		if c := logger.Check(zapcore.WarnLevel, "failed to clean up directory"); c != nil {
			c.Write(
				zap.String("directory", directory),
				zap.String("error", err.Error()),
			)
		}
	}
}

func getBackupPath(directory string) string {
	return strings.TrimSuffix(directory, "/") + ".backup/"
}
