package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type rawFile struct {
	Regions []rawEntry `json:"regions"`
}

type rawEntry struct {
	StatusCode int             `json:"statusCode"`
	Body       json.RawMessage `json:"body"`
}

type sdkFile struct {
	Regions []sdkEntry `json:"regions"`
}

type sdkEntry struct {
	Count int             `json:"count"`
	Items json.RawMessage `json:"items"`
	Error string          `json:"error"`
}

type resourceReport struct {
	resource      string
	rawSuccess    int
	rawRows       int
	sdkRows       int
	sdkErrors     int
	missingKeys   []string
	rawWithoutSDK int
	sdkWithoutRaw bool
	unreadable    string
	noComparable  bool
}

var envelopeKeys = map[string]bool{
	"data": true, "items": true, "listData": true, "content": true, "results": true,
	"projects": true, "regions": true, "images": true, "volumeTypes": true, "encryptionTypes": true,
	"page": true, "pageSize": true, "size": true,
	"total": true, "totalItem": true, "totalItems": true, "totalPage": true, "totalPages": true,
	"success": true, "status": true, "code": true, "message": true, "errorCode": true, "errorMsg": true, "extra": true,
}

var payloadKeys = []string{"listData", "data", "items", "content", "results", "projects", "regions", "images", "volumeTypes", "encryptionTypes"}

func checkSmokeOutput(root string) ([]resourceReport, error) {
	rawRoot := filepath.Join(root, "raw")
	sdkRoot := filepath.Join(root, "sdk")
	rawPaths, err := filepath.Glob(filepath.Join(rawRoot, "**", "*.json"))
	if err != nil {
		return nil, err
	}
	if len(rawPaths) == 0 {
		return nil, fmt.Errorf("no raw output found under %s", rawRoot)
	}

	reports := make([]resourceReport, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		rel, err := filepath.Rel(rawRoot, rawPath)
		if err != nil {
			return nil, err
		}
		report := resourceReport{resource: strings.TrimSuffix(filepath.ToSlash(rel), ".json")}
		sdkPath := filepath.Join(sdkRoot, rel)
		if _, err := os.Stat(sdkPath); err != nil {
			report.rawWithoutSDK = 1
			reports = append(reports, report)
			continue
		}
		if err := compareResource(rawPath, sdkPath, &report); err != nil {
			report.unreadable = err.Error()
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].resource < reports[j].resource })
	return reports, nil
}

func printSmokeCheck(reports []resourceReport) bool {
	failed := false
	for _, report := range reports {
		if report.unreadable != "" {
			failed = true
			fmt.Printf("%s: unreadable: %s\n", report.resource, report.unreadable)
			continue
		}
		if len(report.missingKeys) > 0 || report.rawWithoutSDK > 0 || report.sdkWithoutRaw {
			failed = true
			fmt.Printf("%s: raw_ok=%d raw_rows=%d sdk_rows=%d sdk_errors=%d missing_keys=%v raw_without_sdk=%d sdk_without_raw=%t\n",
				report.resource, report.rawSuccess, report.rawRows, report.sdkRows, report.sdkErrors, report.missingKeys, report.rawWithoutSDK, report.sdkWithoutRaw)
		}
	}
	if failed {
		return false
	}
	fmt.Printf("smokecheck: ok (%d resources)\n", len(reports))
	return true
}

func compareResource(rawPath, sdkPath string, report *resourceReport) error {
	var raw rawFile
	if err := readJSON(rawPath, &raw); err != nil {
		return err
	}
	var sdk sdkFile
	if err := readJSON(sdkPath, &sdk); err != nil {
		return err
	}

	rawKeys := map[string]bool{}
	sdkKeys := map[string]bool{}
	for _, entry := range raw.Regions {
		if entry.StatusCode < 200 || entry.StatusCode >= 300 || len(entry.Body) == 0 {
			continue
		}
		report.rawSuccess++
		payloads := payloadsFromBody(entry.Body)
		report.rawRows += len(payloads)
		for _, payload := range payloads {
			collectKeys(payload, rawKeys)
		}
	}
	for _, entry := range sdk.Regions {
		if entry.Error != "" {
			report.sdkErrors++
			continue
		}
		report.sdkRows += entry.Count
		payloads := payloadsFromRaw(entry.Items)
		for _, payload := range payloads {
			collectKeys(payload, sdkKeys)
		}
	}

	report.noComparable = len(rawKeys) == 0 || len(sdkKeys) == 0
	if len(rawKeys) == 0 || len(sdkKeys) == 0 {
		return nil
	}
	for key := range rawKeys {
		if !sdkKeys[key] {
			report.missingKeys = append(report.missingKeys, key)
		}
	}
	sort.Strings(report.missingKeys)
	return nil
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(out)
}

func payloadsFromBody(body json.RawMessage) []any {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return payloadsFromValue(value)
}

func payloadsFromRaw(raw json.RawMessage) []any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return payloadsFromValue(value)
}

func payloadsFromValue(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case map[string]any:
		for _, key := range payloadKeys {
			if child, ok := typed[key]; ok {
				return payloadsFromValue(child)
			}
		}
		if onlyEnvelopeKeys(typed) {
			return nil
		}
		return []any{typed}
	default:
		return nil
	}
}

func onlyEnvelopeKeys(value map[string]any) bool {
	if len(value) == 0 {
		return true
	}
	for key := range value {
		if !envelopeKeys[key] {
			return false
		}
	}
	return true
}

func collectKeys(value any, keys map[string]bool) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectKeys(item, keys)
		}
	case map[string]any:
		for key, child := range typed {
			if !envelopeKeys[key] {
				keys[key] = true
			}
			collectKeys(child, keys)
		}
	}
}
