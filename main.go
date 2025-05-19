package caddy_site_deployer

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var lock sync.Mutex = sync.Mutex{}

func init() {
	caddy.RegisterModule(SiteDeployer{})
}

type SiteDeployer struct {
	// The path to the root of the site. Default is `{http.vars.root}`
	Root string `json:"root,omitempty"`

	// Maximimum size of the uploaded (compressed) archive in MB
	MaxSizeMB int64 `json:"max_size_mb,omitempty"`

	// MaxSizeMB converted to byte
	maxSizeB int64

	// Caddy structured logger
	logger *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (SiteDeployer) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.site_deployer",
		New: func() caddy.Module { return new(SiteDeployer) },
	}
}

// Provision sets up the Static Site Deployer.
func (deployer *SiteDeployer) Provision(ctx caddy.Context) error {
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

func (deployer *SiteDeployer) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Request are processed sequencially to avoid conflict
	lock.Lock()
	defer lock.Unlock()

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
			http.StatusBadRequest,
			fmt.Errorf("failed to create gzip reader for artifact: %w", err),
		)

	}
	defer gzipReader.Close()

	// Extract tarball to temporary directory
	tarReader := tar.NewReader(gzipReader)
	tempDir, err := extractTarToTemp(deployer.logger, tarReader)
	if err != nil {
		return caddyhttp.Error(
			http.StatusBadRequest,
			fmt.Errorf("could not extract tarball to temps folder: %w", err),
		)
	}
	defer cleanupDirectory(deployer.logger, tempDir)

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
		backupDirectory := getBackupPath(targetDirectory)
		err := os.Rename(targetDirectory, backupDirectory)
		if err != nil {
			return caddyhttp.Error(
				http.StatusInternalServerError,
				fmt.Errorf("failed to backup target directory %s: %w", targetDirectory, err),
			)
		}
		defer cleanupDirectory(deployer.logger, backupDirectory)
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

func (deployer *SiteDeployer) rollback(targetDirectory string) error {
	backupDirectory := getBackupPath(targetDirectory)
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
