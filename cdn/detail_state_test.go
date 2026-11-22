package cdn

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestWebAcceleratorRequiresIdentityAndStatusBeforeWrites(t *testing.T) {
	for _, data := range []string{
		`{"cdnId":"cdn-1"}`, `{"cdnId":"cdn-1","status":null}`,
		`{"cdnId":"cdn-2","status":1}`, `{"status":1}`,
		`{"cdnId":null,"status":1}`, `{"cdnId":"","status":1}`,
	} {
		for _, operation := range writeCalls() {
			t.Run(operation.name+"/"+data, func(t *testing.T) {
				writes := 0
				h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						jsonReply(w, `{"success":true,"data":`+data+`}`)
						return
					}
					writes++
					jsonReply(w, okEnvelope)
				})
				clock := &fakeClock{}
				clock.install(h.Client)
				if err := operation.run(h); err == nil || writes != 0 {
					t.Fatalf("error = %v, writes = %d; want invalid detail with no write", err, writes)
				}
			})
		}
	}
}

func TestGetWebAcceleratorRequiresIdentityAndStatus(t *testing.T) {
	for _, data := range []string{
		`{"cdnId":"cdn-1"}`, `{"cdnId":"cdn-1","status":null}`,
		`{"cdnId":"cdn-2","status":1}`, `{"status":0}`,
	} {
		t.Run(data, func(t *testing.T) {
			h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"data":`+data+`}`))
			out, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: "cdn-1"})
			if err == nil || out != nil {
				t.Fatalf("output = %+v, error = %v; want rejected detail", out, err)
			}
		})
	}
}

func TestGetWebAcceleratorExplicitStatuses(t *testing.T) {
	for _, status := range []int{0, 1, 3, 4, 5, 99} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			h := newVCDN(t, testKey, reply(200, "application/json",
				fmt.Sprintf(`{"success":true,"data":{"cdnId":12,"status":%d}}`, status)))
			out, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: "12"})
			if err != nil || out.WebAccelerator.Status != status || out.WebAccelerator.CDNID != "12" {
				t.Fatalf("output = %+v, error = %v", out, err)
			}
		})
	}
}
