package engine

import (
	"context"
	"fmt"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/windmilleng/tilt/internal/build"
	"github.com/windmilleng/tilt/internal/k8s"
	"github.com/windmilleng/tilt/internal/model"
	"github.com/windmilleng/tilt/internal/synclet"
	"google.golang.org/grpc"
)

var _ BuildAndDeployer = &SyncletBuildAndDeployer{}

type SyncletBuildAndDeployer struct {
	// NOTE(maia): hacky intermediate SyncletBaD takes a single client,
	// assumes port forwarding a single synclet on <port> -- later, will need
	// a map of NodeID -> syncletClient
	sCli synclet.SyncletClient

	kCli k8s.Client
}

func DefaultSyncletClient(env k8s.Env) (synclet.SyncletClient, error) {
	if env != k8s.EnvGKE {
		return nil, nil
	}

	conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", synclet.Port), grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("connecting to synclet: %v", err)
	}
	cli := synclet.NewGRPCClient(conn)
	return cli, nil
}

func NewSyncletBuildAndDeployer(sCli synclet.SyncletClient, kCli k8s.Client) *SyncletBuildAndDeployer {
	return &SyncletBuildAndDeployer{
		sCli: sCli,
		kCli: kCli,
	}
}

func (sbd *SyncletBuildAndDeployer) BuildAndDeploy(ctx context.Context, service model.Service, state BuildState) (BuildResult, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "SyncletBuildAndDeployer-BuildAndDeploy")
	span.SetTag("service", service.Name.String())
	defer span.Finish()

	// TODO(maia): proper output for this stuff

	if err := sbd.canSyncletBuild(ctx, service, state); err != nil {
		return BuildResult{}, err
	}

	return sbd.updateViaSynclet(ctx, service, state)
}

// canSyncletBuild returns an error if we CAN'T build this service via the synclet
func (sbd *SyncletBuildAndDeployer) canSyncletBuild(ctx context.Context,
	service model.Service, state BuildState) error {

	// TODO(maia): put service.Validate() upstream if we're gonna want to call it regardless
	// of implementation of BuildAndDeploy?
	err := service.Validate()
	if err != nil {
		return err
	}

	// SyncletBuildAndDeployer doesn't support initial build
	if state.IsEmpty() {
		return fmt.Errorf("prev. build state is empty; synclet build does not support initial deploy")
	}

	// Can't do container update if we don't know what container service is running in.
	if !state.LastResult.HasContainer() {
		return fmt.Errorf("prev. build state has no container")
	}

	return nil
}

func (sbd *SyncletBuildAndDeployer) updateViaSynclet(ctx context.Context,
	service model.Service, state BuildState) (BuildResult, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "SyncletBuildAndDeployer-updateViaSynclet")
	defer span.Finish()

	paths, err := build.FilesToPathMappings(state.FilesChanged(), service.Mounts)
	if err != nil {
		return BuildResult{}, err
	}

	// archive files to copy to container
	ab := build.NewArchiveBuilder()
	err = ab.ArchivePathsIfExist(ctx, paths)
	if err != nil {
		return BuildResult{}, fmt.Errorf("archivePathsIfExists: %v", err)
	}
	archive, err := ab.BytesBuffer()
	if err != nil {
		return BuildResult{}, err
	}

	// get files to rm
	toRemove, err := build.MissingLocalPaths(ctx, paths)
	if err != nil {
		return BuildResult{}, fmt.Errorf("missingLocalPaths: %v", err)
	}
	// TODO(maia): can refactor MissingLocalPaths to just return ContainerPaths?
	containerPathsToRm := build.PathMappingsToContainerPaths(toRemove)

	cID := state.LastResult.Container

	cmds, err := build.BoilSteps(service.Steps, paths)
	if err != nil {
		return BuildResult{}, err
	}
	err = sbd.sCli.UpdateContainer(ctx, cID, archive.Bytes(), containerPathsToRm, cmds)
	if err != nil {
		return BuildResult{}, err
	}

	return BuildResult{
		Entities:  state.LastResult.Entities,
		Container: cID,
	}, nil
}

func (sbd *SyncletBuildAndDeployer) GetContainerForBuild(ctx context.Context, build BuildResult) (k8s.ContainerID, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "SyncletBuildAndDeployer-GetContainerForBuild")
	defer span.Finish()

	// get pod running the image we just deployed
	pID, err := sbd.kCli.PodWithImage(ctx, build.Image)
	if err != nil {
		return "", fmt.Errorf("PodWithImage (img = %s): %v", build.Image, err)
	}

	// get container that's running the app for the pod we found
	cID, err := sbd.sCli.GetContainerIdForPod(ctx, pID)
	if err != nil {
		return "", fmt.Errorf("syncletClient.GetContainerIdForPod (pod = %s): %v", pID, err)
	}

	return cID, nil
}
