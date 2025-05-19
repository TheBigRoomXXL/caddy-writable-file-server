package caddy_site_deployer

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func newLogsCapture() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return zap.New(core), logs
}
func TestGetBackupPath(t *testing.T) {
	path := "/path/to/dir/"
	pathBackup := getBackupPath(path)
	assert.Equal(t, "/path/to/dir.backup/", pathBackup)
}

func TestCleanupDirectory(t *testing.T) {
	logger, log := newLogsCapture()
	tempDir, err := os.MkdirTemp("", "test-cleanup-")
	if err != nil {
		panic(err)
	}
	cleanupDirectory(logger, tempDir)
	assert.NoDirExists(t, tempDir)
	assert.Equal(t, 0, log.Len())
}

func TestCleanupEmtpyDirectory(t *testing.T) {
	logger, log := newLogsCapture()
	cleanupDirectory(logger, "/tmp/does-not-exist")
	assert.Equal(t, 0, log.Len())
}
