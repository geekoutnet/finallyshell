package config

type TunnelManagerSettings struct {
	NoAutostart bool `json:"noAutostart,omitempty"`
}

func (s *TunnelManagerSettings) Autostart() bool { return s == nil || !s.NoAutostart }
