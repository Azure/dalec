package main

import (
	"context"
	"strings"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	internalPrefix = "[internal] "
)

func convertStatus(event *client.SolveStatus) *progrock.StatusUpdate {
	if event == nil {
		return nil
	}
	var status progrock.StatusUpdate
	for _, v := range event.Vertexes {
		vtx := &progrock.Vertex{
			Id:     v.Digest.String(),
			Name:   v.Name,
			Cached: v.Cached,
		}
		if strings.HasPrefix(v.Name, internalPrefix) {
			vtx.Internal = true
			vtx.Name = strings.TrimPrefix(v.Name, internalPrefix)
		}
		for _, input := range v.Inputs {
			vtx.Inputs = append(vtx.Inputs, input.String())
		}
		if v.Started != nil {
			vtx.Started = timestamppb.New(*v.Started)
		}
		if v.Completed != nil {
			vtx.Completed = timestamppb.New(*v.Completed)
		}
		if v.Error != "" {
			if strings.HasSuffix(v.Error, context.Canceled.Error()) {
				vtx.Canceled = true
			} else {
				msg := v.Error
				vtx.Error = &msg
			}
		}
		status.Vertexes = append(status.Vertexes, vtx)
	}

	for _, s := range event.Statuses {
		task := &progrock.VertexTask{
			Vertex:  s.Vertex.String(),
			Name:    s.ID,
			Total:   s.Total,
			Current: s.Current,
		}
		if s.Started != nil {
			task.Started = timestamppb.New(*s.Started)
		}
		if s.Completed != nil {
			task.Completed = timestamppb.New(*s.Completed)
		}
		status.Tasks = append(status.Tasks, task)
	}

	for _, s := range event.Logs {
		status.Logs = append(status.Logs, &progrock.VertexLog{
			Vertex:    s.Vertex.String(),
			Stream:    progrock.LogStream(s.Stream),
			Data:      s.Data,
			Timestamp: timestamppb.New(s.Timestamp),
		})
	}

	return &status
}

func handleEvents(ctx context.Context, ch <-chan *client.SolveStatus, r *progrock.Recorder) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			if event == nil {
				return
			}
			var digsts []digest.Digest
			for _, v := range event.Vertexes {
				rr := withProgGroup(v.ProgressGroup, r)
				if rr != nil {
					rr.Join(v.Digest)
				} else {
					digsts = append(digsts, v.Digest)
				}
			}
			r.Join(digsts...)
			if err := r.Record(convertStatus(event)); err != nil {
				return
			}
		}
	}
}

func withProgGroup(pg *pb.ProgressGroup, r *progrock.Recorder) *progrock.Recorder {
	if pg == nil {
		return nil
	}
	if pg.Weak {
		return r.WithGroup(pg.Name, progrock.Weak())
	}
	return r.WithGroup(pg.Name)
}
