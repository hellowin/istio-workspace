package k8s

import (
	"encoding/json"

	"emperror.dev/errors"
	appsv1 "k8s.io/api/apps/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maistra/istio-workspace/pkg/model"
	"github.com/maistra/istio-workspace/pkg/reference"
	"github.com/maistra/istio-workspace/pkg/template"
)

const (
	// DeploymentKind is the k8s Kind for a Deployment.
	DeploymentKind = "Deployment"
)

var _ model.Locator = DeploymentLocator
var _ model.Revertor = DeploymentRevertor
var _ model.Manipulator = deploymentManipulator{}

// DeploymentManipulator represents a model.Manipulator implementation for handling Deployment objects.
func DeploymentManipulator(engine template.Engine) model.Manipulator {
	return deploymentManipulator{engine: engine}
}

type deploymentManipulator struct {
	engine template.Engine
}

func (d deploymentManipulator) TargetResourceType() client.Object {
	return &appsv1.Deployment{}
}
func (d deploymentManipulator) Mutate() model.Mutator {
	return DeploymentMutator(d.engine)
}
func (d deploymentManipulator) Revert() model.Revertor {
	return DeploymentRevertor
}

// DeploymentLocator attempts to locate a Deployment kind based on Ref name.
func DeploymentLocator(ctx model.SessionContext, ref *model.Ref) bool {
	if !ref.KindName.SupportsKind(DeploymentKind) {
		return false
	}
	deployment, err := getDeployment(ctx, ctx.Namespace, ref.KindName.Name)
	if err != nil {
		if k8sErrors.IsNotFound(err) { // Ref is not a Deployment type
			return false
		}
		ctx.Log.Error(err, "Could not get Deployment", "name", deployment.Name)

		return false
	}
	ref.AddTargetResource(model.NewLocatedResource(DeploymentKind, deployment.Name, deployment.Spec.Template.Labels))

	return true
}

// DeploymentMutator attempts to clone the located Deployment.
func DeploymentMutator(engine template.Engine) model.Mutator {
	return func(ctx model.SessionContext, ref *model.Ref) error {
		targets := ref.GetTargets(model.Kind(DeploymentKind))
		if len(targets) == 0 {
			return nil
		}
		target := targets[0]

		deployment, err := getDeployment(ctx, ctx.Namespace, target.Name)
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return nil
			}

			return err
		}
		ctx.Log.Info("Found Deployment", "name", deployment.Name)

		if ref.Strategy == model.StrategyExisting {
			return nil
		}

		deploymentClone, err := cloneDeployment(engine, deployment.DeepCopy(), ref, ref.GetNewVersion(ctx.Name))
		if err != nil {
			ctx.Log.Info("Failed to clone Deployment", "name", deployment.Name)

			return err
		}
		if err = reference.Add(ctx.ToNamespacedName(), deploymentClone); err != nil {
			ctx.Log.Error(err, "failed to add relation reference", "kind", deploymentClone.Kind, "name", deploymentClone.Name)
		}
		if _, err = getDeployment(ctx, deploymentClone.Namespace, deploymentClone.Name); err == nil {
			return nil
		}

		err = ctx.Client.Create(ctx, deploymentClone)
		if err != nil {
			ctx.Log.Info("Failed to create cloned Deployment", "name", deploymentClone.Name)
			ref.AddResourceStatus(model.NewFailedResource(DeploymentKind, deploymentClone.Name, model.ActionCreated, err.Error()))

			return errors.WrapWithDetails(err, "failed to create cloned Deployment", "kind", DeploymentKind, "name", deploymentClone.Name)
		}
		ctx.Log.Info("Cloned Deployment", "name", deploymentClone.Name)
		ref.AddResourceStatus(model.NewSuccessResource(DeploymentKind, deploymentClone.Name, model.ActionCreated))

		return nil
	}
}

// DeploymentRevertor attempts to delete the cloned Deployment.
func DeploymentRevertor(ctx model.SessionContext, ref *model.Ref) error {
	statuses := ref.GetResources(model.Kind(DeploymentKind))
	for _, status := range statuses {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: status.Name, Namespace: ctx.Namespace},
		}
		ctx.Log.Info("Found Deployment", "name", status.Name)
		err := ctx.Client.Delete(ctx, deployment)
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return nil
			}
			ctx.Log.Info("Failed to delete Deployment", "name", status.Name)
			ref.AddResourceStatus(model.NewFailedResource(DeploymentKind, status.Name, status.Action, err.Error()))

			return errors.WrapWithDetails(err, "failed to delete Deployment", "kind", DeploymentKind, "name", status.Name)
		}
		ref.RemoveResourceStatus(model.NewSuccessResource(DeploymentKind, status.Name, status.Action))
	}

	return nil
}

func cloneDeployment(engine template.Engine, deployment *appsv1.Deployment, ref *model.Ref, version string) (*appsv1.Deployment, error) {
	originalDeployment, err := json.Marshal(deployment)
	if err != nil {
		return nil, errors.Wrap(err, "failed reading deployment json")
	}

	modifiedDeployment, err := engine.Run(ref.Strategy, originalDeployment, version, ref.Args)
	if err != nil {
		return nil, errors.Wrap(err, "failed to modify deployment")
	}

	clone := appsv1.Deployment{}
	err = json.Unmarshal(modifiedDeployment, &clone)
	if err != nil {
		return nil, errors.Wrap(err, "failed unmarshalling json of modified deployment")
	}

	return &clone, nil
}

func getDeployment(ctx model.SessionContext, namespace, name string) (*appsv1.Deployment, error) {
	deployment := appsv1.Deployment{}
	err := ctx.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &deployment)

	return &deployment, errors.WrapWithDetails(err, "failed finding deployment in namespace ", "kind", DeploymentKind, "name", name, "namespace", namespace)
}
