package mariner2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

type getDigestFunc func(intput llb.State) (string, string, error)

func getDigestFromClientFn(ctx context.Context, client gwclient.Client) getDigestFunc {
	return func(input llb.State) (name string, dgst string, _ error) {
		base := llb.Image(marinerRef, llb.WithMetaResolver(client))
		st := base.Run(
			llb.AddMount("/tmp/st", input, llb.Readonly),
			llb.Dir("/tmp/st"),
			shArgs("set -e -o pipefail; sha256sum * >> /digest"),
		).State

		def, err := llb.Diff(base, st).Marshal(ctx)
		if err != nil {
			return "", "", err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return "", "", err
		}
		dt, err := res.Ref.ReadFile(ctx, gwclient.ReadRequest{
			Filename: "/digest",
		})
		if err != nil {
			return "", "", err
		}

		// Format is `<hash> <filename>`
		split := bytes.Fields(bytes.TrimSpace(dt))
		return string(split[1]), string(split[0]), nil
	}
}

func handleToolkitRoot(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	st, err := spec2ToolkitRootLLB(spec, getDigestFromClientFn(ctx, client), sOpt)
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling llb: %w", err)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err
}

func spec2ToolkitRootLLB(spec *dalec.Spec, getDigest getDigestFunc, sOpt dalec.SourceOpts) (*llb.State, error) {
	scratch := llb.Scratch()
	specs, err := rpm.Dalec2SpecLLB(spec, &scratch, targetKey, "/")
	if err != nil {
		return &scratch, err
	}

	sources, err := rpm.Dalec2SourcesLLB(spec, sOpt)
	if err != nil {
		return &scratch, err
	}

	inputs := sources
	inputs = append(inputs, specs)

	// The mariner toolkit wants a signatures file in the spec dir (next to the spec file) that contains the sha256sum of all sources.
	sigs := make(map[string]string, len(sources))
	for _, src := range sources {
		fName, dgst, err := getDigest(src)
		if err != nil {
			return &scratch, fmt.Errorf("could not get digest for source: %w", err)
		}
		sigs[fName] = dgst
	}

	type sigData struct {
		Signatures map[string]string `json:"Signatures"`
	}

	var sd sigData
	sd.Signatures = sigs
	dt, err := json.Marshal(sd)
	if err != nil {
		return &scratch, fmt.Errorf("could not marshal signatures: %w", err)
	}
	inputs = append(inputs, llb.Scratch().File(
		llb.Mkfile(spec.Name+".signatures.json", 0o600, dt),
	))

	return dalec.MergeAtPath(&scratch, inputs, filepath.Join("/SPECS", spec.Name)), nil
}
