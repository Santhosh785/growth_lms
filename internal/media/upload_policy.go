package media

import (
	"errors"
	"path"
	"strings"
)

// Asset-type keys accepted by the upload validators. These mirror the
// models.AssetType* constants; they are duplicated here as plain strings so
// this package stays a leaf (the handler passes the model value straight in).
const (
	assetImage = "image"
	assetVideo = "video"
	assetFile  = "file"
)

// Server-enforced upload size ceilings, checked after the browser's
// direct-to-storage PUT via the HeadObject size (see UploadFileComplete).
const (
	MaxImageBytes = 10 << 20  // 10 MiB
	MaxFileBytes  = 100 << 20 // 100 MiB
	MaxVideoBytes = 5 << 30   // 5 GiB (Bunny enforces its own cap; informational)
)

// Validation failures. Handlers map these to 400/413 responses.
var (
	ErrEmptyFilename   = errors.New("filename is required")
	ErrUnsafeFilename  = errors.New("filename contains path separators or traversal")
	ErrDisallowedType  = errors.New("file extension is not allowed for this asset type")
	ErrArchiveRejected = errors.New("archive uploads are not allowed")
	ErrUnknownType     = errors.New("unknown asset type")
	ErrTooLarge        = errors.New("file exceeds the maximum allowed size")
)

// Per-type extension allowlists (lowercase, no leading dot). SVG is
// deliberately excluded from images: it can carry inline scripts and would
// bypass the sanitize package's allowlist once served from storage.
var (
	allowedImageExt = map[string]bool{
		"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "avif": true,
	}
	allowedVideoExt = map[string]bool{
		"mp4": true, "mov": true, "webm": true, "m4v": true, "mkv": true,
	}
	allowedFileExt = map[string]bool{
		"pdf": true, "txt": true, "csv": true, "md": true, "rtf": true,
		"doc": true, "docx": true, "ppt": true, "pptx": true,
		"xls": true, "xlsx": true, "odt": true, "ods": true, "odp": true,
	}
)

// archiveExt is rejected for every asset type (plan.md Task 11 "archive
// validation"). SCORM packages, the one legitimate archive upload, have their
// own dedicated importer and never flow through the media path.
var archiveExt = map[string]bool{
	"zip": true, "tar": true, "gz": true, "tgz": true, "rar": true,
	"7z": true, "bz2": true, "xz": true, "z": true, "lz": true,
}

// SanitizeFilename returns the safe base name of a client-supplied filename,
// rejecting empties, path separators, traversal ("..") and control
// characters. The result never contains a separator, so it cannot escape the
// storage-key prefix it is appended to.
func SanitizeFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrEmptyFilename
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", ErrUnsafeFilename
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ErrUnsafeFilename
		}
	}
	base := path.Base(name)
	if base == "." || base == ".." || base == "/" {
		return "", ErrUnsafeFilename
	}
	return base, nil
}

// ValidateUploadName sanitizes filename and checks its extension against the
// allowlist for assetType, rejecting archives outright. It returns the
// sanitized base filename that callers should persist and use in the storage
// key.
func ValidateUploadName(assetType, filename string) (string, error) {
	safe, err := SanitizeFilename(filename)
	if err != nil {
		return "", err
	}
	e := extension(safe)
	if archiveExt[e] {
		return "", ErrArchiveRejected
	}
	var allowed map[string]bool
	switch assetType {
	case assetImage:
		allowed = allowedImageExt
	case assetVideo:
		allowed = allowedVideoExt
	case assetFile:
		allowed = allowedFileExt
	default:
		return "", ErrUnknownType
	}
	if !allowed[e] {
		return "", ErrDisallowedType
	}
	return safe, nil
}

// MaxUploadBytes is the server-enforced size ceiling for assetType.
func MaxUploadBytes(assetType string) int64 {
	switch assetType {
	case assetImage:
		return MaxImageBytes
	case assetVideo:
		return MaxVideoBytes
	default:
		return MaxFileBytes
	}
}

// extension returns the lowercase extension without the dot, or "" if none.
func extension(filename string) string {
	i := strings.LastIndex(filename, ".")
	if i < 0 || i == len(filename)-1 {
		return ""
	}
	return strings.ToLower(filename[i+1:])
}
