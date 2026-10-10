package backup

type Backend struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

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

// PolicyConfig keeps verified daily settings and cadence enable flags.
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
type DailyConfig struct {
	Retention           *int    `json:"retention"`
	BackupType          *string `json:"backupType"`
	IncrementalQuantity *int    `json:"incrementalQuantity"`
}
