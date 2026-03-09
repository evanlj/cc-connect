# TAPD Comment Templates (HTML)

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
