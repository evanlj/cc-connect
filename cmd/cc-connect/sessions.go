package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type sessionListItem struct {
	Project    string `json:"project"`
	SessionKey string `json:"session_key"`
	Platform   string `json:"platform"`
}

var sessionsGet = apiGet

func runSessions(args []string) {
	if code := runSessionsWithIO(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func runSessionsWithIO(args []string, stdout, stderr io.Writer) int {
	var (
		project    string
		dataDir    string
		outputJSON bool
	)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--json":
			outputJSON = true
		case "--help", "-h":
			printSessionsUsageTo(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "Error: unknown argument: %s\n", args[i])
			printSessionsUsageTo(stdout)
			return 1
		}
	}

	if project == "" {
		project = os.Getenv("CC_PROJECT")
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		return 1
	}

	apiPath := "/sessions"
	if strings.TrimSpace(project) != "" {
		apiPath += "?project=" + url.QueryEscape(strings.TrimSpace(project))
	}

	resp, err := sessionsGet(sockPath, apiPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: failed to connect to cc-connect: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		return 1
	}

	var sessions []sessionListItem
	if err := json.Unmarshal(body, &sessions); err != nil {
		fmt.Fprintf(stderr, "Error: invalid response JSON: %v\n", err)
		return 1
	}

	if outputJSON {
		out, err := json.Marshal(sessions)
		if err != nil {
			fmt.Fprintf(stderr, "Error: marshal JSON output failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}

	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "No active sessions.")
		return 0
	}

	fmt.Fprintf(stdout, "Active sessions (%d):\n\n", len(sessions))
	for _, item := range sessions {
		fmt.Fprintf(stdout, "- [%s] %s (%s)\n", item.Project, item.SessionKey, item.Platform)
	}
	return 0
}

func apiGet(sockPath, path string) (*http.Response, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	return client.Get("http://unix" + path)
}

func printSessionsUsage() {
	printSessionsUsageTo(os.Stdout)
}

func printSessionsUsageTo(w io.Writer) {
	fmt.Fprintln(w, `Usage: cc-connect sessions [options]

List active sessions from cc-connect internal API.

Options:
  -p, --project <name>   Filter by project name
      --json             Print structured JSON array
      --data-dir <path>  Data directory (default: ~/.cc-connect)
  -h, --help             Show this help

Examples:
  cc-connect sessions
  cc-connect sessions --json
  cc-connect sessions -p myproj --json`)
}
