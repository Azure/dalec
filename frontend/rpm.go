package frontend

import "fmt"

type RPMConfig struct {
	Setup []RPMSetupConfig
}

type RPMSetupConfig struct {
	Sources []RPMSource
}

type RPMSetupOrder string

const (
	RPMSetupOrderNone   RPMSetupOrder = ""
	RPMSetupOrderBefore RPMSetupOrder = "before"
	RPMSetupOrderAfter  RPMSetupOrder = "after"
)

func (o RPMSetupOrder) Validate() error {
	switch o {
	case RPMSetupOrderNone, RPMSetupOrderBefore, RPMSetupOrderAfter:
		return nil
	default:
		return fmt.Errorf("unknown setup order: %q", o)
	}
}

type RPMSource struct {
	// Name is the name of the source this represents
	Name     string
	NoUnpack bool
	Order    RPMSetupOrder
}
