package caddy_writable_file_server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBackupPathDirectory(t *testing.T) {
	path := "/path/to/dir/"
	pathBackup := getBackupPath("tested", path)
	assert.Equal(t, "/path/to/dir.tested-backup/", pathBackup)
}

func TestGetBackupPathFile(t *testing.T) {
	path := "/path/to/file"
	pathBackup := getBackupPath("tested", path)
	assert.Equal(t, "/path/to/file.tested-backup", pathBackup)
}

func TestGetTempPathDirectory(t *testing.T) {
	path := "/path/to/dir/"
	pathTmp := getTempPath("tested", path)
	assert.Equal(t, "/path/to/dir-tested-tmp/", pathTmp)
}

func TestGetTempPathFile(t *testing.T) {
	path := "/path/to/file"
	pathTmp := getTempPath("tested", path)
	assert.Equal(t, "/path/to/file-tested-tmp", pathTmp)
}

// TEST: ExtractFile

// TEST: ExtractDirectory

// TEST: roolback file

// TEST: rollback directory
