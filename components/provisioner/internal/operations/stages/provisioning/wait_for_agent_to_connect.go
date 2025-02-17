package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/kyma-project/control-plane/components/provisioner/internal/runtime"

	"github.com/kyma-project/control-plane/components/provisioner/internal/apperrors"
	"github.com/kyma-project/control-plane/components/provisioner/internal/util"

	"github.com/kyma-incubator/compass/components/director/pkg/graphql"
	"github.com/kyma-project/control-plane/components/provisioner/internal/director"
	"github.com/kyma-project/control-plane/components/provisioner/internal/model"
	"github.com/kyma-project/control-plane/components/provisioner/internal/operations"
	"github.com/kyma-project/control-plane/components/provisioner/internal/util/k8s"
	"github.com/kyma-project/kyma/components/compass-runtime-agent/pkg/apis/compass/v1alpha1"
	compass_conn_clientset "github.com/kyma-project/kyma/components/compass-runtime-agent/pkg/client/clientset/versioned/typed/compass/v1alpha1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	defaultCompassConnectionName = "compass-connection"
)

type CompassConnectionClientConstructor func(k8sConfig *rest.Config) (compass_conn_clientset.CompassConnectionInterface, error)

func NewCompassConnectionClient(k8sConfig *rest.Config) (compass_conn_clientset.CompassConnectionInterface, error) {
	compassConnClientset, err := compass_conn_clientset.NewForConfig(k8sConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error: failed to create Compass Connection client")
	}

	return compassConnClientset.CompassConnections(), nil
}

type WaitForAgentToConnectStep struct {
	newCompassConnectionClient CompassConnectionClientConstructor
	runtimeConfigurator        runtime.Configurator
	directorClient             director.DirectorClient
	nextStep                   model.OperationStage
	timeLimit                  time.Duration
}

func NewWaitForAgentToConnectStep(
	ccClientProvider CompassConnectionClientConstructor,
	runtimeConfigurator runtime.Configurator,
	nextStep model.OperationStage,
	timeLimit time.Duration,
	directorClient director.DirectorClient) *WaitForAgentToConnectStep {

	return &WaitForAgentToConnectStep{
		newCompassConnectionClient: ccClientProvider,
		runtimeConfigurator:        runtimeConfigurator,
		directorClient:             directorClient,
		nextStep:                   nextStep,
		timeLimit:                  timeLimit,
	}
}

func (s *WaitForAgentToConnectStep) Name() model.OperationStage {
	return model.WaitForAgentToConnect
}

func (s *WaitForAgentToConnectStep) TimeLimit() time.Duration {
	return s.timeLimit
}

func (s *WaitForAgentToConnectStep) Run(cluster model.Cluster, _ model.Operation, logger logrus.FieldLogger) (operations.StageResult, error) {

	if cluster.Kubeconfig == nil {
		return operations.StageResult{}, fmt.Errorf("error: kubeconfig is nil")
	}

	k8sConfig, err := k8s.ParseToK8sConfig([]byte(*cluster.Kubeconfig))
	if err != nil {
		return operations.StageResult{}, util.K8SErrorToAppError(err)
	}

	compassConnClient, err := s.newCompassConnectionClient(k8sConfig)
	if err != nil {
		return operations.StageResult{}, util.K8SErrorToAppError(err).SetComponent(apperrors.ErrCompassConnectionClient)
	}

	compassConnCR, err := compassConnClient.Get(context.Background(), defaultCompassConnectionName, v1meta.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Infof("Compass Connection not yet found on cluster")
			return operations.StageResult{Stage: s.Name(), Delay: 5 * time.Second}, nil
		}

		return operations.StageResult{}, util.K8SErrorToAppError(errors.Wrap(err, "error getting Compass Connection CR on the Runtime")).SetComponent(apperrors.ErrCompassConnection)
	}

	if compassConnCR.Status.State == v1alpha1.ConnectionFailed {
		logger.Warn("Compass Connection is in Failed state, trying to reconfigure runtime")
		err := s.runtimeConfigurator.ConfigureRuntime(cluster, *cluster.Kubeconfig)
		if err != nil {
			return operations.StageResult{}, err.Append("error: Compass Connection is in Failed state: reconfigure runtime faiure")
		}
		return operations.StageResult{Stage: s.Name(), Delay: 2 * time.Minute}, nil
	}

	if compassConnCR.Status.State != v1alpha1.Synchronized {
		if compassConnCR.Status.State == v1alpha1.SynchronizationFailed {
			logger.Warnf("Runtime Agent Connected but resource synchronization failed state: %s", compassConnCR.Status.State)
			return s.setConnectedRuntimeStatusCondition(cluster, logger), nil
		}
		if compassConnCR.Status.State == v1alpha1.MetadataUpdateFailed {
			logger.Warnf("Runtime Agent Connected but metadata update failed: %s", compassConnCR.Status.State)
			return s.setConnectedRuntimeStatusCondition(cluster, logger), nil
		}

		logger.Infof("Compass Connection not yet in Synchronized state, current state: %s", compassConnCR.Status.State)
		return operations.StageResult{Stage: s.Name(), Delay: 2 * time.Second}, nil
	}

	return s.setConnectedRuntimeStatusCondition(cluster, logger), nil
}

func (s *WaitForAgentToConnectStep) setConnectedRuntimeStatusCondition(cluster model.Cluster, logger logrus.FieldLogger) operations.StageResult {
	err := util.RetryOnError(5*time.Second, 3, "Error while setting runtime status condition in Director: %s", func() (err apperrors.AppError) {
		err = s.directorClient.SetRuntimeStatusCondition(cluster.ID, graphql.RuntimeStatusConditionConnected, cluster.Tenant)
		return
	})
	if err != nil {
		logger.Errorf("Failed to set runtime %s status condition: %s", graphql.RuntimeStatusConditionConnected.String(), err.Error())
		return operations.StageResult{Stage: s.Name(), Delay: 2 * time.Second}
	}
	return operations.StageResult{Stage: s.nextStep, Delay: 0}
}
