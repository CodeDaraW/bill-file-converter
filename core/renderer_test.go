package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectImagesReturnsAllPages(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"page-1.png", "page-2.png", "page-3.png", "page-4.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	images, err := collectImages(dir, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 4 {
		t.Fatalf("expected all images, got %d", len(images))
	}
	if filepath.Base(images[3].Path) != "page-4.png" {
		t.Fatalf("expected fourth image to be page-4.png, got %#v", images)
	}
}
