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

package commands

import (
	"context"
	"fmt"

	"flag"
	"github.com/google/subcommands"

	_ "embed"
)

//go:embed LICENSE
var license string

// LicenseCmd implements the subcommands interface for the "license"
// subcommand.
type LicenseCmd struct {
}

// Name returns the subcommand name.
func (l *LicenseCmd) Name() string { return "license" }

// Synopsis describes the subcommand behavior.
func (l *LicenseCmd) Synopsis() string {
	return "Display the application license."
}

// Usage describes the subcommand usage.
func (l *LicenseCmd) Usage() string {
	return "license"
}

// SetFlags describes the subcommand flags.
func (l *LicenseCmd) SetFlags(f *flag.FlagSet) {}

// Execute prints the embedded license.
func (l *LicenseCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	fmt.Println(license)

	return subcommands.ExitSuccess
}
