package agent

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbockelm/swamp/internal/models"
)

// sarifDocument is a minimal SARIF 2.1.0 structure for parsing results.
type sarifDocument struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	Level   string `json:"level"`
	RuleID  string `json:"ruleId"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			StartLine int `json:"startLine"`
			EndLine   int `json:"endLine"`
			Snippet   struct {
				Text string `json:"text"`
			} `json:"snippet"`
		} `json:"region"`
	} `json:"physicalLocation"`
}

// ParseOutput holds parsed output plus extracted individual findings.
type ParseOutput struct {
	Results   []models.AnalysisResult
	Findings  []models.Finding
	GitCommit string // resolved commit SHA from git_sha.txt
}

// ParseOutputDir scans the output directory for analysis artifacts and
// builds AnalysisResult records for each one. Also extracts individual
// SARIF findings for the findings table.
func ParseOutputDir(outputDir, analysisID, projectID string) (*ParseOutput, error) {
	out := &ParseOutput{}

	// Read the resolved git commit SHA if the agent recorded it.
	if shaBytes, err := os.ReadFile(filepath.Join(outputDir, "git_sha.txt")); err == nil {
		sha := strings.TrimSpace(string(shaBytes))
		// Validate it looks like a hex SHA (7-40 chars).
		if len(sha) >= 7 && len(sha) <= 40 {
			valid := true
			for _, c := range sha {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
					valid = false
					break
				}
			}
			if valid {
				out.GitCommit = sha
			}
		}
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, fmt.Errorf("read output dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			if entry.Name() == "exploits" {
				tarName := "exploits.tar.gz"
				tarPath := filepath.Join(outputDir, tarName)
				if err := createTarGz(tarPath, filepath.Join(outputDir, "exploits")); err != nil {
					// Log but don't fail the whole parse.
					continue
				}
				fi, err := os.Stat(tarPath)
				if err != nil {
					continue
				}
				out.Results = append(out.Results, models.AnalysisResult{
					AnalysisID:     analysisID,
					Filename:       tarName,
					FileSize:       fi.Size(),
					ResultType:     "exploit_tarball",
					ContentType:    "application/gzip",
					Summary:        "Exploit proof-of-concept artifacts",
					SeverityCounts: json.RawMessage(`{}`),
				})
			}
			continue
		}

		name := entry.Name()
		fullPath := filepath.Join(outputDir, name)

		// Skip the git SHA file — it's metadata, not a result artifact.
		if name == "git_sha.txt" {
			continue
		}

		fi, err := entry.Info()
		if err != nil {
			continue
		}

		var result models.AnalysisResult
		result.AnalysisID = analysisID
		result.Filename = name
		result.FileSize = fi.Size()

		switch {
		case strings.HasSuffix(name, ".sarif"):
			result.ResultType = "sarif"
			result.ContentType = "application/json"
			result.Summary, result.FindingCount, result.SeverityCounts = parseSARIF(fullPath)
			// Extract individual findings for the findings table.
			findings := extractFindings(fullPath, analysisID, projectID)
			out.Findings = append(out.Findings, findings...)

		case name == "notes.md":
			result.ResultType = "analysis_notes"
			result.ContentType = "text/markdown"
			result.Summary = "Analyst notes for future runs"

		case name == "prompt.md":
			result.ResultType = "analysis_prompt"
			result.ContentType = "text/markdown"
			result.Summary = "Prompt sent to the analysis agent"

		case name == "context.md":
			result.ResultType = "analysis_context"
			result.ContentType = "text/markdown"
			result.Summary = "Prior findings and notes provided as context"

		case strings.HasSuffix(name, ".md"):
			result.ResultType = "markdown_report"
			result.ContentType = "text/markdown"
			result.Summary = "Markdown analysis report"

		case strings.HasSuffix(name, ".log"):
			result.ResultType = "agent_log"
			result.ContentType = "text/plain"
			result.Summary = "Agent execution log"

		case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"):
			result.ResultType = "exploit_tarball"
			result.ContentType = "application/gzip"
			result.Summary = "Exploit proof-of-concept artifacts"

		default:
			result.ResultType = "other"
			result.ContentType = "application/octet-stream"
			result.Summary = "Analysis artifact"
		}

		// Ensure severity_counts is never nil (DB column is NOT NULL jsonb).
		if result.SeverityCounts == nil {
			result.SeverityCounts = json.RawMessage(`{}`)
		}

		out.Results = append(out.Results, result)
	}

	return out, nil
}

// parseSARIF reads a SARIF file and extracts finding counts and severity breakdown.
func parseSARIF(path string) (summary string, findingCount int, severityCounts json.RawMessage) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "Failed to read SARIF file", 0, nil
	}
	return ParseSARIFBytes(data)
}

// ParseSARIFBytes parses SARIF data from bytes and returns finding counts.
// This is exported for use by the worker result upload handler.
func ParseSARIFBytes(data []byte) (summary string, findingCount int, severityCounts json.RawMessage) {
	var doc sarifDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return "Failed to parse SARIF file", 0, nil
	}

	counts := map[string]int{
		"critical": 0,
		"high":     0,
		"medium":   0,
		"low":      0,
		"info":     0,
	}

	total := 0
	for _, run := range doc.Runs {
		for _, result := range run.Results {
			total++
			switch strings.ToLower(result.Level) {
			case "error":
				counts["high"]++
			case "warning":
				counts["medium"]++
			case "note":
				counts["low"]++
			default:
				counts["info"]++
			}
		}
	}

	countsJSON, _ := json.Marshal(counts)
	summary = fmt.Sprintf("Found %d findings: %d high, %d medium, %d low",
		total, counts["high"], counts["medium"], counts["low"])
	return summary, total, json.RawMessage(countsJSON)
}

// extractFindings parses a SARIF file and returns individual Finding records.
func extractFindings(path, analysisID, projectID string) []models.Finding {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ExtractFindingsFromBytes(data, analysisID, projectID)
}

// ExtractFindingsFromBytes parses SARIF data from bytes and returns Finding records.
// This is exported for use by the worker result upload handler.
func ExtractFindingsFromBytes(data []byte, analysisID, projectID string) []models.Finding {
	var doc sarifDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}

	var findings []models.Finding
	for _, run := range doc.Runs {
		for _, res := range run.Results {
			rawJSON, _ := json.Marshal(res)

			var filePath string
			var startLine, endLine int
			var snippet string
			if len(res.Locations) > 0 {
				loc := res.Locations[0].PhysicalLocation
				filePath = loc.ArtifactLocation.URI
				startLine = loc.Region.StartLine
				endLine = loc.Region.EndLine
				snippet = loc.Region.Snippet.Text
			}

			// Build a stable fingerprint from rule + file + line.
			fingerprint := ""
			if fp, ok := res.PartialFingerprints["primaryLocationLineHash"]; ok {
				fingerprint = fp
			}
			if fingerprint == "" {
				fingerprint = fmt.Sprintf("%s:%s:%d", res.RuleID, filePath, startLine)
			}

			findings = append(findings, models.Finding{
				ProjectID:   projectID,
				AnalysisID:  analysisID,
				RuleID:      res.RuleID,
				Level:       res.Level,
				Message:     res.Message.Text,
				FilePath:    filePath,
				StartLine:   startLine,
				EndLine:     endLine,
				Snippet:     snippet,
				Fingerprint: fingerprint,
				RawJSON:     json.RawMessage(rawJSON),
			})
		}
	}
	return findings
}

// createTarGz creates a gzipped tar archive of the given directory.
func createTarGz(outPath, srcDir string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create tarball: %w", err)
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	defer func() { _ = gw.Close() }()

	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Build a relative path rooted at "exploits/".
		rel, err := filepath.Rel(filepath.Dir(srcDir), path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		_, err = io.Copy(tw, file)
		return err
	})
}
