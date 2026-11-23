package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Atishay8192261/logfold/internal/logscan"
)

type pathList []string

func (p *pathList) String() string {
	return strings.Join(*p, ", ")
}

func (p *pathList) Set(path string) error {
	*p = append(*p, path)
	return nil
}

func compareCommand(args []string) {
	flags := flag.NewFlagSet("compare", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: logfold compare [-baseline reference.log] incident.log [incident-2.log ...]")
		flags.PrintDefaults()
	}
	var baseline pathList
	flags.Var(&baseline, "baseline", "reference log path (repeat for multiple files)")
	flags.Parse(args)
	if flags.NArg() == 0 {
		flags.Usage()
		os.Exit(2)
	}

	scan := logscan.New()
	usedStdin := false
	for _, input := range []struct {
		paths    []string
		baseline bool
	}{{flags.Args(), false}, {baseline, true}} {
		for _, path := range input.paths {
			if path == "-" {
				if usedStdin {
					fatal("compare: stdin can only be used once")
				}
				usedStdin = true
			}
			if err := scanFile(scan, path, input.baseline); err != nil {
				fatal("compare: %s: %v", path, err)
			}
		}
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if err := out.Encode(scan.Report()); err != nil {
		fatal("compare: write report: %v", err)
	}
}

func scanFile(scan *logscan.Scan, path string, baseline bool) error {
	var reader io.Reader = os.Stdin
	name := "stdin"
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		name, reader = filepath.Base(path), file
		if strings.EqualFold(filepath.Ext(path), ".gz") {
			compressed, err := gzip.NewReader(reader)
			if err != nil {
				return err
			}
			defer compressed.Close()
			reader = compressed
		}
	}
	if err := scan.Start(name, baseline); err != nil {
		return err
	}
	defer scan.End()
	_, err := io.Copy(scan, reader)
	return err
}
