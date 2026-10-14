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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"text/template"

	"flag"
	"log"
	"github.com/google/subcommands"
	"chisel/constants"

	_ "embed"
)

const (
	grafanaReleaseURL   = "https://dl.grafana.com/enterprise/release/"
	version             = "13.0.1"
	tarName             = "grafana-enterprise-" + version + ".linux-amd64.tar.gz"
	dlURL               = grafanaReleaseURL + tarName
	versionedGrafanaDir = "grafana-" + version
)

//go:embed model.json
var model string

//go:embed queries/project_deltas.sql
var projectDeltasQuery string

//go:embed queries/lines_changed_by_project.sql
var linesChangedByProjectQuery string

//go:embed queries/file_deltas.sql
var fileDeltasQuery string

//go:embed queries/lines_changed_over_time.sql
var linesChangedOverTimeQuery string

//go:embed queries/branch_aware_commit_stats.sql
var branchAwareCommitStatsQuery string

//go:embed queries/branch_aware_file_stats.sql
var branchAwareFileStatsQuery string

//go:embed queries/branch_agnostic_commit_stats.sql
var branchAgnosticCommitStatsQuery string

//go:embed queries/branch_agnostic_file_stats.sql
var branchAgnosticFileStatsQuery string

// GrafanaCmd implements the subcommands interface for the "contents"
// subcommand.
type GrafanaCmd struct {
	port int
}

// Name returns the subcommand name.
func (c *GrafanaCmd) Name() string { return "grafana" }

// Synopsis describes the subcommand behavior.
func (c *GrafanaCmd) Synopsis() string {
	return "Grafana subcommands help setting up a standalone grafana instance to view a local sqlite database."
}

// Usage describes the subcommand usage.
func (c *GrafanaCmd) Usage() string {
	return "grafana <flags> <install|server|createuser|createdashboard>\n"
}

// SetFlags describes the subcommand flags.
func (c *GrafanaCmd) SetFlags(f *flag.FlagSet) {
	f.IntVar(&c.port, "port", 3000, "Port for grafana server")
}

// Execute forwards the flags and options to the appropriate handler.
func (c *GrafanaCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	args := f.Args()
	if len(args) == 0 {
		log.Printf("Improper usage. Usage: %v", c.Usage())
		return subcommands.ExitUsageError
	}
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}
	o := opts[0].(*Opts)
	appConfig, err := loadConfigWithOverrides(o)
	if err != nil {
		log.Printf("loadConfigWithOverrides failed: %v", err)
		return subcommands.ExitFailure
	}

	grafanaDir := path.Join(appConfig.WorkDir, constants.GrafanaDir)
	action := args[0]
	switch action {
	case "install":
		if err := install(grafanaDir); err != nil {
			log.Print(err)
			return subcommands.ExitFailure
		}
	case "server":
		if err := server(grafanaDir, c.port); err != nil {
			log.Print(err)
			return subcommands.ExitFailure
		}
	case "createuser":
		if err := createUser(c.port); err != nil {
			log.Print(err)
			return subcommands.ExitFailure
		}
	case "createdashboard":
		sqlitePath, err := filepath.Abs(path.Join(appConfig.WorkDir, constants.SqliteDir, constants.SqliteDBFile))
		if err != nil {
			log.Print(err)
			return subcommands.ExitFailure
		}
		if err := createDashboard(c.port, sqlitePath); err != nil {
			log.Print(err)
			return subcommands.ExitFailure
		}
	default:
		log.Printf("Unsupported action: %q", action)
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

func install(grafanaDir string) error {
	if err := os.MkdirAll(grafanaDir, 0755); err != nil {
		return err
	}
	if err := exec.Command("wget", dlURL, "-P", grafanaDir).Run(); err != nil {
		return fmt.Errorf("wget failed: %v", err)
	}

	cmd := exec.Command("tar", "-zxf", tarName)
	cmd.Dir = grafanaDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar failed: %v", err)
	}

	cmd = exec.Command("bin/grafana", "cli", "--pluginsDir", "data/plugins", "plugins", "install", "frser-sqlite-datasource")
	cmd.Dir = path.Join(grafanaDir, versionedGrafanaDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sqlite plugin install failed: %v", err)
	}

	return nil
}

func server(grafanaDir string, port int) error {
	cmd := exec.Command("bin/grafana", "server")
	cmd.Dir = path.Join(grafanaDir, versionedGrafanaDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("GF_SERVER_HTTP_PORT=%d", port),
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("server failed: %v", err)
	}

	return nil
}

func httpDoJSON(method, url string, data []byte) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("Error creating request: %s", err)
	}

	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Error sending request: %s", err)
	}
	defer resp.Body.Close()
	rspData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Check the response status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("Request failed with status code: %d", resp.StatusCode)
	}

	return rspData, nil
}

func createUser(port int) error {
	url := fmt.Sprintf("http://admin:admin@localhost:%d/api/admin/users", port)
	data := `{"name":"Chisel", "email":"", "login":"chisel", "password":"chisel", "OrgId":1}`
	rsp, err := httpDoJSON("POST", url, []byte(data))
	if err != nil {
		return err
	}
	type UsersRsp struct {
		ID int `json:"id,omitempty"`
	}
	var usersRsp UsersRsp
	if err := json.Unmarshal(rsp, &usersRsp); err != nil {
		return err
	}
	url = fmt.Sprintf("http://admin:admin@localhost:%d/api/orgs/1/users/%d", port, usersRsp.ID)
	data = `{"role":"Admin"}`
	if _, err := httpDoJSON("PATCH", url, []byte(data)); err != nil {
		return err
	}

	return nil
}

func createDashboard(port int, dbPath string) error {
	url := fmt.Sprintf("http://admin:admin@localhost:%d/api/datasources", port)
	data := fmt.Sprintf(`{
		"name": "Chisel",
		"type": "frser-sqlite-datasource",
		"access": "proxy",
		"isDefault": true,
		"jsonData": {
			"path": "%s"
		}
	}`, dbPath)

	_, err := httpDoJSON("POST", url, []byte(data))
	if err != nil {
		return err
	}

	tmpl, err := template.New("dashboard").Parse(model)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	dataMap := map[string]string{
		"ProjectDeltasQuery":             escapeJSONString(projectDeltasQuery),
		"LinesChangedByProjectQuery":     escapeJSONString(linesChangedByProjectQuery),
		"FileDeltasQuery":                escapeJSONString(fileDeltasQuery),
		"LinesChangedOverTimeQuery":      escapeJSONString(linesChangedOverTimeQuery),
		"BranchAwareCommitStatsQuery":    escapeJSONString(branchAwareCommitStatsQuery),
		"BranchAwareFileStatsQuery":      escapeJSONString(branchAwareFileStatsQuery),
		"BranchAgnosticCommitStatsQuery": escapeJSONString(branchAgnosticCommitStatsQuery),
		"BranchAgnosticFileStatsQuery":   escapeJSONString(branchAgnosticFileStatsQuery),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, dataMap); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}
	finalModel := buf.String()

	url = fmt.Sprintf("http://admin:admin@localhost:%d/api/dashboards/db", port)
	_, err = httpDoJSON("POST", url, []byte(finalModel))

	return err
}

func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
