package caddy_static_deployer

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	caddy.RegisterModule(StaticSiteDeployer{})
}

type StaticSiteDeployer struct {
	// The path to the root of the site. Default is `{http.vars.root}` if set,
	// or current working directory otherwise. This should be a trusted value.
	Root string `json:"root,omitempty"`

	// Maximimum size of the uploaded (compressed) archive in MB
	MaxSizeMB int64 `json:"max_size_mb,omitempty"`

	// MaxSizeMB converted to byte
	maxSizeB int64

	// Caddy structured logger
	logger *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (StaticSiteDeployer) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.static_site_deployer",
		New: func() caddy.Module { return new(StaticSiteDeployer) },
	}
}

// Provision sets up the Static Site Deployer.
func (deployer *StaticSiteDeployer) Provision(ctx caddy.Context) error {
	deployer.logger = ctx.Logger()

	if deployer.Root == "" {
		deployer.Root = "{http.vars.root}"
	}

	if deployer.MaxSizeMB == 0 {
		deployer.MaxSizeMB = 2
	}

	deployer.maxSizeB = deployer.MaxSizeMB * 1024 * 1024

	return nil
}

func (deployer *StaticSiteDeployer) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Validate HTTP Method
	if r.Method != "PUT" {
		w.Write([]byte("Only PUT method is allowed"))
		return caddyhttp.Error(
			http.StatusMethodNotAllowed, fmt.Errorf("unauthorized method: %s", r.Method),
		)
	}

	// Validate Content-Type
	if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		w.Write([]byte("Only 'multipart/form-data' content-type is allowed"))
		return caddyhttp.Error(
			http.StatusUnprocessableEntity, fmt.Errorf("unauthorized method: %s", r.Method),
		)
	}

	// The following checks are taken directly from the static file module and kept to
	// ensure we don't miss a dangerous edge-case:
	// https: //github.com/caddyserver/caddy/blob/a76d005a94ff8ee19fc17f5409b4089c2bfd1a60/modules/caddyhttp/fileserver/staticfiles.go#L264
	if runtime.GOOS == "windows" {
		// reject paths with Alternate Data Streams (ADS)
		if strings.Contains(r.URL.Path, ":") {
			return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("illegal ADS path"))
		}
		// reject paths with "8.3" short names
		trimmedPath := strings.TrimRight(r.URL.Path, ". ") // Windows ignores trailing dots and spaces, sigh
		if len(path.Base(trimmedPath)) <= 12 && strings.Contains(trimmedPath, "~") {
			return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("illegal short name"))
		}
		// both of those could bypass file hiding or possibly leak information even if the file is not hidden
	}

	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	root := repl.ReplaceAll(deployer.Root, ".")

	if !strings.HasSuffix(r.URL.Path, "/") {
		w.Write([]byte("Path must point to a directory."))
		return caddyhttp.Error(
			http.StatusUnprocessableEntity, errors.New("path  must point to a directory"),
		)
	}

	targetDirectory := caddyhttp.SanitizedPathJoin(root, r.URL.Path)
	if targetDirectory == root {
		targetDirectory += "/" // Side effect of SanitizedPathJoin
	}

	if c := deployer.logger.Check(zapcore.DebugLevel, "sanitized path join"); c != nil {
		c.Write(
			zap.String("site_root", root),
			zap.String("request_path", r.URL.Path),
			zap.String("result", targetDirectory),
		)
	}

	// Parse the form data
	if err := r.ParseMultipartForm(deployer.maxSizeB); err != nil {
		return caddyhttp.Error(
			http.StatusBadRequest, fmt.Errorf("failed to parse multipart body: %w", err),
		)
	}

	artifact, _, err := r.FormFile("artifact")
	if err != nil {
		w.Write([]byte("Could not retrieve artifact from body"))
		return caddyhttp.Error(
			http.StatusUnprocessableEntity,
			fmt.Errorf("could not retrieve artifact from body: %w", err),
		)
	}
	defer artifact.Close()

	// Assume a gzipped tar archive
	gzipReader, err := gzip.NewReader(artifact)
	if err != nil {
		return caddyhttp.Error(
			http.StatusInternalServerError,
			fmt.Errorf("failed to create gzip reader for artifact: %w", err),
		)

	}
	defer gzipReader.Close()

	// Extract tarball to temporary directory
	tarReader := tar.NewReader(gzipReader)
	tempDir, err := deployer.ExtractTarToTemp(tarReader)
	if err != nil {
		return caddyhttp.Error(
			http.StatusInternalServerError,
			fmt.Errorf("could not extract tarball to temps folder: %w", err),
		)
	}
	defer deployer.removeDirectory(tempDir)

	// Backup target directory or create it and its parents if new
	_, err = os.Stat(targetDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return caddyhttp.Error(
			http.StatusInternalServerError,
			fmt.Errorf("could not stat targetDirectory: %w", err),
		)
	}

	if err != nil && errors.Is(err, os.ErrNotExist) {
		// Target directory does not exist, we make it with its parent (using default permission).
		if err := os.MkdirAll(targetDirectory, 0755); err != nil {
			return caddyhttp.Error(
				http.StatusInternalServerError,
				fmt.Errorf("failed to create target directory %s: %w", targetDirectory, err),
			)
		}
	} else {
		// Target directory exist, we create a backup from it.
		backupDirectory := deployer.getBackupPath(targetDirectory)
		err := os.Rename(targetDirectory, backupDirectory)
		if err != nil {
			return caddyhttp.Error(
				http.StatusInternalServerError,
				fmt.Errorf("failed to backup target directory %s: %w", targetDirectory, err),
			)
		}
		defer deployer.removeDirectory(backupDirectory)
	}

	// Swap target directory with artifact using atomic `Rename`
	err = os.Rename(tempDir, targetDirectory)
	if err != nil && strings.Contains(err.Error(), "invalid cross-device link") {
		// This is an edge case where the temporary directory is on another partition
		// Because of that we cannot use `Rename` and we must use copy instead.
		err = os.CopyFS(targetDirectory, os.DirFS(tempDir))
		if err != nil {
			_ = deployer.rollback(targetDirectory)
			return caddyhttp.Error(
				http.StatusInternalServerError,
				fmt.Errorf("failed to copy temporary directoy (%s) to target (%s): %w", tempDir, targetDirectory, err),
			)
		}
	} else if err != nil {
		_ = deployer.rollback(targetDirectory)
		return caddyhttp.Error(
			http.StatusInternalServerError,
			fmt.Errorf("failed to swap temporary directoy (%s) with target (%s): %w", tempDir, targetDirectory, err),
		)
	}

	return nil
}

