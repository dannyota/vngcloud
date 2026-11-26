package volume

import (
	"net/http"
	"strings"
	"testing"
)

func TestVolumePricesSelectedDisabledZoneSizeReason(t *testing.T) {
	c := newTestClient(t, volumeDiscoveryHandler(t, map[string]string{"/v1/project-1/zones": `{"data":[{"uuid":"HCM03-1C","isEnabled":false}]}`}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
	out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: "HCM03-1C", IOPS: 3000, Encryption: "none", Size: 5001})
	if err != nil || out == nil || len(out.Items) != 1 || out.Items[0].Price.Status != "inapplicable" {
		t.Fatalf("output %+v error %v", out, err)
	}
	for _, note := range out.Metadata.Notes {
		if note.Topic == "inapplicable" {
			if !strings.Contains(note.Text, "size") || strings.Contains(note.Text, "zone") {
				t.Fatalf("reason %q", note.Text)
			}
			return
		}
	}
	t.Fatal("missing inapplicable reason")
}
