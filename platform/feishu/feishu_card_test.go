package feishu

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildDebateControlCardJSON_CompatibleText(t *testing.T) {
	cardJSON := buildDebateControlCardJSON()
	if !json.Valid([]byte(cardJSON)) {
		t.Fatalf("debate control card is not valid json: %s", cardJSON)
	}

	lower := strings.ToLower(cardJSON)
	if strings.Contains(cardJSON, "`") {
		t.Fatalf("card json should not contain markdown backticks, got: %s", cardJSON)
	}
	if strings.Contains(lower, "<room_id>") {
		t.Fatalf("card json should not contain angle-bracket placeholder <room_id>, got: %s", cardJSON)
	}
}

func TestBuildSquadControlCardJSON_CompatibleText(t *testing.T) {
	cardJSON := buildSquadControlCardJSON("")
	if !json.Valid([]byte(cardJSON)) {
		t.Fatalf("squad control card is not valid json: %s", cardJSON)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if root["schema"] != "2.0" {
		t.Fatalf("expected schema 2.0 squad card, got %#v", root["schema"])
	}
	lower := strings.ToLower(cardJSON)
	if strings.Contains(cardJSON, "`") {
		t.Fatalf("card json should not contain markdown backticks, got: %s", cardJSON)
	}
	if strings.Contains(lower, "<run_id>") {
		t.Fatalf("card json should not contain angle-bracket placeholder <run_id>, got: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, "squad_main_form") {
		t.Fatalf("expected form name squad_main_form in card json")
	}
}

func TestBuildSquadControlCardJSON_DefaultRunID(t *testing.T) {
	cardJSON := buildSquadControlCardJSON("squad_20260322_000001_000042")
	if !strings.Contains(cardJSON, "squad_20260322_000001_000042") {
		t.Fatalf("default run id not embedded: %s", cardJSON)
	}
}

func TestSquadFormField_Nested(t *testing.T) {
	form := map[string]interface{}{
		"squad_main_form": map[string]interface{}{
			"run_id": "squad_x",
		},
	}
	if got := squadFormField(form, "run_id"); got != "squad_x" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildSquadCommandByAction_JudgeRework(t *testing.T) {
	cmd, toast := buildSquadCommandByAction("judge_rework", "squad_1", "问题：测试不足；方案：补充单测")
	if !strings.Contains(cmd, "/squad judge-review squad_1 rework") {
		t.Fatalf("unexpected command: %s", cmd)
	}
	if strings.TrimSpace(toast) == "" {
		t.Fatalf("toast should not be empty")
	}
}

func TestBuildSquadCommandByAction_JudgeReworkRequireNote(t *testing.T) {
	cmd, toast := buildSquadCommandByAction("judge_rework", "squad_1", "")
	if !strings.Contains(cmd, "请先填写不通过原因与修改方案") {
		t.Fatalf("should return guidance when rework note is missing, got: %s", cmd)
	}
	if !strings.Contains(toast, "返工信息为空") {
		t.Fatalf("unexpected toast: %s", toast)
	}
}

func TestBuildSquadTemplateByAction_UsesRunIDPlaceholder(t *testing.T) {
	cmd, _ := buildSquadTemplateByAction("approve_task", "")
	if !strings.Contains(cmd, "RUN_ID") {
		t.Fatalf("template should include RUN_ID placeholder when run_id is empty, got: %s", cmd)
	}
}

func TestBuildSquadCommandByAction_SkipTask(t *testing.T) {
	cmd, toast := buildSquadCommandByAction("skip_task", "squad_2", "先跳过")
	if !strings.Contains(cmd, "/squad skip-task squad_2") {
		t.Fatalf("unexpected skip command: %s", cmd)
	}
	if strings.TrimSpace(toast) == "" {
		t.Fatalf("toast should not be empty")
	}
}
