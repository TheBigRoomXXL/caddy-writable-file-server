package caddy_site_deployer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ╔══════════════════════════════════════════════════════════════════════════════╗
// ║                          Fixtures And Helpers                                ║
// ╚══════════════════════════════════════════════════════════════════════════════╝

func newTestSiteDeployer() *SiteDeployer {
	tmp, err := os.MkdirTemp("", "caddy-site-deployer-test-")
	if err != nil {
		log.Fatal(tmp)
	}
	return &SiteDeployer{
		Root:      tmp,
		MaxSizeMB: 1,
		maxSizeB:  1024 * 1024,
		logger:    zap.NewNop(),
	}
}

func newFile() io.ReadCloser {
	file, err := os.Open("tests/assets/test.txt")
	if err != nil {
		panic(err)
	}
	return file
}

func newTar() io.ReadCloser {
	file, err := os.Open("tests/assets/test.tar")
	if err != nil {
		panic(err)
	}
	return file
}

func newTarGz() io.ReadCloser {
	file, err := os.Open("tests/assets/test.tar.gz")
	if err != nil {
		panic(err)
	}
	return file
}

type MockHandler struct {
}

func (m *MockHandler) ServeHTTP(http.ResponseWriter, *http.Request) error { return nil }

func assertFileExist(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if assert.NoError(t, err, "Expected file %q to exist", path) {
		assert.False(t, info.IsDir(), "Expected %q to be a file, but it is a directory", path)
	}
}

func assertDirectoryExist(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if assert.NoError(t, err, "Expected directory %q to exist", path) {
		assert.True(t, info.IsDir(), "Expected %q to be a directory, but it is a file", path)
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	entries, err := os.ReadDir(path)
	if assert.NoError(t, err, "Error reading directory %q", path) {
		assert.Empty(t, entries, "Expected directory %q to be empty, but found %d entries", path, len(entries))
	}
}

// ╔══════════════════════════════════════════════════════════════════════════════╗
// ║                               Invalid Requests                               ║
// ╚══════════════════════════════════════════════════════════════════════════════╝

func TestOnlyPUTAndDeleteAllowed(t *testing.T) {
	var tests = []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPatch,
		http.MethodPost,
	}
	deployer := newTestSiteDeployer()

	for _, method := range tests {
		t.Run(method, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
			r, _ := http.NewRequestWithContext(ctx, method, "/", bytes.NewBuffer([]byte{}))

			w := httptest.NewRecorder()

			err := deployer.ServeHTTP(w, r, &MockHandler{})
			errHandler, ok := err.(caddyhttp.HandlerError)

			assert.NotNil(t, err)
			assert.True(t, ok)
			assert.Equal(t, http.StatusMethodNotAllowed, errHandler.StatusCode)
			assert.Equal(t, fmt.Sprintf("Unauthorized method: %s\n", method), w.Body.String())
		})
	}
}

func TestRejectWindowADSPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping windows specific tests")
	}

	pathWithADS := `\Path\To\Your\File.txt:hiddenstream.txt`

	deployer := newTestSiteDeployer()
	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", pathWithADS, bytes.NewBuffer([]byte{}))
	r.Header.Add("Content-Type", "application/octet-stream")

	w := httptest.NewRecorder()

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	errHandler, ok := err.(caddyhttp.HandlerError)

	assert.NotNil(t, err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, errHandler.StatusCode)
}

// TODO: TestRejectBodyTooLarge

// ╔══════════════════════════════════════════════════════════════════════════════╗
// ║                                 Upload File                                  ║
// ╚══════════════════════════════════════════════════════════════════════════════╝

func TestUploadFile(t *testing.T) {
	deployer := newTestSiteDeployer()

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/test.txt", newFile())
	r.Header.Add("Content-Type", "application/octet-stream")

	w := httptest.NewRecorder()

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	assert.Nil(t, err)

	data, err := os.ReadFile(deployer.Root + "/test.txt")
	assert.Nil(t, err)
	assert.Equal(t, "Hi. What are you doing here?\n", string(data))
}

func TestUploadFileWithEmptyBody(t *testing.T) {
	deployer := newTestSiteDeployer()

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/test.txt", bytes.NewBuffer([]byte{}))
	r.Header.Add("Content-Type", "application/octet-stream")

	w := httptest.NewRecorder()

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	assert.Nil(t, err)

	data, err := os.ReadFile(deployer.Root + "/test.txt")
	assert.Nil(t, err)
	assert.Equal(t, len(data), 0)
}

// ╔══════════════════════════════════════════════════════════════════════════════╗
// ║                               Upload Directory                               ║
// ╚══════════════════════════════════════════════════════════════════════════════╝

func TestUploadDirectoryTar(t *testing.T) {
	deployer := newTestSiteDeployer()

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/", newTar())
	r.Header.Add("Content-Type", "application/x-tar")

	w := httptest.NewRecorder()

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	assert.Nil(t, err)
}

func TestUploadDirectoryTarGz(t *testing.T) {
	deployer := newTestSiteDeployer()

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/", newTarGz())
	r.Header.Add("Content-Type", "application/x-tar+gzip")

	w := httptest.NewRecorder()

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	assert.Nil(t, err)

	// Check directory structure
	assertDirectoryExist(t, deployer.Root+"/tested/with-file/")
	assertDirectoryExist(t, deployer.Root+"/tested/empty-file/")
	assertDirectoryExist(t, deployer.Root+"/tested/no-file/")
	assertDirectoryEmpty(t, deployer.Root+"/tested/no-file/")
	assertFileExist(t, deployer.Root+"/tested/empty-file/empty.txt")
	assertFileExist(t, deployer.Root+"/tested/with-file/deep.txt")

	// Check files content
	data, err := os.ReadFile(deployer.Root + "/tested/empty-file/empty.txt")
	assert.Nil(t, err)
	assert.Equal(t, len(data), 0)

	data, err = os.ReadFile(deployer.Root + "/tested/with-file/deep.txt")
	assert.Nil(t, err)
	assert.Equal(t, "deeeep!\n", string(data))

}

func TestUploadDirectoryWithEmptyBody(t *testing.T) {
	deployer := newTestSiteDeployer()

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/tested/", bytes.NewBuffer([]byte{}))
	r.Header.Add("Content-Type", "application/x-tar")

	w := httptest.NewRecorder()

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	assert.Nil(t, err)

	assertDirectoryExist(t, deployer.Root+"/tested/")
	assertDirectoryEmpty(t, deployer.Root+"/tested/")
}

// ╔══════════════════════════════════════════════════════════════════════════════╗
// ║                                 Delete File                                  ║
// ╚══════════════════════════════════════════════════════════════════════════════╝

// TODO: DeleteFile

// ╔══════════════════════════════════════════════════════════════════════════════╗
// ║                               Delete Directory                               ║
// ╚══════════════════════════════════════════════════════════════════════════════╝

// TODO: DeleteDirectory
