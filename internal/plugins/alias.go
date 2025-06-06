package plugins

import (
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

var Register = registry.Register
var Graph = registry.Graph

type Type = plugin.Type
type Registration = plugin.Registration
type InitContext = plugin.InitContext
