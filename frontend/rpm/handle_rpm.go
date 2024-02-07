package rpm

import (
	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
)

// Builds an RPM and source RPM from a spec
//
// `topDir` is the rpmbuild top directory which should contain the SOURCES and SPECS directories along with any other necessary files
//
// `workerImg` is the image to use for the build
// It is expected to have rpmbuild and any other necessary build dependencies installed
//
// `specPath` is the path to the spec file to build relative to `topDir`
func Build(topDir, workerImg llb.State, specPath string, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Build RPM"))
	return workerImg.Run(
		// some notes on these args:
		//  - _topdir is the directory where rpmbuild will look for SOURCES and SPECS
		//  - _srcrpmdir is the directory where rpmbuild will put the source RPM
		//  - _rpmdir is the directory where rpmbuild will put the RPM
		//  - --buildroot is the work directory where rpmbuild will build the RPM
		//  - -ba tells rpmbuild to build the source and binary RPMs
		//
		// TODO(cpuguy83): specPath would ideally never change.
		// We don't want to have to re-run this step just because the path changed, this should be based on inputs only (ie the content of the rpm spec, sources, etc)
		// The path should not be an input.
		shArgs(`rpmbuild --define "_topdir /build/top" --define "_srcrpmdir /build/out/SRPMS" --define "_rpmdir /build/out/RPMS" --buildroot /build/tmp/work -ba `+specPath),
		llb.AddMount("/build/top", topDir),
		llb.AddMount("/build/tmp", llb.Scratch(), llb.Tmpfs()),
		llb.Dir("/build/top"),
		dalec.WithConstraints(opts...),
	).
		AddMount("/build/out", llb.Scratch())
}
