package config

type AlertRules struct {
	Unreachable bool    `json:"unreachable,omitempty"`
	CPU         float64 `json:"cpu,omitempty"`
	CPUMinutes  int     `json:"cpuMinutes,omitempty"`
	Memory      float64 `json:"memory,omitempty"`
	Disk        float64 `json:"disk,omitempty"`
	Load        float64 `json:"loadPerCore,omitempty"`
	Services    bool    `json:"services,omitempty"`
}

func (s Settings) AlertsFor(hostID string) AlertRules {
	if s.AlertRules == nil {
		return AlertRules{}
	}
	if rules, ok := (*s.AlertRules)[hostID]; ok {
		return rules
	}
	return (*s.AlertRules)[""]
}
