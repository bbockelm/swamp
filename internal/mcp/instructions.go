package mcp

// instructions provides guidance to AI agents on how to effectively use the
// SWAMP MCP tools. This is returned in the MCP initialize response.
const instructions = `You are connected to SWAMP (Software Assurance Marketplace), an AI-powered security analysis platform. Use these tools to manage projects, run security analyses, and work with findings.

## Quick Start

1. Use list_projects to see available projects
2. Use list_packages with a project_id to see its Git repositories
3. Use list_analyses to see past security scans
4. Use list_findings or get_findings_summary to review security issues

## Typical Workflows

### Review latest analysis results
1. list_projects → find the project
2. list_analyses(project_id) → find the latest completed analysis
3. get_findings_summary(project_id) → get overview of open issues
4. list_findings(project_id, level="error") → see critical findings
5. get_finding(project_id, finding_id) → get full details + code snippet

### Start a new security analysis
1. list_packages(project_id) → get package IDs
2. start_analysis(project_id, package_ids) → trigger analysis
3. get_analysis(project_id, analysis_id) → poll until status is "completed"
4. list_findings(project_id, analysis_id=...) → review new findings

### Triage findings to create bugfixes
1. get_findings_summary(project_id) → see what needs attention
2. list_findings(project_id, status="open", level="error") → get critical open issues
3. get_finding(project_id, finding_id) → get the code snippet, file path, line numbers, and raw SARIF data
4. Use the file_path, start_line, end_line, snippet, and git_url to understand the vulnerable code
5. Create a fix based on the finding details
6. annotate_finding(project_id, finding_id, status="mitigated", note="Fixed in PR #123") → mark as addressed

### Read the full analysis report
1. list_results(project_id, analysis_id) → find the 'markdown' result
2. get_result_content(project_id, analysis_id, result_id) → read the full report

### Get raw SARIF data
1. list_results(project_id, analysis_id) → find the 'sarif' result
2. get_result_content(project_id, analysis_id, result_id) → get full SARIF JSON

## Understanding Findings

Each finding represents a potential security vulnerability discovered by the AI analysis agent.

Key fields:
- rule_id: The type of vulnerability (e.g., "security/sql-injection", "security/xss")
- level: Severity — "error" (high/critical), "warning" (medium), "note" (low)
- file_path: The source file containing the vulnerability
- start_line / end_line: Line numbers of the vulnerable code
- snippet: The relevant code excerpt
- message: Human-readable description of the issue
- git_url: The repository URL for context
- status: Triage state — "open", "confirmed", "false_positive", "not_relevant", "wont_fix", "mitigated"

## Tips

- Analysis runs asynchronously. After start_analysis, poll get_analysis every few seconds.
- Use get_findings_summary for a quick project health overview.
- Use list_findings with search="" to find findings mentioning specific patterns.
- get_result_content can return SARIF, markdown reports, or analysis notes.
- Findings include git_url and line numbers — use these to locate and fix the code.
- The snippet field contains the vulnerable code excerpt for quick context.
`
