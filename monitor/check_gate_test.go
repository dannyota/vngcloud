package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckGateCancellationAndIndependentIDs(t *testing.T) {
	for _, operation := range []string{"pause", "resume", "update"} {
		t.Run(operation, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var sameIDReads atomic.Int64
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/chk-1") && sameIDReads.Add(1) == 1 {
					close(entered)
					<-release
				}
				body := fmt.Sprintf(currentCheckJSON, StatusEnabled)
				if strings.HasSuffix(r.URL.Path, "/chk-2") {
					body = strings.ReplaceAll(body, "chk-1", "chk-2")
				}
				_, _ = w.Write([]byte(body))
			}))
			name := "updated-check"
			owner := make(chan error, 1)
			go func() {
				_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &name})
				owner <- err
			}()
			<-entered
			ctx, cancel := context.WithCancel(context.Background())
			waiter := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "pause":
					_, err = client.PauseCheck(ctx, &PauseCheckInput{CheckID: "chk-1"})
				case "resume":
					_, err = client.ResumeCheck(ctx, &ResumeCheckInput{CheckID: "chk-1"})
				default:
					_, err = client.UpdateCheck(ctx, &UpdateCheckInput{CheckID: "chk-1", Name: &name})
				}
				waiter <- err
			}()
			cancel()
			independent := make(chan error, 1)
			go func() {
				_, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{CheckID: "chk-2"})
				independent <- err
			}()
			waiterReturned, otherReturned := false, false
			select {
			case err := <-waiter:
				waiterReturned = true
				if !errors.Is(err, context.Canceled) {
					t.Errorf("waiter error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Error("canceled same-check waiter remained blocked")
			}
			select {
			case err := <-independent:
				otherReturned = true
				if err != nil {
					t.Errorf("unrelated check: %v", err)
				}
			case <-time.After(time.Second):
				t.Error("unrelated check remained blocked")
			}
			close(release)
			if err := <-owner; err != nil {
				t.Errorf("owner: %v", err)
			}
			if !waiterReturned {
				if err := <-waiter; !errors.Is(err, context.Canceled) {
					t.Errorf("waiter after release: %v", err)
				}
			}
			if !otherReturned {
				if err := <-independent; err != nil {
					t.Errorf("unrelated check after release: %v", err)
				}
			}
			if sameIDReads.Load() != 1 {
				t.Fatalf("same-check reads = %d, want owner only", sameIDReads.Load())
			}
		})
	}
}
