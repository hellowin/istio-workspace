package istio

import (
	"strings"

	"emperror.dev/errors"
	istionetwork "istio.io/client-go/pkg/apis/networking/v1alpha3"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maistra/istio-workspace/pkg/model"
	"github.com/maistra/istio-workspace/pkg/reference"
)

const (
	// GatewayKind is the k8s Kind for a istio Gateway.
	GatewayKind = "Gateway"
)

var _ model.Mutator = GatewayMutator
var _ model.Revertor = GatewayRevertor
var _ model.Manipulator = gatewayManipulator{}

// GatewayManipulator represents a model.Manipulator implementation for handling Gateway objects.
func GatewayManipulator() model.Manipulator {
	return gatewayManipulator{}
}

type gatewayManipulator struct {
}

func (d gatewayManipulator) TargetResourceType() client.Object {
	return &istionetwork.Gateway{}
}
func (d gatewayManipulator) Mutate() model.Mutator {
	return GatewayMutator
}
func (d gatewayManipulator) Revert() model.Revertor {
	return GatewayRevertor
}

// GatewayMutator attempts to expose a external host on the gateway.
func GatewayMutator(ctx model.SessionContext, ref *model.Ref) error {
	var errs []error
	for _, gwName := range ref.GetTargets(model.Kind(GatewayKind)) {
		gw, err := getGateway(ctx, ctx.Namespace, gwName.Name)
		if err != nil {
			ref.AddResourceStatus(model.NewFailedResource(GatewayKind, gw.Name, model.ActionLocated, err.Error()))
			errs = append(errs, err)

			continue
		}

		ctx.Log.Info("Found Gateway", "name", gw.Name)
		mutatedGw, addedHosts := mutateGateway(ctx, *gw)

		if err = reference.Add(ctx.ToNamespacedName(), &mutatedGw); err != nil {
			ctx.Log.Error(err, "failed to add relation reference", "kind", mutatedGw.Kind, "name", mutatedGw.Name)
		}
		err = ctx.Client.Update(ctx, &mutatedGw)
		if err != nil {
			ref.AddResourceStatus(model.NewFailedResource(GatewayKind, mutatedGw.Name, model.ActionModified, err.Error()))
			errs = append(errs, errors.WrapIfWithDetails(err, "failed updateing gateway", "kind", GatewayKind, "name", mutatedGw.Name))

			continue
		}

		ref.AddResourceStatus(model.ResourceStatus{
			Kind:    GatewayKind,
			Name:    mutatedGw.Name,
			Action:  model.ActionModified,
			Success: true,
			Prop: map[string]string{
				"hosts": strings.Join(addedHosts, ","),
			}})
	}

	return errors.WrapIfWithDetails(
		errors.Combine(errs...),
		"failed to manipulate gateway for session", "session", ctx.Name, "namespace", ctx.Namespace, "ref", ref.KindName.Name)
}

// GatewayRevertor looks at the Ref.ResourceStatus and attempts to revert the state of the mutated objects.
func GatewayRevertor(ctx model.SessionContext, ref *model.Ref) error {
	var errs []error
	resources := ref.GetResources(model.Kind(GatewayKind))

	for _, resource := range resources {
		gw, err := getGateway(ctx, ctx.Namespace, resource.Name)
		if err != nil {
			if k8sErrors.IsNotFound(err) { // Not found, nothing to clean
				continue
			}
			ref.AddResourceStatus(model.NewFailedResource(GatewayKind, resource.Name, resource.Action, err.Error()))
			errs = append(errs, err)

			continue
		}

		ctx.Log.Info("Found Gateway", "name", resource.Name)
		mutatedGw := revertGateway(ctx, *gw)
		if err = reference.Remove(ctx.ToNamespacedName(), &mutatedGw); err != nil {
			ctx.Log.Error(err, "failed to remove relation reference", "kind", mutatedGw.Kind, "name", mutatedGw.Name)
		}
		err = ctx.Client.Update(ctx, &mutatedGw)
		if err != nil {
			ref.AddResourceStatus(model.NewFailedResource(GatewayKind, resource.Name, resource.Action, err.Error()))
			errs = append(errs, errors.WrapIfWithDetails(err, "failed updateing gateway", "kind", GatewayKind, "name", mutatedGw.Name))

			continue
		}
		// ok, removed
		ref.RemoveResourceStatus(model.NewSuccessResource(GatewayKind, resource.Name, resource.Action))
	}

	return errors.WrapIfWithDetails(
		errors.Combine(errs...),
		"failed to reverting gateway for session", "session", ctx.Name, "namespace", ctx.Namespace, "ref", ref.KindName.Name)
}

