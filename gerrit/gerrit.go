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

// Package gerrit communicates with Gerrit and Gitiles API.
package gerrit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/sethvargo/go-retry"
)

const defaultNumRetries = 20

var (
	errBranchNotFound = errors.New("branch not found")
)

// WriteFileOpts are options for WriteFile.
type WriteFileOpts struct {
	Host       string
	Project    string
	Ref        string
	File       string
	Cookies    []*http.Cookie
	NumRetries uint
}

func httpDo(url string, cookies []*http.Cookie) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}
	client := &http.Client{}

	return client.Do(req)
}

func writeFile(opts WriteFileOpts, w io.Writer) error {
	url := fmt.Sprintf("https://%s/%s/+show/%s/%s?format=TEXT", opts.Host, url.QueryEscape(opts.Project), opts.Ref, url.QueryEscape(opts.File))
	resp, err := httpDo(url, opts.Cookies)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	src, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request errored with code: %v", resp.StatusCode)
	}

	dst := make([]byte, base64.StdEncoding.DecodedLen(len(src)))
	n, err := base64.StdEncoding.Decode(dst, src)
	if err != nil {
		return err
	}
	_, err = w.Write(dst[:n])

	return err
}

// WriteFile writes the file from the matching (host, project, commit) tuple to
// the writer.
func WriteFile(opts WriteFileOpts, w io.Writer) error {
	ctx := context.Background()

	backoff := retry.NewExponential(1 * time.Second)
	withRetries := retry.WithMaxRetries(defaultNumRetries, backoff)
	err := retry.Do(ctx, withRetries, func(ctx context.Context) error {
		err := writeFile(opts, w)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to write file: %v\n", err)
		}

		return retry.RetryableError(err)
	})
	if err != nil {
		return fmt.Errorf("failed to write file for host: %s project: %s commit: %s file: %s err: %v", opts.Host, opts.Project, opts.Ref, opts.File, err)
	}

	return nil
}

type commit struct {
	Revision string `json:"revision"`
}

func branchCommit(host, project, branch string, cookies []*http.Cookie) (string, error) {
	url := fmt.Sprintf("https://%s/projects/%s/branches/%s", host, url.QueryEscape(project), branch)
	resp, err := httpDo(url, cookies)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		if bytes.Contains(b, []byte("Not found: "+branch)) {
			return "", errBranchNotFound
		}
		return "", fmt.Errorf("request errored with code: %v", resp.StatusCode)
	}
	// https://gerrit-review.googlesource.com/Documentation/rest-api.html
	// Strip the magic prefix
	_, after, found := bytes.Cut(b, []byte{'\n'})
	if !found {
		return "", errors.New("unable to strip the magic prefix")
	}

	c := commit{}
	if err := json.Unmarshal(after, &c); err != nil {
		return "", err
	}

	return c.Revision, nil
}

// BranchCommit returns the revision for the matching (host, project, branch).
func BranchCommit(host, project, branch string, cookies []*http.Cookie, numRetries uint) (string, error) {
	ctx := context.Background()

	var queryResult string
	backoff := retry.NewExponential(1 * time.Second)
	withRetries := retry.WithMaxRetries(defaultNumRetries, backoff)
	err := retry.Do(ctx, withRetries, func(ctx context.Context) error {
		var innerErr error
		queryResult, innerErr = branchCommit(host, project, branch, cookies)
		if innerErr != nil {
			fmt.Fprintf(os.Stderr, "failed to read branch commit: %v\n", innerErr)
		}
		// Don't retry branch not found errors
		if errors.Is(innerErr, errBranchNotFound) {
			return innerErr
		}
		return retry.RetryableError(innerErr)
	})
	// Don't bubble up a missing branch as an error. The empty string provides
	// that signal to the caller.
	if errors.Is(err, errBranchNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read branch commit for host: %s project: %s branch: %s err: %v", host, project, branch, err)
	}

	return queryResult, nil
}
