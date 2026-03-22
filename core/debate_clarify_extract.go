package core

import (
	"encoding/json"
	"regexp"
	"strings"
)

var clarifyJSONCodeFencePattern = regexp.MustCompile("(?is)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")

type clarifyDecision struct {
	NeedMore     bool
	Question     string
	RefinedTopic string
	Missing      []string
	Summary      string
}

func extractClarifyDecision(reply string) (clarifyDecision, string) {
	reply = strings.TrimSpace(strings.ReplaceAll(reply, "\r\n", "\n"))
	if reply == "" {
		return clarifyDecision{}, ""
	}
	visible := reply
	decision := clarifyDecision{}

	matches := clarifyJSONCodeFencePattern.FindAllStringSubmatchIndex(reply, -1)
	for _, m := range matches {
		if len(m) < 4 {
			continue
		}
		rawJSON := strings.TrimSpace(reply[m[2]:m[3]])
		if rawJSON == "" {
			continue
		}
		if parsed, ok := parseClarifyDecisionJSON(rawJSON); ok {
			decision = parsed
			visible = strings.TrimSpace(reply[:m[0]] + "\n" + reply[m[1]:])
			break
		}
	}
	visible = strings.TrimSpace(visible)

	if strings.TrimSpace(decision.RefinedTopic) == "" {
		decision.RefinedTopic = extractSectionLine(visible, "已锁定议题")
	}
	if strings.TrimSpace(decision.Question) == "" {
		decision.Question = extractSectionLine(visible, "给用户的问题")
	}
	if strings.TrimSpace(decision.Summary) == "" {
		decision.Summary = extractSectionLine(visible, "澄清结论")
	}
	if strings.TrimSpace(decision.Question) == "" && decision.NeedMore {
		decision.Question = extractFirstQuestionLine(visible)
	}
	if !decision.NeedMore && strings.TrimSpace(decision.RefinedTopic) == "" {
		decision.RefinedTopic = extractFirstMeaningfulParagraph(visible)
	}
	return decision, visible
}

func parseClarifyDecisionJSON(raw string) (clarifyDecision, bool) {
	out := clarifyDecision{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out, false
	}
	typ := strings.ToLower(strings.TrimSpace(asString(m["type"])))
	if typ != "" && typ != "clarify_decision" {
		return out, false
	}
	out.NeedMore = asBool(m["need_more"])
	out.Question = strings.TrimSpace(asString(m["question"]))
	out.RefinedTopic = strings.TrimSpace(asString(m["refined_topic"]))
	out.Summary = strings.TrimSpace(asString(m["summary"]))
	out.Missing = asStringSlice(m["missing"])
	return out, true
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "yes" || s == "1"
	case float64:
		return t != 0
	default:
		return false
	}
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			val := strings.TrimSpace(asString(item))
			if val == "" {
				continue
			}
			out = append(out, val)
		}
		return out
	case []string:
		out := make([]string, 0, len(t))
		for _, item := range t {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func extractSectionLine(content, section string) string {
	if strings.TrimSpace(content) == "" || strings.TrimSpace(section) == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, section) {
			line = strings.TrimSpace(strings.TrimPrefix(line, "【"+section+"】"))
			line = strings.TrimSpace(strings.TrimPrefix(line, section+"："))
			line = strings.TrimSpace(strings.TrimPrefix(line, section+":"))
			line = strings.TrimLeft(line, "-：: ")
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func extractFirstQuestionLine(content string) string {
	lines := strings.Split(content, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, "？") || strings.Contains(line, "?") {
			return strings.TrimSpace(strings.TrimLeft(line, "-•*0123456789.、) ）"))
		}
	}
	return ""
}

func extractFirstMeaningfulParagraph(content string) string {
	lines := strings.Split(content, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "-•*0123456789.、) ）"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "a)") || strings.HasPrefix(strings.ToLower(line), "b)") {
			continue
		}
		return line
	}
	return ""
}
