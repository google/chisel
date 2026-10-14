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
	"chisel/constants"
)

// VersionCmd implements the subcommands interface for the "version"
// subcommand.
type VersionCmd struct {
}

// Name returns the subcommand name.
func (v *VersionCmd) Name() string { return "version" }

// Synopsis describes the subcommand behavior.
func (v *VersionCmd) Synopsis() string {
	return "Display the application version."
}

// Usage describes the subcommand usage.
func (v *VersionCmd) Usage() string {
	return "version"
}

// SetFlags describes the subcommand flags.
func (v *VersionCmd) SetFlags(f *flag.FlagSet) {}

// Execute forwards the flags and options to the appropriate handler.
func (v *VersionCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	fmt.Println(constants.Version)

	return subcommands.ExitSuccess
}
