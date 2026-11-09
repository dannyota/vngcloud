package billing

import (
	"fmt"
	"time"

	"danny.vn/vngcloud/internal/core"
)

var historyNow = time.Now

var historyZone = time.FixedZone("UTC+07:00", 7*60*60)

type historyDates struct{ start, end string }

func resolveHistoryDates(op, start, end string) (historyDates, error) {
	invalid := func() (historyDates, error) {
		return historyDates{}, fmt.Errorf("%w: %s requires a valid inclusive history date range", core.ErrInvalidInput, op)
	}
	if start == "" || core.CheckDate(op, "StartDate", start) != nil {
		return invalid()
	}
	if end == "" {
		end = historyNow().In(historyZone).Format("2006-01-02")
	}
	if core.CheckDate(op, "EndDate", end) != nil || start > end {
		return invalid()
	}
	from, err := time.ParseInLocation("2006-01-02", start, historyZone)
	if err != nil {
		return invalid()
	}
	through, err := time.ParseInLocation("2006-01-02", end, historyZone)
	if err != nil {
		return invalid()
	}
	exclusive := through.AddDate(0, 0, 1)
	if exclusive.Year() > 9999 {
		return invalid()
	}
	return historyDates{start: from.Format("02/01/2006"), end: exclusive.Format("02/01/2006")}, nil
}