// ExtractTarToTemp extracts a tarball to a new temporary directory.
// It includes security checks to prevent path traversal attacks.
// This function wraps deployer.extractTarToTemp to ensure all error path lead to a cleanup.
// Do not call deployer.extractTarToTemp directly!
func (deployer *StaticSiteDeployer) ExtractTarToTemp(tarReader *tar.Reader) (string, error) {
	tempDir, err := deployer.extractTarToTemp(tarReader)
	if err != nil {
		os.RemoveAll(tempDir)
		tempDir = ""
	}
	return tempDir, err
}

func (deployer *StaticSiteDeployer) extractTarToTemp(tarReader *tar.Reader) (string, error) {
	tempDir, err := os.MkdirTemp("", "artifact-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}

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
			if c := deployer.logger.Check(zapcore.DebugLevel, "skipping unwanted tar entry type"); c != nil {
				c.Write(
					zap.String("entry name", header.Name),
					zap.String("entry type", string(header.Typeflag)),
				)
			}
		}
	}

	return tempDir, nil
}

func (deployer *StaticSiteDeployer) rollback(targetDirectory string) error {
	backupDirectory := deployer.getBackupPath(targetDirectory)
	if _, err := os.Stat(backupDirectory); err != nil {
		if c := deployer.logger.Check(zapcore.ErrorLevel, "failure during rollback"); c != nil {
			c.Write(
				zap.String("targetDirectory", targetDirectory),
				zap.String("backupDirectory", backupDirectory),
				zap.String("error", fmt.Sprintf("could not stat backup directory: %s", err.Error())),
			)
		}
		return err
	}
	// First we cleaup the targetDirectory if it still exist
	err := os.RemoveAll(targetDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if c := deployer.logger.Check(zapcore.ErrorLevel, "failure during rollback"); c != nil {
			c.Write(
				zap.String("targetDirectory", targetDirectory),
				zap.String("backupDirectory", backupDirectory),
				zap.String("error", fmt.Sprintf("could not clean target directory: %s", err.Error())),
			)
		}
		return err
	}
	err = os.Rename(backupDirectory, targetDirectory)
	if err != nil {
		if c := deployer.logger.Check(zapcore.ErrorLevel, "failure during rollback"); c != nil {
			c.Write(
				zap.String("targetDirectory", targetDirectory),
				zap.String("backupDirectory", backupDirectory),
				zap.String("error", fmt.Sprintf("could not rename backup directory: %s", err.Error())),
			)
		}
	}

	return err
}

func (deployer *StaticSiteDeployer) removeDirectory(directory string) {
	if _, err := os.Stat(directory); err == nil {
		if err := os.RemoveAll(directory); err != nil {
			if c := deployer.logger.Check(zapcore.ErrorLevel, "failed to clean up existing directory"); c != nil {
				c.Write(
					zap.String("directory", directory),
					zap.String("error", err.Error()),
				)
			}
		}
	} else if !os.IsNotExist(err) {
		if c := deployer.logger.Check(zapcore.ErrorLevel, "failed to stat directory during cleanup"); c != nil {
			c.Write(
				zap.String("directory", directory),
				zap.String("error", err.Error()),
			)
		}
	}
}

func (deployer *StaticSiteDeployer) getBackupPath(directory string) string {
	return strings.TrimSuffix(directory, "/") + ".backup/"
}
