package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/ini"
)

type objectProfile struct {
	Output   string
	ReadOnly string
}

// Object commands read policy without loading management credentials or caches.
func loadObjectProfile(flags *globalFlags) (objectProfile, error) {
	var profile objectProfile
	name := resolvedProfileName(flags)
	if err := validateProfileName(name); err != nil {
		return profile, err
	}
	path, explicit, err := resolveConfigureFilePath(envConfigFileVar, defaultConfigName)
	if err != nil {
		return profile, err
	}
	f, err := openRegularFileNonBlocking(path)
	if errors.Is(err, os.ErrNotExist) && !explicit && name == "default" {
		return profile, nil
	}
	if err != nil {
		return profile, fmt.Errorf("%w: could not open local object profile config", vngcloud.ErrInvalidConfig)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return profile, fmt.Errorf("%w: object profile config must be a regular file", vngcloud.ErrInvalidConfig)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigureFileSize+1))
	if err != nil || len(data) > maxConfigureFileSize {
		return profile, fmt.Errorf("%w: could not read bounded object profile config", vngcloud.ErrInvalidConfig)
	}
	if err := f.Close(); err != nil {
		return profile, fmt.Errorf("%w: could not close object profile config", vngcloud.ErrInvalidConfig)
	}
	file, err := ini.Parse("object profile config", bytes.NewReader(data))
	if err != nil {
		return profile, fmt.Errorf("%w: %w", vngcloud.ErrInvalidConfig, err)
	}
	section, exists := file.Section(configSectionName(name))
	if !exists && name != "default" {
		return profile, fmt.Errorf("%w: named object profile is absent from config", vngcloud.ErrInvalidConfig)
	}
	profile.Output, profile.ReadOnly = section["output"], section["read_only"]
	return profile, nil
}
