# TAPD HTML Templates

> 注意：TAPD 的“描述/评论”是富文本渲染，**会折叠纯文本换行（`\n`）**。  
> 凡是希望在页面“可见换行”的内容，必须使用 HTML（如 `<br/>`、`<p>`、`<ul><li>`、`<code>`）。
>
> **占位符约定（强制）**：
> - `*_html` 结尾的占位符：表示内容已做 **HTML escape**，并将换行 `\n` 转为 `<br/>` 的字符串。
> - 典型转换（伪代码）：`html.escape(text).replace("\n", "<br/>")`

## 0) Implementation Plan & DoD Backfill Template
```html
<p><strong>实现方案 &amp; DoD 回填（需求 {{story_id}}）</strong></p>
<p>回填时间：<code>{{time}}</code><br/>
方案模型：<code>{{plan_model}}</code><br/>
主任务模型：<code>{{main_model}}</code><br/>
验收任务模型：<code>{{accept_model}}</code></p>
<p>方案文档（Repo Path）：<code>{{plan_doc_path}}</code></p>
<p><strong>实现方案（v{{plan_version}}）</strong></p>
<p><code>{{plan_html}}</code></p>
<p><strong>DoD / 证据要求</strong></p>
<p><code>{{dod_html}}</code></p>
<p><strong>风险与回滚</strong></p>
<p><code>{{risk_html}}</code></p>
```

## 1) Prompt Backfill Template
```html
<p><strong>提示词回填（需求 {{story_id}}）</strong></p>
<p>回填时间：<code>{{time}}</code><br/>
方案模型：<code>{{plan_model}}</code><br/>
主任务模型：<code>{{main_model}}</code><br/>
验收任务模型：<code>{{accept_model}}</code></p>
<p><strong>主子任务提示词</strong></p>
<p><code>{{main_prompt_html}}</code></p>
<p><strong>验收子任务提示词</strong></p>
<p><code>{{accept_prompt_html}}</code></p>
```

## 2) Main Task Backfill Template
```html
<p><strong>主子任务执行回填</strong></p>
<p>需求ID：<code>{{story_id}}</code><br/>
执行时间：<code>{{time}}</code><br/>
主任务模型：<code>{{main_model}}</code><br/>
验收任务模型：<code>{{accept_model}}</code></p>
<p><strong>交付范围</strong></p>
<ul>
  <li>输出目录：<code>{{output_path}}</code></li>
  <li>关键产物：<code>{{key_outputs_html}}</code></li>
  <li>证据路径：<code>{{evidence_paths_html}}</code></li>
  <li>提交记录：<code>{{commit_sha}}</code></li>
</ul>
<p><strong>流程留痕</strong><br/>
<code>jobId={{job_id}}</code><br/>
<code>log={{log_path}}</code><br/>
<code>summary={{summary_html}}</code></p>
```

## 3) Acceptance Itemized Template
```html
<p><strong>验收子任务回填（逐条）</strong></p>
<p>验收模型：<code>{{accept_model}}</code><br/>
验收时间：<code>{{time}}</code><br/>
总结果：<strong>{{overall_result}}</strong></p>

<p><strong>{{idx}}. {{item_title}}</strong><br/>
验收内容：{{content_html}}<br/>
验收过程：{{process_html}}<br/>
验收结果：<strong>{{result}}</strong><br/>
证据：<code>{{evidence_html}}</code></p>

<p><strong>汇总</strong><br/>
PASS：<code>{{pass_count}}</code>；FAIL：<code>{{fail_count}}</code><br/>
状态建议：<code>{{status_suggestion}}</code></p>
```

## 4) Test Case Menu Backfill Template
```html
<p><strong>测试用例菜单回填</strong></p>
<ul>
  <li><code>{{tcase_id_1}}</code> - {{tcase_name_1}}</li>
  <li><code>{{tcase_id_2}}</code> - {{tcase_name_2}}</li>
  <li><code>{{tcase_id_3}}</code> - {{tcase_name_3}}</li>
</ul>
<p>关联关系：<code>{{relation_ids}}</code></p>
```
