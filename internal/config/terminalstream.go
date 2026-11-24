package config

type HighlightRule struct {
	Pattern       string `json:"pattern"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	Color         string `json:"color,omitempty"`
	Bold          bool   `json:"bold,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Host          string `json:"host,omitempty"`
}

type TerminalHighlight struct {
	Disabled  bool            `json:"disabled,omitempty"`
	UseCustom bool            `json:"useCustom,omitempty"`
	Rules     []HighlightRule `json:"rules,omitempty"`
}

type TerminalRecording struct {
	Automatic bool   `json:"automatic,omitempty"`
	Text      bool   `json:"text,omitempty"`
	Folder    string `json:"folder,omitempty"`
	LimitMiB  int    `json:"limitMiB,omitempty"`
}
