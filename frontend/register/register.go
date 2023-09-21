// Package register automatically registers all known targets with the main frontend

package register

import (
	"github.com/azure/dalec/frontend/debug"
	"github.com/azure/dalec/frontend/mariner2"
)

func init() {
	debug.RegisterTargets()
	mariner2.RegisterTargets()
}
