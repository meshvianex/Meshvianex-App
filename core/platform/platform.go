package platform

import (
	"runtime"
)

type Capabilities struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Network      []string `json:"network,omitempty"`
}

func Detect() Capabilities {
	return Capabilities{OS: runtime.GOOS, Architecture: runtime.GOARCH, Network: []string{"tcp"}}
}
func (c Capabilities) Supports(name string) bool {
	for _, v := range c.Network {
		if v == name {
			return true
		}
	}
	return false
}
