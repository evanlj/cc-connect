package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func runSendFile(args []string) {
	var (
		project    string
		sessionKey string
		dataDir    string
		filePath   string
	)

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		case "--session", "--session-key", "-s":
			if i+1 < len(args) {
				i++
				sessionKey = args[i]
			}
		case "--file", "--path":
			if i+1 < len(args) {
				i++
				filePath = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--help", "-h":
			printSendFileUsage()
			return
		default:
			positional = append(positional, args[i])
		}
	}

	if project == "" {
		project = os.Getenv("CC_PROJECT")
	}
	if sessionKey == "" {
		sessionKey = os.Getenv("CC_SESSION_KEY")
	}
	if filePath == "" && len(positional) > 0 {
		filePath = strings.Join(positional, " ")
	}

	sessionKey = strings.TrimSpace(sessionKey)
	filePath = strings.TrimSpace(filePath)
	if sessionKey == "" {
		fmt.Fprintln(os.Stderr, "Error: session key is required (use --session or CC_SESSION_KEY)")
		os.Exit(1)
	}
	if filePath == "" {
		fmt.Fprintln(os.Stderr, "Error: file path is required")
		printSendFileUsage()
		os.Exit(1)
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: file not found: %v\n", err)
		os.Exit(1)
	}
	if stat.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: path is a directory, not a file: %s\n", filePath)
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]string{
		"project":     project,
		"session_key": sessionKey,
		"file_path":   filePath,
	})

	resp, err := apiPost(sockPath, "/send-file", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		os.Exit(1)
	}

	fmt.Printf("File sent successfully: %s\n", filepath.Base(filePath))
}

func printSendFileUsage() {
	fmt.Println(`Usage: cc-connect send-file [options] <file_path>

Upload and send a local file to an active cc-connect session.

Options:
  -p, --project <name>       Target project (optional if only one project)
  -s, --session <key>        Target session key (or use CC_SESSION_KEY)
      --file <path>          File path (or pass as positional argument)
      --data-dir <path>      Data directory (default: ~/.cc-connect)
  -h, --help                 Show this help

Examples:
  cc-connect send-file -s "feishu:oc_xxx:ou_yyy" "D:\reports\daily.md"
  cc-connect send-file -p myproj -s "feishu:oc_xxx:ou_yyy" --file "D:\docs\方案评审.pdf"`)
}
