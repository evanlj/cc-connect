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

var askPost = apiPost

type askTimelineEvent struct {
	Type      string `json:"type"`
	AtMS      int64  `json:"at_ms"`
	Content   string `json:"content,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
}

type askResponse struct {
	Status     string             `json:"status"`
	SessionKey string             `json:"session_key"`
	Content    string             `json:"content"`
	LatencyMS  int64              `json:"latency_ms"`
	ToolCount  int                `json:"tool_count"`
	Timeline   []askTimelineEvent `json:"timeline"`
}

func runAsk(args []string) {
	if code := runAskWithIO(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func runAskWithIO(args []string, stdout, stderr io.Writer) int {
	var (
		project     string
		sessionKey  string
		dataDir     string
		prompt      string
		timeoutSec  = 120
		speak       bool
		speakPrefix string
		outputJSON  bool
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
					fmt.Fprintln(stderr, "Error: --timeout-sec must be a positive integer")
					return 1
				}
				timeoutSec = n
			}
		case "--speak":
			speak = true
		case "--json":
			outputJSON = true
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
			printAskUsageTo(stdout)
			return 0
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
		fmt.Fprintln(stderr, "Error: session key is required (use --session or CC_SESSION_KEY)")
		return 1
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(stderr, "Error: prompt is required")
		printAskUsageTo(stdout)
		return 1
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		return 1
	}

	payload, _ := json.Marshal(map[string]any{
		"project":      project,
		"session_key":  sessionKey,
		"prompt":       prompt,
		"timeout_sec":  timeoutSec,
		"speak":        speak,
		"speak_prefix": speakPrefix,
	})

	resp, err := askPost(sockPath, "/ask", payload)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		return 1
	}

	var result askResponse
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(stderr, "Error: invalid response JSON: %v\n", err)
		return 1
	}

	if outputJSON {
		out, err := json.Marshal(result)
		if err != nil {
			fmt.Fprintf(stderr, "Error: marshal JSON output failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}

	fmt.Fprintf(stdout, "Session: %s\n", result.SessionKey)
	fmt.Fprintf(stdout, "Latency: %d ms\n", result.LatencyMS)
	if result.ToolCount > 0 {
		fmt.Fprintf(stdout, "Tools: %d\n", result.ToolCount)
	}
	fmt.Fprintln(stdout, "----")
	fmt.Fprintln(stdout, result.Content)
	return 0
}

func printAskUsage() {
	printAskUsageTo(os.Stdout)
}

func printAskUsageTo(w io.Writer) {
	fmt.Fprintln(w, `Usage: cc-connect ask [options] <prompt>

Ask the model in a target session and wait for final response (sync).

Options:
  -p, --project <name>       Target project (optional if only one project)
  -s, --session <key>        Target session key (or use CC_SESSION_KEY)
      --prompt <text>        Prompt text (or pass as positional text)
      --timeout-sec <sec>    Timeout in seconds (default: 120)
      --json                 Print structured JSON output
      --speak                Also send answer back to the session chat
      --speak-prefix <text>  Prefix when --speak is enabled
      --data-dir <path>      Data directory (default: ~/.cc-connect)
  -h, --help                 Show this help

Examples:
  cc-connect ask -s "feishu:oc_xxx:ou_yyy" "请给出三条风险"
  cc-connect ask -p myproj -s "feishu:oc_xxx:ou_yyy" --timeout-sec 180 --prompt "Summarize this task"
  cc-connect ask -s "feishu:oc_xxx:ou_yyy" --json "输出结构化结果"
  cc-connect ask -s "feishu:oc_xxx:ou_yyy" --speak --speak-prefix "【剑主】" "从工程角度审查方案"`)
}
