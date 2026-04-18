package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"danny.vn/vngcloud"
)

type config struct {
	Regions    []string `json:"regions"`
	RootEmail  string   `json:"rootEmail"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	TOTPSecret string   `json:"totpSecret"`
	TOTPCode   string   `json:"totpCode"`
}

func main() {
	ctx := context.Background()

	configPath := flag.String("config", "", "path to one basic example config JSON; defaults to examples/basic/config.*.json except config.example.json")
	flag.Parse()

	configPaths, err := resolveConfigPaths(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve configs: %v\n", err)
		os.Exit(1)
	}
	if err := os.RemoveAll(outputRoot); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clear output: %v\n", err)
		os.Exit(1)
	}

	rawOutputs := newRawCaptureStore()
	sdkOutputs := newSDKOutputStore()

	for _, configPath := range configPaths {
		cfg, err := loadConfig(configPath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "load config %s: %v\n", configPath, err)
			os.Exit(1)
		}
		if len(cfg.Regions) == 0 {
			_, _ = fmt.Fprintf(os.Stderr, "load config %s: at least one region is required\n", configPath)
			os.Exit(1)
		}

		configName := configName(configPath)
		fmt.Printf("config: %s\n", configName)
		for regionIndex, region := range cfg.Regions {
			iamUser := &vngcloud.IAMUserAuth{
				RootEmail: cfg.RootEmail,
				Username:  cfg.Username,
				Password:  cfg.Password,
			}
			switch {
			case cfg.TOTPCode != "":
				code := normalizeTOTPCode(cfg.TOTPCode)
				iamUser.TOTP = vngcloud.TOTPFunc(func(context.Context) (string, error) {
					return code, nil
				})
			case cfg.TOTPSecret != "":
				secret := strings.TrimSpace(cfg.TOTPSecret)
				if looksLikeTOTPCode(secret) {
					code := normalizeTOTPCode(secret)
					iamUser.TOTP = vngcloud.TOTPFunc(func(context.Context) (string, error) {
						return code, nil
					})
				} else {
					iamUser.TOTP = &vngcloud.SecretTOTP{Secret: secret}
				}
			}

			client, err := vngcloud.NewClient(ctx, vngcloud.Config{
				Region:  region,
				IAMUser: iamUser,
			}, vngcloud.WithResponseCapture(func(captured vngcloud.ResponseCapture) {
				rawOutputs.add(configName, region, captured)
			}))
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "create client for config %s region %s: %v\n", configName, region, err)
				os.Exit(1)
			}

			fmt.Printf("region: %s\n", client.Region())
			sdkOutputs.setConfig(configName)
			showProjects(ctx, client, sdkOutputs)
			showPortal(ctx, client, sdkOutputs)
			showCompute(ctx, client, sdkOutputs)
			showVolume(ctx, client, sdkOutputs)
			showNetwork(ctx, client, sdkOutputs)
			showLoadBalancer(ctx, client, sdkOutputs)
			showGlobalLoadBalancer(ctx, client, sdkOutputs)
			if regionIndex == 0 {
				showGlobalLoadBalancerCatalog(ctx, client, sdkOutputs)
			}
			showDNS(ctx, client, sdkOutputs)
			showContainerRegistry(ctx, client, sdkOutputs)
		}
	}

	if err := rawOutputs.writeAll(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write raw output: %v\n", err)
		os.Exit(1)
	}
	if err := sdkOutputs.writeAll(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write sdk output: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("raw output: examples/basic/output/raw")
	fmt.Println("sdk output: examples/basic/output/sdk")

	reports, err := checkSmokeOutput(outputRoot)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "smokecheck: %v\n", err)
		os.Exit(1)
	}
	if !printSmokeCheck(reports) {
		os.Exit(1)
	}
}

func resolveConfigPaths(path string) ([]string, error) {
	if strings.TrimSpace(path) != "" {
		return []string{path}, nil
	}
	paths, err := filepath.Glob("examples/basic/config.*.json")
	if err != nil {
		return nil, err
	}
	filtered := paths[:0]
	for _, path := range paths {
		if filepath.Base(path) == "config.example.json" {
			continue
		}
		filtered = append(filtered, path)
	}
	sort.Strings(filtered)
	if len(filtered) == 0 {
		return nil, errors.New("no configs matched examples/basic/config.*.json")
	}
	return filtered, nil
}

func configName(path string) string {
	name := filepath.Base(path)
	name = strings.TrimPrefix(name, "config.")
	name = strings.TrimSuffix(name, ".json")
	return name
}

func looksLikeTOTPCode(value string) bool {
	value = normalizeTOTPCode(value)
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeTOTPCode(value string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(value))
}

func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
