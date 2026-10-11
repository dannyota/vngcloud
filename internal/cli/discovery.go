package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/storage"
	"danny.vn/vngcloud/volume"
)

func discoveryRead[C, In, Out any](name string, method func(*C, context.Context, *In) (*Out, error), opts ...readOption) Op[C] {
	op := Read(name, method, opts...)
	op.discovery = true
	op.call = func(_ *cobra.Command, client *C, ctx context.Context, in any) (any, error) {
		return method(client, ctx, in.(*In))
	}
	op.extraFlags = func(cmd *cobra.Command) {
		cmd.Flags().String("sort-by", "", "sort by name, monthly-price, or unit-price")
		cmd.Flags().Bool("descending", false, "sort known values in descending order")
	}
	return op
}

func validateDiscoverySort(cmd *cobra.Command) error {
	key, _ := cmd.Flags().GetString("sort-by")
	if key != "" && key != "name" && key != "monthly-price" && key != "unit-price" {
		return newUsageError("--sort-by must be name, monthly-price, or unit-price")
	}
	return nil
}

func numericDiscoveryCompare(a, b *float64, descending bool) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return 1
	}
	if b == nil {
		return -1
	}
	result := cmp.Compare(*a, *b)
	if descending {
		return -result
	}
	return result
}

func volumeDiscoveryCompare(a, b volume.VolumePrice) int {
	aid, bid := a.VolumeType.VolumeTypeID, b.VolumeType.VolumeTypeID
	if aid == "" {
		aid = a.VolumeType.ID
	}
	if bid == "" {
		bid = b.VolumeType.ID
	}
	return cmp.Or(cmp.Compare(a.ZoneID, b.ZoneID), cmp.Compare(a.VolumeType.Name, b.VolumeType.Name), cmp.Compare(aid, bid), cmp.Compare(a.EncryptionTypeID, b.EncryptionTypeID))
}
func storageDiscoveryCompare(a, b storage.StoragePrice) int {
	return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Type, b.Type), cmp.Compare(a.TypeID, b.TypeID), cmp.Compare(a.PurchaseTypeID, b.PurchaseTypeID))
}

func sortDiscovery(out any, key string, descending bool) {
	switch out := out.(type) {
	case *volume.ListVolumePricesOutput:
		slices.SortStableFunc(out.Items, func(a, b volume.VolumePrice) int {
			result := 0
			switch key {
			case "name":
				result = cmp.Compare(a.VolumeType.Name, b.VolumeType.Name)
				if descending {
					result = -result
				}
			case "monthly-price":
				result = numericDiscoveryCompare(a.Price.MonthlyPrice, b.Price.MonthlyPrice, descending)
			case "unit-price":
				result = numericDiscoveryCompare(a.PricePerGB, b.PricePerGB, descending)
			}
			return cmp.Or(result, volumeDiscoveryCompare(a, b))
		})
	case *storage.ListStoragePricesOutput:
		slices.SortStableFunc(out.Items, func(a, b storage.StoragePrice) int {
			result := 0
			switch key {
			case "name":
				result = cmp.Compare(a.Type, b.Type)
				if descending {
					result = -result
				}
			case "monthly-price":
				result = numericDiscoveryCompare(a.Price.MonthlyPrice, b.Price.MonthlyPrice, descending)
			case "unit-price":
				result = numericDiscoveryCompare(a.PricePerGB, b.PricePerGB, descending)
			}
			return cmp.Or(result, storageDiscoveryCompare(a, b))
		})
	}
}

func discoveryAmount(value *float64) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.2f", *value)
}
func discoveryEnabled(value *bool) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatBool(*value)
}
func discoveryText(value string) string {
	if value == "" {
		return "unknown"
	}
	return escapeControlChars(value)
}

func renderDiscovery(stdout, stderr io.Writer, format, query string, out any) error {
	var metadata pricing.DiscoveryMetadata
	rows := []any{}
	switch out := out.(type) {
	case *volume.ListVolumePricesOutput:
		metadata = out.Metadata
		for _, row := range out.Items {
			encryption := row.EncryptionTypeID
			if encryption == "" {
				encryption = "none"
			}
			rows = append(rows, map[string]any{"Region": metadata.Region, "ZoneID": row.ZoneID, "ZoneName": row.ZoneName, "ZoneEnabled": discoveryEnabled(row.ZoneEnabled), "Type": row.VolumeType.Name, "ID": row.VolumeType.ID, "VolumeTypeID": row.VolumeType.VolumeTypeID, "IOPS": row.VolumeType.IOPS, "Encryption": encryption, "SizeGB": row.SizeGB, "Status": row.Price.Status, "Source": row.Price.Source, "MonthlyPrice": discoveryAmount(row.Price.MonthlyPrice), "PricePerGB": discoveryAmount(row.PricePerGB), "Currency": discoveryText(row.Price.Currency), "Tax": discoveryText(row.Price.Tax)})
		}
	case *storage.ListStoragePricesOutput:
		metadata = out.Metadata
		for _, row := range out.Items {
			rows = append(rows, map[string]any{"Region": row.Region, "Type": row.Type, "TypeID": row.TypeID, "PurchaseType": row.PurchaseType, "PurchaseTypeID": row.PurchaseTypeID, "PriceKey": row.PriceKey, "QuotaGB": row.QuotaGB, "MinQuotaGB": row.MinQuotaGB, "MaxQuotaGB": row.MaxQuotaGB, "Status": row.Price.Status, "Source": row.Price.Source, "MonthlyPrice": discoveryAmount(row.Price.MonthlyPrice), "PricePerGB": discoveryAmount(row.PricePerGB), "Currency": discoveryText(row.Price.Currency), "Tax": discoveryText(row.Price.Tax)})
		}
	default:
		return errors.New("unsupported discovery output")
	}
	if format != outputJSON {
		for _, note := range metadata.Notes {
			if _, err := fmt.Fprintf(stderr, "%s: %s\n", escapeControlChars(note.Topic), escapeControlChars(note.Text)); err != nil {
				return err
			}
		}
		if !metadata.Complete {
			if _, err := fmt.Fprintln(stderr, "discovery coverage is incomplete"); err != nil {
				return err
			}
		}
	}
	if format == outputJSON || strings.TrimSpace(query) != "" {
		return renderOutput(stdout, format, query, out, false)
	}
	if format == outputTable {
		return renderTable(stdout, rows)
	}
	return renderText(stdout, rows)
}
