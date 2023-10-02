package mariner2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/azure/dalec"
	"github.com/azure/dalec/frontend"
	"github.com/azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
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
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := spec2ToolkitRootLLB(spec, noMerge, getDigestFromClientFn(ctx, client), client, frontend.ForwarderFromClient(ctx, client))
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
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

func spec2ToolkitRootLLB(spec *dalec.Spec, noMerge bool, getDigest getDigestFunc, mr llb.ImageMetaResolver, forward dalec.ForwarderFunc) (llb.State, error) {
	specs, err := rpm.Dalec2SpecLLB(spec, llb.Scratch(), targetKey, "/")
	if err != nil {
		return llb.Scratch(), err
	}

	sources, err := rpm.Dalec2SourcesLLB(spec, mr, forward)
	if err != nil {
		return llb.Scratch(), err
	}

	inputs := append(sources, specs)

	// The mariner toolkit wants a signatures file in the spec dir (next to the spec file) that contains the sha256sum of all sources.
	sigs := make(map[string]string, len(sources))
	for _, src := range sources {
		fName, dgst, err := getDigest(src)
		if err != nil {
			return llb.Scratch(), fmt.Errorf("could not get digest for source: %w", err)
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
		return llb.Scratch(), fmt.Errorf("could not marshal signatures: %w", err)
	}
	inputs = append(inputs, llb.Scratch().File(
		llb.Mkfile(spec.Name+".signatures.json", 0600, dt),
	))

	return dalec.MergeAtPath(llb.Scratch(), inputs, filepath.Join("/SPECS", spec.Name)), nil
}
