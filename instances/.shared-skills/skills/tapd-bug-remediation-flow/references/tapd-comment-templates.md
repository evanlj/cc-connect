# TAPD Comment Templates (HTML)

> 重要：TAPD 富文本会折叠纯文本换行（`\n`）。  
> 模板中的 `*_html`（如 `{prompt_html}` / `{plan_html}`）必须是 **已做 HTML escape 且换行转 `<br  />`** 的字符串，例如：  
> `prompt_html = html.escape(prompt).replace("\n", "<br  />")`

## 0) Fix Plan & DoD Backfill (Mandatory)
```html
<p><strong>修复方案 &amp; DoD 确认并回填（vX）</strong></p>
<ul>
  <li>缺陷：<code>{bug_id}</code></li>
  <li>方案模型：<code>{plan_model}</code></li>
  <li>主任务模型：<code>{main_model}</code></li>
  <li>验收模型：<code>{accept_model}</code></li>
  <li>确认时间：<code>{time}</code></li>
</ul>
<p>方案文档（Repo Path）：<code>{plan_doc_path}</code></p>
<p><strong>修复方案</strong><br  />{plan_html}</p>
<p><strong>DoD / 证据要求</strong><br  />{dod_html}</p>
<p><strong>风险与回滚</strong><br  />{risk_html}</p>
```

## 1) Execution Prompt Backfill
```html
<p><strong>执行提示词确认并回填（vX）</strong></p>
<ul>
  <li>缺陷：<code>{bug_id}</code></li>
  <li>执行模型：<code>{model}</code></li>
  <li>确认时间：<code>{time}</code></li>
</ul>
<p><strong>执行提示词正文</strong><br  />{prompt_html}</p>
```

## 2) Acceptance Prompt Backfill
```html
<p><strong>验收提示词确认并回填（vX）</strong></p>
<ul>
  <li>缺陷：<code>{bug_id}</code></li>
  <li>验收模型：<code>{model}</code></li>
  <li>确认时间：<code>{time}</code></li>
</ul>
<p><strong>验收提示词正文</strong><br  />{prompt_html}</p>
```

## 3) FAIL Cause Backfill
```html
<p><strong>验收未通过原因（进入下一轮整改）</strong></p>
<ul>
  <li>缺陷：<code>{bug_id}</code></li>
  <li>结论：<code>FAIL</code></li>
</ul>
<p><strong>必须整改项</strong></p>
<ul>
  <li>{must_fix_1}</li>
  <li>{must_fix_2}</li>
</ul>
<p><strong>建议优化项</strong></p>
<ul>
  <li>{suggest_1}</li>
</ul>
```

## 4) Final PASS Backfill
```html
<p><strong>缺陷修复完成（PASS）</strong></p>
<ul>
  <li>缺陷：<code>{bug_id}</code></li>
  <li>提交：<code>{commit_id}</code></li>
  <li>日志：<code>{log_path}</code></li>
  <li>状态建议：<code>resolved</code></li>
</ul>
```
