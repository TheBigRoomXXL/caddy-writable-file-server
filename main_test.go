package caddy_site_deployer

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

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
