# TAPD Run Manifest（执行留痕）契约草案

目的：每次 TAPD 执行都生成一个可机器读取的摘要，支撑：
- 复盘（发生了什么）
- 审计（证据在哪里）
- 自动 closeout gate（缺什么一眼能看出来）
- 信任光谱右移（L1→L2→L3 的基础）

建议文件名：
- `tapd-run-manifest.json`

建议路径（示例）：
- `instances/<instance>/output/tapd/<entity_id>/<yyyyMMdd-HHmmss>/tapd-run-manifest.json`

---

## v0 字段建议（JSON）

```json
{
  "run_id": "2026-03-11_152900",
  "workspace_id": 66052431,
  "entity_type": "story",
  "entity_id": "1166052431001000075",
  "status_before": "status_4",
  "status_after": "status_6",
  "models": {
    "plan": "5.2",
    "main": "5.2",
    "acceptance": "5.3-codex"
  },
  "tapd_comment_ids": {
    "plan_backfill": "1166...",
    "prompt_backfill": "1166...",
    "main_result": "1166...",
    "acceptance_detail": "1166...",
    "round_trace": "1166..."
  },
  "plan_doc_path": "repo/.tapd/plan/story-1166...-plan-v1.md",
  "tcase": {
    "tcase_id": "1166...",
    "relation_id": "1166..."
  },
  "evidence": [
    {
      "type": "log",
      "path": "instances/instance-c/output/tapd/1166.../....log"
    },
    {
      "type": "report",
      "path": "instances/instance-c/output/tapd/1166.../acceptance-report.json"
    }
  ],
  "process_verification": {
    "cc_connect_restarted": false,
    "notes": "默认不重启进程"
  }
}
```

---

## v0 约束（强制建议）

- 所有路径尽量用相对路径（相对仓库根目录）
- `tapd_comment_ids` / `tcase_id` / `relation_id` 必须能在 TAPD 上定位
