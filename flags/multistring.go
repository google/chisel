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

// Package flags implements stdlib friendly flags.
package flags

import "strings"

// MultiString implements the flag API.
type MultiString []string

// String representation of a MultiString.
func (m *MultiString) String() string {
	return strings.Join(*m, ", ")
}

// Set adds an input value to the MultiString.
func (m *MultiString) Set(value string) error {
	*m = append(*m, value)
	return nil
}

// Get returns the MultiString as a string slice.
func (m *MultiString) Get() any {
	return []string(*m)
}
