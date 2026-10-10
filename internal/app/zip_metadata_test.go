package app

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A file-count cap alone cannot bound zip.Reader's allocation of Extra fields.
func TestZIPMetadataMemoryBound(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "metadata.zip")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for i := 0; i < 270; i++ {
		h := &zip.FileHeader{Name: fmt.Sprintf("file-%03d.txt", i), Method: zip.Store, Extra: make([]byte, 32768)}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	err = preflightZip(filename, 5000)
	e, ok := err.(*uploadFailure)
	if !ok || e.status != 413 {
		t.Fatalf("large metadata must fail before zip.Reader allocation: %v", err)
	}
}
