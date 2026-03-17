package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func runAsk(args []string) {
	var (
		project     string
		sessionKey  string
		dataDir     string
		prompt      string
		timeoutSec  = 120
		speak       bool
		speakPrefix string
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
		case "--prompt":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		case "--timeout", "--timeout-sec":
			if i+1 < len(args) {
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n <= 0 {
					fmt.Fprintln(os.Stderr, "Error: --timeout-sec must be a positive integer")
					os.Exit(1)
				}
				timeoutSec = n
			}
		case "--speak":
			speak = true
		case "--speak-prefix":
			if i+1 < len(args) {
				i++
				speakPrefix = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--help", "-h":
			printAskUsage()
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
	if prompt == "" && len(positional) > 0 {
		prompt = strings.Join(positional, " ")
	}

	if strings.TrimSpace(sessionKey) == "" {
		fmt.Fprintln(os.Stderr, "Error: session key is required (use --session or CC_SESSION_KEY)")
		os.Exit(1)
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "Error: prompt is required")
		printAskUsage()
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]any{
		"project":      project,
		"session_key":  sessionKey,
		"prompt":       prompt,
		"timeout_sec":  timeoutSec,
		"speak":        speak,
		"speak_prefix": speakPrefix,
	})

	resp, err := apiPost(sockPath, "/ask", payload)
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

	var result struct {
		Status     string `json:"status"`
		SessionKey string `json:"session_key"`
		Content    string `json:"content"`
		LatencyMS  int64  `json:"latency_ms"`
		ToolCount  int    `json:"tool_count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid response JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Session: %s\n", result.SessionKey)
	fmt.Printf("Latency: %d ms\n", result.LatencyMS)
	if result.ToolCount > 0 {
		fmt.Printf("Tools: %d\n", result.ToolCount)
	}
	fmt.Println("----")
	fmt.Println(result.Content)
}

func printAskUsage() {
	fmt.Println(`Usage: cc-connect ask [options] <prompt>

Ask the model in a target session and wait for final response (sync).

Options:
  -p, --project <name>       Target project (optional if only one project)
  -s, --session <key>        Target session key (or use CC_SESSION_KEY)
      --prompt <text>        Prompt text (or pass as positional text)
      --timeout-sec <sec>    Timeout in seconds (default: 120)
      --speak                Also send answer back to the session chat
      --speak-prefix <text>  Prefix when --speak is enabled
      --data-dir <path>      Data directory (default: ~/.cc-connect)
  -h, --help                 Show this help

Examples:
  cc-connect ask -s "feishu:oc_xxx:ou_yyy" "请给出三条风险"
  cc-connect ask -p myproj -s "feishu:oc_xxx:ou_yyy" --timeout-sec 180 --prompt "Summarize this task"
  cc-connect ask -s "feishu:oc_xxx:ou_yyy" --speak --speak-prefix "【剑主】" "从工程角度审查方案"`)
}
