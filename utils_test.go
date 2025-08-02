package caddy_site_deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBackupPathDirectory(t *testing.T) {
	path := "/path/to/dir/"
	pathBackup := getBackupPath(path)
	assert.Equal(t, "/path/to/dir.backup/", pathBackup)
}

// TEST: TestGetBackupPathFile

// TEST: TestGetTempsPathDirectory

// TEST: TestGetTempsPathFile

// TEST: ExtractFile

// TEST: ExtractDirectory

// TEST: roolback file

// TEST: rollback directory
