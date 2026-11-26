package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"

	"danny.vn/vngcloud/pricing"
)

func estimateInputError(path string) error {
	return newUsageError("--cli-input-json: invalid estimate field at %s", path)
}

func applyEstimateInputJSON(raw string, target *pricing.EstimateInput) error {
	data, err := estimateInputBytes(raw)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := checkEstimateJSON(dec, reflect.TypeFor[pricing.EstimateInput](), "setup", 0); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return estimateInputError("setup")
	}
	var input pricing.EstimateInput
	if err := json.Unmarshal(data, &input); err != nil {
		return estimateInputError("setup")
	}
	if err := validateEstimateInput(&input); err != nil {
		return err
	}
	*target = input
	return nil
}

func estimateInputBytes(raw string) ([]byte, error) {
	path, file := strings.CutPrefix(raw, "file://")
	if !file {
		if len(raw) > maxCLIInputJSONSize {
			return nil, newUsageError("--cli-input-json: estimate exceeds 1 MiB")
		}
		return []byte(raw), nil
	}
	f, err := openRegularFileNonBlocking(path)
	if err != nil {
		return nil, newUsageError("--cli-input-json: cannot open estimate file")
	}
	return readEstimateFile(f)
}

func readEstimateFile(f *os.File) ([]byte, error) {
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, newUsageError("--cli-input-json: estimate requires a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCLIInputJSONSize+1))
	if err != nil {
		return nil, newUsageError("--cli-input-json: cannot read estimate file")
	}
	if len(data) > maxCLIInputJSONSize {
		return nil, newUsageError("--cli-input-json: estimate exceeds 1 MiB")
	}
	return data, nil
}

func checkEstimateJSON(dec *json.Decoder, typ reflect.Type, path string, depth int) error {
	token, err := dec.Token()
	if err != nil {
		return estimateInputError(path)
	}
	if token == nil && depth != 0 {
		return nil
	}
	if delim, ok := token.(json.Delim); ok {
		if depth >= 16 {
			return estimateInputError(path)
		}
		switch {
		case delim == '{' && typ.Kind() == reflect.Struct:
			seen := map[string]bool{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return estimateInputError(path)
				}
				key, ok := keyToken.(string)
				if !ok {
					return estimateInputError(path)
				}
				field, ok := typ.FieldByName(key)
				if !ok || !field.IsExported() {
					return estimateInputError(path)
				}
				fieldPath := path + "." + field.Name
				if seen[key] {
					return estimateInputError(fieldPath)
				}
				seen[key] = true
				if err := checkEstimateJSON(dec, field.Type, fieldPath, depth+1); err != nil {
					return err
				}
			}
		case delim == '[' && typ.Kind() == reflect.Slice:
			for dec.More() {
				if err := checkEstimateJSON(dec, typ.Elem(), path+"[]", depth+1); err != nil {
					return err
				}
			}
		default:
			return estimateInputError(path)
		}
		if _, err := dec.Token(); err != nil {
			return estimateInputError(path)
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.String:
		if _, ok := token.(string); ok {
			return nil
		}
	case reflect.Bool:
		if _, ok := token.(bool); ok {
			return nil
		}
	case reflect.Int, reflect.Int64:
		if number, ok := token.(json.Number); ok {
			if _, err := number.Int64(); err == nil {
				return nil
			}
		}
	}
	return estimateInputError(path)
}

var estimateCLIKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var estimateCLIPathPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
var estimateCLITypePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateEstimateInput(in *pricing.EstimateInput) error {
	if in.MaxRequests < 0 || in.MaxRequests > 2000 {
		return estimateInputError("MaxRequests")
	}
	rows, quantity := 0, 0
	for _, count := range []int{len(in.Servers), len(in.Volumes), len(in.PublicIPs), len(in.Storage)} {
		if count > 1000-rows {
			return estimateInputError("setup")
		}
		rows += count
	}
	if rows == 0 {
		return estimateInputError("setup")
	}
	keys := map[string]bool{}
	check := func(key string, count int) error {
		if !estimateCLIKeyPattern.MatchString(key) || keys[key] {
			return estimateInputError("Key")
		}
		keys[key] = true
		if count < 1 || count > 1000 || count > 10000-quantity {
			return estimateInputError("Quantity")
		}
		quantity += count
		return nil
	}
	servers := map[string]pricing.EstimateServer{}
	for _, server := range in.Servers {
		if err := check(server.Key, server.Quantity); err != nil {
			return err
		}
		if err := validateEstimateServer(server); err != nil {
			return err
		}
		servers[server.Key] = server
	}
	for _, volume := range in.Volumes {
		if err := check(volume.Key, volume.Quantity); err != nil {
			return err
		}
		if volume.Size <= 0 || !estimateCLIPathPattern.MatchString(volume.ZoneID) || !estimateCLIPathPattern.MatchString(volume.VolumeTypeID) ||
			(volume.EncryptionTypeID != "" && !estimateCLITypePattern.MatchString(volume.EncryptionTypeID)) {
			return estimateInputError("Volumes[]")
		}
		if volume.ServerKey != "" {
			server, ok := servers[volume.ServerKey]
			if !ok || volume.ZoneID != server.ZoneID || (volume.EncryptionTypeID != "" && server.RootDiskEncryptionTypeID == "") {
				return estimateInputError("Volumes[].ServerKey")
			}
		}
	}
	for _, ip := range in.PublicIPs {
		if err := check(ip.Key, ip.Quantity); err != nil {
			return err
		}
		switch ip.Kind {
		case "public-vip":
			if ip.Type != "public" && ip.Type != "public-vm" && ip.Type != "public-mkp" {
				return estimateInputError("PublicIPs[].Type")
			}
		case "floating-ip", "external-interface", "reservation":
			if ip.Type != "" {
				return estimateInputError("PublicIPs[].Type")
			}
		default:
			return estimateInputError("PublicIPs[].Kind")
		}
	}
	for _, storage := range in.Storage {
		if err := check(storage.Key, storage.Quantity); err != nil {
			return err
		}
		if storage.Type == "" || storage.QuotaGB <= 0 {
			return estimateInputError("Storage[]")
		}
	}
	return nil
}

func validateEstimateServer(server pricing.EstimateServer) error {
	for _, id := range []string{server.ZoneID, server.FlavorID, server.ImageID, server.RootDiskTypeID} {
		if !estimateCLIPathPattern.MatchString(id) {
			return estimateInputError("Servers[]")
		}
	}
	if server.RootDiskSize <= 0 || server.DataDiskSize < 0 || (server.DataDiskSize > 0) != (server.DataDiskTypeID != "") ||
		(server.DataDiskTypeID != "" && !estimateCLIPathPattern.MatchString(server.DataDiskTypeID)) ||
		(server.DataDiskEncryptionTypeID != "" && server.DataDiskSize == 0) {
		return estimateInputError("Servers[]")
	}
	for _, id := range []string{server.RootDiskEncryptionTypeID, server.DataDiskEncryptionTypeID} {
		if id != "" && !estimateCLITypePattern.MatchString(id) {
			return estimateInputError("Servers[]")
		}
	}
	return nil
}
