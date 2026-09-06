package web

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

// multipartFile wraps one uploaded part.
type multipartFile struct {
	fh *multipart.FileHeader
}

// allowedImageTypes is what a photograph may be.
//
// It is a closed set rather than "anything image/*", because the export writes
// the file extension into a public URL and a marketplace fetcher decides how to
// handle the image from it. A format nothing can display is worse than a
// rejected upload: the listing goes live with a broken picture and no error.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// read returns the file's bytes, its cleaned name, and its DETECTED content type.
//
// ⚠ The content type is sniffed from the bytes, NOT taken from the part's own
// Content-Type header. The header is supplied by whoever made the request and is
// therefore a claim rather than a fact; believing it would let a caller store
// arbitrary bytes under an image extension on a URL this application publishes
// to the public internet. http.DetectContentType reads the magic prefix, which
// is what a marketplace fetcher and a browser will do too.
func (m *multipartFile) read() (data []byte, name string, contentType string, err error) {
	name = filepath.Base(strings.TrimSpace(m.fh.Filename))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "upload"
	}

	if m.fh.Size > maxPhotoBytes {
		return nil, name, "", fmt.Errorf("larger than %d MB", maxPhotoBytes>>20)
	}

	f, err := m.fh.Open()
	if err != nil {
		return nil, name, "", fmt.Errorf("could not be read")
	}
	defer f.Close()

	// LimitReader as well as the size check above: Size is what the client
	// declared in the part header, and a mismatched declaration should truncate
	// rather than let the read run away.
	data, err = io.ReadAll(io.LimitReader(f, maxPhotoBytes+1))
	if err != nil {
		return nil, name, "", fmt.Errorf("could not be read")
	}
	if len(data) == 0 {
		return nil, name, "", fmt.Errorf("was empty")
	}
	if int64(len(data)) > maxPhotoBytes {
		return nil, name, "", fmt.Errorf("larger than %d MB", maxPhotoBytes>>20)
	}

	contentType = http.DetectContentType(data)
	// DetectContentType may return a type with parameters; the store compares
	// bare types.
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	if !allowedImageTypes[contentType] {
		return nil, name, contentType,
			fmt.Errorf("is a %s, and only JPEG, PNG, WebP and GIF can be published", contentType)
	}
	return data, name, contentType, nil
}
