package media

import (
	"errors"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr error
	}{
		{"photo.png", "photo.png", nil},
		{"  report.pdf  ", "report.pdf", nil},
		{"", "", ErrEmptyFilename},
		{"   ", "", ErrEmptyFilename},
		{"../../etc/passwd", "", ErrUnsafeFilename},
		{"a/b.png", "", ErrUnsafeFilename},
		{`a\b.png`, "", ErrUnsafeFilename},
		{"bad..name.png", "", ErrUnsafeFilename},
		{"ctrl\x00.png", "", ErrUnsafeFilename},
	}
	for _, tc := range cases {
		got, err := SanitizeFilename(tc.in)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("SanitizeFilename(%q) err = %v, want %v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateUploadName(t *testing.T) {
	cases := []struct {
		assetType string
		filename  string
		want      string
		wantErr   error
	}{
		{assetImage, "avatar.PNG", "avatar.PNG", nil},
		{assetImage, "clip.mp4", "", ErrDisallowedType},
		{assetImage, "logo.svg", "", ErrDisallowedType}, // SVG intentionally excluded
		{assetFile, "notes.pdf", "notes.pdf", nil},
		{assetFile, "sheet.xlsx", "sheet.xlsx", nil},
		{assetFile, "malware.exe", "", ErrDisallowedType},
		{assetFile, "package.zip", "", ErrArchiveRejected},
		{assetFile, "backup.tar.gz", "", ErrArchiveRejected},
		{assetVideo, "lecture.mp4", "lecture.mp4", nil},
		{assetVideo, "lecture.pdf", "", ErrDisallowedType},
		{"bogus", "x.png", "", ErrUnknownType},
		{assetFile, "../escape.pdf", "", ErrUnsafeFilename},
		{assetImage, "noext", "", ErrDisallowedType},
	}
	for _, tc := range cases {
		got, err := ValidateUploadName(tc.assetType, tc.filename)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("ValidateUploadName(%q,%q) err = %v, want %v", tc.assetType, tc.filename, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("ValidateUploadName(%q,%q) = %q, want %q", tc.assetType, tc.filename, got, tc.want)
		}
	}
}

func TestMaxUploadBytes(t *testing.T) {
	if MaxUploadBytes(assetImage) != MaxImageBytes {
		t.Error("image ceiling mismatch")
	}
	if MaxUploadBytes(assetVideo) != MaxVideoBytes {
		t.Error("video ceiling mismatch")
	}
	if MaxUploadBytes(assetFile) != MaxFileBytes {
		t.Error("file ceiling mismatch")
	}
	if MaxUploadBytes("anything-else") != MaxFileBytes {
		t.Error("unknown type should default to the file ceiling")
	}
}
