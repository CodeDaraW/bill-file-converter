package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ExternalRenderer struct {
	Command string
	DPI     int
}

func NewExternalRenderer() ExternalRenderer {
	return ExternalRenderer{DPI: 200}
}

func (r ExternalRenderer) Check(ctx context.Context) error {
	cmd, err := r.command()
	if err != nil {
		return err
	}
	_, err = exec.LookPath(cmd)
	if err != nil {
		return fmt.Errorf("PDF renderer %q not found: install poppler (pdftoppm) or mupdf (mutool)", cmd)
	}
	return nil
}

func (r ExternalRenderer) Render(ctx context.Context, input Input, outputDir string) ([]PageImage, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}
	inputPath, cleanup, err := materializeInput(input, outputDir)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cmd, err := r.command()
	if err != nil {
		return nil, err
	}
	if cmd == "mutool" {
		return r.renderWithMutool(ctx, inputPath, outputDir)
	}
	return r.renderWithPdftoppm(ctx, inputPath, outputDir)
}

func (r ExternalRenderer) command() (string, error) {
	if r.Command != "" {
		return r.Command, nil
	}
	if _, err := exec.LookPath("pdftoppm"); err == nil {
		return "pdftoppm", nil
	}
	if _, err := exec.LookPath("mutool"); err == nil {
		return "mutool", nil
	}
	return "pdftoppm", nil
}

func (r ExternalRenderer) renderWithPdftoppm(ctx context.Context, inputPath, outputDir string) ([]PageImage, error) {
	prefix := filepath.Join(outputDir, "page")
	dpi := r.DPI
	if dpi == 0 {
		dpi = 200
	}
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", fmt.Sprint(dpi), inputPath, prefix)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("render pdf with pdftoppm: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return collectImages(outputDir, "image/png")
}

func (r ExternalRenderer) renderWithMutool(ctx context.Context, inputPath, outputDir string) ([]PageImage, error) {
	dpi := r.DPI
	if dpi == 0 {
		dpi = 200
	}
	pattern := filepath.Join(outputDir, "page-%d.png")
	cmd := exec.CommandContext(ctx, "mutool", "draw", "-r", fmt.Sprint(dpi), "-o", pattern, inputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("render pdf with mutool: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return collectImages(outputDir, "image/png")
}

func materializeInput(input Input, outputDir string) (string, func(), error) {
	if input.Path != "" {
		return input.Path, func() {}, nil
	}
	tmp, err := os.CreateTemp(outputDir, "input-*.pdf")
	if err != nil {
		return "", nil, err
	}
	if _, err := io.Copy(tmp, input.Reader); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", nil, err
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

func collectImages(outputDir, mimeType string) ([]PageImage, error) {
	matches, err := filepath.Glob(filepath.Join(outputDir, "*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("renderer produced no page images")
	}
	images := make([]PageImage, len(matches))
	for i, path := range matches {
		images[i] = PageImage{Page: i + 1, Path: path, MIMEType: mimeType}
	}
	return images, nil
}
