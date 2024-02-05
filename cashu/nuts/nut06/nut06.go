package nut06

type MintInfo struct {
	Name            string         `json:"name"`
	Pubkey          string         `json:"pubkey"`
	Version         string         `json:"version"`
	Description     string         `json:"description"`
	LongDescription string         `json:"description_long,omitempty"`
	Contact         [][]string     `json:"contact,omitempty"`
	Motd            string         `json:"motd,omitempty"`
	Nuts            map[string]any `json:"nuts"`
}
