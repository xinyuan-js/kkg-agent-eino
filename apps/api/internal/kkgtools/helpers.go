package kkgtools

import (
	"fmt"
	"strings"
)

func clampInt(value, fallback, maxValue int) int {
	if value <= 0 {
		value = fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampInt64(value, fallback, maxValue int64) int64 {
	if value <= 0 {
		value = fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampInt64Min(value, minValue int64) int64 {
	if value < minValue {
		return minValue
	}
	return value
}

func submitSummary(out any) string {
	record, ok := out.(map[string]any)
	if !ok {
		return "submitted"
	}
	id := compactToolValue(record["submission_id"])
	if id == "" {
		id = compactToolValue(record["id"])
	}
	status := compactToolValue(record["status_label"])
	if id == "" && status == "" {
		return "submitted"
	}
	if id == "" {
		return "submitted " + status
	}
	if status == "" {
		return "submission " + id
	}
	return "submission " + id + " " + status
}

func submissionResultSummary(submissionID int64) func(map[string]any) string {
	return func(out map[string]any) string {
		id := ""
		if submissionID > 0 {
			id = fmt.Sprintf("%d", submissionID)
		}
		if out != nil {
			if value := compactToolValue(out["id"]); value != "" {
				id = value
			}
			if value := compactToolValue(out["submission_id"]); value != "" {
				id = value
			}
			if status := compactToolValue(out["status_label"]); status != "" {
				if id == "" {
					return "latest submission " + status
				}
				return "submission " + id + " " + status
			}
		}
		if id == "" {
			return "latest submission"
		}
		return "submission " + id
	}
}

func compactToolValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func countLines(code string) int {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0
	}
	return strings.Count(code, "\n") + 1
}