func mutateGateway(ctx model.SessionContext, source istionetwork.Gateway) (mutatedGw istionetwork.Gateway, addedHosts []string) {
	if source.Annotations == nil {
		source.Annotations = map[string]string{}
	}
	addedHosts = []string{}
	var existingHosts []string
	if hosts := source.Annotations[LabelIkeHosts]; hosts != "" {
		existingHosts = strings.Split(hosts, ",") // split on empty string return empty (len(1))
	}
	for _, server := range source.Spec.Servers {
		hosts := server.Hosts
		for _, host := range hosts {
			newHost := ctx.Name + "." + host
			if !existInList(existingHosts, host) && !existInList(existingHosts, newHost) {
				existingHosts = append(existingHosts, newHost)
				hosts = append(hosts, newHost)
			}
			if existInList(existingHosts, newHost) {
				addedHosts = append(addedHosts, newHost)
			}
		}
		for _, existing := range existingHosts {
			baseHost := strings.Join(strings.Split(existing, ".")[1:], ".")
			if !existInList(hosts, existing) && existInList(hosts, baseHost) {
				hosts = append(hosts, existing)
			}
		}
		server.Hosts = hosts
	}
	source.Annotations[LabelIkeHosts] = strings.Join(existingHosts, ",")

	return source, addedHosts
}

func revertGateway(ctx model.SessionContext, source istionetwork.Gateway) istionetwork.Gateway {
	if source.Annotations == nil {
		source.Annotations = map[string]string{}
	}
	var existingHosts []string
	if hosts := source.Annotations[LabelIkeHosts]; hosts != "" {
		existingHosts = strings.Split(hosts, ",") // split on empty string return empty (len(1))
	}
	var toBeRemovedHosts []string
	for _, server := range source.Spec.Servers {
		hosts := server.Hosts
		for i := 0; i < len(hosts); i++ {
			host := hosts[i]
			if existInList(existingHosts, host) && strings.HasPrefix(host, ctx.Name+".") {
				toBeRemovedHosts = append(toBeRemovedHosts, host)
				hosts = append(hosts[:i], hosts[i+1:]...)
				i--
			}
		}
		server.Hosts = hosts
	}
	for _, toBeRemoved := range toBeRemovedHosts {
		existingHosts = removeFromList(existingHosts, toBeRemoved)
	}
	if len(existingHosts) == 0 {
		delete(source.Annotations, LabelIkeHosts)
	} else {
		source.Annotations[LabelIkeHosts] = strings.Join(existingHosts, ",")
	}

	return source
}

func getGateway(ctx model.SessionContext, namespace, name string) (*istionetwork.Gateway, error) {
	Gateway := istionetwork.Gateway{}
	err := ctx.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &Gateway)

	return &Gateway, errors.WrapWithDetails(err, "failed finding gateway in namespace", "name", name, "namespace", namespace)
}

func existInList(hosts []string, host string) bool {
	for _, eh := range hosts {
		if eh == host {
			return true
		}
	}

	return false
}

func removeFromList(hosts []string, host string) []string {
	for i, eh := range hosts {
		if eh == host {
			hosts = append(hosts[:i], hosts[i+1:]...)

			return hosts
		}
	}

	return hosts
}
