package storage

import (
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

// statusCase is one HTTP status a settings call must handle.
type statusCase struct {
	status  int
	wantErr bool
	want    error
}

var statusCases = []statusCase{
	{http.StatusOK, false, nil},
	{http.StatusBadRequest, true, nil},
	{http.StatusForbidden, true, vngcloud.ErrPermission},
	{http.StatusNotFound, true, vngcloud.ErrNotFound},
	{http.StatusInternalServerError, true, nil},
	{http.StatusBadGateway, true, nil},
}

// checkStatusCase runs call with a success body for a success status and a
// non-envelope body for an error status, and checks the result.
func checkStatusCase(t *testing.T, tt statusCase, call func(body string, status int) error, okBody string) {
	t.Helper()
	body := okBody
	if tt.wantErr {
		body = `{"message":"x"}`
	}
	err := call(body, tt.status)
	if !tt.wantErr {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
		t.Fatalf("err = %v, want *APIError with status %d", err, tt.status)
	}
	if tt.want != nil && !errors.Is(err, tt.want) {
		t.Fatalf("err = %v, want %v", err, tt.want)
	}
}
