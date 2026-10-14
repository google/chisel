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
	"fmt"
	"strings"
)

type emailMapperFn func(email string) (string, error)

func mapEmail(email string) (string, error) {
	_, domain, found := strings.Cut(email, "@")
	domain = "@" + domain
	if !found {
		return "", fmt.Errorf("couldn't decode email %q", email)
	}

	return domain, nil
}

func emailMapperFactory(mapper string) (emailMapperFn, error) {
	return mapEmail, nil
}
