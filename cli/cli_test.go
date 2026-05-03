package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListTypes(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"list-types"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "cmb_debit") {
		t.Fatalf("missing adapter list: %s", out.String())
	}
}

func TestConfigInit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"config", "init", "-out", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "openai-compatible") {
		t.Fatalf("unexpected config: %s", data)
	}
}

func TestConvertRequiresPDFArgument(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"convert", "-type", "cmb_debit"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "expected at least one PDF file") {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
}

func TestNormalizeConvertFlagsAfterPDF(t *testing.T) {
	args := normalizeFlagArgs([]string{
		"/tmp/statement.pdf",
		"--type", "cmb_debit",
		"--config=config.json",
		"--out", "output",
	}, map[string]bool{
		"-type": true, "--type": true,
		"-config": true, "--config": true,
		"-out": true, "--out": true,
	})
	want := []string{"--type", "cmb_debit", "--config=config.json", "--out", "output", "/tmp/statement.pdf"}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized args = %#v, want %#v", args, want)
	}
}
