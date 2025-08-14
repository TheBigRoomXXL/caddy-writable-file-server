package caddy_site_deployer

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// create target and copy the content of reader into it.
func extractFile(target string, reader io.Reader) *ErrorDeployement {

	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, FILE_PERM)
	if err != nil {
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("failed to open target file '%s' for extraction: %w", target, err),
			"",
		}
	}
	defer file.Close()

	// Stream from reader to file in chunks
	if _, err := io.Copy(file, reader); err != nil {
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("failed to copy data to file '%s' for extraction: %w", target, err),
			"",
		}
	}

	return nil
}

// TODO: implementation extractDirectory
func extractDirectory(target string, reader io.Reader, contentType string) *ErrorDeployement {
	switch contentType {
	case "application/x-tar":
		return extractTar(target, reader)
	case "application/tar":
		return extractTar(target, reader)
	case "application/x-tar+gzip":
		return extractTarGz(target, reader)
	case "application/tar+gzip":
		return extractTarGz(target, reader)
	case "application/x-gzip":
		return extractTarGz(target, reader)
	case "application/gzip":
		return extractTarGz(target, reader)
	default:
		return &ErrorDeployement{
			http.StatusBadRequest,
			errors.New("bad content-type: only 'application/x-tar' and 'application/x-tar+gzip' are allowed for directories"),
			"bad content-type: only 'application/x-tar' and 'application/x-tar+gzip' are allowed",
		}
	}
}

func extractTarGz(target string, reader io.Reader) *ErrorDeployement {
	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return &ErrorDeployement{
			http.StatusInternalServerError,
			fmt.Errorf("failed to wrap body in gzip reader: %w", err),
			"",
		}
	}
	defer gzr.Close()

	return extractTar(target, gzr)
}

func extractTar(target string, reader io.Reader) *ErrorDeployement {
	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return &ErrorDeployement{
				http.StatusInternalServerError,
				fmt.Errorf("failed to extract tar: %w", err),
				"",
			}
		}

		targetPath := filepath.Join(target, hdr.Name)

		// Prevent path traversal attacks
		if !strings.HasPrefix(targetPath, filepath.Clean(target)+string(os.PathSeparator)) {
			return &ErrorDeployement{
				http.StatusInternalServerError,
				fmt.Errorf("security error: path traversal: %w", err),
				"",
			}
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return &ErrorDeployement{
					http.StatusInternalServerError,
					fmt.Errorf("failed to extract tar: %w", err),
					"",
				}
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return &ErrorDeployement{
					http.StatusInternalServerError,
					fmt.Errorf("failed to extract tar: %w", err),
					"",
				}
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return &ErrorDeployement{
					http.StatusInternalServerError,
					fmt.Errorf("failed to extract tar: %w", err),
					"",
				}
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return &ErrorDeployement{
					http.StatusInternalServerError,
					fmt.Errorf("failed to extract tar: %w", err),
					"",
				}
			}
			outFile.Close()
		default:
			// We ignore other types
		}
	}
	return nil
}
