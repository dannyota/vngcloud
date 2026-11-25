package cli

import (
	"io"

	"danny.vn/vngcloud/monitor"
)

func monitorDashboardView(op Op[monitor.Client]) Op[monitor.Client] {
	op.render = func(w io.Writer, format, query string, out any) error {
		if format == outputJSON || query != "" {
			return renderOutput(w, format, query, out, false)
		}
		rows := []map[string]any{}
		add := func(d monitor.Dashboard) {
			rows = append(rows, map[string]any{"ID": d.ID, "Name": d.Name, "System": d.System, "Favorite": d.Favorite, "UpdatedAt": d.UpdatedAt})
		}
		switch value := out.(type) {
		case *monitor.ListDashboardsOutput:
			for _, d := range value.Items {
				add(d)
			}
		case *monitor.GetDashboardOutput:
			add(value.Dashboard)
		}
		return renderOutput(w, format, "", rows, false)
	}
	return op
}
