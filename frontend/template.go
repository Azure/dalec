package frontend

import "github.com/Azure/dalec"

func DefaultLibexecSubpath(s *dalec.Spec, k string) string {
	if s.Artifacts.Libexec[k].SubPath != "" {
		return s.Artifacts.Libexec[k].SubPath
	}

	return s.Name
}
