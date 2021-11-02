// Code generated by Wire. DO NOT EDIT.

//go:generate wire
//+build !wireinject

package engine

import (
	"context"

	"github.com/google/wire"
	"github.com/jonboulle/clockwork"
	"github.com/tilt-dev/wmclient/pkg/dirs"
	"go.opentelemetry.io/otel/sdk/trace"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/tilt-dev/tilt/internal/analytics"
	"github.com/tilt-dev/tilt/internal/build"
	"github.com/tilt-dev/tilt/internal/container"
	"github.com/tilt-dev/tilt/internal/containerupdate"
	"github.com/tilt-dev/tilt/internal/controllers/core/cmd"
	"github.com/tilt-dev/tilt/internal/controllers/core/kubernetesapply"
	"github.com/tilt-dev/tilt/internal/controllers/core/liveupdate"
	"github.com/tilt-dev/tilt/internal/docker"
	"github.com/tilt-dev/tilt/internal/dockercompose"
	"github.com/tilt-dev/tilt/internal/dockerfile"
	"github.com/tilt-dev/tilt/internal/engine/buildcontrol"
	"github.com/tilt-dev/tilt/internal/k8s"
	"github.com/tilt-dev/tilt/internal/localexec"
	"github.com/tilt-dev/tilt/internal/store"
	"github.com/tilt-dev/tilt/internal/store/liveupdates"
	"github.com/tilt-dev/tilt/internal/tracer"
	"github.com/tilt-dev/tilt/pkg/apis/core/v1alpha1"
)

// Injectors from wire.go:

func provideFakeBuildAndDeployer(ctx context.Context, docker2 docker.Client, kClient k8s.Client, dir *dirs.TiltDevDir, env k8s.Env, updateMode liveupdates.UpdateModeFlag, dcc dockercompose.DockerComposeClient, clock build.Clock, kp buildcontrol.KINDLoader, analytics2 *analytics.TiltAnalytics, ctrlClient client.Client, st store.RStore, execer localexec.Execer) (buildcontrol.BuildAndDeployer, error) {
	dockerUpdater := containerupdate.NewDockerUpdater(docker2)
	execUpdater := containerupdate.NewExecUpdater(kClient)
	kubeContext := provideFakeKubeContext(env)
	runtime := k8s.ProvideContainerRuntime(ctx, kClient)
	clusterEnv := provideFakeDockerClusterEnv(docker2, env, kubeContext, runtime)
	liveupdatesUpdateMode, err := liveupdates.ProvideUpdateMode(updateMode, kubeContext, clusterEnv)
	if err != nil {
		return nil, err
	}
	scheme := v1alpha1.NewScheme()
	reconciler := liveupdate.NewReconciler(st, dockerUpdater, execUpdater, liveupdatesUpdateMode, kubeContext, ctrlClient, scheme)
	liveUpdateBuildAndDeployer := buildcontrol.NewLiveUpdateBuildAndDeployer(reconciler, clock)
	labels := _wireLabelsValue
	dockerImageBuilder := build.NewDockerImageBuilder(docker2, labels)
	dockerBuilder := build.DefaultDockerBuilder(dockerImageBuilder)
	execCustomBuilder := build.NewExecCustomBuilder(docker2, clock)
	namespace := provideFakeK8sNamespace()
	kubernetesapplyReconciler := kubernetesapply.NewReconciler(ctrlClient, kClient, scheme, dockerBuilder, kubeContext, st, namespace, execer)
	imageBuildAndDeployer := buildcontrol.NewImageBuildAndDeployer(dockerBuilder, execCustomBuilder, kClient, env, kubeContext, analytics2, clock, kp, ctrlClient, kubernetesapplyReconciler)
	imageBuilder := buildcontrol.NewImageBuilder(dockerBuilder, execCustomBuilder)
	dockerComposeBuildAndDeployer := buildcontrol.NewDockerComposeBuildAndDeployer(dcc, docker2, imageBuilder, clock)
	localexecEnv := provideFakeEnv()
	cmdExecer := cmd.ProvideExecer(localexecEnv)
	proberManager := cmd.ProvideProberManager()
	clockworkClock := clockwork.NewRealClock()
	controller := cmd.NewController(ctx, cmdExecer, proberManager, ctrlClient, st, clockworkClock, scheme)
	localTargetBuildAndDeployer := buildcontrol.NewLocalTargetBuildAndDeployer(clock, ctrlClient, controller)
	buildOrder := DefaultBuildOrder(liveUpdateBuildAndDeployer, imageBuildAndDeployer, dockerComposeBuildAndDeployer, localTargetBuildAndDeployer, liveupdatesUpdateMode, env, runtime)
	spanExporter := _wireSpanExporterValue
	traceTracer := tracer.InitOpenTelemetry(spanExporter)
	compositeBuildAndDeployer := NewCompositeBuildAndDeployer(buildOrder, traceTracer)
	return compositeBuildAndDeployer, nil
}

var (
	_wireLabelsValue       = dockerfile.Labels{}
	_wireSpanExporterValue = (trace.SpanExporter)(nil)
)

// wire.go:

var DeployerBaseWireSet = wire.NewSet(buildcontrol.BaseWireSet, wire.Value(UpperReducer), DefaultBuildOrder, wire.Bind(new(buildcontrol.BuildAndDeployer), new(*CompositeBuildAndDeployer)), NewCompositeBuildAndDeployer)

var DeployerWireSetTest = wire.NewSet(
	DeployerBaseWireSet, wire.InterfaceValue(new(trace.SpanExporter), (trace.SpanExporter)(nil)),
)

var DeployerWireSet = wire.NewSet(
	DeployerBaseWireSet,
)

func provideFakeEnv() *localexec.Env {
	return localexec.EmptyEnv()
}

func provideFakeK8sNamespace() k8s.Namespace {
	return "default"
}

func provideFakeKubeContext(env k8s.Env) k8s.KubeContext {
	return k8s.KubeContext(string(env))
}

// A simplified version of the normal calculation we do
// about whether we can build direct to a cluser
func provideFakeDockerClusterEnv(c docker.Client, k8sEnv k8s.Env, kubeContext k8s.KubeContext, runtime container.Runtime) docker.ClusterEnv {
	env := c.Env()
	isDockerRuntime := runtime == container.RuntimeDocker
	isLocalDockerCluster := k8sEnv == k8s.EnvMinikube || k8sEnv == k8s.EnvMicroK8s || k8sEnv == k8s.EnvDockerDesktop
	if isDockerRuntime && isLocalDockerCluster {
		env.BuildToKubeContexts = append(env.BuildToKubeContexts, string(kubeContext))
	}

	fake, ok := c.(*docker.FakeClient)
	if ok {
		fake.FakeEnv = env
	}

	return docker.ClusterEnv(env)
}
