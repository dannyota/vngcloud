package backup

// Backend is a Backup Center backend described in docs/design/backup-services-api.md.
type Backend struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Policy is a Backup Center policy described in docs/design/backup-services-api.md.
type Policy struct {
	ID                  string       `json:"id"`
	BackendID           string       `json:"backendId"`
	ProjectID           string       `json:"projectId"`
	Product             string       `json:"product"`
	Name                string       `json:"name"`
	IsDefault           bool         `json:"isDefault"`
	BackupInstanceCount int          `json:"backupInstanceCount"`
	Config              PolicyConfig `json:"config"`
	CreatedAt           string       `json:"createdAt"`
	UpdatedAt           string       `json:"updatedAt"`
}

// PolicyConfig keeps verified daily settings and cadence enable flags, as
// described in docs/design/backup-services-api.md.
// Hourly, weekly, and monthly configuration details are withheld.
type PolicyConfig struct {
	Hour              int          `json:"hour"`
	Minute            int          `json:"minute"`
	TimeZone          string       `json:"timeZone"`
	HourlyEnabled     bool         `json:"hourlyEnabled"`
	DailyEnabled      bool         `json:"dailyEnabled"`
	WeeklyEnabled     bool         `json:"weeklyEnabled"`
	MonthlyEnabled    bool         `json:"monthlyEnabled"`
	IsProtectedServer bool         `json:"isProtectedServer"`
	DailyConfig       *DailyConfig `json:"dailyConfig"`
}

// DailyConfig preserves absent settings as nil, including in an empty object.
// Its response shape is described in docs/design/backup-services-api.md.
type DailyConfig struct {
	Retention           *int    `json:"retention"`
	BackupType          *string `json:"backupType"`
	IncrementalQuantity *int    `json:"incrementalQuantity"`
}
