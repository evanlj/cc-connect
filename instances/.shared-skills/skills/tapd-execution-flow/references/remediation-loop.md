# FAIL Remediation Loop

## Trigger
Enter remediation loop when acceptance result is FAIL.

## Mandatory Actions
1. Keep TAPD story status at `status_4`.
2. Backfill failed items to TAPD:
   - mandatory failures
   - suggestion-level improvements
3. Re-confirm models (main and acceptance, separately).
4. Regenerate prompts with explicit “fix previous failures” focus.
5. Ask user confirmation for both prompts.
6. Backfill regenerated prompts to TAPD.
7. Execute fix task.
8. Execute re-acceptance with itemized evidence.

## Exit Condition
- Only exit loop when acceptance is PASS.
- Then update status to `status_6`.

## Round Trace (every remediation round)
- `jobId`
- log path
- TAPD comment IDs
- failure summary / fix summary
- status transition result
