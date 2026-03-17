package core

import (
	"encoding/json"
	"regexp"
	"strings"
)

var sectionHeaderPattern = regexp.MustCompile(`^\s*(?:\d+\s*[)\.、]?\s*)?【\s*([^】]+)\s*】\s*(.*)$`)
var writebackCodeFencePattern = regexp.MustCompile("(?is)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")

type blackboardWritebackPayload struct {
	Type         string `json:"type"`
	RoomID       string `json:"room_id"`
	Role         string `json:"role"`
	Round        int    `json:"round"`
	BaseRevision int    `json:"base_revision"`
	Stance       string `json:"stance"`
	Basis        string `json:"basis"`
	Risk         string `json:"risk"`
	Action       string `json:"action"`
}

func ExtractRoleContribution(reply string) RoleContribution {
	reply = strings.ReplaceAll(reply, "\r\n", "\n")
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return RoleContribution{}
	}

	writeback, visible := extractWritebackPayload(reply)
	sections := splitStructuredSections(visible)
	c := RoleContribution{
		RoomID:       strings.TrimSpace(writeback.RoomID),
		Role:         strings.TrimSpace(writeback.Role),
		Round:        writeback.Round,
		BaseRevision: writeback.BaseRevision,
		Stance:       firstNonEmpty(strings.TrimSpace(writeback.Stance), strings.TrimSpace(sections["stance"])),
		Basis:        firstNonEmpty(strings.TrimSpace(writeback.Basis), strings.TrimSpace(sections["basis"])),
		Risk:         firstNonEmpty(strings.TrimSpace(writeback.Risk), strings.TrimSpace(sections["risk"])),
		Action:       firstNonEmpty(strings.TrimSpace(writeback.Action), strings.TrimSpace(sections["action"])),
		DisplayReply: strings.TrimSpace(visible),
	}

	if c.Stance == "" {
		c.Stance = firstMeaningfulLine(visible)
	}
	if c.Action == "" && len(c.Summary) == 0 {
		c.Action = firstMeaningfulLine(sections["summary"])
	}
	c.Summary = buildContributionSummary(c)
	return c
}

func extractWritebackPayload(reply string) (blackboardWritebackPayload, string) {
	var payload blackboardWritebackPayload
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return payload, ""
	}

	matches := writebackCodeFencePattern.FindAllStringSubmatchIndex(reply, -1)
	for _, m := range matches {
		// m: [fullStart, fullEnd, group1Start, group1End]
		if len(m) < 4 {
			continue
		}
		fullStart, fullEnd := m[0], m[1]
		rawJSON := strings.TrimSpace(reply[m[2]:m[3]])
		if rawJSON == "" {
			continue
		}
		var p blackboardWritebackPayload
		if err := json.Unmarshal([]byte(rawJSON), &p); err != nil {
			continue
		}
		if !looksLikeWritebackPayload(p) {
			continue
		}
		visible := strings.TrimSpace(reply[:fullStart] + "\n" + reply[fullEnd:])
		return p, visible
	}
	return payload, reply
}

func looksLikeWritebackPayload(p blackboardWritebackPayload) bool {
	typ := strings.ToLower(strings.TrimSpace(p.Type))
	if typ != "" && typ != "blackboard_writeback" && typ != "discussion_writeback" {
		return false
	}
	if strings.TrimSpace(p.Role) != "" || strings.TrimSpace(p.RoomID) != "" {
		return true
	}
	return strings.TrimSpace(p.Stance) != "" || strings.TrimSpace(p.Action) != ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func splitStructuredSections(reply string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(reply, "\n")
	curKey := ""

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		m := sectionHeaderPattern.FindStringSubmatch(line)
		if len(m) > 0 {
			curKey = normalizeSectionKey(m[1])
			if curKey == "" {
				curKey = ""
				continue
			}
			if tail := strings.TrimSpace(m[2]); tail != "" {
				out[curKey] = appendSection(out[curKey], tail)
			}
			continue
		}
		if curKey == "" {
			// No section yet: treat as stance prefix.
			out["stance"] = appendSection(out["stance"], line)
			continue
		}
		out[curKey] = appendSection(out[curKey], line)
	}
	return out
}

func normalizeSectionKey(raw string) string {
	key := strings.TrimSpace(raw)
	switch {
	case strings.Contains(key, "观点"):
		return "stance"
	case strings.Contains(key, "依据"):
		return "basis"
	case strings.Contains(key, "风险"):
		return "risk"
	case strings.Contains(key, "建议"), strings.Contains(key, "动作"), strings.Contains(key, "行动"):
		return "action"
	case strings.Contains(key, "摘要"), strings.Contains(key, "写回"):
		return "summary"
	default:
		return ""
	}
}

func appendSection(base, line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return strings.TrimSpace(base)
	}
	if strings.TrimSpace(base) == "" {
		return line
	}
	return strings.TrimSpace(base) + "\n" + line
}

func firstMeaningfulLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "-•*0123456789.、) ）")
		if line != "" {
			return line
		}
	}
	return ""
}

func buildContributionSummary(c RoleContribution) []string {
	out := make([]string, 0, 3)
	for _, s := range []string{c.Stance, c.Action, c.Risk} {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		s = firstMeaningfulLine(s)
		if s == "" {
			continue
		}
		out = append(out, truncateStr(s, 90))
		if len(out) >= 3 {
			break
		}
	}
	return out
}
