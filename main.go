package caddy_site_deployer

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const DIR_PERM = 0740
const FILE_PERM = 0640

var lock sync.Mutex = sync.Mutex{}

func init() {
	caddy.RegisterModule(SiteDeployer{})
	// TODO: unit tests
	// TODO: integration tests (add file, add tar, add tar.gz, delete file, delete directory)
	// TODO: only use ErrorDeployement on not 500 errors
	// TODO: add end of line on error message
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
		deployer.MaxSizeMB = 32
	}

	deployer.maxSizeB = deployer.MaxSizeMB * 1024 * 1024

	return nil
}

func (deployer *SiteDeployer) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Request are processed sequencially to avoid conflict
	lock.Lock()
	defer lock.Unlock()

	id := GetId()

	// The following checks are taken directly from the static file module and kept to
	// ensure we don't miss a dangerous edge-case:
	// https://github.com/caddyserver/caddy/blob/a76d005a94ff8ee19fc17f5409b4089c2bfd1a60/modules/caddyhttp/fileserver/staticfiles.go#L264
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
	// End of copied code

	target := caddyhttp.SanitizedPathJoin(root, r.URL.Path)
	if target == root {
		target += "/" // Side effect of SanitizedPathJoin
	}

	if c := deployer.logger.Check(zapcore.DebugLevel, "sanitized path join"); c != nil {
		c.Write(
			zap.String("site_root", root),
			zap.String("request_path", r.URL.Path),
			zap.String("result", target),
		)
	}

	// Root request to handler
	var err *ErrorDeployement
	switch r.Method {
	case http.MethodPut:
		err = deployer.HandlePut(id, target, r)
	case http.MethodDelete:
		err = deployer.HandleDelete(id, target, r)
	default:
		w.Write([]byte(fmt.Sprintf("Unauthorized method: %s\n", r.Method)))
		return caddyhttp.Error(http.StatusMethodNotAllowed, errors.New("unauthorized method"))
	}

	if err != nil {
		deployer.logger.Log(zapcore.DebugLevel, err.Error())
		level := zapcore.WarnLevel
		if err.StatusCode >= 500 {
			level = zapcore.ErrorLevel
			err.Public = ""
		}
		deployer.logger.Log(level, err.Private.Error(), zap.Int("statusCode", err.StatusCode))
		w.Write([]byte(err.Error()))
		return caddyhttp.Error(err.StatusCode, err.Private)
	}
	deployer.logger.Log(zapcore.DebugLevel, "err is fucking null")

	return nil
}

func (deployer *SiteDeployer) HandlePut(id string, target string, r *http.Request) *ErrorDeployement {
	// We make sure tu close the body if it is not empty
	if r.Body != nil {
		defer r.Body.Close()
	}

	isDirectory := strings.HasSuffix(target, "/")

	// We prepare all the data in a temporary location
	// If the target directory does not exist, we create it
	targetTemp := getTempPath(id, target)
	var targetTempDir string
	if isDirectory {
		targetTempDir = targetTemp
	} else {
		targetTempDir = filepath.Dir(target)
	}
	if err := os.MkdirAll(targetTempDir, DIR_PERM); err != nil {
		// TODO: return 400 on directory = existing file
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("failed to create target directory %s: %w", target, err),
			"",
		}
	}

	// We extract the body to a temporary location
	var errExtract *ErrorDeployement
	if isDirectory {
		errExtract = extractDirectory(targetTemp, r.Body, r.Header.Get("content-type"))
	} else {
		errExtract = extractFile(targetTemp, r.Body)
	}

	if errExtract != nil {
		deployer.logger.Log(zapcore.DebugLevel, errExtract.Error())
		return errExtract
	}

	deployer.logger.Log(zapcore.DebugLevel, " errExtract is nil")

	// Check the state of the target
	_, err := os.Stat(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("could not stat target: %w", err),
			"",
		}
	}

	// We backup target if it already exist
	if err == nil {
		targetBackup := getBackupPath(id, target)
		err = os.Rename(target, targetBackup)
		if err != nil {
			return &ErrorDeployement{
				http.StatusInternalServerError,
				fmt.Errorf("failed to backup target directory %s: %w", target, err),
				"",
			}
		}
		defer func() {
			// We only clear the backup if everything happened without issues
			// (rollback takes care of cleaning up the backup if successfull)
			if err == nil {

				err := os.RemoveAll(targetBackup)
				deployer.logger.Log(zapcore.DebugLevel, "removing", zap.String("targetBackup", targetBackup))
				deployer.logger.Log(zapcore.DebugLevel, "line 198", zap.Error(err))

			}
		}()
	}

	// Swap target directory with artifact using atomic `Rename`
	err = os.Rename(targetTemp, target)
	if err != nil {
		err := fmt.Errorf("failed to swap temporary directoy (%s) with target (%s): %w", targetTemp, target, err)
		errRollback := rollback(id, target)
		if errRollback != nil {
			err = fmt.Errorf("failed to swap temporary directoy (%s) with target (%s): %w AND failed to rollback: %w", targetTemp, target, err, err)
		}
		return &ErrorDeployement{http.StatusInternalServerError, err, ""}
	}

	return nil
}

func (deployer *SiteDeployer) HandleDelete(id string, target string, r *http.Request) *ErrorDeployement {
	// Check the state of the target
	_, err := os.Stat(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("could not stat target: %w", err),
			"",
		}
	}

	// If it does not exists return 404
	if errors.Is(err, os.ErrNotExist) {
		return &ErrorDeployement{
			http.StatusNotFound,
			fmt.Errorf("trying to delete a target that does not exist: %w", err),
			"Not Found.",
		}
	}

	// Otherwise we just delete the target
	err = os.RemoveAll(target)
	deployer.logger.Log(zapcore.DebugLevel, "removing", zap.String("target", target))

	deployer.logger.Log(zapcore.DebugLevel, "line 238", zap.Error(err))

	if err != nil {
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("failed to delete target: %w", err),
			"",
		}
	}
	return nil
}
