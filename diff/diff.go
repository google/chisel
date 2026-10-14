// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package diff provides formatters to display differences between strings.
package diff

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/akedrou/textdiff"
)

const (
	colorRed       = "\x1b[31m"
	colorGreen     = "\x1b[32m"
	colorTurquoise = "\x1b[96m"
	colorReset     = "\x1b[0m"
)

const trailingNewLine = `\ No newline at end of file`

// Formatter generates diff output between two input string slices.
type Formatter struct {
	headerColor string
	addColor    string
	removeColor string
	resetColor  string
}

// NewDecoratedFormatter is a formatter that adds ANSI color codes to the
// output.
func NewDecoratedFormatter() *Formatter {
	return &Formatter{
		headerColor: colorTurquoise,
		addColor:    colorGreen,
		removeColor: colorRed,
		resetColor:  colorReset,
	}
}

// Format will format the differences between the input string slices.
// The Format of textdiff.Unified is:
// ---
// +++
// @@ -0,0 +1 @@
// <body>
// \ No newline at end of file
func (f *Formatter) Format(left, right []string, leftStart, rightStart int, writer io.Writer) error {
	str := textdiff.Unified("", "", strings.Join(left, "\n"), strings.Join(right, "\n"))
	headerIdx := strings.Index(str, "@")
	strFromHeader := str[headerIdx:]
	bodyIdx := strings.Index(strFromHeader, "\n")
	strBody := strFromHeader[bodyIdx+1:]

	var bytes bytes.Buffer
	leftCount := len(left)
	rightCount := len(right)
	bytes.WriteString(f.headerColor)
	bytes.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", leftStart, leftCount, rightStart, rightCount))
	bytes.WriteString(f.resetColor)

	scanner := bufio.NewScanner(strings.NewReader(strBody))
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), trailingNewLine) {
			continue
		}
		if strings.HasPrefix(scanner.Text(), "+") {
			bytes.WriteString(f.addColor)
		} else {
			bytes.WriteString(f.removeColor)
		}
		bytes.WriteString(scanner.Text() + "\n")
		bytes.WriteString(f.resetColor)
	}
	if scanner.Err() != nil {
		return fmt.Errorf("Error: unable to scan input : %v", scanner.Err())
	}

	_, err := writer.Write(bytes.Bytes())
	return err
}
