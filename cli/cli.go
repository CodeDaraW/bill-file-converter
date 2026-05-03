package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/deb-sig/bill-file-converter/core"
	"github.com/deb-sig/bill-file-converter/core/adapters"
	"github.com/deb-sig/bill-file-converter/core/providers"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "convert":
		return runConvert(ctx, args[1:], stdout, stderr, false)
	case "inspect":
		return runConvert(ctx, args[1:], stdout, stderr, true)
	case "list-types":
		return runListTypes(stdout)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "providers":
		return runProviders(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runConvert(ctx context.Context, args []string, stdout, stderr io.Writer, inspect bool) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	typeKey := fs.String("type", "", "bill type adapter key")
	configPath := fs.String("config", "config.json", "config file path")
	outputDir := fs.String("out", "output", "output directory")
	args = normalizeFlagArgs(args, map[string]bool{
		"-type": true, "--type": true,
		"-config": true, "--config": true,
		"-out": true, "--out": true,
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(stderr, "expected at least one PDF file")
		return 2
	}
	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	provider, err := providers.New(config.Provider)
	if err != nil {
		fmt.Fprintf(stderr, "create provider: %v\n", err)
		return 1
	}
	renderer := core.ExternalRenderer{Command: config.Renderer.Command, DPI: config.Renderer.DPI}
	input := core.Input{}
	for _, path := range fs.Args() {
		input.Files = append(input.Files, core.InputFile{Path: path, FileName: filepath.Base(path)})
	}
	if len(input.Files) == 1 {
		input.Path = input.Files[0].Path
		input.FileName = input.Files[0].FileName
		input.Files = nil
	}
	result, err := core.Convert(ctx, input, core.Options{
		Provider:        provider,
		Renderer:        renderer,
		AdapterKey:      *typeKey,
		AdapterRegistry: adapters.BuiltinRegistry(),
		OutputDir:       *outputDir,
		SkipCSV:         inspect,
		MaxConcurrency:  config.Conversion.MaxConcurrency,
		LogWriter:       stderr,
		Temperature:     config.Provider.Temperature,
	})
	if err != nil {
		var validation core.ValidationError
		if errors.As(err, &validation) {
			fmt.Fprintf(stderr, "validation failed; wrote %s\n", result.Artifacts.JSONPath)
			return 1
		}
		fmt.Fprintf(stderr, "convert: %v\n", err)
		return 1
	}
	if inspect {
		fmt.Fprintf(stdout, "inspection completed: %s\n", result.Artifacts.JSONPath)
		return 0
	}
	fmt.Fprintf(stdout, "task %s wrote %s and %s\n", result.TaskID, result.Artifacts.JSONPath, result.Artifacts.CSVPath)
	return 0
}

func runListTypes(stdout io.Writer) int {
	for _, adapter := range adapters.BuiltinRegistry().List() {
		fmt.Fprintf(stdout, "%s\t%s\n", adapter.Key, adapter.Name)
	}
	return 0
}

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "init" {
		fmt.Fprintln(stderr, "usage: config init [-out config.json]")
		return 2
	}
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "config.json", "config output path")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintf(stderr, "%s already exists\n", *out)
		return 1
	}
	if err := WriteDefaultConfig(*out); err != nil {
		fmt.Fprintf(stderr, "write config: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", *out)
	return 0
}

func runProviders(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "test" {
		fmt.Fprintln(stderr, "usage: providers test [-config config.json]")
		return 2
	}
	fs := flag.NewFlagSet("providers test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "config.json", "config file path")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	provider, err := providers.New(config.Provider)
	if err != nil {
		fmt.Fprintf(stderr, "create provider: %v\n", err)
		return 1
	}
	if pinger, ok := provider.(core.Pinger); ok {
		if err := pinger.Ping(ctx); err != nil {
			fmt.Fprintf(stderr, "provider ping failed: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintln(stderr, "warning: provider does not support ping; only static config validation performed")
	}
	renderer := core.ExternalRenderer{Command: config.Renderer.Command, DPI: config.Renderer.DPI}
	if err := renderer.Check(ctx); err != nil {
		fmt.Fprintf(stderr, "renderer: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "provider reachable and renderer usable")
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: bill-file-converter <convert|inspect|list-types|config|providers> [options]")
}

func normalizeFlagArgs(args []string, flagsWithValue map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(arg) == 0 || arg[0] != '-' || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := arg
		for idx, ch := range arg {
			if ch == '=' {
				name = arg[:idx]
				break
			}
		}
		if flagsWithValue[name] && !hasInlineValue(arg) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positionals...)
}

func hasInlineValue(arg string) bool {
	for _, ch := range arg {
		if ch == '=' {
			return true
		}
	}
	return false
}
