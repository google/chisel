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

// Package truncate provides functions to reduce the size of a string
// while respecting rune boundaries.
package truncate

import (
	"strings"
	"unicode/utf8"
)

// Trim returns a new string that is smaller than maxSize bytes. It does not
// simply cut the string at the byte boundary, but trims to the nearest legal
// rune. No truncation occurs if a negative maxSize is provided.
func Trim(s string, maxSize int) string {
	if maxSize < 0 {
		return s
	}

	// len() will return the number of bytes and not the number of runes.
	if len(s) <= maxSize {
		return s
	}
	asBytes := []byte(s[:maxSize])

	for len(asBytes) > 0 {
		if r, _ := utf8.DecodeLastRune(asBytes); r != utf8.RuneError {
			break
		}
		asBytes = asBytes[:len(asBytes)-1]
	}

	return string(asBytes)
}

// Join joins the lines with the separator, but limits the total size to maxSize.
// It avoids joining the remaining lines once the maxSize is exceeded, and
// ensures the returned string respects rune boundaries (using Trim).
// Invalid UTF-8 is replaced with the replacement character U+FFFD.
func Join(lines []string, sep string, maxSize int) string {
	if maxSize < 0 {
		return strings.ToValidUTF8(strings.Join(lines, sep), "\uFFFD")
	}
	if maxSize == 0 || len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	currLen := 0
	for i, line := range lines {
		if i > 0 {
			b.WriteString(sep)
			currLen += len(sep)
		}

		b.WriteString(line)
		currLen += len(line)

		if currLen > maxSize {
			trimmed := Trim(b.String(), maxSize)
			return strings.ToValidUTF8(trimmed, "\uFFFD")
		}
	}

	return strings.ToValidUTF8(b.String(), "\uFFFD")
}
