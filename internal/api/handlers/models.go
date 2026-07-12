package handlers

type dnsRecordInput struct {
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type blocklistInput struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

type domainInput struct {
	Domain string `json:"domain"`
}

type deviceAliasInput struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Notes    string `json:"notes"`
}

type eventInput struct {
	Type        string
	Severity    string
	Title       string
	Description string
	ClientIP    string
	Domain      string
	Metadata    map[string]any
	Source      string
}
