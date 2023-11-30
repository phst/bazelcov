// Copyright 2023 Philipp Stephani
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Binary bazelcov helps generating a coverage report for projects that use
// Bazel.  It runs bazel coverage and then genhtml to write a coverage report
// in HTML form.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	var bazel, genhtml, reportDir string
	flag.Usage = usage
	flag.StringVar(&bazel, "bazel", "bazel", "name or path of the Bazel program")
	flag.StringVar(&genhtml, "genhtml", "genhtml", "name or path of the genhtml program")
	flag.StringVar(&reportDir, "output", "coverage-report", "directory into which to write the coverage report")
	flag.Parse()
	targets := flag.Args()
	if len(targets) == 0 {
		targets = []string{"//..."}
	}
	bazel, err := exec.LookPath(bazel)
	if err != nil {
		log.Fatal(err)
	}
	genhtml, err = exec.LookPath(genhtml)
	if err != nil {
		log.Fatal(err)
	}
	reportDir, err = filepath.Abs(reportDir)
	if err != nil {
		log.Fatal(err)
	}
	workspace, err := bazelWorkspace(bazel)
	if err != nil {
		log.Fatal(err)
	}
	reportFile, err := collectCoverage(bazel, targets)
	if err != nil {
		log.Fatal(err)
	}
	report, err := os.ReadFile(reportFile)
	if err != nil {
		log.Fatal(err)
	}
	report = munge(report, workspace)
	if err := genHTML(workspace, genhtml, report, reportDir); err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: bazelcov [flags...] [targets...]")
	flag.PrintDefaults()
}

func bazelWorkspace(prog string) (string, error) {
	cmd := exec.Command(prog, "info", "workspace")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("can’t determine Bazel workspace: %s", err)
	}
	n := len(out)
	if n == 0 {
		return "", errors.New("can’t determine Bazel workspace: empty output")
	}
	// Chop off trailing newline.
	if out[n-1] == '\n' {
		out = out[:n-1]
	}
	return string(out), nil
}

func collectCoverage(prog string, targets []string) (string, error) {
	args := []string{
		"coverage",
		"--color=no",
		"--curses=no",
		"--test_output=summary",
		"--test_summary=terse",
		"--noshow_progress",
		"--noshow_loading_progress",
		"--noprogress_in_terminal_title",
		"--combined_report=lcov",
		"--",
	}
	args = append(args, targets...)
	buf := new(bytes.Buffer)
	cmd := exec.Command(prog, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error running %s coverage: %s", prog, err)
	}
	matches := logRegexp.FindAllSubmatch(buf.Bytes(), -1)
	if n := len(matches); n != 1 {
		return "", fmt.Errorf("found %d coverage report logs in %s output instead of one", n, prog)
	}
	return string(matches[0][1]), nil
}

var logRegexp = regexp.MustCompile(`(?m)^INFO: LCOV coverage report is located at (/.+\.dat)$`)

func munge(report []byte, workspace string) []byte {
	// coverage.py occasionally writes branch coverage data for line 0,
	// which genhtml doesn’t accept.
	report = lineZeroBranchRegexp.ReplaceAllLiteral(report, nil)
	// Make filenames absolute.
	makeAbsolute := func(b []byte) []byte {
		const prefix = "SF:"
		rel := strings.TrimPrefix(string(b), prefix)
		abs := filepath.Join(workspace, rel)
		return []byte(prefix + abs)
	}
	return relativeFilenameRegexp.ReplaceAllFunc(report, makeAbsolute)
}

var (
	lineZeroBranchRegexp   = regexp.MustCompile(`(?m)^BRDA:0,.+\n`)
	relativeFilenameRegexp = regexp.MustCompile(`(?m)^SF:[^/].+$`)
)

func genHTML(workspace, prog string, report []byte, output string) error {
	temp, err := os.CreateTemp("", "coverage-*.info")
	if err != nil {
		return fmt.Errorf("can’t write report: %s", err)
	}
	defer temp.Close()
	defer os.Remove(temp.Name())

	if _, err := temp.Write(report); err != nil {
		return fmt.Errorf("can’t write report: %s", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("can’t write report: %s", err)
	}

	cmd := exec.Command(
		"genhtml",
		"--output-directory="+output,
		"--branch-coverage",
		"--demangle-cpp",
		"--rc=genhtml_demangle_cpp_params=--no-strip-underscore",
		"--",
		temp.Name(),
	)
	cmd.Dir = workspace
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("can’t write report: %s", err)
	}
	return nil
}
