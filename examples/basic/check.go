package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type rawFile struct {
	Regions []rawEntry `json:"regions"`
}

type rawEntry struct {
	Config     string          `json:"config"`
	Region     string          `json:"region"`
	StatusCode int             `json:"statusCode"`
	Body       json.RawMessage `json:"body"`
}

type sdkFile struct {
	Regions []sdkEntry `json:"regions"`
}

type sdkEntry struct {
	Config string          `json:"config"`
	Region string          `json:"region"`
	Scope  string          `json:"scope"`
	Count  int             `json:"count"`
	Items  json.RawMessage `json:"items"`
	Error  string          `json:"error"`
}

type resourceReport struct {
	resource          string
	rawSuccess        int
	rawRows           int
	sdkRows           int
	sdkErrors         int
	missingKeys       []string
	rawWithoutSDK     int
	sdkWithoutRaw     bool
	unreadable        string
	noComparable      bool
	comparisonSkipped string
}

type captureScope struct {
	config string
	region string
}

type payloadCoverage struct {
	rows int
	keys map[string]int
}

var envelopeKeys = map[string]bool{
	"data": true, "datas": true, "items": true, "listData": true, "lstData": true, "content": true, "results": true,
	"projects": true, "regions": true, "images": true, "volumeTypes": true, "encryptionTypes": true,
	"page": true, "pageSize": true, "size": true,
	"total": true, "totalItem": true, "totalItems": true, "totalPage": true, "totalPages": true,
	"success": true, "status": true, "code": true, "message": true, "errorCode": true, "errorMsg": true, "extra": true,
}

var payloadKeys = []string{"listData", "lstData", "data", "datas", "items", "Items", "content", "results", "projects", "regions", "images", "volumeTypes", "encryptionTypes"}

func checkSmokeOutput(root string) ([]resourceReport, error) {
	rawRoot := filepath.Join(root, "raw")
	sdkRoot := filepath.Join(root, "sdk")
	rawPaths, err := smokeArtifactPaths(rawRoot)
	if err != nil {
		return nil, err
	}
	sdkPaths, err := smokeArtifactPaths(sdkRoot)
	if err != nil {
		return nil, err
	}
	if len(rawPaths) == 0 && len(sdkPaths) == 0 {
		return nil, fmt.Errorf("no output found under %s", root)
	}

	reports := make([]resourceReport, 0, len(rawPaths)+len(sdkPaths))
	matched := make(map[string]bool, len(rawPaths))
	for _, rawPath := range rawPaths {
		rel, err := filepath.Rel(rawRoot, rawPath)
		if err != nil {
			return nil, err
		}
		matched[rel] = true
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
	for _, sdkPath := range sdkPaths {
		rel, err := filepath.Rel(sdkRoot, sdkPath)
		if err != nil {
			return nil, err
		}
		if !matched[rel] {
			report := resourceReport{
				resource:      strings.TrimSuffix(filepath.ToSlash(rel), ".json"),
				sdkWithoutRaw: true,
			}
			if reason := uncapturedOutputReason(report.resource); reason != "" {
				report.comparisonSkipped = reason
				if err := inspectUncapturedSDK(sdkPath, &report); err != nil {
					report.unreadable = err.Error()
				}
			}
			reports = append(reports, report)
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].resource < reports[j].resource })
	return reports, nil
}

func smokeArtifactPaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if path == root && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() && filepath.Ext(path) == ".json" {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func inspectUncapturedSDK(path string, report *resourceReport) error {
	var sdk sdkFile
	if err := readJSON(path, &sdk); err != nil {
		return err
	}
	report.noComparable = len(sdk.Regions) == 0
	for _, entry := range sdk.Regions {
		if entry.Error != "" {
			report.sdkErrors++
			continue
		}
		var items any
		if len(entry.Items) > 0 {
			if err := json.Unmarshal(entry.Items, &items); err != nil {
				return errors.New("invalid SDK items")
			}
		}
		count := countItems(items)
		if count != entry.Count {
			return errors.New("SDK count does not match its items")
		}
		report.sdkRows += count
	}
	return nil
}

func printSmokeCheck(reports []resourceReport) bool {
	failed := false
	skipped := 0
	for _, report := range reports {
		if report.unreadable != "" {
			failed = true
			fmt.Printf("%s: unreadable: %s\n", report.resource, report.unreadable)
			continue
		}
		if report.comparisonSkipped != "" && report.sdkErrors == 0 && !report.noComparable {
			skipped++
			fmt.Printf("%s: sdk_without_raw=true comparison=skipped (%s)\n", report.resource, report.comparisonSkipped)
			continue
		}
		if report.sdkErrors > 0 || report.noComparable || report.rawRows != report.sdkRows ||
			len(report.missingKeys) > 0 || report.rawWithoutSDK > 0 || report.sdkWithoutRaw {
			failed = true
			fmt.Printf("%s: raw_ok=%d raw_rows=%d sdk_rows=%d sdk_errors=%d missing_keys=%v raw_without_sdk=%d sdk_without_raw=%t no_comparable=%t\n",
				report.resource, report.rawSuccess, report.rawRows, report.sdkRows, report.sdkErrors, report.missingKeys, report.rawWithoutSDK, report.sdkWithoutRaw, report.noComparable)
		}
	}
	if failed {
		return false
	}
	fmt.Printf("smokecheck: ok (%d compared, %d SDK-only)\n", len(reports)-skipped, skipped)
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

	accountConfigs := make(map[string]bool)
	for _, entry := range sdk.Regions {
		if entry.Scope == "account" {
			accountConfigs[entry.Config] = true
		}
	}
	rawCoverage, err := rawPayloadCoverage(raw, accountConfigs, report)
	if err != nil {
		return err
	}
	sdkCoverage, err := sdkPayloadCoverage(sdk, report)
	if err != nil {
		return err
	}

	report.noComparable = report.rawSuccess == 0 || len(sdk.Regions) == 0
	missing := make(map[string]bool)
	for scope, rawPayload := range rawCoverage {
		sdkPayload := sdkCoverage[scope]
		if sdkPayload == nil {
			report.rawWithoutSDK++
			continue
		}
		if rawPayload.rows != sdkPayload.rows || (rawPayload.rows > 0 && len(rawPayload.keys) == 0) {
			report.noComparable = true
		}
		for key, count := range rawPayload.keys {
			if sdkPayload.keys[key] < count {
				missing[key] = true
			}
		}
	}
	for scope := range sdkCoverage {
		if rawCoverage[scope] == nil {
			report.sdkWithoutRaw = true
		}
	}
	for key := range missing {
		report.missingKeys = append(report.missingKeys, key)
	}
	sort.Strings(report.missingKeys)
	return nil
}

func rawPayloadCoverage(raw rawFile, accountConfigs map[string]bool, report *resourceReport) (map[captureScope]*payloadCoverage, error) {
	coverage := make(map[captureScope]*payloadCoverage)
	for _, entry := range raw.Regions {
		if entry.StatusCode < 200 || entry.StatusCode >= 300 {
			continue
		}
		report.rawSuccess++
		payloads, err := payloadsFromBody(entry.Body)
		if err != nil {
			return nil, err
		}
		report.rawRows += len(payloads)
		scope := captureScope{config: entry.Config, region: entry.Region}
		if accountConfigs[entry.Config] {
			scope.region = ""
		}
		addPayloadCoverage(coverage, scope, payloads)
	}
	return coverage, nil
}

func sdkPayloadCoverage(sdk sdkFile, report *resourceReport) (map[captureScope]*payloadCoverage, error) {
	coverage := make(map[captureScope]*payloadCoverage)
	for _, entry := range sdk.Regions {
		if entry.Error != "" {
			report.sdkErrors++
			continue
		}
		payloads, count, err := payloadsFromRaw(entry.Items, report.resource)
		if err != nil {
			return nil, err
		}
		if count != entry.Count {
			return nil, errors.New("SDK count does not match its items")
		}
		report.sdkRows += len(payloads)
		scope := captureScope{config: entry.Config, region: entry.Region}
		if entry.Scope == "account" {
			scope.region = ""
		}
		addPayloadCoverage(coverage, scope, payloads)
	}
	return coverage, nil
}

func addPayloadCoverage(coverage map[captureScope]*payloadCoverage, scope captureScope, payloads []any) {
	if coverage[scope] == nil {
		coverage[scope] = &payloadCoverage{keys: make(map[string]int)}
	}
	entry := coverage[scope]
	entry.rows += len(payloads)
	for _, payload := range payloads {
		collectKeys(payload, "", entry.keys)
	}
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("artifact must contain one JSON value")
	}
	return nil
}

func payloadsFromBody(body json.RawMessage) ([]any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("invalid captured response body")
	}
	payloads := payloadsFromValue(value)
	if payloads == nil {
		return nil, errors.New("captured response has no comparable payload")
	}
	return payloads, nil
}

func payloadsFromRaw(raw json.RawMessage, resource string) ([]any, int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, 0, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, 0, errors.New("invalid SDK items")
	}
	payloads, err := sdkPayloadsFromValue(resource, value)
	if err != nil {
		return nil, 0, err
	}
	if payloads == nil {
		return nil, 0, errors.New("SDK items have no comparable payload")
	}
	return payloads, countItems(value), nil
}

func sdkPayloadsFromValue(resource string, value any) ([]any, error) {
	var field string
	switch resource {
	case "monitor/check_detail":
		field = "Check"
	case "monitor/channel_detail":
		field = "Channel"
	case "billing/current_period_cost":
		field = "PeriodCost"
	case "glb/usage_history":
		field = "Items"
	default:
		return payloadsFromValue(value), nil
	}
	if values, ok := value.([]any); ok {
		payloads := []any{}
		for _, item := range values {
			children, err := sdkPayloadsFromValue(resource, item)
			if err != nil {
				return nil, err
			}
			payloads = append(payloads, children...)
		}
		return payloads, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid SDK output wrapper")
	}
	child, ok := object[field]
	if !ok || child == nil {
		return nil, errors.New("missing SDK output payload")
	}
	return payloadsFromValue(child), nil
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

func collectKeys(value any, prefix string, keys map[string]int) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectKeys(item, prefix, keys)
		}
	case map[string]any:
		for key, child := range typed {
			path := prefix + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			keys[path]++
			collectKeys(child, path, keys)
		}
	}
}
