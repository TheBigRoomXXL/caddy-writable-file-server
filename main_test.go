package caddy_site_deployer

import (
	"bytes"
	"context"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

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

func newMultipartFormFromFile(filePath string) io.Reader {
	bodyWriter := new(bytes.Buffer)
	formWriter := multipart.NewWriter(bodyWriter)
	formWriter.SetBoundary("DIVNEKSNXXMD")
	defer formWriter.Close()

	file, err := os.Open(filePath)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	part, err := formWriter.CreateFormFile("artifact", filepath.Base(filePath))
	if err != nil {
		panic(err)
	}

	_, err = io.Copy(part, file)
	if err != nil {
		panic(err)
	}

	return bodyWriter
}

type MockHandler struct {
}

func (m *MockHandler) ServeHTTP(http.ResponseWriter, *http.Request) error { return nil }

func TestOnlyPUTAllowed(t *testing.T) {
	var tests = []string{
		http.MethodGet,
		http.MethodDelete,
		http.MethodHead,
		http.MethodPatch,
		http.MethodPost,
	}
	deployer := newTestSiteDeployer()

	for _, method := range tests {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(method, "/", nil)

			err := deployer.ServeHTTP(w, r, &MockHandler{})
			errHandler, ok := err.(caddyhttp.HandlerError)

			assert.NotNil(t, err)
			assert.True(t, ok)
			assert.Equal(t, http.StatusMethodNotAllowed, errHandler.StatusCode)
			assert.Equal(t, "Only PUT method is allowed", w.Body.String())
		})
	}
}

func TestOnlyMultipartFormaDataIsAllowed(t *testing.T) {
	var tests = []string{
		"text/plain",
		"text/html",
		"text/xml",
		"application/json",
		"application/octet-stream",
	}
	deployer := newTestSiteDeployer()

	for _, contentType := range tests {
		t.Run(contentType, func(t *testing.T) {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest("PUT", "/", nil)
			r.Header.Add("Content-Type", contentType)

			err := deployer.ServeHTTP(w, r, &MockHandler{})
			errHandler, ok := err.(caddyhttp.HandlerError)

			assert.NotNil(t, err)
			assert.True(t, ok)
			assert.Equal(t, http.StatusUnprocessableEntity, errHandler.StatusCode)
			assert.Equal(t, "Only 'multipart/form-data' content-type is allowed", w.Body.String())
		})
	}
}

func TestRejectWindowADSPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping windows specific tests")
	}

	pathWithADS := `\Path\To\Your\File.txt:hiddenstream.txt`

	deployer := newTestSiteDeployer()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest("PUT", pathWithADS, nil)
	r.Header.Add("Content-Type", "multipart/form-data")

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	errHandler, ok := err.(caddyhttp.HandlerError)

	assert.NotNil(t, err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, errHandler.StatusCode)
}

func TestRejectEmptyBody(t *testing.T) {
	deployer := newTestSiteDeployer()
	w := httptest.NewRecorder()
	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/", nil)
	r.Header.Add("Content-Type", "multipart/form-data")

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	errHandler, ok := err.(caddyhttp.HandlerError)

	assert.NotNil(t, err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, errHandler.StatusCode)
}

func TestRejectMissingArtifact(t *testing.T) {
	deployer := newTestSiteDeployer()
	w := httptest.NewRecorder()

	bodyWriter := new(bytes.Buffer)
	formWriter := multipart.NewWriter(bodyWriter)
	formWriter.WriteField("not-artifact", "some-date")
	formWriter.Close()

	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})

	r, _ := http.NewRequestWithContext(ctx, "PUT", "/", bodyWriter)
	r.Header.Add("Content-Type", "multipart/form-data; boundary="+formWriter.Boundary())

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	errHandler, ok := err.(caddyhttp.HandlerError)

	assert.NotNil(t, err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusUnprocessableEntity, errHandler.StatusCode)
	assert.Equal(t, "Could not retrieve artifact from body", w.Body.String())
}

func TestUploadFileInsteadOfDirectory(t *testing.T) {
	deployer := newTestSiteDeployer()
	ctx := context.WithValue(context.Background(), caddy.ReplacerCtxKey, &caddy.Replacer{})
	w := httptest.NewRecorder()
	body := newMultipartFormFromFile("test_assets/index.txt")
	r, _ := http.NewRequestWithContext(ctx, "PUT", "/", body)
	r.Header.Add("Content-Type", "multipart/form-data; boundary=DIVNEKSNXXMD")

	err := deployer.ServeHTTP(w, r, &MockHandler{})
	errHandler, ok := err.(caddyhttp.HandlerError)

	assert.NotNil(t, err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, errHandler.StatusCode)
}
